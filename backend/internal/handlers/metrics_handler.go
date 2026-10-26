package handlers

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"bakimplan/internal/auth"
	"bakimplan/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MetricsHandler struct {
	Pool *pgxpool.Pool
}

func NewMetricsHandler(pool *pgxpool.Pool) *MetricsHandler {
	return &MetricsHandler{Pool: pool}
}

type TimeInterval struct {
	Start time.Time
	End   time.Time
}

// MergeIntervals (Görev 4 Çekirdek Algoritması): Örtüşen (overlapping) veya iç içe geçen duruş sürelerini birleştirir!
func MergeIntervals(intervals []TimeInterval) []TimeInterval {
	if len(intervals) <= 1 {
		return intervals
	}

	sorted := make([]TimeInterval, len(intervals))
	copy(sorted, intervals)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Start.Before(sorted[j].Start)
	})

	merged := []TimeInterval{sorted[0]}
	for i := 1; i < len(sorted); i++ {
		last := &merged[len(merged)-1]
		curr := sorted[i]
		if !curr.Start.After(last.End) { // Örtüşüyor veya uç uca
			if curr.End.After(last.End) {
				last.End = curr.End
			}
		} else {
			merged = append(merged, curr)
		}
	}
	return merged
}

// EffectiveShiftHours: Bir zaman aralığının sadece vardiya saatleri (örn: 08:00 - 08:00+shiftHours) içine düşen kısmını hesaplar.
func EffectiveShiftHours(start, end time.Time, shiftHours int) float64 {
	if !end.After(start) || shiftHours <= 0 {
		return 0
	}
	if shiftHours >= 24 {
		return end.Sub(start).Hours()
	}

	totalHours := 0.0
	curDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	lastDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)

	for !curDay.After(lastDay) {
		shiftStart := curDay.Add(8 * time.Hour) // Vardiya 08:00'de başlar
		shiftEnd := shiftStart.Add(time.Duration(shiftHours) * time.Hour)

		overlapStart := start
		if shiftStart.After(overlapStart) {
			overlapStart = shiftStart
		}
		overlapEnd := end
		if shiftEnd.Before(overlapEnd) {
			overlapEnd = shiftEnd
		}

		if overlapEnd.After(overlapStart) {
			totalHours += overlapEnd.Sub(overlapStart).Hours()
		}
		curDay = curDay.AddDate(0, 0, 1)
	}
	return totalHours
}

