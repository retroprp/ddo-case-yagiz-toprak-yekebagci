package handlers_test

// F-3 (iş emri yaşam döngüsü) ve F-6 (teknisyen atama) uçlarının
// gerçek PostgreSQL üzerinde doğrulanması.
//
// Kendi router'ını kurar; böylece cmms_test.go'daki mevcut kurulum
// değiştirilmeden yeni uçlar test edilebilir.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"bakimplan/internal/auth"
	"bakimplan/internal/database"
	"bakimplan/internal/handlers"
	appmw "bakimplan/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupLifecycleRouter(t *testing.T) (*pgxpool.Pool, http.Handler) {
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
			p.Use(appmw.RequireIdempotencyKey())
			p.Use(appmw.Idempotency(pool, extractUserID))
			p.Use(appmw.AuditTrail(pool, extractUserID))

			p.Get("/auth/me", authH.Me)
			p.Get("/equipments/tree", eqH.GetTree)
			p.Post("/equipments", eqH.Create)

			p.Get("/work-orders", maintH.ListWorkOrders)
			p.Post("/work-orders", maintH.CreateWorkOrder)
			p.With(auth.RequireRoles("teknisyen", "planlama", "yonetici")).
				Post("/work-orders/{id}/complete", maintH.CompleteWorkOrder)
			p.With(auth.RequireRoles("teknisyen", "planlama", "yonetici")).
				Patch("/work-orders/{id}/status", lifeH.UpdateStatus)
			p.With(auth.RequireRoles("planlama", "yonetici")).
				Post("/work-orders/{id}/assign", lifeH.AssignTechnician)
			p.With(auth.RequireRoles("planlama", "yonetici")).
				Delete("/work-orders/{id}/assign", lifeH.Unassign)
			p.Get("/work-orders/{id}/events", lifeH.ListEvents)

			p.Get("/parts", invH.ListParts)
			p.Get("/warehouses", invH.ListWarehouses)
			p.Post("/work-orders/{id}/reserve-part", invH.ReservePartForWorkOrder)
			p.Get("/technicians/workload", maintH.GetTechnicianWorkload)
			p.Get("/audit-logs", authH.ListAuditLogs)
		})
	})

	t.Cleanup(func() { pool.Close() })
	return pool, r
}

