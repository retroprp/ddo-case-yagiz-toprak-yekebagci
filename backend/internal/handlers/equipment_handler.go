package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"bakimplan/internal/auth"
	"bakimplan/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EquipmentHandler struct {
	Pool *pgxpool.Pool
}

func NewEquipmentHandler(pool *pgxpool.Pool) *EquipmentHandler {
	return &EquipmentHandler{Pool: pool}
}

var expectedParentLevel = map[string]string{
	"tesis":   "",       // Kök düğüm, parent_id NULL olmalı
	"hat":     "tesis",  // Hat yalnızca Tesis altına bağlanabilir
	"makine":  "hat",    // Makine yalnızca Hat altına bağlanabilir
	"bilesen": "makine", // Bileşen yalnızca Makine altına bağlanabilir
}

// validateHierarchyLevel: Tesis -> Hat -> Makine -> Bileşen seviye kuralını doğrular.
func validateHierarchyLevel(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, childLevel string, parentID *int) error {
	expectedParent, ok := expectedParentLevel[childLevel]
	if !ok {
		return fmt.Errorf("geçersiz ekipman seviyesi: %s", childLevel)
	}

	if expectedParent == "" {
		if parentID != nil {
			return fmt.Errorf("'tesis' en üst seviyedir, başka bir ekipmanın altına bağlanamaz (parent_id null olmalıdır)")
		}
		return nil
	}

	if parentID == nil {
		return fmt.Errorf("'%s' seviyesindeki bir ekipman kök olamaz; bir '%s' altına bağlanmalıdır", childLevel, expectedParent)
	}

	var actualParentLevel string
	err := q.QueryRow(ctx, `SELECT level_type FROM equipments WHERE id = $1`, *parentID).Scan(&actualParentLevel)
	if err != nil {
		return fmt.Errorf("üst ekipman (#%d) bulunamadı", *parentID)
	}

	if actualParentLevel != expectedParent {
		return fmt.Errorf("hiyerarşi kuralı ihlali: '%s' seviyesi yalnızca '%s' altına bağlanabilir (hedef ebeveyn '%s' seviyesinde)",
			childLevel, expectedParent, actualParentLevel)
	}

	return nil
}

type EquipmentNode struct {
	ID                  int              `json:"id"`
	Code                string           `json:"code"`
	Name                string           `json:"name"`
	LevelType           string           `json:"level_type"`
	ParentID            *int             `json:"parent_id"`
	ShiftHoursPerDay    int              `json:"shift_hours_per_day"`
	IsCritical          bool             `json:"is_critical"`
	Status              string           `json:"status"`
	DirectCostKurus     int64            `json:"direct_cost_kurus"`
	RollupCostKurus     int64            `json:"rollup_cost_kurus"`
	DirectDowntimeHours float64          `json:"direct_downtime_hours"`
	RollupDowntimeHours float64          `json:"rollup_downtime_hours"`
	Children            []*EquipmentNode `json:"children"`
}

