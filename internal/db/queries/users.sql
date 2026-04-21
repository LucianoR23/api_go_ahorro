-- Queries sobre la tabla users.
-- Formato sqlc: cada query tiene una línea "-- name: NombreFuncion :tipo"
-- donde :tipo puede ser:
--   :one   devuelve exactamente 1 fila
--   :many  devuelve N filas ([]T)
--   :exec  ejecuta sin devolver filas (INSERT/UPDATE/DELETE simple)
--   :execrows  ejecuta y devuelve filas afectadas


-- name: CreateUser :one
-- Crea un usuario nuevo y devuelve la fila completa (para obtener id + timestamps).
INSERT INTO users (email, password_hash, first_name, last_name)
VALUES ($1, $2, $3, $4)
RETURNING *;


-- name: GetUserByID :one
-- Filtra soft-deleted. Un token viejo de una cuenta borrada → pgx.ErrNoRows
-- → el middleware lo trata como sesión inválida.
SELECT * FROM users WHERE id = $1 AND deleted_at IS NULL;


-- name: GetUserByEmail :one
-- Usado en login. Devuelve pgx.ErrNoRows si no existe → mapeamos a error de dominio.
-- Filtra soft-deleted: un email anonimizado ya no matchea el formato original
-- pero además, aunque coincidiera, la fila queda invisible.
SELECT * FROM users WHERE email = $1 AND deleted_at IS NULL;


-- name: SoftDeleteUser :exec
-- Marca el user como borrado y anonimiza el email para liberar el UNIQUE
-- y permitir re-registro con el mismo email. El sufijo incluye el id
-- (único garantizado) + un dominio que no es deliverable.
UPDATE users
SET deleted_at = now(),
    email      = 'deleted+' || id::text || '@ahorra.deleted',
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;


-- name: CountHouseholdsOwnedByUser :one
-- Cuenta cuántos hogares tienen a este user como owner. Usado por la baja
-- de cuenta para exigir transferencia previa.
SELECT COUNT(*) AS count
FROM household_members
WHERE user_id = $1 AND role = 'owner';


-- name: UpdateUserName :one
-- Actualiza nombre y apellido juntos (si se edita uno se reenvían ambos).
UPDATE users
SET first_name = $2,
    last_name  = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;


-- name: UpdateUserProfile :one
-- Actualiza nombre/apellido/email en una sola query. El caller es
-- responsable de validar cada uno antes. La colisión de email la captura
-- el UNIQUE constraint y se mapea a ErrConflict en el repo.
UPDATE users
SET first_name = $2,
    last_name  = $3,
    email      = $4,
    updated_at = now()
WHERE id = $1
RETURNING *;


-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = $2,
    updated_at = now()
WHERE id = $1;


-- name: IsUserSuperadmin :one
-- Flag global para gatear endpoints /admin/*. Independiente del rol por-hogar.
-- Se setea manualmente por DB; no hay endpoint para modificarlo.
SELECT is_superadmin FROM users
WHERE id = $1 AND deleted_at IS NULL;
