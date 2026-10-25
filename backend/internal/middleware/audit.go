package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func RecordAudit(ctx context.Context, pool *pgxpool.Pool, userID *int, action, entityType, entityID string, oldData, newData any) {
	var oldJSON, newJSON []byte
	if oldData != nil {
		oldJSON, _ = json.Marshal(oldData)
	}
	if newData != nil {
		newJSON, _ = json.Marshal(newData)
	}

	_, _ = pool.Exec(ctx,
		`INSERT INTO audit_logs (user_id, action, entity_type, entity_id, old_data, new_data)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, action, entityType, entityID, oldJSON, newJSON,
	)
}

func RecordAuditTx(ctx context.Context, tx pgx.Tx, userID *int, action, entityType, entityID string, oldData, newData any) error {
	var oldJSON, newJSON []byte
	if oldData != nil {
		oldJSON, _ = json.Marshal(oldData)
	}
	if newData != nil {
		newJSON, _ = json.Marshal(newData)
	}

	_, err := tx.Exec(ctx,
		`INSERT INTO audit_logs (user_id, action, entity_type, entity_id, old_data, new_data)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, action, entityType, entityID, oldJSON, newJSON,
	)
	return err
}

type auditStatusRecorder struct {
	http.ResponseWriter
	statusCode int
	wrote      bool
}

func (s *auditStatusRecorder) WriteHeader(code int) {
	if s.wrote {
		return
	}
	s.wrote = true
	s.statusCode = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *auditStatusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(b)
}

// AuditTrail, kimliği doğrulanmış her değiştirici isteği (POST/PATCH/PUT/DELETE)
// başarıyla sonuçlandığında audit_logs'a yazar. Handler'ların yazdığı ayrıntılı
// kayıtlara ek olarak "kim, ne zaman, hangi uç" izini garanti eder.
//
// auth.Authenticate middleware'inden SONRA bağlanmalıdır; aksi hâlde userID
// her zaman nil olur.
func AuditTrail(pool *pgxpool.Pool, userID UserIDFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
			default:
				next.ServeHTTP(w, r)
				return
			}

			rec := &auditStatusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rec, r)

			// Yalnızca gerçekten bir kayıt oluştuysa iz bırak.
			if rec.statusCode < 200 || rec.statusCode >= 300 {
				return
			}

			var uid *int
			if userID != nil {
				uid = userID(r)
			}

			entityType := strings.TrimPrefix(r.URL.Path, "/api/v1/")
			if i := strings.IndexByte(entityType, '/'); i >= 0 {
				entityType = entityType[:i]
			}
			action := strings.ToLower(r.Method)
			if pattern := chi.RouteContext(r.Context()).RoutePattern(); pattern != "" {
				action = strings.ToLower(r.Method) + " " + pattern
			}

			RecordAudit(r.Context(), pool, uid, action, entityType, chi.URLParam(r, "id"),
				nil,
				map[string]any{
					"path":   r.URL.Path,
					"status": rec.statusCode,
				},
			)
		})
	}
}
