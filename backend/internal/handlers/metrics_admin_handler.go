package handlers

import (
	"errors"
	"net/http"
	"time"

	"bakimplan/internal/auth"
	"bakimplan/internal/middleware"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MetricsAdminHandler — materyalize view'ın yenilenmesi.
//
// Neden ayrı bir uç?
//
//	REFRESH MATERIALIZED VIEW bir transaction içinde çalışamaz (CONCURRENTLY
//	varyantı). Bu yüzden handler'ın kendi transaction'ı yok; tek çağrı olarak
//	işlenir. Otomatik tetiklemek de doğru değil: her iş emri yazımında refresh
//	etmek 85 ms'lik maliyeti yazma yoluna eklerdi (1M satırda).
//
//	Bunun yerine: toplu değişiklikten sonra (seed, gece işi, manuel) çağrılır.
type MetricsAdminHandler struct {
	Pool *pgxpool.Pool
}

func NewMetricsAdminHandler(pool *pgxpool.Pool) *MetricsAdminHandler {
	return &MetricsAdminHandler{Pool: pool}
}

// RefreshMetrics — POST /api/v1/admin/refresh-metrics
//
// Materyalize view'ı yeniler ve ne kadar sürdüğünü döner.
// Yalnızca `yonetici` rolü (rapor altyapısına dokunuyor).
func (h *MetricsAdminHandler) RefreshMetrics(w http.ResponseWriter, r *http.Request) {
	started := time.Now()

	// REFRESH ... CONCURRENTLY transaction bloğu içinde çalışmaz;
	// pool üzerinden tek cümle olarak çağırıyoruz.
	var rowCount int64
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT refresh_mv_equipment_wo_stats()`,
	).Scan(&rowCount); err != nil {
		middleware.WriteError(w, http.StatusInternalServerError, "REFRESH_FAILED",
			"Metrik görünümü yenilenemedi: "+err.Error())
		return
	}

	// Denetim izi: kim, ne zaman yeniledi. (audit_logs append-only)
	var uid *int
	if claims, ok := auth.GetUserFromContext(r.Context()); ok && claims != nil {
		id := claims.UserID
		uid = &id
	}
	middleware.RecordAudit(r.Context(), h.Pool, uid, "REFRESH_METRICS",
		"mv_equipment_wo_stats", "-",
		nil,
		map[string]any{"row_count": rowCount},
	)

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"view":        "mv_equipment_wo_stats",
		"row_count":   rowCount,
		"duration_ms": time.Since(started).Milliseconds(),
		"refreshed_at": time.Now().UTC(),
	})
}

// RefreshState — GET /api/v1/admin/refresh-metrics
//
// Raporun ne kadar taze olduğunu gösterir. Arayüzde "son güncelleme"
// etiketi için kullanılır; kimse tahmin etmek zorunda kalmaz.
func (h *MetricsAdminHandler) RefreshState(w http.ResponseWriter, r *http.Request) {
	var (
		name        string
		refreshedAt time.Time
		durationMS  int
		rowCount    int64
	)

	err := h.Pool.QueryRow(r.Context(), `
		SELECT name, refreshed_at, duration_ms, row_count
		  FROM metrics_refresh_state
		 WHERE name = 'mv_equipment_wo_stats'`,
	).Scan(&name, &refreshedAt, &durationMS, &rowCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Migration henüz çalışmadıysa ya da kayıt yoksa boş durum dön.
			middleware.WriteJSON(w, http.StatusOK, map[string]any{
				"view":  "mv_equipment_wo_stats",
				"ready": false,
			})
			return
		}
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"view":         name,
		"ready":        true,
		"refreshed_at": refreshedAt,
		"age_seconds":  int(time.Since(refreshedAt).Seconds()),
		"duration_ms":  durationMS,
		"row_count":    rowCount,
	})
}
