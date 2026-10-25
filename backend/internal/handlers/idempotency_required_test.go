package handlers_test

// Idempotency-Key zorunluluğunun doğrulanması (Faz 2 / Ortak Kural 2).
//
// Spec: "Kritik POST uçlarında Idempotency-Key header kontrolü".
// Kanıtlanan davranış:
//  1. Kritik uçta anahtar yoksa 400 IDEMPOTENCY_KEY_REQUIRED
//  2. Anahtar varsa istek normal işlenir (201)
//  3. Zorunluluk okuma uçlarını etkilemez (GET 200)
//  4. Kapsam tek uçla sınırlı değil: complete/status/assign/failure/stock/trigger-due

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"bakimplan/internal/auth"
	"bakimplan/internal/database"
	"bakimplan/internal/handlers"
	appmw "bakimplan/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// setupIdemRouter kendi router'ını kurar; diğer test dosyalarından bağımsızdır.
func setupIdemRouter(t *testing.T) (*pgxpool.Pool, http.Handler) {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://cmms_user:cmms_secret_pass@localhost:5432/bakimplan_db?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := database.ConnectAndMigrate(ctx, dbURL)
	if err != nil {
		t.Skipf("PostgreSQL erişilemedi, entegrasyon testi atlandı (%v)", err)
	}
	if err := handlers.SeedDefaultUsers(ctx, pool); err != nil {
		t.Fatalf("varsayılan kullanıcılar oluşturulamadı: %v", err)
	}

	extractUserID := func(r *http.Request) *int {
		if claims, ok := auth.GetUserFromContext(r.Context()); ok && claims != nil {
			id := claims.UserID
			return &id
		}
		return nil
	}

	authH := handlers.NewAuthHandler(pool)
	eqH := handlers.NewEquipmentHandler(pool)
	invH := handlers.NewInventoryHandler(pool)
	maintH := handlers.NewMaintenanceHandler(pool)
	lifeH := handlers.NewWorkOrderLifecycleHandler(pool)

	r := chi.NewRouter()
	r.Route("/api/v1", func(api chi.Router) {
		api.Post("/auth/login", authH.Login)
		api.Group(func(p chi.Router) {
			p.Use(auth.Authenticate)
			// Sıra kritik: kimlik -> anahtar zorunluluğu -> tekrar oynatma koruması
			p.Use(appmw.RequireIdempotencyKey())
			p.Use(appmw.Idempotency(pool, extractUserID))
			p.Use(appmw.AuditTrail(pool, extractUserID))

			p.Get("/equipments/tree", eqH.GetTree)
			p.Get("/work-orders", maintH.ListWorkOrders)
			p.Post("/work-orders", maintH.CreateWorkOrder)
			p.Post("/work-orders/{id}/complete", maintH.CompleteWorkOrder)
			p.Patch("/work-orders/{id}/status", lifeH.UpdateStatus)
			p.Post("/work-orders/{id}/assign", lifeH.AssignTechnician)
			p.Delete("/work-orders/{id}/assign", lifeH.Unassign)
			p.Get("/work-orders/{id}/events", lifeH.ListEvents)
			p.Post("/failure-reports", maintH.CreateFailureReport)
			p.Post("/preventive-plans/trigger-due", maintH.TriggerDueTimePMs)
			p.Get("/parts", invH.ListParts)
			p.Post("/stock-movements", invH.CreateStockMovement)
		})
	})

	t.Cleanup(func() { pool.Close() })
	return pool, r
}

// doIdemReq anahtar EKLEMEDEN istek atar (zorunluluk testi için).
func doIdemReq(r http.Handler, method, path, token string, body any) (*httptest.ResponseRecorder, map[string]any) {
	return doIdemReqWithKey(r, method, path, token, body, "")
}

func doIdemReqWithKey(r http.Handler, method, path, token string, body any, key string) (*httptest.ResponseRecorder, map[string]any) {
	var buf []byte
	if body != nil {
		buf, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	out := map[string]any{}
	_ = json.NewDecoder(rec.Body).Decode(&out)
	return rec, out
}

// tokenForRole, verilen roldeki ilk kullanıcı için JWT üretir.
func tokenForRole(t *testing.T, pool *pgxpool.Pool, role string) (int, string) {
	t.Helper()
	var id int
	var fullName string
	if err := pool.QueryRow(context.Background(),
		`SELECT id, full_name FROM users WHERE role = $1 ORDER BY id ASC LIMIT 1`, role,
	).Scan(&id, &fullName); err != nil {
		t.Fatalf("'%s' rolünde kullanıcı yok: %v", role, err)
	}
	tok, err := auth.GenerateToken(id, role+"@bakimplan.local", fullName, role)
	if err != nil {
		t.Fatalf("token üretilemedi: %v", err)
	}
	return id, tok
}

// insertTestEquipment, testin kendi ekipmanını üretir (veriden bağımsız).
func insertTestEquipment(t *testing.T, pool *pgxpool.Pool, prefix string) int {
	t.Helper()
	var id int
	err := pool.QueryRow(context.Background(),
		`INSERT INTO equipments (code, name, level_type) VALUES ($1, 'Idempotency Test Makinesi', 'makine') RETURNING id`,
		fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()),
	).Scan(&id)
	if err != nil {
		t.Fatalf("ekipman oluşturulamadı: %v", err)
	}
	return id
}

