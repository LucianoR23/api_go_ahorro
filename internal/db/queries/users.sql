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


-- name: CreateUserWithoutPassword :one
-- Registro vía OAuth (Google): sin password_hash, con email ya verificado
-- (el provider externo nos garantiza la propiedad del email).
INSERT INTO users (email, password_hash, first_name, last_name, email_verified_at)
VALUES ($1, NULL, $2, $3, now())
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
    email      = 'deleted+' || id::text || '@deleted.invalid',
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;


-- name: CountHouseholdsOwnedByUser :one
-- Active shared households the user owns. Account deletion requires a
-- transfer first; a soft-deleted household does not count (plan 0.8), and
-- "Mi plata" goes with the account (plan 1.2).
SELECT COUNT(*) AS count
FROM household_members hm
JOIN households h ON h.id = hm.household_id
WHERE hm.user_id = $1 AND hm.role = 'owner' AND h.kind = 'shared' AND h.deleted_at IS NULL;


-- name: SoftDeletePersonalHouseholdOfUser :exec
-- Account deletion: "Mi plata" is soft-deleted with the account, its data
-- kept like any deleted household (plan 1.2).
UPDATE households
SET deleted_at = now(), updated_at = now()
WHERE created_by = $1 AND kind = 'personal' AND deleted_at IS NULL;


-- name: LockUserForDeletion :one
-- Account deletion: locks the user row and, through the second statement,
-- their memberships, so an ownership transfer to them waits for the
-- deletion to commit.
SELECT id FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;


-- name: LockUserMemberships :exec
SELECT 1 FROM household_members WHERE user_id = $1 FOR UPDATE;


-- name: DeleteUserPushSubscriptions :exec
DELETE FROM push_subscriptions WHERE user_id = $1;


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


-- name: SetUserPendingEmail :exec
-- An email change waiting for the link sent to the new address (plan 0.5).
UPDATE users SET pending_email = $2, updated_at = now() WHERE id = $1;


-- name: ApplyUserEmailChange :one
-- The new address was verified: it becomes the email, verified, and the
-- pending one is cleared. A collision with another account hits the UNIQUE
-- constraint (the repo maps it to ErrConflict).
UPDATE users
SET email             = sqlc.arg(new_email),
    pending_email     = NULL,
    email_verified_at = now(),
    updated_at        = now()
WHERE id = $1 AND deleted_at IS NULL
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
