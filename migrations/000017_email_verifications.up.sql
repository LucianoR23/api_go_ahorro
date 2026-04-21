-- ============================================================
-- email_verifications + users.email_verified_at
-- ============================================================
-- Flujo "verificá tu email" para registros SIN invite (si venís por invite,
-- el email ya fue validado por delivery del mail de invitación y el flujo
-- de register lo marca verificado directamente).
--
-- Mismo patrón que password_resets: token plano por email, hash en DB,
-- single-use, TTL.

ALTER TABLE users
    ADD COLUMN email_verified_at TIMESTAMPTZ;

CREATE TABLE email_verifications (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT        NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_email_verifications_user_active
    ON email_verifications(user_id)
    WHERE used_at IS NULL;
