-- Queries de payment_methods. Métodos de pago del user.
-- Nunca se borran: toggle de is_active.


-- name: CreatePaymentMethod :one
-- Usado tanto por el endpoint público como por Register (para crear Efectivo).
-- El CHECK del schema valida la combinación kind/allows_installments.
INSERT INTO payment_methods (owner_user_id, bank_id, name, kind, allows_installments)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;


-- name: GetPaymentMethodByID :one
SELECT * FROM payment_methods WHERE id = $1;


-- name: ListPaymentMethodsByOwner :many
-- Lista los métodos activos del user. Orden: primero por kind (para
-- agrupar visualmente), después por nombre.
SELECT * FROM payment_methods
WHERE owner_user_id = $1 AND is_active = true
ORDER BY kind ASC, name ASC;


-- name: ListAllPaymentMethodsByOwner :many
-- Incluye inactivos. Orden: primero los activos (is_active DESC) para
-- que la pantalla de configuración muestre los vigentes arriba y los
-- "borrados" debajo, con opción de revivirlos.
SELECT * FROM payment_methods
WHERE owner_user_id = $1
ORDER BY is_active DESC, kind ASC, name ASC;


-- name: UpdatePaymentMethod :one
-- Solo nombre, bank_id y allows_installments son editables.
-- kind y owner_user_id son inmutables (cambiarlos rompería historial).
-- El CHECK del schema se reevalúa en el UPDATE, así que intentar poner
-- allows_installments=true en un debit/cash/transfer falla en DB.
UPDATE payment_methods
SET name = $2,
    bank_id = $3,
    allows_installments = $4
WHERE id = $1
RETURNING *;


-- name: SetPaymentMethodActive :one
UPDATE payment_methods
SET is_active = $2
WHERE id = $1
RETURNING *;


-- name: CountActivePaymentMethodsByOwner :one
-- Usada en validaciones: no dejar al user sin ningún método activo
-- (Efectivo no se puede desactivar si es el último).
SELECT COUNT(*) AS total
FROM payment_methods
WHERE owner_user_id = $1 AND is_active = true;


-- name: GetPaymentMethodByOwnerAndName :one
-- Busca por (owner, name) sin filtrar por is_active. El service la usa
-- al crear un método: si encuentra una fila inactiva con ese nombre la
-- reactiva ("revive") preservando id e historial de expenses.
SELECT * FROM payment_methods
WHERE owner_user_id = $1 AND name = $2
LIMIT 1;


-- name: ReactivatePaymentMethod :one
-- Marca is_active=true y actualiza los campos mutables (bank_id,
-- allows_installments). kind es inmutable: si el user intenta crear con
-- un kind distinto al del registro inactivo, el service rechaza antes
-- de llamar acá, así que este UPDATE asume kind ya válido.
UPDATE payment_methods
SET is_active           = true,
    bank_id             = $2,
    allows_installments = $3
WHERE id = $1
RETURNING *;
