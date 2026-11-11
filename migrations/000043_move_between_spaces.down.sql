ALTER TABLE expense_installments
    DROP CONSTRAINT expense_installments_expense_household_fkey,
    ADD CONSTRAINT expense_installments_expense_household_fkey
        FOREIGN KEY (expense_id, household_id)
        REFERENCES expenses(id, household_id) ON DELETE CASCADE;
