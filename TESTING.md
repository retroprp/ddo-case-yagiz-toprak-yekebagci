# Test ve Doğrulama Kılavuzu

Bu doküman, BakımPlan CMMS'in **ne ile**, **nasıl** ve **hangi kanıtla** doğrulandığını
açıklar. Üç katman vardır:

| Katman | Araç | Ne kanıtlar | Süre |
|---|---|---|---|
| **1. Uçtan uca** | `make verify` (`cmd/verify`) | Kapsam maddeleri gerçekten çalışıyor mu | ~3 s |
| **2. Otomatik test** | `go test ./...` | Algoritmalar ve iş kuralları doğru mu | ~5 s |
| **3. Yük testi** | `make load` (k6) | Yanıt süreleri hedefin altında mı | ~15 s |

---

## 1. Uçtan uca doğrulama — `make verify`

Bağımsız bir Go programı (`cmd/verify`) ayağa kalkmış API'ye **gerçek HTTP** atar ve
`PASS/FAIL` tablosu basar. Test framework'ü değildir; bu yüzden Docker imajı içinde de,
host'tan da çalışır.

```bash
make verify
# veya doğrudan:
docker compose --profile verify run --rm verify
```

Örnek çıktı:

```
KİMLİK     | DURUM  | SÜRE     | KANIT / AÇIKLAMA
--------------------------------------------------------------------------------
HEALTH     | PASS   | 2ms      | status=healthy, database=connected
AUTH-LOGIN | PASS   | 439ms    | 5 rol JWT doğrulandı, hatalı parola 401 reddedildi
RBAC-403   | PASS   | 2ms      | Tokensız->401, Yetkisiz Rol->403 başarıyla engellendi
F-1        | PASS   | 1.1s     | Kök tesis sayısı: 5 (Toplam 2000+ ekipman)
F-2        | PASS   | 13ms     | Arıza #502 oluşturuldu ve Idempotency doğrulandı
F-3        | PASS   | 5ms      | İş emri #6001 açıldı, next_cursor=5992
F-4        | PASS   | 4ms      | PM Planı #101 (planned bazlı) oluşturuldu
F-5        | PASS   | 3ms      | 50 yedek parça hareket toplamlarıyla doğrulandı
F-6        | PASS   | 3ms      | 1 teknisyen iş yükü hesaplandı
F-7        | PASS   | 6ms      | MTBF=16599.4s, MTTR=2.9s, Availability=%99.98
T-1        | PASS   | 76ms     | Sayaç WO #6002 üretildi, birikmeme doğrulandı
T-2        | PASS   | 7ms      | Alt ağaca ve kendine taşıma 409 CYCLE_DETECTED
T-3        | PASS   | 151ms    | 15 eşzamanlı istekten tam 5 kabul, 10 stok reddi
T-4        | PASS   | 45ms     | Örtüşen (10-12 & 11-13) -> 3.0s birleştirildi
--------------------------------------------------------------------------------
TÜM DOĞRULAMALAR BAŞARILI (14 / 14 PASS)!
```

> ⚠️ **Doğrulama veriyi değiştirir.** Aynı veritabanında iki kez koşturmayın; T-2/T-3
> düşer çünkü ekipman zaten taşınmış, stok tüketilmiş olur. Her koşudan önce
> `make reset-demo`.

### Neden ayrı bir araç, neden `go test` değil?

`go test` paket içi fonksiyonları sınar. `cmd/verify` ise **kullanıcının gördüğü sözleşmeyi**
sınar: HTTP durum kodları, JSON alan adları, hata kodları. Ayrıca imaj içinden çalıştığı
için "benim makinemde çalışıyordu" durumunu ortadan kaldırır.

---

## 2. Otomatik testler — `make test`

```bash
make test
# eşdeğeri (DB host'a port açmadığı için testler compose ağında koşar):
docker compose --profile test run --rm test
```

> Testler `test` servisinde, imajın Go araç zincirini içeren `builder`
> katmanında çalışır ve `db:5432` üzerinden bağlanır. `-race` bayrağı cgo
> gerektirdiği için servis içinde `gcc` kurulur.

**53 test**, beş kategori:

| Kategori | Adet | Veritabanı | Ne sınar |
|---|---|---|---|
| `TestUnit*` | 14 | ❌ yok | Saf fonksiyonlar (bcrypt, JWT, interval merge, vardiya, hata zarfı, RBAC) |
| `TestIntegration*` | 16 | ✅ gerçek PostgreSQL | Uçlar + SQL davranışı |
| `TestLifecycle*` | 6 | ✅ | Durum makinesi, atama, geçmiş |
| `TestIdemKey*` | 4 | ✅ | Idempotency zorunluluğu ve kapsamı |
| `TestPaging*` | 7 | ✅ | Keyset sayfalama: tekrar yok, tam gezinme, sınır durumu |
| `TestMV*` | 6 | ✅ | Materyalize view: kaynak seçimi, bayatlama, refresh, RBAC |

