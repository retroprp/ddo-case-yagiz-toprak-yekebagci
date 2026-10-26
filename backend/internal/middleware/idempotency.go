package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// IdempotencyTTL: bir anahtarın ne kadar süre tekrar oynatılabilir kalacağı.
	IdempotencyTTL = 24 * time.Hour
	// maxIdempotencyBody: istek/yanıt gövdesi için üst sınır (2 MiB).
	maxIdempotencyBody = 2 << 20
	// maxIdempotencyKey: DB kolonu VARCHAR(255).
	maxIdempotencyKey = 255
)

// criticalIdempotencyRoutes, Idempotency-Key başlığı ZORUNLU olan uçlardır.
//
// Anahtar, chi'nin route pattern'i ile eşleşir ("POST /api/v1/work-orders").
// RoutePattern kullanılıyor çünkü gerçek yol parametre içerir
// (/work-orders/42/complete) ve onu sabit listeyle eşleştirmek mümkün değildir.
//
// Liste bilinçli olarak açık: yeni bir uç eklendiğinde buraya yazılmadıkça
// anahtar zorunlu olmaz. Böylece kapsam gözden geçirilebilir kalır.
var criticalIdempotencyRoutes = map[string]bool{
	"POST /api/v1/equipments":                     true,
	"PATCH /api/v1/equipments/{id}/move":          true,
	"POST /api/v1/equipments/{id}/meter-readings": true,

	"POST /api/v1/failure-reports": true,

	"POST /api/v1/work-orders":                   true,
	"POST /api/v1/work-orders/{id}/complete":     true,
	"PATCH /api/v1/work-orders/{id}/status":      true,
	"POST /api/v1/work-orders/{id}/assign":       true,
	"DELETE /api/v1/work-orders/{id}/assign":     true,
	"POST /api/v1/work-orders/{id}/reserve-part": true,

	"POST /api/v1/stock-movements": true,

	"POST /api/v1/preventive-plans":             true,
	"POST /api/v1/preventive-plans/trigger-due": true,

	"POST /api/v1/downtime-events":       true,
	"PATCH /api/v1/downtime-events/{id}": true,

	// Rapor altyapısı: yenileme bir yazma işlemi, idempotent olması gerek
	// (aynı anahtarla tekrar gelirse ikinci REFRESH çalışmasın).
	"POST /api/v1/admin/refresh-metrics": true,
}

// RequireIdempotencyKey, kritik uçlarda Idempotency-Key başlığını zorunlu kılar.
//
// Neden ayrı bir middleware: Idempotency middleware'i anahtar yoksa isteği
// normal işler (tekrar oynatma koruması devre dışı kalır). Bu katman, korumanın
// sessizce atlanmasını engeller ve istemciye açık bir hata döner.
//
// auth.Authenticate'ten SONRA, Idempotency'den ÖNCE bağlanmalıdır.
func RequireIdempotencyKey() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pattern := chi.RouteContext(r.Context()).RoutePattern()
			if !criticalIdempotencyRoutes[r.Method+" "+pattern] {
				next.ServeHTTP(w, r)
				return
			}

			if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
				WriteError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED",
					"Bu uç için Idempotency-Key başlığı zorunludur (tekrar gönderimde çift kayıt oluşmasını engeller).")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// UserIDFunc isteğin sahibini döndürür. Nil dönerse anonim (user_id=0) sayılır.
//
// Neden callback: middleware paketi auth paketini import edemez (auth zaten bu
// paketi import ediyor), bu yüzden kullanıcı kimliği dışarıdan enjekte edilir.
type UserIDFunc func(*http.Request) *int

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	wrote      bool
	body       bytes.Buffer
}

func (r *responseRecorder) WriteHeader(code int) {
	if r.wrote {
		return
	}
	r.wrote = true
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	if r.body.Len() < maxIdempotencyBody {
		r.body.Write(b)
	}
	return r.ResponseWriter.Write(b)
}

