package expenses_test

// POST /expenses/parse-receipt (plan 3.3): one Claude call reads the image,
// the server writes it as a sentence the rules read, nothing is saved and
// the image is not kept.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/config"
	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

// fakeClaudeVision answers like the Messages API and keeps the media type
// of each image block it got.
type fakeClaudeVision struct {
	mu     sync.Mutex
	answer string
	media  []string
}

func (f *fakeClaudeVision) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Content []struct {
				Type   string `json:"type"`
				Source struct {
					MediaType string `json:"media_type"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &req)
	f.mu.Lock()
	for _, c := range req.Messages[0].Content {
		if c.Type == "image" {
			f.media = append(f.media, c.Source.MediaType)
		}
	}
	answer := f.answer
	f.mu.Unlock()
	out, _ := json.Marshal(map[string]any{
		"content":     []map[string]string{{"type": "text", "text": answer}},
		"stop_reason": "end_turn",
		"usage":       map[string]int{"input_tokens": 1800, "output_tokens": 50},
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

func TestParseReceipt(t *testing.T) {
	t.Parallel()
	f := &fakeClaudeVision{answer: `{"intent":"expense","amount":20000,"currency":"ARS","date":"",
		"installments":0,"payment":"visa galicia","description":"Juan Pérez"}`}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	s := apitest.New(t, apitest.WithConfig(func(c *config.Config) {
		c.AIEnabled = true
		c.AnthropicAPIKey = "sk-test"
		c.AnthropicBaseURL = srv.URL
	}))
	u := s.Register(t, apitest.RegisterOpts{FirstName: "Ana", Verify: true})
	h := s.CreateHousehold(t, u, "")
	galicia := s.CreateCreditCard(t, u, "Visa Galicia", 28, 8, "2026-11-28", "2026-12-08")

	png := append([]byte("\x89PNG\x0D\x0A\x1A\x0A"), make([]byte, 64)...)
	send := func(t *testing.T, contentType string, body []byte, token string) *apitest.Res {
		t.Helper()
		return s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/expenses/parse-receipt", Token: token, Household: h.ID,
			Body: body, Header: http.Header{"Content-Type": {contentType}}})
	}

	t.Run("read and written as a sentence", func(t *testing.T) {
		var got struct {
			parsed
			Transcript *string `json:"transcript"`
		}
		send(t, "image/png", png, u.Token).Status(t, http.StatusOK).JSON(t, &got)
		if got.Transcript == nil || *got.Transcript != "Juan Pérez 20000 visa galicia" || !got.Understood ||
			*got.Amount != 20000 || *got.PaymentMethodID != galicia.ID || *got.Description != "Juan Pérez" || got.Source != "ai" {
			t.Fatalf("got %+v", got)
		}
		if f.media[0] != "image/png" {
			t.Errorf("sent %q", f.media[0])
		}
		var task string
		if err := s.Pool.QueryRow(context.Background(),
			`SELECT task FROM ai_usage WHERE user_id = $1`, u.ID).Scan(&task); err != nil {
			t.Fatal(err)
		}
		if task != "parse_receipt" {
			t.Errorf("ai_usage %s", task)
		}
	})

	t.Run("a photo over the JSON cap is taken", func(t *testing.T) {
		big := append(bytes.Clone(png), make([]byte, 2<<20)...)
		send(t, "image/png", big, u.Token).Status(t, http.StatusOK)
	})

	t.Run("nothing is saved", func(t *testing.T) {
		var n int
		if err := s.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM expenses WHERE household_id = $1`, h.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%d expenses saved", n)
		}
	})

	t.Run("bad input", func(t *testing.T) {
		calls := len(f.media)
		send(t, "audio/webm", png, u.Token).Status(t, http.StatusUnprocessableEntity)
		send(t, "image/png", nil, u.Token).Status(t, http.StatusUnprocessableEntity)
		send(t, "image/jpeg", png, u.Token).Status(t, http.StatusUnprocessableEntity)
		send(t, "image/png", []byte("<svg onload=x>"), u.Token).Status(t, http.StatusUnprocessableEntity)
		huge := append(bytes.Clone(png), make([]byte, 5<<20)...)
		if res := send(t, "image/png", huge, u.Token); res.Code == http.StatusOK {
			t.Errorf("an image over 5 MB got 200")
		}
		if res := send(t, "image/png", png, ""); res.Code != http.StatusUnauthorized {
			t.Errorf("no token: %d", res.Code)
		}
		other := s.Register(t)
		if res := send(t, "image/png", png, other.Token); res.Code == http.StatusOK {
			t.Errorf("a stranger got 200")
		}
		if len(f.media) != calls {
			t.Errorf("Claude called %d more times", len(f.media)-calls)
		}
	})
}
