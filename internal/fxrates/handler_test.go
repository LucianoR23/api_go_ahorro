package fxrates_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
)

func TestCurrentRatesEndpoint(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	u := s.Register(t)

	// No rates fetched yet (background jobs are off in apitest): empty list, not null.
	res := s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/exchange-rates/current", Token: u.Token}).Status(t, http.StatusOK)
	if strings.TrimSpace(string(res.Body)) != "[]" {
		t.Errorf("body = %s", res.Body)
	}
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/exchange-rates/current"}).WantError(t, http.StatusUnauthorized, "unauthorized", "")
}
