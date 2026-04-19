-- name: UpsertSplitRule :one
-- Usado al bootstrappear un hogar (owner) y al sumar miembros (weight=1.0).
-- También desde PATCH /households/{id}/split cuando el owner edita pesos.
INSERT INTO household_split_rules (household_id, user_id, weight)
VALUES ($1, $2, $3)
ON CONFLICT (household_id, user_id) DO UPDATE
SET weight = EXCLUDED.weight,
    updated_at = NOW()
RETURNING *;

-- name: ListSplitRulesByHousehold :many
SELECT * FROM household_split_rules
WHERE household_id = $1
ORDER BY updated_at ASC;

-- name: GetSplitRule :one
SELECT * FROM household_split_rules
WHERE household_id = $1 AND user_id = $2;

-- name: DeleteSplitRule :exec
-- Normalmente no se usa (al sacar miembro el CASCADE lo limpia), pero
-- lo expongo por simetría con el resto del CRUD.
DELETE FROM household_split_rules
WHERE household_id = $1 AND user_id = $2;
