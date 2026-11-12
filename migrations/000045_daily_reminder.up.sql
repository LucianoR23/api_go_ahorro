-- 000045_daily_reminder (plan 10.2): an optional daily push asking whether
-- the person spent anything today, sent at a time they choose (app
-- timezone) only when they loaded nothing that day. Off by default.
-- reminder_at goes in 15-minute steps: the job runs every 15 minutes.
ALTER TABLE notification_preferences
    ADD COLUMN reminder_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN reminder_at      TIME    NOT NULL DEFAULT '21:00',
    ADD CONSTRAINT notification_preferences_reminder_step
        CHECK (date_part('minute', reminder_at)::int % 15 = 0 AND date_part('second', reminder_at) = 0);

-- The job looks up who is due in each 15-minute slot.
CREATE INDEX idx_notification_preferences_reminder
    ON notification_preferences (reminder_at) WHERE reminder_enabled;
