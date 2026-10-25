package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"bakimplan/internal/database"
	"bakimplan/internal/handlers"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://cmms_user:cmms_secret_pass@localhost:5432/bakimplan_db?sslmode=disable"
	}

	seedVal := int64(42)
	if s := os.Getenv("SEED_VALUE"); s != "" {
		if parsed, err := strconv.ParseInt(s, 10, 64); err == nil {
			seedVal = parsed
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := database.ConnectAndMigrate(ctx, dbURL)
	if err != nil {
		slog.Error("simulator failed to connect to db", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := handlers.SeedDefaultUsers(ctx, pool); err != nil {
		slog.Error("simulator failed to seed users", "error", err)
		os.Exit(1)
	}

	if err := runDeterministicSeed(ctx, pool, seedVal); err != nil {
		slog.Error("simulator seed failed", "error", err)
		os.Exit(1)
	}

	// Eğer tek seferlik moddaysa çık; aksi halde konteynerin 'Exited' görünmemesi için deterministik veriyi bozmadan sinyal bekle
	if os.Getenv("RUN_ONCE") == "true" {
		slog.Info("simulator completed in RUN_ONCE mode")
		return
	}

	slog.Info("deterministic seed verified and locked; simulator service standing by without mutating seeded state", "seed", seedVal)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	slog.Info("simulator shutting down gracefully")
}

func runDeterministicSeed(ctx context.Context, pool *pgxpool.Pool, seed int64) error {
	var existingCount int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM equipments`).Scan(&existingCount)
	if existingCount >= 2000 {
		slog.Info("database already seeded with 2000+ equipments, preserving deterministic state", "count", existingCount)
		return nil
	}

	start := time.Now()
	slog.Info("starting 100% deterministic CMMS data generation...", "seed", seed)

	rng := rand.New(rand.NewSource(seed))
	// Sabit referans tarihi: Aynı seed her çalıştığında saniyesine kadar birebir aynı tarihleri üretir!
	baseTime := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// 1. 2.000 Ekipman Üretimi (4 Seviyeli Hiyerarşi: Tesis -> Hat -> Makine -> Bileşen)
	// 1..5: Tesis (5 adet)
	// 6..30: Hat (25 adet)
	// 31..300: Makine (270 adet)
	// 301..2000: Bileşen (1700 adet) -> Toplam = 2000 Ekipman
	var eqRows [][]any
	for id := 1; id <= 2000; id++ {
		var code, name, levelType string
		var parentID any
		shiftHours := 16
		isCritical := false
		status := "aktif"

		if id <= 5 {
			levelType = "tesis"
			code = fmt.Sprintf("TES-%02d", id)
			name = fmt.Sprintf("Üretim Tesisi %d (Bursa Fabrika-%d)", id, id)
			parentID = nil
			isCritical = true
		} else if id <= 30 {
			levelType = "hat"
			p := ((id - 6) % 5) + 1
			parentID = p
			code = fmt.Sprintf("HAT-%03d", id)
			name = fmt.Sprintf("Montaj ve İşleme Hattı %d (Tesis-%d)", id, p)
			isCritical = (id%3 == 0)
		} else if id <= 300 {
			levelType = "makine"
			p := ((id - 31) % 25) + 6
			parentID = p
			code = fmt.Sprintf("MAK-%04d", id)
			name = fmt.Sprintf("CNC / Pres Makinesi #%d", id)
			isCritical = (rng.Intn(4) == 0)
			if rng.Intn(3) == 0 {
				shiftHours = 24
			}
			if id == 31 || id == 32 {
				status = "bakimda"
			}
		} else {
			levelType = "bilesen"
			p := ((id - 301) % 270) + 31
			parentID = p
			code = fmt.Sprintf("BIL-%04d", id)
			name = fmt.Sprintf("Hidrolik / Servo Bileşen #%d", id)
			isCritical = (rng.Intn(10) == 0)
		}

		eqRows = append(eqRows, []any{
			code, name, levelType, parentID, shiftHours, isCritical, status, baseTime.AddDate(-3, 0, 0),
		})
	}

	_, err = tx.CopyFrom(
		ctx,
		pgx.Identifier{"equipments"},
		[]string{"code", "name", "level_type", "parent_id", "shift_hours_per_day", "is_critical", "status", "created_at"},
		pgx.CopyFromRows(eqRows),
	)
	if err != nil {
		return fmt.Errorf("copy equipments: %w", err)
	}

	// 2. Depolar (3 Farklı Depo)
	warehouses := []struct{ Code, Name string }{
		{"DEPO-ANA", "Merkez Yedek Parça Deposu"},
		{"DEPO-HAT", "Üretim Hatları Acil Stok Deposu"},
		{"DEPO-ELK", "Elektronik ve Otomasyon Deposu"},
	}
	for _, w := range warehouses {
		_, err := tx.Exec(ctx, `INSERT INTO warehouses (code, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`, w.Code, w.Name)
		if err != nil {
			return err
		}
	}

	// 3. Yedek Parçalar (50 Parça) ve Deterministik Stok Hareketleri
	var partRows [][]any
	for i := 1; i <= 50; i++ {
		sku := fmt.Sprintf("PRT-%04d", i)
		name := fmt.Sprintf("Endüstriyel Rulman / Sensör Modülü Tip-%d", i)
		costKurus := int64((rng.Intn(45) + 5) * 10000)
		reorder := 5 + rng.Intn(10)
		isCrit := (i <= 10)
		partRows = append(partRows, []any{sku, name, costKurus, reorder, isCrit, baseTime.AddDate(-3, 0, 0)})
	}
	_, err = tx.CopyFrom(
		ctx,
		pgx.Identifier{"parts"},
		[]string{"sku", "name", "unit_cost_kurus", "reorder_point", "is_critical", "created_at"},
		pgx.CopyFromRows(partRows),
	)
	if err != nil {
		return fmt.Errorf("copy parts: %w", err)
	}

	var bomRows [][]any
	var stockRows [][]any
	for pID := 1; pID <= 50; pID++ {
		bomRows = append(bomRows, []any{pID + 30, pID})
		bomRows = append(bomRows, []any{((pID % 5) + 1), pID})

		initialQty := 40 + rng.Intn(60)
		if pID == 1 {
			initialQty = 3 // Kritik stok uyarısı örneği
		}
		stockRows = append(stockRows, []any{pID, 1, initialQty, "initial_seed", baseTime.AddDate(-3, 0, 0)})
		stockRows = append(stockRows, []any{pID, 2, 15, "initial_seed", baseTime.AddDate(-2, 0, 0)})
	}

	_, _ = tx.CopyFrom(ctx, pgx.Identifier{"equipment_parts_bom"}, []string{"equipment_id", "part_id"}, pgx.CopyFromRows(bomRows))
	_, err = tx.CopyFrom(ctx, pgx.Identifier{"stock_movements"}, []string{"part_id", "warehouse_id", "quantity_change", "movement_type", "created_at"}, pgx.CopyFromRows(stockRows))
	if err != nil {
		return fmt.Errorf("copy stock movements: %w", err)
	}

	// 4. Önleyici Bakım Planları (100 Plan: Süre ve Sayaç Bazlı)
	var pmRows [][]any
	for i := 1; i <= 100; i++ {
		eqID := 30 + i
		planType := "both"
		intDays := 30
		intHours := 500
		calcBasis := "completed"
		if i%2 == 0 {
			calcBasis = "planned"
		}
		nextDue := baseTime.AddDate(0, 0, (i%20)-5)
		nextMeter := 500
		pmRows = append(pmRows, []any{
			eqID, fmt.Sprintf("Periyodik 500s / 30g Bakım Planı #%d", i),
			planType, intDays, intHours, calcBasis, nextDue, 0, nextMeter, true, baseTime.AddDate(-2, 0, 0),
		})
	}
	_, err = tx.CopyFrom(
		ctx,
		pgx.Identifier{"preventive_plans"},
		[]string{"equipment_id", "name", "plan_type", "interval_days", "interval_hours", "calc_basis", "next_due_at", "last_trigger_meter", "next_due_meter", "is_active", "created_at"},
		pgx.CopyFromRows(pmRows),
	)
	if err != nil {
		return fmt.Errorf("copy pm plans: %w", err)
	}

	// 5. 3 Yıllık Arıza Bildirimleri, İş Emirleri (6.000 Adet), Duruş Olayları ve Sayaç Okumaları
	var woRows [][]any
	var dtRows [][]any
	var meterRows [][]any
	var failRows [][]any

	priorities := []string{"dusuk", "normal", "yuksek", "acil"}
	severities := []string{"dusuk", "orta", "yuksek", "kritik"}

	for i := 1; i <= 6000; i++ {
		var eqID int
		if i%5 == 0 {
			eqID = 31 + (i % 10)
		} else {
			eqID = 31 + rng.Intn(1969)
		}

		daysAgo := rng.Intn(365 * 3)
		hourOffset := 8 + rng.Intn(10)
		createdAt := baseTime.AddDate(0, 0, -daysAgo).Add(time.Duration(hourOffset) * time.Hour)

		woType := "corrective"
		if i%4 == 0 {
			woType = "preventive"
		}

		status := "tamamlandi"
		var completedAt any = createdAt.Add(time.Duration(2+rng.Intn(6)) * time.Hour)
		if i > 5950 {
			status = "atandi"
			completedAt = nil
		}

		laborCost := int64((rng.Intn(20) + 5) * 5000)
		partsCost := int64((rng.Intn(30) + 5) * 10000)
		prio := priorities[rng.Intn(len(priorities))]

		woRows = append(woRows, []any{
			fmt.Sprintf("WO-HIST-%06d", i),
			eqID,
			woType,
			1,
			status,
			prio,
			fmt.Sprintf("%s Bakım / Onarım İş Emri #%d", woType, i),
			laborCost,
			partsCost,
			createdAt,
			createdAt,
			completedAt,
			createdAt,
		})

		if woType == "corrective" && i <= 3000 {
			durHours := 1 + rng.Intn(5)
			dtStart := createdAt
			dtEnd := dtStart.Add(time.Duration(durHours) * time.Hour)
			dtRows = append(dtRows, []any{
				eqID, "unplanned", dtStart, dtEnd, fmt.Sprintf("Arıza duruşu #%d", i), dtStart,
			})
		}

		if i <= 500 {
			failRows = append(failRows, []any{
				eqID, 3, fmt.Sprintf("Anormal titreşim ve ısınma bildirimi #%d", i),
				"Üretim vardiyasında tespit edilen performans düşüşü.",
				severities[rng.Intn(len(severities))], "is_emrine_donustu", createdAt,
			})
		}
	}

	for m := 1; m <= 2000; m++ {
		eqID := 31 + (m % 200)
		reading := ((m-1)/200 + 1) * 40 // Maksimum 400 saate kadar çıkar (500 saat eşiğini testlere bırakır)
		recTime := baseTime.AddDate(0, 0, -(2000-m)/2)
		meterRows = append(meterRows, []any{eqID, reading, 1, recTime})
	}

	_, err = tx.CopyFrom(
		ctx,
		pgx.Identifier{"work_orders"},
		[]string{"code", "equipment_id", "wo_type", "assigned_to", "status", "priority", "title", "labor_cost_kurus", "parts_cost_kurus", "scheduled_at", "started_at", "completed_at", "created_at"},
		pgx.CopyFromRows(woRows),
	)
	if err != nil {
		return fmt.Errorf("copy work orders: %w", err)
	}

	_, err = tx.CopyFrom(
		ctx,
		pgx.Identifier{"downtime_events"},
		[]string{"equipment_id", "downtime_type", "start_time", "end_time", "reason", "created_at"},
		pgx.CopyFromRows(dtRows),
	)
	if err != nil {
		return fmt.Errorf("copy downtime events: %w", err)
	}

	_, err = tx.CopyFrom(
		ctx,
		pgx.Identifier{"failure_reports"},
		[]string{"equipment_id", "reported_by", "title", "description", "severity", "status", "created_at"},
		pgx.CopyFromRows(failRows),
	)
	if err != nil {
		return fmt.Errorf("copy failure reports: %w", err)
	}

	_, err = tx.CopyFrom(
		ctx,
		pgx.Identifier{"meter_readings"},
		[]string{"equipment_id", "reading_hours", "recorded_by", "recorded_at"},
		pgx.CopyFromRows(meterRows),
	)
	if err != nil {
		return fmt.Errorf("copy meter readings: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	slog.Info("deterministic seed completed!",
		"equipments", len(eqRows),
		"work_orders", len(woRows),
		"downtime_events", len(dtRows),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}