// TestIdemKey01_MissingKeyIsRejected — kritik POST anahtarsız reddedilmeli.
func TestIdemKey01_MissingKeyIsRejected(t *testing.T) {
	pool, router := setupIdemRouter(t)
	_, adminTok := tokenForRole(t, pool, "yonetici")
	eqID := insertTestEquipment(t, pool, "EQ-IDEM")

	rec, out := doIdemReq(router, http.MethodPost, "/api/v1/work-orders", adminTok, map[string]any{
		"equipment_id": eqID,
		"title":        "anahtarsız deneme",
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("anahtarsız kritik POST 400 dönmeli, gelen %d %v", rec.Code, out)
	}
	errBody, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("standart hata zarfı bekleniyordu, gelen: %v", out)
	}
	if errBody["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("hata kodu IDEMPOTENCY_KEY_REQUIRED olmalı, gelen: %v", errBody["code"])
	}
}

// TestIdemKey02_WithKeyIsAccepted — anahtar varsa akış bozulmamalı.
func TestIdemKey02_WithKeyIsAccepted(t *testing.T) {
	pool, router := setupIdemRouter(t)
	_, adminTok := tokenForRole(t, pool, "yonetici")
	eqID := insertTestEquipment(t, pool, "EQ-IDEM-OK")

	rec, out := doIdemReqWithKey(router, http.MethodPost, "/api/v1/work-orders", adminTok, map[string]any{
		"equipment_id": eqID,
		"title":        "anahtarlı istek",
	}, fmt.Sprintf("idem-ok-%d", time.Now().UnixNano()))

	if rec.Code != http.StatusCreated {
		t.Fatalf("anahtarlı istek 201 dönmeli, gelen %d %v", rec.Code, out)
	}
	if out["id"] == nil {
		t.Fatalf("yanıtta id olmalı: %v", out)
	}
}

// TestIdemKey03_ReadEndpointsUnaffected — GET uçları anahtar istemez.
func TestIdemKey03_ReadEndpointsUnaffected(t *testing.T) {
	pool, router := setupIdemRouter(t)
	_, adminTok := tokenForRole(t, pool, "yonetici")

	for _, path := range []string{"/api/v1/work-orders", "/api/v1/equipments/tree", "/api/v1/parts"} {
		rec, out := doIdemReq(router, http.MethodGet, path, adminTok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s anahtarsız 200 dönmeli, gelen %d %v", path, rec.Code, out)
		}
	}
}

// TestIdemKey04_ScopeCoversAllCriticalRoutes — zorunluluk tek uçla sınırlı değil.
func TestIdemKey04_ScopeCoversAllCriticalRoutes(t *testing.T) {
	pool, router := setupIdemRouter(t)
	_, adminTok := tokenForRole(t, pool, "yonetici")
	teknisyenID, _ := tokenForRole(t, pool, "teknisyen")
	eqID := insertTestEquipment(t, pool, "EQ-IDEM-SCOPE")

	rec, out := doIdemReqWithKey(router, http.MethodPost, "/api/v1/work-orders", adminTok, map[string]any{
		"equipment_id": eqID,
		"title":        "kapsam testi",
	}, fmt.Sprintf("idem-scope-%d", time.Now().UnixNano()))
	if rec.Code != http.StatusCreated {
		t.Fatalf("hazırlık iş emri açılamadı: %d %v", rec.Code, out)
	}
	woID := int64(out["id"].(float64))

	cases := []struct {
		method string
		path   string
		body   map[string]any
	}{
		{http.MethodPost, fmt.Sprintf("/api/v1/work-orders/%d/complete", woID), map[string]any{}},
		{http.MethodPatch, fmt.Sprintf("/api/v1/work-orders/%d/status", woID), map[string]any{"status": "iptal"}},
		{http.MethodPost, fmt.Sprintf("/api/v1/work-orders/%d/assign", woID), map[string]any{"technician_id": teknisyenID}},
		{http.MethodPost, "/api/v1/failure-reports", map[string]any{"equipment_id": eqID, "title": "x", "severity": "orta"}},
		{http.MethodPost, "/api/v1/stock-movements", map[string]any{"part_id": 1, "warehouse_id": 1, "quantity_change": 1}},
		{http.MethodPost, "/api/v1/preventive-plans/trigger-due", map[string]any{}},
	}

	for _, c := range cases {
		got, body := doIdemReq(router, c.method, c.path, adminTok, c.body)
		if got.Code != http.StatusBadRequest {
			t.Fatalf("%s %s anahtarsız 400 dönmeli, gelen %d %v", c.method, c.path, got.Code, body)
		}
		errBody, ok := body["error"].(map[string]any)
		if !ok || errBody["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
			t.Fatalf("%s %s hata kodu IDEMPOTENCY_KEY_REQUIRED olmalı, gelen %v", c.method, c.path, body)
		}
	}
}
