package ai

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Route says which provider and model serve a task.
//
// AI_ROUTES syntax: comma-separated "task=provider:model[:effort[:thinking]]"
// entries, with "default" for every task not listed. Effort is the
// provider's own level (Claude: low, medium, high; Gemini: minimal, low,
// medium, high). Thinking applies to Claude only: disabled (the default),
// adaptive or between_tools, as each model accepts.
type Route struct {
	Provider string
	Model    string
	Effort   string
	Thinking string
}

const defaultRoute = "default"

// ParseRoutes reads AI_ROUTES. A "default" entry is required.
func ParseRoutes(s string) (map[string]Route, error) {
	out := map[string]Route{}
	for _, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		task, spec, ok := strings.Cut(entry, "=")
		task = strings.TrimSpace(task)
		if !ok || task == "" {
			return nil, fmt.Errorf("AI_ROUTES: %q needs task=provider:model", entry)
		}
		f := strings.Split(strings.TrimSpace(spec), ":")
		if len(f) < 2 || len(f) > 4 || f[0] == "" || f[1] == "" {
			return nil, fmt.Errorf("AI_ROUTES: %q needs provider:model[:effort[:thinking]]", entry)
		}
		r := Route{Provider: f[0], Model: f[1]}
		if len(f) > 2 {
			r.Effort = f[2]
		}
		if len(f) > 3 {
			r.Thinking = f[3]
		}
		if r.Provider != "anthropic" && r.Provider != "gemini" {
			return nil, fmt.Errorf("AI_ROUTES: unknown provider %q", r.Provider)
		}
		if _, dup := out[task]; dup {
			return nil, fmt.Errorf("AI_ROUTES: %q appears twice", task)
		}
		out[task] = r
	}
	if _, ok := out[defaultRoute]; !ok {
		return nil, fmt.Errorf("AI_ROUTES: a %q entry is required", defaultRoute)
	}
	return out, nil
}

// Price is what a model costs in dollars per million tokens. Audio is the
// audio input price; zero means the same as In.
type Price struct {
	In, Out, Audio float64
}

// ParsePrices reads AI_PRICES: comma-separated "model=in/out[/audio]"
// entries in USD per million tokens.
func ParsePrices(s string) (map[string]Price, error) {
	out := map[string]Price{}
	for _, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		model, spec, ok := strings.Cut(entry, "=")
		model = strings.TrimSpace(model)
		if !ok || model == "" {
			return nil, fmt.Errorf("AI_PRICES: %q needs model=in/out", entry)
		}
		f := strings.Split(strings.TrimSpace(spec), "/")
		if len(f) < 2 || len(f) > 3 {
			return nil, fmt.Errorf("AI_PRICES: %q needs in/out[/audio]", entry)
		}
		nums := make([]float64, len(f))
		for i, v := range f {
			n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil || n < 0 || math.IsInf(n, 0) || math.IsNaN(n) {
				return nil, fmt.Errorf("AI_PRICES: %q has a bad number %q", entry, v)
			}
			nums[i] = n
		}
		p := Price{In: nums[0], Out: nums[1]}
		if len(nums) == 3 {
			p.Audio = nums[2]
		}
		out[model] = p
	}
	return out, nil
}

// Cost is the price of one answer in millionths of a dollar.
func (p Price) Cost(o Output) int64 {
	audio := p.Audio
	if audio == 0 {
		audio = p.In
	}
	text := o.InputTokens - o.AudioTokens
	if text < 0 {
		text = 0
	}
	// tokens × USD per million tokens = micro-USD.
	micro := float64(text)*p.In + float64(o.AudioTokens)*audio + float64(o.OutputTokens)*p.Out
	return int64(math.Ceil(micro))
}
