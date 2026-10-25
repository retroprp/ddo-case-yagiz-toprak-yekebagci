package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"bakimplan/internal/auth"
	"bakimplan/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MaintenanceHandler struct {
	Pool *pgxpool.Pool
}

func NewMaintenanceHandler(pool *pgxpool.Pool) *MaintenanceHandler {
	return &MaintenanceHandler{Pool: pool}
}

type createFailureReq struct {
	EquipmentID int    `json:"equipment_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
}

func (h *MaintenanceHandler) CreateFailureReport(w http.ResponseWriter, r *http.Request) {
	var req createFailureReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON.")
		return
	}
	if req.EquipmentID <= 0 || req.Title == "" {
		middleware.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "equipment_id ve title zorunludur.")
		return
	}
	if req.Severity == "" {
		req.Severity = "orta"
	}

	claims, _ := auth.GetUserFromContext(r.Context())
	userID := 1
	if claims != nil {
		userID = claims.UserID
	}

	var id int64
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO failure_reports (equipment_id, reported_by, title, description, severity)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		req.EquipmentID, userID, req.Title, req.Description, req.Severity,
	).Scan(&id)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "DB_ERROR", err.Error())
		return
	}

	middleware.WriteJSON(w, http.StatusCreated, map[string]any{
		"id":           id,
		"equipment_id": req.EquipmentID,
		"title":        req.Title,
		"severity":     req.Severity,
		"status":       "acik",
	})
}

func (h *MaintenanceHandler) ListFailureReports(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT f.id, f.equipment_id, e.name, f.title, f.severity, f.status, f.created_at
		 FROM failure_reports f
		 JOIN equipments e ON e.id = f.equipment_id
		 ORDER BY f.id DESC LIMIT 100`)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	var items []map[string]any
	for rows.Next() {
		var id int64
		var eqID int
		var eqName, title, severity, status string
		var createdAt time.Time
		if err := rows.Scan(&id, &eqID, &eqName, &title, &severity, &status, &createdAt); err == nil {
			items = append(items, map[string]any{
				"id":             id,
				"equipment_id":   eqID,
				"equipment_name": eqName,
				"title":          title,
				"severity":       severity,
				"status":         status,
				"created_at":     createdAt,
			})
		}
	}
	if items == nil {
		items = []map[string]any{}
	}
	middleware.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *MaintenanceHandler) ListWorkOrders(w http.ResponseWriter, r *http.Request) {
	cursorStr := r.URL.Query().Get("cursor")
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 200 {
		limit = l
	}

	var cursor int64 = 9223372036854775807
	if c, err := strconv.ParseInt(cursorStr, 10, 64); err == nil && c > 0 {
		cursor = c
	}

	rows, err := h.Pool.Query(r.Context(),
		`SELECT w.id, w.code, w.equipment_id, e.name, w.wo_type, w.status, w.priority, w.title,
		        w.assigned_to, w.labor_cost_kurus, w.parts_cost_kurus, w.created_at
		 FROM work_orders w
		 JOIN equipments e ON e.id = w.equipment_id
		 WHERE w.id < $1
		 ORDER BY w.id DESC
		 LIMIT $2`,
		cursor, limit,
	)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	var items []map[string]any
	var nextCursor *int64
	for rows.Next() {
		var id int64
		var eqID int
		var assignedTo *int
		var code, eqName, woType, status, priority, title string
		var laborCost, partsCost int64
		var createdAt time.Time
		if err := rows.Scan(&id, &code, &eqID, &eqName, &woType, &status, &priority, &title, &assignedTo, &laborCost, &partsCost, &createdAt); err == nil {
			items = append(items, map[string]any{
				"id":               id,
				"code":             code,
				"equipment_id":     eqID,
				"equipment_name":   eqName,
				"wo_type":          woType,
				"status":           status,
				"priority":         priority,
				"title":            title,
				"assigned_to":      assignedTo,
				"labor_cost_kurus": laborCost,
				"parts_cost_kurus": partsCost,
				"created_at":       createdAt,
			})
			idCopy := id
			nextCursor = &idCopy
		}
	}
	if items == nil {
		items = []map[string]any{}
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nextCursor,
	})
}

