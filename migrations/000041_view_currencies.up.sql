-- Plan 5.6: reports can be shown in other currencies and crypto, view only.
--   - exchange_rates.currency: the official real, Chilean and Uruguayan peso
--     (DolarAPI), USDT (DolarAPI's crypto dollar), and bitcoin and ether
--     (CoinGecko). Movements still take only ARS, USD and EUR.
--   - The rate columns grow: a bitcoin costs more than 99.999.999 pesos.
--   - report_amount takes the currency to show and, for every currency but
--     the dollar, today's rate. A movement loaded in that currency keeps its
--     amount.

ALTER TABLE exchange_rates DROP CONSTRAINT exchange_rates_currency_check;
ALTER TABLE exchange_rates ADD CONSTRAINT exchange_rates_currency_check
    CHECK (currency IN ('USD', 'EUR', 'BRL', 'CLP', 'UYU', 'USDT', 'BTC', 'ETH'));

ALTER TABLE exchange_rates
    ALTER COLUMN rate_avg TYPE NUMERIC(18, 4),
    ALTER COLUMN rate_buy TYPE NUMERIC(18, 4),
    ALTER COLUMN rate_sell TYPE NUMERIC(18, 4);

DROP FUNCTION report_amount(NUMERIC, TEXT, NUMERIC, DATE, TEXT, TEXT);

-- report_amount: a movement's amount as a report shows it.
--   - p_view NULL: its amount in pesos.
--   - a movement loaded in p_view: its own amount.
--   - p_rate set: its pesos at that rate (today's, for every currency).
--   - otherwise (the dollar, day by day): its pesos at p_source's dollar on
--     p_day.
CREATE FUNCTION report_amount(
    p_base NUMERIC, p_currency TEXT, p_amount NUMERIC, p_day DATE,
    p_source TEXT, p_tz TEXT, p_view TEXT, p_rate NUMERIC
) RETURNS NUMERIC
LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN p_view IS NULL THEN p_base
        WHEN p_currency = p_view THEN p_amount
        WHEN p_rate IS NOT NULL THEN p_base / p_rate
        ELSE p_base / fx_usd_rate(p_source, p_day, p_tz)
    END
$$;
