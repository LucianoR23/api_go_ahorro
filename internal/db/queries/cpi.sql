-- name: UpsertCPI :execrows
-- Stores a whole series at once. A month already stored is only touched
-- when INDEC revised its value; returns how many rows were new or changed.
INSERT INTO cpi_index (month, value)
SELECT unnest(sqlc.arg(months)::date[]), unnest(sqlc.arg(vals)::text[])::numeric
ON CONFLICT (month) DO UPDATE SET value = EXCLUDED.value, fetched_at = now()
WHERE cpi_index.value <> EXCLUDED.value;

-- name: ListCPI :many
SELECT month, value FROM cpi_index ORDER BY month;
