package apitest

import (
	"net/http"
	"testing"
)

// User is an account created through the API.
type User struct {
	ID        string
	Email     string
	Password  string
	FirstName string
	LastName  string
	Token     string // access token from register/login
}

// RegisterOpts customizes Register. Zero values get sensible defaults.
type RegisterOpts struct {
	Email       string
	Password    string
	FirstName   string
	LastName    string
	InviteToken string
	Verify      bool // confirm the verification email right away
}

// DefaultPassword satisfies the API's password policy.
const DefaultPassword = "Test-password-123!"

type authResponse struct {
	User struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
	AccessToken string `json:"accessToken"`
}

// Register creates a user via POST /auth/register.
func (s *Server) Register(t testing.TB, o ...RegisterOpts) User {
	t.Helper()
	var opt RegisterOpts
	if len(o) > 0 {
		opt = o[0]
	}
	if opt.Email == "" {
		opt.Email = UniqueEmail()
	}
	if opt.Password == "" {
		opt.Password = DefaultPassword
	}
	if opt.FirstName == "" {
		opt.FirstName = "Ana"
	}
	if opt.LastName == "" {
		opt.LastName = "Test"
	}
	body := map[string]string{
		"email": opt.Email, "password": opt.Password,
		"firstName": opt.FirstName, "lastName": opt.LastName,
	}
	if opt.InviteToken != "" {
		body["inviteToken"] = opt.InviteToken
	}
	res := s.Do(t, Req{Method: http.MethodPost, Path: "/auth/register", Body: body})
	res.Status(t, http.StatusCreated)
	var ar authResponse
	res.JSON(t, &ar)

	u := User{ID: ar.User.ID, Email: opt.Email, Password: opt.Password,
		FirstName: opt.FirstName, LastName: opt.LastName, Token: ar.AccessToken}
	if opt.Verify {
		s.VerifyEmail(t, u)
	}
	return u
}

// VerifyEmail confirms the last verification email sent to u.
func (s *Server) VerifyEmail(t testing.TB, u User) {
	t.Helper()
	token := s.Mail.LastTo(t, u.Email).Token(t)
	s.Do(t, Req{Method: http.MethodPost, Path: "/auth/verify-email", Body: map[string]string{"token": token}}).
		Status(t, http.StatusNoContent)
}

// Household is a household created through the API.
type Household struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	BaseCurrency string `json:"baseCurrency"`
}

// CreateHousehold creates a household owned by u via POST /households.
func (s *Server) CreateHousehold(t testing.TB, u User, name string) Household {
	t.Helper()
	if name == "" {
		name = "Casa de " + u.FirstName
	}
	res := s.Do(t, Req{Method: http.MethodPost, Path: "/households", Token: u.Token,
		Body: map[string]string{"name": name, "baseCurrency": "ARS"}})
	res.Status(t, http.StatusCreated)
	var h Household
	res.JSON(t, &h)
	return h
}
