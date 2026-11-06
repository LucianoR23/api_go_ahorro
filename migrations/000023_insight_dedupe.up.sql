-- Plan 0.3: insight dedupe.
--
-- 000020 left three holes:
--   * user_id is NULL for household-level insights, and NULLs are distinct in a
--     unique index, so running the job twice in a day duplicated them.
--   * goal alerts had no ref_id, so they were unique per (user, day): a second
--     goal over its limit the same day was dropped. They now carry the goal id.
--   * the ref_id index had no date, so scheduled insights with a ref (goal
--     alerts, card closing reminders) could only ever happen once per ref.
--
-- New shape (all NULLS NOT DISTINCT, Postgres 15+):
--   * no ref: one per (household, user, day, type)                 [summaries, reviews, savings alerts]
--   * scheduled with ref: one per (household, user, type, ref, day) [goal and due-soon alerts, card reminders]
--   * event with ref: one per (household, user, type, ref)          [shared expense, invite, settlement, spike]
--
-- Existing duplicates under the new keys are removed first, keeping the
-- oldest row of each group (only repeated notifications go, never the first).

DELETE FROM daily_insights d
USING (
    SELECT id, row_number() OVER (
        PARTITION BY household_id, user_id, insight_date, insight_type
        ORDER BY created_at, id
    ) AS n
    FROM daily_insights
    WHERE ref_id IS NULL
) dup
WHERE d.id = dup.id AND dup.n > 1;

DELETE FROM daily_insights d
USING (
    SELECT id, row_number() OVER (
        PARTITION BY household_id, user_id, insight_type, ref_id, insight_date
        ORDER BY created_at, id
    ) AS n
    FROM daily_insights
    WHERE ref_id IS NOT NULL AND insight_type IN ('alert', 'credit_period_reminder')
) dup
WHERE d.id = dup.id AND dup.n > 1;

DELETE FROM daily_insights d
USING (
    SELECT id, row_number() OVER (
        PARTITION BY household_id, user_id, insight_type, ref_id
        ORDER BY created_at, id
    ) AS n
    FROM daily_insights
    WHERE ref_id IS NOT NULL AND insight_type NOT IN ('alert', 'credit_period_reminder')
) dup
WHERE d.id = dup.id AND dup.n > 1;

DROP INDEX IF EXISTS daily_insights_unique_no_ref;
DROP INDEX IF EXISTS daily_insights_unique_with_ref;

CREATE UNIQUE INDEX daily_insights_unique_no_ref
    ON daily_insights (household_id, user_id, insight_date, insight_type)
    NULLS NOT DISTINCT
    WHERE ref_id IS NULL;

CREATE UNIQUE INDEX daily_insights_unique_scheduled_ref
    ON daily_insights (household_id, user_id, insight_type, ref_id, insight_date)
    NULLS NOT DISTINCT
    WHERE ref_id IS NOT NULL AND insight_type IN ('alert', 'credit_period_reminder');

CREATE UNIQUE INDEX daily_insights_unique_event_ref
    ON daily_insights (household_id, user_id, insight_type, ref_id)
    NULLS NOT DISTINCT
    WHERE ref_id IS NOT NULL AND insight_type NOT IN ('alert', 'credit_period_reminder');
