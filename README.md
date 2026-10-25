# BakımPlan — CMMS (Bakım Onarım Yönetim Sistemi)

**Geliştirici:** Yağız Toprak Yekebağcı
**Proje:** Üniversite Dijital Dönüşüm Ofisi (DDO) Yazılım Ekibi Alım Projesi

Bir üretim tesisinin **ekipman hiyerarşisini** (Tesis → Hat → Makine → Bileşen), **arıza
bildirimlerini**, **iş emirlerini**, **önleyici bakım takvimini**, **yedek parça stoğunu** ve
**güvenilirlik göstergelerini** (MTBF, MTTR, Kullanılabilirlik) uçtan uca yöneten sistem.

**Teknoloji yığını:** Go 1.26 · chi · pgx/v5 (ham SQL, ORM yok) · PostgreSQL 16 ·
Vue 3 `<script setup>` · Vite · TailwindCSS · Docker Compose

---

## 1. Hızlı Başlangıç

Sistem hiçbir manuel veritabanı veya migrasyon adımına ihtiyaç duymaz. Tek komut şemayı
kurar, migrasyonları uygular ve deterministik simülatör ile **2.000 ekipman + 3 yıllık
bakım/arıza geçmişini** yükler.

```bash
git clone https://github.com/retroprp/ddo-case-yagiz-toprak-yekebagci.git
cd ddo-case-yagiz-toprak-yekebagci
cp .env.example .env
docker compose up --build -d
```

İlk açılışta servislerin sağlıklı olmasını bekle:

```bash
docker compose ps            # db, backend, frontend → healthy görünmeli
curl http://localhost:8080/health
```

### Erişim adresleri

| Servis | Adres | Açıklama |
|---|---|---|
| **Yönetim paneli** | <http://localhost:3000> | Vue 3 SPA, 5 rol, 8 ekran |
| **Backend API** | <http://localhost:8080> | Go + chi |
| **Sağlık kontrolü** | <http://localhost:8080/health> | DB ping dahil |
| **PostgreSQL** | `127.0.0.1:5432` | Yalnızca localhost'a bağlı |

> Simülatör tek seferlik çalışır (`RUN_ONCE=true`) ve `Exited (0)` görünür. Bu normaldir;
> deterministik veriyi bozmamak için bilinçli olarak döngüye girmez.

---

## 2. Demo Kullanıcılar

Tüm hesapların parolası: **`Password123!`**
Arayüzün üst barındaki **"Hızlı Rol Geçişi"** butonlarıyla tek tıkla rol değiştirilebilir.

| Rol kodu | E-posta | Yetki kapsamı |
|---|---|---|
| `yonetici` | yonetici@bakimplan.local | Tüm modüller, denetim kayıtları, metrikler |
| `planlama` | planlama@bakimplan.local | Ekipman ekleme/taşıma, iş emri açma ve atama, PM planlama |
| `teknisyen` | teknisyen@bakimplan.local | İş emri başlatma/tamamlama, sayaç okuması, parça rezervasyonu |
| `uretim` | uretim@bakimplan.local | Arıza bildirimi, duruş kaydı, ekipman/metrik görüntüleme |
| `depo` | depo@bakimplan.local | Stok hareketleri, parça rezervasyonu |

---

## 3. API Uçları

Tümü `Authorization: Bearer <jwt>` gerektirir (`/health` ve `/auth/login` hariç).
Değiştirici metotlarda (POST/PATCH/DELETE) **kritik uçlarda `Idempotency-Key` zorunludur**.

### Kimlik

| Metot | Yol | Roller |
|---|---|---|
| `POST` | `/api/v1/auth/login` | — (açık) |
| `GET` | `/api/v1/auth/me` | tümü |

### Ekipman hiyerarşisi (F-1 / T-2)

| Metot | Yol | Roller | Not |
|---|---|---|---|
| `GET` | `/api/v1/equipments/tree` | tümü | `WITH RECURSIVE` + roll-up maliyet/duruş |
| `POST` | `/api/v1/equipments` | planlama, yonetici | Seviye matrisi doğrulanır (422) |
| `PATCH` | `/api/v1/equipments/{id}/move` | planlama, yonetici | Döngü → 409 `CYCLE_DETECTED` |
| `POST` | `/api/v1/equipments/{id}/meter-readings` | teknisyen, planlama, uretim, yonetici | Eşik → otomatik iş emri (T-1) |

