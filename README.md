# BakımPlan — CMMS (Bakım Onarım Yönetim Sistemi)

**Geliştirici:** Yağız Toprak Yekebağcı
**Proje:** Üniversite Dijital Dönüşüm Ofisi (DDO) Yazılım Ekibi Alım Projesi

Bir üretim tesisinin **ekipman hiyerarşisini** (Tesis → Hat → Makine → Bileşen), **arıza
bildirimlerini**, **iş emirlerini**, **önleyici bakım takvimini**, **yedek parça stoğunu** ve
**güvenilirlik göstergelerini** (MTBF, MTTR, Kullanılabilirlik) uçtan uca yöneten sistem.

**Teknoloji yığını:** Go 1.26 · chi · pgx/v5 (ham SQL, ORM yok) · PostgreSQL 16 ·
Vue 3 `<script setup>` · Vite · TailwindCSS · Docker Compose

### Teknoloji seçimleri (gerekçeli)

| Seçim | Gerekçe | Değerlendirilen alternatif |
|---|---|---|
| **Go + chi** | `net/http` uyumlu, `RoutePattern()` verir (idempotency kapsamını yol şablonuyla eşlemek için gerekli). Derlenen tek binary, düşük bellek | gin/echo/fiber: chi yeterli ve standart kütüphaneye en yakın |
| **pgx/v5, ham SQL** | `WITH RECURSIVE`, `SELECT … FOR UPDATE`, partial index, `CopyFrom` bu projenin merkezinde; ORM bunların hepsinde soyutlama sızdırır | GORM (N+1 riski, CTE desteği zayıf), sqlc (üretilen kod okunaksız) |
| **PostgreSQL 16** | Zorunlu. Kısmi tekil indeks, `ON CONFLICT`, `REFRESH … CONCURRENTLY`, JSONB, trigger | — |
| **Vue 3 `<script setup>` + Vite** | Kurumun (DDO) frontend standardı; `ref`/`computed` ile düşük boilerplate; Vite ile anlık HMR ve küçük bundle (166 KB) | React: ekip standardı Vue; Svelte: bileşen ekosistemi daha dar |
| **TailwindCSS (build-time)** | Utility-first → CSS büyümez (22 KB), responsive kırılımlar tek satırda. **CDN değil**: CDN production için değil, offline çalışmaz, CSP ihlali yapar | Bootstrap: özelleştirme maliyeti yüksek |
| **Reaktif `store` (Pinia değil)** | State yüzeyi küçük (oturum + 2 banner). Pinia'nın devtools/SSR getirisi maliyetinden az | Pinia: gerekirse tek dosyada izole olduğu için geçiş ~20 dk |
| **`fetch` (Axios değil)** | Tek sarmalayıcı (`apiFetch`) hata zarfını, idempotency başlığını ve `silent` modunu yönetiyor; ek bağımlılık gereksiz | Axios: interceptor avantajı burada `fetch` ile 20 satırda karşılanıyor |

Ayrıntılı mimari kararlar: [`DECISIONS.md`](DECISIONS.md) (10 ADR).

---

## 1. Hızlı Başlangıç

Sistem hiçbir manuel veritabanı, migrasyon veya seed adımına ihtiyaç duymaz. Depo
klonlandıktan sonra **tek komut** ile ayağa kalkar.

### 1.1. Depoyu edinme (ön koşul)

Kodu indirip proje dizinine geçin:

```bash
git clone https://github.com/retroprp/ddo-case-yagiz-toprak-yekebagci.git
cd ddo-case-yagiz-toprak-yekebagci
```

### 1.2. Tek komutla başlatma

```bash
cp .env.example .env
docker compose up --build -d
```

Bu komut şunların **hepsini** kendisi yapar:

| Adım | Nasıl |
|---|---|
| Şemayı kurar | Gömülü migrasyonlar (`001`, `002`) ilk açılışta uygulanır |
| 2.000 ekipmanı yükler | Simülatör servisi `SEED_VALUE=42` ile deterministik veri üretir |
| Servisleri sıraya koyar | `depends_on: service_healthy` — DB hazır olmadan API başlamaz |
| Ağ ve portları açar | `3000` (panel), `8080` (API). **DB host'a açılmaz** — yalnızca compose ağında (`db:5432`) |

Manuel adım yok: `npm install`, `psql < schema.sql`, `migrate`, `seed` gibi komutların
hiçbirini yazmanız gerekmez.

#### İsteğe bağlı: ayarları özelleştirme

Varsayılanlarla çalışmak istiyorsanız ek adım gerekmez — `docker-compose.yml` içindeki
`${VAR:-varsayılan}` değerleri kullanılır. Ayar değiştirecekseniz (JWT anahtarı, seed
değeri, telefondan erişim için API adresi vb.) `.env` oluşturun:

