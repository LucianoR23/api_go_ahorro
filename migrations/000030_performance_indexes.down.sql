DROP INDEX idx_installments_household_due_unpaid;
DROP INDEX idx_installments_household_due;
DROP INDEX idx_installments_household_billing;
DROP INDEX idx_installments_expense_household;
CREATE INDEX idx_installments_expense    ON expense_installments(expense_id);
CREATE INDEX idx_installments_billing    ON expense_installments(billing_date);
CREATE INDEX idx_installments_due_unpaid ON expense_installments(due_date) WHERE is_paid = false;

ALTER TABLE expense_installments
    DROP CONSTRAINT expense_installments_expense_household_fkey,
    ADD CONSTRAINT expense_installments_expense_id_fkey
        FOREIGN KEY (expense_id) REFERENCES expenses(id) ON DELETE CASCADE;
ALTER TABLE expense_installments DROP COLUMN household_id;
ALTER TABLE expenses DROP CONSTRAINT expenses_id_household_key;

DROP INDEX idx_settlements_from_user;
DROP INDEX idx_payment_methods_bank;
DROP INDEX idx_households_created_by;
DROP INDEX idx_household_split_rules_user;
DROP INDEX idx_household_invites_accepted_by;
DROP INDEX idx_expenses_category;
DROP INDEX idx_audit_events_actor;
DROP INDEX idx_credit_cards_debit_payment_method;
DROP INDEX idx_household_invites_invited_by;
DROP INDEX idx_settlements_to_user;
DROP INDEX idx_goals_category;
DROP INDEX idx_recurring_incomes_payment_method;
DROP INDEX idx_recurring_incomes_received_by;
DROP INDEX idx_recurring_expenses_created_by;
DROP INDEX idx_recurring_expenses_payment_method;
DROP INDEX idx_recurring_expenses_category;
DROP INDEX idx_incomes_payment_method;
