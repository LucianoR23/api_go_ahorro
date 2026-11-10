package insights

// Characterization tests (plan 0.11) for goal alert copy. They pin today's
// strings, including one thing worth changing later (noted, not fixed here):
// the limit body shows the raw goal type ("category_limit") to the user.
// Money is es-AR since plan 1.10c.

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

func progress(goalType string, current, target float64) domain.BudgetGoalProgress {
	return domain.BudgetGoalProgress{
		Goal:             domain.BudgetGoal{GoalType: goalType},
		CurrentAmount:    decimal.NewFromFloat(current),
		TargetAmount:     decimal.NewFromFloat(target),
		TargetAmountBase: decimal.NewFromFloat(target),
		BaseCurrency:     "ARS",
	}
}

func TestAlertText(t *testing.T) {
	day10 := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	day20 := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		p         domain.BudgetGoalProgress
		at        time.Time
		wantOK    bool
		wantTitle string
		wantBody  string
		wantSev   string
	}{
		{name: "limit under 80% is silent", p: progress(domain.GoalTypeCategoryLimit, 79.99, 100), at: day10},
		{
			name: "limit at 80% warns", p: progress(domain.GoalTypeCategoryLimit, 80, 100), at: day10, wantOK: true,
			wantTitle: "Objetivo al 80%", wantBody: "Llevás $ 80 gastados de $ 100 (category_limit).",
			wantSev: domain.InsightSeverityWarning,
		},
		{
			name: "limit at 100% is critical", p: progress(domain.GoalTypeTotalLimit, 1234.5, 1234.5), at: day10, wantOK: true,
			wantTitle: "Objetivo al 100%", wantBody: "Llevás $ 1.234,50 gastados de $ 1.234,50 (total_limit).",
			wantSev: domain.InsightSeverityCritical,
		},
		{
			name: "limit over 100% stays critical", p: progress(domain.GoalTypeTotalLimit, 150, 100), at: day10, wantOK: true,
			wantTitle: "Objetivo al 150%", wantBody: "Llevás $ 150 gastados de $ 100 (total_limit).",
			wantSev: domain.InsightSeverityCritical,
		},
		{name: "limit with zero target is silent", p: progress(domain.GoalTypeTotalLimit, 50, 0), at: day10},
		{name: "savings before the 20th is silent", p: progress(domain.GoalTypeSavings, 0, 100), at: day10},
		{
			name: "savings from the 20th under 50% warns", p: progress(domain.GoalTypeSavings, 49, 100), at: day20, wantOK: true,
			wantTitle: "Vas atrasado con tu ahorro", wantBody: "Llevás $ 49 ahorrados de $ 100 objetivo este mes.",
			wantSev: domain.InsightSeverityWarning,
		},
		{
			name: "savings can be negative (spent more than earned)", p: progress(domain.GoalTypeSavings, -20, 100), at: day20, wantOK: true,
			wantTitle: "Vas atrasado con tu ahorro", wantBody: "Llevás -$ 20 ahorrados de $ 100 objetivo este mes.",
			wantSev: domain.InsightSeverityWarning,
		},
		{name: "savings at 50% is silent", p: progress(domain.GoalTypeSavings, 50, 100), at: day20},
		{name: "savings with zero target is silent", p: progress(domain.GoalTypeSavings, 0, 0), at: day20},
		{name: "unknown goal type is silent", p: progress("other", 500, 100), at: day20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, body, sev, ok := alertText(tt.p, tt.at)
			if ok != tt.wantOK || title != tt.wantTitle || body != tt.wantBody || sev != tt.wantSev {
				t.Errorf("alertText = (%q, %q, %q, %v)\n want (%q, %q, %q, %v)",
					title, body, sev, ok, tt.wantTitle, tt.wantBody, tt.wantSev, tt.wantOK)
			}
		})
	}
}