// GetTree: WITH RECURSIVE kullanarak tüm ekipman ağacını ve alt kırılımların toplam maliyet/duruş (roll-up) değerlerini hesaplar.
func (h *EquipmentHandler) GetTree(w http.ResponseWriter, r *http.Request) {
	query := `
	WITH RECURSIVE tree AS (
		SELECT id, parent_id, id AS root_id
		FROM equipments
		UNION ALL
		SELECT e.id, e.parent_id, t.root_id
		FROM equipments e
		INNER JOIN tree t ON e.parent_id = t.id
	),
	wo_costs AS (
		SELECT equipment_id, COALESCE(SUM(labor_cost_kurus + parts_cost_kurus), 0) AS total_cost
		FROM work_orders
		WHERE status != 'iptal'
		GROUP BY equipment_id
	),
	dt_hours AS (
		SELECT equipment_id, COALESCE(SUM(EXTRACT(EPOCH FROM (end_time - start_time)) / 3600.0), 0) AS total_hours
		FROM downtime_events
		GROUP BY equipment_id
	),
	rollup AS (
		SELECT 
			t.root_id AS equipment_id,
			COALESCE(SUM(wc.total_cost), 0)::BIGINT AS rollup_cost_kurus,
			COALESCE(SUM(dh.total_hours), 0)::FLOAT8 AS rollup_downtime_hours
		FROM tree t
		LEFT JOIN wo_costs wc ON wc.equipment_id = t.id
		LEFT JOIN dt_hours dh ON dh.equipment_id = t.id
		GROUP BY t.root_id
	)
	SELECT 
		e.id, e.code, e.name, e.level_type, e.parent_id, e.shift_hours_per_day, e.is_critical, e.status,
		COALESCE(wc.total_cost, 0)::BIGINT AS direct_cost_kurus,
		COALESCE(r.rollup_cost_kurus, 0)::BIGINT AS rollup_cost_kurus,
		ROUND(COALESCE(dh.total_hours, 0)::NUMERIC, 2)::FLOAT8 AS direct_downtime_hours,
		ROUND(COALESCE(r.rollup_downtime_hours, 0)::NUMERIC, 2)::FLOAT8 AS rollup_downtime_hours
	FROM equipments e
	LEFT JOIN wo_costs wc ON wc.equipment_id = e.id
	LEFT JOIN dt_hours dh ON dh.equipment_id = e.id
	LEFT JOIN rollup r ON r.equipment_id = e.id
	ORDER BY e.id ASC;
	`

	rows, err := h.Pool.Query(r.Context(), query)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", "Ekipman ağacı sorgulanamadı: "+err.Error())
		return
	}
	defer rows.Close()

	nodeMap := make(map[int]*EquipmentNode)
	var allNodes []*EquipmentNode

	for rows.Next() {
		n := &EquipmentNode{Children: []*EquipmentNode{}}
		if err := rows.Scan(
			&n.ID, &n.Code, &n.Name, &n.LevelType, &n.ParentID,
			&n.ShiftHoursPerDay, &n.IsCritical, &n.Status,
			&n.DirectCostKurus, &n.RollupCostKurus,
			&n.DirectDowntimeHours, &n.RollupDowntimeHours,
		); err != nil {
			middleware.WriteError(w, http.StatusInternalServerError, "SCAN_ERROR", err.Error())
			return
		}
		nodeMap[n.ID] = n
		allNodes = append(allNodes, n)
	}

	var roots []*EquipmentNode
	for _, n := range allNodes {
		if n.ParentID != nil {
			if parent, ok := nodeMap[*n.ParentID]; ok {
				parent.Children = append(parent.Children, n)
				continue
			}
		}
		roots = append(roots, n)
	}
	if roots == nil {
		roots = []*EquipmentNode{}
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"tree":       roots,
		"flat_count": len(allNodes),
	})
}

type createEquipmentReq struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	LevelType        string `json:"level_type"`
	ParentID         *int   `json:"parent_id"`
	ShiftHoursPerDay int    `json:"shift_hours_per_day"`
	IsCritical       bool   `json:"is_critical"`
}

func (h *EquipmentHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createEquipmentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Geçersiz istek gövdesi.")
		return
	}
	if req.Code == "" || req.Name == "" || req.LevelType == "" {
		middleware.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "code, name ve level_type zorunludur.")
		return
	}
	if req.ShiftHoursPerDay <= 0 || req.ShiftHoursPerDay > 24 {
		req.ShiftHoursPerDay = 16
	}

	// Hiyerarşi Seviye Kontrolü (Tesis -> Hat -> Makine -> Bileşen)
	if err := validateHierarchyLevel(r.Context(), h.Pool, req.LevelType, req.ParentID); err != nil {
		middleware.WriteError(w, http.StatusUnprocessableEntity, "INVALID_HIERARCHY_LEVEL", err.Error())
		return
	}

	var id int
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO equipments (code, name, level_type, parent_id, shift_hours_per_day, is_critical)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		req.Code, req.Name, req.LevelType, req.ParentID, req.ShiftHoursPerDay, req.IsCritical,
	).Scan(&id)

	if err != nil {
		middleware.WriteError(w, http.StatusConflict, "EQUIPMENT_CREATE_FAILED", "Ekipman oluşturulamadı: "+err.Error())
		return
	}

	claims, _ := auth.GetUserFromContext(r.Context())
	var uid *int
	if claims != nil {
		uid = &claims.UserID
	}
	middleware.RecordAudit(r.Context(), h.Pool, uid, "CREATE_EQUIPMENT", "equipment", strconv.Itoa(id), nil, req)

	middleware.WriteJSON(w, http.StatusCreated, map[string]any{
		"id":         id,
		"code":       req.Code,
		"name":       req.Name,
		"level_type": req.LevelType,
		"parent_id":  req.ParentID,
	})
}