### Arıza ve iş emirleri (F-2 / F-3)

| Metot | Yol | Roller | Not |
|---|---|---|---|
| `GET` | `/api/v1/failure-reports` | tümü | Son 100 kayıt |
| `POST` | `/api/v1/failure-reports` | uretim, teknisyen, planlama, yonetici | |
| `GET` | `/api/v1/work-orders` | tümü | Keyset sayfalama (`cursor`, `limit`) |
| `POST` | `/api/v1/work-orders` | planlama, yonetici | |
| `POST` | `/api/v1/work-orders/{id}/complete` | teknisyen, planlama, yonetici | Stok tüketimi + PM yeniden planlama |
| `PATCH` | `/api/v1/work-orders/{id}/status` | teknisyen, planlama, yonetici | Geçiş matrisi |
| `POST` | `/api/v1/work-orders/{id}/assign` | planlama, yonetici | Yalnızca `teknisyen` rolüne |
| `DELETE` | `/api/v1/work-orders/{id}/assign` | planlama, yonetici | Atamayı kaldır → `acik` |
| `GET` | `/api/v1/work-orders/{id}/events` | tümü | Durum geçmişi, aktör adıyla |

### Önleyici bakım (F-4 / T-1)

| Metot | Yol | Roller |
|---|---|---|
| `GET` | `/api/v1/preventive-plans` | tümü |
| `POST` | `/api/v1/preventive-plans` | planlama, yonetici |
| `POST` | `/api/v1/preventive-plans/trigger-due` | planlama, yonetici |

### Stok (F-5 / T-3)

| Metot | Yol | Roller | Not |
|---|---|---|---|
| `GET` | `/api/v1/parts` | tümü | Stok = `SUM(stock_movements.quantity_change)` |
| `GET` | `/api/v1/warehouses` | tümü | Salt okunur |
| `POST` | `/api/v1/stock-movements` | depo, yonetici | |
| `POST` | `/api/v1/work-orders/{id}/reserve-part` | depo, teknisyen, planlama, yonetici | `SELECT ... FOR UPDATE` |

### İş yükü ve metrikler (F-6 / F-7 / T-4)

| Metot | Yol | Roller | Not |
|---|---|---|---|
| `GET` | `/api/v1/technicians/workload` | teknisyen, planlama, yonetici | |
| `GET` | `/api/v1/metrics/reliability` | tümü | `?equipment_id=&days=` |
| `POST` | `/api/v1/downtime-events` | teknisyen, planlama, uretim, yonetici | |
| `PATCH` | `/api/v1/downtime-events/{id}` | planlama, yonetici | Metrikler anında yeniden hesaplanır |

### Denetim

| Metot | Yol | Roller | Not |
|---|---|---|---|
| `GET` | `/api/v1/audit-logs` | yonetici | Append-only (trigger korumalı) |

Tam istek koleksiyonu: [`requests.http`](requests.http) — 5 rol token'ı, 51 istek, hazır assertion'lar.

---

## 4. Hata Formatı

Tüm uçlar tek bir zarf kullanır:

```json
{
  "error": {
    "code": "INSUFFICIENT_STOCK",
    "message": "Depoda yeterli kullanılabilir stok yok. Mevcut: 1, İstenen: 2"
  }
}
```

Sık karşılaşılan kodlar:

| Kod | HTTP | Anlamı |
|---|---|---|
| `ERR_*` / `VALIDATION_ERROR` | 400 | Gövde veya parametre hatası |
| `UNAUTHORIZED` / `INVALID_CREDENTIALS` | 401 | Token yok / geçersiz |
| `FORBIDDEN_ROLE` | 403 | Rol yetkisiz |
| `WO_NOT_FOUND` / `PART_NOT_FOUND` | 404 | Kayıt yok |
| `INVALID_TRANSITION` | 409 | Durum makinesine aykırı geçiş |
| `CYCLE_DETECTED` | 409 | Ağaçta döngü |
| `INSUFFICIENT_STOCK` | 409 | Stok yetmez |
| `IDEMPOTENCY_KEY_REQUIRED` | 400 | Kritik uçta anahtar yok |
| `IDEMPOTENCY_CONFLICT` | 409 | Aynı anahtar farklı gövde |
| `BOM_INCOMPATIBLE` | 422 | Parça ekipmanla uyumsuz |
| `INVALID_HIERARCHY_LEVEL` | 422 | Yanlış seviye (ör. makine → tesis) |

