package auth_test

// Characterization tests of the auth and user endpoints (item 0.21b), run
// against the real router and a fresh database through apitest. Known audit
// bugs are pinned with testutil.KnownBug and flip green in the item that
// fixes them (mostly 0.5).

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

// userJSON is the user object every auth endpoint returns.
type userJSON struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	FirstName       string     `json:"firstName"`
	LastName        string     `json:"lastName"`
	EmailVerifiedAt *time.Time `json:"emailVerifiedAt"`
	PendingEmail    *string    `json:"pendingEmail"`
	IsSuperadmin    bool       `json:"isSuperadmin"`
}

type authJSON struct {
	User            userJSON  `json:"user"`
	AccessToken     string    `json:"accessToken"`
	AccessExpiresAt time.Time `json:"accessExpiresAt"`
}

type errorJSON struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field"`
}

func getMe(t testing.TB, s *apitest.Server, token string) userJSON {
	t.Helper()
	var u userJSON
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/me", Token: token}).
		Status(t, http.StatusOK).JSON(t, &u)
	return u
}

func decodeError(t testing.TB, r *apitest.Res) errorJSON {
	t.Helper()
	var e errorJSON
	r.JSON(t, &e)
	return e
}

// wantError checks status, code and (when not empty) the offending field.
func wantError(t testing.TB, r *apitest.Res, status int, code, field string) {
	t.Helper()
	r.Status(t, status)
	var e errorJSON
	r.JSON(t, &e)
	if e.Code != code {
		t.Errorf("error code = %q, want %q (body %s)", e.Code, code, r.Body)
	}
	if field != "" && e.Field != field {
		t.Errorf("error field = %q, want %q (body %s)", e.Field, field, r.Body)
	}
}

func post(path string, body any) apitest.Req {
	return apitest.Req{Method: http.MethodPost, Path: path, Body: body}
}
