package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"bakimplan/internal/auth"
	"bakimplan/internal/middleware"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AuthHandler struct {
	Pool *pgxpool.Pool
}

func NewAuthHandler(pool *pgxpool.Pool) *AuthHandler {
	return &AuthHandler{Pool: pool}
}

// SeedDefaultUsers: Sistem kalkarken 5 rol için zorunlu demo kullanıcılarını garanti altına alır.
// Tüm demo hesapların şifresi: Password123!
func SeedDefaultUsers(ctx context.Context, pool *pgxpool.Pool) error {
	hash, err := auth.HashPassword("Password123!")
	if err != nil {
		return err
	}

	demoUsers := []struct {
		Email    string
		FullName string
		Role     string
	}{
		{"teknisyen@bakimplan.local", "Enis Çeker (Teknisyen)", "teknisyen"},
		{"planlama@bakimplan.local", "İnan Mahmud (Bakım Planlama)", "planlama"},
		{"uretim@bakimplan.local", "Mehmet Demir (Üretim Sorumlusu)", "uretim"},
		{"depo@bakimplan.local", "Erdinç Arslan (Depo Sorumlusu)", "depo"},
		{"yonetici@bakimplan.local", "Yağız Toprak (Yönetici)", "yonetici"},
	}

	for _, u := range demoUsers {
		_, err := pool.Exec(ctx,
			`INSERT INTO users (email, password_hash, full_name, role)
			 VALUES ($1, $2, $3, $4)
			 ON CONFLICT (email) DO NOTHING`,
			u.Email, hash, u.FullName, u.Role,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON gövdesi.")
		return
	}

	if req.Email == "" || req.Password == "" {
		middleware.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "E-posta ve parola alanları zorunludur.")
		return
	}

	var id int
	var email, hash, fullName, role string
	err := h.Pool.QueryRow(r.Context(),
		`SELECT id, email, password_hash, full_name, role FROM users WHERE email = $1`,
		req.Email,
	).Scan(&id, &email, &hash, &fullName, &role)

	if err != nil || !auth.CheckPassword(req.Password, hash) {
		middleware.WriteError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "E-posta veya parola hatalı.")
		return
	}

	token, err := auth.GenerateToken(id, email, fullName, role)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "TOKEN_ERROR", "Token üretilemedi.")
		return
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"user": map[string]any{
			"id":        id,
			"email":     email,
			"full_name": fullName,
			"role":      role,
		},
	})
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.GetUserFromContext(r.Context())
	if !ok {
		middleware.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Oturum bulunamadı.")
		return
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"id":        claims.UserID,
		"email":     claims.Email,
		"full_name": claims.FullName,
		"role":      claims.Role,
	})
}

func (h *AuthHandler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	limit, cursor, err := parsePageParams(r, defaultPageLimit, maxPageLimit)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_CURSOR", err.Error())
		return
	}

	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, user_id, action, entity_type, entity_id, old_data, new_data, created_at
		 FROM audit_logs
		 WHERE ($1::bigint IS NULL OR id < $1)
		 ORDER BY id DESC
		 LIMIT $2`,
		cursor, limit+1)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	var logs []map[string]any
	for rows.Next() {
		var id int64
		var userID *int
		var action, entityType, entityID string
		var oldData, newData any
		var createdAt any
		if err := rows.Scan(&id, &userID, &action, &entityType, &entityID, &oldData, &newData, &createdAt); err == nil {
			logs = append(logs, map[string]any{
				"id":          id,
				"user_id":     userID,
				"action":      action,
				"entity_type": entityType,
				"entity_id":   entityID,
				"old_data":    oldData,
				"new_data":    newData,
				"created_at":  createdAt,
			})
		}
	}

	logs, next, hasMore := trimPage(logs, limit, func(m map[string]any) int64 { return m["id"].(int64) })
	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"items":       logs,
		"next_cursor": next,
		"has_more":    hasMore,
	})
}
