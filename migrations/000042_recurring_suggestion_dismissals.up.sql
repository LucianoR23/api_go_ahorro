-- 000042_recurring_suggestion_dismissals (plan 7.3): expenses that repeat
-- every month are suggested as recurring expenses. When someone in the space
-- says "not recurring", the suggestion's key (normalized description and
-- currency) is stored here so it stops showing for every member and device.
CREATE TABLE recurring_suggestion_dismissals (
    household_id UUID        NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    key          TEXT        NOT NULL CHECK (char_length(key) BETWEEN 1 AND 300),
    dismissed_by UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (household_id, key)
);

CREATE INDEX idx_recurring_suggestion_dismissals_dismissed_by
    ON recurring_suggestion_dismissals(dismissed_by);
