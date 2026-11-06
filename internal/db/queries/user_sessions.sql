-- ===================== user_sessions (plan 0.5) =====================

-- name: CreateUserSession :one
INSERT INTO user_sessions (user_id, refresh_hash, user_agent, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUserSessionByRefreshHash :one
SELECT * FROM user_sessions WHERE refresh_hash = $1;

-- name: GetUserSessionByPrevRefreshHash :one
SELECT * FROM user_sessions WHERE prev_refresh_hash = $1;

-- name: RotateUserSession :one
-- Conditional on the current hash, so two concurrent refreshes with the same
-- token cannot both rotate: the loser gets no row.
UPDATE user_sessions
SET prev_refresh_hash = refresh_hash,
    refresh_hash      = sqlc.arg(new_hash),
    rotated_at        = now(),
    last_used_at      = now(),
    expires_at        = sqlc.arg(expires_at)
WHERE id = sqlc.arg(id) AND refresh_hash = sqlc.arg(current_hash) AND revoked_at IS NULL
RETURNING *;

-- name: RevokeUserSession :exec
UPDATE user_sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeAllUserSessions :exec
UPDATE user_sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL;

-- name: RevokeOtherUserSessions :exec
UPDATE user_sessions SET revoked_at = now()
WHERE user_id = $1 AND id <> sqlc.arg(keep_id) AND revoked_at IS NULL;

-- name: DeleteStaleUserSessions :exec
-- Housekeeping on each sign-in: drops this user's sessions that expired or
-- were revoked more than a day ago.
DELETE FROM user_sessions
WHERE user_id = $1
  AND (expires_at < now() - interval '1 day' OR revoked_at < now() - interval '1 day');
