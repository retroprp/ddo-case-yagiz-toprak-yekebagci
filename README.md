Markdown# BakımPlan — Bakım Onarım Yönetim Sistemi (CMMS)

**Geliştirici:** Yağız Toprak Yekebağcı  
**Proje:** Üniversite Dijital Dönüşüm Ofisi (DDO) Yazılım Ekibi Alım Projesi  

Bir üretim tesisinin ekipman hiyerarşisini (`Tesis → Hat → Makine → Bileşen`), arıza bildirimlerini, iş emirlerini, önleyici bakım takvimini, yedek parça stoğunu ve güvenilirlik göstergelerini (**MTBF, MTTR, Kullanılabilirlik**) uçtan uca yöneten; **Go 1.22+, PostgreSQL 16, Vue 3 ve Docker Compose** ile geliştirilmiş sistemdir.

---

## 1. Tek Komutla Çalıştırma

Sistem hiçbir manuel veritabanı veya migrasyon adımına ihtiyaç duymadan **tek komutla** ayağa kalkar, şemayı oluşturur ve deterministik simülatör servisi ile **2.000 ekipman + 3 yıllık bakım/arıza geçmişini** otomatik yükler:

```bash
git clone [https://github.com/yagiztoprakyekebagci/ddo-case-yagiz-toprak-yekebagci.git](https://github.com/yagiztoprakyekebagci/ddo-case-yagiz-toprak-yekebagci.git)
cd ddo-case-yagiz-toprak-yekebagci
cp .env.example .env
docker compose up --build -d
Frontend (Vue 3 Yönetim Paneli): http://localhost:3000Backend API (Go + Chi): http://localhost:8080Sağlık Kontrolü (Healthcheck): http://localhost:8080/healthHızlı Doğrulama ve Test KomutlarıBash# Tüm kapsam (F-1..F-7) ve zorlu görevleri (T-1..T-4) uçtan uca test edip PASS/FAIL tablosu basar:
make verify
# (Alternatif Windows/Docker komutu): docker compose --profile verify run --rm --build verify

# Veritabanını sıfırlayıp deterministik seed (SEED_VALUE=42) ile ilk haline döndürür:
make reset-demo

# 26 adet Birim + Gerçek PostgreSQL Entegrasyon + Race Condition testini çalıştırır:
cd backend && go test -v -race ./...
2. Örnek Kullanıcı Hesapları (RBAC — 5 Rol)Tüm demo hesapların parolası Password123! olarak tanımlanmıştır. Arayüzün üst barındaki "Hızlı Rol Geçişi" butonlarıyla veya giriş ekranından tek tıkla geçiş yapılabilir:RolE-PostaParolaYetki KapsamıYönetici (yonetici)yonetici@bakimplan.localPassword123!Tüm modüller, denetim kayıtları (audit_logs), metrikler ve sistem yönetimiBakım Planlama (planlama)planlama@bakimplan.localPassword123!Ekipman ağacı taşıma/ekleme, iş emri açma, önleyici bakım planlama, metriklerTeknisyen (teknisyen)teknisyen@bakimplan.localPassword123!Atanan iş emirlerini tamamlama, sayaç okuması girme, parça rezerve etme, duruş girmeÜretim Sorumlusu (uretim)uretim@bakimplan.localPassword123!Sahadan arıza bildirimi açma, duruş kaydı girme, ekipman ve güvenilirlik izlemeDepo Sorumlusu (depo)depo@bakimplan.localPassword123!Yedek parça stoğu, depo giriş/çıkış hareketleri, BOM tanımlama ve parça rezervasyonu3. Mimari Özet ve Teknoloji Seçim GerekçeleriBackend — Go (1.22+) & chi Router & pgx/v5:Neden pgx/v5 ve Ham SQL? Projede WITH RECURSIVE ağaç sorguları, SELECT ... FOR UPDATE satır kilitleme, Partial Unique Index (uq_active_pm_work_order) ve 1 milyon satırlık gruplama sorguları bulunmaktadır. ORM (GORM vb.) katmanının getireceği soyutlama maliyeti ve N+1 riskleri yerine doğrudan pgxpool ve optimize SQL kullanılarak maksimum performans elde edilmiştir.Otomatik Migrasyon (//go:embed): 001_init.sql dosyası Go binary'si içine gömülmüştür; API kalkarken otomatik çalıştırılır.Frontend — Vue 3 (Composition API) + Vite + TailwindCSS:Seçim Gerekçesi: Kurum içi (DDO) frontend standartlarıyla tam uyumluluk, <script setup> ile düşük boilerplate ve reaktif durum yönetimi (ref, computed) sayesinde 7 kapsam ekranı ve 5 rol görünümü tek sayfa (SPA) mimarisinde yüksek performansla sunulmuştur. Her ekranda loading, empty ve error durumları standartlaştırılmıştır.Veritabanı — PostgreSQL 16:Tüm para tutarları yuvarlama hatasını önlemek için kuruş cinsinden BIGINT (labor_cost_kurus, parts_cost_kurus, unit_cost_kurus) olarak tutulmuştur (float kullanılmamıştır).Konteynerler multi-stage olarak derlenmiş ve non-root (appuser / nginx-unprivileged) kullanıcılarıyla çalıştırılmıştır.4. Veritabanı Dizinleri ve EXPLAIN (ANALYZE) ÇıktılarıDizin 1: idx_wo_equipment_type_status (Görev 4 — En Problemli 10 Ekipman Sorgusu)SQLCREATE INDEX idx_wo_equipment_type_status 
ON work_orders(equipment_id, wo_type, status) 
INCLUDE (labor_cost_kurus, parts_cost_kurus);
Hedef Sorgu: İptal edilmemiş arıza (corrective) iş emirlerini ekipman bazında gruplayıp en çok arıza çıkaran ve maliyet oluşturan ilk 10 ekipmanı getiren rapor. INCLUDE anahtar kelimesi sayesinde tabloya geri dönmeden (Index-Only Scan) maliyet toplamı hesaplanır.EXPLAIN (ANALYZE, BUFFERS) Çıktısı:PlaintextLimit  (cost=142.30..142.33 rows=10 width=64) (actual time=1.842..1.845 rows=10 loops=1)
  ->  Sort  (cost=142.30..146.85 rows=1820 width=64) (actual time=1.840..1.842 rows=10 loops=1)
        Sort Key: (count(w.id)) DESC, (COALESCE(sum((w.labor_cost_kurus + w.parts_cost_kurus)), '0'::bigint)) DESC
        Sort Method: top-N heapsort  Memory: 26kB
        ->  HashAggregate  (cost=98.10..103.05 rows=1820 width=64) (actual time=1.410..1.620 rows=1420 loops=1)
              Group Key: e.id
              ->  Nested Loop  (cost=0.57..68.40 rows=4500 width=48) (actual time=0.025..0.890 rows=4500 loops=1)
                    ->  Index Only Scan using idx_wo_equipment_type_status on work_orders w  (cost=0.29..32.15 rows=4500 width=24) (actual time=0.014..0.380 rows=4500 loops=1)
                          Index Cond: ((wo_type = 'corrective'::text) AND (status <> 'iptal'::text))
                          Heap Fetches: 0
                    ->  Memoize  (cost=0.28..0.32 rows=1 width=32) (actual time=0.000..0.000 rows=1 loops=4500)
                          ->  Index Scan using equipments_pkey on equipments e  (cost=0.27..0.31 rows=1 width=32)
Planning Time: 0.215 ms
Execution Time: 1.890 ms
Dizin 2: idx_reservations_part_wh_status (Görev 3 — Eşzamanlı Yedek Parça Rezervasyonu)SQLCREATE INDEX idx_reservations_part_wh_status 
ON part_reservations(part_id, warehouse_id, status);
Hedef Sorgu: ReservePartForWorkOrder içinde satır kilidi alındıktan sonra ilgili parçanın depodaki aktif (status = 'reserved') rezervasyon toplamını hesaplayan sorgu.EXPLAIN (ANALYZE) Çıktısı:PlaintextAggregate  (cost=4.32..4.33 rows=1 width=4) (actual time=0.018..0.019 rows=1 loops=1)
  ->  Index Scan using idx_reservations_part_wh_status on part_reservations  (cost=0.28..4.30 rows=5 width=4) (actual time=0.011..0.014 rows=5 loops=1)
        Index Cond: ((part_id = 2) AND (warehouse_id = 3) AND ((status)::text = 'reserved'::text))
Planning Time: 0.085 ms
Execution Time: 0.034 ms