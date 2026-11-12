package ai

import (
	"testing"

	"github.com/LucianoR23/valuo_api/internal/config"
)

func TestParseRoutes(t *testing.T) {
	t.Parallel()
	got, err := ParseRoutes(" default=anthropic:claude-haiku-5-5:low , parse_audio=gemini:gemini-3.5-flash-lite:minimal,summarize=anthropic:claude-sonnet-5-5:medium:between_tools")
	if err != nil {
		t.Fatal(err)
	}
	if got["default"] != (Route{Provider: "anthropic", Model: "claude-haiku-5-5", Effort: "low"}) {
		t.Errorf("default = %+v", got["default"])
	}
	if got["summarize"].Thinking != "between_tools" || got["parse_audio"].Provider != "gemini" {
		t.Errorf("routes = %+v", got)
	}

	for _, bad := range []string{
		"",
		"parse_audio=gemini:x", // no default
		"default=openai:gpt",   // unknown provider
		"default=anthropic",    // no model
		"default=anthropic:m,default=anthropic:m", // twice
		"default=anthropic:m:low:disabled:extra",  // too many fields
		"=anthropic:m",                            // no task
	} {
		if _, err := ParseRoutes(bad); err == nil {
			t.Errorf("ParseRoutes(%q) accepted", bad)
		}
	}
}

func TestParsePricesAndCost(t *testing.T) {
	t.Parallel()
	p, err := ParsePrices("claude-haiku-5-5=0.10/0.50, gemini-3.1-flash-lite=0.25/1.50/0.50")
	if err != nil {
		t.Fatal(err)
	}
	// 500 input and 150 output tokens on Haiku: 50 + 75 = 125 micro-USD.
	if got := p["claude-haiku-5-5"].Cost(Output{InputTokens: 500, OutputTokens: 150}); got != 125 {
		t.Errorf("haiku cost = %d, want 125", got)
	}
	// 960 audio tokens (30 s) + 200 text in, 100 out on 3.1 Flash-Lite:
	// 200*0.25 + 960*0.50 + 100*1.50 = 50 + 480 + 150 = 680.
	if got := p["gemini-3.1-flash-lite"].Cost(Output{InputTokens: 1160, AudioTokens: 960, OutputTokens: 100}); got != 680 {
		t.Errorf("gemini cost = %d, want 680", got)
	}
	// A fraction of a micro-dollar rounds up, never to free.
	if got := p["claude-haiku-5-5"].Cost(Output{InputTokens: 1}); got != 1 {
		t.Errorf("tiny cost = %d, want 1", got)
	}
	for _, bad := range []string{"m=1", "m=a/b", "m=-1/2", "=1/2", "m=1/2/3/4"} {
		if _, err := ParsePrices(bad); err == nil {
			t.Errorf("ParsePrices(%q) accepted", bad)
		}
	}
}

// The shipped defaults must parse and price every route, or the API would
// not start.
func TestDefaultsAreConsistent(t *testing.T) {
	t.Parallel()
	routes, err := ParseRoutes(config.DefaultAIRoutes)
	if err != nil {
		t.Fatal(err)
	}
	prices, err := ParsePrices(config.DefaultAIPrices)
	if err != nil {
		t.Fatal(err)
	}
	for task, r := range routes {
		if _, ok := prices[r.Model]; !ok {
			t.Errorf("%s: no price for %s", task, r.Model)
		}
	}
	if routes["parse_audio"].Provider != "gemini" || routes["default"].Model != "claude-haiku-5-5" {
		t.Errorf("defaults changed: %+v", routes)
	}
}

func TestMicroUSD(t *testing.T) {
	t.Parallel()
	if MicroUSD(3) != 3_000_000 || MicroUSD(0.05) != 50_000 {
		t.Errorf("MicroUSD wrong: %d %d", MicroUSD(3), MicroUSD(0.05))
	}
}