```bash
cp .env.example .env    # sonra düzenleyin; .env git'e girmez
docker compose up -d --build
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
| **PostgreSQL** | `db:5432` (yalnızca compose ağı) | Host'a port açılmaz; `docker compose exec db psql …` ile erişilir |

> **Simülatör artık ayakta kalır.** Seed bittikten sonra kapanmaz; 5 saniyede bir
> `simulator_state` tablosuna "hâlâ çalışıyorum" sinyali (heartbeat) yazar.
> Bu sayede arayüzdeki **Canlı Sistem** rozeti gerçek durumu gösterir
> (`Canlı · 3 sn`), ama üretilen veri **değişmez** — determinizm korunur.
> Durumu API'den de okuyabilirsiniz: `GET /api/v1/simulator/status`.

---

## 2. Demo Kullanıcılar

Tüm hesapların parolası: **`Password123!`**

Giriş ekranındaki **"Demo Hesaplar"** listesine tıklamak yalnızca formu doldurur; girişi
siz başlatırsınız. Rol değiştirmek için **Çıkış** yapıp diğer hesapla giriş yapın —
oturum sırasında rol değiştirilemez.

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

### Rapor altyapısı

| Metot | Yol | Roller | Not |
|---|---|---|---|
| `POST` | `/api/v1/admin/refresh-metrics` | yonetici | Materyalize view yenileme (`Idempotency-Key` zorunlu) |
| `GET` | `/api/v1/admin/refresh-metrics` | yonetici | Raporun tazeliği (`age_seconds`, `refreshed_at`) |

### Simülatör durumu

| Metot | Yol | Roller | Not |
|---|---|---|---|
| `GET` | `/api/v1/simulator/status` | tüm oturumlar | Canlılık sinyali: `running`, `age_seconds`, `ticks`, `mode`, `seed` |

Örnek yanıt:

```json
{
  "running": true,
  "mode": "deterministic",
  "seed": 42,
  "ticks": 570,
  "last_tick": "2026-10-26T18:35:12.482Z",
  "age_seconds": 1,
  "stale_after_s": 30
}
```

Arayüz bu ucu 5 saniyede bir okur ve üst bardaki rozeti günceller. Sinyal 30
saniyeden eskiyse rozet **"Simülatör bekliyor"** olur — yanlış "canlı" izlenimi
verilmez. Yanıtın `ticks` alanı her 5 saniyede bir artar; bu, servisin gerçekten
ayakta olduğunun kanıtıdır.

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
└──────────────┘                  └──────▲───────┘            └──────▲───────┘
       │                                 │                           │
       │  GET /simulator/status          │                    CopyFrom (bulk,
       │  (rozet, 5 sn'de bir)           │                     yalnızca ilk kurulum)
       │                                 │                           │
       └─────────────┐            ┌──────┴───────────────────────────┴──────┐
                     └───────────▶│  simulator  (SEED_VALUE=42)             │
                                  │  seed biter → heartbeat döngüsü (5 sn)  │
                                  └────────────────┬────────────────────────┘
                                                   │ SELECT / UPDATE
                                                   ▼
                                          [simulator_state]
                                          (mode, seed, ticks, last_tick)
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

Tüm ölçümler canlı veritabanında `EXPLAIN (ANALYZE, BUFFERS)` ile alınmıştır;
çıktılar aşağıda **olduğu gibi** yapıştırılmıştır (özetlenmemiş, kırpılmamış).

### 7.1 Seed verisi (2.000 ekipman, 8.500 iş emri)

| Sorgu | Plan | Execution Time |
|---|---|---|
| T-2 ekipman ağacı + roll-up | Recursive Union + Index Only Scan | **59,8 ms** |
| Keyset sayfalama (`id < 8500 LIMIT 50`) | Index Scan Backward | **0,050 ms** |
| OFFSET sayfalama (`OFFSET 8000 LIMIT 50`) | Index Scan Backward, **8.050 satır okundu** | **0,896 ms** |
| En problemli 10 — materyalize view | Index Scan (`mv_..._rank_idx`) | **0,078 ms** |
| En problemli 10 — canlı sorgu | Hash Join + HashAggregate | **3,904 ms** |

#### Dizin 1 — `idx_wo_equipment_type_status` (T-2 roll-up)

Tasarım: `(equipment_id, wo_type, status) INCLUDE (labor_cost_kurus, parts_cost_kurus)`
Hedef sorgu: recursive ağaç + alt ağaç maliyet/duruş toplamı.

```
 Limit  (cost=372879.80..372879.81 rows=5 width=44) (actual time=32.969..32.973 rows=5 loops=1)
   Buffers: shared hit=554 dirtied=6
   CTE tree
     ->  Recursive Union  (cost=0.00..34833.68 rows=1336664 width=12) (actual time=0.010..2.217 rows=7783 loops=1)
           Buffers: shared hit=56
           ->  Seq Scan on equipments  (cost=0.00..48.24 rows=2024 width=12) (actual time=0.009..0.232 rows=2098 loops=1)
                 Buffers: shared hit=28
           ->  Hash Join  (cost=73.54..2141.88 rows=133464 width=12) (actual time=0.118..0.323 rows=1421 loops=4)
                 Hash Cond: (t_1.id = e.parent_id)
                 Buffers: shared hit=28
                 ->  WorkTable Scan on tree t_1  (cost=0.00..404.80 rows=20240 width=8) (actual time=0.000..0.059 rows=1946 loops=4)
                 ->  Hash  (cost=48.24..48.24 rows=2024 width=8) (actual time=0.335..0.335 rows=2009 loops=1)
                       Buckets: 2048  Batches: 1  Memory Usage: 95kB
                       Buffers: shared hit=28
                       ->  Seq Scan on equipments e  (cost=0.00..48.24 rows=2024 width=8) (actual time=0.017..0.144 rows=2098 loops=1)
                             Buffers: shared hit=28
   ->  Sort  (cost=338046.12..338046.62 rows=200 width=44) (actual time=13.612..13.614 rows=5 loops=1)
         Sort Key: t.root_id
         Sort Method: top-N heapsort  Memory: 25kB
         Buffers: shared hit=554 dirtied=6
         ->  HashAggregate  (cost=338040.30..338042.80 rows=200 width=44) (actual time=13.152..13.422 rows=2098 loops=1)
               Group Key: t.root_id
               Batches: 1  Memory Usage: 641kB
               Buffers: shared hit=554 dirtied=6
               ->  Merge Right Join  (cost=181017.50..276829.52 rows=6121078 width=20) (actual time=4.711..10.718 rows=25577 loops=1)
                     Merge Cond: (w.equipment_id = t.id)
                     Buffers: shared hit=554 dirtied=6
                     ->  Index Only Scan using idx_wo_equipment_type_status on work_orders w  (cost=0.29..633.05 rows=8568 width=20) (actual time=0.048..2.487 rows=8523 loops=1)
                           Heap Fetches: 2442
                           Buffers: shared hit=498 dirtied=6
                     ->  Materialize  (cost=181017.21..187700.53 rows=1336664 width=8) (actual time=4.647..6.043 rows=25577 loops=1)
                           Buffers: shared hit=56
                           ->  Sort  (cost=181017.21..184358.87 rows=1336664 width=8) (actual time=4.642..4.904 rows=7783 loops=1)
                                 Sort Key: t.id
                                 Sort Method: quicksort  Memory: 436kB
                                 Buffers: shared hit=56
                                 ->  CTE Scan on tree t  (cost=0.00..26733.28 rows=1336664 width=8) (actual time=0.013..3.472 rows=7783 loops=1)
                                       Buffers: shared hit=56
 Planning Time: 1.541 ms
 JIT:
   Functions: 23
   Timing: Generation 1.180 ms, Inlining 0.000 ms, Optimization 0.518 ms, Emission 18.872 ms, Total 20.571 ms
 Execution Time: 59.774 ms
