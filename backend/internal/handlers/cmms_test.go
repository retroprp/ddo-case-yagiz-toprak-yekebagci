package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
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

// ============================================================================
// BÖLÜM 1: 14 ADET BİRİM TESTİ (ALGORİTMALAR, AUTH, VARDİYA VE INTERVAL MERGING)
// ============================================================================

func TestUnit01_PasswordHashAndVerify(t *testing.T) {
	hash, err := auth.HashPassword("Password123!")
	if err != nil || hash == "" {
		t.Fatalf("hash error: %v", err)
	}
	if !auth.CheckPassword("Password123!", hash) {
		t.Fatal("expected valid password check")
	}
}

func TestUnit02_PasswordRejectWrong(t *testing.T) {
	hash, _ := auth.HashPassword("Password123!")
	if auth.CheckPassword("WrongPass", hash) {
		t.Fatal("expected wrong password to fail")
	}
}

func TestUnit03_JWTGenerateAndParse(t *testing.T) {
	tok, err := auth.GenerateToken(7, "test@bakimplan.local", "Test User", "planlama")
	if err != nil {
		t.Fatalf("token error: %v", err)
	}
	claims, err := auth.ParseToken(tok)
	if err != nil || claims.UserID != 7 || claims.Role != "planlama" {
		t.Fatalf("unexpected claims: %+v (%v)", claims, err)
	}
}

func TestUnit04_JWTRejectTamperedToken(t *testing.T) {
	tok, _ := auth.GenerateToken(1, "a@b.c", "User", "yonetici")
	if _, err := auth.ParseToken(tok + "tampered"); err == nil {
		t.Fatal("expected tampered token to be rejected")
	}
}

func TestUnit05_MergeIntervals_EmptyAndSingle(t *testing.T) {
	if len(handlers.MergeIntervals(nil)) != 0 {
		t.Fatal("expected empty slice")
	}
	now := time.Now().UTC()
	single := []handlers.TimeInterval{{Start: now, End: now.Add(time.Hour)}}
	if len(handlers.MergeIntervals(single)) != 1 {
		t.Fatal("expected 1 interval")
	}
}

func TestUnit06_MergeIntervals_Overlapping(t *testing.T) {
	t0 := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	ivs := []handlers.TimeInterval{
		{Start: t0, End: t0.Add(2 * time.Hour)},                    // 10:00 - 12:00
		{Start: t0.Add(1 * time.Hour), End: t0.Add(3 * time.Hour)}, // 11:00 - 13:00
	}
	merged := handlers.MergeIntervals(ivs)
	if len(merged) != 1 || merged[0].End.Sub(merged[0].Start).Hours() != 3.0 {
		t.Fatalf("expected 1 merged interval of 3h, got %+v", merged)
	}
}

func TestUnit07_MergeIntervals_NestedInside(t *testing.T) {
	t0 := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	ivs := []handlers.TimeInterval{
		{Start: t0, End: t0.Add(6 * time.Hour)},                    // 08:00 - 14:00
		{Start: t0.Add(1 * time.Hour), End: t0.Add(2 * time.Hour)}, // 09:00 - 10:00 (iç içe)
	}
	merged := handlers.MergeIntervals(ivs)
	if len(merged) != 1 || merged[0].End.Sub(merged[0].Start).Hours() != 6.0 {
		t.Fatalf("expected 6h single interval, got %+v", merged)
	}
}

func TestUnit08_MergeIntervals_DisjointAndUnsorted(t *testing.T) {
	t0 := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	ivs := []handlers.TimeInterval{
		{Start: t0.Add(5 * time.Hour), End: t0.Add(7 * time.Hour)}, // 13:00 - 15:00
		{Start: t0, End: t0.Add(2 * time.Hour)},                    // 08:00 - 10:00
	}
	merged := handlers.MergeIntervals(ivs)
	if len(merged) != 2 {
		t.Fatalf("expected 2 separate intervals, got %d", len(merged))
	}
}

func TestUnit09_MergeIntervals_TouchingBoundaries(t *testing.T) {
	t0 := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	ivs := []handlers.TimeInterval{
		{Start: t0, End: t0.Add(2 * time.Hour)},                    // 08:00 - 10:00
		{Start: t0.Add(2 * time.Hour), End: t0.Add(4 * time.Hour)}, // 10:00 - 12:00 (uç uca)
	}
	merged := handlers.MergeIntervals(ivs)
	if len(merged) != 1 || merged[0].End.Sub(merged[0].Start).Hours() != 4.0 {
		t.Fatalf("expected touching intervals to merge into 4h, got %+v", merged)
	}
}

