-- Plan 5.1: rates from DolarAPI, a rate source per household and an
-- override per expense or income.
--   - exchange_rates.source: every DolarAPI "casa". bolsa is the MEP dollar.
--   - households.fx_source: the rate the household converts with. MEP by
--     default (decided with the user on 2026-11-10).
--   - expenses/incomes.rate_source: the source rate_used came from. NULL when
--     no conversion happened (same currency) or for rows from before 5.1,
--     which used blue.

ALTER TABLE exchange_rates DROP CONSTRAINT exchange_rates_source_check;
ALTER TABLE exchange_rates ADD CONSTRAINT exchange_rates_source_check
    CHECK (source IN ('oficial', 'blue', 'bolsa', 'contadoconliqui', 'tarjeta', 'cripto', 'mayorista'));

ALTER TABLE households ADD COLUMN fx_source TEXT NOT NULL DEFAULT 'bolsa'
    CHECK (fx_source IN ('oficial', 'blue', 'bolsa', 'contadoconliqui', 'tarjeta', 'cripto', 'mayorista'));

ALTER TABLE expenses ADD COLUMN rate_source TEXT
    CHECK (rate_source IN ('oficial', 'blue', 'bolsa', 'contadoconliqui', 'tarjeta', 'cripto', 'mayorista'));

ALTER TABLE incomes ADD COLUMN rate_source TEXT
    CHECK (rate_source IN ('oficial', 'blue', 'bolsa', 'contadoconliqui', 'tarjeta', 'cripto', 'mayorista'));