```

Dikkat: `Index Only Scan using idx_wo_equipment_type_status` — tabloya hiç
gidilmiyor (covering index sayesinde maliyet kolonları indeksten okunuyor).
`Heap Fetches: 2442` görünüyor çünkü az önce eklenen test satırları henüz
visibility map'e işlenmemiş; `VACUUM` sonrası 0'a düşer.

#### Dizin 2 — `mv_equipment_wo_stats_rank_idx` (T-4 "en problemli 10")

Tasarım: `(failure_wo_count DESC, total_cost_kurus DESC)` — sorgunun sıralamasıyla
birebir aynı. Hedef sorgu: `ORDER BY failure_wo_count DESC, total_cost_kurus DESC LIMIT 10`.

```
 Limit  (cost=0.56..5.09 rows=10 width=66) (actual time=0.021..0.048 rows=10 loops=1)
   Buffers: shared hit=37
   ->  Nested Loop  (cost=0.56..753.25 rows=1659 width=66) (actual time=0.021..0.047 rows=10 loops=1)
         Buffers: shared hit=37
         ->  Index Scan using mv_equipment_wo_stats_rank_idx on mv_equipment_wo_stats m  (cost=0.28..119.95 rows=1659 width=20) (actual time=0.009..0.015 rows=10 loops=1)
               Index Cond: (failure_wo_count > 0)
               Buffers: shared hit=7
         ->  Index Scan using equipments_pkey on equipments e  (cost=0.28..0.38 rows=1 width=50) (actual time=0.003..0.003 rows=1 loops=10)
               Index Cond: (id = m.equipment_id)
               Buffers: shared hit=30
 Planning Time: 0.463 ms
 Execution Time: 0.078 ms
```

Bu indeks olmadan aynı sorgu (materyalize view üzerinde bile) sıralama için
tüm görünümü okuyup `Sort` yapardı. İndeks sayesinde `LIMIT 10` ilk 10 satırda
duruyor: **0,078 ms**.

### 7.2 Keyset vs OFFSET (aynı sayfa, aynı veri)

```
-- Keyset: WHERE id < 8500 ORDER BY id DESC LIMIT 50
 Limit  (cost=0.29..2.91 rows=50 width=23) (actual time=0.022..0.036 rows=50 loops=1)
   Buffers: shared hit=9
   ->  Index Scan Backward using work_orders_pkey on work_orders  (cost=0.29..445.06 rows=8481 width=23) (actual time=0.021..0.033 rows=50 loops=1)
         Index Cond: (id < 8500)
         Buffers: shared hit=9
 Execution Time: 0.050 ms

-- OFFSET: ORDER BY id DESC OFFSET 8000 LIMIT 50
 Limit  (cost=397.93..400.41 rows=50 width=23) (actual time=1.595..1.608 rows=50 loops=1)
   Buffers: shared hit=258 dirtied=1
   ->  Index Scan Backward using work_orders_pkey on work_orders  (cost=0.29..426.16 rows=8568 width=23) (actual time=0.018..1.425 rows=8050 loops=1)
         Buffers: shared hit=258 dirtied=1
 Execution Time: 0.896 ms
```

`rows=8050` — OFFSET 8.050 satırı gerçekten okudu ve attı. Keyset'te `rows=50`.
Fark sayfa derinliğiyle büyür; 1 milyon satırda 100.000. OFFSET = 100.000 satır okuma.

### 7.3 1.000.000 iş emri ile ölçek testi

Gerçek satır sayısı: **1.008.523**. Ölçüm sonrası veritabanı `down -v` + seed ile
temizlendi (tekrarlanabilirlik için).

| Yaklaşım | Plan | Execution Time |
|---|---|---|
| Canlı `work_orders` taraması | **Parallel Seq Scan, 3 worker** (253.294 satır/worker) | **93,2 ms** |
| Materyalize view + sıralama indeksi | Index Scan → 10 satır | **0,100 ms** |
| `refresh_mv_equipment_wo_stats()` | 1M satır agregasyonu | **86,4 ms** |

**~932× fark** (93,2 / 0,100).

#### Canlı sorgu (1M satır)

```
 Limit  (cost=27004.31..27004.33 rows=10 width=66) (actual time=101.821..105.000 rows=10 loops=1)
   Buffers: shared hit=14873 read=122 written=33
   ->  Sort  (cost=27004.31..27009.37 rows=2024 width=66) (actual time=101.819..104.997 rows=10 loops=1)
         Sort Key: (count(w.id)) DESC, ((COALESCE(sum((w.labor_cost_kurus + w.parts_cost_kurus)), '0'::numeric))::bigint) DESC
         Sort Method: top-N heapsort  Memory: 27kB
         Buffers: shared hit=14873 read=122 written=33
         ->  Finalize GroupAggregate  (cost=26417.43..26960.57 rows=2024 width=66) (actual time=97.710..104.624 rows=1986 loops=1)
               Group Key: e.id
               ->  Gather Merge  (cost=26417.43..26889.73 rows=4048 width=90) (actual time=97.697..101.651 rows=5186 loops=1)
                     Workers Planned: 2
                     Workers Launched: 2
                     ->  Sort  (cost=25417.41..25422.47 rows=2024 width=90) (actual time=94.579..94.707 rows=1729 loops=3)
                           Sort Key: e.id
                           Sort Method: quicksort  Memory: 279kB
                           ->  Partial HashAggregate  (cost=25280.96..25306.26 rows=2024 width=90) (actual time=93.762..94.022 rows=1729 loops=3)
                                 Group Key: e.id
                                 Batches: 1  Memory Usage: 625kB
                                 ->  Hash Join  (cost=73.54..22103.61 rows=317735 width=74) (actual time=0.616..69.431 rows=253294 loops=3)
                                       Hash Cond: (w.equipment_id = e.id)
                                       ->  Parallel Seq Scan on work_orders w  (cost=0.00..21194.27 rows=315868 width=28) (actual time=0.006..31.108 rows=253294 loops=3)
                                             Filter: (((status)::text <> 'iptal'::text) AND ((wo_type)::text = 'corrective'::text))
                                             Rows Removed by Filter: 82880
                                             Buffers: shared hit=14891
                                       ->  Hash  (cost=48.24..48.24 rows=2024 width=50) (actual time=0.545..0.545 rows=2098 loops=3)
                                             Buckets: 4096 (originally 2048)  Batches: 1 (originally 1)  Memory Usage: 204kB
                                             ->  Seq Scan on equipments e  (cost=0.00..48.24 rows=2024 width=50) (actual time=0.008..0.193 rows=2098 loops=3)
                                                   Buffers: shared hit=84
 Planning Time: 0.817 ms
 Execution Time: 93.229 ms
