-- Queries de households y household_members.


-- name: CreateHousehold :one
INSERT INTO households (name, base_currency, created_by)
VALUES ($1, $2, $3)
RETURNING *;


-- name: GetHouseholdByID :one
SELECT * FROM households WHERE id = $1;


-- name: ListHouseholdsForUser :many
-- Lista todos los hogares a los que pertenece un user.
-- JOIN con household_members para filtrar por membresía.
SELECT h.*
FROM households h
INNER JOIN household_members hm ON hm.household_id = h.id
WHERE hm.user_id = $1
ORDER BY h.created_at ASC;


-- name: UpdateHousehold :one
UPDATE households
SET name = $2,
    base_currency = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;


-- name: DeleteHousehold :exec
-- ON DELETE CASCADE en household_members → limpia la membresía automáticamente.
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
-- Devuelve true si el user pertenece al hogar. Usada por el middleware de autz.
SELECT EXISTS (
    SELECT 1 FROM household_members
    WHERE household_id = $1 AND user_id = $2
) AS is_member;


-- name: GetHouseholdMemberRole :one
-- Devuelve el rol del user en el household. Usada para chequear owner
-- antes de operaciones privilegiadas (editar/borrar hogar, invitar).
-- Si no es miembro devuelve pgx.ErrNoRows (el repo lo mapea a ErrNotFound).
SELECT role
FROM household_members
WHERE household_id = $1 AND user_id = $2;
