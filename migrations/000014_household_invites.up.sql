-- ============================================================
-- household_invites
-- ============================================================
-- Invitaciones a un hogar por email. El owner crea una invitación,
-- se envía un mail con un link que lleva un token random (32 bytes).
-- Guardamos solo el HASH del token (SHA-256): si alguien lee la DB,
-- no puede usar los tokens pendientes.
--
-- Ciclos:
--   pendiente  → accepted_at IS NULL, revoked_at IS NULL, expires_at > now()
--   aceptada   → accepted_at IS NOT NULL  (single-use)
--   revocada   → revoked_at IS NOT NULL
--   expirada   → expires_at <= now() (implícito)
--
-- Un mismo (household, email) puede tener varias filas históricas; solo
-- una puede estar activa a la vez (parcial unique debajo).
CREATE TABLE household_invites (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id  UUID        NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    email         CITEXT      NOT NULL,
    token_hash    TEXT        NOT NULL UNIQUE,
    invited_by    UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    expires_at    TIMESTAMPTZ NOT NULL,
    accepted_at   TIMESTAMPTZ,
    accepted_by   UUID        REFERENCES users(id) ON DELETE SET NULL,
    revoked_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Evita duplicados activos: un email solo puede tener una invitación
-- pendiente por hogar al mismo tiempo. Si el owner reinvita, primero
-- debe revocar la anterior (o esperar a que expire).
CREATE UNIQUE INDEX idx_household_invites_active
    ON household_invites(household_id, email)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;

-- Para listar pendientes del hogar rápido.
CREATE INDEX idx_household_invites_household
    ON household_invites(household_id)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;
