Markdown# BakımPlan — Test ve Doğrulama Kılavuzu (`TESTING.md`)

## 1. Tek Komutla Otomatik Doğrulama (`make verify`)

Sistem ayağa kalktıktan sonra tüm gereksinimleri (`F-1`..`F-7`, `T-1`..`T-4`, `/health`, `401/403 RBAC` ve `Idempotency-Key`) otomatik olarak sınamak için:

```bash
make verify
# Windows PowerShell'de make kurulu değilse doğrudan eşdeğer Docker komutu:
docker compose --profile verify run --rm --build verify
Veritabanını ilk deterministik seed (SEED_VALUE=42) haline sıfırlamak için:Bashmake reset-demo
# veya: docker compose down -v && docker compose up --build -d
2. Demo Kullanıcı Hesapları (Her Rol İçin)Rol KoduE-PostaParolaAçıklamayoneticiyonetici@bakimplan.localPassword123!Tüm uçlara ve /api/v1/audit-logs ekranına erişebilirplanlamaplanlama@bakimplan.localPassword123!Ekipman, iş emri ve önleyici bakım planlarını yönetirteknisyenteknisyen@bakimplan.localPassword123!İş emri tamamlar, sayaç okuması girer, parça rezerve ederuretimuretim@bakimplan.localPassword123!Sahadan arıza bildirimi ve duruş olayı açardepodepo@bakimplan.localPassword123!Yedek parça stoğunu, hareketleri ve rezervasyonları yönetir3. Doğrulama Kimlikleri, Nasıl Denenir ve Kanıt Komutları TablosuKimlikÖzellik / GörevNasıl Denenir (API / Arayüz)Beklenen SonuçKanıt KomutuF-1Ekipman HiyerarşisiGET /api/v1/equipments/tree2.000 ekipman 4 seviyeli ağaç olarak döner (200 OK)docker compose --profile verify run --rm verify (F-1 satırı)F-2Arıza BildirimiPOST /api/v1/failure-reports (Idempotency-Key ile)Arıza kaydı açılır (201), aynı key ile tekrarda yeni kayıt açılmazgo test -v -run TestIntegration02 ./internal/handlersF-3İş Emri YönetimiGET /api/v1/work-orders?limit=25&cursor=...Keyset sayfalama ile iş emirleri ve next_cursor dönergo test -v -run TestIntegration10 ./internal/handlersF-4Önleyici Bakım PlanlarıPOST /api/v1/preventive-plansSüre ve sayaç bazlı bakım planı kaydedilir (201 Created)docker compose --profile verify run --rm verify (F-4 satırı)F-5Yedek Parça StoğuGET /api/v1/partsStok = hareketlerin toplamı ve kritik sıfır stok uyarısı dönerdocker compose --profile verify run --rm verify (F-5 satırı)F-6Teknisyen İş YüküGET /api/v1/technicians/workloadTeknisyenlerin aktif ve tamamlanan iş sayıları listelenirgo test -v -run TestIntegration11 ./internal/handlersF-7Güvenilirlik PaneliGET /api/v1/metrics/reliabilityMTBF, MTTR, Availability ve En Problemli 10 Ekipman dönerdocker compose --profile verify run --rm verify (F-7 satırı)T-1Önleyici Bakım TakvimiPOST /api/v1/equipments/{id}/meter-readings & /completeEşik aşılınca otomatik iş emri açılır; gecikenler birikmez; planned/completed sonraki tarihi hesaplarcd backend && go test -v -run "TestIntegration07|TestIntegration08" ./internal/handlersT-2Ekipman Ağacı & Döngü EngellemePATCH /api/v1/equipments/6/move ({"new_parent_id": 31})Üst düğümü kendi alt ağacına taşıma 409 CYCLE_DETECTED ile reddedilir; maliyetler ağaçta toplanırcd backend && go test -v -run "TestIntegration03|TestIntegration04" ./internal/handlersT-3Yedek Parça Rezervasyonu (Yarış Durumu)POST /api/v1/work-orders/{id}/reserve-part (20 eşzamanlı goroutine)Stokta 4 adet varken 20 eşzamanlı istekten tam 4'ü 201, 16'sı 409 INSUFFICIENT_STOCK alırcd backend && go test -v -race -run "TestIntegration05|TestIntegration06" ./internal/handlersT-4MTBF, MTTR & Örtüşen DuruşlarPOST /api/v1/downtime-events (10:00-12:00 ve 11:00-13:00)İki örtüşen duruş 4 saat değil 3.0 saat olarak birleştirilir; geçmiş olay düzeltilince yeniden hesaplanırcd backend && go test -v -run "TestUnit06|TestUnit11|TestIntegration09" ./internal/handlers4. Arayüzde (http://localhost:3000) Elle Denenecek Kısa Senaryo ListesiRol Değişimi ve RBAC Testi: Üst bardan "Teknisyen" butonuna tıklayın. Sol menüde yalnızca teknisyenin yetkili olduğu ekranların kaldığını görün. Sol menüdeki "Yetki Testi" bağlantısına tıkladığınızda sunucunun 403 FORBIDDEN_ROLE döndürdüğünü kırmızı hata banner'ında doğrulayın. Tekrar "Yönetici" rolüne geçin.Görev 2 (Ekipman Ağacı ve Döngü Testi): Sol menüden "Ekipman Ağacı (F-1 / T-2)" sekmesine geçin. Tesis ve Hatların yanında alt makinelerden toplanan Roll-up Maliyet ve Roll-up Duruş saatlerini inceleyin. Sağdaki "Döngü Testi (Hat #6 → #31)" butonuna basın ve 409 [CYCLE_DETECTED] engelini görün.Görev 1 (Sayaç Bazlı Otomatik İş Emri): "Önleyici Bakım & Sayaç (F-4 / T-1)" sekmesine geçin. Sağdaki formdan Ekipman #31 için 600 saat sayaç okuması gönderin; otomatik iş emri üretildiğini görün. Aynı okumayı tekrar gönderdiğinizde yeni iş emri birikmediğini doğrulayın.Görev 3 (Parça Rezervasyonu ve Stok Uyarısı): "Yedek Parça & Depo (F-5 / T-3)" sekmesine geçin. Parça #1'in kritik stok uyarısını inceleyin ve sağdaki formdan İş Emri #1 için parça rezerve edin.Görev 4 (MTBF, MTTR ve En Problemli 10 Ekipman): "Güvenilirlik & MTBF (F-7 / T-4)" sekmesinde birleşmiş arıza sayılarını ve En Problemli 10 Ekipman tablosunu inceleyin; sağdaki formdan yeni bir duruş olayı ekleyip metriklerin anında yeniden hesaplandığını görün.
---

### 4. `requests.http` (VS Code REST Client Elle Deneme Koleksiyonu)

```http
@baseUrl = http://localhost:8080
@apiUrl = http://localhost:8080/api/v1

