package httpx_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

func TestWriteErrorConflictMessages(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("ya sos miembro del hogar: %w", domain.ErrConflict), "ya sos miembro del hogar"},
		{domain.NewConflictError("Ya existe una cuenta con este email."), "Ya existe una cuenta con este email."},
		{domain.ErrConflict, "conflicto con el estado actual"},
	} {
		rec := httptest.NewRecorder()
		httpx.WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), slog.New(slog.DiscardHandler), tc.err)
		var body struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusConflict || body.Code != "conflict" || body.Message != tc.want {
			t.Errorf("%v -> %d %+v, want 409 %q", tc.err, rec.Code, body, tc.want)
		}
	}
}
