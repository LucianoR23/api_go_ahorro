-- Queries de households y household_members.


-- name: CreateHousehold :one
INSERT INTO households (name, base_currency, created_by)
VALUES ($1, $2, $3)
RETURNING *;


-- name: GetHouseholdByID :one
-- Filtra soft-deleted: un hogar borrado por su owner es invisible para
-- miembros y workers. Restore/purge se manejan en queries /admin/*.
SELECT * FROM households WHERE id = $1 AND deleted_at IS NULL;


-- name: GetHouseholdByIDIncludingDeleted :one
-- Versión "admin": devuelve la fila incluso si está soft-deleted. Usada
-- solo por los endpoints /admin/* (restore, purge).
SELECT * FROM households WHERE id = $1;


-- name: ListHouseholdsForUser :many
-- Lista todos los hogares a los que pertenece un user. Filtra soft-deleted
-- para que el user no vea hogares que fueron "borrados" (aunque siga su
-- membresía en la tabla).
SELECT h.*
FROM households h
INNER JOIN household_members hm ON hm.household_id = h.id
WHERE hm.user_id = $1 AND h.deleted_at IS NULL
ORDER BY h.created_at ASC;


-- name: ListAllHouseholdIDs :many
-- Para workers que iteran todos los hogares (insights, reports). Saltea
-- soft-deleted: un hogar borrado no debe generar insights ni reports.
SELECT id FROM households WHERE deleted_at IS NULL;


-- name: ListDeletedHouseholds :many
-- Para /admin/households/deleted: lista todos los hogares soft-deleted
-- ordenados por fecha de borrado (más reciente primero). JOIN con users
-- para devolver info del owner actual (preferimos el owner vigente en
-- household_members sobre created_by, porque pudo haber habido transfer).
--
-- COALESCE: si no hay owner actual (caso raro de data inconsistente),
-- cae al created_by para no perder la referencia.
SELECT
    h.id,
    h.name,
    h.base_currency,
    h.created_by,
    h.created_at,
    h.updated_at,
    h.deleted_at,
    COALESCE(owner_u.id, creator.id)            AS owner_id,
    COALESCE(owner_u.email, creator.email)      AS owner_email,
    COALESCE(owner_u.first_name, creator.first_name) AS owner_first_name,
    COALESCE(owner_u.last_name, creator.last_name)   AS owner_last_name
FROM households h
INNER JOIN users creator ON creator.id = h.created_by
LEFT JOIN household_members hm ON hm.household_id = h.id AND hm.role = 'owner'
LEFT JOIN users owner_u ON owner_u.id = hm.user_id
WHERE h.deleted_at IS NOT NULL
ORDER BY h.deleted_at DESC;


-- name: UpdateHousehold :one
UPDATE households
SET name = $2,
    base_currency = $3,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;


-- name: SoftDeleteHousehold :exec
-- Marca el hogar como borrado. Los miembros pierden acceso pero toda la
-- data (expenses, goals, settlements, etc.) queda intacta. El superadmin
-- puede restaurar o purgar desde /admin/*.
UPDATE households
SET deleted_at = now(),
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;


-- name: RestoreHousehold :exec
-- Admin-only: limpia deleted_at y deja el hogar accesible de nuevo.
UPDATE households
SET deleted_at = NULL,
    updated_at = now()
WHERE id = $1 AND deleted_at IS NOT NULL;


-- name: PurgeHousehold :exec
-- Admin-only: borrado físico. ON DELETE CASCADE arrastra miembros,
-- expenses, goals, settlements, split_rules, categories, invites. Irreversible.
DELETE FROM households WHERE id = $1;


-- ============================================================
-- household_members
-- ============================================================


-- name: AddHouseholdMember :one
INSERT INTO household_members (household_id, user_id, role)
VALUES ($1, $2, $3)
RETURNING *;


-- name: RemoveHouseholdMember :exec
DELETE FROM household_members
WHERE household_id = $1 AND user_id = $2;


-- name: RemoveAllMembershipsForUser :exec
-- Usado en DELETE /me: desvincula al user de todos los hogares. El caller
-- ya validó que no es owner de ninguno (sino rechazamos la baja).
DELETE FROM household_members WHERE user_id = $1;


-- name: ListHouseholdMembers :many
-- Devuelve los miembros con el nombre/email del user (JOIN).
-- Usamos sqlc.embed(u) para que genere un struct anidado con todo user,
-- así el handler puede devolver la info combinada sin queries extra.
SELECT sqlc.embed(u), hm.role, hm.joined_at
FROM household_members hm
INNER JOIN users u ON u.id = hm.user_id
WHERE hm.household_id = $1
ORDER BY hm.joined_at ASC;


-- name: IsHouseholdMember :one
-- Devuelve true si el user pertenece al hogar Y el hogar no está soft-deleted.
-- Un hogar borrado se comporta como si no existiera para todos los endpoints
-- del API. Esto es lo que garantiza que los miembros pierden acceso al instante
-- cuando el owner hace DELETE.
SELECT EXISTS (
    SELECT 1 FROM household_members hm
    INNER JOIN households h ON h.id = hm.household_id
    WHERE hm.household_id = $1 AND hm.user_id = $2 AND h.deleted_at IS NULL
) AS is_member;


-- name: UpdateHouseholdMemberRole :one
-- Actualiza el rol de un miembro. Devuelve la fila o pgx.ErrNoRows si el
-- par (household, user) no existe.
UPDATE household_members
SET role = $3
WHERE household_id = $1 AND user_id = $2
RETURNING *;


-- name: CountHouseholdOwners :one
-- Cuenta los owners vigentes de un hogar. Sirve para invariantes (ej:
-- no permitir demotions que dejen 0 owners).
SELECT COUNT(*) AS count
FROM household_members
WHERE household_id = $1 AND role = 'owner';


-- name: GetHouseholdMemberRole :one
-- Devuelve el rol del user en el household. Usada para chequear owner
-- antes de operaciones privilegiadas (editar/borrar hogar, invitar).
-- Si no es miembro devuelve pgx.ErrNoRows (el repo lo mapea a ErrNotFound).
SELECT role
FROM household_members
WHERE household_id = $1 AND user_id = $2;
