-- Plan 4.1: credit card statements.
--
-- A statement is the sum of a card's installments whose billing_date falls
-- in its closing month (period_ym), plus manual adjustments. Totals are not
-- stored: they are computed from the installments, so editing an expense
-- never leaves a stale total. These tables keep only what the user enters:
-- the minimum payment, the payment and the adjustments.

CREATE TABLE credit_card_statements (
    credit_card_id  UUID NOT NULL REFERENCES credit_cards(id) ON DELETE CASCADE,
    period_ym       TEXT NOT NULL CHECK (period_ym ~ '^\d{4}-(0[1-9]|1[0-2])$'),
    -- In pesos: Argentine statements state the minimum in ARS.
    minimum_payment NUMERIC(14, 2) CHECK (minimum_payment >= 0),
    paid_at         TIMESTAMPTZ,
    paid_amount     NUMERIC(14, 2) CHECK (paid_amount > 0),
    paid_currency   TEXT CHECK (paid_currency IN ('ARS', 'USD', 'EUR')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (credit_card_id, period_ym),
    -- A payment has all three or none.
    CONSTRAINT credit_card_statements_payment_complete CHECK (
        (paid_at IS NULL AND paid_amount IS NULL AND paid_currency IS NULL)
        OR (paid_at IS NOT NULL AND paid_amount IS NOT NULL AND paid_currency IS NOT NULL)
    )
);

-- Interest, taxes, fees and credits printed on the statement but not
-- loaded as expenses. "credit" subtracts; the others add.
CREATE TABLE credit_card_statement_adjustments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    credit_card_id UUID NOT NULL,
    period_ym      TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('interest', 'tax', 'fee', 'credit', 'other')),
    description    TEXT NOT NULL CHECK (char_length(description) BETWEEN 1 AND 80),
    amount         NUMERIC(14, 2) NOT NULL CHECK (amount > 0),
    currency       TEXT NOT NULL DEFAULT 'ARS' CHECK (currency IN ('ARS', 'USD', 'EUR')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT credit_card_statement_adjustments_statement_fk
        FOREIGN KEY (credit_card_id, period_ym)
        REFERENCES credit_card_statements (credit_card_id, period_ym) ON DELETE CASCADE
);

CREATE INDEX idx_card_statement_adjustments_statement
    ON credit_card_statement_adjustments (credit_card_id, period_ym);
