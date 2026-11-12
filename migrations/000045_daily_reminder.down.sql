DROP INDEX IF EXISTS idx_notification_preferences_reminder;
ALTER TABLE notification_preferences
    DROP CONSTRAINT IF EXISTS notification_preferences_reminder_step,
    DROP COLUMN IF EXISTS reminder_at,
    DROP COLUMN IF EXISTS reminder_enabled;
