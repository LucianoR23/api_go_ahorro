-- ============================================================
-- password_resets
-- ============================================================
-- Tokens de reseteo de contraseña. Mismo patrón que household_invites:
-- el token plano se manda por email, acá solo guardamos el SHA-256.
--
-- Ciclo:
--   pendiente  → used_at IS NULL, expires_at > now()
--   usada      → used_at IS NOT NULL  (single-use)
--   expirada   → expires_at <= now() (implícito)
--
-- TTL recomendado: 1h (balance entre UX y ventana de ataque si la mailbox
-- se filtra). Se define en el service, no en DB.
CREATE TABLE password_resets (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT        NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Para invalidar tokens activos del user cuando se emite uno nuevo
-- (así el último mail es el único válido).
CREATE INDEX idx_password_resets_user_active
    ON password_resets(user_id)
    WHERE used_at IS NULL;
