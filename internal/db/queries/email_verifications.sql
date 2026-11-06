-- Queries de email_verifications + flag de verificación en users.


-- name: CreateEmailVerification :one
-- new_email NULL verifies the current email; set, it confirms an email change.
INSERT INTO email_verifications (user_id, token_hash, expires_at, new_email)
VALUES ($1, $2, $3, $4)
RETURNING *;


-- name: GetEmailVerificationByTokenHash :one
SELECT * FROM email_verifications WHERE token_hash = $1;


-- name: MarkEmailVerificationUsed :one
-- Single-use condicional: solo matchea si aún no fue usado y no expiró.
UPDATE email_verifications
SET used_at = now()
WHERE id = $1
  AND used_at IS NULL
  AND expires_at > now()
RETURNING *;


-- name: InvalidateActiveEmailVerificationsForUser :exec
-- Al emitir un nuevo token, invalidamos los anteriores del user.
UPDATE email_verifications
SET used_at = now()
WHERE user_id = $1
  AND used_at IS NULL;


-- name: MarkUserEmailVerified :exec
-- Idempotente: si ya estaba verificado no hace nada.
UPDATE users
SET email_verified_at = now(),
    updated_at = now()
WHERE id = $1
  AND email_verified_at IS NULL;
