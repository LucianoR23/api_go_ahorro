package admin_test

// AI spend section of the superadmin panel (plan 8.1, §8.6).

import (
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

type aiPanel struct {
	Enabled bool `json:"enabled"`
	Limits  struct {
		DailyBudget int64 `json:"dailyBudgetMicroUsd"`
		UserDaily   int64 `json:"userDailyMicroUsd"`
		PerMinute   int   `json:"perMinute"`
		PerDay      int   `json:"perDay"`
	} `json:"limits"`
	Today struct {
		Cost         int64 `json:"costMicroUsd"`
		Calls        int32 `json:"calls"`
		Errors       int32 `json:"errors"`
		Blocked      int32 `json:"blocked"`
		AvgLatencyMs int32 `json:"avgLatencyMs"`
	} `json:"today"`
	Month struct {
		Cost  int64 `json:"costMicroUsd"`
		Calls int32 `json:"calls"`
	} `json:"month"`
	ByTask []struct {
		Task    string `json:"task"`
		Calls   int32  `json:"calls"`
		Errors  int32  `json:"errors"`
		Blocked int32  `json:"blocked"`
		Cost    int64  `json:"costMicroUsd"`
	} `json:"byTask"`
	TopUsers []struct {
		UserID  string `json:"userId"`
		Email   string `json:"email"`
		Calls   int32  `json:"calls"`
		Blocked int32  `json:"blocked"`
		Cost    int64  `json:"costMicroUsd"`
	} `json:"topUsers"`
}

func TestAIPanel(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	admin := s.Register(t)
	s.MakeSuperadmin(t, admin)
	ana := s.Register(t, apitest.RegisterOpts{Email: "ana.ia@example.com"})
	bob := s.Register(t, apitest.RegisterOpts{Email: "bob.ia@example.com"})

	var empty aiPanel
	get(t, s, admin, "/admin/ai", &empty)
	if empty.Enabled || empty.Today.Calls != 0 || len(empty.ByTask) != 0 || len(empty.TopUsers) != 0 {
		t.Errorf("empty panel = %+v", empty)
	}
	if empty.Limits.DailyBudget != 3_000_000 || empty.Limits.UserDaily != 50_000 || empty.Limits.PerMinute != 10 || empty.Limits.PerDay != 100 {
		t.Errorf("limits = %+v", empty.Limits)
	}

	_, err := s.Pool.Exec(t.Context(), `
		INSERT INTO ai_usage (user_id, task, channel, provider, model, status, cost_micro_usd, latency_ms, created_at) VALUES
		($1, 'parse_expense', 'app', 'anthropic', 'claude-haiku-5-5', 'ok', 125, 800, NOW()),
		($1, 'parse_expense', 'app', 'anthropic', 'claude-haiku-5-5', 'invalid_output', 50, 900, NOW()),
		($1, 'parse_audio', 'telegram', 'gemini', 'gemini-3.5-flash-lite', 'ok', 430, 1200, NOW()),
		($2, 'parse_expense', 'app', '', '', 'rate_limited', 0, 0, NOW()),
		($2, 'summarize', 'system', 'anthropic', 'claude-haiku-5-5', 'ok', 9999, 500,
		 date_trunc('month', NOW() AT TIME ZONE 'America/Argentina/Buenos_Aires') AT TIME ZONE 'America/Argentina/Buenos_Aires' - INTERVAL '1 day')`,
		ana.ID, bob.ID)
	if err != nil {
		t.Fatal(err)
	}

	var p aiPanel
	get(t, s, admin, "/admin/ai", &p)
	if p.Today.Cost != 605 || p.Today.Calls != 3 || p.Today.Errors != 1 || p.Today.Blocked != 1 || p.Today.AvgLatencyMs != 1000 {
		t.Errorf("today = %+v", p.Today)
	}
	// Last month's summary is left out.
	if p.Month.Cost != 605 || p.Month.Calls != 3 {
		t.Errorf("month = %+v", p.Month)
	}
	if len(p.ByTask) != 2 || p.ByTask[0].Task != "parse_audio" || p.ByTask[0].Cost != 430 ||
		p.ByTask[1].Task != "parse_expense" || p.ByTask[1].Calls != 2 || p.ByTask[1].Errors != 1 || p.ByTask[1].Blocked != 1 {
		t.Errorf("byTask = %+v", p.ByTask)
	}
	if len(p.TopUsers) != 2 || p.TopUsers[0].Email != "ana.ia@example.com" || p.TopUsers[0].Cost != 605 || p.TopUsers[0].Calls != 3 ||
		p.TopUsers[1].Email != "bob.ia@example.com" || p.TopUsers[1].Blocked != 1 || p.TopUsers[1].Calls != 0 {
		t.Errorf("topUsers = %+v", p.TopUsers)
	}
}
