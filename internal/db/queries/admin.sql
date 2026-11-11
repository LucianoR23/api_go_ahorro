-- ===================== superadmin panel (plan 1.11) =====================
-- Aggregates and account metadata only (plan §8b): no amounts, no
-- descriptions, nothing from inside a space.

-- name: AdminOverviewCounts :one
SELECT
    (SELECT count(*) FROM users WHERE deleted_at IS NULL)::bigint AS accounts,
    (SELECT count(*) FROM users
     WHERE deleted_at IS NULL AND created_at >= sqlc.arg(week_start)::timestamptz)::bigint AS new_week,
    (SELECT count(*) FROM users
     WHERE deleted_at IS NULL AND created_at >= sqlc.arg(month_start)::timestamptz)::bigint AS new_month,
    (SELECT count(*) FROM users WHERE deleted_at IS NULL AND email_verified_at IS NOT NULL)::bigint AS verified,
    (SELECT count(DISTINCT s.user_id) FROM user_sessions s
     JOIN users u ON u.id = s.user_id AND u.deleted_at IS NULL
     WHERE s.last_used_at >= sqlc.arg(active_since)::timestamptz)::bigint AS active,
    (SELECT count(*) FROM households WHERE kind = 'shared' AND deleted_at IS NULL)::bigint AS households,
    (SELECT count(*) FROM households WHERE kind = 'shared' AND deleted_at IS NOT NULL)::bigint AS deleted_households,
    (SELECT count(*) FROM waitlist_entries WHERE confirmed_at IS NOT NULL)::bigint AS waitlist;

-- name: AdminMovementsByMonth :many
-- Expenses and incomes created per month (by creation, not by spend date),
-- months cut in the given time zone.
WITH months AS (
    SELECT m::date AS month,
           (m::timestamp AT TIME ZONE sqlc.arg(tz)::text) AS starts,
           ((m + interval '1 month')::timestamp AT TIME ZONE sqlc.arg(tz)::text) AS ends
    FROM generate_series(sqlc.arg(from_month)::date, sqlc.arg(to_month)::date, interval '1 month') AS m
)
SELECT months.month::date AS month,
       (SELECT count(*) FROM expenses e
        WHERE e.created_at >= months.starts AND e.created_at < months.ends)::bigint AS expenses,
       (SELECT count(*) FROM incomes i
        WHERE i.created_at >= months.starts AND i.created_at < months.ends)::bigint AS incomes
FROM months
ORDER BY months.month;

-- name: AdminListUsers :many
-- q is lower-cased with LIKE wildcards escaped by the caller.
SELECT u.id, u.email, u.first_name, u.last_name, u.created_at, u.email_verified_at,
       u.onboarded_at, u.is_superadmin, u.deleted_at,
       (SELECT max(s.last_used_at) FROM user_sessions s WHERE s.user_id = u.id)::timestamptz AS last_seen_at,
       (SELECT count(*) FROM household_members hm
        JOIN households h ON h.id = hm.household_id AND h.kind = 'shared' AND h.deleted_at IS NULL
        WHERE hm.user_id = u.id)::bigint AS households
FROM users u
WHERE sqlc.arg(q)::text = ''
   OR lower(u.email) LIKE '%' || sqlc.arg(q)::text || '%'
   OR lower(u.first_name || ' ' || u.last_name) LIKE '%' || sqlc.arg(q)::text || '%'
ORDER BY u.created_at DESC, u.id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: AdminGetUser :one
SELECT u.id, u.email, u.first_name, u.last_name, u.created_at, u.email_verified_at,
       u.onboarded_at, u.is_superadmin, u.deleted_at, u.pending_email,
       (u.password_hash IS NOT NULL)::bool AS has_password,
       (SELECT max(s.last_used_at) FROM user_sessions s WHERE s.user_id = u.id)::timestamptz AS last_seen_at,
       (SELECT count(*) FROM user_sessions s
        WHERE s.user_id = u.id AND s.revoked_at IS NULL AND s.expires_at > now())::bigint AS active_sessions
FROM users u
WHERE u.id = $1;

-- name: AdminUserProviders :many
SELECT provider FROM user_identities WHERE user_id = $1 ORDER BY provider;

-- name: AdminUserHouseholds :many
-- The account's spaces: name, kind, role and size. Nothing from inside them.
SELECT h.id, h.name, h.kind, hm.role, hm.joined_at, h.deleted_at,
       (SELECT count(*) FROM household_members m WHERE m.household_id = h.id)::bigint AS members
FROM household_members hm
JOIN households h ON h.id = hm.household_id
WHERE hm.user_id = $1
ORDER BY h.kind DESC, hm.joined_at;

-- name: AdminListAuditEvents :many
-- Every account's events, newest first. user_id matches the account an
-- event happened to or the one that did it. patterns are LIKE patterns
-- (empty = all). Keyset pagination on (created_at, id).
SELECT a.id, a.user_id, a.actor_id, a.action, a.target, a.ip, a.device, a.metadata, a.created_at,
       u.email AS user_email, act.email AS actor_email
FROM audit_events a
LEFT JOIN users u ON u.id = a.user_id
LEFT JOIN users act ON act.id = a.actor_id
WHERE (sqlc.narg(user_id)::uuid IS NULL OR a.user_id = sqlc.narg(user_id) OR a.actor_id = sqlc.narg(user_id))
  AND (cardinality(sqlc.arg(patterns)::text[]) = 0 OR a.action LIKE ANY (sqlc.arg(patterns)::text[]))
  AND (sqlc.narg(before_at)::timestamptz IS NULL
       OR (a.created_at, a.id) < (sqlc.narg(before_at)::timestamptz, sqlc.narg(before_id)::uuid))
ORDER BY a.created_at DESC, a.id DESC
LIMIT sqlc.arg(lim);
