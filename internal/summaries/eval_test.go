//go:build aismoke

package summaries

// Eval set of the AI summaries (plan 8.2), against the real provider. Never
// runs in CI: it needs the build tag and a real key, and spends about a
// tenth of a cent per run. Run it after changing a prompt or a model:
//
//	ANTHROPIC_API_KEY=... go test -tags aismoke -run Eval -v ./internal/summaries/
//
// Each case must give one plain paragraph that passes Validate, copies
// only numbers from the facts and mentions the required figures.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/ai"
	"github.com/LucianoR23/valuo_api/internal/config"
)

type evalCase struct {
	Name           string       `json:"name"`
	Kind           string       `json:"kind"`
	Weekly         WeeklyFacts  `json:"weekly"`
	Monthly        MonthlyFacts `json:"monthly"`
	MustMention    []string     `json:"mustMention"`
	MustNotMention []string     `json:"mustNotMention"`
}

func TestEvalSummaries(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	raw, err := os.ReadFile("testdata/eval/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []evalCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	routes, _ := ai.ParseRoutes(config.DefaultAIRoutes)
	r, ok := routes[string(ai.TaskSummarize)]
	if !ok {
		t.Fatal("no summarize route")
	}
	provider := ai.NewAnthropic(key, "", nil)
	w := New(nil, nil)

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			p, facts := w.weekly, any(c.Weekly)
			if c.Kind == "monthly" {
				p, facts = w.monthly, c.Monthly
			}
			data, _ := json.Marshal(facts)
			out, err := provider.Generate(context.Background(), ai.Call{
				Model: r.Model, Effort: r.Effort, Thinking: r.Thinking, System: p.System,
				Parts:  []ai.Part{ai.TextPart("<facts>" + string(data) + "</facts>")},
				Schema: schema, MaxOutputTokens: maxOutputTokens,
			})
			if err != nil {
				t.Fatal(err)
			}
			var a answer
			if err := json.Unmarshal([]byte(out.Text), &a); err != nil || out.Truncated {
				t.Fatalf("answer %q: %v (truncated %v)", out.Text, err, out.Truncated)
			}
			if err := a.Validate(); err != nil {
				t.Fatalf("%q: %v", a.Body, err)
			}
			if bad := strangeNumbers(a.Body, string(data)); len(bad) > 0 {
				t.Errorf("%q has numbers not in the facts: %v", a.Body, bad)
			}
			low := strings.ToLower(a.Body)
			for _, m := range c.MustMention {
				if !strings.Contains(low, strings.ToLower(m)) {
					t.Errorf("%q does not mention %q", a.Body, m)
				}
			}
			for _, m := range c.MustNotMention {
				if strings.Contains(low, strings.ToLower(m)) {
					t.Errorf("%q mentions %q", a.Body, m)
				}
			}
			t.Logf("%s (%d in / %d out tokens)", a.Body, out.InputTokens, out.OutputTokens)
		})
	}
}
