-- Plan 0.5: server-side sessions. One row per signed-in device. The refresh
-- token is an opaque random value; only its SHA-256 is stored. Each refresh
-- rotates it and keeps the previous hash, so presenting an old token again
-- (a stolen copy) is detected and revokes the session.
-- No IP column on purpose: the privacy policy keeps IPs 7 days at most, and a
-- session can outlive that.
CREATE TABLE user_sessions (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_hash      BYTEA NOT NULL,
    prev_refresh_hash BYTEA,
    user_agent        TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    rotated_at        TIMESTAMPTZ,
    expires_at        TIMESTAMPTZ NOT NULL,
    revoked_at        TIMESTAMPTZ
);

CREATE UNIQUE INDEX user_sessions_refresh_hash ON user_sessions (refresh_hash);
CREATE INDEX user_sessions_prev_refresh_hash ON user_sessions (prev_refresh_hash) WHERE prev_refresh_hash IS NOT NULL;
CREATE INDEX user_sessions_user_active ON user_sessions (user_id) WHERE revoked_at IS NULL;
