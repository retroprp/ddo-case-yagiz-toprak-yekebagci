package handlers_test

// Materyalize view (mv_equipment_wo_stats) davranışının doğrulanması.
//
// Kanıtlanan:
//  1. Rapor varsayılan olarak MV'den okur (top_source=materialized_view)
//  2. ?fresh=true canlı sorguya düşer, aynı sonucu verir
//  3. Yeni iş emri eklendiğinde MV bayat kalır, refresh sonrası güncellenir
//  4. Refresh ucu yalnızca yöneticiye açık (403 aksi hâlde)
//  5. Refresh anahtarsız çağrılamaz (Idempotency-Key zorunlu)
//
// Neden bu test önemli: materyalize view'ın en tehlikeli özelliği sessiz
// bayatlamadır. Kullanıcı "en problemli ekipman" der, eski sayıyı görür,
// kimse fark etmez. Bu test bayatlığı ve yenilemeyi açıkça kanıtlar.

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

func setupMV(t *testing.T) (*pgxpool.Pool, http.Handler) {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://cmms_user:cmms_secret_pass@localhost:5432/bakimplan_db?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := database.ConnectAndMigrate(ctx, dbURL)
	if err != nil {
		t.Skipf("PostgreSQL erişilemedi, entegrasyon testi atlandı (%v)", err)
	}
	if err := handlers.SeedDefaultUsers(ctx, pool); err != nil {
		t.Fatalf("varsayılan kullanıcılar oluşturulamadı: %v", err)
	}

	authH := handlers.NewAuthHandler(pool)
	metH := handlers.NewMetricsHandler(pool)
	adminH := handlers.NewMetricsAdminHandler(pool)

	r := chi.NewRouter()
	r.Route("/api/v1", func(api chi.Router) {
		api.Post("/auth/login", authH.Login)
		api.Group(func(p chi.Router) {
			p.Use(auth.Authenticate)
			p.Use(appmw.RequireIdempotencyKey())
			p.Use(appmw.Idempotency(pool, func(r *http.Request) *int {
				if c, ok := auth.GetUserFromContext(r.Context()); ok {
					id := c.UserID
					return &id
				}
				return nil
			}))
			p.Use(appmw.AuditTrail(pool, func(r *http.Request) *int {
				if c, ok := auth.GetUserFromContext(r.Context()); ok {
					id := c.UserID
					return &id
				}
				return nil
			}))

			p.Get("/metrics/reliability", metH.GetReliabilityMetrics)
			p.With(auth.RequireRoles("yonetici")).Get("/admin/refresh-metrics", adminH.RefreshState)
			p.With(auth.RequireRoles("yonetici")).Post("/admin/refresh-metrics", adminH.RefreshMetrics)
		})
	})

	t.Cleanup(func() { pool.Close() })
	return pool, r
}

func mvToken(t *testing.T, pool *pgxpool.Pool, role string) string {
	t.Helper()
	var id int
	var name string
	if err := pool.QueryRow(context.Background(),
		`SELECT id, full_name FROM users WHERE role = $1 ORDER BY id LIMIT 1`, role).Scan(&id, &name); err != nil {
		t.Fatalf("'%s' rolünde kullanıcı yok: %v", role, err)
	}
	tok, err := auth.GenerateToken(id, role+"@bakimplan.local", name, role)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return tok
}

func doMV(t *testing.T, router http.Handler, method, path, token string, body any, key string) (int, map[string]any) {
	t.Helper()
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
	router.ServeHTTP(rec, req)

	out := map[string]any{}
	_ = json.NewDecoder(rec.Body).Decode(&out)
	return rec.Code, out
}

func refreshMV(t *testing.T, router http.Handler, token string) {
	t.Helper()
	code, out := doMV(t, router, http.MethodPost, "/api/v1/admin/refresh-metrics", token, nil,
		fmt.Sprintf("mv-refresh-%d", time.Now().UnixNano()))
	if code != http.StatusOK {
		t.Fatalf("refresh başarısız: %d %v", code, out)
	}
}

