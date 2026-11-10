-- name: UpsertExchangeRate :exec
-- The worker runs every 15 min even when the upstream has nothing new
-- (weekends, holidays): a quote already stored is not duplicated, only
-- marked as confirmed again. fetched_at is "last confirmed by an upstream"
-- (plan 5.2): a rate nobody confirmed for a day is stale.
INSERT INTO exchange_rates (currency, source, last_update, rate_avg, rate_buy, rate_sell)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (currency, source, last_update) DO UPDATE SET fetched_at = now();

-- name: GetLatestExchangeRate :one
SELECT *
FROM exchange_rates
WHERE currency = $1 AND source = $2
ORDER BY last_update DESC
LIMIT 1;

-- name: ListLatestExchangeRates :many
-- Devuelve la última fila por (currency, source) usando DISTINCT ON.
-- Útil para el endpoint /current y para rehidratar el caché al arrancar.
SELECT DISTINCT ON (currency, source)
    currency, source, last_update, rate_avg, rate_buy, rate_sell, fetched_at
FROM exchange_rates
ORDER BY currency, source, last_update DESC;

-- name: ListExchangeRateHistory :many
-- One quote per day (the day's last, in the app's timezone) since a date,
-- oldest first (plan 5.2).
SELECT * FROM (
    SELECT DISTINCT ON ((last_update AT TIME ZONE sqlc.arg(tz)::text)::date)
        (last_update AT TIME ZONE sqlc.arg(tz)::text)::date AS day,
        currency, source, last_update, rate_avg, rate_buy, rate_sell, fetched_at
    FROM exchange_rates
    WHERE currency = sqlc.arg(currency) AND source = sqlc.arg(source)
      AND last_update >= sqlc.arg(since)::timestamptz
    ORDER BY (last_update AT TIME ZONE sqlc.arg(tz)::text)::date, last_update DESC
) days
ORDER BY day;
