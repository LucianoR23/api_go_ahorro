-- ===================== Inbound email (plan 3.5) =====================

-- name: GetInboundAddress :one
SELECT * FROM inbound_addresses WHERE user_id = $1;

-- name: CreateInboundAddress :one
-- Lazily, the first time the user opens the screen. A concurrent first
-- open keeps the row that won.
INSERT INTO inbound_addresses (user_id, local_hash, local_enc, key_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE SET user_id = EXCLUDED.user_id
RETURNING *;

-- name: InboundRecipient :one
-- The user a mail is for, with the account email (always an accepted sender).
SELECT a.user_id, a.sender_hashes, u.email
FROM inbound_addresses a
JOIN users u ON u.id = a.user_id
WHERE a.local_hash = $1 AND u.deleted_at IS NULL;

-- name: AcceptInboundSender :exec
UPDATE inbound_addresses
SET sender_hashes = array_append(sender_hashes, sqlc.arg(sender_hash)::bytea)
WHERE user_id = sqlc.arg(user_id)
  AND NOT (sqlc.arg(sender_hash)::bytea = ANY (sender_hashes))
  AND cardinality(sender_hashes) < 10;

-- name: InsertInboundMessage :one
-- A retry of the same mail (same Message-ID) is ignored: no row comes back.
INSERT INTO inbound_messages (user_id, dedupe_hash, kind, status, source, parsed, sender_enc, gmail_code)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (user_id, dedupe_hash) DO NOTHING
RETURNING id;

-- name: ListOpenInboundMessages :many
-- What the app shows: open expense mails and Gmail codes of the last day.
SELECT * FROM inbound_messages
WHERE user_id = $1
  AND ((kind = 'expense' AND status IN ('pending', 'sender_pending'))
       OR (kind = 'gmail_confirmation' AND received_at >= sqlc.arg(codes_since)::timestamptz))
ORDER BY received_at DESC
LIMIT 50;

-- name: GetInboundMessage :one
SELECT * FROM inbound_messages WHERE id = $1 AND user_id = $2;

-- name: ResolveInboundMessage :execrows
-- Confirm or dismiss an open message; the sender address is dropped.
UPDATE inbound_messages
SET status = sqlc.arg(status), expense_id = sqlc.narg(expense_id),
    resolved_at = NOW(), sender_enc = NULL
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id)
  AND kind = 'expense' AND status IN ('pending', 'sender_pending');

-- name: AcceptInboundMessageSender :execrows
UPDATE inbound_messages
SET status = 'pending', sender_enc = NULL
WHERE id = $1 AND user_id = $2 AND status = 'sender_pending';

-- name: DeleteOldInboundMessages :execrows
-- Retention (plan §8b): Gmail codes after a day; resolved, unreadable and
-- ignored mails after 30 days. Open ones stay until the user acts.
DELETE FROM inbound_messages
WHERE (kind = 'gmail_confirmation' AND received_at < sqlc.arg(codes_before)::timestamptz)
   OR (status IN ('confirmed', 'dismissed', 'unreadable')
       AND COALESCE(resolved_at, received_at) < sqlc.arg(resolved_before)::timestamptz);

-- name: CountSenderPending :one
-- Caps what an unknown sender can pile up in someone's list.
SELECT COUNT(*)::int FROM inbound_messages
WHERE user_id = $1 AND status = 'sender_pending';
