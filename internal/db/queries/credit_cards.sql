-- Queries de credit_cards. 1-a-1 con payment_methods de kind='credit'.
-- Se crean siempre en la misma transacción que el payment_method (opción A).


-- name: CreateCreditCard :one
INSERT INTO credit_cards (
    payment_method_id,
    alias,
    last_four,
    default_closing_day,
    default_due_day,
    debit_payment_method_id
)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;


-- name: GetCreditCardByPaymentMethodID :one
-- Devuelve el detalle de tarjeta asociado al payment_method dado.
-- Si no existe (método no es credit) → pgx.ErrNoRows → repo mapea a ErrNotFound.
SELECT * FROM credit_cards WHERE payment_method_id = $1;


-- name: ListCreditCardsByOwner :many
-- Lista las tarjetas activas del user con el payment_method embebido,
-- para evitar N+1 en el frontend (una sola llamada devuelve todo).
-- Filtra por payment_method.is_active = true: una tarjeta sin método
-- activo no se muestra.
SELECT sqlc.embed(cc), sqlc.embed(pm)
FROM credit_cards cc
INNER JOIN payment_methods pm ON pm.id = cc.payment_method_id
WHERE pm.owner_user_id = $1
  AND pm.is_active = true
ORDER BY cc.alias ASC;


-- name: UpdateCreditCard :one
-- Alias, last_four y ciclo son editables. payment_method_id es inmutable.
-- debit_payment_method_id cambiable (ej: cambiás la cuenta de débito automático).
-- La validación "mismo owner / no auto-referencia" se hace en el service.
UPDATE credit_cards
SET alias                   = $2,
    last_four               = $3,
    default_closing_day     = $4,
    default_due_day         = $5,
    debit_payment_method_id = $6
WHERE id = $1
RETURNING *;