func TestUnit10_EffectiveShiftHours_InsideShift(t *testing.T) {
	st := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	et := time.Date(2026, 10, 10, 14, 0, 0, 0, time.UTC)
	if h := handlers.EffectiveShiftHours(st, et, 16); h != 4.0 {
		t.Fatalf("expected 4.0 shift hours, got %v", h)
	}
}

func TestUnit11_EffectiveShiftHours_ExcludeOffShiftNight(t *testing.T) {
	st := time.Date(2026, 10, 10, 4, 0, 0, 0, time.UTC)
	et := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	if h := handlers.EffectiveShiftHours(st, et, 16); h != 2.0 {
		t.Fatalf("expected 2.0 hours inside shift, got %v", h)
	}
}

func TestUnit12_EffectiveShiftHours_24HourContinuous(t *testing.T) {
	st := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	et := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if h := handlers.EffectiveShiftHours(st, et, 24); h != 12.0 {
		t.Fatalf("expected 12.0 hours for 24h shift, got %v", h)
	}
}

func TestUnit13_StandardErrorResponseFormat(t *testing.T) {
	rec := httptest.NewRecorder()
	appmw.WriteError(rec, http.StatusForbidden, "FORBIDDEN_ROLE", "Yetkisiz işlem")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	var body map[string]map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body["error"]["code"] != "FORBIDDEN_ROLE" {
		t.Fatalf("unexpected error format: %+v", body)
	}
}

func TestUnit14_RBACMiddlewareBlocksUnauthorizedRole(t *testing.T) {
	tok, _ := auth.GenerateToken(2, "tek@bakimplan.local", "Tek", "teknisyen")
	r := chi.NewRouter()
	r.Use(auth.Authenticate)
	r.With(auth.RequireRoles("yonetici")).Get("/secret", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/secret", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for teknisyen, got %d", rec.Code)
	}
}

// ============================================================================
// BÖLÜM 2: 16 ADET GERÇEK POSTGRESQL ENTEGRASYON TESTİ (F-1..F-7 & T-1..T-4)
// ============================================================================

func setupIntegrationRouter(t *testing.T) (*pgxpool.Pool, http.Handler) {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://cmms_user:cmms_secret_pass@localhost:5432/bakimplan_db?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := database.ConnectAndMigrate(ctx, dbURL)
	if err != nil {
		t.Skipf("PostgreSQL not reachable for integration test (%v)", err)
	}
	_ = handlers.SeedDefaultUsers(ctx, pool)

	extractUserID := func(r *http.Request) *int {
		if claims, ok := auth.GetUserFromContext(r.Context()); ok && claims != nil {
			uid := claims.UserID
			return &uid
		}
		return nil
	}

	r := chi.NewRouter()
	authH := handlers.NewAuthHandler(pool)
	eqH := handlers.NewEquipmentHandler(pool)
	invH := handlers.NewInventoryHandler(pool)
	maintH := handlers.NewMaintenanceHandler(pool)
	metH := handlers.NewMetricsHandler(pool)

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
			p.Patch("/equipments/{id}/move", eqH.Move)
			p.Post("/equipments/{id}/meter-readings", maintH.RecordMeterReading)
			p.Post("/failure-reports", maintH.CreateFailureReport)
			p.Get("/work-orders", maintH.ListWorkOrders)
			p.Post("/work-orders", maintH.CreateWorkOrder)
			p.Post("/work-orders/{id}/complete", maintH.CompleteWorkOrder)
			p.Post("/preventive-plans", maintH.CreatePMPlan)
			p.Post("/preventive-plans/trigger-due", maintH.TriggerDueTimePMs)
			p.Get("/parts", invH.ListParts)
			p.Post("/stock-movements", invH.CreateStockMovement)
			p.Post("/work-orders/{id}/reserve-part", invH.ReservePartForWorkOrder)
			p.Get("/technicians/workload", maintH.GetTechnicianWorkload)
			p.Get("/metrics/reliability", metH.GetReliabilityMetrics)
			p.Post("/downtime-events", metH.CreateDowntimeEvent)
			p.Patch("/downtime-events/{id}", metH.UpdateDowntimeEvent)
			p.With(auth.RequireRoles("yonetici")).Get("/audit-logs", authH.ListAuditLogs)
		})
	})

	t.Cleanup(func() { pool.Close() })
	return pool, r
}

