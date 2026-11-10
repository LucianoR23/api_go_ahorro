-- Plan 5.3: reports in dollars.
--
-- fx_usd_rate: the selling price of a dollar source on a calendar day (in
-- tz), the day's last quote. A day before the first stored quote uses the
-- oldest one. NULL only when the source has no quotes at all.
CREATE FUNCTION fx_usd_rate(p_source TEXT, p_day DATE, p_tz TEXT) RETURNS NUMERIC
LANGUAGE sql STABLE AS $$
    SELECT COALESCE(
        (SELECT COALESCE(NULLIF(rate_sell, 0), rate_avg) FROM exchange_rates
          WHERE currency = 'USD' AND source = p_source
            AND last_update < ((p_day + 1)::timestamp AT TIME ZONE p_tz)
          ORDER BY last_update DESC LIMIT 1),
        (SELECT COALESCE(NULLIF(rate_sell, 0), rate_avg) FROM exchange_rates
          WHERE currency = 'USD' AND source = p_source
          ORDER BY last_update ASC LIMIT 1)
    )
$$;

-- report_amount: a movement's amount as a report shows it. Without a
-- source, its amount in the base currency (pesos). With one, in dollars: a
-- movement loaded in dollars keeps its amount; any other is its pesos at the
-- source's dollar on p_day.
CREATE FUNCTION report_amount(p_base NUMERIC, p_currency TEXT, p_amount NUMERIC, p_day DATE, p_source TEXT, p_tz TEXT)
RETURNS NUMERIC
LANGUAGE sql STABLE AS $$
    SELECT CASE
        WHEN p_source IS NULL THEN p_base
        WHEN p_currency = 'USD' THEN p_amount
        ELSE p_base / fx_usd_rate(p_source, p_day, p_tz)
    END
$$;
