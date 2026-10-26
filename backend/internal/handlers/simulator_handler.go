package handlers

import (
	"errors"
	"net/http"
	"time"

	"bakimplan/internal/middleware"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SimulatorHandler — simülatörün canlılık durumu.
//
// Neden bu uç var: simülatör deterministik (aynı tohum = aynı veri), bu yüzden
// seed verisini sürekli değiştirmiyor. Ama arayüzde sistemin canlı olduğu
// görünmeli (spec §8). Simülatör 5 saniyede bir `simulator_state` tablosuna
// heartbeat yazar; bu uç onu okur. Seed verisi değişmez.
type SimulatorHandler struct {
	Pool *pgxpool.Pool
}

func NewSimulatorHandler(pool *pgxpool.Pool) *SimulatorHandler {
	return &SimulatorHandler{Pool: pool}
}

// staleAfter: bu süreden uzun süredir sinyal yoksa "çalışmıyor" sayılır.
// Simülatör 5 sn'de bir yazdığı için 30 sn güvenli bir eşik.
const staleAfter = 30 * time.Second

// Status — GET /api/v1/simulator/status
//
// Yanıt: çalışıyor mu, son sinyal ne zaman, kaç tick, hangi tohum.
// Tüm kimliği doğrulanmış roller okuyabilir (arayüz rozeti için).
func (h *SimulatorHandler) Status(w http.ResponseWriter, r *http.Request) {
	var (
		mode     string
		seed     int64
		ticks    int64
		lastTick time.Time
	)

	err := h.Pool.QueryRow(r.Context(), `
		SELECT mode, seed, ticks, last_tick
		  FROM simulator_state
		 WHERE name = 'default'`,
	).Scan(&mode, &seed, &ticks, &lastTick)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			middleware.WriteJSON(w, http.StatusOK, map[string]any{
				"running": false,
				"reason":  "simülatör henüz heartbeat yazmadı",
			})
			return
		}
		middleware.WriteError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}

	age := time.Since(lastTick)
	running := age <= staleAfter

	middleware.WriteJSON(w, http.StatusOK, map[string]any{
		"running":       running,
		"mode":          mode,
		"seed":          seed,
		"ticks":         ticks,
		"last_tick":     lastTick,
		"age_seconds":   int(age.Seconds()),
		"stale_after_s": int(staleAfter.Seconds()),
		"note":          "deterministik mod: seed verisi sabit, simülatör yalnızca ayakta olduğunu bildirir",
	})
}
