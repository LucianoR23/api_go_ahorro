-- ===================== audit_events (plan 0.17) =====================

-- name: CreateAuditEvent :exec
INSERT INTO audit_events (user_id, actor_id, action, target, ip, user_agent, device, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListAuditEventsForUser :many
SELECT * FROM audit_events
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: AuditDeviceSeenRecently :one
-- Whether the user signed in from this device label in the window (new-device
-- alert), and whether they ever signed in at all (no alert on the first one).
SELECT
    EXISTS (SELECT 1 FROM audit_events a
            WHERE a.user_id = $1 AND a.action = 'login.success' AND a.device = $2
              AND a.created_at > now() - make_interval(days => sqlc.arg(days)::int))::bool AS seen,
    EXISTS (SELECT 1 FROM audit_events b
            WHERE b.user_id = $1 AND b.action = 'login.success')::bool AS any_login;

-- name: ClearOldAuditIPs :execrows
UPDATE audit_events SET ip = NULL
WHERE ip IS NOT NULL AND created_at < now() - interval '7 days';

-- name: DeleteOldAuditEvents :execrows
DELETE FROM audit_events WHERE created_at < now() - interval '12 months';

-- name: DeleteAuditEventsForUser :exec
DELETE FROM audit_events WHERE user_id = $1;
