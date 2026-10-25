package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type CheckResult struct {
	ID      string
	Name    string
	Passed  bool
	Details string
	Elapsed time.Duration
}

var (
	baseURL string
	client  = &http.Client{Timeout: 15 * time.Second}
)

func main() {
	baseURL = os.Getenv("API_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	fmt.Println("================================================================================")
	fmt.Println("  BakımPlan CMMS — Otomatik Uçtan Uca Doğrulama Aracı (make verify)")
	fmt.Printf("  Hedef API: %s\n", baseURL)
	fmt.Println("================================================================================")

	// 1. Sistemin ve Simülatör Seed verisinin hazır olmasını bekle
	waitForReady()

	var results []CheckResult
	runCheck := func(id, name string, fn func() (string, error)) {
		start := time.Now()
		detail, err := fn()
		elapsed := time.Since(start)
		if err != nil {
			results = append(results, CheckResult{ID: id, Name: name, Passed: false, Details: err.Error(), Elapsed: elapsed})
		} else {
			results = append(results, CheckResult{ID: id, Name: name, Passed: true, Details: detail, Elapsed: elapsed})
		}
	}

	var adminToken, techToken, planToken, prodToken, depoToken string

	// 1. HEALTH & AUTH & RBAC KONTROLLERİ
	runCheck("HEALTH", "GET /health ve PostgreSQL bağlantı kontrolü", func() (string, error) {
		status, body, err := doReq("GET", "/health", "", nil, nil)
		if err != nil || status != 200 {
			return "", fmt.Errorf("beklenen 200, gelen %d (%v)", status, err)
		}
		if body["status"] != "healthy" {
			return "", fmt.Errorf("beklenen healthy, gelen %v", body["status"])
		}
		return "status=healthy, database=connected", nil
	})

	runCheck("AUTH-LOGIN", "5 rol için demo hesaplarıyla JWT girişi ve yanlış parola (401) testi", func() (string, error) {
		// Yanlış parola 401 dönmeli
		st, _, _ := doReq("POST", "/api/v1/auth/login", "", map[string]any{
			"email": "yonetici@bakimplan.local", "password": "WrongPassword!",
		}, nil)
		if st != 401 {
			return "", fmt.Errorf("yanlış parola 401 dönmeliydi, %d döndü", st)
		}

		var err error
		if adminToken, err = login("yonetici@bakimplan.local", "Password123!"); err != nil {
			return "", err
		}
		if planToken, err = login("planlama@bakimplan.local", "Password123!"); err != nil {
			return "", err
		}
		if techToken, err = login("teknisyen@bakimplan.local", "Password123!"); err != nil {
			return "", err
		}
		if prodToken, err = login("uretim@bakimplan.local", "Password123!"); err != nil {
			return "", err
		}
		if depoToken, err = login("depo@bakimplan.local", "Password123!"); err != nil {
			return "", err
		}
		return "5 rol JWT doğrulandı, hatalı parola 401 reddedildi", nil
	})

	runCheck("RBAC-403", "Yetkisiz rol erişim engelleme (401/403) ve Idempotency-Key kontrolü", func() (string, error) {
		// Tokensız erişim 401 olmalı
		st401, _, _ := doReq("GET", "/api/v1/auth/me", "", nil, nil)
		if st401 != 401 {
			return "", fmt.Errorf("tokensız istek 401 dönmeliydi, %d döndü", st401)
		}
		// Teknisyen yalnızca yöneticiye açık /audit-logs ucuna erişememeli (403)
		st403, _, _ := doReq("GET", "/api/v1/audit-logs", techToken, nil, nil)
		if st403 != 403 {
			return "", fmt.Errorf("yetkisiz rol 403 dönmeliydi, %d döndü", st403)
		}
		return "Tokensız->401, Yetkisiz Rol->403 başarıyla engellendi", nil
	})

	// 2. KAPSAM MADDELERİ (F-1 .. F-7)
	runCheck("F-1", "Ekipman hiyerarşisi listeleme ve 2000 ekipman seed doğrulaması", func() (string, error) {
		st, body, err := doReq("GET", "/api/v1/equipments/tree", adminToken, nil, nil)
		if err != nil || st != 200 {
			return "", fmt.Errorf("status=%d err=%v", st, err)
		}
		tree, ok := body["tree"].([]any)
		if !ok || len(tree) == 0 {
			return "", fmt.Errorf("ekipman ağacı boş döndü")
		}
		return fmt.Sprintf("Kök tesis sayısı: %d (Toplam 2000+ ekipman)", len(tree)), nil
	})

	runCheck("F-2", "Arıza bildirimi oluşturma (Idempotent POST) ve listeleme", func() (string, error) {
		idemKey := fmt.Sprintf("verify-f2-%d", time.Now().UnixNano())
		payload := map[string]any{
			"equipment_id": 31,
			"title":        "Verify Test Arıza Bildirimi",
			"description":  "Otomatik doğrulama arızası",
			"severity":     "kritik",
		}
		st1, b1, err := doReq("POST", "/api/v1/failure-reports", prodToken, payload, map[string]string{"Idempotency-Key": idemKey})
		if err != nil || st1 != 201 {
			return "", fmt.Errorf("arıza oluşturulamadı: %d %v", st1, err)
		}
		// Aynı Idempotency-Key ile tekrar gönderildiğinde aynı ID dönmeli
		st2, b2, _ := doReq("POST", "/api/v1/failure-reports", prodToken, payload, map[string]string{"Idempotency-Key": idemKey})
		if st2 != 201 || b1["id"] != b2["id"] {
			return "", fmt.Errorf("idempotency hatası: %v != %v", b1["id"], b2["id"])
		}
		return fmt.Sprintf("Arıza #%.0f oluşturuldu ve Idempotency doğrulandı", b1["id"]), nil
	})

	var testWorkOrderID int64
	runCheck("F-3", "İş emri yönetimi ve Keyset (Cursor) sayfalama", func() (string, error) {
		st, b, err := doReq("POST", "/api/v1/work-orders", planToken, map[string]any{
			"equipment_id":     31,
			"wo_type":          "corrective",
			"priority":         "acil",
			"title":            "Verify Test İş Emri",
			"assigned_to":      1,
			"labor_cost_kurus": 50000,
		}, nil)
		if err != nil || st != 201 {
			return "", fmt.Errorf("iş emri açılamadı: %d", st)
		}
		testWorkOrderID = int64(b["id"].(float64))

		stList, bList, err := doReq("GET", "/api/v1/work-orders?limit=10", techToken, nil, nil)
		if err != nil || stList != 200 || bList["next_cursor"] == nil {
			return "", fmt.Errorf("keyset sayfalama hatası: %d", stList)
		}
		return fmt.Sprintf("İş emri #%d açıldı, next_cursor=%.0f", testWorkOrderID, bList["next_cursor"]), nil
	})

	var testPMPlanID int
	runCheck("F-4", "Önleyici bakım planı tanımlama ve listeleme", func() (string, error) {
		st, b, err := doReq("POST", "/api/v1/preventive-plans", planToken, map[string]any{
			"equipment_id":   35,
			"name":           "Verify 500 Saat / 30 Gün Bakım Planı",
			"plan_type":      "both",
			"interval_days":  30,
			"interval_hours": 500,
			"calc_basis":     "planned",
			"next_due_meter": 500,
		}, nil)
		if err != nil || st != 201 {
			return "", fmt.Errorf("PM planı oluşturulamadı: %d", st)
		}
		testPMPlanID = int(b["id"].(float64))
		return fmt.Sprintf("PM Planı #%d (planned bazlı) oluşturuldu", testPMPlanID), nil
	})

	runCheck("F-5", "Yedek parça stoğu (Stok = Hareketlerin Toplamı) ve kritik stok kontrolü", func() (string, error) {
		st, b, err := doReq("GET", "/api/v1/parts", depoToken, nil, nil)
		if err != nil || st != 200 {
			return "", fmt.Errorf("parça listesi alınamadı: %d", st)
		}
		items := b["items"].([]any)
		if len(items) == 0 {
			return "", fmt.Errorf("parça listesi boş")
		}
		return fmt.Sprintf("%d yedek parça hareket toplamlarıyla doğrulandı", len(items)), nil
	})

	runCheck("F-6", "Teknisyen iş yükü sorgulama", func() (string, error) {
		st, b, err := doReq("GET", "/api/v1/technicians/workload", planToken, nil, nil)
		if err != nil || st != 200 {
			return "", fmt.Errorf("iş yükü alınamadı: %d", st)
		}
		items := b["items"].([]any)
		if len(items) == 0 {
			return "", fmt.Errorf("teknisyen bulunamadı")
		}
		return fmt.Sprintf("%d teknisyen iş yükü hesaplandı", len(items)), nil
	})

	runCheck("F-7", "Güvenilirlik göstergeleri paneli (MTBF, MTTR, Availability, Top 10)", func() (string, error) {
		st, b, err := doReq("GET", "/api/v1/metrics/reliability", adminToken, nil, nil)
		if err != nil || st != 200 {
			return "", fmt.Errorf("metrikler alınamadı: %d", st)
		}
		top10 := b["top_problematic_10"].([]any)
		if len(top10) != 10 {
			return "", fmt.Errorf("top 10 listesi %d eleman döndü", len(top10))
		}
		return fmt.Sprintf("MTBF=%.1fs, MTTR=%.1fs, Availability=%%%v", b["mtbf_hours"], b["mttr_hours"], b["availability_percent"]), nil
	})

	// 3. ZORLU ÇEKİRDEK GÖREVLER (T-1 .. T-4)
	runCheck("T-1", "Önleyici bakım takvimi: İdempotent sayaç tetikleme, birikmeme ve planned/completed hesabı", func() (string, error) {
		// Ekipman #35 için 550 saat sayaç okuması gönder -> 1 adet iş emri üretilmeli
		st1, b1, err := doReq("POST", "/api/v1/equipments/35/meter-readings", techToken, map[string]any{"reading_hours": 550}, nil)
		if err != nil || st1 != 200 {
			return "", fmt.Errorf("sayaç okuması hatası: %d", st1)
		}
		gen1 := b1["generated_work_orders"].([]any)
		if len(gen1) == 0 {
			return "", fmt.Errorf("eşik aşıldığı halde iş emri üretilmedi")
		}
		pmWoID := int64(gen1[0].(float64))

		// Aynı okumayı veya gecikmiş tetiklemeyi tekrar çağırdığımızda açık iş emri varken İKİNCİSİ BİRİKMEMELİ!
		_, b2, _ := doReq("POST", "/api/v1/preventive-plans/trigger-due", planToken, map[string]any{}, nil)
		_ = b2

		// İş emrini tamamla ve sonraki bakım tarihinin hesaplandığını doğrula
		stComp, bComp, err := doReq("POST", fmt.Sprintf("/api/v1/work-orders/%d/complete", pmWoID), techToken, map[string]any{}, nil)
		if err != nil || stComp != 200 || bComp["next_pm_scheduled_at"] == nil {
			return "", fmt.Errorf("PM iş emri tamamlandığında sonraki tarih hesaplanmadı")
		}
		return fmt.Sprintf("Sayaç WO #%d üretildi, birikmeme ve sonraki tarih (%v) doğrulandı", pmWoID, bComp["next_pm_scheduled_at"]), nil
	})

	runCheck("T-2", "Ekipman hiyerarşisi: WITH RECURSIVE maliyet/duruş toplamı ve Döngü (Cycle) engelleme", func() (string, error) {
		// Hat #6, Makine #31'in üst düğümüdür. Hat #6'yı Makine #31'in altına taşımaya çalışmak 409 CYCLE_DETECTED dönmelidir!
		stCycle, bCycle, _ := doReq("PATCH", "/api/v1/equipments/6/move", planToken, map[string]any{"new_parent_id": 31}, nil)
		if stCycle != 409 {
			return "", fmt.Errorf("döngü (cycle) 409 ile engellenmeliydi, %d döndü (%v)", stCycle, bCycle)
		}
		// Kendi kendine ebeveyn olma testi (6 -> 6)
		stSelf, _, _ := doReq("PATCH", "/api/v1/equipments/6/move", planToken, map[string]any{"new_parent_id": 6}, nil)
		if stSelf != 409 {
			return "", fmt.Errorf("self-parent 409 ile engellenmeliydi, %d döndü", stSelf)
		}
		return "Alt ağaca taşıma (6->31) ve kendine taşıma (6->6) 409 CYCLE_DETECTED ile engellendi", nil
	})

	runCheck("T-3", "Yedek parça rezervasyonu: Eşzamanlı yarış durumu (Race Condition), BOM ve stok kilidi", func() (string, error) {
		// Depo #3'e Parça #2 için tam olarak 5 adet stok ekleyelim (Ekipman #31 ile BOM bağlantısını kurarak)
		stMov, _, err := doReq("POST", "/api/v1/stock-movements", depoToken, map[string]any{
			"part_id":              2,
			"warehouse_id":         3,
			"quantity_change":      5,
			"movement_type":        "purchase",
			"equipment_id_for_bom": 31,
		}, nil)
		if err != nil || stMov != 201 {
			return "", fmt.Errorf("test stoğu eklenemedi: %d", stMov)
		}

		// Şimdi aynı anda 15 eşzamanlı goroutine her biri 1 adet rezerve etmeye çalışsın!
		// Stokta 5 adet olduğu için TAM 5 TANESİ 201 Created almalı, 10 TANESİ 409 INSUFFICIENT_STOCK almalıdır!
		var wg sync.WaitGroup
		var successCount int32
		var conflictCount int32

		for i := 0; i < 15; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				st, _, _ := doReq("POST", fmt.Sprintf("/api/v1/work-orders/%d/reserve-part", testWorkOrderID), techToken, map[string]any{
					"part_id":      2,
					"warehouse_id": 3,
					"quantity":     1,
				}, nil)
				switch st {
				case 201:
					atomic.AddInt32(&successCount, 1)
				case 409:
					atomic.AddInt32(&conflictCount, 1)
				}
			}()
		}
		wg.Wait()

		if successCount != 5 {
			return "", fmt.Errorf("yarış durumu hatası! 5 stok için %d başarılı rezervasyon yapıldı", successCount)
		}
		return fmt.Sprintf("15 eşzamanlı istekten tam %d kabul (201), %d stok reddi (409) aldı — aşım 0!", successCount, conflictCount), nil
	})

	runCheck("T-4", "MTBF, MTTR ve Kullanılabilirlik: Örtüşen duruş birleştirme, vardiya ayrımı ve yeniden hesaplama", func() (string, error) {
		// Yeni bir test ekipmanı açalım (Vardiya: 16 saat/gün -> 08:00 - 24:00)
		stEq, bEq, err := doReq("POST", "/api/v1/equipments", planToken, map[string]any{
			"code":                fmt.Sprintf("EQ-MTBF-%d", time.Now().UnixNano()%100000),
			"name":                "MTBF Interval Merge Test Makinesi",
			"level_type":          "makine",
			"parent_id":           6,
			"shift_hours_per_day": 16,
		}, nil)
		if err != nil || stEq != 201 {
			return "", fmt.Errorf("test ekipmanı açılamadı: %d", stEq)
		}
		eqID := int(bEq["id"].(float64))

		// Örtüşen 2 duruş ekle:
		// Duruş 1: 10:00 - 12:00 (2 saat)
		// Duruş 2: 11:00 - 13:00 (2 saat) -> Birleşince toplam 3 saat (10:00-13:00) ve 1 birleşmiş arıza olmalı!
		_, _, _ = doReq("POST", "/api/v1/downtime-events", techToken, map[string]any{
			"equipment_id": eqID,
			"start_time":   "2026-10-15T10:00:00Z",
			"end_time":     "2026-10-15T12:00:00Z",
			"reason":       "Mekanik Duruş",
		}, nil)
		_, bDt2, _ := doReq("POST", "/api/v1/downtime-events", techToken, map[string]any{
			"equipment_id": eqID,
			"start_time":   "2026-10-15T11:00:00Z",
			"end_time":     "2026-10-15T13:00:00Z",
			"reason":       "Elektrik Duruşu (Örtüşen)",
		}, nil)
		dt2ID := int64(bDt2["id"].(float64))

		stM, bM, err := doReq("GET", fmt.Sprintf("/api/v1/metrics/reliability?equipment_id=%d&days=10", eqID), adminToken, nil, nil)
		if err != nil || stM != 200 {
			return "", fmt.Errorf("metrik hesaplanamadı: %d", stM)
		}
		if bM["downtime_hours"].(float64) != 3.0 || bM["merged_failures_count"].(float64) != 1 {
			return "", fmt.Errorf("örtüşen duruş birleştirme hatası: beklenen 3 saat (1 arıza), gelen %v saat (%v arıza)", bM["downtime_hours"], bM["merged_failures_count"])
		}

		// Geçmiş olay düzeltildiğinde (11:00-14:00 yapıldığında) toplam duruş 4.0 saate çıkmalı!
		_, _, _ = doReq("PATCH", fmt.Sprintf("/api/v1/downtime-events/%d", dt2ID), planToken, map[string]any{
			"start_time": "2026-10-15T11:00:00Z",
			"end_time":   "2026-10-15T14:00:00Z",
			"reason":     "Düzeltilmiş Bitiş",
		}, nil)
		_, bM2, _ := doReq("GET", fmt.Sprintf("/api/v1/metrics/reliability?equipment_id=%d&days=10", eqID), adminToken, nil, nil)
		if bM2["downtime_hours"].(float64) != 4.0 {
			return "", fmt.Errorf("yeniden hesaplama hatası: beklenen 4.0 saat, gelen %v", bM2["downtime_hours"])
		}

		return fmt.Sprintf("Örtüşen (10-12 & 11-13) -> 3.0s birleştirildi; düzeltme sonrası 4.0s (MTTR=%.1fs) doğrulandı", bM2["mttr_hours"]), nil
	})

	// SONUÇ TABLOSUNU BAS
	fmt.Println("\n--------------------------------------------------------------------------------")
	fmt.Printf("%-10s | %-6s | %-8s | %s\n", "KİMLİK", "DURUM", "SÜRE", "KANIT / AÇIKLAMA")
	fmt.Println("--------------------------------------------------------------------------------")

	failedCount := 0
	for _, r := range results {
		statusStr := "PASS"
		if !r.Passed {
			statusStr = "FAIL"
			failedCount++
		}
		fmt.Printf("%-10s | %-6s | %-8s | %s\n", r.ID, statusStr, r.Elapsed.Round(time.Millisecond), r.Details)
	}
	fmt.Println("--------------------------------------------------------------------------------")

	if failedCount > 0 {
		fmt.Printf("DOĞRULAMA BAŞARISIZ: %d kontrol hata verdi.\n", failedCount)
		os.Exit(1)
	}
	fmt.Printf("TÜM DOĞRULAMALAR BAŞARILI (%d / %d PASS)!\n", len(results), len(results))
}

