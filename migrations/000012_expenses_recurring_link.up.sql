-- Enlace opcional expense → recurring_expense que lo generó.
-- NULL = gasto variable (manual); NOT NULL = gasto recurrente/fijo.
-- ON DELETE SET NULL: si borran la plantilla recurrente, los expenses
-- históricos se mantienen pero pierden el link (se vuelven "variables").
ALTER TABLE expenses
    ADD COLUMN recurring_expense_id UUID REFERENCES recurring_expenses(id) ON DELETE SET NULL;

CREATE INDEX idx_expenses_recurring ON expenses(recurring_expense_id) WHERE recurring_expense_id IS NOT NULL;
