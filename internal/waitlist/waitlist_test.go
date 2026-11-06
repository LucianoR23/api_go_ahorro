package waitlist_test

// Plan B.5: the waitlist against its contract (valuo_web/docs/waitlist-contract.md,
// section 6: every response, CORS, Turnstile, rate limit, confirm and unsubscribe).

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

const landing = "landing.test"

var tokenN int

// body: a valid sign-up with a fresh Turnstile token for the landing.
func body(s *apitest.Server, email string, extra map[string]any) map[string]any {
	tokenN++
	tok := "ts-" + email + "-" + string(rune('a'+tokenN%26)) + time.Now().Format("150405.000000")
	s.Turnstile.Add(tok, landing)
	b := map[string]any{
		"email": email, "monotributo": false, "form": "hero",
		"notice_version": "aviso-2026-11-04+priv-0.4", "turnstile_token": tok,
	}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func signup(t *testing.T, s *apitest.Server, b any) *apitest.Res {
	t.Helper()
	return s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/waitlist", Body: b})
}

// mails waits for the background confirmation mails to addr.
func mails(t *testing.T, s *apitest.Server, addr string, want int) []apitest.Mail {
	t.Helper()
	var got []apitest.Mail
	deadline := time.Now().Add(5 * time.Second)
	for {
		got = got[:0]
		for _, m := range s.Mail.Sent() {
			if m.To[0] == addr {
				got = append(got, m)
			}
		}
		if len(got) >= want || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(got) != want {
		t.Fatalf("mails to %s = %d, want %d", addr, len(got), want)
	}
	return got
}

var confirmLink = regexp.MustCompile(`https://landing\.test/confirmar#token=([A-Za-z0-9_%-]+)`)
var unsubLink = regexp.MustCompile(`https://api\.test/waitlist/unsubscribe\?token=([A-Za-z0-9_-]+)`)

func confirmToken(t *testing.T, m apitest.Mail) string {
	t.Helper()
	sub := confirmLink.FindStringSubmatch(m.HTML)
	if sub == nil {
		t.Fatalf("no confirmation link in %q", m.HTML)
	}
	return sub[1]
}

func confirm(t *testing.T, s *apitest.Server, token string) *apitest.Res {
	t.Helper()
	return s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/waitlist/confirm", Body: map[string]string{"token": token}})
}

func TestSignup(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("new address: 202, a confirmation mail with one-click unsubscribe", func(t *testing.T) {
		email := apitest.UniqueEmail()
		res := signup(t, s, body(s, "  "+strings.ToUpper(email)+" ", map[string]any{"monotributo": true, "form": "cta",
			"source": " Instagram Ads! ", "medium": "SOCIAL", "campaign": "  ", "referrer_path": "/precios"}))
		res.Status(t, http.StatusAccepted)
		if strings.TrimSpace(string(res.Body)) != `{"status":"pending_confirmation"}` || strings.Contains(string(res.Body), email) {
			t.Errorf("body = %s", res.Body)
		}
		m := mails(t, s, email, 1)[0]
		if m.From != "TestBrand <waitlist@example.test>" || !strings.Contains(m.Subject, "lista de espera de TestBrand") {
			t.Errorf("mail = %q from %q", m.Subject, m.From)
		}
		u := unsubLink.FindStringSubmatch(m.HTML)
		if u == nil || m.Headers["List-Unsubscribe"] != "<https://api.test/waitlist/unsubscribe?token="+u[1]+">" ||
			m.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
			t.Errorf("unsubscribe link/headers = %v %v", u, m.Headers)
		}
		for _, want := range []string{"AGENCIA DE ACCESO A LA INFORMACIÓN PÚBLICA", "https://landing.test/legal/privacidad", "hola@example.test", "48 horas"} {
			if !strings.Contains(m.HTML, want) {
				t.Errorf("mail lacks %q", want)
			}
		}
		confirmToken(t, m)

		var monotributo bool
		var form, notice string
		var source, medium, campaign, ref *string
		if err := s.Pool.QueryRow(t.Context(), `SELECT monotributo, form, source, medium, campaign, referrer_path, notice_version
			FROM waitlist_entries WHERE email = $1`, email).Scan(&monotributo, &form, &source, &medium, &campaign, &ref, &notice); err != nil {
			t.Fatal(err)
		}
		if !monotributo || form != "cta" || source == nil || *source != "instagram-ads" || medium == nil || *medium != "social" ||
			campaign != nil || ref == nil || *ref != "/precios" || notice != "aviso-2026-11-04+priv-0.4" {
			t.Errorf("stored = %v %s %v %v %v %v %s", monotributo, form, source, medium, campaign, ref, notice)
		}
	})

	t.Run("a known address gets the same answer; resends are throttled; confirmed ones get nothing", func(t *testing.T) {
		email := apitest.UniqueEmail()
		first := signup(t, s, body(s, email, nil)).Status(t, http.StatusAccepted)
		mails(t, s, email, 1)
		again := signup(t, s, body(s, email, nil)).Status(t, http.StatusAccepted)
		if string(first.Body) != string(again.Body) {
			t.Errorf("bodies differ: %s vs %s", first.Body, again.Body)
		}
		time.Sleep(200 * time.Millisecond)
		mails(t, s, email, 1) // within 10 minutes: no second mail

		// 10 minutes later: a new link, and the old one stops working.
		old := confirmToken(t, mails(t, s, email, 1)[0])
		if _, err := s.Pool.Exec(t.Context(), `UPDATE waitlist_entries SET last_sent_at = now() - interval '11 minutes' WHERE email = $1`, email); err != nil {
			t.Fatal(err)
		}
		signup(t, s, body(s, email, nil)).Status(t, http.StatusAccepted)
		fresh := confirmToken(t, mails(t, s, email, 2)[1])
		confirm(t, s, old).Status(t, http.StatusGone)
		confirm(t, s, fresh).Status(t, http.StatusOK)

		if _, err := s.Pool.Exec(t.Context(), `UPDATE waitlist_entries SET last_sent_at = now() - interval '11 minutes' WHERE email = $1`, email); err != nil {
			t.Fatal(err)
		}
		signup(t, s, body(s, email, nil)).Status(t, http.StatusAccepted)
		time.Sleep(200 * time.Millisecond)
		mails(t, s, email, 2) // confirmed: nothing more
	})

	t.Run("validation", func(t *testing.T) {
		for name, tc := range map[string]struct {
			extra map[string]any
			field string
		}{
			"invalid email":        {map[string]any{"email": "nope"}, "email"},
			"display name":         {map[string]any{"email": "Ana <ana@example.test>"}, "email"},
			"long local part":      {map[string]any{"email": strings.Repeat("a", 65) + "@example.test"}, "email"},
			"bad form":             {map[string]any{"form": "footer"}, "form"},
			"missing monotributo":  {map[string]any{"monotributo": nil}, "monotributo"},
			"no notice version":    {map[string]any{"notice_version": ""}, "notice_version"},
			"long notice version":  {map[string]any{"notice_version": strings.Repeat("v", 33)}, "notice_version"},
			"query in the referer": {map[string]any{"referrer_path": "/?utm_source=x"}, "referrer_path"},
			"unknown field":        {map[string]any{"utm_term": "x"}, "body"},
		} {
			t.Run(name, func(t *testing.T) {
				b := body(s, apitest.UniqueEmail(), tc.extra)
				if v, ok := tc.extra["monotributo"]; ok && v == nil {
					delete(b, "monotributo")
				}
				signup(t, s, b).WantError(t, http.StatusUnprocessableEntity, "validation", tc.field)
			})
		}
		big := body(s, apitest.UniqueEmail(), map[string]any{"campaign": strings.Repeat("x", 5000)})
		signup(t, s, big).WantError(t, http.StatusUnprocessableEntity, "validation", "body")
	})

	t.Run("Turnstile: missing, unknown, reused or from another site is 403", func(t *testing.T) {
		email := apitest.UniqueEmail()
		b := body(s, email, nil)
		delete(b, "turnstile_token")
		signup(t, s, b).WantError(t, http.StatusForbidden, "turnstile_failed", "")
		signup(t, s, body(s, email, map[string]any{"turnstile_token": "made-up"})).WantError(t, http.StatusForbidden, "turnstile_failed", "")

		ok := body(s, email, nil)
		signup(t, s, ok).Status(t, http.StatusAccepted)
		signup(t, s, ok).WantError(t, http.StatusForbidden, "turnstile_failed", "") // same token twice

		s.Turnstile.Add("ts-elsewhere", "evil.test")
		signup(t, s, body(s, email, map[string]any{"turnstile_token": "ts-elsewhere"})).WantError(t, http.StatusForbidden, "turnstile_failed", "")
	})
}

func TestConfirm(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	email := apitest.UniqueEmail()
	signup(t, s, body(s, email, nil)).Status(t, http.StatusAccepted)
	token := confirmToken(t, mails(t, s, email, 1)[0])

	var ok struct{ Status string }
	confirm(t, s, token).Status(t, http.StatusOK).JSON(t, &ok)
	confirm(t, s, token).Status(t, http.StatusOK) // idempotent
	if ok.Status != "confirmed" {
		t.Errorf("status = %q", ok.Status)
	}
	confirm(t, s, "made-up").WantError(t, http.StatusGone, "expired", "")
	confirm(t, s, "").WantError(t, http.StatusGone, "expired", "")

	late := apitest.UniqueEmail()
	signup(t, s, body(s, late, nil)).Status(t, http.StatusAccepted)
	lateToken := confirmToken(t, mails(t, s, late, 1)[0])
	if _, err := s.Pool.Exec(t.Context(), `UPDATE waitlist_entries SET confirm_expires_at = now() - interval '1 minute' WHERE email = $1`, late); err != nil {
		t.Fatal(err)
	}
	confirm(t, s, lateToken).WantError(t, http.StatusGone, "expired", "")
}

func TestUnsubscribe(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	count := func(email string) int {
		var n int
		if err := s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM waitlist_entries WHERE email = $1`, email).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	email := apitest.UniqueEmail()
	signup(t, s, body(s, email, nil)).Status(t, http.StatusAccepted)
	token := unsubLink.FindStringSubmatch(mails(t, s, email, 1)[0].HTML)[1]

	page := s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/waitlist/unsubscribe?token=" + token}).Status(t, http.StatusOK)
	if !strings.Contains(string(page.Body), `<form method="post"`) || count(email) != 1 {
		t.Errorf("GET must show a button and delete nothing (rows %d)", count(email))
	}
	// RFC 8058 one-click, as a mail client sends it.
	s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/waitlist/unsubscribe?token=" + token,
		Body: "List-Unsubscribe=One-Click", Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}}).Status(t, http.StatusOK)
	if count(email) != 0 {
		t.Error("unsubscribe kept the entry")
	}
	s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/waitlist/unsubscribe?token=" + token}).Status(t, http.StatusOK) // idempotent

	forged := token[:len(token)-2] + "AA"
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/waitlist/unsubscribe?token=" + forged}).Status(t, http.StatusNotFound)
	s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/waitlist/unsubscribe?token=" + forged}).Status(t, http.StatusNotFound)
}

func TestWaitlistCORSAndRateLimit(t *testing.T) {
	t.Parallel()
	s := apitest.New(t, apitest.WithRealRateLimits())

	preflight := func(path, origin string) *apitest.Res {
		return s.Do(t, apitest.Req{Method: http.MethodOptions, Path: path, Header: http.Header{
			"Origin": {origin}, "Access-Control-Request-Method": {"POST"}, "Access-Control-Request-Headers": {"content-type"},
		}})
	}
	ok := preflight("/waitlist", "https://landing.test")
	if ok.Header.Get("Access-Control-Allow-Origin") != "https://landing.test" || ok.Header.Get("Access-Control-Allow-Credentials") != "" ||
		ok.Header.Get("Access-Control-Max-Age") != "600" {
		t.Errorf("landing preflight = %v", ok.Header)
	}
	for _, origin := range []string{"https://evil.test", "http://localhost:3005"} { // the app's origin is not the landing
		if got := preflight("/waitlist", origin).Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("preflight from %s allowed: %q", origin, got)
		}
	}
	// The app keeps its own CORS (with credentials) elsewhere.
	app := preflight("/expenses", "http://localhost:3005")
	if app.Header.Get("Access-Control-Allow-Origin") != "http://localhost:3005" || app.Header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("app preflight = %v", app.Header)
	}

	var last *apitest.Res
	for range 6 {
		last = signup(t, s, body(s, apitest.UniqueEmail(), nil))
	}
	last.WantError(t, http.StatusTooManyRequests, "rate_limited", "")
	if last.Header.Get("Retry-After") == "" {
		t.Error("429 without Retry-After")
	}
}

func TestAdmin(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	admin, user := s.Register(t), s.Register(t)
	s.MakeSuperadmin(t, admin)

	confirmed := apitest.UniqueEmail()
	signup(t, s, body(s, confirmed, map[string]any{"monotributo": true, "source": "instagram"})).Status(t, http.StatusAccepted)
	confirm(t, s, confirmToken(t, mails(t, s, confirmed, 1)[0])).Status(t, http.StatusOK)
	pending := apitest.UniqueEmail()
	signup(t, s, body(s, pending, nil)).Status(t, http.StatusAccepted)

	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/admin/waitlist.csv", Token: user.Token}).WantError(t, http.StatusForbidden, "forbidden", "")
	csv := s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/admin/waitlist.csv", Token: admin.Token}).Status(t, http.StatusOK)
	text := string(csv.Body)
	if !strings.HasPrefix(text, "email,monotributo,form,source,medium,campaign,referrer_path,notice_version,created_at,confirmed_at\n") ||
		!strings.Contains(text, confirmed+",true,hero,instagram,") || strings.Contains(text, pending) {
		t.Errorf("export = %q", text)
	}

	s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/admin/waitlist?email=" + strings.ToUpper(confirmed), Token: admin.Token}).Status(t, http.StatusNoContent)
	s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/admin/waitlist?email=nope", Token: admin.Token}).WantError(t, http.StatusUnprocessableEntity, "validation", "email")
	var left int
	if err := s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM waitlist_entries WHERE email = $1`, confirmed).Scan(&left); err != nil || left != 0 {
		t.Errorf("deleted entry left %d, %v", left, err)
	}

	var exports, deletes int
	if err := s.Pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE action = 'admin.waitlist_exported'),
		count(*) FILTER (WHERE action = 'admin.waitlist_deleted') FROM audit_events WHERE actor_id = $1`, admin.ID).Scan(&exports, &deletes); err != nil {
		t.Fatal(err)
	}
	if exports != 1 || deletes != 1 {
		t.Errorf("audit: %d exports, %d deletes", exports, deletes)
	}
}
