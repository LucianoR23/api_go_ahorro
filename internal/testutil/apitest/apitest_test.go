package apitest_test

// Smoke tests of the harness itself: the real router answers, factories work
// end to end, every test gets its own database, and outbound calls are blocked.

import (
	"net/http"
	"os"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

func TestHealth(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/health/live"}).Status(t, http.StatusOK)
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/health/ready"}).Status(t, http.StatusOK)
}

func TestRegisterVerifyAndCreateHousehold(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	u := s.Register(t, apitest.RegisterOpts{Verify: true})
	if u.ID == "" || u.Token == "" {
		t.Fatalf("register returned %+v", u)
	}
	mail := s.Mail.LastTo(t, u.Email)
	if mail.From != s.Config.VerificationFromEmail {
		t.Errorf("verification sent from %q, want %q", mail.From, s.Config.VerificationFromEmail)
	}

	h := s.CreateHousehold(t, u, "")
	if h.ID == "" || h.BaseCurrency != "ARS" {
		t.Fatalf("household = %+v", h)
	}

	var list []apitest.Household
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/households", Token: u.Token}).
		Status(t, http.StatusOK).JSON(t, &list)
	// "Mi plata" (created on register, plan 1.2) first, then the hogar.
	if len(list) != 2 || list[1].ID != h.ID {
		t.Fatalf("households = %+v, want Mi plata and %s", list, h.ID)
	}
}

func TestEachServerHasItsOwnDatabase(t *testing.T) {
	t.Parallel()
	a, b := apitest.New(t), apitest.New(t)
	u := a.Register(t)

	var n int
	if err := b.Pool.QueryRow(t.Context(), `SELECT count(*) FROM users WHERE email = $1`, u.Email).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("user from server A is visible in server B's database")
	}
}

func TestUnauthenticatedRequestIsRejected(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	res := s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/households"})
	res.Status(t, http.StatusUnauthorized)
}
