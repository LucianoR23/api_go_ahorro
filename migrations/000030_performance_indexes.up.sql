-- Plan 0.14 (audit §11 Performance).
--
-- 1. Indexes on the foreign keys that had none: the ten in the audit plus
--    eight more the catalog check found (an index that doesn't start with
--    the key's column doesn't count). Postgres does not index the
--    referencing side: without them, a parent delete or update scans the
--    whole child table, and joins on these columns cannot use an index.
--    TestForeignKeysAreIndexed keeps it that way for later migrations.
CREATE INDEX idx_incomes_payment_method             ON incomes(payment_method_id);
CREATE INDEX idx_recurring_expenses_category        ON recurring_expenses(category_id);
CREATE INDEX idx_recurring_expenses_payment_method  ON recurring_expenses(payment_method_id);
CREATE INDEX idx_recurring_expenses_created_by      ON recurring_expenses(created_by);
CREATE INDEX idx_recurring_incomes_received_by      ON recurring_incomes(received_by);
CREATE INDEX idx_recurring_incomes_payment_method   ON recurring_incomes(payment_method_id);
CREATE INDEX idx_goals_category                     ON budget_goals(category_id);
CREATE INDEX idx_settlements_to_user                ON settlement_payments(to_user);
CREATE INDEX idx_household_invites_invited_by       ON household_invites(invited_by);
CREATE INDEX idx_credit_cards_debit_payment_method  ON credit_cards(debit_payment_method_id);
CREATE INDEX idx_audit_events_actor                 ON audit_events(actor_id);
CREATE INDEX idx_expenses_category                  ON expenses(category_id);
CREATE INDEX idx_household_invites_accepted_by      ON household_invites(accepted_by);
CREATE INDEX idx_household_split_rules_user         ON household_split_rules(user_id);
CREATE INDEX idx_households_created_by              ON households(created_by);
CREATE INDEX idx_payment_methods_bank               ON payment_methods(bank_id);
CREATE INDEX idx_settlements_from_user              ON settlement_payments(from_user);

-- 2. expense_installments.household_id. Reports, goals, balances and the
--    due-soon reminder filter installments by household and date; with the
--    household only on expenses, Postgres had to read every installment in
--    the date range (all households) and join to filter. The column is a
--    copy of expenses.household_id, kept equal by a composite foreign key:
--    an installment can't point at an expense of another household.
ALTER TABLE expenses ADD CONSTRAINT expenses_id_household_key UNIQUE (id, household_id);

ALTER TABLE expense_installments ADD COLUMN household_id UUID;
UPDATE expense_installments i
SET household_id = e.household_id
FROM expenses e
WHERE e.id = i.expense_id;
ALTER TABLE expense_installments ALTER COLUMN household_id SET NOT NULL;

ALTER TABLE expense_installments
    DROP CONSTRAINT expense_installments_expense_id_fkey,
    ADD CONSTRAINT expense_installments_expense_household_fkey
        FOREIGN KEY (expense_id, household_id)
        REFERENCES expenses(id, household_id) ON DELETE CASCADE;

-- Date filters always come with a household now; the global date indexes
-- are replaced by household-first ones. The expression index matches the
-- COALESCE(due_date, billing_date) the queries use for "what is due".
DROP INDEX idx_installments_expense;
DROP INDEX idx_installments_billing;
DROP INDEX idx_installments_due_unpaid;
CREATE INDEX idx_installments_expense_household    ON expense_installments(expense_id, household_id);
CREATE INDEX idx_installments_household_billing    ON expense_installments(household_id, billing_date);
CREATE INDEX idx_installments_household_due        ON expense_installments(household_id, (COALESCE(due_date, billing_date)));
CREATE INDEX idx_installments_household_due_unpaid ON expense_installments(household_id, due_date) WHERE is_paid = false;
