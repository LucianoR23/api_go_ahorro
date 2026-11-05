package auth_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
)

func TestRegister(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("creates the user, opens a session and sends the verification email", func(t *testing.T) {
		email := apitest.UniqueEmail()
		res := s.Do(t, post("/auth/register", map[string]string{
			"email": "  " + strings.ToUpper(email) + " ", "password": apitest.DefaultPassword,
			"firstName": "  Ana ", "lastName": " Pérez ",
		})).Status(t, http.StatusCreated)

		var a authJSON
		res.JSON(t, &a)
		if a.User.Email != email {
			t.Errorf("email = %q, want normalized %q", a.User.Email, email)
		}
		if a.User.FirstName != "Ana" || a.User.LastName != "Pérez" {
			t.Errorf("names = %q %q, want trimmed", a.User.FirstName, a.User.LastName)
		}
		if a.User.EmailVerifiedAt != nil || a.User.IsSuperadmin {
			t.Errorf("new user = %+v, want unverified and not superadmin", a.User)
		}
		if a.AccessToken == "" || a.AccessExpiresAt.IsZero() {
			t.Fatalf("no access token in %s", res.Body)
		}

		c := res.Cookie(apitest.RefreshCookie)
		if c == nil || c.Value == "" || !c.HttpOnly || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
			t.Errorf("refresh cookie = %+v, want HttpOnly, Path=/, SameSite=Lax", c)
		}

		if got := getMe(t, s, a.AccessToken); got.ID != a.User.ID {
			t.Errorf("/me id = %s, want %s", got.ID, a.User.ID)
		}

		mail := s.Mail.LastTo(t, email)
		if mail.From != s.Config.VerificationFromEmail || !strings.Contains(mail.HTML, s.Config.AppBaseURL+"/verify-email?token=") {
			t.Errorf("verification mail = from %q, html %q", mail.From, mail.HTML)
		}

		var cash int
		if err := s.Pool.QueryRow(t.Context(),
			`SELECT count(*) FROM payment_methods WHERE owner_user_id = $1 AND kind = 'cash'`, a.User.ID).Scan(&cash); err != nil {
			t.Fatal(err)
		}
		if cash != 1 {
			t.Errorf("cash payment methods = %d, want the bootstrap Efectivo", cash)
		}
	})

	t.Run("empty lastName is allowed", func(t *testing.T) {
		s.Do(t, post("/auth/register", map[string]string{
			"email": apitest.UniqueEmail(), "password": apitest.DefaultPassword, "firstName": "Cher", "lastName": "",
		})).Status(t, http.StatusCreated)
	})

	t.Run("duplicate email is a conflict, case-insensitive", func(t *testing.T) {
		u := s.Register(t)
		wantError(t, s.Do(t, post("/auth/register", map[string]string{
			"email": strings.ToUpper(u.Email), "password": apitest.DefaultPassword, "firstName": "Otra",
		})), http.StatusConflict, "conflict", "")
	})

	valid := func() map[string]string {
		return map[string]string{"email": apitest.UniqueEmail(), "password": apitest.DefaultPassword, "firstName": "Ana"}
	}
	cases := []struct {
		name, key, value, field string
	}{
		{"empty email", "email", "", "email"},
		{"malformed email", "email", "not-an-email", "email"},
		{"short password", "password", "Ab1defg", "password"},
		{"password without uppercase", "password", "abcdefg1", "password"},
		{"password without lowercase", "password", "ABCDEFG1", "password"},
		{"password without digit", "password", "Abcdefgh", "password"},
		{"password over 128 chars", "password", "Aa1" + strings.Repeat("x", 126), "password"},
		{"empty firstName", "firstName", "   ", "firstName"},
		{"firstName over 100 chars", "firstName", strings.Repeat("a", 101), "firstName"},
		{"lastName over 100 chars", "lastName", strings.Repeat("a", 101), "lastName"},
	}
	for _, tc := range cases {
		t.Run("validation: "+tc.name, func(t *testing.T) {
			body := valid()
			body[tc.key] = tc.value
			wantError(t, s.Do(t, post("/auth/register", body)), http.StatusUnprocessableEntity, "validation", tc.field)
			for _, m := range s.Mail.Sent() {
				if m.To[0] == body["email"] {
					t.Errorf("verification sent for a rejected registration")
				}
			}
		})
	}

	t.Run("malformed JSON", func(t *testing.T) {
		res := s.Do(t, post("/auth/register", "{not json"))
		wantError(t, res, http.StatusUnprocessableEntity, "validation", "body")
		if e := decodeError(t, res); e.Message != "JSON inválido" {
			t.Errorf("message = %q", e.Message)
		}
	})

	t.Run("wrong field type", func(t *testing.T) {
		wantError(t, s.Do(t, post("/auth/register", map[string]any{"email": 1})),
			http.StatusUnprocessableEntity, "validation", "email")
	})

	t.Run("unknown field is rejected", func(t *testing.T) {
		body := valid()
		body["passwrod"] = "typo"
		res := s.Do(t, post("/auth/register", body))
		wantError(t, res, http.StatusUnprocessableEntity, "validation", "body")

		testutil.KnownBug(t, "0.1", "decoder errors reach the client in English (json: unknown field ...)", func(t testing.TB) {
			if msg := decodeError(t, res).Message; strings.Contains(msg, "json:") {
				t.Errorf("message %q is the raw Go decoder error", msg)
			}
		})
	})
}
