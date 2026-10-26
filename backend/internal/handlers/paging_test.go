package handlers_test

// Keyset (cursor) sayfalamasının doğrulanması.
//
// Kanıtlanan davranış:
//  1. limit uygulanır, has_more doğru raporlanır
//  2. cursor ile sonraki sayfa gelir, kayıt tekrar etmez
//  3. tüm sayfalar gezildiğinde toplam = benzersiz (kayma/tekrar yok)
//  4. hatalı cursor/limit → 400 INVALID_CURSOR
//  5. limit üst sınırı aşarsa kırpılır (200)
//
// Neden bu test önemli: sayfalama hataları sessizdir. Aynı kayıt iki sayfada
// görünür veya son kayıt hiç gelmez; kimse fark etmez. Bu test bunu yakalar.

import (
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

func setupPagingRouter(t *testing.T) (*pgxpool.Pool, http.Handler) {
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

	authH := handlers.NewAuthHandler(pool)
	eqH := handlers.NewEquipmentHandler(pool)
	invH := handlers.NewInventoryHandler(pool)
	maintH := handlers.NewMaintenanceHandler(pool)

	r := chi.NewRouter()
	r.Route("/api/v1", func(api chi.Router) {
		api.Post("/auth/login", authH.Login)
		api.Group(func(p chi.Router) {
			p.Use(auth.Authenticate)
			p.Use(appmw.AuditTrail(pool, func(r *http.Request) *int {
				if c, ok := auth.GetUserFromContext(r.Context()); ok {
					id := c.UserID
					return &id
				}
				return nil
			}))

			p.Get("/equipments/tree", eqH.GetTree)
			p.Get("/work-orders", maintH.ListWorkOrders)
			p.Get("/failure-reports", maintH.ListFailureReports)
			p.Get("/preventive-plans", maintH.ListPMPlans)
			p.Get("/parts", invH.ListParts)
			p.Get("/audit-logs", authH.ListAuditLogs)
		})
	})

	t.Cleanup(func() { pool.Close() })
	return pool, r
}

type pageResult struct {
	Items      []map[string]any `json:"items"`
	NextCursor *int64           `json:"next_cursor"`
	HasMore    bool             `json:"has_more"`
}

func getPage(t *testing.T, router http.Handler, path, token string) pageResult {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("%s beklenen 200, gelen %d — %s", path, rec.Code, rec.Body.String())
	}

	var out pageResult
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("%s yanıtı çözülemedi: %v", path, err)
	}
	return out
}

func adminTokenPaging(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id int
	var name string
	if err := pool.QueryRow(context.Background(),
		`SELECT id, full_name FROM users WHERE role = 'yonetici' ORDER BY id LIMIT 1`).Scan(&id, &name); err != nil {
		t.Fatalf("yönetici bulunamadı: %v", err)
	}
	tok, err := auth.GenerateToken(id, "yonetici@bakimplan.local", name, "yonetici")
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return tok
}

// TestPaging01_BasicPage — limit uygulanır, has_more ve next_cursor tutarlı.
func TestPaging01_BasicPage(t *testing.T) {
	pool, router := setupPagingRouter(t)
	tok := adminTokenPaging(t, pool)

	page := getPage(t, router, "/api/v1/work-orders?limit=5", tok)

	if len(page.Items) != 5 {
		t.Fatalf("limit=5 için 5 kayıt bekleniyordu, gelen %d", len(page.Items))
	}
	if !page.HasMore {
		t.Fatalf("5 kayıttan sonra devamı olmalı (has_more=true)")
	}
	if page.NextCursor == nil {
		t.Fatalf("has_more=true iken next_cursor dolu olmalı")
	}
}

// TestPaging02_NoDuplicateAcrossPages — sayfalar arası kayıt tekrarı yok.
func TestPaging02_NoDuplicateAcrossPages(t *testing.T) {
	pool, router := setupPagingRouter(t)
	tok := adminTokenPaging(t, pool)

	seen := map[string]bool{}
	var cursor *int64
	pages := 0

	for pages < 5 {
		path := "/api/v1/work-orders?limit=7"
		if cursor != nil {
			path += fmt.Sprintf("&cursor=%d", *cursor)
		}
		page := getPage(t, router, path, tok)
		if len(page.Items) == 0 {
			break
		}
		for _, it := range page.Items {
			key := fmt.Sprintf("%v", it["id"])
			if seen[key] {
				t.Fatalf("kayıt iki farklı sayfada tekrar etti: id=%s", key)
			}
			seen[key] = true
		}
		if !page.HasMore || page.NextCursor == nil {
			break
		}
		cursor = page.NextCursor
		pages++
	}

	if len(seen) == 0 {
		t.Fatalf("hiç kayıt dönmedi")
	}
}