type moveEquipmentReq struct {
	NewParentID *int `json:"new_parent_id"`
}

// Move: Ekipmanı ağaçta başka bir düğümün altına taşır; WITH RECURSIVE ile döngü (cycle) oluşmasını ve yanlış seviye bağlanmasını engeller!
func (h *EquipmentHandler) Move(w http.ResponseWriter, r *http.Request) {
	eqID, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Geçersiz ekipman ID.")
		return
	}

	var req moveEquipmentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON.")
		return
	}

	if req.NewParentID != nil && *req.NewParentID == eqID {
		middleware.WriteError(w, http.StatusConflict, "CYCLE_DETECTED", "Bir ekipman kendisinin alt düğümü olamaz (döngü hatası).")
		return
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "TX_ERROR", err.Error())
		return
	}
	defer tx.Rollback(ctx)

	var oldParentID *int
	var childLevel string
	err = tx.QueryRow(ctx, `SELECT parent_id, level_type FROM equipments WHERE id = $1 FOR UPDATE`, eqID).Scan(&oldParentID, &childLevel)
	if err != nil {
		middleware.WriteError(w, http.StatusNotFound, "NOT_FOUND", "Ekipman bulunamadı.")
		return
	}

	// 1. ÖNCE DÖNGÜ (CYCLE) KONTROLÜ: Yeni parent_id, bu ekipmanın kendi alt ağacında (descendants) yer alıyor mu?
	if req.NewParentID != nil {
		var createsCycle bool
		cycleQuery := `
		WITH RECURSIVE descendants AS (
			SELECT id FROM equipments WHERE parent_id = $1
			UNION ALL
			SELECT e.id FROM equipments e
			INNER JOIN descendants d ON e.parent_id = d.id
		)
		SELECT EXISTS (SELECT 1 FROM descendants WHERE id = $2);
		`
		if err := tx.QueryRow(ctx, cycleQuery, eqID, *req.NewParentID).Scan(&createsCycle); err != nil {
			middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
			return
		}
		if createsCycle {
			middleware.WriteError(w, http.StatusConflict, "CYCLE_DETECTED", "Hedef ebeveyn bu ekipmanın alt ağacında bulunuyor! Döngü (cycle) engellendi.")
			return
		}
	}

	// 2. HİYERARŞİ SEVİYE TİPİ KONTROLÜ (Tesis -> Hat -> Makine -> Bileşen)
	if err := validateHierarchyLevel(ctx, tx, childLevel, req.NewParentID); err != nil {
		middleware.WriteError(w, http.StatusUnprocessableEntity, "INVALID_HIERARCHY_LEVEL", err.Error())
		return
	}

	_, err = tx.Exec(ctx, `UPDATE equipments SET parent_id = $1 WHERE id = $2`, req.NewParentID, eqID)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "MOVE_FAILED", err.Error())
		return
	}

	claims, _ := auth.GetUserFromContext(ctx)
	var uid *int
	if claims != nil {
		uid = &claims.UserID
	}
	_ = middleware.RecordAuditTx(ctx, tx, uid, "MOVE_EQUIPMENT", "equipment", strconv.Itoa(eqID),
		map[string]any{"parent_id": oldParentID},
		map[string]any{"parent_id": req.NewParentID},
	)

	if err := tx.Commit(ctx); err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "COMMIT_ERROR", err.Error())
		return
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"id":            eqID,
		"level_type":    childLevel,
		"old_parent_id": oldParentID,
		"new_parent_id": req.NewParentID,
		"status":        "moved",
	})
}
