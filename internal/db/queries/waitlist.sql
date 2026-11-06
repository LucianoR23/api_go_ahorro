-- ===================== waitlist_entries (plan B.5) =====================

-- name: GetWaitlistEntryByEmail :one
SELECT * FROM waitlist_entries WHERE email = $1;

-- name: CreateWaitlistEntry :one
INSERT INTO waitlist_entries (email, monotributo, form, source, medium, campaign, referrer_path,
                              notice_version, confirm_token_hash, confirm_expires_at, last_sent_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
RETURNING *;

-- name: RefreshWaitlistEntry :exec
-- A new sign-up for an unconfirmed address: new confirmation token, latest
-- notice version; the interest flag only turns on; first-touch attribution
-- is kept.
UPDATE waitlist_entries
SET monotributo        = monotributo OR sqlc.arg(monotributo)::bool,
    notice_version     = sqlc.arg(notice_version),
    confirm_token_hash = sqlc.arg(confirm_token_hash),
    confirm_expires_at = sqlc.arg(confirm_expires_at),
    last_sent_at       = now()
WHERE id = sqlc.arg(id) AND confirmed_at IS NULL;

-- name: GetWaitlistEntryByConfirmHash :one
SELECT * FROM waitlist_entries WHERE confirm_token_hash = $1;

-- name: ConfirmWaitlistEntry :exec
UPDATE waitlist_entries SET confirmed_at = now() WHERE id = $1 AND confirmed_at IS NULL;

-- name: DeleteWaitlistEntry :execrows
DELETE FROM waitlist_entries WHERE id = $1;

-- name: DeleteWaitlistEntryByEmail :execrows
DELETE FROM waitlist_entries WHERE email = $1;

-- name: DeleteStaleWaitlistEntries :execrows
-- Unconfirmed sign-ups older than 30 days (contract section 5).
DELETE FROM waitlist_entries WHERE confirmed_at IS NULL AND created_at < now() - interval '30 days';

-- name: ListConfirmedWaitlistEntries :many
SELECT * FROM waitlist_entries WHERE confirmed_at IS NOT NULL ORDER BY confirmed_at;