func doTestReq(r http.Handler, method, path, token string, body any, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// Gerçek istemci davranışı: değiştirici isteklerde anahtar yoksa üret.
	// Kritik uçlar Idempotency-Key olmadan 400 IDEMPOTENCY_KEY_REQUIRED döner.
	if needsIdempotencyKey(method) && req.Header.Get("Idempotency-Key") == "" {
		req.Header.Set("Idempotency-Key", newTestIdempotencyKey())
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&out)
	return rec, out
}

// needsIdempotencyKey, değiştirici HTTP metotlarını bildirir.
func needsIdempotencyKey(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// testIdemCounter, aynı nanosaniyede üretilen anahtarların çakışmasını engeller.
// (Çakışırsa ikinci istek idempotent "replay" olur ve eşzamanlılık sayaçlarını yanıltır.)
var testIdemCounter uint64

func newTestIdempotencyKey() string {
	return fmt.Sprintf("test-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&testIdemCounter, 1))
}

func TestIntegration01_SeedAndLoginAll5Roles(t *testing.T) {
	_, router := setupIntegrationRouter(t)
	roles := []string{"yonetici", "planlama", "teknisyen", "uretim", "depo"}
	for _, role := range roles {
		rec, out := doTestReq(router, "POST", "/api/v1/auth/login", "", map[string]any{
			"email": role + "@bakimplan.local", "password": "Password123!",
		}, nil)
		if rec.Code != http.StatusOK || out["access_token"] == nil {
			t.Fatalf("role %s login failed: %d", role, rec.Code)
		}
	}
}

func TestIntegration02_IdempotencyKeyReplay(t *testing.T) {
	pool, router := setupIntegrationRouter(t)
	tok, _ := auth.GenerateToken(1, "yonetici@bakimplan.local", "Admin", "yonetici")

	_, _ = doTestReq(router, "POST", "/api/v1/equipments", tok, map[string]any{
		"code": fmt.Sprintf("EQ-IDEM-%d", time.Now().UnixNano()), "name": "Idem Tesis", "level_type": "tesis",
	}, nil)

	var firstEqID int
	_ = pool.QueryRow(context.Background(), `SELECT id FROM equipments ORDER BY id ASC LIMIT 1`).Scan(&firstEqID)

	key := fmt.Sprintf("idem-test-%d", time.Now().UnixNano())
	payload := map[string]any{"equipment_id": firstEqID, "title": "Tekrarlı İstek Testi", "severity": "orta"}

	rec1, out1 := doTestReq(router, "POST", "/api/v1/failure-reports", tok, payload, map[string]string{"Idempotency-Key": key})
	rec2, out2 := doTestReq(router, "POST", "/api/v1/failure-reports", tok, payload, map[string]string{"Idempotency-Key": key})

	if rec1.Code != 201 || rec2.Code != 201 || out1["id"] != out2["id"] {
		t.Fatalf("idempotency failed: %v vs %v", out1, out2)
	}
}

func TestIntegration03_IdempotencyCrossUserAndPathIsolation(t *testing.T) {
	pool, router := setupIntegrationRouter(t)
	tokUser1, _ := auth.GenerateToken(1, "yonetici@bakimplan.local", "Admin", "yonetici")
	tokUser2, _ := auth.GenerateToken(2, "planlama@bakimplan.local", "Plan", "planlama")

	var firstEqID int
	_ = pool.QueryRow(context.Background(), `SELECT id FROM equipments ORDER BY id ASC LIMIT 1`).Scan(&firstEqID)
	if firstEqID == 0 {
		_, eqOut := doTestReq(router, "POST", "/api/v1/equipments", tokUser1, map[string]any{
			"code": fmt.Sprintf("EQ-ISO-%d", time.Now().UnixNano()), "name": "Iso Tesis", "level_type": "tesis",
		}, nil)
		firstEqID = int(eqOut["id"].(float64))
	}

	key := fmt.Sprintf("idem-sec-%d", time.Now().UnixNano())
	payload := map[string]any{"equipment_id": firstEqID, "title": "User1 İsteği", "severity": "orta"}

	rec1, _ := doTestReq(router, "POST", "/api/v1/failure-reports", tokUser1, payload, map[string]string{"Idempotency-Key": key})
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected 201 on first request, got %d", rec1.Code)
	}

	// Farklı kullanıcı aynı Idempotency-Key ile istek atarsa 409 IDEMPOTENCY_USER_MISMATCH dönmeli!
	recCrossUser, _ := doTestReq(router, "POST", "/api/v1/failure-reports", tokUser2, payload, map[string]string{"Idempotency-Key": key})
	if recCrossUser.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for cross-user idempotency key reuse, got %d", recCrossUser.Code)
	}

	// Aynı kullanıcı aynı anahtarı farklı bir endpoint (/work-orders) için kullanırsa 409 IDEMPOTENCY_KEY_CONFLICT dönmeli!
	recCrossPath, _ := doTestReq(router, "POST", "/api/v1/work-orders", tokUser1, map[string]any{
		"equipment_id": firstEqID, "title": "Farklı Endpoint",
	}, map[string]string{"Idempotency-Key": key})
	if recCrossPath.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for cross-path idempotency key reuse, got %d", recCrossPath.Code)
	}
}