---

## 5. Mimari

```
┌──────────────┐    HTTP/JSON     ┌──────────────┐    pgx     ┌──────────────┐
│  Vue 3 SPA   │ ───────────────▶ │  Go API      │ ─────────▶ │ PostgreSQL16 │
│  nginx:8080  │ ◀─────────────── │  chi:8080    │ ◀───────── │  :5432       │
└──────────────┘                  └──────────────┘            └──────────────┘
                                         ▲
                                         │ CopyFrom (bulk)
                                  ┌──────┴───────┐
                                  │  simulator   │  tek seferlik, SEED_VALUE=42
                                  └──────────────┘
```

### Klasör yapısı

```
backend/
  cmd/api/            HTTP sunucusu, route tanımları
  cmd/simulator/      deterministik veri üreteci (2000 ekipman, 3 yıl geçmiş)
  cmd/verify/         uçtan uca PASS/FAIL doğrulama aracı (F-1..F-7, T-1..T-4)
  internal/auth/      JWT üretimi/doğrulaması, bcrypt, rol sabitleri
  internal/config/    ortam değişkenleri
  internal/database/  pgxpool + gömülü sürümlü migrasyon çalıştırıcısı
  internal/handlers/  HTTP handler'ları (ekipman, stok, bakım, metrik, lifecycle)
  internal/middleware/idempotency, audit, hata zarfı
  internal/httpx/     JSON/hata yardımcıları
frontend/
  src/views/          ReliabilityView, EquipmentTreeView, OperationsView, InventoryAndAuditView
  src/components/     EquipmentNode (özyinelemeli ağaç düğümü)
  src/router/         vue-router + rol guard
  src/store.js        reaktif state + apiFetch sarmalayıcı
```

### Neden ORM yok?

`WITH RECURSIVE` ağaç sorguları, `SELECT ... FOR UPDATE` satır kilitleme, partial unique
index ve `CopyFrom` toplu yazma bu projenin merkezinde. ORM bunların hepsinde soyutlama
sızdırır ve N+1 riski getirir. Ayrıntı: [`DECISIONS.md`](DECISIONS.md).

---

## 6. Zorlu Görevler Nasıl Çözüldü

### T-1 · Önleyici bakım takvimi

- Süre (`interval_days`) ve sayaç (`interval_hours`) bazlı planlar; `calc_basis`
  `planned` veya `completed` olabilir.
- **Geciken bakım birikmez:** `work_orders` üzerinde kısmi tekil indeks
  ```sql
  CREATE UNIQUE INDEX uq_active_pm_work_order
      ON work_orders (preventive_plan_id)
      WHERE preventive_plan_id IS NOT NULL
        AND status IN ('acik', 'atandi', 'devam_ediyor');
  ```
  Sayaç eşiği aşıldığında `INSERT ... ON CONFLICT DO NOTHING` çalışır: plan için zaten açık
  iş emri varsa ikincisi oluşmaz. Kural **veritabanı motorunda** garanti edilir, uygulama
  kontrolüne bırakılmaz.

### T-2 · Ekipman hiyerarşisi

- `WITH RECURSIVE` ile alt ağaç maliyeti ve duruş süresi yukarı toplanır (roll-up).
- Taşımada iki koruma: (1) kendi alt ağacına taşıma → 409 `CYCLE_DETECTED`,
  (2) seviye matrisi ihlali (ör. makine → tesis) → 422 `INVALID_HIERARCHY_LEVEL`.
- Roll-up doğruluğu SQL ile birebir karşılaştırılarak test edilir.

### T-3 · Stok ve rezervasyon

- Stok bir kolon değil, **defter toplamı**: `SUM(quantity_change)`.
- Rezervasyonda `SELECT ... FOR UPDATE` ile parça satırı kilitlenir, kilit altında
  `available = on_hand - reserved` yeniden hesaplanır.
