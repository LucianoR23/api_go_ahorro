-- Períodos reales de una tarjeta de crédito. period_ym es el mes al que
-- pertenece la factura (derivado del mes de closing_date: "2026-05").
--
-- Diseño:
--   * closing_date/due_date son fechas completas, no solo días. Así evitamos
--     calcular ajustes por fin de mes/feriados/etc en el backend: el user
--     (o el frontend) carga exactamente lo que dice el resumen.
--   * due_date >= closing_date: el vencimiento nunca es anterior al cierre.
--     Si el banco avisa "cierre 20, vence 10", el "10" es del mes siguiente.
--   * PK (card, period_ym): una sola fila por mes por tarjeta. PUT hace upsert.
--   * Si no hay fila para un mes dado, el service de expenses cae a los
--     defaults (default_closing_day/default_due_day) de credit_cards.
CREATE TABLE credit_card_periods (
    credit_card_id  UUID NOT NULL REFERENCES credit_cards(id) ON DELETE CASCADE,
    period_ym       TEXT NOT NULL CHECK (period_ym ~ '^\d{4}-\d{2}$'),
    closing_date    DATE NOT NULL,
    due_date        DATE NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (credit_card_id, period_ym),
    CHECK (due_date >= closing_date)
);

CREATE INDEX idx_credit_card_periods_card ON credit_card_periods(credit_card_id, period_ym DESC);
