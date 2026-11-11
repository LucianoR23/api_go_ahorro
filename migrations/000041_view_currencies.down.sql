DROP FUNCTION report_amount(NUMERIC, TEXT, NUMERIC, DATE, TEXT, TEXT, TEXT, NUMERIC);

CREATE FUNCTION report_amount(p_base NUMERIC, p_currency TEXT, p_amount NUMERIC, p_day DATE, p_source TEXT, p_tz TEXT)
RETURNS NUMERIC
LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN p_source IS NULL THEN p_base
        WHEN p_currency = 'USD' THEN p_amount
        ELSE p_base / fx_usd_rate(p_source, p_day, p_tz)
    END
$$;

DELETE FROM exchange_rates WHERE currency NOT IN ('USD', 'EUR');

ALTER TABLE exchange_rates
    ALTER COLUMN rate_avg TYPE NUMERIC(12, 4),
    ALTER COLUMN rate_buy TYPE NUMERIC(12, 4),
    ALTER COLUMN rate_sell TYPE NUMERIC(12, 4);

ALTER TABLE exchange_rates DROP CONSTRAINT exchange_rates_currency_check;
ALTER TABLE exchange_rates ADD CONSTRAINT exchange_rates_currency_check
    CHECK (currency IN ('USD', 'EUR'));
