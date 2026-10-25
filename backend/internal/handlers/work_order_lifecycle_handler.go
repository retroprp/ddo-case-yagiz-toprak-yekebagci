package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bakimplan/internal/auth"
	"bakimplan/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WorkOrderLifecycleHandler — F-3 (iş emri yaşam döngüsü) ve F-6 (teknisyen atama).
type WorkOrderLifecycleHandler struct {
	Pool *pgxpool.Pool
}

func NewWorkOrderLifecycleHandler(pool *pgxpool.Pool) *WorkOrderLifecycleHandler {
	return &WorkOrderLifecycleHandler{Pool: pool}
}

var workOrderTransitions = map[string][]string{
	"acik":         {"atandi", "iptal"},
	"atandi":       {"devam_ediyor", "acik", "iptal"},
	"devam_ediyor": {"tamamlandi", "iptal"},
	"tamamlandi":   {},
	"iptal":        {},
}

func canTransition(from, to string) bool {
	for _, allowed := range workOrderTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

func allowedNextStatuses(from string) []string {
	if next, ok := workOrderTransitions[from]; ok {
		return next
	}
	return []string{}
}

// requestActorID, JWT'den doğrulanmış kullanıcı kimliğini döner (yoksa nil).
func requestActorID(r *http.Request) *int {
	if claims, ok := auth.GetUserFromContext(r.Context()); ok && claims != nil {
		id := claims.UserID
		return &id
	}
	return nil
}

// logWorkOrderEvent, durum geçmişine bir satır ekler. Çağıran tarafla aynı
// transaction içinde çalışır; işlem geri alınırsa kayıt da geri alınır.
func logWorkOrderEvent(ctx context.Context, tx pgx.Tx, woID int64, from, to string, actor *int, note string) error {
	var fromArg any
	if from != "" {
		fromArg = from
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO work_order_events (work_order_id, from_status, to_status, actor_id, note)
		 VALUES ($1, $2, $3, $4, $5)`,
		woID, fromArg, to, actor, note,
	)
	return err
}

type updateWorkOrderStatusReq struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

// UpdateStatus — PATCH /api/v1/work-orders/{id}/status
//
// Gövde: {"status": "devam_ediyor", "note": "vardiya başında başlandı"}
// Yanıt: {"work_order_id": 12, "from_status": "atandi", "to_status": "devam_ediyor", "allowed_next": [...]}
func (h *WorkOrderLifecycleHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	woID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || woID <= 0 {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Geçersiz iş emri ID.")
		return
	}

	var req updateWorkOrderStatusReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON.")
		return
	}

	target := strings.TrimSpace(req.Status)
	if _, known := workOrderTransitions[target]; !known {
		middleware.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"Geçersiz hedef durum. Bilinen durumlar: acik, atandi, devam_ediyor, tamamlandi, iptal.")
		return
	}
	if target == "tamamlandi" {
		middleware.WriteError(w, http.StatusConflict, "USE_COMPLETE_ENDPOINT",
			"Tamamlama işlemi parça tüketimi ve önleyici bakım yeniden planlaması yapar; POST /api/v1/work-orders/{id}/complete kullanın.")
		return
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "TX_ERROR", err.Error())
		return
	}
	defer tx.Rollback(ctx)

	var current string
	if err := tx.QueryRow(ctx,
		`SELECT status FROM work_orders WHERE id = $1 FOR UPDATE`, woID,
	).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			middleware.WriteError(w, http.StatusNotFound, "WO_NOT_FOUND", "İş emri bulunamadı.")
			return
		}
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if current == target {
		middleware.WriteError(w, http.StatusConflict, "INVALID_TRANSITION",
			"İş emri zaten '"+target+"' durumunda.")
		return
	}
	if !canTransition(current, target) {
		middleware.WriteError(w, http.StatusConflict, "INVALID_TRANSITION",
			"'"+current+"' durumundan '"+target+"' durumuna geçilemez.")
		return
	}

	switch target {
	case "devam_ediyor":
		// Başlama zamanı ilk kez yazılır; tekrar başlatmada korunur.
		_, err = tx.Exec(ctx,
			`UPDATE work_orders SET status = $1, started_at = COALESCE(started_at, NOW()) WHERE id = $2`,
			target, woID)
	case "acik":
		// Atamayı geri alma: iş emri yeniden havuza döner.
		_, err = tx.Exec(ctx,
			`UPDATE work_orders SET status = $1, assigned_to = NULL WHERE id = $2`,
			target, woID)
	default: // atandi, iptal
		_, err = tx.Exec(ctx, `UPDATE work_orders SET status = $1 WHERE id = $2`, target, woID)
	}
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "UPDATE_ERROR", err.Error())
		return
	}

	if err := logWorkOrderEvent(ctx, tx, woID, current, target, requestActorID(r), strings.TrimSpace(req.Note)); err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "EVENT_ERROR", err.Error())
		return
	}

	if err := tx.Commit(ctx); err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "COMMIT_ERROR", err.Error())
		return
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"work_order_id": woID,
		"from_status":   current,
		"to_status":     target,
		"allowed_next":  allowedNextStatuses(target),
	})
}

type assignTechnicianReq struct {
	TechnicianID *int   `json:"technician_id"`
	Note         string `json:"note"`
}

// AssignTechnician — POST /api/v1/work-orders/{id}/assign
//
// Gövde: {"technician_id": 1}
// Teknisyen rolü doğrulanır; iş emri 'acik' ise 'atandi' olur.
func (h *WorkOrderLifecycleHandler) AssignTechnician(w http.ResponseWriter, r *http.Request) {
	woID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || woID <= 0 {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Geçersiz iş emri ID.")
		return
	}

	var req assignTechnicianReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Geçersiz JSON.")
		return
	}
	if req.TechnicianID == nil || *req.TechnicianID <= 0 {
		middleware.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Pozitif bir technician_id zorunludur.")
		return
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "TX_ERROR", err.Error())
		return
	}
	defer tx.Rollback(ctx)

	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM users WHERE id = $1`, *req.TechnicianID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			middleware.WriteError(w, http.StatusNotFound, "TECHNICIAN_NOT_FOUND", "Kullanıcı bulunamadı.")
			return
		}
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	if role != "teknisyen" {
		middleware.WriteError(w, http.StatusUnprocessableEntity, "NOT_A_TECHNICIAN",
			"Atama yalnızca 'teknisyen' rolündeki kullanıcılara yapılabilir (bu kullanıcının rolü: "+role+").")
		return
	}

	var current string
	var previous *int
	if err := tx.QueryRow(ctx,
		`SELECT status, assigned_to FROM work_orders WHERE id = $1 FOR UPDATE`, woID,
	).Scan(&current, &previous); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			middleware.WriteError(w, http.StatusNotFound, "WO_NOT_FOUND", "İş emri bulunamadı.")
			return
		}
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if current == "tamamlandi" || current == "iptal" {
		middleware.WriteError(w, http.StatusConflict, "WO_CLOSED",
			"Kapanmış ('"+current+"') iş emrine atama yapılamaz.")
		return
	}
	if current == "devam_ediyor" {
		middleware.WriteError(w, http.StatusConflict, "WO_IN_PROGRESS",
			"Başlamış iş emrinin teknisyeni değiştirilemez; önce durumu 'atandi' yapın.")
		return
	}

	nextStatus := current
	if current == "acik" {
		nextStatus = "atandi"
	}

	if _, err := tx.Exec(ctx,
		`UPDATE work_orders SET assigned_to = $1, status = $2 WHERE id = $3`,
		*req.TechnicianID, nextStatus, woID,
	); err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "UPDATE_ERROR", err.Error())
		return
	}

	note := strings.TrimSpace(req.Note)
	if note == "" {
		switch {
		case previous == nil:
			note = "teknisyen atandı"
		case *previous != *req.TechnicianID:
			note = "teknisyen değiştirildi"
		default:
			note = "atama yenilendi"
		}
	}
	if err := logWorkOrderEvent(ctx, tx, woID, current, nextStatus, requestActorID(r), note); err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "EVENT_ERROR", err.Error())
		return
	}

	if err := tx.Commit(ctx); err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "COMMIT_ERROR", err.Error())
		return
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"work_order_id":     woID,
		"assigned_to":       *req.TechnicianID,
		"previous_assignee": previous,
		"status":            nextStatus,
	})
}

