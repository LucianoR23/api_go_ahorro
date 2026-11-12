-- 000047_monthly_review_kind (plan 8.2): the monthly summary is now also an
-- in-app notice (insight_type 'monthly_review'), muted with the same
-- preference as the monthly email ('monthly_report.in_app').
CREATE OR REPLACE FUNCTION insight_kind(p_type TEXT, p_metadata JSONB) RETURNS TEXT
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN p_type = 'shared_expense' THEN 'shared_expense'
        WHEN p_type = 'settlement' THEN 'settlement'
        WHEN p_type IN ('card_closing_soon', 'card_due_soon', 'credit_period_reminder') THEN 'cards'
        WHEN p_type = 'alert' AND p_metadata ->> 'kind' = 'installment_due_soon' THEN 'installments'
        WHEN p_type = 'alert' THEN 'goals'
        WHEN p_type = 'recurring_spike' THEN 'recurring'
        WHEN p_type IN ('weekly_review', 'daily_summary') THEN 'weekly_review'
        WHEN p_type = 'monthly_review' THEN 'monthly_report'
        ELSE NULL
    END
$$;
