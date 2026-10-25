-- 002: İş emri yaşam döngüsü geçmişi (F-3) ve teknisyen atama izleri (F-6)
--
-- Her durum geçişi burada saklanır: kim, ne zaman, hangi durumdan hangi duruma.
-- work_orders.status yalnızca "şu an nerede" sorusunu yanıtlar; geçmiş bu tabloda.
-- Tablo append-only mantığıyla kullanılır (UPDATE/DELETE için uygulama yolu yok).

CREATE TABLE IF NOT EXISTS work_order_events (
    id            BIGSERIAL PRIMARY KEY,
    work_order_id BIGINT NOT NULL REFERENCES work_orders(id) ON DELETE CASCADE,
    from_status   VARCHAR(30),
    to_status     VARCHAR(30) NOT NULL,
    actor_id      INT REFERENCES users(id),
    note          TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Bir iş emrinin geçmişi her zaman "en yeni önce" okunur.
CREATE INDEX IF NOT EXISTS idx_wo_events_wo ON work_order_events (work_order_id, id DESC);

-- Denetim ekranı için zaman bazlı tarama.
CREATE INDEX IF NOT EXISTS idx_wo_events_created ON work_order_events (created_at DESC, id DESC);

-- Atanmış teknisyenin açık işleri: atama ekranında ve iş yükü hesabında kullanılır.
CREATE INDEX IF NOT EXISTS idx_wo_assigned_open
    ON work_orders (assigned_to, status)
    WHERE status IN ('atandi', 'devam_ediyor');