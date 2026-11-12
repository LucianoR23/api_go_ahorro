package insights

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/money"
	"github.com/LucianoR23/valuo_api/internal/notifprefs"
	"github.com/LucianoR23/valuo_api/internal/summaries"
)

// AI summaries (plan 8.2): the weekly review and the monthly review keep
// their rule-based text, and the AI rewrites it from the same figures when
// it can. One summary per space, seen by every member; its AI cost counts
// against the space owner.

// SummaryWriter writes a summary's paragraph, or returns false to keep the
// rule-based text. *summaries.Writer implements it.
type SummaryWriter interface {
	Weekly(ctx context.Context, sp summaries.Space, f summaries.WeeklyFacts) (string, bool)
	Monthly(ctx context.Context, sp summaries.Space, f summaries.MonthlyFacts) (string, bool)
}

// SpaceMembersAdapter returns a space's owner and all its members.
type SpaceMembersAdapter func(ctx context.Context, householdID uuid.UUID) (owner uuid.UUID, members []uuid.UUID, err error)

// MonthNumbers are a space's figures for one month, in its base currency.
type MonthNumbers struct {
	Currency   string
	Spent      decimal.Decimal
	Income     decimal.Decimal
	FixedPct   float64
	Categories []MonthCategory // largest first
}

// MonthCategory is one category's spending in a month.
type MonthCategory struct {
	Name  string
	Total decimal.Decimal
	Pct   float64 // of the month's spending
}

// MonthNumbersAdapter computes MonthNumbers (app.go wraps the reports
// service, so insights does not import it).
type MonthNumbersAdapter func(ctx context.Context, householdID uuid.UUID, month time.Time) (MonthNumbers, error)

// SetSummaries wires the AI writer, the members lookup (owner for the AI
// cost, everyone for the weekly push) and the month figures. Without it the
// weekly review stays rule-based and in-app only, and there is no monthly
// review.
func (s *Service) SetSummaries(w SummaryWriter, members SpaceMembersAdapter, months MonthNumbersAdapter) {
	s.writer, s.members, s.months = w, members, months
}

// topCategoriesInSummary: how many categories the facts name.
const topCategoriesInSummary = 3

var monthNames = [...]string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio",
	"agosto", "septiembre", "octubre", "noviembre", "diciembre"}

func monthName(t time.Time) string { return monthNames[t.Month()-1] }

// pct writes a whole percentage: "38%".
func pct(v float64) string { return fmt.Sprintf("%.0f%%", v) }

// change writes how now compares with before: "20% más", "15% menos",
// "igual". Empty when there is nothing to compare with.
func change(now, before decimal.Decimal) string {
	if before.Sign() <= 0 {
		return ""
	}
	p := now.Sub(before).Div(before).Mul(decimal.NewFromInt(100)).Round(0)
	switch p.Sign() {
	case 0:
		return "igual"
	case -1:
		return p.Neg().String() + "% menos"
	}
	return p.String() + "% más"
}

// owner returns the space owner, or false without the members lookup.
func (s *Service) owner(ctx context.Context, householdID uuid.UUID) (uuid.UUID, []uuid.UUID, bool) {
	if s.members == nil {
		return uuid.Nil, nil, false
	}
	owner, members, err := s.members(ctx, householdID)
	if err != nil {
		s.logWarn(ctx, "space members lookup falló", "householdId", householdID.String(), "error", err)
		return uuid.Nil, nil, false
	}
	return owner, members, true
}

// weeklyAIBody asks the writer for the week's paragraph.
func (s *Service) weeklyAIBody(ctx context.Context, householdID, owner uuid.UUID, thisWeek, prev decimal.Decimal, counts SpentCounts, monthDue decimal.Decimal, cur string, from, to time.Time) (string, bool) {
	if s.writer == nil {
		return "", false
	}
	cats, err := s.repo.TopCategories(ctx, householdID, from, to, topCategoriesInSummary)
	if err != nil {
		s.logWarn(ctx, "weekly top categories falló", "householdId", householdID.String(), "error", err)
		return "", false
	}
	f := summaries.WeeklyFacts{
		SpentThisWeek: money.Format(thisWeek, cur),
		Movements:     counts.Total,
		Categories:    counts.DistinctCategories,
		Change:        change(thisWeek, prev),
	}
	if prev.Sign() > 0 {
		f.SpentLastWeek = money.Format(prev, cur)
	}
	if monthDue.Sign() > 0 {
		f.StillDue = money.Format(monthDue, cur)
	}
	for _, c := range cats {
		cat := summaries.Category{Name: c.Name, Amount: money.Format(c.Total, cur)}
		if thisWeek.Sign() > 0 {
			cat.Share = pct(c.Total.Div(thisWeek).Mul(decimal.NewFromInt(100)).InexactFloat64())
		}
		f.TopCategories = append(f.TopCategories, cat)
	}
	return s.writer.Weekly(ctx, summaries.Space{HouseholdID: householdID, OwnerID: owner}, f)
}

