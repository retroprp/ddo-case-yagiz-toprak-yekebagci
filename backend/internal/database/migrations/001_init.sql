-- 1. KULLANICILAR VE ROLLER
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    full_name VARCHAR(150) NOT NULL,
    role VARCHAR(50) NOT NULL CHECK (role IN ('teknisyen', 'planlama', 'uretim', 'depo', 'yonetici')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 2. EKİPMAN HİYERARŞİSİ (Görev 2: Tesis -> Hat -> Makine -> Bileşen)
CREATE TABLE IF NOT EXISTS equipments (
    id SERIAL PRIMARY KEY,
    code VARCHAR(100) UNIQUE NOT NULL,
    name VARCHAR(200) NOT NULL,
    level_type VARCHAR(50) NOT NULL CHECK (level_type IN ('tesis', 'hat', 'makine', 'bilesen')),
    parent_id INT REFERENCES equipments(id) ON DELETE RESTRICT,
    shift_hours_per_day INT NOT NULL DEFAULT 16 CHECK (shift_hours_per_day BETWEEN 1 AND 24),
    is_critical BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(50) NOT NULL DEFAULT 'aktif' CHECK (status IN ('aktif', 'arizali', 'bakimda', 'pasif')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_no_self_parent CHECK (id <> parent_id)
);

CREATE INDEX IF NOT EXISTS idx_equipments_parent_id ON equipments(parent_id);

-- 3. DEPOLAR VE YEDEK PARÇALAR (Görev 3)
CREATE TABLE IF NOT EXISTS warehouses (
    id SERIAL PRIMARY KEY,
    code VARCHAR(50) UNIQUE NOT NULL,
    name VARCHAR(150) NOT NULL
);

CREATE TABLE IF NOT EXISTS parts (
    id SERIAL PRIMARY KEY,
    sku VARCHAR(100) UNIQUE NOT NULL,
    name VARCHAR(200) NOT NULL,
    unit_cost_kurus BIGINT NOT NULL CHECK (unit_cost_kurus >= 0), -- Float yasak, kuruş cinsinden tam sayı
    reorder_point INT NOT NULL DEFAULT 5 CHECK (reorder_point >= 0),
    is_critical BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Ekipman - Parça Uyumluluğu (BOM)
CREATE TABLE IF NOT EXISTS equipment_parts_bom (
    equipment_id INT NOT NULL REFERENCES equipments(id) ON DELETE CASCADE,
    part_id INT NOT NULL REFERENCES parts(id) ON DELETE CASCADE,
    PRIMARY KEY (equipment_id, part_id)
);

-- Stok Hareketleri (Kural: Stok = hareketlerin toplamı)
CREATE TABLE IF NOT EXISTS stock_movements (
    id BIGSERIAL PRIMARY KEY,
    part_id INT NOT NULL REFERENCES parts(id) ON DELETE RESTRICT,
    warehouse_id INT NOT NULL REFERENCES warehouses(id) ON DELETE RESTRICT,
    quantity_change INT NOT NULL CHECK (quantity_change <> 0), -- Giriş (+), Çıkış (-)
    movement_type VARCHAR(50) NOT NULL CHECK (movement_type IN ('initial_seed', 'purchase', 'consume', 'adjustment')),
    reference_work_order_id BIGINT,
    created_by INT REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_stock_movements_part_wh ON stock_movements(part_id, warehouse_id);

-- 4. ARIZA BİLDİRİMLERİ VE ÖNLEYİCİ BAKIM PLANLARI (Görev 1)
CREATE TABLE IF NOT EXISTS failure_reports (
    id BIGSERIAL PRIMARY KEY,
    equipment_id INT NOT NULL REFERENCES equipments(id) ON DELETE RESTRICT,
    reported_by INT NOT NULL REFERENCES users(id),
    title VARCHAR(250) NOT NULL,
    description TEXT,
    severity VARCHAR(50) NOT NULL CHECK (severity IN ('dusuk', 'orta', 'yuksek', 'kritik')),
    status VARCHAR(50) NOT NULL DEFAULT 'acik' CHECK (status IN ('acik', 'is_emrine_donustu', 'reddedildi', 'cozuldu')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS preventive_plans (
    id SERIAL PRIMARY KEY,
    equipment_id INT NOT NULL REFERENCES equipments(id) ON DELETE CASCADE,
    name VARCHAR(200) NOT NULL,
    plan_type VARCHAR(20) NOT NULL CHECK (plan_type IN ('time', 'meter', 'both')),
    interval_days INT CHECK (interval_days IS NULL OR interval_days > 0),
    interval_hours INT CHECK (interval_hours IS NULL OR interval_hours > 0),
    calc_basis VARCHAR(20) NOT NULL DEFAULT 'completed' CHECK (calc_basis IN ('planned', 'completed')),
    last_planned_at TIMESTAMPTZ,
    last_completed_at TIMESTAMPTZ,
    next_due_at TIMESTAMPTZ,
    last_trigger_meter INT NOT NULL DEFAULT 0,
    next_due_meter INT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS meter_readings (
    id BIGSERIAL PRIMARY KEY,
    equipment_id INT NOT NULL REFERENCES equipments(id) ON DELETE CASCADE,
    reading_hours INT NOT NULL CHECK (reading_hours >= 0),
    recorded_by INT REFERENCES users(id),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_meter_readings_eq_time ON meter_readings(equipment_id, recorded_at DESC);

-- 5. İŞ EMİRLERİ VE PARÇA REZERVASYONLARI
CREATE TABLE IF NOT EXISTS work_orders (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(100) UNIQUE NOT NULL,
    equipment_id INT NOT NULL REFERENCES equipments(id) ON DELETE RESTRICT,
    wo_type VARCHAR(30) NOT NULL CHECK (wo_type IN ('corrective', 'preventive')),
    preventive_plan_id INT REFERENCES preventive_plans(id),
    failure_report_id BIGINT REFERENCES failure_reports(id),
    assigned_to INT REFERENCES users(id),
    status VARCHAR(30) NOT NULL DEFAULT 'acik' CHECK (status IN ('acik', 'atandi', 'devam_ediyor', 'tamamlandi', 'iptal')),
    priority VARCHAR(30) NOT NULL DEFAULT 'normal' CHECK (priority IN ('dusuk', 'normal', 'yuksek', 'acil')),
    title VARCHAR(250) NOT NULL,
    labor_cost_kurus BIGINT NOT NULL DEFAULT 0 CHECK (labor_cost_kurus >= 0),
    parts_cost_kurus BIGINT NOT NULL DEFAULT 0 CHECK (parts_cost_kurus >= 0),
    scheduled_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Geciken önleyici bakımların birikmemesi için: Aynı plan için en fazla 1 tane aktif (açık/atandı/devam_ediyor) iş emri olabilir!
CREATE UNIQUE INDEX IF NOT EXISTS uq_active_pm_work_order 
ON work_orders (preventive_plan_id) 
WHERE preventive_plan_id IS NOT NULL AND status IN ('acik', 'atandi', 'devam_ediyor');

-- Görev 4: 1 Milyon İş Emrinde "En Problemli 10 Ekipman" Raporu İçin Kritik İndeksler
CREATE INDEX IF NOT EXISTS idx_wo_equipment_type_status ON work_orders(equipment_id, wo_type, status) INCLUDE (labor_cost_kurus, parts_cost_kurus);
CREATE INDEX IF NOT EXISTS idx_wo_assigned_status ON work_orders(assigned_to, status);

CREATE TABLE IF NOT EXISTS part_reservations (
    id BIGSERIAL PRIMARY KEY,
    work_order_id BIGINT NOT NULL REFERENCES work_orders(id) ON DELETE CASCADE,
    part_id INT NOT NULL REFERENCES parts(id) ON DELETE RESTRICT,
    warehouse_id INT NOT NULL REFERENCES warehouses(id) ON DELETE RESTRICT,
    quantity INT NOT NULL CHECK (quantity > 0),
    status VARCHAR(30) NOT NULL DEFAULT 'reserved' CHECK (status IN ('reserved', 'consumed', 'released')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_reservations_part_wh_status ON part_reservations(part_id, warehouse_id, status);

-- 6. DURUŞ OLAYLARI (Görev 4: MTBF, MTTR, Availability)
CREATE TABLE IF NOT EXISTS downtime_events (
    id BIGSERIAL PRIMARY KEY,
    equipment_id INT NOT NULL REFERENCES equipments(id) ON DELETE CASCADE,
    work_order_id BIGINT REFERENCES work_orders(id) ON DELETE SET NULL,
    downtime_type VARCHAR(30) NOT NULL DEFAULT 'unplanned' CHECK (downtime_type IN ('unplanned', 'planned')),
    start_time TIMESTAMPTZ NOT NULL,
    end_time TIMESTAMPTZ NOT NULL,
    reason VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_downtime_order CHECK (end_time > start_time)
);

CREATE INDEX IF NOT EXISTS idx_downtime_eq_times ON downtime_events(equipment_id, downtime_type, start_time, end_time);

-- 7. DENETİM KAYDI (Append-Only Audit Log) VE IDEMPOTENCY
CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGSERIAL PRIMARY KEY,
    user_id INT,
    action VARCHAR(100) NOT NULL,
    entity_type VARCHAR(100) NOT NULL,
    entity_id VARCHAR(100) NOT NULL,
    old_data JSONB,
    new_data JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    key VARCHAR(255) PRIMARY KEY,
    user_id INT NOT NULL DEFAULT 0,          -- anahtarı ilk kullanan kullanıcı
    method VARCHAR(20) NOT NULL,
    path VARCHAR(255) NOT NULL,
    request_hash TEXT NOT NULL DEFAULT '',   -- aynı anahtar + farklı gövde -> 409
    state VARCHAR(20) NOT NULL DEFAULT 'in_progress' CHECK (state IN ('in_progress', 'completed')),
    status_code INT,                         -- in_progress iken NULL
    response_body JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours'
);

-- Mevcut (eski) şemadan gelen kurulumlar için: eksik kolonları tamamla.
ALTER TABLE idempotency_keys ADD COLUMN IF NOT EXISTS user_id INT;
UPDATE idempotency_keys SET user_id = 0 WHERE user_id IS NULL;
ALTER TABLE idempotency_keys ALTER COLUMN user_id SET DEFAULT 0;
ALTER TABLE idempotency_keys ALTER COLUMN user_id SET NOT NULL;

ALTER TABLE idempotency_keys ADD COLUMN IF NOT EXISTS request_hash TEXT;
UPDATE idempotency_keys SET request_hash = '' WHERE request_hash IS NULL;
ALTER TABLE idempotency_keys ALTER COLUMN request_hash SET NOT NULL;

ALTER TABLE idempotency_keys ADD COLUMN IF NOT EXISTS state VARCHAR(20) NOT NULL DEFAULT 'in_progress';
ALTER TABLE idempotency_keys ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours';
ALTER TABLE idempotency_keys ALTER COLUMN status_code DROP NOT NULL;
ALTER TABLE idempotency_keys ALTER COLUMN response_body DROP NOT NULL;

CREATE INDEX IF NOT EXISTS idx_idempotency_expiry ON idempotency_keys (expires_at);

-- Denetim kaydı değiştirilemez (append-only): UPDATE/DELETE veritabanı seviyesinde engellenir.
CREATE OR REPLACE FUNCTION audit_logs_block_modification() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs is append-only';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_logs_append_only ON audit_logs;
CREATE TRIGGER audit_logs_append_only
    BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_block_modification();