-- name: GetNotificationPreferences :one
SELECT * FROM notification_preferences WHERE user_id = $1;

-- name: UpsertNotificationPreferences :one
INSERT INTO notification_preferences (user_id, disabled, quiet_enabled, quiet_start, quiet_end)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id) DO UPDATE SET
    disabled      = EXCLUDED.disabled,
    quiet_enabled = EXCLUDED.quiet_enabled,
    quiet_start   = EXCLUDED.quiet_start,
    quiet_end     = EXCLUDED.quiet_end,
    updated_at    = NOW()
RETURNING *;
