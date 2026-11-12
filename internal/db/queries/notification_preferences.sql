-- name: GetNotificationPreferences :one
SELECT * FROM notification_preferences WHERE user_id = $1;

-- name: UpsertNotificationPreferences :one
INSERT INTO notification_preferences (user_id, disabled, quiet_enabled, quiet_start, quiet_end, reminder_enabled, reminder_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (user_id) DO UPDATE SET
    disabled         = EXCLUDED.disabled,
    quiet_enabled    = EXCLUDED.quiet_enabled,
    quiet_start      = EXCLUDED.quiet_start,
    quiet_end        = EXCLUDED.quiet_end,
    reminder_enabled = EXCLUDED.reminder_enabled,
    reminder_at      = EXCLUDED.reminder_at,
    updated_at       = NOW()
RETURNING *;

-- name: DueDailyReminders :many
-- People whose daily reminder (plan 10.2) falls in [from_time, to_time) and
-- who loaded nothing since day_start: no expense they created and no income
-- they received, leaving out the ones generated from a recurring entry.
-- Only onboarded accounts with at least one device to push to.
SELECT p.user_id
FROM notification_preferences p
JOIN users u ON u.id = p.user_id
WHERE p.reminder_enabled
  AND p.reminder_at >= sqlc.arg(from_time)::time
  AND p.reminder_at < sqlc.arg(to_time)::time
  AND u.deleted_at IS NULL
  AND u.onboarded_at IS NOT NULL
  AND EXISTS (SELECT 1 FROM push_subscriptions s WHERE s.user_id = p.user_id)
  AND NOT EXISTS (
      SELECT 1 FROM expenses e
      WHERE e.created_by = p.user_id
        AND e.created_at >= sqlc.arg(day_start)::timestamptz
        AND e.recurring_expense_id IS NULL)
  AND NOT EXISTS (
      SELECT 1 FROM incomes i
      WHERE i.received_by = p.user_id
        AND i.created_at >= sqlc.arg(day_start)::timestamptz
        AND i.recurring_income_id IS NULL)
ORDER BY p.user_id;
