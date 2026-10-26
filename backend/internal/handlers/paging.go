package handlers

import (
	"fmt"
	"net/http"
	"strconv"
)

// Keyset (cursor) sayfalama — ortak yardımcılar.
//
// Neden OFFSET değil de keyset?
//
//	OFFSET 10000, veritabanına "ilk 10.000 satırı oku, sonra at" der.
//	Maliyet sayfa derinliğiyle doğrusal artar ve araya yeni kayıt girdiğinde
//	aynı satır iki farklı sayfada görünebilir (kayma problemini).
//
//	Keyset'te istemciye "son gördüğüm kaydın kimliği" verilir; sorgu
//	`WHERE id < cursor` ile indeksten doğrudan başlar. Sayfa derinliğinden
//	bağımsız sabit maliyet, tekrar yok.
//
// Cursor neden opak değil (düz tamsayı)?
//
//	Bu tablolarda `id` BIGSERIAL ve tekilliği garantili; sıralama anahtarı
//	olarak yeterli. İstemcinin cursor'ı okuyup anlaması bir sakınca değil.
//	Eğer sıralama iş anahtarına göre olsaydı (ör. created_at) bileşik cursor
//	gerekirdi: base64("2026-10-25T22:37:31Z|6001").
const (
	defaultPageLimit = 50
	maxPageLimit     = 200
)

// parsePageParams, `limit` ve `cursor` query parametrelerini okur.
//
// Dönen cursor nil ise "ilk sayfa" demektir. Hatalı değer gelirse 400
// dönebilmek için error döner (sessizce yok saymak istemciyi yanıltır:
// kullanıcı 2. sayfayı ister, sunucu 1. sayfayı verir).
func parsePageParams(r *http.Request, def, max int) (limit int, cursor *int64, err error) {
	query := r.URL.Query()

	limit = def
	if raw := query.Get("limit"); raw != "" {
		n, convErr := strconv.Atoi(raw)
		if convErr != nil || n <= 0 {
			return 0, nil, fmt.Errorf("limit pozitif bir tamsayı olmalı")
		}
		if n > max {
			// Üst sınırı sessizce uygula: istemci 10.000 istese de 200 döner.
			// (Reddetmek yerine sınırlamak, sayfalama istemcileri için daha az kırıcı.)
			n = max
		}
		limit = n
	}

	if raw := query.Get("cursor"); raw != "" {
		n, convErr := strconv.ParseInt(raw, 10, 64)
		if convErr != nil || n <= 0 {
			return 0, nil, fmt.Errorf("cursor pozitif bir tamsayı olmalı")
		}
		cursor = &n
	}

	return limit, cursor, nil
}

// trimPage, limit+1 satır çekildiği varsayımıyla çalışır.
//
//	Neden limit+1? "Daha fazla var mı" sorusunun cevabı ancak bir satır
//	fazla okuyarak kesinleşir. `len(rows) == limit` kontrolü yanıltıcıdır:
//	tam `limit` kayıt kaldıysa "devam var" sanılır ve istemci boş sayfa alır.
//
//	Dönen next, sonraki istekte `cursor` olarak gönderilir; son sayfada nil.
func trimPage[T any](rows []T, limit int, idOf func(T) int64) (items []T, next *int64, hasMore bool) {
	hasMore = len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	if len(rows) == 0 {
		return []T{}, nil, hasMore
	}
	items = rows
	if hasMore {
		last := idOf(rows[len(rows)-1])
		next = &last
	}
	return items, next, hasMore
}
