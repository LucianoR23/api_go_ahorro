-- Histórico de cotizaciones. Cada fetch de bluelytics escribe acá.
--
-- PK (currency, source, last_update): bluelytics puede reportar el mismo
-- last_update por varios minutos (fin de semana/feriado AR); con esta PK
-- el worker hace INSERT ... ON CONFLICT DO NOTHING y no duplica filas.
--
-- Las tasas van relativas a ARS:
--   - rate_avg = (buy + sell) / 2 — usado para convertir a ARS.
--   - rate_buy / rate_sell quedan guardados por si más adelante
--     queremos distinguir compra/venta.
--
-- source: "blue" (bluelytics blue) u "oficial". Hoy el worker solo usa blue,
-- pero dejamos el campo para extender.
CREATE TABLE exchange_rates (
    currency     TEXT NOT NULL CHECK (currency IN ('USD','EUR')),
    source       TEXT NOT NULL DEFAULT 'blue' CHECK (source IN ('blue','oficial')),
    last_update  TIMESTAMPTZ NOT NULL,
    rate_avg     NUMERIC(12, 4) NOT NULL CHECK (rate_avg > 0),
    rate_buy     NUMERIC(12, 4) NOT NULL,
    rate_sell    NUMERIC(12, 4) NOT NULL,
    fetched_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (currency, source, last_update)
);

CREATE INDEX idx_exchange_rates_latest ON exchange_rates(currency, source, last_update DESC);
