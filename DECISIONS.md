---

### 2. `DECISIONS.md` (Mimari Karar Kayıtları — ADR)

```markdown
# Mimari Karar Kayıtları (Architecture Decision Records — ADR)

Bu doküman, **BakımPlan CMMS** geliştirilirken alınan kritik mimari kararları, değerlendirilen alternatifleri ve sonuçlarını kayıt altına alır.

---

## ADR-001: ORM Yerine Ham SQL (`pgx/v5`) ve Veritabanı Seviyesi Kısıtların Kullanımı

- **Bağlam:** Sistemde 4 seviyeli ekipman hiyerarşisi üzerinde maliyet toplama (roll-up), eşzamanlı stok rezervasyonlarında satır kilitleme ve 1 milyon iş emrinde hızlı raporlama gereksinimleri vardır.
- **Seçenekler:**
  1. *GORM (Go ORM):* Hızlı CRUD geliştirme sağlar ancak `WITH RECURSIVE`, `SELECT ... FOR UPDATE` ve `INCLUDE` covering index sorgularında soyutlama sızıntısı ve performans kaybı yaratır.
  2. *`pgx/v5` + Ham SQL:* Doğrudan PostgreSQL binary protokolünü kullanır, toplu veri basımında `CopyFrom` protokolünü destekler ve SQL sorguları üzerinde %100 hakimiyet sağlar.
- **Karar:** **`pgx/v5` (`pgxpool`) ve Ham SQL** seçilmiştir.
- **Sonuç:** Simülatör servisi 2.000 ekipmanı ve binlerce geçmiş kaydı `CopyFrom` ile **< 1 saniyede** veritabanına yazabilmiş; tüm zorlu görev sorguları milisaniyeler içinde çalışmıştır.

---

## ADR-002: Eşzamanlı Stok Rezervasyonunda Pessimistic Locking (`SELECT ... FOR UPDATE`) ve Hareket Tabanlı Stok

- **Bağlam:** Görev 3 kapsamında birden fazla teknisyen veya planlamacı aynı anda aynı yedek parçayı kendi iş emrine rezerve etmeye çalıştığında stoktan fazla rezervasyon yapılmaması ve "Stok = hareketlerin toplamı" kuralının korunması gerekmektedir.
- **Seçenekler:**
  1. *Uygulama İçi Mutex (`sync.Mutex`):* Yalnızca tek bir Go API instance'ı çalışırken koruma sağlar; yatay ölçeklemede (birden fazla API konteyneri) yarış durumunu engelleyemez.
  2. *Optimistic Locking (Versiyon Kolonu):* Çakışma anında isteği reddeder veya uygulama tarafında karmaşık retry döngüsü gerektirir.
  3. *PostgreSQL `SELECT ... FOR UPDATE` ile Satır Kilitleme + Hareket Toplamı:* Transaction başında ilgili `parts` satırı kilitlenir, `stock_movements` ve `part_reservations` tablolarından net kullanılabilir stok hesaplanır ve rezervasyon yazılır.
- **Karar:** **Seçenek 3 (`SELECT ... FOR UPDATE` + Hareket Toplamı)** seçilmiştir.
- **Sonuç:** Hem çoklu konteyner ortamında güvenli eşzamanlılık sağlandı hem de `TestIntegration05_ConcurrentPartReservationRaceCondition_Task3` testinde 20 eşzamanlı goroutine ile stok aşımının **0** olduğu kanıtlandı.

---

## ADR-003: Önleyici Bakımların Birikmemesi İçin Partial Unique Index Tasarımı

- **Bağlam:** Görev 1'de geciken önleyici bakımların üst üste yığılmaması (örneğin 3 ay geciken 30 günlük bir plan için 3 ayrı iş emri açılmaması) ve sayaç okumaları tekrarlandığında idempotent davranması gerekmektedir.
- **Seçenekler:**
  1. *Uygulama Katmanında `SELECT COUNT(*)` Kontrolü:* Eşzamanlı iki sayaç okumasında (race condition) iki iş emri birden açılma riski taşır.
  2. *PostgreSQL Partial Unique Index (`uq_active_pm_work_order`):* `work_orders` tablosunda `WHERE preventive_plan_id IS NOT NULL AND status IN ('acik', 'atandi', 'devam_ediyor')` koşuluyla tekillik kısıtı koymak.
- **Karar:** **Partial Unique Index + `INSERT ... ON CONFLICT DO NOTHING`** yapısı seçilmiştir.
- **Sonuç:** Veritabanı motoru seviyesinde bir önleyici bakım planı için aynı anda **en fazla 1 aktif iş emri** bulunması garanti altına alınmıştır.

---

## ADR-004: Frontend Katmanında Vue 3 (Composition API) ve TailwindCSS Seçimi

- **Bağlam:** 5 farklı kullanıcı rolünün (Teknisyen, Planlama, Üretim, Depo, Yönetici) yetkilerine göre farklı ekranları gördüğü, mobil uyumlu ve hızlı bir arayüz gerekmektedir.
- **Seçenekler:**
  1. *Sunucu Taraflı Şablonlar (Go `html/template` + HTMX):* SPA deneyimi ve zengin istemci durumu (rol geçişleri, anlık filtreleme) için ek efor gerektirir.
  2. *Vue 3 (`<script setup>`) + Vite + TailwindCSS:* Üniversite Dijital Dönüşüm Ofisi teknoloji yığınıyla uyumludur, reaktif state yönetimi çok temizdir ve multi-stage Nginx imajı ile ~25 MB boyutunda sunulabilir.
- **Karar:** **Vue 3 + Vite + TailwindCSS** seçilmiştir.
- **Sonuç:** Tüm kapsam maddeleri (`F-1`..`F-7`), zorlu görev tetikleyicileri (`T-1`..`T-4`), yükleniyor/boş/hata durumları ve hızlı rol değiştirici tek ve akıcı bir arayüzde tamamlanmıştır.