// topFor, raporda belirli bir ekipmanın satırını döner (yoksa nil).
func topFor(metrics map[string]any, equipmentID int) map[string]any {
	rows, _ := metrics["top_problematic_10"].([]any)
	for _, raw := range rows {
		row := raw.(map[string]any)
		if int(row["equipment_id"].(float64)) == equipmentID {
			return row
		}
	}
	return nil
}

// TestMV01_ReportUsesMaterializedView — varsayılan kaynak MV.
func TestMV01_ReportUsesMaterializedView(t *testing.T) {
	pool, router := setupMV(t)
	tok := mvToken(t, pool, "yonetici")
	refreshMV(t, router, tok)

	code, out := doMV(t, router, http.MethodGet, "/api/v1/metrics/reliability?days=30", tok, nil, "")
	if code != http.StatusOK {
		t.Fatalf("rapor 200 dönmeli, gelen %d", code)
	}
	if out["top_source"] != "materialized_view" {
		t.Fatalf("varsayılan kaynak materialized_view olmalı, gelen %v", out["top_source"])
	}
	if out["top_refreshed_at"] == nil {
		t.Fatalf("MV kaynağında top_refreshed_at dolu olmalı")
	}
}

// TestMV02_FreshParamFallsBackToLive — ?fresh=true canlı sorgu.
func TestMV02_FreshParamFallsBackToLive(t *testing.T) {
	pool, router := setupMV(t)
	tok := mvToken(t, pool, "yonetici")

	code, out := doMV(t, router, http.MethodGet, "/api/v1/metrics/reliability?days=30&fresh=true", tok, nil, "")
	if code != http.StatusOK {
		t.Fatalf("rapor 200 dönmeli, gelen %d", code)
	}
	if out["top_source"] != "live" {
		t.Fatalf("?fresh=true ile kaynak live olmalı, gelen %v", out["top_source"])
	}
}

