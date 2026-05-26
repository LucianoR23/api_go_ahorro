-- 000021_variable_recurring_expenses: soporte de monto variable mes a mes
-- para servicios tipo luz/expensas/wifi cuyo importe cambia pero la "serie"
-- es la misma (mismo proveedor, mismo cliente, mismo concepto).
--
-- Modelo:
--   * amount_is_variable=false (default) → comportamiento clásico:
--     el worker genera el expense con `amount` y queda confirmado.
--   * amount_is_variable=true → el worker genera un expense en estado
--     'draft' usando `amount` como estimado (último valor conocido).
--     El usuario confirma manualmente con el monto real de la factura.
--
-- alert_threshold_pct: si el actual_amount al confirmar supera al anterior
-- en este %, se dispara un insight tipo 'recurring_spike'. NULL = sin alerta.
--
-- last_amount + last_confirmed_at: cache para no recomputar stats en cada
-- listado. Se actualiza en el momento de confirmar el expense draft.

ALTER TABLE recurring_expenses
    ADD COLUMN amount_is_variable  BOOLEAN        NOT NULL DEFAULT false,
    ADD COLUMN alert_threshold_pct NUMERIC(5, 2)  CHECK (alert_threshold_pct > 0 AND alert_threshold_pct <= 500),
    ADD COLUMN last_amount         NUMERIC(12, 2),
    ADD COLUMN last_confirmed_at   TIMESTAMPTZ;

-- Estado del expense generado por una serie variable. Para gastos no variables
-- y gastos manuales el estado es 'confirmed' (default).
ALTER TABLE expenses
    ADD COLUMN status TEXT NOT NULL DEFAULT 'confirmed'
        CHECK (status IN ('draft', 'confirmed'));

-- Index parcial: la UI necesita listar rápido los drafts pendientes del hogar.
CREATE INDEX idx_expenses_household_draft
    ON expenses(household_id, spent_at DESC)
    WHERE status = 'draft';