**39 test gerçek PostgreSQL'e bağlanır** (spec: en az 10 entegrasyon testi).

Veritabanı erişilemezse entegrasyon testleri **`SKIP`** olur, `FAIL` olmaz. Bu bilinçli:
CI'da PostgreSQL servis olarak tanımlı, yerelde yoksa testler yine anlamlı çalışır.

### Unit testler (14)

| Test | Doğruladığı |
|---|---|
| `TestUnit01` / `02` | bcrypt hash ↔ doğrulama, yanlış parola reddi |
| `TestUnit03` / `04` | JWT üret/parse, kurcalanmış token reddi |
| `TestUnit05`–`09` | Interval merge: boş, tek, örtüşen, iç içe, bitişik, sırasız |
| `TestUnit10`–`12` | Vardiya içi/dışı süre hesabı, 24 saat sürekli |
| `TestUnit13` | Standart hata zarfı `{"error":{"code","message"}}` |
| `TestUnit14` | RBAC: yetkisiz rol → 403 |

`TestUnit05`–`09` özellikle önemli: T-4 metriğinin kalbi olan interval merging'in
sınır durumları (bitişik aralıklar `[10,12]` + `[12,14]` → `[10,14]`) burada kilitlenir.

### Entegrasyon testleri (16)

| Test | Kapsam | Kanıt |
|---|---|---|
| `TestIntegration01` | 5 rol login | Her rol token alır |
| `02` | Idempotency replay | Aynı anahtar → aynı ID |
| `03` | Kullanıcı/uç izolasyonu | Farklı kullanıcı → 409 |
| `04` | Cycle koruması (T-2) | Alt ağaca taşıma → 409 |
| `05` | Seviye matrisi (T-2) | Makine → tesis → 422 |
| `06` | Roll-up doğruluğu (T-2) | Alt ağaç maliyeti = toplam |
| `07` | **Yarış koşulu (T-3)** | 20 eşzamanlı → 4 başarı, 16 red |
| `08` | Tamamlamada stok tüketimi (T-3) | Rezerve → consumed + çıkış hareketi |
| `09` | BOM uyumsuzluğu (T-3) | Yanlış parça → 422 |
| `10` | Sayaç + birikmeme (T-1) | Eşik aşımı → 1 iş emri |
| `11` | `calc_basis=planned` (T-1) | Gecikmeli tamamlama → planlanan tarihe göre |
| `12` | `calc_basis=completed` (T-1) | Fiili tarihe göre |
| `13` | MTBF/örtüşme (T-4) | Örtüşen duruşlar birleşir |
| `14` | Keyset sayfalama (F-3) | `limit` + `cursor` |
| `15` | Teknisyen iş yükü (F-6) | Aktif/tamamlanan sayıları |
| `16` | Audit append-only | `UPDATE/DELETE` trigger ile engellenir |

### Lifecycle testleri (6)

`TestLifecycle01` akış: `acik → atandi → devam_ediyor → tamamlandi`, geçmişte 3 kayıt.
`02` geçersiz geçişler (409). `03` atama doğrulaması (422/404). `04` iptal terminal.
`05` RBAC. `06` iş yükü sayacı.

### Idempotency zorunluluğu testleri (4)

`TestIdemKey01` anahtarsız → 400 `IDEMPOTENCY_KEY_REQUIRED`.
`02` anahtarlı → 201. `03` GET etkilenmez. `04` kapsam 6 farklı kritik uçta doğrulanır.

### Sayfalama testleri (7) — `paging_test.go`

| Test | Kanıtladığı |
|---|---|
| `TestPaging01_BasicPage` | `limit` uygulanır, `has_more` + `next_cursor` tutarlı |
| `02_NoDuplicateAcrossPages` | Sayfalar arası kayıt **tekrar etmez** |
| `03_FullTraversal` | Tüm sayfalar gezilince kayıp yok (50/50 parça) |
| `04_LastPageHasNoCursor` | Son sayfada `next_cursor` null |
| `05_InvalidParams` | Bozuk `cursor`/`limit` → 400 `INVALID_CURSOR` |
| `06_LimitIsCapped` | `limit=5000` → 200'e kırpılır |
| `07_AllPaginatedEndpoints` | 5 uç da aynı sözleşmeye uyar |