// TestMV03_StaleUntilRefresh — yeni iş emri MV'de görünmez, refresh sonrası görünür.
//
// Bu test materyalize view'ın tüm amacını kanıtlar: hız için tazelikten
// feragat ediyoruz, ama bayatlık görünür (top_refreshed_at) ve düzeltilebilir
// (refresh ucu).
func TestMV03_StaleUntilRefresh(t *testing.T) {
	pool, router := setupMV(t)
	tok := mvToken(t, pool, "yonetici")
	ctx := context.Background()

	// Raporun başlangıç durumunu sabitle.
	refreshMV(t, router, tok)

	// Yeni bir ekipman + 3 arıza iş emri: bu ekipman MV'de henüz yok.
	var eqID int
	if err := pool.QueryRow(ctx,
		`INSERT INTO equipments (code, name, level_type) VALUES ($1, 'MV Stale Test', 'makine') RETURNING id`,
		fmt.Sprintf("EQ-MV-%d", time.Now().UnixNano()),
	).Scan(&eqID); err != nil {
		t.Fatalf("ekipman oluşturulamadı: %v", err)
	}

	// Top-10'a girebilecek kadar çok arıza üret (599 arıza).
	// Mevcut en yüksek değer ~602 olduğu için 599 ile listeye girer.
	for i := 0; i < 599; i++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO work_orders (code, equipment_id, wo_type, status, priority, title, labor_cost_kurus)
			 VALUES ($1, $2, 'corrective', 'tamamlandi', 'normal', 'mv stale', 1000)`,
			fmt.Sprintf("WO-MV-%d-%d", eqID, i), eqID,
		); err != nil {
			t.Fatalf("iş emri eklenemedi: %v", err)
		}
	}

	// 1) Refresh ÖNCESİ: MV bayat → yeni ekipman listede yok.
	_, stale := doMV(t, router, http.MethodGet, "/api/v1/metrics/reliability?days=3650", tok, nil, "")
	if row := topFor(stale, eqID); row != nil {
		t.Fatalf("refresh öncesi yeni ekipman MV'de görünmemeliydi (failure_wo_count=%v)", row["failure_wo_count"])
	}

	// 2) Canlı sorgu doğru cevabı verir (kaçış yolu çalışıyor).
	_, live := doMV(t, router, http.MethodGet, "/api/v1/metrics/reliability?days=3650&fresh=true", tok, nil, "")
	liveRow := topFor(live, eqID)
	if liveRow == nil {
		t.Fatalf("canlı sorgu yeni ekipmanı görmeliydi")
	}
	if int(liveRow["failure_wo_count"].(float64)) != 599 {
		t.Fatalf("canlı sorgu 599 arıza bekleniyordu, gelen %v", liveRow["failure_wo_count"])
	}

	// 3) Refresh sonrası MV de aynı cevabı verir.
	refreshMV(t, router, tok)
	_, fresh := doMV(t, router, http.MethodGet, "/api/v1/metrics/reliability?days=3650", tok, nil, "")
	freshRow := topFor(fresh, eqID)
	if freshRow == nil {
		t.Fatalf("refresh sonrası yeni ekipman MV'de görünmeliydi")
	}
	if int(freshRow["failure_wo_count"].(float64)) != 599 {
		t.Fatalf("refresh sonrası 599 bekleniyordu, gelen %v", freshRow["failure_wo_count"])
	}
}

// TestMV04_RefreshRequiresManager — refresh yalnızca yöneticiye açık.
func TestMV04_RefreshRequiresManager(t *testing.T) {
	pool, router := setupMV(t)
	tekTok := mvToken(t, pool, "teknisyen")

	code, out := doMV(t, router, http.MethodPost, "/api/v1/admin/refresh-metrics", tekTok, nil,
		fmt.Sprintf("mv-403-%d", time.Now().UnixNano()))
	if code != http.StatusForbidden {
		t.Fatalf("teknisyen refresh edememeli (403), gelen %d %v", code, out)
	}

	code, _ = doMV(t, router, http.MethodGet, "/api/v1/admin/refresh-metrics", tekTok, nil, "")
	if code != http.StatusForbidden {
		t.Fatalf("teknisyen tazelik durumunu okuyamamalı (403), gelen %d", code)
	}
}

// TestMV05_RefreshRequiresIdempotencyKey — anahtarsız refresh 400.
func TestMV05_RefreshRequiresIdempotencyKey(t *testing.T) {
	pool, router := setupMV(t)
	tok := mvToken(t, pool, "yonetici")

	code, out := doMV(t, router, http.MethodPost, "/api/v1/admin/refresh-metrics", tok, nil, "")
	if code != http.StatusBadRequest {
		t.Fatalf("anahtarsız refresh 400 dönmeli, gelen %d %v", code, out)
	}
	errBody, ok := out["error"].(map[string]any)
	if !ok || errBody["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("IDEMPOTENCY_KEY_REQUIRED bekleniyordu, gelen %v", out)
	}
}

// TestMV06_RefreshStateReportsFreshness — tazelik ucu anlamlı değer döner.
func TestMV06_RefreshStateReportsFreshness(t *testing.T) {
	pool, router := setupMV(t)
	tok := mvToken(t, pool, "yonetici")
	refreshMV(t, router, tok)

	code, out := doMV(t, router, http.MethodGet, "/api/v1/admin/refresh-metrics", tok, nil, "")
	if code != http.StatusOK {
		t.Fatalf("tazelik ucu 200 dönmeli, gelen %d", code)
	}
	if out["ready"] != true {
		t.Fatalf("refresh sonrası ready=true olmalı, gelen %v", out["ready"])
	}
	if out["refreshed_at"] == nil {
		t.Fatalf("refreshed_at dolu olmalı")
	}
	age, ok := out["age_seconds"].(float64)
	if !ok || age < 0 {
		t.Fatalf("age_seconds negatif olamaz, gelen %v", out["age_seconds"])
	}
	if rows, ok := out["row_count"].(float64); !ok || rows <= 0 {
		t.Fatalf("row_count pozitif olmalı, gelen %v", out["row_count"])
	}
}