### 1. Sağlık Kontrolü (GET /health)
GET {{baseUrl}}/health
Accept: application/json

### 2. Yönetici Girişi (JWT Token Al)
# @name adminLogin
POST {{apiUrl}}/auth/login
Content-Type: application/json

{
  "email": "yonetici@bakimplan.local",
  "password": "Password123!"
}

###
@token = {{adminLogin.response.body.access_token}}

### 3. Yanlış Parola Testi (401 Unauthorized Beklenir)
POST {{apiUrl}}/auth/login
Content-Type: application/json

{
  "email": "yonetici@bakimplan.local",
  "password": "WrongPassword!"
}

### 4. Aktif Kullanıcı Bilgisi (GET /auth/me)
GET {{apiUrl}}/auth/me
Authorization: Bearer {{token}}

### 5. [F-1 & T-2] Ekipman Hiyerarşisi ve Roll-up Maliyet/Duruş Ağacı
GET {{apiUrl}}/equipments/tree
Authorization: Bearer {{token}}

### 6. [T-2] Ekipman Ağacında Döngü (Cycle) Engelleme Testi (Hat #6 -> Altındaki Makine #31'e Taşıma -> 409 Conflict)
PATCH {{apiUrl}}/equipments/6/move
Authorization: Bearer {{token}}
Content-Type: application/json

