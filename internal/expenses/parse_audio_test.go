package expenses_test

// POST /expenses/parse-audio (plan 3.2): one Gemini call returns the
// transcript and the fields, the rules read the transcript, nothing is
// saved and the audio is not kept.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/config"
	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

// fakeGemini answers like the Interactions API and keeps the MIME types of
// the media parts it got.
type fakeGemini struct {
	mu     sync.Mutex
	answer string
	mimes  []string
}

func (f *fakeGemini) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input []struct {
			Type     string `json:"type"`
			MIMEType string `json:"mime_type"`
		} `json:"input"`
	}
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &req)
	f.mu.Lock()
	for _, p := range req.Input {
		if p.Type == "audio" {
			f.mimes = append(f.mimes, p.MIMEType)
		}
	}
	answer := f.answer
	f.mu.Unlock()
	out, _ := json.Marshal(map[string]any{
		"status": "completed",
		"steps":  []map[string]any{{"type": "model_output", "content": []map[string]string{{"type": "text", "text": answer}}}},
		"usage": map[string]any{
			"total_input_tokens":       300,
			"input_tokens_by_modality": []map[string]any{{"modality": "text", "tokens": 140}, {"modality": "audio", "tokens": 160}},
			"total_output_tokens":      60,
		},
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

func TestParseAudio(t *testing.T) {
	t.Parallel()
	f := &fakeGemini{answer: `{"intent":"expense","transcript":"café 2500 con la visa galicia","amount":2500,"currency":"",
		"date":"","installments":0,"payment":"visa galicia","description":"café","shared":false}`}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	s := apitest.New(t, apitest.WithConfig(func(c *config.Config) {
		c.AIEnabled = true
		c.AnthropicAPIKey = "sk-test"
		c.GeminiAPIKey = "g-test"
		c.GeminiBaseURL = srv.URL
	}))
	u := s.Register(t, apitest.RegisterOpts{FirstName: "Ana", Verify: true})
	h := s.CreateHousehold(t, u, "")
	galicia := s.CreateCreditCard(t, u, "Visa Galicia", 28, 8, "2026-11-28", "2026-12-08")

	send := func(t *testing.T, contentType string, body []byte, token string) *apitest.Res {
		t.Helper()
		return s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/expenses/parse-audio", Token: token, Household: h.ID,
			Body: body, Header: http.Header{"Content-Type": {contentType}}})
	}

	t.Run("heard and read", func(t *testing.T) {
		var got struct {
			parsed
			Transcript *string `json:"transcript"`
		}
		send(t, "audio/mp4", []byte("fake aac"), u.Token).Status(t, http.StatusOK).JSON(t, &got)
		if got.Transcript == nil || *got.Transcript != "café 2500 con la visa galicia" || !got.Understood ||
			*got.Amount != 2500 || *got.PaymentMethodID != galicia.ID || *got.Description != "Café" || got.Source != "ai" {
			t.Fatalf("got %+v", got)
		}
		if f.mimes[0] != "audio/m4a" {
			t.Errorf("sent %q", f.mimes[0])
		}
		var task string
		var seconds int
		if err := s.Pool.QueryRow(context.Background(),
			`SELECT task, audio_seconds FROM ai_usage WHERE user_id = $1`, u.ID).Scan(&task, &seconds); err != nil {
			t.Fatal(err)
		}
		if task != "parse_audio" || seconds != 5 {
			t.Errorf("ai_usage %s %d", task, seconds)
		}
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
		send(t, "image/png", []byte("png"), u.Token).Status(t, http.StatusUnprocessableEntity)
		send(t, "audio/webm", nil, u.Token).Status(t, http.StatusUnprocessableEntity)
		big := []byte(strings.Repeat("a", 1<<20+1))
		if res := send(t, "audio/webm", big, u.Token); res.Code == http.StatusOK {
			t.Errorf("a too long note got 200")
		}
		other := s.Register(t)
		if res := send(t, "audio/webm", []byte("x"), other.Token); res.Code == http.StatusOK {
			t.Errorf("a stranger got 200")
		}
		if len(f.mimes) != 1 {
			t.Errorf("Gemini called %d times", len(f.mimes))
		}
	})
}