- Kanıt: 20 eşzamanlı istek, 4 adet stok → **tam 4 başarı, 16 red, aşım 0**.

### T-4 · MTBF / MTTR / Kullanılabilirlik

- Örtüşen ve iç içe geçen duruş aralıkları **birleştirilir** (interval merging), böylece
  çift sayım olmaz.
- Vardiya dışı süreler paydadan düşülür.
- Geçmiş bir olay güncellendiğinde metrikler anında yeniden hesaplanır (anlık hesap,
  materyalize kopya yok → tutarsızlık riski yok).

---

## 7. Performans

Ölçümler `EXPLAIN (ANALYZE, BUFFERS)` ile alınmıştır.

### Seed verisi (2.000 ekipman, 6.000 iş emri)

| Sorgu | Plan | Süre |
|---|---|---|
| Ekipman ağacı + roll-up | Recursive Union + Index Only Scan | ~41 ms |
| En problemli 10 ekipman | Hash Join + HashAggregate | ~3,7 ms |
| Keyset sayfalama (`id < $1 LIMIT 50`) | Index Scan Backward | ~0,05 ms |

### 1.000.000 iş emri ile ölçek testi

| Yaklaşım | Plan | Süre |
|---|---|---|
| Doğrudan `work_orders` taraması | Parallel Seq Scan | **114 ms** |
| Materyalize view + `REFRESH` | MV üzerinden okuma | **1,1 ms** (refresh maliyeti 85 ms) |

**Yorum:** Mevcut `idx_wo_equipment_type_status (equipment_id, wo_type, status)` indeksi
roll-up sorgusunda **Index Only Scan** ile kullanılıyor (heap fetch 0), ancak "en problemli
10 ekipman" raporunda `WHERE wo_type = ... AND status <> ...` öneki olmadığı için tarama
planına düşüyor. 6.000 satırda bu fark önemsiz (3,7 ms); 1 milyon satırda rapor ucunun
materyalize view üzerinden okunması ~100× kazanç sağlıyor. Rapor ucu için doğru takas bu;
gerçek zamansal doğruluk gerektiren uçlar (iş emri listesi, stok) doğrudan tablodan okumaya
devam ediyor.

---

## 8. Test ve Doğrulama

```bash
make verify        # uçtan uca F-1..F-7 + T-1..T-4 + auth tablosu
make test          # 40 test: unit + gerçek PostgreSQL entegrasyonu (race detector)
make reset-demo    # veritabanını sıfırla, deterministik seed ile yeniden kur
make load          # k6 yük testi (grafana/k6)
```

Mevcut durum: **40 test PASS**, `make verify` → **14 / 14 PASS**.
Ayrıntı ve senaryo tablosu: [`TESTING.md`](TESTING.md).

---

## 9. Telefondan (Mobil) Erişim

Uygulama responsive tasarlandı; ancak `localhost` telefondan **telefonun kendisi** demektir.
Bu yüzden API adresini bilgisayarın yerel IP'si ile yeniden derlemek gerekir.

```bash
# 1) Bilgisayarın yerel IP'sini bul (Windows)
ipconfig | findstr IPv4        # örn. 192.168.1.42

# 2) Frontend'i bu adresle yeniden derle
#    .env dosyasına ekle:
#    VITE_API_URL=http://192.168.1.42:8080/api/v1
docker compose up -d --build frontend

# 3) Windows Güvenlik Duvarı'nda 3000 ve 8080 portlarına izin ver
# 4) Telefonun tarayıcısından aç:
#    http://192.168.1.42:3000
```

