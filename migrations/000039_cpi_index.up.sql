-- Plan 5.4: real-terms spending. INDEC's national CPI (nivel general, base
-- December 2016 = 100), one row per month. Global: every household uses the
-- same index. Filled by the daily cpi_refresh job.
CREATE TABLE cpi_index (
    month      DATE PRIMARY KEY CHECK (EXTRACT(DAY FROM month) = 1),
    value      NUMERIC(14, 4) NOT NULL CHECK (value > 0),
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
