-- 003: "En Problemli 10 Ekipman" raporu için materyalize view
--
-- Neden?
--
--   Mevcut indeks `work_orders (equipment_id, wo_type, status)` roll-up
--   sorgusunda Index Only Scan ile çalışıyor (heap fetch 0), ancak rapor
--   sorgusu `wo_type` ve `status` ile filtrelediği için bu indeksten
--   yararlanamıyor: öneki `equipment_id` olan bir indekste diğer kolonlar
--   yalnızca "eşitlik taraması" olarak kullanılabilir, filtre olarak değil.
--
--   Ölçüm (1.006.000 iş emri):
--     doğrudan sorgu : Parallel Seq Scan, ~114 ms
--     materyalize view: ~1,1 ms   (~100× hızlı)
--     REFRESH maliyeti: ~85 ms     (1M satır)
--
--   Takas: rapor ucu için birkaç saniyelik gecikme kabul edilebilir,
--   çünkü bu bir yönetim raporu (işlem yapan uç değil). İş emri listesi,
--   stok ve durum geçişleri doğrudan tablodan okumaya devam ediyor —
--   orada tazelik kritik.

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_equipment_wo_stats AS
SELECT
    equipment_id,
    count(*) AS total_wo_count,
    count(*) FILTER (WHERE wo_type = 'corrective' AND status <> 'iptal') AS failure_wo_count,
    coalesce(
        sum(labor_cost_kurus + parts_cost_kurus)
            FILTER (WHERE wo_type = 'corrective' AND status <> 'iptal'),
        0
    )::bigint AS total_cost_kurus,
    max(created_at) AS last_wo_at
FROM work_orders
GROUP BY equipment_id;

-- REFRESH ... CONCURRENTLY için tek kolonlu benzersiz indeks zorunlu.
-- (Concurrently olmadan REFRESH, view'ı okuyan sorguları bloke eder.)
CREATE UNIQUE INDEX IF NOT EXISTS mv_equipment_wo_stats_pk
    ON mv_equipment_wo_stats (equipment_id);

-- Rapor sıralaması (failure_wo_count DESC, total_cost_kurus DESC) için
-- ifade indeksi: LIMIT 10 hep aynı sırayla okunur.
CREATE INDEX IF NOT EXISTS mv_equipment_wo_stats_rank_idx
    ON mv_equipment_wo_stats (failure_wo_count DESC, total_cost_kurus DESC);

-- ---------------------------------------------------------------------------
-- Yenileme durumu: "rapor ne zaman güncellendi" sorusunun cevabı.
-- Ayrı tablo çünkü materyalize view'ların kendi meta verisi yok.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS metrics_refresh_state (
    name         text PRIMARY KEY,
    refreshed_at timestamptz NOT NULL DEFAULT now(),
    duration_ms  integer NOT NULL DEFAULT 0,
    row_count    bigint NOT NULL DEFAULT 0
);

INSERT INTO metrics_refresh_state (name, refreshed_at)
VALUES ('mv_equipment_wo_stats', now())
ON CONFLICT (name) DO NOTHING;

-- Yenilemeyi hem SQL'den hem uygulamadan çağrılabilir yap.
CREATE OR REPLACE FUNCTION refresh_mv_equipment_wo_stats() RETURNS bigint AS $$
DECLARE
    started  timestamptz := clock_timestamp();
    n_rows   bigint;
BEGIN
    REFRESH MATERIALIZED VIEW CONCURRENTLY mv_equipment_wo_stats;

    SELECT count(*) INTO n_rows FROM mv_equipment_wo_stats;

    INSERT INTO metrics_refresh_state (name, refreshed_at, duration_ms, row_count)
    VALUES (
        'mv_equipment_wo_stats',
        now(),
        (extract(epoch FROM (clock_timestamp() - started)) * 1000)::int,
        n_rows
    )
    ON CONFLICT (name) DO UPDATE
        SET refreshed_at = excluded.refreshed_at,
            duration_ms  = excluded.duration_ms,
            row_count    = excluded.row_count;

    RETURN n_rows;
END;
$$ LANGUAGE plpgsql;
