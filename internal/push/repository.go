// Package push implementa Web Push (VAPID) para notificar a usuarios.
// Persiste subscriptions en DB y envía con SherClockHolmes/webpush-go.
package push

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Subscription es el registro persistido en DB. Matchea uno-a-uno con lo
// que el PushManager del browser devuelve al suscribirse.
type Subscription struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Endpoint   string
	P256dh     string
	Auth       string
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Upsert guarda la subscription. Si ya existe el mismo endpoint (mismo
// browser re-suscribiéndose), actualiza las keys y last_seen_at.
func (r *Repository) Upsert(ctx context.Context, userID uuid.UUID, endpoint, p256dh, auth, userAgent string) (Subscription, error) {
	const q = `
INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, user_agent)
VALUES ($1, $2, $3, $4, NULLIF($5, ''))
ON CONFLICT (endpoint) DO UPDATE SET
    user_id      = EXCLUDED.user_id,
    p256dh       = EXCLUDED.p256dh,
    auth         = EXCLUDED.auth,
    user_agent   = COALESCE(EXCLUDED.user_agent, push_subscriptions.user_agent),
    last_seen_at = NOW()
RETURNING id, user_id, endpoint, p256dh, auth, COALESCE(user_agent, ''), created_at, last_seen_at`

	var s Subscription
	err := r.pool.QueryRow(ctx, q, userID, endpoint, p256dh, auth, userAgent).Scan(
		&s.ID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth, &s.UserAgent, &s.CreatedAt, &s.LastSeenAt,
	)
	if err != nil {
		return Subscription{}, fmt.Errorf("push.Upsert: %w", err)
	}
	return s, nil
}

// ListByUser devuelve todas las subs activas del user. Si no hay, slice vacío.
func (r *Repository) ListByUser(ctx context.Context, userID uuid.UUID) ([]Subscription, error) {
	const q = `
SELECT id, user_id, endpoint, p256dh, auth, COALESCE(user_agent, ''), created_at, last_seen_at
FROM push_subscriptions
WHERE user_id = $1
ORDER BY last_seen_at DESC`

	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("push.ListByUser: %w", err)
	}
	defer rows.Close()

	out := make([]Subscription, 0)
	for rows.Next() {
		var s Subscription
		if err := rows.Scan(&s.ID, &s.UserID, &s.Endpoint, &s.P256dh, &s.Auth, &s.UserAgent, &s.CreatedAt, &s.LastSeenAt); err != nil {
			return nil, fmt.Errorf("push.ListByUser scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteByEndpoint borra una subscription por su endpoint único. Se usa en
// logout/unsubscribe. Valida ownership para evitar que un user borre subs
// ajenas. Idempotente: si no existe, devuelve nil (no es un error).
func (r *Repository) DeleteByEndpoint(ctx context.Context, userID uuid.UUID, endpoint string) error {
	const q = `DELETE FROM push_subscriptions WHERE user_id = $1 AND endpoint = $2`
	_, err := r.pool.Exec(ctx, q, userID, endpoint)
	if err != nil {
		return fmt.Errorf("push.DeleteByEndpoint: %w", err)
	}
	return nil
}

// DeleteByEndpointRaw borra por endpoint sin validar user — lo usa el service
// cuando el push provider responde 404/410 (la sub expiró del lado del browser).
func (r *Repository) DeleteByEndpointRaw(ctx context.Context, endpoint string) error {
	const q = `DELETE FROM push_subscriptions WHERE endpoint = $1`
	_, err := r.pool.Exec(ctx, q, endpoint)
	if err != nil {
		return fmt.Errorf("push.DeleteByEndpointRaw: %w", err)
	}
	return nil
}

// Touch actualiza last_seen_at cuando el cliente vuelve a hacer ping. No
// falla si no existe (caso raro; el cliente debería re-suscribir).
func (r *Repository) Touch(ctx context.Context, endpoint string) error {
	const q = `UPDATE push_subscriptions SET last_seen_at = NOW() WHERE endpoint = $1`
	_, err := r.pool.Exec(ctx, q, endpoint)
	if err != nil {
		return fmt.Errorf("push.Touch: %w", err)
	}
	return nil
}
