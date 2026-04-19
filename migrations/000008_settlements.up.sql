-- CP7: libro de deudas entre miembros.
--
-- household_split_rules: peso por miembro para dividir gastos compartidos.
--   weight decimal libre (se normaliza al dividir: share_i = weight_i / SUM × amount).
--   Default weight=1.0 → división equitativa.
--   Se seedea en households.Service.Create (owner) y AddMember (invitado).
--
-- settlement_payments: registro de "A le pagó X a B" para saldar deuda.
--   NO lleva payment_method — es solo un registro del libro. La plata se movió
--   afuera (transferencia, efectivo). El service valida amount_base ≤ deuda_actual.
--   ON DELETE RESTRICT en from_user/to_user: no podés borrar un user que tiene
--   settlements pendientes sin antes resolverlos.

CREATE TABLE household_split_rules (
    household_id  UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    weight        NUMERIC(8, 4) NOT NULL DEFAULT 1.0 CHECK (weight >= 0),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (household_id, user_id)
);

CREATE TABLE settlement_payments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id   UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    from_user      UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    to_user        UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    amount_base    NUMERIC(14, 2) NOT NULL CHECK (amount_base > 0),
    base_currency  TEXT NOT NULL CHECK (base_currency IN ('ARS','USD','EUR')),
    note           TEXT,
    paid_at        DATE NOT NULL DEFAULT CURRENT_DATE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (from_user <> to_user)
);

CREATE INDEX idx_split_rules_household ON household_split_rules(household_id);
CREATE INDEX idx_settlements_household ON settlement_payments(household_id);
CREATE INDEX idx_settlements_pair      ON settlement_payments(household_id, from_user, to_user);
CREATE INDEX idx_settlements_paid_at   ON settlement_payments(household_id, paid_at DESC);
