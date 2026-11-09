-- Back to the 000023 indexes. Card reminders repeated on several days would
-- break the event-with-ref index, so they go first.

DELETE FROM daily_insights WHERE insight_type IN ('card_closing_soon', 'card_due_soon');

DROP INDEX IF EXISTS daily_insights_unique_scheduled_ref;
DROP INDEX IF EXISTS daily_insights_unique_event_ref;

CREATE UNIQUE INDEX daily_insights_unique_scheduled_ref
    ON daily_insights (household_id, user_id, insight_type, ref_id, insight_date)
    NULLS NOT DISTINCT
    WHERE ref_id IS NOT NULL AND insight_type IN ('alert', 'credit_period_reminder');

CREATE UNIQUE INDEX daily_insights_unique_event_ref
    ON daily_insights (household_id, user_id, insight_type, ref_id)
    NULLS NOT DISTINCT
    WHERE ref_id IS NOT NULL AND insight_type NOT IN ('alert', 'credit_period_reminder');
