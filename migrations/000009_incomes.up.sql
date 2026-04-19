-- 000009_incomes: ingresos + ingresos recurrentes
--
-- incomes: registro simple (no tiene cuotas como expenses). Se usa para
-- cerrar la vista de savings = incomes(mes) - installments.due_date(mes).
-- amount_base está denormalizado con rate congelado al momento de crear
-- (mismo patrón que expenses) para que reportes históricos sean estables
-- aunque cambien las tasas.
--
-- recurring_incomes: plantilla que el worker 00:30 reproduce el día correcto
-- según frequency/day_of_month/day_of_week/month_of_year. last_generated
-- evita doble-ejecución si el worker se levanta dos veces el mismo día.
-- payment_method_id es opcional (SET NULL): es solo info, no descuenta de
-- ningún lado (plata entrante, no saliente).

CREATE TABLE incomes (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id      UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    received_by       UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    payment_method_id UUID REFERENCES payment_methods(id) ON DELETE SET NULL,

    amount        NUMERIC(12, 2) NOT NULL CHECK (amount > 0),
    currency      TEXT NOT NULL DEFAULT 'ARS' CHECK (currency IN ('ARS','USD','EUR')),
    amount_base   NUMERIC(14, 2) NOT NULL,
    base_currency TEXT NOT NULL CHECK (base_currency IN ('ARS','USD','EUR')),
    rate_used     NUMERIC(12, 4),
    rate_at       TIMESTAMPTZ,

    source      TEXT NOT NULL CHECK (source IN ('salary','freelance','gift','investment','refund','other')),
    description TEXT NOT NULL,
    received_at DATE NOT NULL DEFAULT CURRENT_DATE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE recurring_incomes (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id      UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    received_by       UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    payment_method_id UUID REFERENCES payment_methods(id) ON DELETE SET NULL,

    amount      NUMERIC(12, 2) NOT NULL CHECK (amount > 0),
    currency    TEXT NOT NULL DEFAULT 'ARS' CHECK (currency IN ('ARS','USD','EUR')),
    description TEXT NOT NULL,
    source      TEXT NOT NULL CHECK (source IN ('salary','freelance','gift','investment','refund','other')),

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

CREATE INDEX idx_incomes_household_date            ON incomes(household_id, received_at DESC);
CREATE INDEX idx_incomes_received_by               ON incomes(received_by);
CREATE INDEX idx_recurring_incomes_household_active ON recurring_incomes(household_id, is_active) WHERE is_active = true;