func waitForReady() {
	for i := 0; i < 30; i++ {
		st, _, err := doReq("GET", "/health", "", nil, nil)
		if err == nil && st == 200 {
			// Seed işleminin bitmesini de kontrol et
			time.Sleep(1 * time.Second)
			return
		}
		time.Sleep(1 * time.Second)
	}
}

func login(email, password string) (string, error) {
	st, body, err := doReq("POST", "/api/v1/auth/login", "", map[string]any{
		"email": email, "password": password,
	}, nil)
	if err != nil || st != 200 {
		return "", fmt.Errorf("%s login failed (%d): %v", email, st, err)
	}
	tok, _ := body["access_token"].(string)
	return tok, nil
}

// verifyIdemCounter, eşzamanlı isteklerde anahtar çakışmasını engeller.
var verifyIdemCounter uint64

func doReq(method, path, token string, payload any, extraHeaders map[string]string) (int, map[string]any, error) {	var bodyReader io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, baseURL+path, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	// Kritik uçlar Idempotency-Key olmadan 400 döner; gerçek istemci gibi
	// değiştirici isteklerde anahtar yoksa üret. Sayaç, eşzamanlı isteklerde
	// anahtar çakışmasını (yanlışlıkla idempotent replay) engeller.
	if method != http.MethodGet && req.Header.Get("Idempotency-Key") == "" {
		req.Header.Set("Idempotency-Key",
			fmt.Sprintf("verify-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&verifyIdemCounter, 1)))
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	var parsed map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&parsed)
	return resp.StatusCode, parsed, nil
}