type createWorkOrderReq struct {
	Code           string `json:"code"`
	EquipmentID    int    `json:"equipment_id"`
	WoType         string `json:"wo_type"`
	Priority       string `json:"priority"`
	Title          string `json:"title"`
	AssignedTo     *int   `json:"assigned_to"`
	LaborCostKurus int64  `json:"labor_cost_kurus"`
}

func (h *MaintenanceHandler) CreateWorkOrder(w http.ResponseWriter, r *http.Request) {
	var req createWorkOrderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON.")
		return
	}
	if req.EquipmentID <= 0 || req.Title == "" || req.LaborCostKurus < 0 {
		middleware.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Geçerli equipment_id, title ve negatif olmayan labor_cost_kurus zorunludur.")
		return
	}
	if req.WoType == "" {
		req.WoType = "corrective"
	}
	if req.Priority == "" {
		req.Priority = "normal"
	}
	if req.Code == "" {
		req.Code = fmt.Sprintf("WO-%d", time.Now().UnixNano()/1000)
	}

	status := "acik"
	if req.AssignedTo != nil {
		status = "atandi"
	}

	var id int64
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO work_orders (code, equipment_id, wo_type, status, priority, title, assigned_to, labor_cost_kurus, scheduled_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW()) RETURNING id`,
		req.Code, req.EquipmentID, req.WoType, status, req.Priority, req.Title, req.AssignedTo, req.LaborCostKurus,
	).Scan(&id)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "CREATE_WO_FAILED", err.Error())
		return
	}

	middleware.WriteJSON(w, http.StatusCreated, map[string]any{
		"id":           id,
		"code":         req.Code,
		"equipment_id": req.EquipmentID,
		"status":       status,
	})
}

type completeWorkOrderReq struct {
	CompletedAt    *time.Time `json:"completed_at"`
	LaborCostKurus *int64     `json:"labor_cost_kurus"`
}

// CompleteWorkOrder: İş emrini tamamlar, rezerve parçaları fiziksel stoktan düşer ('consume') ve önleyici bakımsa sonraki tarihi hesaplar.
func (h *MaintenanceHandler) CompleteWorkOrder(w http.ResponseWriter, r *http.Request) {
	woID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Geçersiz iş emri ID.")
		return
	}

	var req completeWorkOrderReq
	_ = json.NewDecoder(r.Body).Decode(&req)

	completedTime := time.Now().UTC()
	if req.CompletedAt != nil {
		completedTime = req.CompletedAt.UTC()
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "TX_ERROR", err.Error())
		return
	}
	defer tx.Rollback(ctx)

	var pmPlanID *int
	var scheduledAt *time.Time
	var currentLabor int64
	var currentStatus string
	err = tx.QueryRow(ctx,
		`SELECT preventive_plan_id, scheduled_at, labor_cost_kurus, status FROM work_orders WHERE id = $1 FOR UPDATE`,
		woID,
	).Scan(&pmPlanID, &scheduledAt, &currentLabor, &currentStatus)
	if err != nil {
		middleware.WriteError(w, http.StatusNotFound, "WO_NOT_FOUND", "İş emri bulunamadı.")
		return
	}
	if currentStatus == "tamamlandi" {
		middleware.WriteError(w, http.StatusConflict, "WO_ALREADY_COMPLETED", "Bu iş emri zaten tamamlanmış.")
		return
	}
	if currentStatus == "iptal" {
		middleware.WriteError(w, http.StatusConflict, "WO_CANCELLED", "İptal edilmiş iş emri tamamlanamaz.")
		return
	}

	if req.LaborCostKurus != nil && *req.LaborCostKurus >= 0 {
		currentLabor = *req.LaborCostKurus
	}

	_, err = tx.Exec(ctx,
		`UPDATE work_orders SET status = 'tamamlandi', completed_at = $1, labor_cost_kurus = $2 WHERE id = $3`,
		completedTime, currentLabor, woID,
	)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "UPDATE_ERROR", err.Error())
		return
	}

	// Yaşam döngüsü geçmişine tamamlanma kaydı (F-3). Aynı transaction içinde.
	if err := logWorkOrderEvent(ctx, tx, woID, currentStatus, "tamamlandi", requestActorID(r), "tamamlama"); err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "EVENT_ERROR", err.Error())
		return
	}

	// Bu iş emrine rezerve edilmiş parçaları 'consumed' yap ve stock_movements tablosuna (-quantity) çıkış hareketi yaz!
	resRows, err := tx.Query(ctx,
		`UPDATE part_reservations SET status = 'consumed'
		 WHERE work_order_id = $1 AND status = 'reserved'
		 RETURNING part_id, warehouse_id, quantity`,
		woID,
	)
	if err == nil {
		type consumedItem struct {
			PartID, WarehouseID, Qty int
		}
		var items []consumedItem
		for resRows.Next() {
			var ci consumedItem
			if resRows.Scan(&ci.PartID, &ci.WarehouseID, &ci.Qty) == nil {
				items = append(items, ci)
			}
		}
		resRows.Close()

		for _, ci := range items {
			_, _ = tx.Exec(ctx,
				`INSERT INTO stock_movements (part_id, warehouse_id, quantity_change, movement_type, reference_work_order_id)
				 VALUES ($1, $2, $3, 'consume', $4)`,
				ci.PartID, ci.WarehouseID, -ci.Qty, woID,
			)
		}
	}

	var nextDueAt *time.Time
	if pmPlanID != nil {
		var intervalDays *int
		var calcBasis string
		var planNextDue *time.Time
		err = tx.QueryRow(ctx,
			`SELECT interval_days, calc_basis, next_due_at FROM preventive_plans WHERE id = $1 FOR UPDATE`,
			*pmPlanID,
		).Scan(&intervalDays, &calcBasis, &planNextDue)

		if err == nil && intervalDays != nil && *intervalDays > 0 {
			baseTime := completedTime
			if calcBasis == "planned" {
				if scheduledAt != nil {
					baseTime = *scheduledAt
				} else if planNextDue != nil {
					baseTime = *planNextDue
				}
			}
			computed := baseTime.UTC().AddDate(0, 0, *intervalDays)
			nextDueAt = &computed

			_, _ = tx.Exec(ctx,
				`UPDATE preventive_plans SET last_completed_at = $1, next_due_at = $2 WHERE id = $3`,
				completedTime, computed, *pmPlanID,
			)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "COMMIT_ERROR", err.Error())
		return
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"work_order_id":        woID,
		"status":               "tamamlandi",
		"completed_at":         completedTime,
		"next_pm_scheduled_at": nextDueAt,
	})
}

type createPMPlanReq struct {
	EquipmentID   int        `json:"equipment_id"`
	Name          string     `json:"name"`
	PlanType      string     `json:"plan_type"`
	IntervalDays  *int       `json:"interval_days"`
	IntervalHours *int       `json:"interval_hours"`
	CalcBasis     string     `json:"calc_basis"`
	NextDueAt     *time.Time `json:"next_due_at"`
	NextDueMeter  *int       `json:"next_due_meter"`
}

func (h *MaintenanceHandler) CreatePMPlan(w http.ResponseWriter, r *http.Request) {
	var req createPMPlanReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON.")
		return
	}
	if req.EquipmentID <= 0 || req.Name == "" {
		middleware.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "equipment_id ve name zorunludur.")
		return
	}
	if req.PlanType == "" {
		req.PlanType = "both"
	}
	if req.CalcBasis == "" {
		req.CalcBasis = "completed"
	}
	if req.NextDueAt == nil && req.IntervalDays != nil && *req.IntervalDays > 0 {
		t := time.Now().UTC().AddDate(0, 0, *req.IntervalDays)
		req.NextDueAt = &t
	}
	if req.NextDueMeter == nil && req.IntervalHours != nil && *req.IntervalHours > 0 {
		m := *req.IntervalHours
		req.NextDueMeter = &m
	}

	var id int
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO preventive_plans (equipment_id, name, plan_type, interval_days, interval_hours, calc_basis, next_due_at, next_due_meter)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		req.EquipmentID, req.Name, req.PlanType, req.IntervalDays, req.IntervalHours, req.CalcBasis, req.NextDueAt, req.NextDueMeter,
	).Scan(&id)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "PM_CREATE_ERROR", err.Error())
		return
	}

	middleware.WriteJSON(w, http.StatusCreated, map[string]any{
		"id":             id,
		"equipment_id":   req.EquipmentID,
		"name":           req.Name,
		"calc_basis":     req.CalcBasis,
		"next_due_at":    req.NextDueAt,
		"next_due_meter": req.NextDueMeter,
	})
}

func (h *MaintenanceHandler) ListPMPlans(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT p.id, p.equipment_id, e.name, p.name, p.plan_type, p.interval_days, p.interval_hours, p.calc_basis, p.next_due_at, p.next_due_meter
		 FROM preventive_plans p
		 JOIN equipments e ON e.id = p.equipment_id
		 ORDER BY p.id ASC`)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	var items []map[string]any
	for rows.Next() {
		var id, eqID int
		var eqName, name, planType, calcBasis string
		var intDays, intHours, nextMeter *int
		var nextDueAt *time.Time
		if err := rows.Scan(&id, &eqID, &eqName, &name, &planType, &intDays, &intHours, &calcBasis, &nextDueAt, &nextMeter); err == nil {
			items = append(items, map[string]any{
				"id":             id,
				"equipment_id":   eqID,
				"equipment_name": eqName,
				"name":           name,
				"plan_type":      planType,
				"interval_days":  intDays,
				"interval_hours": intHours,
				"calc_basis":     calcBasis,
				"next_due_at":    nextDueAt,
				"next_due_meter": nextMeter,
			})
		}
	}
	if items == nil {
		items = []map[string]any{}
	}
	middleware.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

