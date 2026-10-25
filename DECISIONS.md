# Mimari Karar Kayıtları (ADR)

Bu doküman, BakımPlan CMMS geliştirilirken alınan kritik kararları, değerlendirilen
alternatifleri ve sonuçlarını kaydeder.

Format: **Bağlam → Seçenekler → Karar → Sonuç**

| # | Karar | Kısa gerekçe |
|---|---|---|
| [001](#adr-001) | ORM yerine `pgx/v5` + ham SQL | Recursive CTE, `FOR UPDATE`, kısmi indeks |
| [002](#adr-002) | Stokta pessimistic locking | Yatay ölçeklemede güvenli |
| [003](#adr-003) | Partial unique index ile "birikmeme" | Kural DB motorunda garanti |
| [004](#adr-004) | Vue 3 + Vite + TailwindCSS | Kurum standardı, düşük boilerplate |
| [005](#adr-005) | Sürümlü gömülü migrasyon | Şema değişikliği geri izlenebilir |
| [006](#adr-006) | Idempotency: rezervasyon + kapsam | TOCTOU yarışı ve token sızıntısı |
| [007](#adr-007) | Durum geçişleri uygulama katmanında | İş kuralı, veri bütünlüğü değil |
| [008](#adr-008) | Ayrı geçmiş tablosu | "Şu an nerede" ≠ "nasıl geldi" |
| [009](#adr-009) | Frontend'te rol guard tek başına yeterli değil | Yetki sunucuda doğrulanır |
| [010](#adr-010) | Vite env'i build arg olarak geçmek | `import.meta.env` derleme anında gömülür |

---

## ADR-001

### ORM yerine ham SQL (`pgx/v5`) ve veritabanı seviyesi kısıtlar

**Bağlam.** Sistemde dört seviyeli ekipman hiyerarşisi üzerinde maliyet toplama (roll-up),
eşzamanlı stok rezervasyonunda satır kilitleme ve 1 milyon iş emrinde hızlı raporlama
gereksinimleri var.

**Seçenekler.**

1. **GORM (Go ORM).** CRUD hızlı yazılır, ancak `WITH RECURSIVE`, `SELECT ... FOR UPDATE`
   ve `INCLUDE` covering index sorgularında soyutlama sızdırır; N+1 riski taşır.
2. **`pgx/v5` + ham SQL.** PostgreSQL binary protokolünü doğrudan kullanır, `CopyFrom`
   toplu yazmayı destekler, SQL üzerinde tam kontrol verir.

**Karar.** `pgx/v5` (`pgxpool`) + ham SQL.

**Sonuç.** Simülatör 2.000 ekipmanı ve binlerce geçmiş kaydı `CopyFrom` ile **< 1 saniyede**
yazıyor. Roll-up sorgusu `Index Only Scan` ile çalışıyor (`Heap Fetches: 0`).

---

## ADR-002

### Eşzamanlı stok rezervasyonunda pessimistic locking ve hareket tabanlı stok

**Bağlam.** Birden fazla teknisyen aynı anda aynı parçayı kendi iş emrine rezerve etmeye
çalışabilir. Stoktan fazla rezervasyon yapılmamalı ve "stok = hareketlerin toplamı" kuralı
korunmalı.

**Seçenekler.**

1. **Uygulama içi `sync.Mutex`.** Yalnızca tek API instance'ında korur; yatay ölçeklemede
   (iki konteyner) yarış devam eder.
2. **Optimistic locking (versiyon kolonu).** Çakışmada istek reddedilir; uygulama tarafında
   retry döngüsü gerekir.
3. **`SELECT ... FOR UPDATE` + hareket toplamı.** Transaction başında `parts` satırı
   kilitlenir; kilit altında `stock_movements` ve `part_reservations` üzerinden kullanılabilir
   stok yeniden hesaplanır, sonra rezervasyon yazılır.

**Karar.** Seçenek 3.

**Sonuç.** Çoklu konteyner ortamında güvenli. `TestIntegration07` 20 eşzamanlı goroutine ile
kanıtlıyor: 4 adet stok → **tam 4 başarı, 16 red, aşım 0**.

**Not.** Stok ayrı bir kolonda tutulmuyor; defter (`stock_movements`) tek doğruluk kaynağı.
Avantajı: her hareket denetlenebilir, geçmiş yeniden üretilebilir. Maliyeti: her okuma
`SUM` gerektirir — indeksle (`part_id, warehouse_id`) ucuz kalıyor.

---

## ADR-003

### Önleyici bakımların birikmemesi için partial unique index

**Bağlam.** 30 günlük bir plan 3 ay gecikirse 3 ayrı iş emri açılmamalı. Sayaç okuması
tekrarlandığında da idempotent davranmalı.

**Seçenekler.**

1. **Uygulamada `SELECT COUNT(*)` kontrolü.** İki eşzamanlı sayaç okumasında iki iş emri
   birden açılabilir (TOCTOU yarışı).
2. **Partial unique index.** `WHERE preventive_plan_id IS NOT NULL AND status IN (...)`
   koşuluyla tekillik kısıtı.

**Karar.** Partial unique index + `INSERT ... ON CONFLICT DO NOTHING`.

**Sonuç.** Kural **veritabanı motorunda** garanti. Uygulama kodu hatalı olsa bile ikinci
aktif iş emri oluşamaz. Ayrıca `ON CONFLICT DO NOTHING` sayesinde sayaç tetiklemesi
kendiliğinden idempotent hâle geliyor: kod `ErrNoRows` alırsa "zaten var" diyor.

```sql
CREATE UNIQUE INDEX uq_active_pm_work_order
    ON work_orders (preventive_plan_id)
    WHERE preventive_plan_id IS NOT NULL
      AND status IN ('acik', 'atandi', 'devam_ediyor');
```

---

## ADR-004

### Frontend'te Vue 3 (Composition API) + Vite + TailwindCSS

**Bağlam.** 5 rolün farklı ekran gördüğü, mobil uyumlu, hızlı bir arayüz gerekiyor.

**Seçenekler.**

1. **Go `html/template` + HTMX.** Sunucu taraflı; SPA deneyimi ve zengin istemci durumu
   (oturum bazlı görünüm, anlık filtre) için ek efor gerekir.
2. **Vue 3 `<script setup>` + Vite + Tailwind.** Kurumun teknoloji yığınıyla uyumlu, düşük
   boilerplate, multi-stage Nginx ile küçük imaj.

**Karar.** Seçenek 2.

**Sonuç.** 8 ekran, 5 rol görünümü, her ekranda `loading / empty / error` durumu.
Tailwind **build-time** derleniyor (CDN değil): 19 kB CSS, 154 kB JS.

**Devamı niteliğinde karar:** 1.015 satırlık tek `App.vue` dosyası ekran başına `.vue`
dosyalarına bölündü. Nedeni: tek dosyada sekiz ekranın state'i iç içe geçiyor, merge
conflict kaçınılmaz hâle geliyor ve code-splitting mümkün olmuyor.

---

## ADR-005

### Sürümlü, gömülü migrasyon çalıştırıcısı

**Bağlam.** Başlangıçta yalnızca `//go:embed migrations/001_init.sql` vardı. Yeni bir şema
değişikliği eklemenin tek yolu 001 dosyasını düzenlemekti — çalışan veritabanlarında geri
alınamaz sapma riski.

**Seçenekler.**

1. **`golang-migrate` bağımlılığı.** Olgun, ama ek binary/CLI ve ayrı sürüm yönetimi.
2. **Kendi çalıştırıcısı.** `embed.FS` ile klasörü göm, dosya adına göre sırala,
   `schema_migrations` tablosunda uygulananları tut, her dosyayı kendi transaction'ında çalıştır.

**Karar.** Seçenek 2 (proje şartnamesi "golang-migrate **veya** gömülü SQL migrasyon"
diyor).

**Sonuç.** Şema değişikliği = yeni dosya (`002_...sql`). Sıra deterministik, her dosya bir kez
çalışır, geri izlenebilir. Mevcut volume'ler otomatik yükseliyor: 001 `IF NOT EXISTS` ile
yazıldığı için tekrar çalışması zararsız, ardından 002 uygulanıyor. Bu senaryo test edildi
(2.000 ekipmanlı eski şema → yeni binary → veri korundu).

**Teknik detay.** Tek SQL dosyasında birden fazla cümle olabildiği için `PgConn().Exec`
(basit protokol) kullanılıyor; `pool.Exec` tek cümle bekler.

---

## ADR-006

### Idempotency: rezervasyon + kapsam + gövde hash'i

**Bağlam.** Ağ koptuğunda istemci aynı POST'u tekrar gönderir. Çift iş emri, çift stok
düşümü olmamalı. Ayrıca anahtar tahmin edilebilirse başka kullanıcının yanıtı sızabilir.

**Seçenekler.**

1. **`SELECT` → işle → `INSERT`.** Klasik TOCTOU: iki eşzamanlı istek de "yok" görür, ikisi
   de işlemi çalıştırır.
2. **`INSERT ... ON CONFLICT DO NOTHING RETURNING`.** Kontrol ve rezervasyon tek atomik
   cümlede; satır dönerse kilidi ben aldım, dönmezse başkası aldı.

**Karar.** Seçenek 2 + şu kurallar:

| Kontrol | Engellediği |
|---|---|
| `(user_id)` kapsamı | Anahtar tahminiyle oturum sızıntısı |
| `(method, path)` kapsamı | Aynı anahtarın başka uçta kullanılması |
| `request_hash` (SHA-256) | Düzeltilmiş gövdenin eski yanıtı alması |
| `state = in_progress` | Eşzamanlı çift çalıştırma |
| `expires_at` (24 saat) | Tablonun sonsuz büyümesi |
| Yalnızca 2xx kaydetme | Başarısız isteğin anahtarı yakması |

**Sonuç.** `TestIdemKey01-04` ve `TestIntegration02-03` bunu doğruluyor.

**İkinci karar: anahtar kritik uçlarda zorunlu.** Middleware anahtar yoksa isteği normal
işliyordu; yani koruma sessizce devre dışı kalabiliyordu. `RequireIdempotencyKey()` katmanı
15 kritik uçta anahtar yoksa **400 `IDEMPOTENCY_KEY_REQUIRED`** döner.

**Neden chi `RoutePattern`?** Gerçek yol parametre içerir (`/work-orders/42/complete`);
sabit string listesiyle eşleşmez. `RoutePattern` şablonu verir (`/work-orders/{id}/complete`).

---

## ADR-007

### Durum geçişleri uygulama katmanında, `CHECK` constraint değil

**Bağlam.** İş emri beş durumda: `acik, atandi, devam_ediyor, tamamlandi, iptal`. Hangi
geçişin geçerli olduğu bağlama bağlı (ör. `acik → devam_ediyor` yalnızca atama yapılmışsa).

**Seçenekler.**

1. **`CHECK` constraint / trigger.** Değer kümesini korur ama bağlamsal kuralı ifade edemez.
2. **Uygulama katmanında geçiş matrisi.** Tek `map` içinde okunabilir, test edilebilir,
   anlamlı hata mesajı üretebilir.

**Karar.** Seçenek 2. Veritabanı `CHECK` yalnızca **değer kümesini** korur (savunma katmanı);
geçiş kuralı Go'da.

**Sonuç.** `INVALID_TRANSITION` 409 ve izinli sonraki durumlar yanıtta (`allowed_next`)
dönüyor. Arayüz butonları bu listeden üretilebilir.

**Özel durum.** `tamamlandi` yalnızca `POST /work-orders/{id}/complete` ile yapılabilir;
`PATCH /status` bunu **409 `USE_COMPLETE_ENDPOINT`** ile reddeder. Sebep: tamamlama yalnızca
`status` yazmıyor — rezerve parçaları stoktan düşüyor, `stock_movements`'a çıkış hareketi
yazıyor, önleyici bakımın `next_due_at`'ını hesaplıyor. İki kod yolu aynı yan etkileri ayrı
ayrı uygulamak zorunda kalmasın.

---

## ADR-008

### Durum geçmişi için ayrı tablo

**Bağlam.** "Şu an nerede" sorusu `work_orders.status` ile yanıtlanıyor. Ama denetim ve
gecikme analizi için "nasıl geldi, kim ne zaman başlattı" da gerekiyor.

**Seçenekler.**

1. **`previous_status` kolonu.** Yalnızca bir adım geriye bakabilir; normalizasyon ihlali.
2. **`work_order_events` tablosu.** Her geçiş bir satır: `from_status, to_status, actor_id,
   note, created_at`.

**Karar.** Seçenek 2.

**Sonuç.** `GET /work-orders/{id}/events` en yeni önce döner, aktör adı ve rolü `users`'tan
çözülür. Kayıtlar durum değişimiyle **aynı transaction** içinde yazılır: işlem geri alınırsa
geçmiş de geri alınır, yarım kayıt kalmaz.

**Not.** Bu, event sourcing'in hafif bir uygulaması: tam event sourcing'de `status` kolonu
olmaz, her okuma projeksiyondan gelir. Burada pragmatik olarak ikisi birlikte tutuldu —
okuma hızlı, geçmiş eksiksiz.

---

## ADR-009

### Arayüzdeki rol kontrolü yetkilendirme sayılmaz

**Bağlam.** Router guard'ı ve buton görünürlüğü rolü kontrol ediyor. Ancak tarayıcıdaki
JavaScript ve `localStorage` kullanıcı tarafından değiştirilebilir.

**Karar.** Rol kontrolü **iki katmanda** ama farklı amaçla:

| Katman | Amaç | Güvenilirlik |
|---|---|---|
| Vue router guard / `hasRole()` | Kullanıcıyı boş ekranla karşılamamak | UX |
| `auth.RequireRoles(...)` (Go) | Erişimi gerçekten engellemek | **Yetkilendirme** |

**Sonuç.** Her uçta `401` (kimlik yok) ile `403` (rol yetkisiz) ayrı ayrı test edilir
(`TestUnit14`, `TestLifecycle05`, `make verify` RBAC satırı).

---

## ADR-010

### Vite ortam değişkenlerini build arg olarak geçmek

**Bağlam.** Frontend API adresini `import.meta.env.VITE_API_URL` üzerinden okuyor. Bu değer
Vite tarafından **derleme anında** statik olarak kodun içine gömülür.

**Seçenekler.**

1. **Runtime `ENV`.** Docker `ENV` ile verilir → **çalışmaz**, çünkü değer build sonrası
   değişmiyor (JS içine sabitlenmiş).
2. **Build `ARG` + `ENV`.** `docker build --build-arg` ile değer verilir, `npm run build`
   sırasında gömülür.
3. **`window.__CONFIG__` (+ `envsubst`).** Tek imajı farklı ortamlara taşımayı sağlar; bu
   proje için gereksiz karmaşıklık.

**Karar.** Seçenek 2 (Seçenek 3 ileride ölçek gerekirse değerlendirilebilir).

**Sonuç.** `docker compose up --build frontend` ile API adresi değiştirilebilir. Telefondan
erişim için `VITE_API_URL=http://<LAN-IP>:8080/api/v1` verilerek yeniden derlenir.
