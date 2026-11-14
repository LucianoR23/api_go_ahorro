-- 000048_inbound_email (plan 3.5): import expenses from forwarded emails.
--
-- Each user gets one address u_<random>@<INBOUND_DOMAIN>. The local part is
-- stored encrypted (AES-256-GCM, plan §8b) to show it, and as an HMAC to
-- find the user when a mail arrives. Senders the user accepted are kept as
-- HMACs only; the account's own email is always accepted.
--
-- inbound_messages holds what was read from each mail, never the raw mail.
-- An expense mail stays 'pending' until the user confirms it in Cargar gasto
-- (the payment method is often not in the mail, so it is not an expense row
-- yet; decided with the user on 2026-11-14) or dismisses it. A mail forwarded
-- from an address not yet accepted is 'sender_pending' and keeps that address
-- encrypted to ask "¿es tuyo?". Gmail's forwarding confirmation keeps only its
-- code. Retention: codes 24 h, resolved rows 30 days (jobs).
CREATE TABLE inbound_addresses (
    user_id       UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    local_hash    BYTEA       NOT NULL UNIQUE,
    local_enc     BYTEA       NOT NULL,
    key_id        SMALLINT    NOT NULL,
    sender_hashes BYTEA[]     NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (cardinality(sender_hashes) <= 10)
);

CREATE TABLE inbound_messages (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    dedupe_hash BYTEA       NOT NULL,
    kind        TEXT        NOT NULL CHECK (kind IN ('expense', 'gmail_confirmation', 'other')),
    status      TEXT        NOT NULL CHECK (status IN
                    ('pending', 'sender_pending', 'confirmed', 'dismissed', 'unreadable')),
    source      TEXT        CHECK (source ~ '^[a-z_]{1,30}$'),
    parsed      JSONB,
    sender_enc  BYTEA,
    gmail_code  TEXT        CHECK (gmail_code ~ '^[0-9]{1,20}$'),
    expense_id  UUID        REFERENCES expenses(id) ON DELETE SET NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ,
    UNIQUE (user_id, dedupe_hash),
    CHECK (status <> 'sender_pending' OR sender_enc IS NOT NULL)
);

CREATE INDEX idx_inbound_messages_open
    ON inbound_messages (user_id, received_at DESC)
    WHERE kind = 'expense' AND status IN ('pending', 'sender_pending');

-- Deleting an expense sets expense_id to NULL here.
CREATE INDEX idx_inbound_messages_expense ON inbound_messages (expense_id);
