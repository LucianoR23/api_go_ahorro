-- Plan 4.3: card reminders (card_closing_soon, card_due_soon) happen once
-- per card and day, like goal alerts: they join the scheduled-with-ref
-- unique index and leave the event-with-ref one. No table changes.

DROP INDEX IF EXISTS daily_insights_unique_scheduled_ref;
DROP INDEX IF EXISTS daily_insights_unique_event_ref;

CREATE UNIQUE INDEX daily_insights_unique_scheduled_ref
    ON daily_insights (household_id, user_id, insight_type, ref_id, insight_date)
    NULLS NOT DISTINCT
    WHERE ref_id IS NOT NULL
      AND insight_type IN ('alert', 'credit_period_reminder', 'card_closing_soon', 'card_due_soon');

CREATE UNIQUE INDEX daily_insights_unique_event_ref
    ON daily_insights (household_id, user_id, insight_type, ref_id)
    NULLS NOT DISTINCT
    WHERE ref_id IS NOT NULL
      AND insight_type NOT IN ('alert', 'credit_period_reminder', 'card_closing_soon', 'card_due_soon');
