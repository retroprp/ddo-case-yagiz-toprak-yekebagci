-- 004: Simülatör canlılık göstergesi (heartbeat)
--
-- Neden ayrı tablo?
--
--   Simülatör deterministik olmalı: aynı tohum (SEED_VALUE=42) her zaman aynı
--   veriyi üretmeli. Bu yüzden seed'lenmiş veriye (ekipman, iş emri, stok…)
--   sürekli yazmıyoruz — aksi hâlde "canlı" görünüm uğruna determinizmi
--   kaybederdik ve `make verify` her koşuda farklı sonuç verirdi.
--
--   Bunun yerine simülatör yalnızca bu tabloya "hâlâ ayaktayım" sinyali yazar
--   (5 saniyede bir). Arayüz bu sinyali okuyup "Canlı Sistem · 3 sn" gösterir.
--   Seed verisi değişmez, ama sistemin canlı olduğu görünür.
--
--   Spec §8: "Arayüzde sistemin canlı olduğu görülebilmelidir."

CREATE TABLE IF NOT EXISTS simulator_state (
    name      text PRIMARY KEY,
    mode      text NOT NULL DEFAULT 'deterministic',
    seed      bigint NOT NULL DEFAULT 0,
    ticks     bigint NOT NULL DEFAULT 0,
    last_tick timestamptz NOT NULL DEFAULT now()
);

-- Tek satır: 'default'. Simülatör bunu günceller, API okur.
INSERT INTO simulator_state (name, mode, seed, ticks, last_tick)
VALUES ('default', 'deterministic', 0, 0, now())
ON CONFLICT (name) DO NOTHING;
