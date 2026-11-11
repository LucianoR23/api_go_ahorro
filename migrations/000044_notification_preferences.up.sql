-- 000044_notification_preferences (plan 10.1): what each person wants to
-- hear about, for every space they are in. No row means the defaults:
-- everything on and quiet hours from 23:00 to 08:00 (app timezone).
--
-- disabled holds "kind.channel" entries that are turned off, for example
-- 'shared_expense.push' or 'monthly_report.email'. The API validates them
-- against its catalog, so a new kind needs no migration.
CREATE TABLE notification_preferences (
    user_id       UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    disabled      TEXT[]      NOT NULL DEFAULT '{}',
    quiet_enabled BOOLEAN     NOT NULL DEFAULT true,
    quiet_start   TIME        NOT NULL DEFAULT '23:00',
    quiet_end     TIME        NOT NULL DEFAULT '08:00',
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (quiet_start <> quiet_end),
    CHECK (cardinality(disabled) <= 50)
);

-- insight_kind maps an in-app notice to the preference kind that mutes it.
-- Invites and tips have no kind of their own: they are always shown.
CREATE FUNCTION insight_kind(p_type TEXT, p_metadata JSONB) RETURNS TEXT
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN p_type = 'shared_expense' THEN 'shared_expense'
        WHEN p_type = 'settlement' THEN 'settlement'
        WHEN p_type IN ('card_closing_soon', 'card_due_soon', 'credit_period_reminder') THEN 'cards'
        WHEN p_type = 'alert' AND p_metadata ->> 'kind' = 'installment_due_soon' THEN 'installments'
        WHEN p_type = 'alert' THEN 'goals'
        WHEN p_type = 'recurring_spike' THEN 'recurring'
        WHEN p_type IN ('weekly_review', 'daily_summary') THEN 'weekly_review'
        ELSE NULL
    END
$$;
