package households_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
)

type previewJSON struct {
	HouseholdID   string    `json:"householdId"`
	HouseholdName string    `json:"householdName"`
	Email         string    `json:"email"`
	ExpiresAt     time.Time `json:"expiresAt"`
	Status        string    `json:"status"`
}

func inspect(t testing.TB, s *apitest.Server, token string) *apitest.Res {
	t.Helper()
	return s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/invites/" + token})
}

func accept(t testing.TB, s *apitest.Server, u apitest.User, token string) *apitest.Res {
	t.Helper()
	return s.Do(t, req(http.MethodPost, "/invites/accept", u.Token, map[string]string{"token": token}))
}

func status(t testing.TB, s *apitest.Server, token string) string {
	t.Helper()
	var p previewJSON
	inspect(t, s, token).Status(t, http.StatusOK).JSON(t, &p)
	return p.Status
}

func TestInvites(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	newHousehold := func(t *testing.T) (apitest.User, apitest.Household) {
		owner := s.Register(t)
		return owner, s.CreateHousehold(t, owner, "Casa Test")
	}

	t.Run("owner invites: email, link and public preview", func(t *testing.T) {
		owner, h := newHousehold(t)
		email := apitest.UniqueEmail()
		before := time.Now()

		var inv apitest.Invite
		s.Do(t, req(http.MethodPost, "/households/"+h.ID+"/invites", owner.Token, map[string]string{"email": " " + strings.ToUpper(email)})).
			Status(t, http.StatusCreated).JSON(t, &inv)
		if inv.Invite.Email != email || inv.Invite.Status != "pending" || inv.Invite.HouseholdID != h.ID || !inv.EmailSent {
			t.Errorf("invite = %+v", inv)
		}
		if inv.AcceptURL != s.Config.AppBaseURL+"/invite/accept?token="+inv.Token {
			t.Errorf("acceptUrl = %q", inv.AcceptURL)
		}
		mail := s.Mail.LastTo(t, email)
		if mail.Subject != "Te invitaron a unirte al hogar Casa Test en TestBrand" || !strings.Contains(mail.HTML, "Te invitaron a TestBrand") {
			t.Errorf("invite mail brand: subject %q", mail.Subject)
		}
		if mail.From != s.Config.InviteFromEmail || mail.Token(t) != inv.Token || !strings.Contains(mail.Subject, "Casa Test") {
			t.Errorf("invite mail = %+v", mail)
		}

		var p previewJSON
		inspect(t, s, inv.Token).Status(t, http.StatusOK).JSON(t, &p)
		if p.HouseholdID != h.ID || p.HouseholdName != "Casa Test" || p.Email != email || p.Status != "pending" {
			t.Errorf("preview = %+v", p)
		}
		if d := p.ExpiresAt.Sub(before); d < 7*24*time.Hour-time.Minute || d > 7*24*time.Hour+time.Minute {
			t.Errorf("invite lasts %v, want 7 days", d)
		}
	})

	t.Run("register with the invite joins and verifies the email", func(t *testing.T) {
		owner, h := newHousehold(t)
		email := apitest.UniqueEmail()
		inv := s.Invite(t, owner, h.ID, email)
		sentBefore := len(s.Mail.Sent())

		var res struct {
			User struct {
				ID              string     `json:"id"`
				EmailVerifiedAt *time.Time `json:"emailVerifiedAt"`
			} `json:"user"`
		}
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/auth/register", Body: map[string]string{
			"email": email, "password": apitest.DefaultPassword, "firstName": "Inv", "inviteToken": inv.Token,
		}}).Status(t, http.StatusCreated).JSON(t, &res)

		if res.User.EmailVerifiedAt == nil {
			t.Errorf("invited user not marked verified")
		}
		if members(t, s, owner, h.ID)[res.User.ID] != "member" {
			t.Errorf("invited user did not join")
		}
		for _, m := range s.Mail.Sent()[sentBefore:] {
			if m.To[0] == email {
				t.Errorf("verification mail sent to an invited user: %q", m.Subject)
			}
		}
		if status(t, s, inv.Token) != "accepted" {
			t.Errorf("invite not accepted")
		}
	})

	// Plan 0.8: registration is one transaction. Before, a failure after the
	// user insert (here, marking the email verified) left the account
	// created and the invite consumed, with a 201.
	t.Run("a registration that fails midway leaves nothing behind", func(t *testing.T) {
		owner, h := newHousehold(t)
		email := apitest.UniqueEmail()
		inv := s.Invite(t, owner, h.ID, email)
		fn := "fail_verify_" + strings.ReplaceAll(strings.ReplaceAll(strings.Split(email, "@")[0], "-", "_"), ".", "_")
		for _, sql := range []string{
			`CREATE FUNCTION ` + fn + `() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN RAISE EXCEPTION 'injected failure'; END $$`,
			`CREATE TRIGGER ` + fn + ` BEFORE UPDATE OF email_verified_at ON users
			FOR EACH ROW WHEN (NEW.email = '` + email + `') EXECUTE FUNCTION ` + fn + `()`,
		} {
			if _, err := s.Pool.Exec(context.Background(), sql); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			_, _ = s.Pool.Exec(context.Background(), `DROP TRIGGER `+fn+` ON users; DROP FUNCTION `+fn+`()`)
		})

		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/auth/register", Body: map[string]string{
			"email": email, "password": apitest.DefaultPassword, "firstName": "Inv", "inviteToken": inv.Token,
		}}).Status(t, http.StatusInternalServerError)

		if n := count(t, s, `SELECT count(*) FROM users WHERE email = $1`, email); n != 0 {
			t.Errorf("user rows after a failed registration = %d, want 0", n)
		}
		if status(t, s, inv.Token) != "pending" {
			t.Errorf("invite consumed by a failed registration")
		}
	})

	t.Run("register with someone else's invite does not join", func(t *testing.T) {
		owner, h := newHousehold(t)
		inv := s.Invite(t, owner, h.ID, apitest.UniqueEmail())
		u := s.Register(t, apitest.RegisterOpts{InviteToken: inv.Token})
		if _, joined := members(t, s, owner, h.ID)[u.ID]; joined {
			t.Errorf("user joined with an invite for another email")
		}
		s.Mail.LastTo(t, u.Email) // got the normal verification mail
		if status(t, s, inv.Token) != "pending" {
			t.Errorf("invite consumed by the wrong user")
		}
	})

	t.Run("existing user accepts while logged in", func(t *testing.T) {
		owner, h := newHousehold(t)
		u := s.Register(t)
		inv := s.Invite(t, owner, h.ID, u.Email)

		var m struct {
			HouseholdID string `json:"householdId"`
			UserID      string `json:"userId"`
			Role        string `json:"role"`
		}
		accept(t, s, u, inv.Token).Status(t, http.StatusOK).JSON(t, &m)
		if m.HouseholdID != h.ID || m.UserID != u.ID || m.Role != "member" {
			t.Errorf("accept = %+v", m)
		}
		if n := count(t, s, `SELECT count(*) FROM household_split_rules WHERE household_id = $1 AND user_id = $2`, h.ID, u.ID); n != 1 {
			t.Errorf("split rule rows for new member = %d, want 1", n)
		}
		accept(t, s, u, inv.Token).WantError(t, http.StatusConflict, "conflict", "")
	})

	t.Run("only the invited address can accept", func(t *testing.T) {
		owner, h := newHousehold(t)
		invited, other := s.Register(t), s.Register(t)
		inv := s.Invite(t, owner, h.ID, invited.Email)
		accept(t, s, other, inv.Token).WantError(t, http.StatusForbidden, "forbidden", "")
		if status(t, s, inv.Token) != "pending" {
			t.Errorf("invite consumed by the wrong user")
		}
	})

	t.Run("inviting an existing member is a conflict", func(t *testing.T) {
		owner, h := newHousehold(t)
		m := s.AddMember(t, owner, h.ID)
		s.Do(t, req(http.MethodPost, "/households/"+h.ID+"/invites", owner.Token, map[string]string{"email": m.Email})).
			WantError(t, http.StatusConflict, "conflict", "")
	})

	t.Run("only the owner manages invites", func(t *testing.T) {
		owner, h := newHousehold(t)
		member := s.AddMember(t, owner, h.ID)
		outsider := s.Register(t)
		inv := s.Invite(t, owner, h.ID, apitest.UniqueEmail())
		for _, u := range []apitest.User{member, outsider} {
			s.Do(t, req(http.MethodPost, "/households/"+h.ID+"/invites", u.Token, map[string]string{"email": apitest.UniqueEmail()})).
				WantError(t, http.StatusForbidden, "forbidden", "")
			s.Do(t, req(http.MethodGet, "/households/"+h.ID+"/invites", u.Token, nil)).WantError(t, http.StatusForbidden, "forbidden", "")
			s.Do(t, req(http.MethodDelete, "/households/invites/"+inv.Invite.ID, u.Token, nil)).WantError(t, http.StatusForbidden, "forbidden", "")
			s.Do(t, req(http.MethodPost, "/households/invites/"+inv.Invite.ID+"/resend", u.Token, nil)).
				WantError(t, http.StatusForbidden, "forbidden", "")
		}
		if status(t, s, inv.Token) != "pending" {
			t.Errorf("invite changed by a non-owner")
		}
	})

	t.Run("list pending, revoke and resend", func(t *testing.T) {
		owner, h := newHousehold(t)
		keep := s.Invite(t, owner, h.ID, apitest.UniqueEmail())
		revoke := s.Invite(t, owner, h.ID, apitest.UniqueEmail())

		s.Do(t, req(http.MethodDelete, "/households/invites/"+revoke.Invite.ID, owner.Token, nil)).Status(t, http.StatusNoContent)
		if status(t, s, revoke.Token) != "revoked" {
			t.Errorf("revoked invite status = %q", status(t, s, revoke.Token))
		}
		s.Do(t, req(http.MethodDelete, "/households/invites/"+revoke.Invite.ID, owner.Token, nil)).WantError(t, http.StatusConflict, "conflict", "")
		s.Do(t, req(http.MethodPost, "/households/invites/"+revoke.Invite.ID+"/resend", owner.Token, nil)).
			WantError(t, http.StatusConflict, "conflict", "")
		u := s.Register(t, apitest.RegisterOpts{Email: revoke.Invite.Email})
		accept(t, s, u, revoke.Token).WantError(t, http.StatusConflict, "conflict", "")

		var pending []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		s.Do(t, req(http.MethodGet, "/households/"+h.ID+"/invites", owner.Token, nil)).Status(t, http.StatusOK).JSON(t, &pending)
		if len(pending) != 1 || pending[0].ID != keep.Invite.ID || pending[0].Status != "pending" {
			t.Errorf("pending = %+v, want only %s", pending, keep.Invite.ID)
		}

		var again apitest.Invite
		s.Do(t, req(http.MethodPost, "/households/invites/"+keep.Invite.ID+"/resend", owner.Token, nil)).
			Status(t, http.StatusOK).JSON(t, &again)
		if again.Token == keep.Token || again.Invite.ID != keep.Invite.ID || !again.EmailSent {
			t.Errorf("resend = %+v", again)
		}
		if mail := s.Mail.LastTo(t, keep.Invite.Email); !strings.HasPrefix(mail.Subject, "Recordatorio") || mail.Token(t) != again.Token {
			t.Errorf("resend mail = %q", mail.Subject)
		}
		inspect(t, s, keep.Token).WantError(t, http.StatusNotFound, "not_found", "")
		if status(t, s, again.Token) != "pending" {
			t.Errorf("new token not pending")
		}
	})

	t.Run("expired invite", func(t *testing.T) {
		owner, h := newHousehold(t)
		u := s.Register(t)
		inv := s.Invite(t, owner, h.ID, u.Email)
		if _, err := s.Pool.Exec(t.Context(), `UPDATE household_invites SET expires_at = now() - interval '1 second' WHERE id = $1`, inv.Invite.ID); err != nil {
			t.Fatal(err)
		}
		if got := status(t, s, inv.Token); got != "expired" {
			t.Errorf("status = %q", got)
		}
		accept(t, s, u, inv.Token).WantError(t, http.StatusConflict, "conflict", "")
		// Resending an expired invite is refused too.
		s.Do(t, req(http.MethodPost, "/households/invites/"+inv.Invite.ID+"/resend", owner.Token, nil)).
			WantError(t, http.StatusConflict, "conflict", "")
	})

	t.Run("validation and unknown ids", func(t *testing.T) {
		owner, h := newHousehold(t)
		path := "/households/" + h.ID + "/invites"
		s.Do(t, req(http.MethodPost, path, owner.Token, map[string]string{"email": ""})).WantError(t, http.StatusUnprocessableEntity, "validation", "email")
		s.Do(t, req(http.MethodPost, path, owner.Token, map[string]string{"email": "nope"})).WantError(t, http.StatusUnprocessableEntity, "validation", "email")
		s.Do(t, req(http.MethodPost, "/households/nope/invites", owner.Token, map[string]string{"email": apitest.UniqueEmail()})).
			WantError(t, http.StatusUnprocessableEntity, "validation", "id")
		s.Do(t, req(http.MethodDelete, "/households/invites/nope", owner.Token, nil)).WantError(t, http.StatusUnprocessableEntity, "validation", "inviteId")
		s.Do(t, req(http.MethodDelete, "/households/invites/00000000-0000-0000-0000-000000000001", owner.Token, nil)).
			WantError(t, http.StatusNotFound, "not_found", "")
		inspect(t, s, "made-up").WantError(t, http.StatusNotFound, "not_found", "")
		accept(t, s, owner, "made-up").WantError(t, http.StatusNotFound, "not_found", "")
		accept(t, s, owner, " ").WantError(t, http.StatusUnprocessableEntity, "validation", "token")
	})

	t.Run("accept and invite management need auth; preview does not", func(t *testing.T) {
		owner, h := newHousehold(t)
		inv := s.Invite(t, owner, h.ID, apitest.UniqueEmail())
		inspect(t, s, inv.Token).Status(t, http.StatusOK)
		for _, r := range []apitest.Req{
			req(http.MethodPost, "/invites/accept", "", map[string]string{"token": inv.Token}),
			req(http.MethodPost, "/households/"+h.ID+"/invites", "", map[string]string{"email": apitest.UniqueEmail()}),
			req(http.MethodGet, "/households/"+h.ID+"/invites", "", nil),
			req(http.MethodDelete, "/households/invites/"+inv.Invite.ID, "", nil),
			req(http.MethodPost, "/households/invites/"+inv.Invite.ID+"/resend", "", nil),
		} {
			s.Do(t, r).WantError(t, http.StatusUnauthorized, "unauthorized", "")
		}
	})

	t.Run("mail failure still creates the invite", func(t *testing.T) {
		t.Parallel()
		s := apitest.New(t)
		owner := s.Register(t)
		h := s.CreateHousehold(t, owner, "")
		s.Mail.Fail = errMail{}
		inv := s.Invite(t, owner, h.ID, apitest.UniqueEmail())
		if inv.EmailSent || inv.Token == "" {
			t.Errorf("invite = %+v, want emailSent=false with a token to share", inv)
		}
	})
}

type errMail struct{}

func (errMail) Error() string { return "mail down" }
