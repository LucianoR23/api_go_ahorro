package auth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

// SessionStore against a real database with a controlled clock (plan 0.5).
func TestSessionStore(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, first_name, last_name)
		VALUES ('s@example.test', 'x', 'S', 'T') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	s := NewSessionStore(pool)
	s.now = func() time.Time { return now }

	t.Run("rotation hands out a new token and the old one stops working", func(t *testing.T) {
		first, err := s.Create(ctx, userID, "Firefox")
		if err != nil {
			t.Fatal(err)
		}
		second, err := s.Rotate(ctx, first.RefreshToken)
		if err != nil || second.ID != first.ID || second.RefreshToken == first.RefreshToken {
			t.Fatalf("rotate = %+v, %v", second, err)
		}
		if _, err := s.Rotate(ctx, "made-up"); err == nil {
			t.Error("an unknown token rotated")
		}
	})

	t.Run("a reused token within the grace window is refused but the session survives", func(t *testing.T) {
		a, _ := s.Create(ctx, userID, "")
		b, _ := s.Rotate(ctx, a.RefreshToken)
		now = now.Add(5 * time.Second)
		if _, err := s.Rotate(ctx, a.RefreshToken); err == nil {
			t.Error("the old token rotated again")
		}
		if _, err := s.Rotate(ctx, b.RefreshToken); err != nil {
			t.Errorf("the current token stopped working after a racing tab: %v", err)
		}
	})

	t.Run("a reused token after the grace window revokes the session", func(t *testing.T) {
		a, _ := s.Create(ctx, userID, "")
		b, _ := s.Rotate(ctx, a.RefreshToken)
		now = now.Add(reuseGrace + time.Second)
		if _, err := s.Rotate(ctx, a.RefreshToken); err == nil {
			t.Error("a stolen old token rotated")
		}
		if _, err := s.Rotate(ctx, b.RefreshToken); err == nil {
			t.Error("the session survived the reuse of an old token")
		}
	})

	t.Run("expired sessions are refused; revoke one, all, or all but one", func(t *testing.T) {
		old, _ := s.Create(ctx, userID, "")
		now = now.Add(RefreshTokenTTL + time.Minute)
		if _, err := s.Rotate(ctx, old.RefreshToken); err == nil {
			t.Error("an expired session rotated")
		}

		keep, _ := s.Create(ctx, userID, "")
		other, _ := s.Create(ctx, userID, "")
		if err := s.RevokeOthers(ctx, userID, keep.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Rotate(ctx, other.RefreshToken); err == nil {
			t.Error("RevokeOthers kept another session")
		}
		kept, err := s.Rotate(ctx, keep.RefreshToken)
		if err != nil {
			t.Fatalf("RevokeOthers revoked the kept session: %v", err)
		}
		if _, err := s.RevokeByToken(ctx, kept.RefreshToken); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Rotate(ctx, kept.RefreshToken); err == nil {
			t.Error("RevokeByToken left the session alive")
		}
		if _, err := s.RevokeByToken(ctx, "unknown"); err != nil {
			t.Errorf("revoking an unknown token = %v, want nil", err)
		}

		x, _ := s.Create(ctx, userID, "")
		if err := s.RevokeAll(ctx, userID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Rotate(ctx, x.RefreshToken); err == nil {
			t.Error("RevokeAll left a session alive")
		}
	})
}