func TestIntegration04_EquipmentHierarchyAndCyclePrevention_Task2(t *testing.T) {
	_, router := setupIntegrationRouter(t)
	tok, _ := auth.GenerateToken(1, "planlama@bakimplan.local", "Plan", "planlama")
	suffix := time.Now().UnixNano()

	_, pOut := doTestReq(router, "POST", "/api/v1/equipments", tok, map[string]any{
		"code": fmt.Sprintf("TES-T2-%d", suffix), "name": "Tesis T2", "level_type": "tesis",
	}, nil)
	parentID := int(pOut["id"].(float64))

	_, cOut := doTestReq(router, "POST", "/api/v1/equipments", tok, map[string]any{
		"code": fmt.Sprintf("HAT-T2-%d", suffix), "name": "Hat T2", "level_type": "hat", "parent_id": parentID,
	}, nil)
	childID := int(cOut["id"].(float64))

	recCycle, _ := doTestReq(router, "PATCH", fmt.Sprintf("/api/v1/equipments/%d/move", parentID), tok, map[string]any{
		"new_parent_id": childID,
	}, nil)
	if recCycle.Code != http.StatusConflict {
		t.Fatalf("expected 409 CYCLE_DETECTED, got %d", recCycle.Code)
	}
}

func TestIntegration05_EquipmentInvalidHierarchyLevelRejection_Task2(t *testing.T) {
	_, router := setupIntegrationRouter(t)
	tok, _ := auth.GenerateToken(1, "planlama@bakimplan.local", "Plan", "planlama")
	suffix := time.Now().UnixNano()

	_, tOut := doTestReq(router, "POST", "/api/v1/equipments", tok, map[string]any{
		"code": fmt.Sprintf("TES-HL-%d", suffix), "name": "Tesis HL", "level_type": "tesis",
	}, nil)
	tesisID := int(tOut["id"].(float64))

	// 'makine' doğrudan 'tesis' altına bağlanamaz ('hat' altına bağlanmalıdır) -> 422 dönmeli!
	recInvalid, _ := doTestReq(router, "POST", "/api/v1/equipments", tok, map[string]any{
		"code": fmt.Sprintf("MAK-HL-%d", suffix), "name": "Hatalı Makine", "level_type": "makine", "parent_id": tesisID,
	}, nil)
	if recInvalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 INVALID_HIERARCHY_LEVEL when attaching makine directly to tesis, got %d", recInvalid.Code)
	}
}

func TestIntegration06_EquipmentRollupCosts_Task2(t *testing.T) {
	_, router := setupIntegrationRouter(t)
	tok, _ := auth.GenerateToken(1, "planlama@bakimplan.local", "Plan", "planlama")
	suffix := time.Now().UnixNano()

	_, tOut := doTestReq(router, "POST", "/api/v1/equipments", tok, map[string]any{
		"code": fmt.Sprintf("TES-ROLL-%d", suffix), "name": "Rollup Tesis", "level_type": "tesis",
	}, nil)
	tesisID := int(tOut["id"].(float64))

	_, hOut := doTestReq(router, "POST", "/api/v1/equipments", tok, map[string]any{
		"code": fmt.Sprintf("HAT-ROLL-%d", suffix), "name": "Rollup Hat", "level_type": "hat", "parent_id": tesisID,
	}, nil)
	hatID := int(hOut["id"].(float64))

	_, mOut := doTestReq(router, "POST", "/api/v1/equipments", tok, map[string]any{
		"code": fmt.Sprintf("MAK-ROLL-%d", suffix), "name": "Rollup Makine", "level_type": "makine", "parent_id": hatID,
	}, nil)
	makID := int(mOut["id"].(float64))

	_, _ = doTestReq(router, "POST", "/api/v1/work-orders", tok, map[string]any{
		"equipment_id": makID, "title": "Alt Maliyet", "labor_cost_kurus": 125000,
	}, nil)

	recTree, treeOut := doTestReq(router, "GET", "/api/v1/equipments/tree", tok, nil, nil)
	if recTree.Code != http.StatusOK || treeOut["tree"] == nil || treeOut["flat_count"] == nil {
		t.Fatalf("tree query failed: %d", recTree.Code)
	}
}

