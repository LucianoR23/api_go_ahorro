-- Extensiones necesarias.
-- pgcrypto: gen_random_uuid() para PKs.
-- citext:   emails case-insensitive sin tener que lower() en cada comparación.
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

-- ============================================================
-- users
-- ============================================================
-- Usuario de la aplicación. El email es la identidad única.
-- password_hash se llena en el flujo de registro (bcrypt/argon2).
CREATE TABLE users (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email         CITEXT      NOT NULL UNIQUE,
    password_hash TEXT        NOT NULL,
    name          TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- households
-- ============================================================
-- Un "hogar" es la unidad multi-tenant: agrupa gastos, miembros,
-- reglas de split, etc. Un user puede pertenecer a varios hogares.
-- base_currency define la moneda en la que se consolidan montos
-- (ej: ARS para un hogar en Argentina — todo se convierte a ARS
-- al momento del gasto usando exchange_rates).
CREATE TABLE households (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name           TEXT        NOT NULL,
    base_currency  CHAR(3)     NOT NULL DEFAULT 'ARS',
    created_by     UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- household_members
-- ============================================================
-- Membresía: qué user pertenece a qué household y con qué rol.
-- role: 'owner'  puede modificar hogar, invitar, borrar
--       'member' puede crear/editar gastos propios y ver compartidos
-- Un user no puede estar duplicado en el mismo hogar (UNIQUE).
CREATE TABLE household_members (
    household_id UUID        NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    user_id      UUID        NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    role         TEXT        NOT NULL CHECK (role IN ('owner', 'member')),
    joined_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (household_id, user_id)
);

-- Índice para ir rápido de user → sus hogares.
-- El PK ya cubre (household_id, user_id) así que queries por user_id
-- solo necesitan este índice inverso.
CREATE INDEX idx_household_members_user ON household_members(user_id);
