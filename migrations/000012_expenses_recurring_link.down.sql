DROP INDEX IF EXISTS idx_expenses_recurring;
ALTER TABLE expenses DROP COLUMN IF EXISTS recurring_expense_id;
