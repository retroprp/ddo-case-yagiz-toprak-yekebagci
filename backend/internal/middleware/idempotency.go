package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

func Idempotency(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Yalnızca durum değiştiren POST/PATCH isteklerinde ve header varsa çalışır
			if r.Method != http.MethodPost && r.Method != http.MethodPatch {
				next.ServeHTTP(w, r)
				return
			}

			idemKey := r.Header.Get("Idempotency-Key")
			if idemKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			ctx := r.Context()
			var savedStatus int
			var savedBody []byte

			err := pool.QueryRow(ctx,
				`SELECT status_code, response_body FROM idempotency_keys WHERE key = $1`,
				idemKey,
			).Scan(&savedStatus, &savedBody)

			if err == nil {
				// Anahtar daha önce kullanılmış! İşlemi tekrar yapmadan aynı cevabı döndür.
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Idempotent-Replay", "true")
				w.WriteHeader(savedStatus)
				_, _ = w.Write(savedBody)
				return
			}

			rec := &responseRecorder{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
				body:           &bytes.Buffer{},
			}

			next.ServeHTTP(rec, r)

			// Başarılı veya istemci hatası yanıtlarını (500 hariç) kaydet
			if rec.statusCode < 500 && rec.body.Len() > 0 && json.Valid(rec.body.Bytes()) {
				_, _ = pool.Exec(ctx,
					`INSERT INTO idempotency_keys (key, method, path, status_code, response_body)
					 VALUES ($1, $2, $3, $4, $5)
					 ON CONFLICT (key) DO NOTHING`,
					idemKey, r.Method, r.URL.Path, rec.statusCode, rec.body.Bytes(),
				)
			}
		})
	}
}
