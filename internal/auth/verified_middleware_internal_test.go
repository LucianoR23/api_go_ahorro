package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

type fakeUsers map[uuid.UUID]domain.User

func (f fakeUsers) GetByID(_ context.Context, id uuid.UUID) (domain.User, error) {
	u, ok := f[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

func TestRequireVerifiedEmail(t *testing.T) {
	verified, unverified := uuid.New(), uuid.New()
	now := time.Now()
	m := NewMiddleware(nil, slog.New(slog.DiscardHandler))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })

	serve := func(ctx context.Context) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		m.RequireVerifiedEmail(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx))
		return rec
	}

	// Not wired: fail closed.
	if rec := serve(ContextWithUserID(context.Background(), verified)); rec.Code != http.StatusForbidden {
		t.Errorf("without a lookup = %d, want 403", rec.Code)
	}

	m.SetUserLookup(fakeUsers{
		verified:   {ID: verified, EmailVerifiedAt: &now},
		unverified: {ID: unverified},
	})
	if rec := serve(ContextWithUserID(context.Background(), verified)); rec.Code != http.StatusTeapot {
		t.Errorf("verified user = %d, want to reach the handler", rec.Code)
	}
	rec := serve(ContextWithUserID(context.Background(), unverified))
	var body struct{ Code, Message string }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusForbidden || body.Code != "email_unverified" {
		t.Errorf("unverified user = %d %+v", rec.Code, body)
	}
	if rec := serve(context.Background()); rec.Code != http.StatusUnauthorized {
		t.Errorf("no user in context = %d, want 401", rec.Code)
	}
}