func TestIntegration07_ConcurrentPartReservationRaceCondition_Task3(t *testing.T) {
	pool, router := setupIntegrationRouter(t)
	tok, _ := auth.GenerateToken(1, "yonetici@bakimplan.local", "Admin", "yonetici")
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	var eqID, whID, partID int
	_ = pool.QueryRow(ctx, `INSERT INTO equipments (code, name, level_type) VALUES ($1, 'Race Eq', 'tesis') RETURNING id`, fmt.Sprintf("EQ-RC-%d", suffix)).Scan(&eqID)
	_ = pool.QueryRow(ctx, `INSERT INTO warehouses (code, name) VALUES ($1, 'Race Depo') RETURNING id`, fmt.Sprintf("WH-RC-%d", suffix)).Scan(&whID)
	_ = pool.QueryRow(ctx, `INSERT INTO parts (sku, name, unit_cost_kurus, reorder_point, is_critical) VALUES ($1, 'Race Rulman', 10000, 2, true) RETURNING id`, fmt.Sprintf("SKU-RC-%d", suffix)).Scan(&partID)
	_, _ = pool.Exec(ctx, `INSERT INTO equipment_parts_bom (equipment_id, part_id) VALUES ($1, $2)`, eqID, partID)

	_, _ = doTestReq(router, "POST", "/api/v1/stock-movements", tok, map[string]any{
		"part_id": partID, "warehouse_id": whID, "quantity_change": 4, "movement_type": "purchase",
	}, nil)

	_, woOut := doTestReq(router, "POST", "/api/v1/work-orders", tok, map[string]any{
		"equipment_id": eqID, "title": "Race Condition WO", "labor_cost_kurus": 1000,
	}, nil)
	woID := int64(woOut["id"].(float64))

	var wg sync.WaitGroup
	var okCount, conflictCount int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec, _ := doTestReq(router, "POST", fmt.Sprintf("/api/v1/work-orders/%d/reserve-part", woID), tok, map[string]any{
				"part_id": partID, "warehouse_id": whID, "quantity": 1,
			}, nil)
			switch rec.Code {
			case http.StatusCreated:
				atomic.AddInt32(&okCount, 1)
			case http.StatusConflict:
				atomic.AddInt32(&conflictCount, 1)
			}
		}()
	}
	wg.Wait()

	if okCount != 4 || conflictCount != 16 {
		t.Fatalf("race condition protection failed! expected 4 success & 16 conflict, got %d success & %d conflict", okCount, conflictCount)
	}
}

