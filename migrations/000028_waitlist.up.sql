-- Plan B.5: landing waitlist (contract: valuo_web/docs/waitlist-contract.md).
-- Data minimization (plan 8b, 13.9): no name, no IP, no user agent at rest.
-- notice_version and the timestamps are the consent record.
CREATE TABLE waitlist_entries (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email              TEXT NOT NULL UNIQUE,
    monotributo        BOOLEAN NOT NULL DEFAULT false,
    form               TEXT NOT NULL CHECK (form IN ('hero', 'cta')),
    source             TEXT CHECK (char_length(source) <= 64),
    medium             TEXT CHECK (char_length(medium) <= 64),
    campaign           TEXT CHECK (char_length(campaign) <= 64),
    referrer_path      TEXT CHECK (char_length(referrer_path) <= 200),
    notice_version     TEXT NOT NULL CHECK (char_length(notice_version) <= 32),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at       TIMESTAMPTZ,
    confirm_token_hash BYTEA UNIQUE,
    confirm_expires_at TIMESTAMPTZ,
    last_sent_at       TIMESTAMPTZ
);

-- The daily cleanup of unconfirmed entries older than 30 days.
CREATE INDEX waitlist_unconfirmed_created ON waitlist_entries (created_at) WHERE confirmed_at IS NULL;