// TestPaging03_FullTraversal — tüm sayfalar gezilince benzersizlik korunur.
func TestPaging03_FullTraversal(t *testing.T) {
	pool, router := setupPagingRouter(t)
	tok := adminTokenPaging(t, pool)

	// Katalog listeleri sabit boyutlu (50 parça), tamamını gezmek hızlı.
	seen := map[string]bool{}
	var cursor *int64

	for i := 0; i < 20; i++ {
		path := "/api/v1/parts?limit=7"
		if cursor != nil {
			path += fmt.Sprintf("&cursor=%d", *cursor)
		}
		page := getPage(t, router, path, tok)
		for _, it := range page.Items {
			seen[fmt.Sprintf("%v", it["id"])] = true
		}
		if !page.HasMore || page.NextCursor == nil {
			break
		}
		cursor = page.NextCursor
	}

	var total int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM parts`).Scan(&total); err != nil {
		t.Fatalf("parça sayısı: %v", err)
	}
	if len(seen) != total {
		t.Fatalf("sayfalama %d parçadan %d tanesini döndürdü (kayıp var)", total, len(seen))
	}
}

// TestPaging04_LastPageHasNoCursor — son sayfada next_cursor null olmalı.
func TestPaging04_LastPageHasNoCursor(t *testing.T) {
	pool, router := setupPagingRouter(t)
	tok := adminTokenPaging(t, pool)

	var total int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM parts`).Scan(&total); err != nil {
		t.Fatalf("parça sayısı: %v", err)
	}

	// limit == toplam: tek sayfada hepsi gelir, devam yok.
	page := getPage(t, router, fmt.Sprintf("/api/v1/parts?limit=%d", total), tok)

	if len(page.Items) != total {
		t.Fatalf("limit=%d için %d kayıt bekleniyordu, gelen %d", total, total, len(page.Items))
	}
	if page.HasMore {
		t.Fatalf("tam %d kayıt dönerken has_more=false olmalı", total)
	}
	if page.NextCursor != nil {
		t.Fatalf("son sayfada next_cursor null olmalı, gelen %v", *page.NextCursor)
	}
}

// TestPaging05_InvalidParams — hatalı cursor/limit 400 döner.
func TestPaging05_InvalidParams(t *testing.T) {
	pool, router := setupPagingRouter(t)
	tok := adminTokenPaging(t, pool)

	cases := []string{
		"/api/v1/parts?cursor=abc",
		"/api/v1/parts?cursor=-1",
		"/api/v1/parts?limit=0",
		"/api/v1/parts?limit=abc",
	}

	for _, path := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s için 400 bekleniyordu, gelen %d", path, rec.Code)
		}
		var body map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&body)
		errObj, ok := body["error"].(map[string]any)
		if !ok || errObj["code"] != "INVALID_CURSOR" {
			t.Fatalf("%s için INVALID_CURSOR bekleniyordu, gelen %v", path, body)
		}
	}
}

// TestPaging06_LimitIsCapped — üst sınır aşılırsa sessizce kırpılır.
func TestPaging06_LimitIsCapped(t *testing.T) {
	pool, router := setupPagingRouter(t)
	tok := adminTokenPaging(t, pool)

	page := getPage(t, router, "/api/v1/work-orders?limit=5000", tok)
	if len(page.Items) > 200 {
		t.Fatalf("limit 200 ile sınırlanmalı, gelen %d kayıt", len(page.Items))
	}
}

// TestPaging07_AllPaginatedEndpoints — beş liste de sayfalama sözleşmesine uyar.
func TestPaging07_AllPaginatedEndpoints(t *testing.T) {
	pool, router := setupPagingRouter(t)
	tok := adminTokenPaging(t, pool)

	paths := []string{
		"/api/v1/work-orders?limit=3",
		"/api/v1/failure-reports?limit=3",
		"/api/v1/preventive-plans?limit=3",
		"/api/v1/parts?limit=3",
		"/api/v1/audit-logs?limit=3",
	}

	for _, path := range paths {
		page := getPage(t, router, path, tok)
		if len(page.Items) > 3 {
			t.Fatalf("%s: limit=3 aşıldı (%d kayıt)", path, len(page.Items))
		}
		// has_more ile next_cursor birbiriyle tutarlı olmalı.
		if page.HasMore && page.NextCursor == nil {
			t.Fatalf("%s: has_more=true ama next_cursor null", path)
		}
		if !page.HasMore && page.NextCursor != nil {
			t.Fatalf("%s: has_more=false ama next_cursor dolu", path)
		}
	}
}
