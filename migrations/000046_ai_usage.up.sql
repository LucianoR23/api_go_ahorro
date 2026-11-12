-- 000046_ai_usage (plan 8.1): one row per call to an AI provider, plus one
-- per request the guardrails stopped before reaching it. It holds metrics
-- only, never the text sent or received (decided with the user on
-- 2026-11-11). Limits, budgets and the admin panel read from here.
--
-- cost_micro_usd is the call's cost in millionths of a dollar, computed from
-- the configured prices. user_id and household_id survive the account purge
-- as NULL so the spend history stays whole.
CREATE TABLE ai_usage (
    id             BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id        UUID        REFERENCES users(id) ON DELETE SET NULL,
    household_id   UUID        REFERENCES households(id) ON DELETE SET NULL,
    task           TEXT        NOT NULL,
    channel        TEXT        NOT NULL,
    provider       TEXT        NOT NULL DEFAULT '',
    model          TEXT        NOT NULL DEFAULT '',
    prompt_version TEXT        NOT NULL DEFAULT '',
    status         TEXT        NOT NULL,
    input_tokens   INTEGER     NOT NULL DEFAULT 0,
    output_tokens  INTEGER     NOT NULL DEFAULT 0,
    audio_seconds  INTEGER     NOT NULL DEFAULT 0,
    cost_micro_usd BIGINT      NOT NULL DEFAULT 0,
    latency_ms     INTEGER     NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (task ~ '^[a-z_]{1,40}$'),
    CHECK (channel IN ('app', 'telegram', 'whatsapp', 'email', 'system')),
    CHECK (status IN ('ok', 'invalid_output', 'provider_error', 'timeout',
                      'disabled', 'rate_limited', 'user_budget', 'global_budget')),
    CHECK (input_tokens >= 0 AND output_tokens >= 0 AND audio_seconds >= 0
           AND cost_micro_usd >= 0 AND latency_ms >= 0)
);

CREATE INDEX idx_ai_usage_user_created ON ai_usage (user_id, created_at);
CREATE INDEX idx_ai_usage_household ON ai_usage (household_id);
CREATE INDEX idx_ai_usage_created ON ai_usage (created_at);
