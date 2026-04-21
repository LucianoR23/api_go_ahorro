-- Queries de household_invites.


-- name: CreateHouseholdInvite :one
INSERT INTO household_invites (household_id, email, token_hash, invited_by, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;


-- name: GetHouseholdInviteByTokenHash :one
SELECT * FROM household_invites WHERE token_hash = $1;


-- name: GetHouseholdInviteByID :one
SELECT * FROM household_invites WHERE id = $1;


-- name: ListPendingInvitesForHousehold :many
-- Invitaciones activas (no aceptadas, no revocadas, no expiradas) de un hogar.
SELECT *
FROM household_invites
WHERE household_id = $1
  AND accepted_at IS NULL
  AND revoked_at IS NULL
  AND expires_at > now()
ORDER BY created_at DESC;


-- name: MarkInviteAccepted :one
UPDATE household_invites
SET accepted_at = now(),
    accepted_by = $2
WHERE id = $1
  AND accepted_at IS NULL
  AND revoked_at IS NULL
  AND expires_at > now()
RETURNING *;


-- name: RevokeInvite :one
UPDATE household_invites
SET revoked_at = now()
WHERE id = $1
  AND accepted_at IS NULL
  AND revoked_at IS NULL
RETURNING *;


-- name: RefreshInviteToken :one
-- Pisa el token_hash y expires_at de una invitación pendiente. Se usa para
-- "reenviar" — genera un nuevo token, invalida implícitamente el anterior
-- (el hash previo ya no existe) y extiende la ventana. Solo matchea si la
-- invitación sigue pendiente (ni aceptada ni revocada).
UPDATE household_invites
SET token_hash = $2,
    expires_at = $3
WHERE id = $1
  AND accepted_at IS NULL
  AND revoked_at IS NULL
RETURNING *;