// genMonthlyReview: on the 1st, the summary of the month that just ended
// (plan 8.2), as an in-app notice for the whole space. The monthly email
// reuses its paragraph when the AI wrote it. A space with no spending and
// no income that month gets none.
func (s *Service) genMonthlyReview(ctx context.Context, householdID uuid.UUID, at time.Time) (bool, error) {
	if s.months == nil || at.Day() != 1 {
		return false, nil
	}
	day := dayStart(at)
	if done, err := s.repo.Exists(ctx, householdID, day, domain.InsightTypeMonthlyReview); err != nil || done {
		return false, err
	}
	month := day.AddDate(0, -1, 0)
	nums, err := s.months(ctx, householdID, month)
	if err != nil {
		return false, err
	}
	if nums.Spent.Sign() == 0 && nums.Income.Sign() == 0 {
		return false, nil
	}
	before, err := s.months(ctx, householdID, month.AddDate(0, -1, 0))
	if err != nil {
		return false, err
	}

	cur, name := nums.Currency, monthName(month)
	body := monthlyBody(nums, before, name, monthName(month.AddDate(0, -1, 0)))
	meta := map[string]any{
		"month":        month.Format("2006-01"),
		"spent":        nums.Spent,
		"spent_prev":   before.Spent,
		"income":       nums.Income,
		"written_by":   "rules",
		"top_category": "",
	}
	if len(nums.Categories) > 0 {
		meta["top_category"] = nums.Categories[0].Name
	}
	if owner, _, ok := s.owner(ctx, householdID); ok && s.writer != nil {
		f := summaries.MonthlyFacts{
			Month: name, Spent: money.Format(nums.Spent, cur),
			Change: change(nums.Spent, before.Spent),
		}
		if before.Spent.Sign() > 0 {
			f.SpentLastMonth = money.Format(before.Spent, cur)
		}
		if nums.Income.Sign() > 0 {
			f.Income = money.Format(nums.Income, cur)
		}
		if nums.Spent.Sign() > 0 && nums.FixedPct > 0 {
			f.FixedShare = pct(nums.FixedPct)
		}
		for i, c := range nums.Categories {
			if i == topCategoriesInSummary {
				break
			}
			f.TopCategories = append(f.TopCategories, summaries.Category{Name: c.Name, Amount: money.Format(c.Total, cur), Share: pct(c.Pct)})
		}
		if text, ok := s.writer.Monthly(ctx, summaries.Space{HouseholdID: householdID, OwnerID: owner}, f); ok {
			body, meta["written_by"] = text, "ai"
		}
	}

	ins, created, err := s.repo.Create(ctx, CreateParams{
		HouseholdID: householdID,
		InsightDate: day,
		InsightType: domain.InsightTypeMonthlyReview,
		Title:       fmt.Sprintf("Tu %s: %s", name, money.Format(nums.Spent, cur)),
		Body:        body,
		Severity:    domain.InsightSeverityInfo,
		Metadata:    meta,
	})
	if created {
		s.publish(ctx, ins)
	}
	return created, err
}

// monthlyBody is the rule-based text of the monthly review.
func monthlyBody(nums, before MonthNumbers, name, prevName string) string {
	cur := nums.Currency
	body := fmt.Sprintf("En %s gastaste %s", name, money.Format(nums.Spent, cur))
	if c := change(nums.Spent, before.Spent); c != "" && c != "igual" {
		body += fmt.Sprintf(", %s que en %s", c, prevName)
	}
	body += "."
	if len(nums.Categories) > 0 {
		c := nums.Categories[0]
		body += fmt.Sprintf(" Lo que más pesó: %s (%s, %s).", c.Name, money.Format(c.Total, cur), pct(c.Pct))
	}
	if nums.Income.Sign() > 0 {
		body += fmt.Sprintf(" Ingresaron %s.", money.Format(nums.Income, cur))
	}
	return body
}

// pushWeekly tells every member that the week's summary is ready. The lock
// screen never shows the amount (plan 1.9).
func (s *Service) pushWeekly(ctx context.Context, ins domain.DailyInsight, members []uuid.UUID) {
	if s.push == nil || len(members) == 0 {
		return
	}
	s.push.NotifyUsersWithAmounts(ctx, members, notifprefs.WeeklyReview,
		"Tu semana", "Ya está el resumen de tu semana.", ins.Body,
		"/notificaciones", "weekly_review:"+ins.HouseholdID.String()+":"+ins.InsightDate.Format(time.DateOnly))
}