```

`Parallel Seq Scan … Rows Removed by Filter: 82880` — tüm tablo taranıyor,
1M satırın 3 worker'a bölünmüş hâli.

#### Materyalize view (1M satır)

```
 Limit  (cost=0.56..5.46 rows=10 width=66) (actual time=0.014..0.055 rows=10 loops=1)
   Buffers: shared hit=38
   ->  Nested Loop  (cost=0.56..954.38 rows=1947 width=66) (actual time=0.014..0.054 rows=10 loops=1)
         Buffers: shared hit=38
         ->  Index Scan using mv_equipment_wo_stats_rank_idx on mv_equipment_wo_stats m  (cost=0.28..224.90 rows=1985 width=20) (actual time=0.008..0.030 rows=10 loops=1)
               Index Cond: (failure_wo_count > 0)
               Buffers: shared hit=8
         ->  Index Scan using equipments_pkey on equipments e  (cost=0.28..0.37 rows=1 width=50) (actual time=0.002..0.002 rows=1 loops=10)
               Index Cond: (id = m.equipment_id)
               Buffers: shared hit=30
 Planning Time: 0.316 ms
 Execution Time: 0.100 ms
```

`shared hit=38` vs canlı sorgunun `shared hit=14873 read=122` — 400× daha az
sayfa erişimi. Süre farkı buradan geliyor.

### 7.4 Doğruluk: MV yalan söylemiyor

1M satırda canlı sorgu ile MV sonucu karşılaştırıldı:

```sql
SELECT count(*) FROM (SELECT * FROM live EXCEPT SELECT * FROM mv) x;  -- 0
SELECT count(*) FROM (SELECT * FROM mv EXCEPT SELECT * FROM live) y;  -- 0
```

İki yönde de 0 fark. MV aynı sayıyı hesaplıyor; sadece ne zaman hesapladığı farklı.

### 7.5 Neden materyalize view, neden indeks yetmedi?

Mevcut `idx_wo_equipment_type_status (equipment_id, wo_type, status)` indeksi
roll-up sorgusunda **Index Only Scan** ile kullanılıyor (§7.1), ama rapor
sorgusunda kullanılamıyor: öneki `equipment_id` olan bir indekste `wo_type` ve
`status` yalnızca **eşitlik koşulu** olarak kullanılabilir, **filtre** olarak
değil. Sorgu `WHERE wo_type = ... AND status <> ...` dediği için (biri eşitlik,
biri eşitsizlik) tarama planına düşüyor.

Denenen alternatifler:

| Alternatif | Sonuç |
|---|---|
| `(wo_type, status, equipment_id)` indeksi | B-tree eşitsizlikte (`status <>`) ikinci kolonu atlayamaz; yine tarama |
| Partial index `WHERE wo_type='corrective' AND status<>'iptal'` | 6.000 satırda işe yarar, 1M'de planlayıcı yine taramayı seçti (ölçüldü: 100 ms) |
| **Materyalize view + sıralama indeksi** | **0,100 ms** — seçilen |

### 7.6 Takas: tazelik

MV birkaç saniye bayat olabilir. Bu kabul edilebilir çünkü rapor bir **yönetim
ekranı**, işlem ucu değil. Tazelik görünür ve kontrol edilebilir:

```bash
# Raporu canlı sorguyla al (MV'yi atla)
GET /api/v1/metrics/reliability?days=30&fresh=true

# MV'yi yenile (yalnızca yönetici, Idempotency-Key zorunlu)
POST /api/v1/admin/refresh-metrics

# Raporun ne kadar taze olduğunu öğren
GET /api/v1/admin/refresh-metrics
# → {"ready":true,"age_seconds":93,"refreshed_at":"...","row_count":1793}
```

Seed sonrası simülatör view'ı otomatik yeniler; aksi hâlde rapor boş kalırdı
(view migration sırasında, veri yokken oluşturuluyor). Bu, `TestMV03` ile
kanıtlanıyor: yeni kayıt eklenir → MV bayat (görünmez) → `?fresh=true` görür →
refresh → MV görür.

---

## 8. Test ve Doğrulama

```bash
make verify        # uçtan uca F-1..F-7 + T-1..T-4 + auth tablosu
make test          # 53 test: unit + gerçek PostgreSQL entegrasyonu (race detector)
make reset-demo    # veritabanını sıfırla, deterministik seed ile yeniden kur
make load          # k6 yük testi (grafana/k6)
```

Mevcut durum: **53 test PASS**, `make verify` → **14 / 14 PASS**.
Ayrıntı ve senaryo tablosu: [`TESTING.md`](TESTING.md).

### Testler neden ayrı bir veritabanında koşar?

Testler kendi ekipman/iş emri/stok kayıtlarını oluşturur ve silmez. Bunlar demo
veritabanına yazıldığında her koşu ağaca yeni sahte düğümler ekliyordu
(`EQ-IDEM-…`, `EQ-MV-…`, `TES-T2-…`) ve arayüzdeki ekipman hiyerarşisi
şişiyordu. Bu yüzden:

- `test` servisi **`db_test`** konteynerine bağlanır (`bakimplan_test` veritabanı),
- `db_test` **`tmpfs`** kullanır: veri bellekte durur, konteyner silinince sıfırlanır,
- normal `docker compose up` bu servisi **hiç başlatmaz** (`profiles: ["test"]`).

Sonuç: testler istediği kadar veri yazsın, **demo verisi değişmez**.
`docker compose --profile test down` ile test konteynerini temizleyebilirsiniz.

---

## 9. Telefondan (Mobil) Erişim

Uygulama responsive tasarlandı (menü hamburger'a düşer, tablolar yatay kaydırılır).
Telefondan açmak için **tek bir şey** gerekir: bilgisayarın yerel IP'si.

```
http://<bilgisayarın-IP'si>:3000
```

### Neden IP'yi koda yazmıyoruz?

Önceden API adresi (`http://192.168.1.42:8080/api/v1`) derleme anında JavaScript'in
içine gömülüyordu. Bu kırılgandı: IP değişince (Wi-Fi ↔ Ethernet, DHCP) yeniden
derlemek gerekiyordu ve yanlış IP ile derlenmiş bir imaj telefonda hiç çalışmıyordu.

