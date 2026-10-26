package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// auditState, bir istek içinde handler'ın kendi denetim kaydını yazıp
// yazmadığını taşır.
//
// Neden gerekli: AuditTrail her mutasyonu "güvenlik ağı" olarak kaydeder.
// Handler'lar da iş kuralına özel ayrıntılı kayıt yazar (eski/yeni değer).
// İkisi birlikte çalışınca aynı işlem iki satır oluyordu:
//
//	#164  patch /api/v1/downtime-events/{id}  {"path":..,"status":200}
//	#163  UPDATE_DOWNTIME                     {"start_time":..,"end_time":..}
//
// Çözüm: handler kayıt yazdığında bu bayrağı kaldırır; middleware görürse
// ikinci satırı yazmaz. Ağ güvenliği korunur (handler unutan uçlar yine
// middleware tarafından kaydedilir), gürültü kalkar.
//
// Context değerleri değiştirilemediği için bayrak bir pointer üzerinden
// taşınır: aynı istek içindeki tüm katmanlar aynı nesneyi görür.
type auditState struct {
	handlerLogged bool
}

type auditStateKey struct{}

// WithAuditState, istek context'ine bayrak taşıyıcısı ekler.
func WithAuditState(ctx context.Context) (context.Context, *auditState) {
	st := &auditState{}
	return context.WithValue(ctx, auditStateKey{}, st), st
}

// markHandlerLogged, handler'ın kendi kaydını yazdığını bildirir.
func markHandlerLogged(ctx context.Context) {
	if st, ok := ctx.Value(auditStateKey{}).(*auditState); ok {
		st.handlerLogged = true
	}
}

func RecordAudit(ctx context.Context, pool *pgxpool.Pool, userID *int, action, entityType, entityID string, oldData, newData any) {
	var oldJSON, newJSON []byte
	if oldData != nil {
		oldJSON, _ = json.Marshal(oldData)
	}
	if newData != nil {
		newJSON, _ = json.Marshal(newData)
	}

	_, _ = pool.Exec(ctx,
		`INSERT INTO audit_logs (user_id, action, entity_type, entity_id, old_data, new_data)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, action, entityType, entityID, oldJSON, newJSON,
	)

	// Aynı istek için middleware'in ikinci (generic) kaydı yazmasını engelle.
	markHandlerLogged(ctx)
}

func RecordAuditTx(ctx context.Context, tx pgx.Tx, userID *int, action, entityType, entityID string, oldData, newData any) error {
	var oldJSON, newJSON []byte
	if oldData != nil {
		oldJSON, _ = json.Marshal(oldData)
	}
	if newData != nil {
		newJSON, _ = json.Marshal(newData)
	}

	_, err := tx.Exec(ctx,
		`INSERT INTO audit_logs (user_id, action, entity_type, entity_id, old_data, new_data)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, action, entityType, entityID, oldJSON, newJSON,
	)
	if err == nil {
		markHandlerLogged(ctx)
	}
	return err
}

type auditStatusRecorder struct {
	http.ResponseWriter
	statusCode int
	wrote      bool
}

func (s *auditStatusRecorder) WriteHeader(code int) {
	if s.wrote {
		return
	}
	s.wrote = true
	s.statusCode = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *auditStatusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(b)
}

// AuditTrail, kimliği doğrulanmış her değiştirici isteği (POST/PATCH/PUT/DELETE)
// başarıyla sonuçlandığında audit_logs'a yazar.
//
// Bu bir "güvenlik ağı"dır: handler kendi ayrıntılı kaydını yazmayı unutursa
// bile kim + hangi uç + hangi sonuç izi kalır. Handler kayıt yazdıysa
// (RecordAudit/RecordAuditTx bayrağı kaldırır) ikinci generic satır yazılmaz —
// aksi hâlde her işlem iki kez görünür.
//
// auth.Authenticate middleware'inden SONRA bağlanmalıdır; aksi hâlde userID
// her zaman nil olur.
func AuditTrail(pool *pgxpool.Pool, userID UserIDFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
			default:
				next.ServeHTTP(w, r)
				return
			}

			ctx, state := WithAuditState(r.Context())
			rec := &auditStatusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))

			// Yalnızca gerçekten bir kayıt oluştuysa iz bırak.
			if rec.statusCode < 200 || rec.statusCode >= 300 {
				return
			}

			// Handler kendi ayrıntılı kaydını yazdıysa tekrar yazma.
			if state.handlerLogged {
				return
			}

			var uid *int
			if userID != nil {
				uid = userID(r)
			}

			entityType := strings.TrimPrefix(r.URL.Path, "/api/v1/")
			if i := strings.IndexByte(entityType, '/'); i >= 0 {
				entityType = entityType[:i]
			}
			action := strings.ToLower(r.Method)
			if pattern := chi.RouteContext(r.Context()).RoutePattern(); pattern != "" {
				action = strings.ToLower(r.Method) + " " + pattern
			}

			RecordAudit(ctx, pool, uid, action, entityType, chi.URLParam(r, "id"),
				nil,
				map[string]any{
					"path":        r.URL.Path,
					"status_code": rec.statusCode,
				},
			)
		})
	}
}