type meterReadingReq struct {
	ReadingHours int `json:"reading_hours"`
}

func (h *MaintenanceHandler) RecordMeterReading(w http.ResponseWriter, r *http.Request) {
	eqID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Geçersiz ekipman ID.")
		return
	}

	var req meterReadingReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ReadingHours < 0 {
		middleware.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Geçerli bir reading_hours giriniz.")
		return
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "TX_ERROR", err.Error())
		return
	}
	defer tx.Rollback(ctx)

	// Sayaç geriye gidemez (Monotonic artış kontrolü)
	var lastReading int
	_ = tx.QueryRow(ctx, `SELECT COALESCE(MAX(reading_hours), 0) FROM meter_readings WHERE equipment_id = $1`, eqID).Scan(&lastReading)
	if req.ReadingHours < lastReading {
		middleware.WriteError(w, http.StatusBadRequest, "METER_ROLLBACK",
			fmt.Sprintf("Sayaç okuması (%d) son okumadan (%d) küçük olamaz.", req.ReadingHours, lastReading))
		return
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO meter_readings (equipment_id, reading_hours) VALUES ($1, $2)`,
		eqID, req.ReadingHours,
	)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "READING_FAILED", err.Error())
		return
	}

	rows, err := tx.Query(ctx,
		`SELECT id, name, interval_hours, next_due_meter
		 FROM preventive_plans
		 WHERE equipment_id = $1 AND is_active = TRUE AND next_due_meter IS NOT NULL AND next_due_meter <= $2
		 FOR UPDATE`,
		eqID, req.ReadingHours,
	)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	type triggeredPlan struct {
		ID            int
		Name          string
		IntervalHours int
		NextDueMeter  int
	}
	var plans []triggeredPlan
	for rows.Next() {
		var p triggeredPlan
		var intHours *int
		if err := rows.Scan(&p.ID, &p.Name, &intHours, &p.NextDueMeter); err == nil {
			if intHours != nil {
				p.IntervalHours = *intHours
			}
			plans = append(plans, p)
		}
	}
	rows.Close()

	var generatedWOs []int64
	for _, p := range plans {
		code := fmt.Sprintf("PM-M-%d-%d", p.ID, p.NextDueMeter)
		var woID int64
		err := tx.QueryRow(ctx,
			`INSERT INTO work_orders (code, equipment_id, wo_type, preventive_plan_id, status, priority, title, scheduled_at)
			 VALUES ($1, $2, 'preventive', $3, 'acik', 'yuksek', $4, NOW())
			 ON CONFLICT DO NOTHING
			 RETURNING id`,
			code, eqID, p.ID, fmt.Sprintf("[Sayaç: %d saat] %s", req.ReadingHours, p.Name),
		).Scan(&woID)

		if err == nil && woID > 0 {
			generatedWOs = append(generatedWOs, woID)
			newNext := req.ReadingHours + p.IntervalHours
			_, _ = tx.Exec(ctx,
				`UPDATE preventive_plans SET last_trigger_meter = $1, next_due_meter = $2 WHERE id = $3`,
				req.ReadingHours, newNext, p.ID,
			)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "COMMIT_ERROR", err.Error())
		return
	}

	if generatedWOs == nil {
		generatedWOs = []int64{}
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"equipment_id":          eqID,
		"reading_hours":         req.ReadingHours,
		"generated_work_orders": generatedWOs,
	})
}

func (h *MaintenanceHandler) TriggerDueTimePMs(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	if ref := r.URL.Query().Get("as_of"); ref != "" {
		if parsed, err := time.Parse(time.RFC3339, ref); err == nil {
			now = parsed
		}
	}

	ctx := r.Context()
	rows, err := h.Pool.Query(ctx,
		`SELECT id, equipment_id, name, next_due_at
		 FROM preventive_plans
		 WHERE is_active = TRUE AND next_due_at IS NOT NULL AND next_due_at <= $1`,
		now,
	)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	type duePlan struct {
		ID          int
		EquipmentID int
		Name        string
		NextDueAt   time.Time
	}
	var plans []duePlan
	for rows.Next() {
		var p duePlan
		if err := rows.Scan(&p.ID, &p.EquipmentID, &p.Name, &p.NextDueAt); err == nil {
			plans = append(plans, p)
		}
	}

	var createdIDs []int64
	for _, p := range plans {
		code := fmt.Sprintf("PM-T-%d-%d", p.ID, p.NextDueAt.Unix())
		var woID int64
		err := h.Pool.QueryRow(ctx,
			`INSERT INTO work_orders (code, equipment_id, wo_type, preventive_plan_id, status, priority, title, scheduled_at)
			 VALUES ($1, $2, 'preventive', $3, 'acik', 'normal', $4, $5)
			 ON CONFLICT DO NOTHING
			 RETURNING id`,
			code, p.EquipmentID, p.ID, "[Periyodik Bakım] "+p.Name, p.NextDueAt,
		).Scan(&woID)
		if err == nil && woID > 0 {
			createdIDs = append(createdIDs, woID)
		}
	}
	if createdIDs == nil {
		createdIDs = []int64{}
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"evaluated_at":          now,
		"generated_work_orders": createdIDs,
	})
}

func (h *MaintenanceHandler) GetTechnicianWorkload(w http.ResponseWriter, r *http.Request) {
	query := `
	SELECT 
		u.id, u.full_name, u.email,
		COUNT(w.id) FILTER (WHERE w.status IN ('atandi', 'devam_ediyor')) AS active_count,
		COUNT(w.id) FILTER (WHERE w.status = 'tamamlandi') AS completed_count
	FROM users u
	LEFT JOIN work_orders w ON w.assigned_to = u.id
	WHERE u.role = 'teknisyen'
	GROUP BY u.id, u.full_name, u.email
	ORDER BY active_count DESC, u.id ASC;
	`
	rows, err := h.Pool.Query(r.Context(), query)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	var items []map[string]any
	for rows.Next() {
		var id, active, completed int
		var fullName, email string
		if err := rows.Scan(&id, &fullName, &email, &active, &completed); err == nil {
			items = append(items, map[string]any{
				"technician_id":   id,
				"full_name":       fullName,
				"email":           email,
				"active_tasks":    active,
				"completed_tasks": completed,
			})
		}
	}
	if items == nil {
		items = []map[string]any{}
	}
	middleware.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}
