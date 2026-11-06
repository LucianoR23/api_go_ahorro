package auth_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
)

const (
	uaFirefoxWin = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:140.0) Gecko/20100101 Firefox/140.0"
	uaSafariIOS  = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
)

type activityJSON struct {
	Items []struct {
		Action   string         `json:"action"`
		Device   string         `json:"device"`
		IP       *string        `json:"ip"`
		Metadata map[string]any `json:"metadata"`
	} `json:"items"`
}

func activity(t *testing.T, s *apitest.Server, token string) activityJSON {
	t.Helper()
	var a activityJSON
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/me/activity", Token: token}).Status(t, http.StatusOK).JSON(t, &a)
	return a
}

func loginFrom(t *testing.T, s *apitest.Server, email, password, ua string) *apitest.Res {
	t.Helper()
	return s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/auth/login",
		Body: map[string]string{"email": email, "password": password}, Header: http.Header{"User-Agent": {ua}}})
}

func countMailsTo(s *apitest.Server, addr, subjectPart string) int {
	n := 0
	for _, m := range s.Mail.Sent() {
		if m.To[0] == addr && strings.Contains(m.Subject, subjectPart) {
			n++
		}
	}
	return n
}

// Plan 0.17: the security audit log as the account owner sees it.
func TestActivity(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("sign-ins, a failed attempt and a password change appear, newest first", func(t *testing.T) {
		u := s.Register(t)
		loginFrom(t, s, u.Email, u.Password, uaFirefoxWin).Status(t, http.StatusOK)
		loginFrom(t, s, u.Email, "Wrong-pass-123", uaSafariIOS).Status(t, http.StatusUnauthorized)
		changePassword(t, s, u.Token, u.Password, newPassword).Status(t, http.StatusNoContent)

		a := activity(t, s, u.Token)
		var actions []string
		for _, it := range a.Items {
			actions = append(actions, it.Action)
		}
		got := strings.Join(actions, ",")
		if !strings.HasPrefix(got, "password.changed,login.failed,") || !strings.Contains(got, "login.success") {
			t.Fatalf("actions = %s", got)
		}
		if a.Items[1].Device != "Safari en iOS" || a.Items[1].IP == nil {
			t.Errorf("failed attempt = %+v", a.Items[1])
		}
	})

	t.Run("a failed attempt on an unknown e-mail records nothing anywhere", func(t *testing.T) {
		loginFrom(t, s, apitest.UniqueEmail(), "Whatever-123", uaFirefoxWin).Status(t, http.StatusUnauthorized)
	})

	t.Run("a sign-in from a device not seen in 90 days mails the owner; a known one does not", func(t *testing.T) {
		u := s.Register(t)
		loginFrom(t, s, u.Email, u.Password, uaFirefoxWin).Status(t, http.StatusOK)
		loginFrom(t, s, u.Email, u.Password, uaFirefoxWin).Status(t, http.StatusOK)
		before := countMailsTo(s, u.Email, "Nuevo inicio de sesión")
		loginFrom(t, s, u.Email, u.Password, uaSafariIOS).Status(t, http.StatusOK)
		if after := countMailsTo(s, u.Email, "Nuevo inicio de sesión"); after != before+1 {
			t.Errorf("new-device mails: before %d, after %d", before, after)
		}
		mail := s.Mail.LastTo(t, u.Email)
		if !strings.Contains(mail.HTML, "Safari en iOS") || !strings.Contains(mail.HTML, "/seguridad") {
			t.Errorf("new-device mail = %q", mail.HTML)
		}
		if a := activity(t, s, u.Token); a.Items[0].Action != "login.new_device" {
			t.Errorf("latest event = %s, want login.new_device", a.Items[0].Action)
		}
	})

	t.Run("logout and logout-all are recorded", func(t *testing.T) {
		u := s.Register(t)
		cookie := s.Login(t, u.Email, u.Password).RefreshCookieValue(t)
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/auth/logout", Header: apitest.WithRefreshCookie(cookie)}).Status(t, http.StatusNoContent)
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/auth/logout-all", Token: u.Token}).Status(t, http.StatusNoContent)
		a := activity(t, s, u.Token)
		if len(a.Items) < 2 || a.Items[0].Action != "logout.all" || a.Items[1].Action != "logout" {
			t.Errorf("activity = %+v", a.Items)
		}
	})

	t.Run("an email change request and its confirmation are recorded", func(t *testing.T) {
		u := s.Register(t)
		next := apitest.UniqueEmail()
		patchMe(t, s, u.Token, map[string]string{"email": next}).Status(t, http.StatusOK)
		verifyEmail(t, s, s.Mail.LastTo(t, next).Token(t)).Status(t, http.StatusNoContent)
		a := activity(t, s, u.Token)
		if a.Items[0].Action != "email.changed" || a.Items[1].Action != "email.change_requested" {
			t.Errorf("activity = %+v", a.Items)
		}
	})

	t.Run("deleting the account deletes its activity", func(t *testing.T) {
		u := s.Register(t)
		s.Login(t, u.Email, u.Password).Status(t, http.StatusOK)
		s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/me", Token: u.Token}).Status(t, http.StatusNoContent)
		var left int
		if err := s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE user_id = $1`, u.ID).Scan(&left); err != nil {
			t.Fatal(err)
		}
		var deleted int
		if err := s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action = 'account.deleted' AND target = $1 AND user_id IS NULL`,
			"user:"+u.ID).Scan(&deleted); err != nil {
			t.Fatal(err)
		}
		if left != 0 || deleted != 1 {
			t.Errorf("events left %d, deletion record %d", left, deleted)
		}
	})

	t.Run("requires auth", func(t *testing.T) {
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/me/activity"}).Status(t, http.StatusUnauthorized)
	})
}