**Neden bu testler önemli:** Sayfalama hataları **sessizdir**. Aynı kayıt iki
sayfada görünür veya son kayıt hiç gelmez; kimse fark etmez. Test, tüm sayfaları
gezip benzersizlik sayarak bunu yakalar.

### Materyalize view testleri (6) — `metrics_mv_test.go`

| Test | Kanıtladığı |
|---|---|
| `TestMV01_ReportUsesMaterializedView` | Varsayılan kaynak `materialized_view` |
| `02_FreshParamFallsBackToLive` | `?fresh=true` → `live` |
| `03_StaleUntilRefresh` | **Bayatlama senaryosu**: yeni kayıt → MV görmez → fresh görür → refresh → MV görür |
| `04_RefreshRequiresManager` | Teknisyen refresh edemez (403) |
| `05_RefreshRequiresIdempotencyKey` | Anahtarsız refresh → 400 |
| `06_RefreshStateReportsFreshness` | Tazelik ucu anlamlı değer döner |

`TestMV03` bu grubun kalbi: materyalize view'ın **tek tehlikeli özelliği**
sessiz bayatlamadır. Test, bayatlığı ve yenilemeyi açıkça kanıtlar.

### Race detector

```bash
go test -race ./...
```

`-race`, eşzamanlı erişimleri izler. T-3'ün yarış testi 20 goroutine açtığı için bu
bayrak olmadan test eksik kalır. Son koşu: **temiz** (data race yok).

---

## 3. Yük testi — `make load`

```bash
make load
# hedefi değiştirmek için:
K6_API_URL=http://localhost:8080 make load
```