func doLifecycleReq(r http.Handler, method, path, token string, body any) (*httptest.ResponseRecorder, map[string]any) {
	var buf []byte
	if body != nil {
		buf, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)

	}
	// Kritik uçlar Idempotency-Key olmadan 400 IDEMPOTENCY_KEY_REQUIRED döner.
	// Sayaç, eşzamanlı goroutine'lerde anahtar çakışmasını önler
	// (çakışırsa ikinci istek idempotent "replay" olur ve sayaç testleri yanılır).
	if method != http.MethodGet && req.Header.Get("Idempotency-Key") == "" {
		req.Header.Set("Idempotency-Key",
			fmt.Sprintf("lc-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&lcIdemCounter, 1)))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	out := map[string]any{}
	_ = json.NewDecoder(rec.Body).Decode(&out)
	return rec, out
}

// lcIdemCounter, lifecycle testlerinde anahtar üretimi için atomik sayaç.
var lcIdemCounter uint64

func userIDByRole(t *testing.T, pool *pgxpool.Pool, role string) (int, string) {
	t.Helper()
	var id int
	var email string
	err := pool.QueryRow(context.Background(),
		`SELECT id, email FROM users WHERE role = $1 ORDER BY id ASC LIMIT 1`, role,
	).Scan(&id, &email)
	if err != nil {
		t.Fatalf("'%s' rolünde kullanıcı bulunamadı: %v", role, err)
	}
	return id, email
}

func newWorkOrder(t *testing.T, pool *pgxpool.Pool, router http.Handler, token string) (int64, int) {
	t.Helper()
	ctx := context.Background()

	var eqID int
	code := fmt.Sprintf("EQ-LC-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx,
		`INSERT INTO equipments (code, name, level_type) VALUES ($1, 'Lifecycle Test Makinesi', 'makine') RETURNING id`,
		code,
	).Scan(&eqID); err != nil {
		t.Fatalf("ekipman oluşturulamadı: %v", err)
	}

	rec, out := doLifecycleReq(router, "POST", "/api/v1/work-orders", token, map[string]any{
		"equipment_id": eqID,
		"title":        "Yaşam döngüsü testi",
		"wo_type":      "corrective",
		"priority":     "normal",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("iş emri açılamadı: %d %v", rec.Code, out)
	}

	woID := int64(out["id"].(float64))
	return woID, eqID
}

// TestLifecycle01: acik -> atandi -> devam_ediyor -> tamamlandi, geçmiş 3 kayıt.
func TestLifecycle01_AssignStartCompleteFlow(t *testing.T) {
	pool, router := setupLifecycleRouter(t)

	yoneticiID, _ := userIDByRole(t, pool, "yonetici")
	teknisyenID, _ := userIDByRole(t, pool, "teknisyen")

	adminTok, _ := auth.GenerateToken(yoneticiID, "yonetici@bakimplan.local", "Yönetici", "yonetici")
	tekTok, _ := auth.GenerateToken(teknisyenID, "teknisyen@bakimplan.local", "Teknisyen", "teknisyen")

	woID, _ := newWorkOrder(t, pool, router, adminTok)

	// 1) Atama: acik -> atandi
	rec, out := doLifecycleReq(router, "POST", fmt.Sprintf("/api/v1/work-orders/%d/assign", woID), adminTok,
		map[string]any{"technician_id": teknisyenID})
	if rec.Code != http.StatusOK {
		t.Fatalf("atama başarısız: %d %v", rec.Code, out)
	}
	if out["status"] != "atandi" {
		t.Fatalf("atama sonrası durum 'atandi' olmalı, gelen: %v", out["status"])
	}

	// 2) Başlatma: atandi -> devam_ediyor
	rec, out = doLifecycleReq(router, "PATCH", fmt.Sprintf("/api/v1/work-orders/%d/status", woID), tekTok,
		map[string]any{"status": "devam_ediyor", "note": "vardiya başında başlandı"})
	if rec.Code != http.StatusOK {
		t.Fatalf("başlatma başarısız: %d %v", rec.Code, out)
	}
	if out["from_status"] != "atandi" || out["to_status"] != "devam_ediyor" {
		t.Fatalf("beklenen atandi->devam_ediyor, gelen %v->%v", out["from_status"], out["to_status"])
	}

	// 3) Tamamlama (yan etkileri olan ayrı uç)
	rec, out = doLifecycleReq(router, "POST", fmt.Sprintf("/api/v1/work-orders/%d/complete", woID), tekTok,
		map[string]any{"labor_cost_kurus": 125000})
	if rec.Code != http.StatusOK {
		t.Fatalf("tamamlama başarısız: %d %v", rec.Code, out)
	}

	// 4) Geçmiş: en yeni önce olmak üzere 3 geçiş kaydı (atama, başlatma, tamamlama)
	rec, out = doLifecycleReq(router, "GET", fmt.Sprintf("/api/v1/work-orders/%d/events", woID), adminTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("geçmiş okunamadı: %d %v", rec.Code, out)
	}
	items, ok := out["items"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("3 geçmiş kaydı bekleniyordu, gelen: %d", len(items))
	}

	first := items[0].(map[string]any)
	if first["to_status"] != "tamamlandi" {
		t.Fatalf("en yeni kayıt tamamlandi olmalı, gelen: %v", first["to_status"])
	}
	if first["actor_name"] == nil || first["actor_name"] == "" {
		t.Fatalf("tamamlama kaydında aktör adı boş olmamalı")
	}
	last := items[2].(map[string]any)
	if last["from_status"] != "acik" || last["to_status"] != "atandi" {
		t.Fatalf("en eski kayıt acik->atandi olmalı, gelen %v->%v", last["from_status"], last["to_status"])
	}
}

// TestLifecycle02: durum makinesi geçersiz geçişleri reddetmeli.
func TestLifecycle02_InvalidTransitions(t *testing.T) {
	pool, router := setupLifecycleRouter(t)
	yoneticiID, _ := userIDByRole(t, pool, "yonetici")
	adminTok, _ := auth.GenerateToken(yoneticiID, "yonetici@bakimplan.local", "Yönetici", "yonetici")

	woID, _ := newWorkOrder(t, pool, router, adminTok)
	path := fmt.Sprintf("/api/v1/work-orders/%d/status", woID)

	// acik -> devam_ediyor (önce atanmalı)
	rec, out := doLifecycleReq(router, "PATCH", path, adminTok, map[string]any{"status": "devam_ediyor"})
	if rec.Code != http.StatusConflict || out["error"].(map[string]any)["code"] != "INVALID_TRANSITION" {
		t.Fatalf("acik->devam_ediyor 409 INVALID_TRANSITION olmalı, gelen %d %v", rec.Code, out)
	}

	// PATCH ile tamamlamak yasak: /complete kullanılmalı
	rec, out = doLifecycleReq(router, "PATCH", path, adminTok, map[string]any{"status": "tamamlandi"})
	if rec.Code != http.StatusConflict || out["error"].(map[string]any)["code"] != "USE_COMPLETE_ENDPOINT" {
		t.Fatalf("tamamlandi için 409 USE_COMPLETE_ENDPOINT bekleniyordu, gelen %d %v", rec.Code, out)
	}

	// Bilinmeyen durum değeri
	rec, _ = doLifecycleReq(router, "PATCH", path, adminTok, map[string]any{"status": "beklemede"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bilinmeyen durum 400 dönmeli, gelen %d", rec.Code)
	}

	// İptal sonrası hiçbir geçiş olmamalı
	rec, out = doLifecycleReq(router, "PATCH", path, adminTok, map[string]any{"status": "iptal"})
	if rec.Code != http.StatusOK {
		t.Fatalf("iptal başarısız: %d %v", rec.Code, out)
	}
	rec, out = doLifecycleReq(router, "PATCH", path, adminTok, map[string]any{"status": "atandi"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("iptal sonrası geçiş 409 olmalı, gelen %d %v", rec.Code, out)
	}
}

// TestLifecycle03: atama yalnızca teknisyen rolüne ve kapanmamış iş emrine yapılabilir.
func TestLifecycle03_AssignValidation(t *testing.T) {
	pool, router := setupLifecycleRouter(t)

	yoneticiID, _ := userIDByRole(t, pool, "yonetici")
	planlamaID, _ := userIDByRole(t, pool, "planlama")
	teknisyenID, _ := userIDByRole(t, pool, "teknisyen")
	adminTok, _ := auth.GenerateToken(yoneticiID, "yonetici@bakimplan.local", "Yönetici", "yonetici")

	woID, _ := newWorkOrder(t, pool, router, adminTok)
	assignPath := fmt.Sprintf("/api/v1/work-orders/%d/assign", woID)

	// Planlama rolündeki kullanıcı teknisyen değil -> 422
	rec, out := doLifecycleReq(router, "POST", assignPath, adminTok, map[string]any{"technician_id": planlamaID})
	if rec.Code != http.StatusUnprocessableEntity || out["error"].(map[string]any)["code"] != "NOT_A_TECHNICIAN" {
		t.Fatalf("yanlış rol 422 NOT_A_TECHNICIAN olmalı, gelen %d %v", rec.Code, out)
	}

	// Olmayan kullanıcı -> 404
	rec, out = doLifecycleReq(router, "POST", assignPath, adminTok, map[string]any{"technician_id": 99999999})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("olmayan kullanıcı 404 olmalı, gelen %d %v", rec.Code, out)
	}

	// Geçerli atama
	rec, out = doLifecycleReq(router, "POST", assignPath, adminTok, map[string]any{"technician_id": teknisyenID})
	if rec.Code != http.StatusOK {
		t.Fatalf("geçerli atama başarısız: %d %v", rec.Code, out)
	}

	// Atamayı kaldır -> acik
	rec, out = doLifecycleReq(router, "DELETE", assignPath, adminTok, nil)
	if rec.Code != http.StatusOK || out["status"] != "acik" {
		t.Fatalf("atama kaldırma başarısız: %d %v", rec.Code, out)
	}
	var assigned *int
	_ = pool.QueryRow(context.Background(), `SELECT assigned_to FROM work_orders WHERE id = $1`, woID).Scan(&assigned)
	if assigned != nil {
		t.Fatalf("atama kaldırıldıktan sonra assigned_to NULL olmalı")
	}

	// Tekrar kaldırma -> 409 NOT_ASSIGNED
	rec, _ = doLifecycleReq(router, "DELETE", assignPath, adminTok, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("zaten atanmamış iş emrinde 409 bekleniyordu, gelen %d", rec.Code)
	}
}

// TestLifecycle04: iptal edilmiş iş emri tamamlanamaz ve atanamaz.
func TestLifecycle04_CancelledWorkOrderIsTerminal(t *testing.T) {
	pool, router := setupLifecycleRouter(t)
	yoneticiID, _ := userIDByRole(t, pool, "yonetici")
	teknisyenID, _ := userIDByRole(t, pool, "teknisyen")
	adminTok, _ := auth.GenerateToken(yoneticiID, "yonetici@bakimplan.local", "Yönetici", "yonetici")

	woID, _ := newWorkOrder(t, pool, router, adminTok)

	rec, out := doLifecycleReq(router, "PATCH", fmt.Sprintf("/api/v1/work-orders/%d/status", woID), adminTok,
		map[string]any{"status": "iptal", "note": "mükerrer kayıt"})
	if rec.Code != http.StatusOK {
		t.Fatalf("iptal başarısız: %d %v", rec.Code, out)
	}

	rec, out = doLifecycleReq(router, "POST", fmt.Sprintf("/api/v1/work-orders/%d/complete", woID), adminTok, map[string]any{})
	if rec.Code != http.StatusConflict || out["error"].(map[string]any)["code"] != "WO_CANCELLED" {
		t.Fatalf("iptal edilmiş iş emri 409 WO_CANCELLED olmalı, gelen %d %v", rec.Code, out)
	}

	rec, out = doLifecycleReq(router, "POST", fmt.Sprintf("/api/v1/work-orders/%d/assign", woID), adminTok,
		map[string]any{"technician_id": teknisyenID})
	if rec.Code != http.StatusConflict || out["error"].(map[string]any)["code"] != "WO_CLOSED" {
		t.Fatalf("iptal edilmiş iş emrine atama 409 WO_CLOSED olmalı, gelen %d %v", rec.Code, out)
	}
}

// TestLifecycle05: RBAC — atama yalnızca planlama/yönetici, durum geçişi teknisyen de yapabilir.
func TestLifecycle05_RBACOnLifecycleEndpoints(t *testing.T) {
	pool, router := setupLifecycleRouter(t)

	yoneticiID, _ := userIDByRole(t, pool, "yonetici")
	depoID, _ := userIDByRole(t, pool, "depo")
	uretimID, _ := userIDByRole(t, pool, "uretim")
	teknisyenID, _ := userIDByRole(t, pool, "teknisyen")

	adminTok, _ := auth.GenerateToken(yoneticiID, "yonetici@bakimplan.local", "Yönetici", "yonetici")
	depoToken, _ := auth.GenerateToken(depoID, "depo@bakimplan.local", "Depo", "depo")
	uretimToken, _ := auth.GenerateToken(uretimID, "uretim@bakimplan.local", "Üretim", "uretim")

	woID, _ := newWorkOrder(t, pool, router, adminTok)

	// Depo: atama yapamaz
	rec, _ := doLifecycleReq(router, "POST", fmt.Sprintf("/api/v1/work-orders/%d/assign", woID), depoToken,
		map[string]any{"technician_id": teknisyenID})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("depo rolü atama yapamamalı (403), gelen %d", rec.Code)
	}

	// Üretim: durum geçişi yapamaz
	rec, _ = doLifecycleReq(router, "PATCH", fmt.Sprintf("/api/v1/work-orders/%d/status", woID), uretimToken,
		map[string]any{"status": "iptal"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("üretim rolü durum değiştirememeli (403), gelen %d", rec.Code)
	}

	// Teknisyen: atama yapamaz
	tekToken, _ := auth.GenerateToken(teknisyenID, "teknisyen@bakimplan.local", "Teknisyen", "teknisyen")
	rec, _ = doLifecycleReq(router, "POST", fmt.Sprintf("/api/v1/work-orders/%d/assign", woID), tekToken,
		map[string]any{"technician_id": teknisyenID})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("teknisyen atama yapamamalı (403), gelen %d", rec.Code)
	}
}

// TestLifecycle06: iş yükü ucu atanan açık işleri saymalı.
func TestLifecycle06_WorkloadCountsAssignedWork(t *testing.T) {
	pool, router := setupLifecycleRouter(t)
	yoneticiID, _ := userIDByRole(t, pool, "yonetici")
	teknisyenID, _ := userIDByRole(t, pool, "teknisyen")
	adminTok, _ := auth.GenerateToken(yoneticiID, "yonetici@bakimplan.local", "Yönetici", "yonetici")

	woID, _ := newWorkOrder(t, pool, router, adminTok)

	rec, _ := doLifecycleReq(router, "POST", fmt.Sprintf("/api/v1/work-orders/%d/assign", woID), adminTok,
		map[string]any{"technician_id": teknisyenID})
	if rec.Code != http.StatusOK {
		t.Fatalf("atama başarısız: %d", rec.Code)
	}

	rec, out := doLifecycleReq(router, "GET", "/api/v1/technicians/workload", adminTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("iş yükü okunamadı: %d", rec.Code)
	}

	var found bool
	for _, raw := range out["items"].([]any) {
		row := raw.(map[string]any)
		if int(row["technician_id"].(float64)) == teknisyenID {
			found = true
			if row["active_tasks"].(float64) < 1 {
				t.Fatalf("atama sonrası aktif iş sayısı en az 1 olmalı, gelen %v", row["active_tasks"])
			}
		}
	}
	if !found {
		t.Fatalf("teknisyen iş yükü listesinde bulunamadı")
	}
}
