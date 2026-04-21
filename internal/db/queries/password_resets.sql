-- Queries de password_resets.


-- name: CreatePasswordReset :one
INSERT INTO password_resets (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING *;


-- name: GetPasswordResetByTokenHash :one
SELECT * FROM password_resets WHERE token_hash = $1;


-- name: MarkPasswordResetUsed :one
-- Single-use: solo matchea si no fue usado aún y no expiró.
UPDATE password_resets
SET used_at = now()
WHERE id = $1
  AND used_at IS NULL
  AND expires_at > now()
RETURNING *;


-- name: InvalidateActivePasswordResetsForUser :exec
-- Al emitir un nuevo token, invalidamos los anteriores del user (los
-- marcamos como usados). Así el último mail es el único válido.
UPDATE password_resets
SET used_at = now()
WHERE user_id = $1
  AND used_at IS NULL;
