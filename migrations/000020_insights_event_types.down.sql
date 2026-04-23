DROP INDEX IF EXISTS daily_insights_unique_with_ref;
DROP INDEX IF EXISTS daily_insights_unique_no_ref;

ALTER TABLE daily_insights DROP COLUMN IF EXISTS ref_id;

ALTER TABLE daily_insights
    ADD CONSTRAINT daily_insights_household_id_user_id_insight_date_insight_t_key
    UNIQUE (household_id, user_id, insight_date, insight_type);
