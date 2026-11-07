package push_test

// Characterization tests of the push subscription endpoints (item 0.21c).
// apitest runs without VAPID keys, so no notification is ever delivered.

import (
	"net/http"
	"os"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

type subJSON struct {
	ID         string `json:"id"`
	Endpoint   string `json:"endpoint"`
	UserAgent  string `json:"userAgent"`
	CreatedAt  string `json:"createdAt"`
	LastSeenAt string `json:"lastSeenAt"`
}

func subBody(endpoint string) map[string]any {
	return map[string]any{"endpoint": endpoint, "keys": map[string]string{"p256dh": "BPk-test-key", "auth": "auth-secret"}}
}

func subscribe(t testing.TB, s *apitest.Server, u apitest.User, endpoint string) subJSON {
	t.Helper()
	var sub subJSON
	s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/push/subscriptions", Token: u.Token, Body: subBody(endpoint),
		Header: http.Header{"User-Agent": {"apitest-browser/1.0"}}}).Status(t, http.StatusCreated).JSON(t, &sub)
	return sub
}

func list(t testing.TB, s *apitest.Server, u apitest.User) []subJSON {
	t.Helper()
	var subs []subJSON
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/push/subscriptions", Token: u.Token}).Status(t, http.StatusOK).JSON(t, &subs)
	return subs
}

func endpoint() string {
	return "https://fcm.googleapis.com/fcm/send/apitest-" + apitest.UniqueEmail()
}

func TestPushSubscriptions(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("VAPID key endpoint is public and reports disabled", func(t *testing.T) {
		var k struct {
			PublicKey string `json:"publicKey"`
			Enabled   bool   `json:"enabled"`
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/push/vapid-public-key"}).Status(t, http.StatusOK).JSON(t, &k)
		if k.Enabled || k.PublicKey != "" {
			t.Errorf("vapid = %+v, want disabled in tests", k)
		}
	})

	t.Run("subscribe, list, re-subscribe is an upsert", func(t *testing.T) {
		u := s.Register(t)
		ep := endpoint()
		first := subscribe(t, s, u, ep)
		if first.ID == "" || first.Endpoint != ep {
			t.Fatalf("subscribe = %+v", first)
		}
		again := subscribe(t, s, u, ep)
		if again.ID != first.ID {
			t.Errorf("re-subscribe created a new row: %s vs %s", again.ID, first.ID)
		}
		subs := list(t, s, u)
		if len(subs) != 1 || subs[0].UserAgent != "apitest-browser/1.0" || subs[0].CreatedAt == "" {
			t.Errorf("list = %+v", subs)
		}
	})

	t.Run("unsubscribe by endpoint and delete by id", func(t *testing.T) {
		u := s.Register(t)
		a, b := subscribe(t, s, u, endpoint()), subscribe(t, s, u, endpoint())
		s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/push/subscriptions", Token: u.Token,
			Body: map[string]string{"endpoint": a.Endpoint}}).Status(t, http.StatusNoContent)
		s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/push/subscriptions/" + b.ID, Token: u.Token}).Status(t, http.StatusNoContent)
		if subs := list(t, s, u); len(subs) != 0 {
			t.Errorf("left = %+v", subs)
		}
		s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/push/subscriptions/" + b.ID, Token: u.Token}).
			WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("users cannot see or delete each other's devices", func(t *testing.T) {
		a, b := s.Register(t), s.Register(t)
		sub := subscribe(t, s, a, endpoint())
		if subs := list(t, s, b); len(subs) != 0 {
			t.Errorf("b sees %+v", subs)
		}
		s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/push/subscriptions/" + sub.ID, Token: b.Token}).
			WantError(t, http.StatusNotFound, "not_found", "")
		// Unsubscribe by endpoint answers 204 but only touches the caller's rows.
		s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/push/subscriptions", Token: b.Token,
			Body: map[string]string{"endpoint": sub.Endpoint}}).Status(t, http.StatusNoContent)
		if subs := list(t, s, a); len(subs) != 1 {
			t.Errorf("a's device deleted by b: %+v", subs)
		}
	})

	t.Run("another user's endpoint is never taken over", func(t *testing.T) {
		a, b := s.Register(t), s.Register(t)
		ep := endpoint()
		subscribe(t, s, a, ep)
		res := s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/push/subscriptions", Token: b.Token, Body: subBody(ep)})
		var still bool
		for _, sub := range list(t, s, a) {
			still = still || sub.Endpoint == ep
		}
		if !still || res.Code == http.StatusCreated {
			t.Errorf("b took over a's endpoint (status %d, a still has it: %v)", res.Code, still)
		}
	})

	t.Run("only known push services are accepted as endpoint", func(t *testing.T) {
		u := s.Register(t)
		res := s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/push/subscriptions", Token: u.Token,
			Body: subBody("http://169.254.169.254/latest/meta-data")})
		res.Status(t, http.StatusUnprocessableEntity)
	})

	t.Run("validation", func(t *testing.T) {
		u := s.Register(t)
		for field, body := range map[string]map[string]any{
			"endpoint":    {"endpoint": " ", "keys": map[string]string{"p256dh": "k", "auth": "a"}},
			"keys.p256dh": {"endpoint": endpoint(), "keys": map[string]string{"auth": "a"}},
			"keys.auth":   {"endpoint": endpoint(), "keys": map[string]string{"p256dh": "k"}},
		} {
			s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/push/subscriptions", Token: u.Token, Body: body}).
				WantError(t, http.StatusUnprocessableEntity, "validation", field)
		}
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/push/subscriptions", Token: u.Token, Body: "{"}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "body")
		s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/push/subscriptions", Token: u.Token, Body: map[string]string{}}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "endpoint")
		s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/push/subscriptions/nope", Token: u.Token}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "id")
	})

	t.Run("requires auth", func(t *testing.T) {
		for _, r := range []apitest.Req{
			{Method: http.MethodGet, Path: "/push/subscriptions"},
			{Method: http.MethodPost, Path: "/push/subscriptions", Body: subBody(endpoint())},
			{Method: http.MethodDelete, Path: "/push/subscriptions", Body: map[string]string{"endpoint": "x"}},
			{Method: http.MethodDelete, Path: "/push/subscriptions/00000000-0000-0000-0000-000000000001"},
			{Method: http.MethodPost, Path: "/admin/push/test"},
		} {
			s.Do(t, r).WantError(t, http.StatusUnauthorized, "unauthorized", "")
		}
	})

	t.Run("account deletion removes the user's devices", func(t *testing.T) {
		u := s.Register(t)
		subscribe(t, s, u, endpoint())
		s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/me", Token: u.Token}).Status(t, http.StatusNoContent)
		var n int
		if err := s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM push_subscriptions WHERE user_id = $1`, u.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%d subscriptions left after account deletion", n)
		}
	})

	t.Run("admin test push: superadmin only, reports VAPID off", func(t *testing.T) {
		u := s.Register(t)
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/admin/push/test", Token: u.Token}).WantError(t, http.StatusForbidden, "forbidden", "")

		s.MakeSuperadmin(t, u)
		subscribe(t, s, u, endpoint())
		var r struct {
			Sent          bool   `json:"sent"`
			Subscriptions int    `json:"subscriptions"`
			Reason        string `json:"reason"`
		}
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/admin/push/test", Token: u.Token}).Status(t, http.StatusOK).JSON(t, &r)
		if r.Sent || r.Subscriptions != 1 || r.Reason == "" {
			t.Errorf("test push = %+v", r)
		}
	})
}
