-- Queries de banks. Los bancos son del user (owner_user_id),
-- nunca se borran: toggle de is_active.


-- name: CreateBank :one
INSERT INTO banks (owner_user_id, name)
VALUES ($1, $2)
RETURNING *;


-- name: GetBankByID :one
SELECT * FROM banks WHERE id = $1;


-- name: ListBanksByOwner :many
-- Lista los bancos activos del user, orden estable por nombre.
-- Si algún día hace falta mostrar los desactivados, se agrega otra query.
SELECT * FROM banks
WHERE owner_user_id = $1 AND is_active = true
ORDER BY name ASC;


-- name: UpdateBankName :one
-- Solo permite cambiar el nombre (único campo editable del modelo).
UPDATE banks
SET name = $2
WHERE id = $1
RETURNING *;


-- name: SetBankActive :one
-- Activa o desactiva un banco. El ON DELETE SET NULL en payment_methods.bank_id
-- NO se dispara acá (no borramos la fila), así que los métodos siguen
-- asociados al banco aunque el banco esté inactivo.
UPDATE banks
SET is_active = $2
WHERE id = $1
RETURNING *;