// Unassign — DELETE /api/v1/work-orders/{id}/assign
//
// Atamayı kaldırır ve iş emrini 'acik' durumuna döndürür.
func (h *WorkOrderLifecycleHandler) Unassign(w http.ResponseWriter, r *http.Request) {
	woID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || woID <= 0 {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Geçersiz iş emri ID.")
		return
	}

	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "TX_ERROR", err.Error())
		return
	}
	defer tx.Rollback(ctx)

	var current string
	var previous *int
	if err := tx.QueryRow(ctx,
		`SELECT status, assigned_to FROM work_orders WHERE id = $1 FOR UPDATE`, woID,
	).Scan(&current, &previous); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			middleware.WriteError(w, http.StatusNotFound, "WO_NOT_FOUND", "İş emri bulunamadı.")
			return
		}
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	if previous == nil {
		middleware.WriteError(w, http.StatusConflict, "NOT_ASSIGNED", "Bu iş emrine atanmış teknisyen yok.")
		return
	}
	if current == "devam_ediyor" || current == "tamamlandi" || current == "iptal" {
		middleware.WriteError(w, http.StatusConflict, "WO_LOCKED",
			"'"+current+"' durumundaki iş emrinden atama kaldırılamaz.")
		return
	}

	if _, err := tx.Exec(ctx,
		`UPDATE work_orders SET assigned_to = NULL, status = 'acik' WHERE id = $1`, woID,
	); err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "UPDATE_ERROR", err.Error())
		return
	}

	if err := logWorkOrderEvent(ctx, tx, woID, current, "acik", requestActorID(r), "atama kaldırıldı"); err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "EVENT_ERROR", err.Error())
		return
	}

	if err := tx.Commit(ctx); err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "COMMIT_ERROR", err.Error())
		return
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"work_order_id":     woID,
		"previous_assignee": previous,
		"status":            "acik",
	})
}

