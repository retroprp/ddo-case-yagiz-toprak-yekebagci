package handlers

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"bakimplan/internal/middleware"

	"github.com/go-chi/chi/v5"
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

	_, err = h.Pool.Exec(r.Context(),
		`UPDATE downtime_events SET start_time = $1, end_time = $2, reason = COALESCE(NULLIF($3, ''), reason) WHERE id = $4`,
		req.StartTime.UTC(), req.EndTime.UTC(), req.Reason, dtID,
	)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "UPDATE_ERROR", err.Error())
		return
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{"id": dtID, "status": "updated_and_recalculated"})
}

// GetReliabilityMetrics (Görev 4 & F-7): MTBF, MTTR ve Kullanılabilirliği (Availability) örtüşen duruşları birleştirerek ve vardiya saatlerine göre hesaplar.
func (h *MetricsHandler) GetReliabilityMetrics(w http.ResponseWriter, r *http.Request) {
	eqIDStr := r.URL.Query().Get("equipment_id")
	daysStr := r.URL.Query().Get("days")
	days := 30
	if d, err := strconv.Atoi(daysStr); err == nil && d > 0 {
		days = d
	}

	shiftHours := 16
	var eqFilter int
	if eqIDStr != "" {
		if id, err := strconv.Atoi(eqIDStr); err == nil && id > 0 {
			eqFilter = id
			_ = h.Pool.QueryRow(r.Context(), `SELECT shift_hours_per_day FROM equipments WHERE id = $1`, eqFilter).Scan(&shiftHours)
		}
	}

	var intervals []TimeInterval
	var err error
	if eqFilter > 0 {
		rows, qErr := h.Pool.Query(r.Context(),
			`SELECT start_time, end_time FROM downtime_events WHERE equipment_id = $1 AND downtime_type = 'unplanned' ORDER BY start_time ASC`,
			eqFilter,
		)
		err = qErr
		if err == nil {
			for rows.Next() {
				var st, et time.Time
				if rows.Scan(&st, &et) == nil {
					intervals = append(intervals, TimeInterval{Start: st.UTC(), End: et.UTC()})
				}
			}
			rows.Close()
		}
	} else {
		rows, qErr := h.Pool.Query(r.Context(),
			`SELECT start_time, end_time FROM downtime_events WHERE downtime_type = 'unplanned' ORDER BY start_time ASC LIMIT 5000`,
		)
		err = qErr
		if err == nil {
			for rows.Next() {
				var st, et time.Time
				if rows.Scan(&st, &et) == nil {
					intervals = append(intervals, TimeInterval{Start: st.UTC(), End: et.UTC()})
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

	totalScheduledHours := float64(days * shiftHours)
	if totalDowntimeHours > totalScheduledHours {
		totalScheduledHours = totalDowntimeHours + float64(shiftHours)
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

	// Görev 4: En Problemli 10 Ekipman Raporu (Composite Index idx_wo_equipment_type_status kullanır)
	topRows, err := h.Pool.Query(r.Context(), `
		SELECT 
			e.id, e.code, e.name, e.level_type,
			COUNT(w.id) AS failure_wo_count,
			COALESCE(SUM(w.labor_cost_kurus + w.parts_cost_kurus), 0)::BIGINT AS total_cost_kurus
		FROM equipments e
		JOIN work_orders w ON w.equipment_id = e.id
		WHERE w.wo_type = 'corrective' AND w.status != 'iptal'
		GROUP BY e.id, e.code, e.name, e.level_type
		ORDER BY failure_wo_count DESC, total_cost_kurus DESC
		LIMIT 10;
	`)
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

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"equipment_id":          eqFilter,
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
	})
}