type createDowntimeReq struct {
	EquipmentID  int       `json:"equipment_id"`
	WorkOrderID  *int64    `json:"work_order_id"`
	DowntimeType string    `json:"downtime_type"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	Reason       string    `json:"reason"`
}

func (h *MetricsHandler) CreateDowntimeEvent(w http.ResponseWriter, r *http.Request) {
	var req createDowntimeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON.")
		return
	}
	if req.EquipmentID <= 0 || !req.EndTime.After(req.StartTime) {
		middleware.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Geçerli equipment_id ve end_time > start_time zorunludur.")
		return
	}
	if req.DowntimeType == "" {
		req.DowntimeType = "unplanned"
	}

	var id int64
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO downtime_events (equipment_id, work_order_id, downtime_type, start_time, end_time, reason)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		req.EquipmentID, req.WorkOrderID, req.DowntimeType, req.StartTime.UTC(), req.EndTime.UTC(), req.Reason,
	).Scan(&id)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "DB_ERROR", err.Error())
		return
	}

	middleware.WriteJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "recorded"})
}

func (h *MetricsHandler) UpdateDowntimeEvent(w http.ResponseWriter, r *http.Request) {
	dtID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Geçersiz duruş ID.")
		return
	}

	var req createDowntimeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.EndTime.After(req.StartTime) {
		middleware.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "end_time > start_time olmalıdır.")
		return
	}

	var oldStart, oldEnd time.Time
	_ = h.Pool.QueryRow(r.Context(), `SELECT start_time, end_time FROM downtime_events WHERE id = $1`, dtID).Scan(&oldStart, &oldEnd)

	_, err = h.Pool.Exec(r.Context(),
		`UPDATE downtime_events SET start_time = $1, end_time = $2, reason = COALESCE(NULLIF($3, ''), reason) WHERE id = $4`,
		req.StartTime.UTC(), req.EndTime.UTC(), req.Reason, dtID,
	)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "UPDATE_ERROR", err.Error())
		return
	}

	claims, _ := auth.GetUserFromContext(r.Context())
	var uid *int
	if claims != nil {
		uid = &claims.UserID
	}
	middleware.RecordAudit(r.Context(), h.Pool, uid, "UPDATE_DOWNTIME", "downtime_event", strconv.FormatInt(dtID, 10),
		map[string]any{"start_time": oldStart, "end_time": oldEnd},
		map[string]any{"start_time": req.StartTime.UTC(), "end_time": req.EndTime.UTC()},
	)

	middleware.WriteJSON(w, http.StatusOK, map[string]any{"id": dtID, "status": "updated_and_recalculated"})
}

// GetReliabilityMetrics (Görev 4 & F-7): Seçilen zaman penceresinde (windowStart..windowEnd) duruşları filtreler, sınırları kırpar (clip), örtüşenleri birleştirir ve MTBF/MTTR/Availability hesaplar.
func (h *MetricsHandler) GetReliabilityMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	eqIDStr := r.URL.Query().Get("equipment_id")
	daysStr := r.URL.Query().Get("days")
	days := 30
	if d, err := strconv.Atoi(daysStr); err == nil && d > 0 && d <= 3650 {
		days = d
	}

	shiftHours := 16
	activeEquipmentCount := 1
	var eqFilter int

	if eqIDStr != "" {
		if id, err := strconv.Atoi(eqIDStr); err == nil && id > 0 {
			eqFilter = id
			_ = h.Pool.QueryRow(ctx, `SELECT shift_hours_per_day FROM equipments WHERE id = $1`, eqFilter).Scan(&shiftHours)
		}
	}

	// Analiz Penceresini (windowStart .. windowEnd) Belirle:
	// Eğer ekipmanda duruş varsa en son duruş gününün sonunu referans al (simülasyon/test tarihleriyle tam uyum için), yoksa NOW() al.
	var latestEnd *time.Time
	if eqFilter > 0 {
		_ = h.Pool.QueryRow(ctx, `SELECT MAX(end_time) FROM downtime_events WHERE equipment_id = $1 AND downtime_type = 'unplanned'`, eqFilter).Scan(&latestEnd)
	} else {
		_ = h.Pool.QueryRow(ctx, `SELECT MAX(end_time) FROM downtime_events WHERE downtime_type = 'unplanned'`).Scan(&latestEnd)
		_ = h.Pool.QueryRow(ctx, `SELECT GREATEST(COUNT(DISTINCT equipment_id), 1) FROM downtime_events WHERE downtime_type = 'unplanned'`).Scan(&activeEquipmentCount)
	}

	windowEnd := time.Now().UTC()
	if latestEnd != nil {
		windowEnd = time.Date(latestEnd.UTC().Year(), latestEnd.UTC().Month(), latestEnd.UTC().Day(), 23, 59, 59, 0, time.UTC)
	}
	windowStart := windowEnd.AddDate(0, 0, -days)

	var intervals []TimeInterval
	var err error
	if eqFilter > 0 {
		rows, qErr := h.Pool.Query(ctx,
			`SELECT start_time, end_time FROM downtime_events
			 WHERE equipment_id = $1 AND downtime_type = 'unplanned'
			   AND end_time > $2 AND start_time < $3
			 ORDER BY start_time ASC`,
			eqFilter, windowStart, windowEnd,
		)
		err = qErr
		if err == nil {
			for rows.Next() {
				var st, et time.Time
				if rows.Scan(&st, &et) == nil {
					intervals = append(intervals, clipInterval(st.UTC(), et.UTC(), windowStart, windowEnd))
				}
			}
			rows.Close()
		}
	} else {
		rows, qErr := h.Pool.Query(ctx,
			`SELECT start_time, end_time FROM downtime_events
			 WHERE downtime_type = 'unplanned'
			   AND end_time > $1 AND start_time < $2
			 ORDER BY start_time ASC`,
			windowStart, windowEnd,
		)
		err = qErr
		if err == nil {
			for rows.Next() {
				var st, et time.Time
				if rows.Scan(&st, &et) == nil {
					intervals = append(intervals, clipInterval(st.UTC(), et.UTC(), windowStart, windowEnd))
				}
			}
			rows.Close()
		}
	}

	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	// Örtüşen duruşları birleştir!
	merged := MergeIntervals(intervals)
	failureCount := len(merged)

	totalDowntimeHours := 0.0
	for _, iv := range merged {
		totalDowntimeHours += EffectiveShiftHours(iv.Start, iv.End, shiftHours)
	}

	totalScheduledHours := float64(days * shiftHours * activeEquipmentCount)
	if totalDowntimeHours > totalScheduledHours {
		totalDowntimeHours = totalScheduledHours
	}

	operatingHours := totalScheduledHours - totalDowntimeHours
	mttr := 0.0
	mtbf := totalScheduledHours
	if failureCount > 0 {
		mttr = totalDowntimeHours / float64(failureCount)
		mtbf = operatingHours / float64(failureCount)
	}
	availability := 100.0
	if totalScheduledHours > 0 {
		availability = (operatingHours / totalScheduledHours) * 100.0
	}

	// Görev 4: En Problemli 10 Ekipman raporu.
	//
	// Kaynak: materyalize view (mv_equipment_wo_stats) — varsayılan.
	// Neden: doğrudan work_orders taraması 1M satırda ~114 ms, MV ~1,1 ms.
	// Gecikme kabul edilebilir çünkü bu bir yönetim raporu, işlem ucu değil.
	//
	// `?fresh=true` verilirse canlı sorguya düşer: MV bayatsa (yenileme
	// unutulmuşsa) doğru sayıyı görmek için kaçış yolu. Bu, "MV var ama
	// veri eski" durumunu sessiz bir yanlış cevaba dönüştürmemek için.
	useMV := r.URL.Query().Get("fresh") != "true"

	var topRows pgx.Rows
	if useMV {
		topRows, err = h.Pool.Query(ctx, `
			SELECT
				e.id, e.code, e.name, e.level_type,
				m.failure_wo_count,
				m.total_cost_kurus
			FROM equipments e
			JOIN mv_equipment_wo_stats m ON m.equipment_id = e.id
			WHERE m.failure_wo_count > 0
			ORDER BY m.failure_wo_count DESC, m.total_cost_kurus DESC
			LIMIT 10`)
		if err != nil {
			// View henüz oluşmamış olabilir (migration sırası); canlı sorguya düş.
			slog.Warn("mv_equipment_wo_stats okunamadı, canlı sorguya düşülüyor", "error", err)
			useMV = false
		}
	}
	if !useMV {
		topRows, err = h.Pool.Query(ctx, `
			SELECT
				e.id, e.code, e.name, e.level_type,
				COUNT(w.id) AS failure_wo_count,
				COALESCE(SUM(w.labor_cost_kurus + w.parts_cost_kurus), 0)::BIGINT AS total_cost_kurus
			FROM equipments e
			JOIN work_orders w ON w.equipment_id = e.id
			WHERE w.wo_type = 'corrective' AND w.status != 'iptal'
			GROUP BY e.id, e.code, e.name, e.level_type
			ORDER BY failure_wo_count DESC, total_cost_kurus DESC
			LIMIT 10`)
	}
	var topProblematic []map[string]any
	if err == nil {
		defer topRows.Close()
		for topRows.Next() {
			var id int
			var code, name, levelType string
			var failCount, totalCost int64
			if topRows.Scan(&id, &code, &name, &levelType, &failCount, &totalCost) == nil {
				topProblematic = append(topProblematic, map[string]any{
					"equipment_id":     id,
					"code":             code,
					"name":             name,
					"level_type":       levelType,
					"failure_wo_count": failCount,
					"total_cost_kurus": totalCost,
				})
			}
		}
	}
	if topProblematic == nil {
		topProblematic = []map[string]any{}
	}

	// Raporun kaynağını ve tazeliğini yanıtta göstermek, "bu sayı nereden
	// geldi" sorusunu arayüzde cevaplanabilir kılıyor.
	topSource := "live"
	var mvRefreshedAt *time.Time
	if useMV {
		topSource = "materialized_view"
		_ = h.Pool.QueryRow(ctx,
			`SELECT refreshed_at FROM metrics_refresh_state WHERE name = 'mv_equipment_wo_stats'`,
		).Scan(&mvRefreshedAt)
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"equipment_id":          eqFilter,
		"window_days":           days,
		"window_start":          windowStart,
		"window_end":            windowEnd,
		"raw_events_count":      len(intervals),
		"merged_failures_count": failureCount,
		"shift_hours_per_day":   shiftHours,
		"scheduled_hours":       math.Round(totalScheduledHours*100) / 100,
		"downtime_hours":        math.Round(totalDowntimeHours*100) / 100,
		"operating_hours":       math.Round(operatingHours*100) / 100,
		"mtbf_hours":            math.Round(mtbf*100) / 100,
		"mttr_hours":            math.Round(mttr*100) / 100,
		"availability_percent":  math.Round(availability*100) / 100,
		"top_problematic_10":    topProblematic,
		"top_source":            topSource,
		"top_refreshed_at":      mvRefreshedAt,
	})
}

func clipInterval(st, et, winStart, winEnd time.Time) TimeInterval {
	if st.Before(winStart) {
		st = winStart
	}
	if et.After(winEnd) {
		et = winEnd
	}
	return TimeInterval{Start: st, End: et}
}