k6 iki senaryo koşar (farklı uçlar farklı SLO'lara tabi):

| Senaryo | VU | Eşik | Ölçülen |
|---|---|---|---|
| `reliability` (`/metrics/reliability`) | 20 | `p(95) < 250 ms` | **~11 ms** |
| `tree` (`/equipments/tree`) | 5 | `p(95) < 3000 ms` | **~790 ms** (577 KB gövde) |

`http_req_failed` eşiği `rate < 0.01`, `checks` eşiği `rate > 0.99`.

**Neden iki ayrı eşik?** Tek eşik koymak, en yavaş ucun tüm testi belirlemesine yol açar.
577 KB'lık ağaç yanıtı 250 ms eşiğini fiziksel olarak imkânsız kılar; tek eşikle test
sürekli kırmızı kalır ve gerçek regresyonu gizler.

---

## 4. Kapsam matrisi

Her gereksinim en az bir otomatik teste bağlıdır:

| Kimlik | Gereksinim | `make verify` | `go test` | Elle |
|---|---|---|---|---|
| F-1 | Ekipman hiyerarşisi | ✅ | `Integration04` | `/equipments` |
| F-2 | Arıza bildirimi | ✅ | `Integration02` | `/failures` |
| F-3 | İş emri + sayfalama | ✅ | `Integration14`, `Lifecycle01-06` | `/workorders` |
| F-4 | Önleyici bakım | ✅ | `Integration10-12` | `/pm` |
| F-5 | Stok + rezervasyon | ✅ | `Integration07-09` | `/inventory` |
| F-6 | Teknisyen iş yükü | ✅ | `Integration15`, `Lifecycle06` | `/workload` |
| F-7 | MTBF/MTTR/Availability | ✅ | `Integration13` | `/reliability` |
| T-1 | Bakım takvimi | ✅ | `Integration10-12` | sayaç formu |
| T-2 | Ağaç + cycle | ✅ | `Integration04-06` | ağaçta taşıma |
| T-3 | Yarış koşulu | ✅ | `Integration07` | rezervasyon formu |
| T-4 | Interval merge | ✅ | `Unit05-09`, `Integration13` | metrik ekranı |
| — | Auth / RBAC | ✅ | `Unit01-04,14`, `Integration01` | çıkış → başka rolle giriş |
| — | Idempotency | ✅ | `TestIdemKey01-04` | aynı isteği tekrar |
| — | Audit append-only | — | `Integration16` | `UPDATE audit_logs` |

---

## 5. Elle doğrulama (API)

### 5.1 VS Code REST Client

`requests.http` dosyasını açın. Üstteki 5 login isteğini sırayla gönderin (token
değişkenleri dolar), sonra diğer istekleri çalıştırın.

> Problems panelinde `Request 'xxx' has not been sent` uyarıları görebilirsiniz.
> Bunlar **hata değil**, `Information` seviyesinde bilgi: referans verilen istek o
> oturumda henüz çalıştırılmamış demek. Bir kez gönderince kaybolur; kalıcı olarak
> gizlemek için panel filtresini **Errors** yapın.

### 5.2 curl ile kritik akış

```bash
BASE=http://localhost:8080
TOKEN=$(curl -s -X POST $BASE/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"yonetici@bakimplan.local","password":"Password123!"}' \
  | jq -r .access_token)

# İş emri aç (anahtar zorunlu)
curl -s -X POST $BASE/api/v1/work-orders \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"equipment_id":31,"title":"Test","wo_type":"corrective"}'

# Aynı isteği aynı anahtarla tekrar → X-Idempotent-Replay: true
```

### 5.3 Arayüzde elle denenecek senaryolar

Tarayıcıda <http://localhost:3000> açılır. Her senaryo 2-3 dakika sürer.

| # | Adım | Beklenen |
|---|---|---|
| 1 | Giriş ekranı açılır | Header'da **rol değiştirme butonu yok**; 5 demo hesap listelenir |
| 2 | "Depo Sorumlusu"na tıkla → **Giriş Yap** | Sol menüde yalnızca **2 ekran**: Ekipman Ağacı, Yedek Parça & Depo |
| 3 | Adres çubuğuna `/audit` yaz | İlk izinli ekrana geri atar (`/equipments`) |
| 4 | Sol menüde **Yetki Testi** | **İndigo** nötr kutu: "Erişim reddedildi (403) — beklenen … Yetkilendirme doğru çalışıyor." Kırmızı hata **yok** |
| 5 | **Çıkış** → "Teknisyen" ile giriş | Menüde 7 ekran, **Denetim Kaydı yok** |
| 6 | **İş Emirleri** → `acik` satırda **Ata** | Modal açılır, teknisyen listesi (aktif iş sayısıyla) gelir; ata → satır `atandi` olur |
| 7 | Aynı satırda **Başlat** | `atandi → devam_ediyor`, üstte yeşil bilgi kutusu; sonraki durumlar listelenir |
| 8 | Aynı satırda **Tamamla** | `tamamlandi`; PM bağlıysa "Sonraki PM: …" bilgisi |
| 9 | Aynı satırda **Geçmiş** | Modal: her geçiş (önce → sonra), zaman, aktör adı ve rolü |
| 10 | **Arıza Bildirimi** ekranında yeni kayıt | 201, liste başına eklenir |
| 11 | **Yedek Parça & Depo** → "Daha fazla parça" | 25 → 50 kayıt; buton kaybolur, "liste sonu" yazar |
| 12 | **Güvenilirlik & MTBF** | 4 KPI kartı + "En Problemli 10" tablosu; `?fresh` farkı API'de |
| 13 | **Yönetici** ile giriş → **Denetim Kaydı** | Her satır **tek** (mükerrer yok); JSONB alanlar Türkçe etiketli, tarih/kuruş biçimli |
| 14 | Pencereyi daralt (mobil genişlik) | Menü hamburger'a düşer, tablolar yatay kaydırılır |
| 15 | Sayfa yenile (F5) | Oturum korunur (token doğrulanır) |

**Kontrol noktaları:** Her ekranda yükleniyor iskeleti, boş durum metni ve
hata + "tekrar dene" butonu bulunmalı. Konsolda hata olmamalı.

---

## 6. Sürekli entegrasyon (CI)

`.github/workflows/ci.yml` iki iş koşar:

| İş | Adımlar |
|---|---|
| **Backend (Go)** | `golangci-lint v2.15.0` → `go build` → `go test -race ./...` |
| **Frontend (Vue)** | `npm ci` → `npm run build` → artifact yükleme |

PostgreSQL 16.4 servis olarak tanımlı; `DATABASE_URL` env'i üzerinden bağlanır.

**Neden frontend job'ı var?** Bir dönem backend yeşilken frontend derlenmiyordu (boş `.vue`
dosyaları). Test edilmeyen şey bozulur; pipeline'ın kapsamı ürünün kapsamına eşitlenmeli.

---

## 7. Bilinen sınırlar

| Konu | Durum |
|---|---|
| `make verify` idempotent değil | Aynı DB'de tekrar koşarsan T-2/T-3 düşer → `make reset-demo` |
| `verify` veriyi değiştirir | Demo DB'sinde çalıştırın; üretimde kullanmayın |
| 1M satır performansı | Ölçüldü: 93,2 ms → 0,100 ms (README §7.3) |
| Materyalize view tazeliği | Refresh açık çağrıyla; `age_seconds` ile izlenebilir (README §7.6) |
| Mobil erişim | `VITE_API_URL` yeniden derleme gerektirir (README §9) |
| DB host'a port açmaz | Host'tan `go test` çalışmaz; `make test` (Docker) kullanın |
