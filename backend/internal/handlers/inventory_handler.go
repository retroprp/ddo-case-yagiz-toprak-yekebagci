package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"bakimplan/internal/auth"
	"bakimplan/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type InventoryHandler struct {
	Pool *pgxpool.Pool
}

func NewInventoryHandler(pool *pgxpool.Pool) *InventoryHandler {
	return &InventoryHandler{Pool: pool}
}

// ListParts: Stok = hareketlerin toplamı kuralına göre tüm parçaların fiziksel, rezerve ve kullanılabilir stoklarını ve uyarıları döner.
func (h *InventoryHandler) ListParts(w http.ResponseWriter, r *http.Request) {
	query := `
	WITH mov AS (
		SELECT part_id, COALESCE(SUM(quantity_change), 0)::INT AS physical_stock
		FROM stock_movements
		GROUP BY part_id
	),
	res AS (
		SELECT part_id, COALESCE(SUM(quantity), 0)::INT AS reserved_stock
		FROM part_reservations
		WHERE status = 'reserved'
		GROUP BY part_id
	)
	SELECT 
		p.id, p.sku, p.name, p.unit_cost_kurus, p.reorder_point, p.is_critical,
		COALESCE(m.physical_stock, 0) AS physical_stock,
		COALESCE(r.reserved_stock, 0) AS reserved_stock,
		(COALESCE(m.physical_stock, 0) - COALESCE(r.reserved_stock, 0)) AS available_stock
	FROM parts p
	LEFT JOIN mov m ON m.part_id = p.id
	LEFT JOIN res r ON r.part_id = p.id
	ORDER BY p.id ASC;
	`

	rows, err := h.Pool.Query(r.Context(), query)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	var items []map[string]any
	for rows.Next() {
		var id, reorderPoint, physical, reserved, available int
		var sku, name string
		var unitCost int64
		var isCritical bool

		if err := rows.Scan(&id, &sku, &name, &unitCost, &reorderPoint, &isCritical, &physical, &reserved, &available); err != nil {
			continue
		}

		items = append(items, map[string]any{
			"id":                    id,
			"sku":                   sku,
			"name":                  name,
			"unit_cost_kurus":       unitCost,
			"reorder_point":         reorderPoint,
			"is_critical":           isCritical,
			"physical_stock":        physical,
			"reserved_stock":        reserved,
			"available_stock":       available,
			"needs_reorder":         available <= reorderPoint,
			"critical_zero_warning": isCritical && available <= 0,
		})
	}
	if items == nil {
		items = []map[string]any{}
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

type addStockMovementReq struct {
	PartID         int    `json:"part_id"`
	WarehouseID    int    `json:"warehouse_id"`
	QuantityChange int    `json:"quantity_change"`
	MovementType   string `json:"movement_type"`
	WorkOrderID    *int64 `json:"work_order_id"`
	AllowBOMLink   *int   `json:"equipment_id_for_bom"`
}

func (h *InventoryHandler) CreateStockMovement(w http.ResponseWriter, r *http.Request) {
	var req addStockMovementReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON.")
		return
	}
	if req.PartID <= 0 || req.WarehouseID <= 0 || req.QuantityChange == 0 {
		middleware.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "part_id, warehouse_id ve sıfırdan farklı quantity_change zorunludur.")
		return
	}
	if req.MovementType == "" {
		req.MovementType = "purchase"
	}

	claims, _ := auth.GetUserFromContext(r.Context())
	var uid *int
	if claims != nil {
		uid = &claims.UserID
	}

	var movID int64
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO stock_movements (part_id, warehouse_id, quantity_change, movement_type, reference_work_order_id, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		req.PartID, req.WarehouseID, req.QuantityChange, req.MovementType, req.WorkOrderID, uid,
	).Scan(&movID)

	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "MOVEMENT_FAILED", err.Error())
		return
	}

	if req.AllowBOMLink != nil && *req.AllowBOMLink > 0 {
		_, _ = h.Pool.Exec(r.Context(),
			`INSERT INTO equipment_parts_bom (equipment_id, part_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			*req.AllowBOMLink, req.PartID,
		)
	}

	middleware.RecordAudit(r.Context(), h.Pool, uid, "STOCK_MOVEMENT", "part", strconv.Itoa(req.PartID), nil, req)

	middleware.WriteJSON(w, http.StatusCreated, map[string]any{
		"movement_id":     movID,
		"part_id":         req.PartID,
		"warehouse_id":    req.WarehouseID,
		"quantity_change": req.QuantityChange,
	})
}

type reservePartReq struct {
	PartID      int `json:"part_id"`
	WarehouseID int `json:"warehouse_id"`
	Quantity    int `json:"quantity"`
}

// ReservePartForWorkOrder (Görev 3): SELECT ... FOR UPDATE ile satır kilitleyerek yarış durumunu (race condition) %100 engeller!
func (h *InventoryHandler) ReservePartForWorkOrder(w http.ResponseWriter, r *http.Request) {
	woID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Geçersiz iş emri ID.")
		return
	}

	var req reservePartReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON.")
		return
	}
	if req.PartID <= 0 || req.WarehouseID <= 0 || req.Quantity <= 0 {
		middleware.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "part_id, warehouse_id ve pozitif quantity zorunludur.")
		return
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "TX_ERROR", err.Error())
		return
	}
	defer tx.Rollback(ctx)

	// 1. Eşzamanlı istekleri sıraya sokmak için parçayı kilitle (SELECT ... FOR UPDATE)!
	var unitCost int64
	var reorderPoint int
	var isCritical bool
	err = tx.QueryRow(ctx,
		`SELECT unit_cost_kurus, reorder_point, is_critical FROM parts WHERE id = $1 FOR UPDATE`,
		req.PartID,
	).Scan(&unitCost, &reorderPoint, &isCritical)
	if err != nil {
		middleware.WriteError(w, http.StatusNotFound, "PART_NOT_FOUND", "Yedek parça bulunamadı.")
		return
	}

	// 2. İş emrinin bağlı olduğu ekipmanı bul
	var equipmentID int
	var woStatus string
	err = tx.QueryRow(ctx, `SELECT equipment_id, status FROM work_orders WHERE id = $1`, woID).Scan(&equipmentID, &woStatus)
	if err != nil {
		middleware.WriteError(w, http.StatusNotFound, "WO_NOT_FOUND", "İş emri bulunamadı.")
		return
	}
	if woStatus == "tamamlandi" || woStatus == "iptal" {
		middleware.WriteError(w, http.StatusConflict, "WO_CLOSED", "Tamamlanmış veya iptal edilmiş iş emrine parça rezerve edilemez.")
		return
	}

	// 3. BOM (Ekipman - Parça Uyumluluğu) Kontrolü: Eğer bu ekipman için BOM tanımlanmışsa parça listede olmalı
	var bomCount int
	_ = tx.QueryRow(ctx, `SELECT COUNT(*) FROM equipment_parts_bom WHERE equipment_id = $1`, equipmentID).Scan(&bomCount)
	if bomCount > 0 {
		var isCompatible bool
		_ = tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM equipment_parts_bom WHERE equipment_id = $1 AND part_id = $2)`,
			equipmentID, req.PartID,
		).Scan(&isCompatible)
		if !isCompatible {
			middleware.WriteError(w, http.StatusUnprocessableEntity, "BOM_INCOMPATIBLE", "Bu yedek parça iş emrindeki ekipman ile uyumlu değil (BOM hatası).")
			return
		}
	}

	// 4. Depodaki Fiziksel Stok (Hareketlerin Toplamı) ve Aktif Rezervasyonları Hesapla
	var physicalStock int
	_ = tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(quantity_change), 0)::INT FROM stock_movements WHERE part_id = $1 AND warehouse_id = $2`,
		req.PartID, req.WarehouseID,
	).Scan(&physicalStock)

	var reservedStock int
	_ = tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(quantity), 0)::INT FROM part_reservations WHERE part_id = $1 AND warehouse_id = $2 AND status = 'reserved'`,
		req.PartID, req.WarehouseID,
	).Scan(&reservedStock)

	availableStock := physicalStock - reservedStock
	if availableStock < req.Quantity {
		middleware.WriteError(w, http.StatusConflict, "INSUFFICIENT_STOCK",
			"Depoda yeterli kullanılabilir stok yok. Mevcut: "+strconv.Itoa(availableStock)+", İstenen: "+strconv.Itoa(req.Quantity))
		return
	}

	// 5. Rezervasyonu Oluştur ve İş Emrinin Parça Maliyetini Güncelle
	var reservationID int64
	err = tx.QueryRow(ctx,
		`INSERT INTO part_reservations (work_order_id, part_id, warehouse_id, quantity, status)
		 VALUES ($1, $2, $3, $4, 'reserved') RETURNING id`,
		woID, req.PartID, req.WarehouseID, req.Quantity,
	).Scan(&reservationID)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "RESERVE_ERROR", err.Error())
		return
	}

	addedCost := unitCost * int64(req.Quantity)
	_, _ = tx.Exec(ctx,
		`UPDATE work_orders SET parts_cost_kurus = parts_cost_kurus + $1 WHERE id = $2`,
		addedCost, woID,
	)

	remainingAfter := availableStock - req.Quantity
	claims, _ := auth.GetUserFromContext(ctx)
	var uid *int
	if claims != nil {
		uid = &claims.UserID
	}
	_ = middleware.RecordAuditTx(ctx, tx, uid, "RESERVE_PART", "work_order", strconv.FormatInt(woID, 10),
		map[string]any{"available_before": availableStock},
		map[string]any{"reservation_id": reservationID, "part_id": req.PartID, "quantity": req.Quantity, "available_after": remainingAfter},
	)

	if err := tx.Commit(ctx); err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "COMMIT_ERROR", err.Error())
		return
	}

	middleware.WriteJSON(w, http.StatusCreated, map[string]any{
		"reservation_id":        reservationID,
		"work_order_id":         woID,
		"part_id":               req.PartID,
		"warehouse_id":          req.WarehouseID,
		"reserved_quantity":     req.Quantity,
		"remaining_available":   remainingAfter,
		"reorder_alert":         remainingAfter <= reorderPoint,
		"critical_zero_warning": isCritical && remainingAfter == 0,
	})
}