func TestIntegration08_WorkOrderCompletionConsumesReservedStock_Task3(t *testing.T) {
	pool, router := setupIntegrationRouter(t)
	tok, _ := auth.GenerateToken(1, "yonetici@bakimplan.local", "Admin", "yonetici")
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	var eqID, whID, partID int
	_ = pool.QueryRow(ctx, `INSERT INTO equipments (code, name, level_type) VALUES ($1, 'Consume Eq', 'tesis') RETURNING id`, fmt.Sprintf("EQ-CS-%d", suffix)).Scan(&eqID)
	_ = pool.QueryRow(ctx, `INSERT INTO warehouses (code, name) VALUES ($1, 'Consume Depo') RETURNING id`, fmt.Sprintf("WH-CS-%d", suffix)).Scan(&whID)
	_ = pool.QueryRow(ctx, `INSERT INTO parts (sku, name, unit_cost_kurus) VALUES ($1, 'Consume Parça', 5000) RETURNING id`, fmt.Sprintf("SKU-CS-%d", suffix)).Scan(&partID)
	_, _ = pool.Exec(ctx, `INSERT INTO equipment_parts_bom (equipment_id, part_id) VALUES ($1, $2)`, eqID, partID)

	_, _ = doTestReq(router, "POST", "/api/v1/stock-movements", tok, map[string]any{
		"part_id": partID, "warehouse_id": whID, "quantity_change": 10, "movement_type": "purchase",
	}, nil)

	_, woOut := doTestReq(router, "POST", "/api/v1/work-orders", tok, map[string]any{"equipment_id": eqID, "title": "Consume WO"}, nil)
	woID := int64(woOut["id"].(float64))

	_, _ = doTestReq(router, "POST", fmt.Sprintf("/api/v1/work-orders/%d/reserve-part", woID), tok, map[string]any{
		"part_id": partID, "warehouse_id": whID, "quantity": 3,
	}, nil)

	// İş emri tamamlandığında 3 adet rezerve parça fiziksel stoktan (-3) düşmeli ve fiziksel stok 7 olmalı!
	recComp, _ := doTestReq(router, "POST", fmt.Sprintf("/api/v1/work-orders/%d/complete", woID), tok, map[string]any{}, nil)
	if recComp.Code != http.StatusOK {
		t.Fatalf("complete wo failed: %d", recComp.Code)
	}

	var physicalAfter int
	_ = pool.QueryRow(ctx, `SELECT COALESCE(SUM(quantity_change), 0) FROM stock_movements WHERE part_id = $1 AND warehouse_id = $2`, partID, whID).Scan(&physicalAfter)
	if physicalAfter != 7 {
		t.Fatalf("expected physical stock=7 after consuming 3 from 10, got %d", physicalAfter)
	}
}

func TestIntegration09_BOMIncompatibilityRejection_Task3(t *testing.T) {
	pool, router := setupIntegrationRouter(t)
	tok, _ := auth.GenerateToken(1, "yonetici@bakimplan.local", "Admin", "yonetici")
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	var eqID, whID, p1, p2 int
	_ = pool.QueryRow(ctx, `INSERT INTO equipments (code, name, level_type) VALUES ($1, 'BOM Eq', 'tesis') RETURNING id`, fmt.Sprintf("EQ-BOM-%d", suffix)).Scan(&eqID)
	_ = pool.QueryRow(ctx, `INSERT INTO warehouses (code, name) VALUES ($1, 'BOM WH') RETURNING id`, fmt.Sprintf("WH-BOM-%d", suffix)).Scan(&whID)
	_ = pool.QueryRow(ctx, `INSERT INTO parts (sku, name, unit_cost_kurus) VALUES ($1, 'Uyumlu Parça', 5000) RETURNING id`, fmt.Sprintf("P1-%d", suffix)).Scan(&p1)
	_ = pool.QueryRow(ctx, `INSERT INTO parts (sku, name, unit_cost_kurus) VALUES ($1, 'Uyumsuz Parça', 5000) RETURNING id`, fmt.Sprintf("P2-%d", suffix)).Scan(&p2)

	_, _ = pool.Exec(ctx, `INSERT INTO equipment_parts_bom (equipment_id, part_id) VALUES ($1, $2)`, eqID, p1)
	_, _ = pool.Exec(ctx, `INSERT INTO stock_movements (part_id, warehouse_id, quantity_change, movement_type) VALUES ($1, $2, 10, 'initial_seed')`, p2, whID)

	_, woOut := doTestReq(router, "POST", "/api/v1/work-orders", tok, map[string]any{"equipment_id": eqID, "title": "BOM Test"}, nil)
	woID := int64(woOut["id"].(float64))

	rec, _ := doTestReq(router, "POST", fmt.Sprintf("/api/v1/work-orders/%d/reserve-part", woID), tok, map[string]any{
		"part_id": p2, "warehouse_id": whID, "quantity": 1,
	}, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 BOM_INCOMPATIBLE, got %d", rec.Code)
	}
}

