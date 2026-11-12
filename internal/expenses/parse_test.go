package expenses_test

// POST /expenses/parse (plan 3.1): rules first, the AI only for an unclear
// amount, nothing saved.

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

// fakeClaude answers like the Messages API with a fixed JSON answer and
// keeps the user messages it got.
type fakeClaude struct {
	mu     sync.Mutex
	answer string
	texts  []string
}

func (f *fakeClaude) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &req)
	f.mu.Lock()
	f.texts = append(f.texts, req.Messages[0].Content[0].Text)
	answer := f.answer
	f.mu.Unlock()
	out, _ := json.Marshal(map[string]any{
		"content":     []map[string]string{{"type": "text", "text": answer}},
		"stop_reason": "end_turn",
		"usage":       map[string]int{"input_tokens": 400, "output_tokens": 60},
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

type parsed struct {
	Understood        bool     `json:"understood"`
	NotFinance        bool     `json:"notFinance"`
	Source            string   `json:"source"`
	Amount            *float64 `json:"amount"`
	Currency          *string  `json:"currency"`
	SpentAt           *string  `json:"spentAt"`
	Installments      *int     `json:"installments"`
	Description       *string  `json:"description"`
	PaymentMethodID   *string  `json:"paymentMethodId"`
	PaymentCandidates []string `json:"paymentCandidates"`
	Shared            bool     `json:"shared"`
	CategoryID        *string  `json:"categoryId"`
	CategorySource    *string  `json:"categorySource"`
}

func TestParseSentence(t *testing.T) {
	t.Parallel()
	f := &fakeClaude{}
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
	santander := s.CreateCreditCard(t, u, "Visa Santander", 20, 2, "2026-11-20", "2026-12-02")

	parse := func(t *testing.T, text string) parsed {
		t.Helper()
		var out parsed
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/expenses/parse", Token: u.Token, Household: h.ID,
			Body: map[string]any{"text": text}}).Status(t, http.StatusOK).JSON(t, &out)
		return out
	}
	aiRows := func(t *testing.T) int {
		t.Helper()
		var n int
		if err := s.Pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM ai_usage WHERE user_id = $1 AND task = 'parse_expense'`, u.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	t.Run("rules read it all", func(t *testing.T) {
		got := parse(t, "zapatillas 120 mil en 6 cuotas visa galicia")
		if !got.Understood || got.Source != "rules" || *got.Amount != 120000 || *got.Installments != 6 ||
			*got.PaymentMethodID != galicia.ID || *got.Description != "Zapatillas" || got.Currency != nil || got.SpentAt != nil {
			t.Fatalf("got %+v", got)
		}
		if n := aiRows(t); n != 0 {
			t.Errorf("AI called %d times", n)
		}
	})

	t.Run("a bare network picks nothing", func(t *testing.T) {
		got := parse(t, "café 2500 visa")
		if got.PaymentMethodID != nil || len(got.PaymentCandidates) != 2 ||
			!strings.Contains(strings.Join(got.PaymentCandidates, ","), santander.ID) {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("the AI reads an amount in words", func(t *testing.T) {
		f.mu.Lock()
		f.answer = `{"intent":"expense","amount":2500,"currency":"","date":"","installments":0,"payment":"visa santander","description":"café","shared":false}`
		f.mu.Unlock()
		got := parse(t, "dos mil quinientos de café con visa santander")
		if !got.Understood || got.Source != "ai" || *got.Amount != 2500 || *got.PaymentMethodID != santander.ID || *got.Description != "Café" {
			t.Fatalf("got %+v", got)
		}
		if n := aiRows(t); n != 1 {
			t.Errorf("ai_usage rows %d", n)
		}
		// Card names never leave: the matching is ours.
		if text := f.texts[len(f.texts)-1]; strings.Contains(text, "Galicia") {
			t.Errorf("sent %s", text)
		}
		// Same sentence again: from the cache.
		parse(t, "dos mil quinientos de café con visa santander")
		if n := aiRows(t); n != 1 {
			t.Errorf("ai_usage rows %d after a repeat", n)
		}
	})

	t.Run("not an expense", func(t *testing.T) {
		f.mu.Lock()
		f.answer = `{"intent":"not_finance","amount":0,"currency":"","date":"","installments":0,"payment":"","description":"","shared":false}`
		f.mu.Unlock()
		if got := parse(t, "escribime un poema"); !got.NotFinance || got.Understood || got.Description != nil {
			t.Fatalf("got %+v", got)
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

	t.Run("too long, or not a member", func(t *testing.T) {
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/expenses/parse", Token: u.Token, Household: h.ID,
			Body: map[string]any{"text": strings.Repeat("a", 301)}}).Status(t, http.StatusUnprocessableEntity)
		other := s.Register(t)
		res := s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/expenses/parse", Token: other.Token, Household: h.ID,
			Body: map[string]any{"text": "café 2500"}})
		if res.Code == http.StatusOK {
			t.Errorf("a stranger got 200")
		}
	})
}
