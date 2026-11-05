package apitest

import (
	"net/http"
	"testing"
)

// Invite is the response of creating or resending a household invite.
type Invite struct {
	Invite struct {
		ID          string `json:"id"`
		HouseholdID string `json:"householdId"`
		Email       string `json:"email"`
		Status      string `json:"status"`
	} `json:"invite"`
	Token     string `json:"token"`
	AcceptURL string `json:"acceptUrl"`
	EmailSent bool   `json:"emailSent"`
}

// Invite makes owner invite email to the household.
func (s *Server) Invite(t testing.TB, owner User, householdID, email string) Invite {
	t.Helper()
	var inv Invite
	s.Do(t, Req{Method: http.MethodPost, Path: "/households/" + householdID + "/invites", Token: owner.Token,
		Body: map[string]string{"email": email}}).Status(t, http.StatusCreated).JSON(t, &inv)
	return inv
}

// AddMember registers a new user through an invite from owner, so the user
// joins the household as a member with a verified email.
func (s *Server) AddMember(t testing.TB, owner User, householdID string) User {
	t.Helper()
	email := UniqueEmail()
	inv := s.Invite(t, owner, householdID, email)
	return s.Register(t, RegisterOpts{Email: email, FirstName: "Miembro", InviteToken: inv.Token})
}

// MakeSuperadmin flips is_superadmin for u directly in the database (there is
// no endpoint for it).
func (s *Server) MakeSuperadmin(t testing.TB, u User) {
	t.Helper()
	if _, err := s.Pool.Exec(t.Context(), `UPDATE users SET is_superadmin = true WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("apitest: make superadmin: %v", err)
	}
}
