package categories_test

// POST /categories/suggest (plan 8.4): history first, AI only when needed.

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

// fakeClaude answers like the Messages API with a fixed category and keeps
// the user messages it got.
type fakeClaude struct {
	mu       sync.Mutex
	category string
	texts    []string
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
	f.mu.Unlock()
	answer, _ := json.Marshal(map[string]any{"match": f.category != "", "category": f.category})
	out, _ := json.Marshal(map[string]any{
		"content":     []map[string]string{{"type": "text", "text": string(answer)}},
		"stop_reason": "end_turn",
		"usage":       map[string]int{"input_tokens": 300, "output_tokens": 10},
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

type suggestion struct {
	CategoryID *string `json:"categoryId"`
	Source     *string `json:"source"`
}

func TestSuggestCategory(t *testing.T) {
	t.Parallel()
	f := &fakeClaude{category: "Salud"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	s := apitest.New(t, apitest.WithConfig(func(c *config.Config) {
		c.AIEnabled = true
		c.AnthropicAPIKey = "sk-test"
		c.AnthropicBaseURL = srv.URL
	}))
	owner := s.Register(t, apitest.RegisterOpts{FirstName: "Owner", Verify: true})
	h := s.CreateHousehold(t, owner, "")
	member := s.AddMember(t, owner, h.ID)

	var cats []categoryJSON
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Token: owner.Token, Household: h.ID}).
		Status(t, http.StatusOK).JSON(t, &cats)
	byName := map[string]string{}
	for _, c := range cats {
		byName[c.Name] = c.ID
	}

	suggest := func(t *testing.T, u apitest.User, desc string) suggestion {
		t.Helper()
		var out suggestion
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/categories/suggest", Token: u.Token, Household: h.ID,
			Body: map[string]any{"description": desc}}).Status(t, http.StatusOK).JSON(t, &out)
		return out
	}
	aiRows := func(t *testing.T) (n int, user string) {
		t.Helper()
		err := s.Pool.QueryRow(context.Background(),
			`SELECT COUNT(*), COALESCE(MAX(user_id::text), '') FROM ai_usage WHERE household_id = $1 AND task = 'categorize'`, h.ID).
			Scan(&n, &user)
		if err != nil {
			t.Fatal(err)
		}
		return n, user
	}

	cash := s.Cash(t, owner)
	s.CreateExpense(t, owner, h.ID, map[string]any{
		"amount": 3200, "spentAt": "2026-03-03", "paymentMethodId": cash.ID,
		"description": "Coto", "categoryId": byName["Súper"],
	})

	t.Run("history answers without the AI", func(t *testing.T) {
		got := suggest(t, member, "  COTO ")
		if got.CategoryID == nil || *got.CategoryID != byName["Súper"] || *got.Source != "history" {
			t.Fatalf("got %+v", got)
		}
		if n, _ := aiRows(t); n != 0 {
			t.Errorf("AI called %d times", n)
		}
	})

	t.Run("a new description asks the AI, billed to who asks", func(t *testing.T) {
		got := suggest(t, member, "Farmacity ibuprofeno")
		if got.CategoryID == nil || *got.CategoryID != byName["Salud"] || *got.Source != "ai" {
			t.Fatalf("got %+v", got)
		}
		if n, user := aiRows(t); n != 1 || user != member.ID {
			t.Errorf("ai_usage rows %d, user %s", n, user)
		}
		// Only the description and the category names go out.
		text := f.texts[len(f.texts)-1]
		if !strings.HasPrefix(text, `<description>"Farmacity ibuprofeno"</description>`) || strings.Contains(text, "3200") ||
			strings.Contains(text, owner.Email) {
			t.Errorf("sent %s", text)
		}
	})

	t.Run("no confident source gives null", func(t *testing.T) {
		f.mu.Lock()
		f.category = ""
		f.mu.Unlock()
		if got := suggest(t, owner, "Cosas varias"); got.CategoryID != nil || got.Source != nil {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("too long, or not a member", func(t *testing.T) {
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/categories/suggest", Token: owner.Token, Household: h.ID,
			Body: map[string]any{"description": strings.Repeat("a", 501)}}).Status(t, http.StatusUnprocessableEntity)
		other := s.Register(t)
		res := s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/categories/suggest", Token: other.Token, Household: h.ID,
			Body: map[string]any{"description": "Coto"}})
		if res.Code == http.StatusOK {
			t.Errorf("a stranger got 200")
		}
	})
}
