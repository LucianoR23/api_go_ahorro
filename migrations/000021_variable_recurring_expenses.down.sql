DROP INDEX IF EXISTS idx_expenses_household_draft;

ALTER TABLE expenses
    DROP COLUMN IF EXISTS status;

ALTER TABLE recurring_expenses
    DROP COLUMN IF EXISTS last_confirmed_at,
    DROP COLUMN IF EXISTS last_amount,
    DROP COLUMN IF EXISTS alert_threshold_pct,
    DROP COLUMN IF EXISTS amount_is_variable;
