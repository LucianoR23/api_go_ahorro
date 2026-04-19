-- ============================================================
-- banks
-- ============================================================
-- Los bancos son del usuario, no del hogar. Mi caja de ahorro es mía,
-- no la comparto con mi pareja aunque vivamos juntos. Permite que cada
-- miembro registre sus propios bancos sin mezclar cuentas.
-- Nunca se borra: is_active=false preserva la referencia desde
-- payment_methods históricos.
CREATE TABLE banks (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          TEXT        NOT NULL,
    is_active     BOOLEAN     NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_user_id, name)
);

-- ============================================================
-- payment_methods
-- ============================================================
-- Medio de pago propiedad de un user. Al crear un expense se referencia
-- este ID para saber de dónde sale la plata.
-- bank_id es opcional: efectivo y algunas wallets no tienen banco atrás.
-- ON DELETE SET NULL en bank_id: si se "borra" (ver down) un banco,
-- el método sigue existiendo (el historial de gastos no se rompe).
--
-- Regla allows_installments según kind (ver CHECK abajo):
--   cash, debit, transfer → forzado false (no pueden tener cuotas)
--   credit                → forzado true  (para eso existe la tarjeta)
--   wallet                → configurable (MP sí, Brubank no)
CREATE TABLE payment_methods (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bank_id             UUID        REFERENCES banks(id) ON DELETE SET NULL,
    name                TEXT        NOT NULL,
    kind                TEXT        NOT NULL CHECK (kind IN ('cash', 'debit', 'credit', 'wallet', 'transfer')),
    allows_installments BOOLEAN     NOT NULL DEFAULT false,
    is_active           BOOLEAN     NOT NULL DEFAULT true,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_user_id, name),
    -- Invariante de negocio: allows_installments debe coincidir con kind
    -- salvo en wallet, que es la única variante configurable.
    CONSTRAINT payment_methods_installments_match_kind CHECK (
        (kind IN ('cash', 'debit', 'transfer') AND allows_installments = false) OR
        (kind = 'credit' AND allows_installments = true) OR
        (kind = 'wallet')
    )
);

-- ============================================================
-- credit_cards
-- ============================================================
-- Detalle específico para payment_methods de kind='credit'.
-- Relación 1-a-1 (UNIQUE en payment_method_id) con ON DELETE CASCADE:
-- si se elimina el método (solo en tests/rollback, prod usa is_active),
-- la credit_card se va con él.
--
-- default_closing_day / default_due_day: definen el ciclo del resumen.
-- Si un gasto tiene spent_at.day <= closing_day entra al resumen del
-- mes actual; si no, al siguiente. El día del mes se clamp-ea cuando
-- el mes no tiene ese día (ej: closing=31 en febrero).
--
-- debit_payment_method_id: cuenta de la que se debita el resumen
-- automáticamente (opcional). SET NULL si se "borra" esa cuenta, así
-- la tarjeta sigue viva aunque pierda su débito automático.
-- Se fuerza que referencia un método distinto y del mismo owner
-- (validación en service, no en DB por complejidad del CHECK).
CREATE TABLE credit_cards (
    id                      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_method_id       UUID        NOT NULL UNIQUE REFERENCES payment_methods(id) ON DELETE CASCADE,
    alias                   TEXT        NOT NULL,
    last_four               TEXT        CHECK (last_four IS NULL OR last_four ~ '^[0-9]{4}$'),
    default_closing_day     INT         NOT NULL CHECK (default_closing_day BETWEEN 1 AND 31),
    default_due_day         INT         NOT NULL CHECK (default_due_day BETWEEN 1 AND 31),
    debit_payment_method_id UUID        REFERENCES payment_methods(id) ON DELETE SET NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Índices parciales: solo indexamos activos, que son los que se listan
-- en el 99% de las queries (pantalla de "mis medios de pago").
CREATE INDEX idx_banks_owner           ON banks(owner_user_id)           WHERE is_active = true;
CREATE INDEX idx_payment_methods_owner ON payment_methods(owner_user_id) WHERE is_active = true;