Artık frontend'in nginx'i `/api/` isteklerini compose ağındaki backend'e vekilliyor
(`frontend/nginx.conf`). Arayüz göreli adres kullanır:

```js
// frontend/src/store.js
export const API_BASE = import.meta.env.VITE_API_URL || 'http://localhost:8080/api/v1'
// .env → VITE_API_URL=/api/v1   (göreli)
```

Sonuçları:

| Kazanç | Açıklama |
|---|---|
| IP'den bağımsız | Hangi IP/hostname ile açılırsa açılsın çalışır, yeniden derleme yok |
| CORS yok | İstek aynı kökene (`:3000`) gider; tarayıcı preflight yapmaz |
| Tek port yeter | Dışarıya yalnızca 3000 açılır; 8080'e telefon için gerek kalmaz |
| Daha az trafik | nginx JSON yanıtlarını gzip'ler (565 KB'lık ekipman ağacı küçülür) |

Doğrulama (proxy'nin çalıştığını gösterir):

```bash
curl -s -X POST http://localhost:3000/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"yonetici@bakimplan.local","password":"Password123!"}'
# → {"access_token":"...","user":{...}}
```

### Adım adım: telefondan açma

```bash
# 1) Bilgisayarın LAN IP'sini bul (Windows)
ipconfig | findstr IPv4
#    Kullanılan adaptörün IPv4'ünü al:  örn. 192.168.1.4
#    NOT: "Deprecated" görünen (kopmuş Wi-Fi gibi) adaptörün IP'sini kullanma.

# 2) Servislerin ayakta olduğunu doğrula
docker compose ps
#    frontend, backend, db "healthy" olmalı

# 3) Telefonla aynı ağda olduğundan emin ol, sonra tarayıcıda aç:
#    http://192.168.1.4:3000
```

**Yeniden derleme gerekmez.** `.env`'ye IP yazmak da gerekmez.

### Windows Güvenlik Duvarı (en sık takılma noktası)

Docker'ın gelen (inbound) kuralları **yalnızca `Private` profilde** izin verir;
ağ `Public` ise telefonun isteği sessizce düşer (tarayıcıda "bağlanamadı").
Kontrol:

```powershell
Get-NetConnectionProfile | Select-Object Name, InterfaceAlias, NetworkCategory
```

`NetworkCategory` **Public** ise, **yönetici olarak** (PowerShell → Sağ tık →
"Yönetici olarak çalıştır") şu komutu çalıştır — en güvenli seçenek ağı
Private yapmaktır:

```powershell
Set-NetConnectionProfile -InterfaceAlias "Ethernet" -NetworkCategory Private
```

Alternatif (ağ profilini değiştirmeden yalnızca portları açmak):

```powershell
New-NetFirewallRule -DisplayName "BakimPlan Frontend 3000" -Direction Inbound `
  -Protocol TCP -LocalPort 3000 -Action Allow -Profile Private,Public
```

> Ev/ofis ağında `Public` profili değiştirmek genelde sorunsuzdur. Ortak
> (kafe, okul) ağlarda güvenlik nedeniyle profil Public kalmalı — o durumda
> ikinci komutu kullanın veya telefonu bir hotspot üzerinden bağlayın.

### Telefondan erişemiyorsan kontrol listesi

| Belirti | Olası sebep | Çözüm |
|---|---|---|
| Tarayıcı "bağlanamadı" | Güvenlik duvarı / Public profil | Yukarıdaki firewall komutu |
| `http://<IP>:8080/health` açılıyor ama `:3000` açılmıyor | Frontend konteyneri ayakta değil | `docker compose ps`, `docker compose up -d` |
| Sayfa açılıyor, veriler gelmiyor | Backend ayakta değil | `docker compose logs backend --tail 30` |
| Çok yavaş | 565 KB ekipman ağacı, zayıf Wi-Fi | Normal; gzip açık, ilk yükleme ~1 sn |
| Adres hiç açılmıyor, IP doğru | Telefon farklı ağda (misafir ağı/mobil veri) | Aynı Wi-Fi'ya bağla |

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
netstat -ano | findstr :8080            # API portunu dinleyen başka süreç var mı?
netstat -ano | findstr :3000            # panel portunu dinleyen başka süreç var mı?
```

> Proje DB'yi host'a açmaz; `5432` çakışması bu projeden kaynaklanmaz.
> Başka bir PostgreSQL'e hâlâ bağlanmak isterseniz `docker compose exec db psql -U cmms_user -d bakimplan_db` kullanın.

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

1. **Güvenlik duvarı / ağ profili (en sık sebep):** Docker kuralları yalnızca
   `Private` profilde izin verir. Ağ `Public` ise istek sessizce düşer.
   ```powershell
   Get-NetConnectionProfile | Select-Object InterfaceAlias, NetworkCategory
   # Public ise (yönetici PowerShell):
   Set-NetConnectionProfile -InterfaceAlias "Ethernet" -NetworkCategory Private
   ```
2. **Doğru IP:** `ipconfig` çıktısında **kullanılan** adaptörün IPv4'ünü al.
   `Deprecated`/`Disconnected` görünen adaptörün IP'si (örn. kopmuş Wi-Fi)
   telefondan erişilemez. Artık API adresi göreli olduğu için **yeniden
   derleme gerekmez**.
3. **Aynı ağ:** Telefon mobil veride değil, aynı Wi-Fi'da olmalı. Misafir ağı
   (guest network) cihazlar arası trafiği çoğu zaman bloklar.
4. **Docker binding:** `docker compose ps` çıktısında `0.0.0.0:3000->8080/tcp`
   görmelisiniz. `127.0.0.1:3000->...` yazıyorsa yalnızca bilgisayardan erişilebilir.
5. **Proxy kontrolü:** Sayfa açılıp veriler gelmiyorsa backend'e ulaşan yol kopuk
   olabilir. Bilgisayardan test:
   ```bash
   curl -s -o /dev/null -w "%{http_code}\n" http://localhost:3000/api/v1/auth/me
   # 401 beklenir (uç çalışıyor, token yok). 502 ise backend ayakta değil.
   ```

---

## 11. Yapılandırma

`.env` (`.env.example`'dan kopyalanır):

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` | cmms_user / cmms_secret_pass / bakimplan_db | DB kimliği |
| `DATABASE_URL` | `postgres://…@db:5432/bakimplan_db` | API ve simülatör bağlantısı |
| — | `bakimplan_test` (`db_test`) | **Yalnızca testler**; ayrı konteyner, bellekte (`tmpfs`), demo veriyle ilgisiz |
| `JWT_SECRET` | demo değeri | **Üretimde değiştirin** |
| `SEED_VALUE` | `42` | Simülatör determinizm tohumu |
| `VITE_API_URL` | `/api/v1` | Tarayıcının kullanacağı API yolu (**build arg**). Göreli: nginx `/api/`'yi backend'e vekiller |
| `K6_API_URL` | `http://backend:8080` | Yük testi hedefi |

`VITE_API_URL` **derleme anında** JavaScript'in içine gömülür (Vite kuralı), yani
`restart` işe yaramaz; değişirse `docker compose up -d --build frontend` gerekir.

**Varsayılan göreli (`/api/v1`) tutulmalı.** Mutlak bir adres yazılırsa (örn.
`http://192.168.1.42:8080/api/v1`) IP değiştiğinde yeniden derlemek gerekir ve
telefondan erişim kırılır — bu projede daha önce tam olarak bu sorun yaşandı.
Göreli kullanımda:

- hangi IP/hostname ile açılırsa açılsın çalışır (telefon dahil),
- istek aynı kökene gittiği için CORS/preflight devreye girmez,
- dışarıya yalnızca 3000 portu açılır (8080'e gerek yok).

Doğrulamak için (mutlak adres **kalmamalı**):

```bash
docker compose exec frontend sh -c "grep -o 'http://[a-z0-9.:]*/api/v1' /usr/share/nginx/html/assets/*.js | head -1"
# çıktı boş olmalı; doluysa mutlak adres gömülmüş demektir
```

`.env` dosyası git'e girmez (`.gitignore`'da). Dosya bulunmazsa `docker-compose.yml`
içindeki `${VAR:-varsayılan}` değerleri kullanılır; yani sistem `.env` olmadan da
çalışır — ancak ayarların tek yerde görünür olması için oluşturulması önerilir.

---

## 12. Dokümanlar

| Dosya | İçerik |
|---|---|
| [`DECISIONS.md`](DECISIONS.md) | Mimari karar kayıtları (ADR) |
| [`TESTING.md`](TESTING.md) | Test stratejisi, senaryo tablosu, kanıtlar |
| [`openapi.yaml`](openapi.yaml) | OpenAPI 3.0 sözleşmesi |
| [`requests.http`](requests.http) | VS Code REST Client koleksiyonu |

---

## Doğrulama Soruları

### 1. En zorlandığın görev hangisiydi? Hangi alternatif çözümleri düşündün ve neden eledin?

**Kısa cevap:** T-3 — yedek parça rezervasyonunda eşzamanlılık. Korunması gereken
değişmez (invariant) şu:

```
sum(rezerve edilmiş miktar) + yeni istek  ≤  depodaki fiziksel stok
```

Naif akışta yarış (race) var: iki istek aynı anda `available = 5` okur, ikisi de
5 rezerve eder → **10 rezerve, 5 stok**. Stok "eksiye" düşmez ama taahhüt edilen
miktar fiziksel stoğu aşar; üretimde bu, bakımın ortasında parça bulunamamasıdır.

**Denediğim/elediğim alternatifler:**

| Alternatif | Neden eledim |
|---|---|
| Go tarafında `sync.Mutex` | Yalnızca tek süreçte çalışır. API iki replikaya çıktığında veya yeniden başladığında koruma tamamen kaybolur — sessizce bozulan bir çözüm |
| `SERIALIZABLE` izolasyon | Doğru, ama çakışmada `40001` hatası döner; istemciye yeniden deneme döngüsü yazmak gerekir. Kullanıcıya "stok yok" (409) yerine anlamsız "tekrar deneyin" göstermek istemedim |
| `parts` tablosuna `version` kolonu (optimistic) | Ek kolon + her istekte retry. Aynı parçaya yük binince boşa giden denemeler artar |
| PostgreSQL advisory lock | Çalışır, ama kilit anahtarını elle yönetmek gerekir; satır kimliğine bağlı olmadığı için yanlış anahtar seçme riski var |
| Redis / harici kilit | Yeni bir altyapı bileşeni; bu projenin kapsamı ve "tek komutla ayağa kalkma" şartı için fazla |

**Seçtiğim çözüm:** İşlem (transaction) içinde parça satırını kilitlemek:

```sql
SELECT unit_cost_kurus, reorder_point, is_critical FROM parts WHERE id = $1 FOR UPDATE
```

Aynı parçaya gelen istekler burada sıraya girer; stok okuması, rezervasyon
toplamı ve `INSERT` aynı işlemde olduğu için kontrol ile yazma arasında boşluk
kalmaz. Ölü kilit (deadlock) riski yok: her istek **tek** satırı kilitler.
Yetersiz stokta temiz bir `409 INSUFFICIENT_STOCK` döner.

**Kanıt (T-3):** 15 eşzamanlı istek, stok tam 5 adet:

```
T-3 | PASS | 118ms | 15 eşzamanlı istekten tam 5 kabul (201), 10 stok reddi (409) aldı — aşım 0!
```

Aynı fikrin T-1'deki karşılığı: "aynı PM planı için ikinci açık iş emri
oluşmasın" kuralı uygulama kodunda değil, **veritabanında** kısmi tekil indeksle
korunuyor (`uq_active_pm_work_order`). Uygulama hatalı olsa bile veritabanı
ikinci kaydı reddeder.

---

### 2. Çözümün kırıldığı ya da yetersiz kaldığı bir senaryo bul ve nedenini açıkla

**Kısa cevap:** "En Problemli 10 Ekipman" raporu **bayat** olabilir. Yeni bir
arıza iş emri açıldığında rapor, materyalize view yenilenene kadar onu göstermez.

**Somut senaryo:** Teknisyen arıza bildirir → iş emri kapanır → yönetici raporu
açar. Rapor hâlâ eski sayıları gösterir. Sebep bilinçli bir takas: canlı
agregasyon 1M satırda **93,2 ms**, materyalize view **0,100 ms** (§7.3). Her
yazma işleminde view'i yenilemek, yazma gecikmesini ~100 ms'ye çıkarırdı.

**Neden kabul edilebilir:** Bayatlık gizlenmiyor, ölçülüp gösteriliyor:

```bash
GET /api/v1/admin/refresh-metrics
# {"ready":true,"age_seconds":93,"refreshed_at":"...","row_count":1793}
```

`age_seconds` alanı raporun kaç saniye önce hesaplandığını söyler; yönetici
`POST /api/v1/admin/refresh-metrics` ile tazeler. `make verify`'ın F-7 adımı da
raporu bu yolla tazeler.

**Diğer bilinen sınırlar (dürüst liste):**

| Sınır | Sebep |
|---|---|
| Keyset sayfalama rastgele sayfaya atlayamaz (`?page=7` yok) | OFFSET derinlikte okuduğu satırları gerçekten okuyup atıyor (ölçüm: `rows=8050`). Hız + tutarlılık için "sonraki sayfa" tercih edildi |
| Simülatör veriyi sürekli değiştirmez | Determinizm şart: aynı `SEED_VALUE=42` her kurulumda aynı veriyi üretmeli, yoksa `make verify` her koşuda farklı sonuç verir. Bu yüzden "canlılık" yalnızca heartbeat ile gösteriliyor — rozet **sürecin** ayakta olduğunu söyler, verinin değiştiğini değil |
| `make verify` idempotent değil | Aynı veritabanında iki kez koşarsa T-2/T-3 düşer (test verisi biriktiği için) → `make reset-demo` |
| Tek kiracılı (single-tenant) | `tenant_id` yok; kurum başına ayrı kurulum varsayılıyor |

---

### 3. Veritabanına eklediğin dizinlerden ikisini seç: hangi sorgu için, dizinsiz/dizinli ölçüm farkı nedir?

Ölçüm yöntemi: indeks **geçici** olarak düşürülüp aynı sorgu ölçüldü, sonra
geri alındı. PostgreSQL'de DDL işlemseldir, bu yüzden kalıcı bir değişiklik yok:

```sql
BEGIN;
DROP INDEX uq_active_pm_work_order;
EXPLAIN (ANALYZE, COSTS OFF) <sorgu>;
ROLLBACK;
```

Her ölçüm 5 kez koşuldu, aşağıdaki değerler **medyan**. Ortam: 6.062 iş emri,
1.793 materyalize view satırı (demo verisi).

**Dizin 1 — `uq_active_pm_work_order`** (kısmi tekil indeks, T-1)
Sorgu: "bu PM planı için zaten açık bir iş emri var mı?"

```sql
SELECT 1 FROM work_orders
 WHERE preventive_plan_id = 7 AND status IN ('acik','atandi','devam_ediyor');
```

| | Plan | Süre (medyan) | Buffers |
|---|---|---|---|
| **Dizinli** | `Index Only Scan` | **0,056 ms** | **3** |
| Dizinsiz | `Seq Scan` (145 buffer) | 0,371 ms | 145 |

**~6,6× hızlı, ~48× daha az sayfa okuma.** Yalnızca hız değil: bu indeks aynı
planda ikinci açık iş emrini **veritabanı seviyesinde** de engelliyor — yani
hatalı bir kod yolu bile veriyi bozamaz.

**Dizin 2 — `mv_equipment_wo_stats_rank_idx`** (sıralama indeksi, F-7)
Sorgu: "en problemli 10 ekipman"

```sql
SELECT equipment_id FROM mv_equipment_wo_stats
 ORDER BY failure_wo_count DESC, total_cost_kurus DESC LIMIT 10;
```

| | Plan | Süre (medyan) | Buffers |
|---|---|---|---|
| **Dizinli** | `Index Scan` → 10 satırda durur | **0,045 ms** | 12 |
| Dizinsiz | `Seq Scan` + top-N `Sort` | 0,272 ms | 18 |

**~6× hızlı.** Buradaki kazanç satır sayısıyla büyür: 1M satırda aynı sorgu
**0,100 ms** (§7.3), canlı tablo taraması **93,2 ms** — çünkü indeks `LIMIT 10`
dolduğunda taramayı bırakır, dizinsiz planda ise tüm görünüm taranıp sıralanır.

**Ölçümün beni yanılttığı yer (bunu da söylemek gerekir):** üçüncü aday
`idx_wo_assigned_open` (kısmi indeks, "teknisyenin açık işleri") için aynı testi
yaptım ve **kazanç çıkmadı**: 6.000 satırda planlayıcı indeksten vazgeçip
`work_orders_pkey` üzerinde geriye tarama yaptı (0,128 ms indeksli / 0,065 ms
dizinsiz). Sebep: `assigned_to = 1` satırların yarısına yakınını seçiyor, yani
filtre seçici değil; 20 satırı bulmak için birincil anahtar üzerinde geriye
gitmek daha ucuz. İndeks bu boyutta gereksiz, tablo büyüdüğünde gerekli olacak.
Bu yüzden Q3'ün cevabı olarak **kazancı ölçülmüş** iki indeksi seçtim.

---

### 4. Yapay zekâ kullandıysan, reddettiğin ya da düzelttiğin bir çıktıyı (önce/sonra) göster

**Önce (reddedilen):** README §7'de performans tablosu şöyleydi — hiçbir ham
`EXPLAIN` çıktısı yoktu, yalnızca yuvarlanmış iddialar:

| Yaklaşım | Plan | Süre |
|---|---|---|
| Doğrudan `work_orders` taraması | Parallel Seq Scan (3 worker) | **106 ms** |
| Materyalize view + sıralama indeksi | Index Scan → 10 satır | **0,06 ms** |
| `REFRESH ... CONCURRENTLY` | 1M satır agregasyonu | **96 ms** |

Metin ayrıca **"~1.800× hız farkı"** ve **"1.006.000 satırda birebir aynı"**
diyordu.

**Neden yanlıştı — üç bağımsız sebep:**

1. **Ham plan yoktu.** Dosyada tek bir `Execution Time` veya `Buffers:` satırı
   bulunmuyordu (ölçtüm: eski sürümde 0, yeni sürümde 8 `Execution Time` +
   29 `Buffers`). Yani sayılar hiçbir komutla yeniden üretilemiyordu; "kanıt"
   diye sunulan şey aslında bir tahmindi.
2. **Aritmetik tutmuyordu.** 106 / 0,06 = **1.766**, metin 1.800 diyordu.
   Gerçek oran da tutmuyordu.
3. **Satır sayısı uydurmaydı.** "1.006.000" yuvarlak bir sayı; gerçek ölçüm
   **1.008.523**. Doğruluk iddiasının dayanağı (`EXCEPT` ile 0 fark) da
   komutuyla birlikte verilmemişti.

En tehlikelisi 1. maddedir: sayı doğru olsa bile **doğrulanamaz** bir iddia
sınavda tek soruda çöker ("planı göster").

**Sonra (düzeltilmiş):** Aynı ölçümler gerçekten koşturuldu, ham çıktılar
README §7'ye yapıştırıldı:

```
 Limit  (cost=0.29..400.41 rows=50 width=23) (actual time=1.595..1.608 rows=50 loops=1)
   Buffers: shared hit=258 dirtied=1
   ->  Index Scan Backward using work_orders_pkey on work_orders
         (actual time=0.018..1.425 rows=8050 loops=1)
 Execution Time: 0.896 ms
```

Gerçek değerler: 1.008.523 satır, canlı tarama **93,2 ms**, MV **0,100 ms**
(≈**932×**, 1.800× değil), refresh **86,4 ms**, `EXCEPT` iki yönde **0 / 0**.

**Çıkarılan kural:** Rapora yalnızca *yeniden koşturulabilir* sayı girer; her
performans iddiasının yanında onu üreten komut ve ham plan bulunur.

**İkinci örnek (kendi yazdığım kodda bulunan hata):** oturum yenileme akışında
`if (... && !path.startsWith('/auth/'))` koşulu, `/auth/me` çağrısını da
kapsıyordu. Sonuç: access token süresi dolduğunda yenileme **hiç denenmiyor**,
kullanıcı doğrudan dışarı atılıyordu. Ağ kaydı kanıtı:

```
GET  /api/v1/auth/me     → 401
POST /api/v1/auth/logout → 204      (yenileme denendi mi? hayır)
```

Düzeltme sonrası aynı senaryo: `GET /auth/me → 401` → `POST /auth/refresh → 200`
→ oturum kesintisiz devam. Hata, "401 gelince ne oluyor?" diye ağ trafiğine
bakılmadan bulunamazdı; bu yüzden testi tarayıcıda gerçek istekle yaptım.

---

### 5. "Önleyici bakım takvimi" görevinin doğru çalıştığını nasıl kanıtladın?

Üç katmanlı kanıt: **uçtan uca araç**, **otomatik test**, **veritabanı değişmezi**.

**(a) Uçtan uca doğrulama aracı**

```bash
make verify
```

```
T-1 | PASS | 77ms | Sayaç WO #6002 üretildi, birikmeme ve sonraki tarih (2026-11-25T19:20:18Z) doğrulandı
```

Bu satır üç şeyi birlikte kanıtlar: sayaç eşiği aşılınca iş emri üretildi,
ikinci tetikleme **birikmedi**, tamamlanınca sonraki bakım tarihi hesaplandı.

**(b) Otomatik test (tek başına koşturulabilir)**

```bash
docker compose --profile test run --rm test \
  sh -c "go test -race -run TestIntegration10 -v ./internal/handlers/"
```

```
=== RUN   TestIntegration10_PreventiveMaintenanceMeterAndNoBacklog_Task1
--- PASS: TestIntegration10_PreventiveMaintenanceMeterAndNoBacklog_Task1 (0.64s)
PASS
ok  	bakimplan/internal/handlers	1.652s
```

Testin içindeki üç iddia:

| Adım | Beklenen | Neden önemli |
|---|---|---|
| Sayaç 520 (eşik 500) | **1** iş emri üretilir | Eşik aşımı yakalanıyor |
| Sayaç 1200 (aynı plan, açık iş emri var) | **0** yeni iş emri | "Birikmeme" kuralı |
| Sayaç 800 (1200'den geriye) | **400 METER_ROLLBACK** | Sayaç monotonluğu; veri bozulmasını engeller |

**(c) Veritabanı değişmezi** — "aynı plan için en fazla bir açık iş emri"

```bash
docker compose exec db psql -U cmms_user -d bakimplan_db -c \
"SELECT preventive_plan_id, count(*) FROM work_orders
  WHERE preventive_plan_id IS NOT NULL
    AND status IN ('acik','atandi','devam_ediyor')
  GROUP BY 1 HAVING count(*) > 1;"
```

```
 preventive_plan_id | aktif_wo
--------------------+----------
(0 rows)
```

Kural iki yerde birden korunuyor: uygulama `ON CONFLICT DO NOTHING` ile dener,
veritabanı da kısmi tekil indeksle (`uq_active_pm_work_order`) reddeder. Yani
kod hatası olsa bile **veri** bozulmaz — asıl güvence budur.

---

## 13. Lisans

Bu proje bir değerlendirme çalışması olarak geliştirilmiştir.