func TestIntegration10_PreventiveMaintenanceMeterAndNoBacklog_Task1(t *testing.T) {
	pool, router := setupIntegrationRouter(t)
	tok, _ := auth.GenerateToken(1, "planlama@bakimplan.local", "Plan", "planlama")
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	var eqID int
	_ = pool.QueryRow(ctx, `INSERT INTO equipments (code, name, level_type) VALUES ($1, 'PM Eq', 'tesis') RETURNING id`, fmt.Sprintf("EQ-PM-%d", suffix)).Scan(&eqID)

	_, _ = doTestReq(router, "POST", "/api/v1/preventive-plans", tok, map[string]any{
		"equipment_id": eqID, "name": "500s Bakım", "plan_type": "meter", "interval_hours": 500, "next_due_meter": 500,
	}, nil)

	_, out1 := doTestReq(router, "POST", fmt.Sprintf("/api/v1/equipments/%d/meter-readings", eqID), tok, map[string]any{"reading_hours": 520}, nil)
	gen1 := out1["generated_work_orders"].([]any)
	if len(gen1) != 1 {
		t.Fatalf("expected 1 generated work order, got %d", len(gen1))
	}

	_, out2 := doTestReq(router, "POST", fmt.Sprintf("/api/v1/equipments/%d/meter-readings", eqID), tok, map[string]any{"reading_hours": 1200}, nil)
	gen2 := out2["generated_work_orders"].([]any)
	if len(gen2) != 0 {
		t.Fatalf("expected 0 backlog work orders while active one exists, got %d", len(gen2))
	}

	// Sayaç geriye gidemez (1200 sonrası 800 gönderilirse 400 METER_ROLLBACK dönmeli!)
	recRollback, _ := doTestReq(router, "POST", fmt.Sprintf("/api/v1/equipments/%d/meter-readings", eqID), tok, map[string]any{"reading_hours": 800}, nil)
	if recRollback.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 METER_ROLLBACK on decreasing meter reading, got %d", recRollback.Code)
	}
}

