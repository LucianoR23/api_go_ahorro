-- ===================== AI usage (plan 8.1) =====================
-- Blocked statuses (disabled, rate_limited, user_budget, global_budget)
-- never reached a provider: they count for the panel, not for limits.

-- name: InsertAIUsage :exec
INSERT INTO ai_usage (user_id, household_id, task, channel, provider, model,
                      prompt_version, status, input_tokens, output_tokens,
                      audio_seconds, cost_micro_usd, latency_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: AIUserUsageSince :one
-- What one person spent since a moment: provider calls in the last minute
-- and since the start of the day, and the day's cost.
SELECT COUNT(*) FILTER (WHERE created_at >= sqlc.arg(minute_start)::timestamptz)::int AS calls_minute,
       COUNT(*)::int AS calls_day,
       COALESCE(SUM(cost_micro_usd), 0)::bigint AS cost_day
FROM ai_usage
WHERE user_id = sqlc.arg(user_id)
  AND created_at >= sqlc.arg(day_start)::timestamptz
  AND status IN ('ok', 'invalid_output', 'provider_error', 'timeout');

-- name: AICostSince :one
SELECT COALESCE(SUM(cost_micro_usd), 0)::bigint
FROM ai_usage
WHERE created_at >= sqlc.arg(since)::timestamptz;

-- name: AIAdminTotals :one
SELECT COALESCE(SUM(cost_micro_usd) FILTER (WHERE created_at >= sqlc.arg(day_start)::timestamptz), 0)::bigint AS cost_day,
       COALESCE(SUM(cost_micro_usd), 0)::bigint AS cost_month,
       COUNT(*) FILTER (WHERE created_at >= sqlc.arg(day_start)::timestamptz
                          AND status IN ('ok', 'invalid_output', 'provider_error', 'timeout'))::int AS calls_day,
       COUNT(*) FILTER (WHERE status IN ('ok', 'invalid_output', 'provider_error', 'timeout'))::int AS calls_month,
       COUNT(*) FILTER (WHERE created_at >= sqlc.arg(day_start)::timestamptz
                          AND status IN ('invalid_output', 'provider_error', 'timeout'))::int AS errors_day,
       COUNT(*) FILTER (WHERE created_at >= sqlc.arg(day_start)::timestamptz
                          AND status IN ('disabled', 'rate_limited', 'user_budget', 'global_budget'))::int AS blocked_day,
       COALESCE(AVG(latency_ms) FILTER (WHERE created_at >= sqlc.arg(day_start)::timestamptz
                                         AND status = 'ok'), 0)::int AS avg_latency_day
FROM ai_usage
WHERE created_at >= sqlc.arg(month_start)::timestamptz;

-- name: AIAdminByTask :many
SELECT task,
       COUNT(*) FILTER (WHERE status IN ('ok', 'invalid_output', 'provider_error', 'timeout'))::int AS calls,
       COUNT(*) FILTER (WHERE status IN ('invalid_output', 'provider_error', 'timeout'))::int AS errors,
       COUNT(*) FILTER (WHERE status IN ('disabled', 'rate_limited', 'user_budget', 'global_budget'))::int AS blocked,
       COALESCE(SUM(cost_micro_usd), 0)::bigint AS cost
FROM ai_usage
WHERE created_at >= sqlc.arg(month_start)::timestamptz
GROUP BY task
ORDER BY cost DESC, task;

-- name: AIAdminTopUsers :many
SELECT a.user_id, u.email,
       COUNT(*) FILTER (WHERE a.status IN ('ok', 'invalid_output', 'provider_error', 'timeout'))::int AS calls,
       COUNT(*) FILTER (WHERE a.status IN ('disabled', 'rate_limited', 'user_budget', 'global_budget'))::int AS blocked,
       COALESCE(SUM(a.cost_micro_usd), 0)::bigint AS cost
FROM ai_usage a
JOIN users u ON u.id = a.user_id
WHERE a.created_at >= sqlc.arg(month_start)::timestamptz
GROUP BY a.user_id, u.email
ORDER BY cost DESC, calls DESC
LIMIT 20;

-- name: ListSuperadminEmails :many
SELECT email FROM users
WHERE is_superadmin AND deleted_at IS NULL
ORDER BY email;
