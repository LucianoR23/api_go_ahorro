DROP INDEX IF EXISTS idx_expenses_payment_method;
DROP INDEX IF EXISTS idx_expenses_shared;
DROP INDEX IF EXISTS idx_expenses_created_by;
DROP INDEX IF EXISTS idx_expenses_household_category;
DROP INDEX IF EXISTS idx_expenses_household_date;
DROP INDEX IF EXISTS idx_installment_shares_user;
DROP INDEX IF EXISTS idx_installments_due_unpaid;
DROP INDEX IF EXISTS idx_installments_billing;
DROP INDEX IF EXISTS idx_installments_expense;

DROP TABLE IF EXISTS expense_installment_shares;
DROP TABLE IF EXISTS expense_installments;
DROP TABLE IF EXISTS expenses;