// Idempotency, Idempotency-Key başlığını veritabanında saklar ve tekrar
// oynatmayı güvenli hâle getirir.
//
// Anahtar global olarak benzersizdir; bu yüzden bir anahtarın başka bir
// kullanıcıya veya başka bir uca "kayması" sessizce kabul edilmez:
//
//  1. Rezervasyon: istek işlenmeden önce 'in_progress' satırı yazılır. Aynı anda
//     gelen ikinci istek bu satırı görür ve 409 alır (çift çalıştırma yok).
//  2. Tamamlama: yanıt 2xx ise durum 'completed' olur, gövde saklanır.
//  3. Yeniden oynatma: aynı anahtar + aynı kullanıcı + aynı uç + aynı gövde
//     -> kayıtlı yanıt 200/201 olarak döner (X-Idempotent-Replay: true).
//  4. Farklı kullanıcı -> 409 IDEMPOTENCY_USER_MISMATCH.
//  5. Farklı uç/metot -> 409 IDEMPOTENCY_KEY_CONFLICT.
//  6. Farklı gövde     -> 409 IDEMPOTENCY_CONFLICT.
//  7. Başarısız istek  -> satır silinir; düzeltilmiş istek aynı anahtarla
//     tekrar denenebilir (anahtar "yanmaz").
func Idempotency(pool *pgxpool.Pool, userID UserIDFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
			default:
				next.ServeHTTP(w, r)
				return
			}

			key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			if len(key) > maxIdempotencyKey {
				WriteError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_TOO_LONG", "Idempotency-Key en fazla 255 karakter olabilir.")
				return
			}

			// Gövdeyi bir kez oku; hem hash için hem handler için kullan.
			raw, err := io.ReadAll(io.LimitReader(r.Body, maxIdempotencyBody))
			if err != nil {
				WriteError(w, http.StatusBadRequest, "INVALID_BODY", "İstek gövdesi okunamadı.")
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(raw))

			reqHash := hashRequest(r.Method, r.URL.Path, raw)

			uid := 0
			if userID != nil {
				if p := userID(r); p != nil {
					uid = *p
				}
			}

			ctx := r.Context()

			// 1) Bu anahtar daha önce kullanılmış mı?
			var (
				savedUserID int
				savedMethod string
				savedPath   string
				savedHash   string
				state       string
				savedCode   *int
				savedBody   []byte
			)
			err = pool.QueryRow(ctx, `
				SELECT user_id, method, path, request_hash, state, status_code, response_body
				FROM idempotency_keys
				WHERE key = $1 AND expires_at > now()`,
				key,
			).Scan(&savedUserID, &savedMethod, &savedPath, &savedHash, &state, &savedCode, &savedBody)

			if err == nil {
				if savedUserID != uid {
					WriteError(w, http.StatusConflict, "IDEMPOTENCY_USER_MISMATCH",
						"Bu Idempotency-Key başka bir kullanıcıya ait.")
					return
				}
				if savedMethod != r.Method || savedPath != r.URL.Path {
					WriteError(w, http.StatusConflict, "IDEMPOTENCY_KEY_CONFLICT",
						"Bu Idempotency-Key farklı bir uç veya metot için kullanılmış.")
					return
				}
				if savedHash != reqHash {
					WriteError(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT",
						"Aynı Idempotency-Key farklı bir istek gövdesiyle kullanılmış.")
					return
				}
				if state != "completed" {
					WriteError(w, http.StatusConflict, "IDEMPOTENCY_IN_PROGRESS",
						"Bu Idempotency-Key ile aynı istek hâlâ işleniyor, lütfen bekleyin.")
					return
				}
				status := http.StatusOK
				if savedCode != nil {
					status = *savedCode
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Idempotent-Replay", "true")
				w.WriteHeader(status)
				if len(savedBody) > 0 {
					_, _ = w.Write(savedBody)
				}
				return
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				WriteError(w, http.StatusInternalServerError, "IDEMPOTENCY_ERROR", "Idempotency kaydı okunamadı.")
				return
			}

			// 2) Rezervasyon: eşzamanlı ikinci isteği burada durdur.
			var reserved string
			err = pool.QueryRow(ctx, `
				INSERT INTO idempotency_keys (key, user_id, method, path, request_hash, state, expires_at)
				VALUES ($1, $2, $3, $4, $5, 'in_progress', now() + $6::interval)
				ON CONFLICT (key) DO NOTHING
				RETURNING key`,
				key, uid, r.Method, r.URL.Path, reqHash, IdempotencyTTL.String(),
			).Scan(&reserved)
			if errors.Is(err, pgx.ErrNoRows) {
				WriteError(w, http.StatusConflict, "IDEMPOTENCY_IN_PROGRESS",
					"Bu Idempotency-Key ile eşzamanlı bir istek işleniyor, lütfen bekleyin.")
				return
			}
			if err != nil {
				WriteError(w, http.StatusInternalServerError, "IDEMPOTENCY_ERROR", "Idempotency kaydı oluşturulamadı.")
				return
			}

			rec := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rec, r)

			// Yanıt tamamlandıktan sonra istemci iptal etse bile kaydı bitir.
			finishCtx := context.WithoutCancel(ctx)

			if rec.statusCode >= 200 && rec.statusCode < 300 {
				var body any
				if json.Valid(rec.body.Bytes()) {
					body = json.RawMessage(rec.body.Bytes())
				}
				_, _ = pool.Exec(finishCtx, `
					UPDATE idempotency_keys
					SET state = 'completed', status_code = $1, response_body = $2
					WHERE key = $3`,
					rec.statusCode, body, key,
				)
				return
			}

			// 3) Başarısız istek anahtarı yakmasın: satırı bırak.
			_, _ = pool.Exec(finishCtx, `DELETE FROM idempotency_keys WHERE key = $1`, key)
		})
	}
}

func hashRequest(method, path string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte{'\n'})
	h.Write([]byte(path))
	h.Write([]byte{'\n'})
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}