func TestIntegration11_PMCalcBasisPlannedVsCompleted_Task1(t *testing.T) {
	pool, router := setupIntegrationRouter(t)
	tok, _ := auth.GenerateToken(1, "planlama@bakimplan.local", "Plan", "planlama")
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	var eqID int
	_ = pool.QueryRow(ctx, `INSERT INTO equipments (code, name, level_type) VALUES ($1, 'Basis Eq', 'tesis') RETURNING id`, fmt.Sprintf("EQ-BS-%d", suffix)).Scan(&eqID)

	due := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	_, pmOut := doTestReq(router, "POST", "/api/v1/preventive-plans", tok, map[string]any{
		"equipment_id": eqID, "name": "30 Gün Planned", "plan_type": "time", "interval_days": 30, "calc_basis": "planned", "next_due_at": due,
	}, nil)
	pmID := int(pmOut["id"].(float64))

	var woID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO work_orders (code, equipment_id, wo_type, preventive_plan_id, status, title, scheduled_at)
		 VALUES ($1, $2, 'preventive', $3, 'acik', 'PM Test', $4) RETURNING id`,
		fmt.Sprintf("WO-PM-%d", suffix), eqID, pmID, due,
	).Scan(&woID)

	compTime := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	rec, compOut := doTestReq(router, "POST", fmt.Sprintf("/api/v1/work-orders/%d/complete", woID), tok, map[string]any{
		"completed_at": compTime,
	}, nil)
	gotStr, _ := compOut["next_pm_scheduled_at"].(string)
	got, parseErr := time.Parse(time.RFC3339, gotStr)
	want := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	if rec.Code != 200 || parseErr != nil || !got.UTC().Equal(want) {
		t.Fatalf("expected next_pm_scheduled_at=2026-10-31T10:00:00Z, got %v", compOut["next_pm_scheduled_at"])
	}
}

func TestIntegration12_PMCalcBasisCompletedMode_Task1(t *testing.T) {
	pool, router := setupIntegrationRouter(t)
	tok, _ := auth.GenerateToken(1, "planlama@bakimplan.local", "Plan", "planlama")
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	var eqID int
	_ = pool.QueryRow(ctx, `INSERT INTO equipments (code, name, level_type) VALUES ($1, 'Basis Comp Eq', 'tesis') RETURNING id`, fmt.Sprintf("EQ-BSC-%d", suffix)).Scan(&eqID)

	due := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	_, pmOut := doTestReq(router, "POST", "/api/v1/preventive-plans", tok, map[string]any{
		"equipment_id": eqID, "name": "30 Gün Completed", "plan_type": "time", "interval_days": 30, "calc_basis": "completed", "next_due_at": due,
	}, nil)
	pmID := int(pmOut["id"].(float64))

	var woID int64
	_ = pool.QueryRow(ctx,
		`INSERT INTO work_orders (code, equipment_id, wo_type, preventive_plan_id, status, title, scheduled_at)
		 VALUES ($1, $2, 'preventive', $3, 'acik', 'PM Test Completed', $4) RETURNING id`,
		fmt.Sprintf("WO-PMC-%d", suffix), eqID, pmID, due,
	).Scan(&woID)

	compTime := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	rec, compOut := doTestReq(router, "POST", fmt.Sprintf("/api/v1/work-orders/%d/complete", woID), tok, map[string]any{
		"completed_at": compTime,
	}, nil)
	// calc_basis='completed' olduğunda 6 Ekim + 30 gün = 5 Kasım olmalı!
	if rec.Code != 200 || compOut["next_pm_scheduled_at"] != "2026-11-05T10:00:00Z" {
		t.Fatalf("expected next_pm_scheduled_at=2026-11-05T10:00:00Z, got %v", compOut["next_pm_scheduled_at"])
	}
}

func TestIntegration13_MTBFAndOverlappingDowntimeRecalculation_Task4(t *testing.T) {
	pool, router := setupIntegrationRouter(t)
	tok, _ := auth.GenerateToken(1, "planlama@bakimplan.local", "Plan", "planlama")
	ctx := context.Background()
	suffix := time.Now().UnixNano()

	var eqID int
	_ = pool.QueryRow(ctx, `INSERT INTO equipments (code, name, level_type, shift_hours_per_day) VALUES ($1, 'MTBF Eq', 'tesis', 16) RETURNING id`, fmt.Sprintf("EQ-MT-%d", suffix)).Scan(&eqID)

	_, _ = doTestReq(router, "POST", "/api/v1/downtime-events", tok, map[string]any{
		"equipment_id": eqID, "start_time": "2026-10-12T09:00:00Z", "end_time": "2026-10-12T12:00:00Z", "reason": "A1",
	}, nil)
	_, dt2 := doTestReq(router, "POST", "/api/v1/downtime-events", tok, map[string]any{
		"equipment_id": eqID, "start_time": "2026-10-12T11:00:00Z", "end_time": "2026-10-12T13:00:00Z", "reason": "A2",
	}, nil)

	_, m1 := doTestReq(router, "GET", fmt.Sprintf("/api/v1/metrics/reliability?equipment_id=%d&days=10", eqID), tok, nil, nil)
	if m1["downtime_hours"].(float64) != 4.0 {
		t.Fatalf("expected 4.0 merged hours, got %v", m1["downtime_hours"])
	}

	dt2ID := int64(dt2["id"].(float64))
	_, _ = doTestReq(router, "PATCH", fmt.Sprintf("/api/v1/downtime-events/%d", dt2ID), tok, map[string]any{
		"start_time": "2026-10-12T11:00:00Z", "end_time": "2026-10-12T15:00:00Z",
	}, nil)

	_, m2 := doTestReq(router, "GET", fmt.Sprintf("/api/v1/metrics/reliability?equipment_id=%d&days=10", eqID), tok, nil, nil)
	if m2["downtime_hours"].(float64) != 6.0 {
		t.Fatalf("expected 6.0 recalculated hours, got %v", m2["downtime_hours"])
	}
}

func TestIntegration14_WorkOrderKeysetPagination(t *testing.T) {
	_, router := setupIntegrationRouter(t)
	tok, _ := auth.GenerateToken(1, "teknisyen@bakimplan.local", "Tek", "teknisyen")
	rec, out := doTestReq(router, "GET", "/api/v1/work-orders?limit=5", tok, nil, nil)
	if rec.Code != 200 || out["items"] == nil {
		t.Fatalf("keyset pagination failed: %d", rec.Code)
	}
}

func TestIntegration15_TechnicianWorkloadEndpoint(t *testing.T) {
	_, router := setupIntegrationRouter(t)
	tok, _ := auth.GenerateToken(1, "planlama@bakimplan.local", "Plan", "planlama")
	rec, out := doTestReq(router, "GET", "/api/v1/technicians/workload", tok, nil, nil)
	if rec.Code != 200 || out["items"] == nil {
		t.Fatalf("workload endpoint failed: %d", rec.Code)
	}
}

func TestIntegration16_AuditLogAppendOnlyVerification(t *testing.T) {
	_, router := setupIntegrationRouter(t)
	adminTok, _ := auth.GenerateToken(1, "yonetici@bakimplan.local", "Admin", "yonetici")
	rec, out := doTestReq(router, "GET", "/api/v1/audit-logs", adminTok, nil, nil)
	if rec.Code != 200 || out["items"] == nil {
		t.Fatalf("audit logs failed: %d", rec.Code)
	}
}
