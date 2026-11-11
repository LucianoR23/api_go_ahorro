-- 000043_move_between_spaces (plan 9.1): an expense can move between Mi plata
-- and a hogar. Its installments point at (expense_id, household_id), so the
-- pair follows the expense when its household changes.
ALTER TABLE expense_installments
    DROP CONSTRAINT expense_installments_expense_household_fkey,
    ADD CONSTRAINT expense_installments_expense_household_fkey
        FOREIGN KEY (expense_id, household_id)
        REFERENCES expenses(id, household_id) ON UPDATE CASCADE ON DELETE CASCADE;
