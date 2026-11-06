-- Plan 0.17: security audit log. One row per security-relevant event of an
-- account (sign-ins, failed sign-ins, password and email changes, sessions,
-- household membership) and every superadmin action.
-- Retention (decided 2026-11-06, privacy policy): the IP is cleared after 7
-- days and events are deleted after 12 months, both by a daily job; a
-- deleted account's events are deleted with it. No amounts in metadata.
CREATE TABLE audit_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID REFERENCES users(id) ON DELETE CASCADE,
    actor_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    action     TEXT NOT NULL,
    target     TEXT NOT NULL DEFAULT '',
    ip         TEXT,
    user_agent TEXT NOT NULL DEFAULT '',
    device     TEXT NOT NULL DEFAULT '',
    metadata   JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_events_user_created ON audit_events (user_id, created_at DESC);
CREATE INDEX audit_events_created ON audit_events (created_at);
CREATE INDEX audit_events_ip_created ON audit_events (created_at) WHERE ip IS NOT NULL;
