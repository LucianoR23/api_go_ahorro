ALTER TABLE incomes DROP COLUMN rate_source;
ALTER TABLE expenses DROP COLUMN rate_source;
ALTER TABLE households DROP COLUMN fx_source;

-- Rates from the new sources do not fit the old check.
DELETE FROM exchange_rates WHERE source NOT IN ('blue', 'oficial');
ALTER TABLE exchange_rates DROP CONSTRAINT exchange_rates_source_check;
ALTER TABLE exchange_rates ADD CONSTRAINT exchange_rates_source_check CHECK (source IN ('blue', 'oficial'));