Telefon ve bilgisayar **aynı Wi-Fi ağında** olmalı. Ayrıntılı anlatım ve sorun giderme
için [Sorun Giderme → Mobil](#mobil-telefondan-açılmıyor) bölümüne bakın.

---

## 10. Sorun Giderme

### `docker compose up` sonrası frontend açılmıyor

```bash
docker compose ps                       # frontend "healthy" mi?
docker compose logs --tail=50 frontend
curl -I http://localhost:3000           # 200 dönmeli
```

### Port zaten kullanımda (`bind: address already in use`)

Başka bir PostgreSQL veya eski bir stack çalışıyor olabilir:

```bash
docker ps --format "{{.Names}} {{.Ports}}"
docker compose down -v                  # bu projenin stack'ini kapat
netstat -ano | findstr :5432            # başka dinleyen var mı?
```

### Veritabanı eski şemada kaldı

```bash
docker compose down -v && docker compose up --build -d
```

Migrasyonlar sürümlüdür (`schema_migrations`). Mevcut volume'ler otomatik yükseltilir;
yine de temiz başlangıç en güvenilir yoldur.

### `make verify` bazı satırlarda FAIL veriyor

Doğrulama aracı veriyi **değiştirir** (ekipman taşır, stok tüketir). Aynı veritabanında iki
kez koşturursan T-2/T-3 düşer. Her koşudan önce:

```bash
make reset-demo
make verify
```

### `401 Unauthorized` — token süresi

JWT varsayılan 24 saat geçerlidir. Panelde **Çıkış** → tekrar giriş yapın.

### `403 FORBIDDEN_ROLE`

Rolünüz o uca yetkili değil. Tablo: [API Uçları](#3-api-uçları). Bu bir hata değil,
tasarım gereği.

### `400 IDEMPOTENCY_KEY_REQUIRED`

Kritik uçlarda `Idempotency-Key` başlığı zorunlu. Panel otomatik gönderir; elle `curl`
kullanıyorsanız başlığı ekleyin:

```bash
curl -X POST http://localhost:8080/api/v1/work-orders \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{"equipment_id":31,"title":"Test"}'
```

### `409 IDEMPOTENCY_CONFLICT`

Aynı anahtar farklı gövdeyle kullanılmış. Ya aynı gövdeyi gönderin ya yeni anahtar üretin.

### `422 BOM_INCOMPATIBLE`

Ekipmanın BOM'unda bu parça yok. Önce BOM tanımlanmalı (`equipment_parts_bom`).

### `422 INVALID_HIERARCHY_LEVEL`

Seviye kuralı: `tesis → hat → makine → bilesen`. Makine doğrudan tesis altına bağlanamaz.

### Mobil (telefondan) açılmıyor

1. **API adresi:** Telefonda `localhost` çalışmaz. `VITE_API_URL` bilgisayarın IP'si
   olmalı; değiştirdikten sonra `docker compose up -d --build frontend`.
2. **Güvenlik duvarı:** Windows Defender → Gelen Kuralları → 3000 ve 8080 TCP izinli mi?
   Hızlı test: telefondan `http://<IP>:8080/health` açılıyorsa backend erişilebilir.
3. **Aynı ağ:** Telefon mobil veride değil, aynı Wi-Fi'da olmalı. Misafir ağı (guest
   network) cihazlar arası trafiği çoğu zaman bloklar.
4. **Docker binding:** `docker compose ps` çıktısında `0.0.0.0:3000->8080/tcp` görmelisiniz.
   `127.0.0.1:3000->...` yazıyorsa yalnızca bilgisayardan erişilebilir.

---

## 11. Yapılandırma

`.env` (`.env.example`'dan kopyalanır):

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | cmms_user / cmms_secret_pass / bakimplan_db | DB kimliği |
| `DATABASE_URL` | `postgres://…@db:5432/bakimplan_db` | API ve simülatör bağlantısı |
| `JWT_SECRET` | demo değeri | **Üretimde değiştirin** |
| `SEED_VALUE` | `42` | Simülatör determinizm tohumu |
| `VITE_API_URL` | `http://localhost:8080/api/v1` | Tarayıcının göreceği API adresi |
| `K6_API_URL` | `http://backend:8080` | Yük testi hedefi |

---

## 12. Dokümanlar

| Dosya | İçerik |
|---|---|
| [`DECISIONS.md`](DECISIONS.md) | Mimari karar kayıtları (ADR) |
| [`TESTING.md`](TESTING.md) | Test stratejisi, senaryo tablosu, kanıtlar |
| [`openapi.yaml`](openapi.yaml) | OpenAPI 3.0 sözleşmesi |
| [`requests.http`](requests.http) | VS Code REST Client koleksiyonu |

---

## 13. Lisans

Bu proje bir değerlendirme çalışması olarak geliştirilmiştir.