{
  "new_parent_id": 31
}

### 7. [F-2] Arıza Bildirimi Oluşturma (Idempotency-Key Destekli)
POST {{apiUrl}}/failure-reports
Authorization: Bearer {{token}}
Idempotency-Key: demo-failure-key-001
Content-Type: application/json

{
  "equipment_id": 31,
  "title": "CNC Hidrolik Basınç Kaybı",
  "description": "Üretim hattında basınç 40 bar altına düştü.",
  "severity": "kritik"
}

### 8. [F-3] İş Emirleri Listeleme (Keyset / Cursor Sayfalama)
GET {{apiUrl}}/work-orders?limit=10
Authorization: Bearer {{token}}

### 9. [F-3] Yeni İş Emri Oluşturma
POST {{apiUrl}}/work-orders
Authorization: Bearer {{token}}
Content-Type: application/json

{
  "equipment_id": 31,
  "wo_type": "corrective",
  "priority": "acil",
  "title": "Hidrolik Valf Değişimi",
  "assigned_to": 1,
  "labor_cost_kurus": 85000
}

### 10. [F-4 & T-1] Önleyici Bakım Planları Listesi
GET {{apiUrl}}/preventive-plans
Authorization: Bearer {{token}}

### 11. [T-1] Sayaç Okuması Gönderme ve İdempotent Otomatik İş Emri Üretimi
POST {{apiUrl}}/equipments/31/meter-readings
Authorization: Bearer {{token}}
Content-Type: application/json

{
  "reading_hours": 650
}

### 12. [T-1] Günü Gelen Önleyici Bakımları Tetikleme (Gecikenler Birikmez)
POST {{apiUrl}}/preventive-plans/trigger-due
Authorization: Bearer {{token}}
Content-Type: application/json

{}

### 13. [F-5 & T-3] Yedek Parça Stoğu (Stok = Hareketlerin Toplamı)
GET {{apiUrl}}/parts
Authorization: Bearer {{token}}

### 14. [T-3] İş Emrine Yedek Parça Rezerve Etme (SELECT ... FOR UPDATE & BOM Kontrollü)
POST {{apiUrl}}/work-orders/1/reserve-part
Authorization: Bearer {{token}}
Content-Type: application/json

{
  "part_id": 1,
  "warehouse_id": 1,
  "quantity": 1
}

### 15. [F-6] Teknisyen İş Yükü Listesi
GET {{apiUrl}}/technicians/workload
Authorization: Bearer {{token}}

### 16. [F-7 & T-4] Güvenilirlik Göstergeleri (MTBF, MTTR, Availability & Top 10 Problemli Ekipman)
GET {{apiUrl}}/metrics/reliability
Authorization: Bearer {{token}}

### 17. [T-4] Yeni Duruş Olayı Ekleme (Örtüşen Duruş Birleştirme Testi)
POST {{apiUrl}}/downtime-events
Authorization: Bearer {{token}}
Content-Type: application/json

{
  "equipment_id": 31,
  "downtime_type": "unplanned",
  "start_time": "2026-10-20T10:00:00Z",
  "end_time": "2026-10-20T12:30:00Z",
  "reason": "Sensör Kalibrasyon Duruşu"
}

### 18. Denetim Kayıtları (Append-Only Audit Logs - Yalnızca Yönetici)
GET {{apiUrl}}/audit-logs
Authorization: Bearer {{token}}