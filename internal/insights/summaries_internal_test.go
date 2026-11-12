package insights

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/notifprefs"
)

func TestChange(t *testing.T) {
	d := decimal.RequireFromString
	for _, c := range []struct{ now, before, want string }{
		{"1500", "1000", "50% más"},
		{"800", "1000", "20% menos"},
		{"1000", "1000", "igual"},
		{"1234", "1000", "23% más"},
		{"500", "0", ""},
	} {
		if got := change(d(c.now), d(c.before)); got != c.want {
			t.Errorf("change(%s, %s) = %q, want %q", c.now, c.before, got, c.want)
		}
	}
}

func TestMonthlyBody(t *testing.T) {
	nums := MonthNumbers{Currency: "ARS", Spent: decimal.NewFromInt(850000), Income: decimal.NewFromInt(1200000),
		Categories: []MonthCategory{{Name: "Súper", Total: decimal.NewFromInt(255000), Pct: 30}}}
	got := monthlyBody(nums, MonthNumbers{Spent: decimal.NewFromInt(1000000)}, "octubre", "septiembre")
	want := "En octubre gastaste $ 850.000, 15% menos que en septiembre. Lo que más pesó: Súper ($ 255.000, 30%). Ingresaron $ 1.200.000."
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

type fakePush struct {
	users                  []uuid.UUID
	kind                   notifprefs.Kind
	title, lock, full, url string
	tag                    string
}

func (f *fakePush) NotifyUsers(context.Context, []uuid.UUID, notifprefs.Kind, string, string, string, string) {
}

func (f *fakePush) NotifyUsersWithAmounts(_ context.Context, users []uuid.UUID, kind notifprefs.Kind, title, body, full, url, tag string) {
	f.users, f.kind, f.title, f.lock, f.full, f.url, f.tag = users, kind, title, body, full, url, tag
}

// The weekly push goes to every member, and the lock screen shows no amount.
func TestPushWeekly(t *testing.T) {
	p := &fakePush{}
	s := &Service{push: p}
	hh, a, b := uuid.New(), uuid.New(), uuid.New()
	s.pushWeekly(context.Background(), domain.DailyInsight{
		HouseholdID: hh, InsightDate: time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC), Body: "Gastaste $ 1.500.",
	}, []uuid.UUID{a, b})
	if len(p.users) != 2 || p.kind != notifprefs.WeeklyReview || p.title != "Tu semana" || strings.Contains(p.lock, "$") ||
		p.full != "Gastaste $ 1.500." || p.url != "/notificaciones" || p.tag != "weekly_review:"+hh.String()+":2026-03-15" {
		t.Errorf("push = %+v", p)
	}
	p.users = nil
	s.pushWeekly(context.Background(), domain.DailyInsight{}, nil)
	if p.users != nil {
		t.Error("pushed to nobody")
	}
}
