-- 000010_recurring_expenses: plantillas de gastos recurrentes.
--
-- Mismo patrón que recurring_incomes: la tabla guarda la "plantilla" y el
-- worker 00:30 reproduce expenses reales el día que corresponde según
-- frequency + day_of_month / day_of_week / month_of_year. last_generated
-- evita doble-creación si el worker tickea dos veces el mismo día.
--
-- Diferencias vs recurring_incomes:
--   - payment_method_id es NOT NULL (si es crédito, el Create de expenses
--     resuelve periodo + cuotas como siempre; si es débito/efectivo va
--     al contado igual que un expense manual)
--   - category_id nullable (lo mismo que expenses)
--   - installments > 0 — default 1; para crédito pueden ser N cuotas
--   - is_shared + currency + description — todos se pasan a expenses.Create
--     tal cual al generar
--
-- NO guardamos shares override acá: el split se resuelve al momento de
-- generar, usando los weights actuales del hogar (household_split_rules).
-- Si el usuario cambia los weights, los próximos gastos recurrentes los
-- toman automáticamente — simpler y más correcto que congelar overrides.

CREATE TABLE recurring_expenses (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id      UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    created_by        UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    category_id       UUID REFERENCES categories(id) ON DELETE SET NULL,
    payment_method_id UUID NOT NULL REFERENCES payment_methods(id) ON DELETE RESTRICT,

    amount       NUMERIC(12, 2) NOT NULL CHECK (amount > 0),
    currency     TEXT NOT NULL DEFAULT 'ARS' CHECK (currency IN ('ARS','USD','EUR')),
    description  TEXT NOT NULL,
    installments INT  NOT NULL DEFAULT 1 CHECK (installments BETWEEN 1 AND 60),
    is_shared    BOOLEAN NOT NULL DEFAULT false,

    frequency     TEXT NOT NULL CHECK (frequency IN ('monthly','weekly','yearly')),
    day_of_month  INT CHECK (day_of_month BETWEEN 1 AND 31),
    day_of_week   INT CHECK (day_of_week BETWEEN 0 AND 6),
    month_of_year INT CHECK (month_of_year BETWEEN 1 AND 12),

    is_active      BOOLEAN NOT NULL DEFAULT true,
    starts_at      DATE NOT NULL DEFAULT CURRENT_DATE,
    ends_at        DATE,
    last_generated DATE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_recurring_expenses_household_active
    ON recurring_expenses(household_id, is_active)
    WHERE is_active = true;

CREATE INDEX idx_recurring_expenses_household_created
    ON recurring_expenses(household_id, created_at DESC);
