-- Back to the 000020 indexes. Rows that 000020's indexes reject (several
-- alerts with the same ref on different days, etc.) would make the CREATE
-- fail, so the down keeps only the oldest row per old key first.

DROP INDEX IF EXISTS daily_insights_unique_event_ref;
DROP INDEX IF EXISTS daily_insights_unique_scheduled_ref;
DROP INDEX IF EXISTS daily_insights_unique_no_ref;

DELETE FROM daily_insights d
USING (
    SELECT id, row_number() OVER (
        PARTITION BY household_id, user_id, insight_type, ref_id
        ORDER BY created_at, id
    ) AS n
    FROM daily_insights
    WHERE ref_id IS NOT NULL
) dup
WHERE d.id = dup.id AND dup.n > 1;

CREATE UNIQUE INDEX daily_insights_unique_no_ref
    ON daily_insights (household_id, user_id, insight_date, insight_type)
    WHERE ref_id IS NULL;

CREATE UNIQUE INDEX daily_insights_unique_with_ref
    ON daily_insights (household_id, user_id, insight_type, ref_id)
    WHERE ref_id IS NOT NULL;
