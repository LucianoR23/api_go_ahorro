-- name: UpsertExchangeRate :exec
-- El worker corre cada 15min aunque bluelytics no tenga dato nuevo
-- (fines de semana, feriados). ON CONFLICT DO NOTHING evita duplicar.
INSERT INTO exchange_rates (currency, source, last_update, rate_avg, rate_buy, rate_sell)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (currency, source, last_update) DO NOTHING;

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
