-- password_hash nullable: users que se registran solo con Google no tienen
-- contraseña local. El login con email/password rechaza el caso (hash NULL
-- nunca matchea) sin filtrar que la cuenta existe — mismo error genérico
-- que email inexistente.
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;

-- Identidades externas (OAuth). Diseño extensible: hoy 'google', mañana
-- 'apple' / 'github' / etc. (provider, subject) es la PK natural: un
-- mismo Google account no puede vincularse dos veces. user_id es FK con
-- ON DELETE CASCADE — si el user se borra físicamente, sus identidades
-- vuelan con él (el soft-delete actual NO toca esta tabla; eso es deliberado:
-- si el user resucita, su vínculo con Google sigue ahí).
CREATE TABLE user_identities (
    provider     TEXT        NOT NULL CHECK (provider IN ('google')),
    subject      TEXT        NOT NULL,
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email        CITEXT      NOT NULL,
    linked_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject)
);

CREATE INDEX idx_user_identities_user ON user_identities(user_id);
