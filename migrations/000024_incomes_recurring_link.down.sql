DROP INDEX IF EXISTS idx_incomes_recurring;
ALTER TABLE incomes DROP COLUMN IF EXISTS recurring_income_id;
