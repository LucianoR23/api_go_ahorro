CREATE TABLE budget_goals (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id   UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    scope          TEXT NOT NULL DEFAULT 'household' CHECK (scope IN ('household','user')),
    user_id        UUID REFERENCES users(id) ON DELETE CASCADE,
    category_id    UUID REFERENCES categories(id) ON DELETE CASCADE,
    goal_type      TEXT NOT NULL CHECK (goal_type IN ('category_limit','total_limit','savings')),
    target_amount  NUMERIC(12, 2) NOT NULL CHECK (target_amount > 0),
    currency       TEXT NOT NULL DEFAULT 'ARS' CHECK (currency IN ('ARS','USD','EUR')),
    period         TEXT NOT NULL DEFAULT 'monthly' CHECK (period IN ('monthly','weekly')),
    is_active      BOOLEAN NOT NULL DEFAULT true,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((scope = 'household' AND user_id IS NULL) OR (scope = 'user' AND user_id IS NOT NULL))
);

CREATE TABLE daily_insights (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    household_id  UUID NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    user_id       UUID REFERENCES users(id) ON DELETE CASCADE,
    insight_date  DATE NOT NULL,
    insight_type  TEXT NOT NULL,
    title         TEXT NOT NULL,
    body          TEXT NOT NULL,
    severity      TEXT NOT NULL DEFAULT 'info' CHECK (severity IN ('info','warning','critical')),
    is_read       BOOLEAN NOT NULL DEFAULT false,
    metadata      JSONB NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(household_id, user_id, insight_date, insight_type)
);

CREATE INDEX idx_goals_household         ON budget_goals(household_id, is_active) WHERE is_active = true;
CREATE INDEX idx_goals_user_active       ON budget_goals(user_id, is_active)      WHERE is_active = true AND user_id IS NOT NULL;
CREATE INDEX idx_insights_household_date ON daily_insights(household_id, insight_date DESC);
CREATE INDEX idx_insights_user_date      ON daily_insights(user_id, insight_date DESC) WHERE user_id IS NOT NULL;
CREATE INDEX idx_insights_unread         ON daily_insights(household_id, is_read)      WHERE is_read = false;
