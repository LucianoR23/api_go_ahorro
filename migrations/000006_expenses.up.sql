-- Core del producto. Cada gasto puede materializarse en 1..N cuotas
-- (credit) o 1 sola cuota (cash/debit/wallet/transfer).
--
-- Decisiones:
--   * ON DELETE CASCADE para expenses→household (si borras el hogar, se van todos los gastos).
--   * ON DELETE RESTRICT para created_by (no se puede borrar un user que registró gastos sin
--     antes resolverlos — mantiene auditoría).
--   * ON DELETE SET NULL para category_id (si borrás una categoría, los gastos conservan
--     el monto e info, solo pierden el tag).
--   * ON DELETE RESTRICT para payment_method_id (no podés borrar un medio con gastos;
--     desactivalo en vez de borrar).
--
-- Denormalización de conversión: guardamos amount + amount_base + rate_used + rate_at
-- en el gasto y también en cada cuota. Si el blue sube mañana, el café de hoy no cambia.
CREATE TABLE expenses (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id       UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    created_by         UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    category_id        UUID REFERENCES categories(id) ON DELETE SET NULL,
    payment_method_id  UUID NOT NULL REFERENCES payment_methods(id) ON DELETE RESTRICT,

    amount             NUMERIC(12, 2) NOT NULL CHECK (amount > 0),
    currency           TEXT NOT NULL DEFAULT 'ARS' CHECK (currency IN ('ARS','USD','EUR')),
    amount_base        NUMERIC(14, 2) NOT NULL,
    base_currency      TEXT NOT NULL CHECK (base_currency IN ('ARS','USD','EUR')),
    rate_used          NUMERIC(12, 4),
    rate_at            TIMESTAMPTZ,

    description        TEXT NOT NULL,
    spent_at           DATE NOT NULL DEFAULT CURRENT_DATE,

    installments       INT NOT NULL DEFAULT 1 CHECK (installments BETWEEN 1 AND 60),
    is_shared          BOOLEAN NOT NULL DEFAULT false,

    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Para credit: N filas (una por cuota).
-- Para el resto (cash/debit/wallet/transfer): 1 sola fila con billing_date = spent_at,
-- due_date NULL, is_paid = true (ya se pagó en el momento).
CREATE TABLE expense_installments (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    expense_id                UUID NOT NULL REFERENCES expenses(id) ON DELETE CASCADE,
    installment_number        INT NOT NULL CHECK (installment_number >= 1),
    installment_amount        NUMERIC(12, 2) NOT NULL CHECK (installment_amount > 0),
    installment_amount_base   NUMERIC(14, 2) NOT NULL,
    billing_date              DATE NOT NULL,
    due_date                  DATE,
    is_paid                   BOOLEAN NOT NULL DEFAULT false,
    paid_at                   TIMESTAMPTZ,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (expense_id, installment_number)
);

CREATE INDEX idx_installments_expense     ON expense_installments(expense_id);
CREATE INDEX idx_installments_billing     ON expense_installments(billing_date);
CREATE INDEX idx_installments_due_unpaid  ON expense_installments(due_date) WHERE is_paid = false;

-- Shares por cuota (solo si expenses.is_shared = true).
-- Una fila por (cuota, user) con monto que le toca a ese user.
-- amount_base_owed puede ser 0 si ese user quedó excluido por split rule.
CREATE TABLE expense_installment_shares (
    installment_id    UUID NOT NULL REFERENCES expense_installments(id) ON DELETE CASCADE,
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount_base_owed  NUMERIC(14, 2) NOT NULL CHECK (amount_base_owed >= 0),
    PRIMARY KEY (installment_id, user_id)
);

CREATE INDEX idx_installment_shares_user ON expense_installment_shares(user_id);

-- Índices de acceso frecuente en expenses.
CREATE INDEX idx_expenses_household_date     ON expenses(household_id, spent_at DESC);
CREATE INDEX idx_expenses_household_category ON expenses(household_id, category_id);
CREATE INDEX idx_expenses_created_by         ON expenses(created_by);
CREATE INDEX idx_expenses_shared             ON expenses(household_id, is_shared) WHERE is_shared = true;
CREATE INDEX idx_expenses_payment_method     ON expenses(payment_method_id);