// ListEvents — GET /api/v1/work-orders/{id}/events
//
// Durum geçmişi, en yeni kayıt önce. Aktör adı users tablosundan çözülür.
func (h *WorkOrderLifecycleHandler) ListEvents(w http.ResponseWriter, r *http.Request) {
	woID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || woID <= 0 {
		middleware.WriteError(w, http.StatusBadRequest, "INVALID_ID", "Geçersiz iş emri ID.")
		return
	}

	rows, err := h.Pool.Query(r.Context(),
		`SELECT e.id, e.from_status, e.to_status, e.actor_id, u.full_name, u.role, e.note, e.created_at
		   FROM work_order_events e
		   LEFT JOIN users u ON u.id = e.actor_id
		  WHERE e.work_order_id = $1
		  ORDER BY e.id DESC
		  LIMIT 200`,
		woID,
	)
	if err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	defer rows.Close()

	items := make([]map[string]any, 0)
	for rows.Next() {
		var (
			id                   int64
			fromStatus           *string
			toStatus             string
			actorID              *int
			actorName, actorRole *string
			note                 string
			createdAt            time.Time
		)
		if err := rows.Scan(&id, &fromStatus, &toStatus, &actorID, &actorName, &actorRole, &note, &createdAt); err != nil {
			continue
		}
		items = append(items, map[string]any{
			"id":          id,
			"from_status": fromStatus,
			"to_status":   toStatus,
			"actor_id":    actorID,
			"actor_name":  actorName,
			"actor_role":  actorRole,
			"note":        note,
			"created_at":  createdAt,
		})
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"work_order_id": woID,
		"items":         items,
	})
}
