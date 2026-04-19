-- Queries sobre la tabla users.
-- Formato sqlc: cada query tiene una línea "-- name: NombreFuncion :tipo"
-- donde :tipo puede ser:
--   :one   devuelve exactamente 1 fila
--   :many  devuelve N filas ([]T)
--   :exec  ejecuta sin devolver filas (INSERT/UPDATE/DELETE simple)
--   :execrows  ejecuta y devuelve filas afectadas


-- name: CreateUser :one
-- Crea un usuario nuevo y devuelve la fila completa (para obtener id + timestamps).
INSERT INTO users (email, password_hash, name)
VALUES ($1, $2, $3)
RETURNING *;


-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;


-- name: GetUserByEmail :one
-- Usado en login. Devuelve pgx.ErrNoRows si no existe → mapeamos a error de dominio.
SELECT * FROM users WHERE email = $1;


-- name: UpdateUserName :one
UPDATE users
SET name = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;


-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = $2,
    updated_at = now()
WHERE id = $1;
