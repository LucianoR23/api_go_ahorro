package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// RefreshTokenTTL: a session survives 7 days without use; every refresh
// extends it (sliding window).
const RefreshTokenTTL = 7 * 24 * time.Hour

// reuseGrace: a just-rotated token presented again within this window is a
// race between two tabs refreshing at once, not a theft. It is refused
// without revoking the session.
const reuseGrace = 30 * time.Second

// Session: the result of signing in or refreshing.
type Session struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	RefreshToken string // plain value for the cookie; never stored
	ExpiresAt    time.Time
}

// SessionStore keeps server-side sessions (plan 0.5): sign-out and password
// changes revoke them, and a reused refresh token revokes the session it
// belonged to.
type SessionStore struct {
	q   *sqlcgen.Queries
	now func() time.Time
}

func NewSessionStore(pool *pgxpool.Pool) *SessionStore {
	return &SessionStore{q: sqlcgen.New(pool), now: time.Now}
}

func newRefreshToken() (plain string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("auth: refresh token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(b)
	return plain, hashRefreshToken(plain), nil
}

func hashRefreshToken(plain string) []byte {
	h := sha256.Sum256([]byte(plain))
	return h[:]
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// Create opens a session for a sign-in and clears the user's stale ones.
func (s *SessionStore) Create(ctx context.Context, userID uuid.UUID, userAgent string) (Session, error) {
	plain, hash, err := newRefreshToken()
	if err != nil {
		return Session{}, err
	}
	if len(userAgent) > 300 {
		userAgent = userAgent[:300]
	}
	exp := s.now().Add(RefreshTokenTTL)
	row, err := s.q.CreateUserSession(ctx, sqlcgen.CreateUserSessionParams{
		UserID: userID, RefreshHash: hash, UserAgent: userAgent, ExpiresAt: ts(exp),
	})
	if err != nil {
		return Session{}, fmt.Errorf("auth.SessionStore.Create: %w", err)
	}
	_ = s.q.DeleteStaleUserSessions(ctx, userID) // housekeeping; never blocks a sign-in
	return Session{ID: row.ID, UserID: userID, RefreshToken: plain, ExpiresAt: exp}, nil
}

// Rotate exchanges a refresh token for a new one. Unknown, expired or revoked
// tokens are refused. A token that was already rotated is a reused copy:
// outside the short grace window it revokes the whole session.
func (s *SessionStore) Rotate(ctx context.Context, presented string) (Session, error) {
	hash := hashRefreshToken(presented)
	row, err := s.q.GetUserSessionByRefreshHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, s.handleReuse(ctx, hash)
	}
	if err != nil {
		return Session{}, fmt.Errorf("auth.SessionStore.Rotate: %w", err)
	}
	if row.RevokedAt.Valid || s.now().After(row.ExpiresAt.Time) {
		return Session{}, domain.NewAuthError("sesión vencida")
	}

	plain, newHash, err := newRefreshToken()
	if err != nil {
		return Session{}, err
	}
	exp := s.now().Add(RefreshTokenTTL)
	rotated, err := s.q.RotateUserSession(ctx, sqlcgen.RotateUserSessionParams{
		ID: row.ID, CurrentHash: hash, NewHash: newHash, ExpiresAt: ts(exp),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Another request rotated it a moment ago: same race as the grace case.
		return Session{}, domain.NewAuthError("sesión vencida")
	}
	if err != nil {
		return Session{}, fmt.Errorf("auth.SessionStore.Rotate: %w", err)
	}
	return Session{ID: rotated.ID, UserID: rotated.UserID, RefreshToken: plain, ExpiresAt: exp}, nil
}

func (s *SessionStore) handleReuse(ctx context.Context, hash []byte) error {
	row, err := s.q.GetUserSessionByPrevRefreshHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NewAuthError("sesión vencida")
	}
	if err != nil {
		return fmt.Errorf("auth.SessionStore.Rotate: %w", err)
	}
	if row.RotatedAt.Valid && s.now().Sub(row.RotatedAt.Time) <= reuseGrace {
		return domain.NewAuthError("sesión vencida")
	}
	if err := s.q.RevokeUserSession(ctx, row.ID); err != nil {
		return fmt.Errorf("auth.SessionStore.Rotate: revoke reused: %w", err)
	}
	return domain.NewAuthError("sesión vencida")
}

// RevokeByToken ends the session that owns the refresh token (sign-out).
// Unknown tokens are ignored: signing out always succeeds.
func (s *SessionStore) RevokeByToken(ctx context.Context, presented string) error {
	row, err := s.q.GetUserSessionByRefreshHash(ctx, hashRefreshToken(presented))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("auth.SessionStore.RevokeByToken: %w", err)
	}
	return s.q.RevokeUserSession(ctx, row.ID)
}

// RevokeAll ends every session of the user (password reset, account deletion,
// "cerrar sesión en todos los dispositivos").
func (s *SessionStore) RevokeAll(ctx context.Context, userID uuid.UUID) error {
	if err := s.q.RevokeAllUserSessions(ctx, userID); err != nil {
		return fmt.Errorf("auth.SessionStore.RevokeAll: %w", err)
	}
	return nil
}

// RevokeOthers ends every session of the user except keep (password change
// from the current device).
func (s *SessionStore) RevokeOthers(ctx context.Context, userID, keep uuid.UUID) error {
	if err := s.q.RevokeOtherUserSessions(ctx, sqlcgen.RevokeOtherUserSessionsParams{UserID: userID, KeepID: keep}); err != nil {
		return fmt.Errorf("auth.SessionStore.RevokeOthers: %w", err)
	}
	return nil
}
