package notifprefs_test

// Notification preferences (plan 10.1).

import (
	"net/http"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

const path = "/me/notification-preferences"

type prefsJSON struct {
	Kinds []struct {
		Kind     string          `json:"kind"`
		Channels map[string]bool `json:"channels"`
	} `json:"kinds"`
	QuietHours struct {
		Enabled bool   `json:"enabled"`
		Start   string `json:"start"`
		End     string `json:"end"`
	} `json:"quietHours"`
	Timezone string `json:"timezone"`
}

func (p prefsJSON) channel(kind, ch string) (on, ok bool) {
	for _, k := range p.Kinds {
		if k.Kind == kind {
			on, ok = k.Channels[ch]
			return on, ok
		}
	}
	return false, false
}

func get(t *testing.T, s *apitest.Server, u apitest.User) prefsJSON {
	t.Helper()
	var p prefsJSON
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: path, Token: u.Token}).Status(t, http.StatusOK).JSON(t, &p)
	return p
}

func put(t *testing.T, s *apitest.Server, u apitest.User, body map[string]any) *apitest.Res {
	t.Helper()
	return s.Do(t, apitest.Req{Method: http.MethodPut, Path: path, Token: u.Token, Body: body})
}

func quiet(enabled bool, start, end string) map[string]any {
	return map[string]any{"enabled": enabled, "start": start, "end": end}
}

func TestPreferences(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("defaults: everything on, quiet from 23 to 8", func(t *testing.T) {
		u := s.Register(t)
		p := get(t, s, u)
		if len(p.Kinds) != 8 || p.Timezone != "America/Argentina/Buenos_Aires" {
			t.Errorf("prefs = %+v", p)
		}
		for _, k := range p.Kinds {
			for ch, on := range k.Channels {
				if !on {
					t.Errorf("%s.%s starts off", k.Kind, ch)
				}
			}
		}
		if q := p.QuietHours; !q.Enabled || q.Start != "23:00" || q.End != "08:00" {
			t.Errorf("quiet hours = %+v", q)
		}
		// Only the channels each kind really uses.
		if _, ok := p.channel("shared_expense", "email"); ok {
			t.Error("shared_expense offers email")
		}
		if _, ok := p.channel("monthly_report", "email"); !ok {
			t.Error("monthly_report does not offer email")
		}
	})

	t.Run("saves muted channels and quiet hours, per person", func(t *testing.T) {
		u, other := s.Register(t), s.Register(t)
		var saved prefsJSON
		put(t, s, u, map[string]any{
			"kinds": []map[string]any{
				{"kind": "shared_expense", "channels": map[string]bool{"push": false, "in_app": true}},
				{"kind": "monthly_report", "channels": map[string]bool{"email": false}},
			},
			"quietHours": quiet(false, "22:30", "07:15"),
		}).Status(t, http.StatusOK).JSON(t, &saved)

		for _, p := range []prefsJSON{saved, get(t, s, u)} {
			if on, _ := p.channel("shared_expense", "push"); on {
				t.Error("shared_expense.push still on")
			}
			if on, _ := p.channel("shared_expense", "in_app"); !on {
				t.Error("shared_expense.in_app turned off")
			}
			if on, _ := p.channel("monthly_report", "email"); on {
				t.Error("monthly_report.email still on")
			}
			if q := p.QuietHours; q.Enabled || q.Start != "22:30" || q.End != "07:15" {
				t.Errorf("quiet hours = %+v", q)
			}
		}
		if on, _ := get(t, s, other).channel("shared_expense", "push"); !on {
			t.Error("someone else's preferences changed")
		}

		// A kind left out of the body is on again.
		put(t, s, u, map[string]any{"quietHours": quiet(true, "23:00", "08:00")}).Status(t, http.StatusOK)
		if on, _ := get(t, s, u).channel("shared_expense", "push"); !on {
			t.Error("shared_expense.push stayed off")
		}
	})

	t.Run("validation", func(t *testing.T) {
		u := s.Register(t)
		ok := quiet(true, "23:00", "08:00")
		put(t, s, u, map[string]any{"kinds": []map[string]any{{"kind": "shared_expense", "channels": map[string]bool{"email": false}}}, "quietHours": ok}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "kinds")
		put(t, s, u, map[string]any{"kinds": []map[string]any{{"kind": "invite", "channels": map[string]bool{"push": false}}}, "quietHours": ok}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "kinds")
		put(t, s, u, map[string]any{"quietHours": quiet(true, "8", "08:00")}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "quietHours.start")
		put(t, s, u, map[string]any{"quietHours": quiet(true, "08:00", "25:00")}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "quietHours.end")
		put(t, s, u, map[string]any{"quietHours": quiet(true, "08:00", "08:00")}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "quietHours.end")
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: path}).Status(t, http.StatusUnauthorized)
	})
}

func TestMutedInAppNotices(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	owner := s.Register(t)
	hh := s.CreateHousehold(t, owner, "Casa")
	member := s.AddMember(t, owner, hh.ID)
	cash := s.Cash(t, owner)
	s.CreateExpense(t, owner, hh.ID, map[string]any{"paymentMethodId": cash.ID, "amount": 1000,
		"spentAt": "2026-11-03", "isShared": true})

	notices := func(u apitest.User) (types []string, unread int64) {
		var list struct {
			Items []struct {
				InsightType string `json:"insightType"`
			} `json:"items"`
			TotalCount int64 `json:"totalCount"`
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/insights", Token: u.Token, Household: hh.ID}).
			Status(t, http.StatusOK).JSON(t, &list)
		for _, it := range list.Items {
			types = append(types, it.InsightType)
		}
		var c struct {
			Unread int64 `json:"unread"`
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/insights/unread-count", Token: u.Token, Household: hh.ID}).
			Status(t, http.StatusOK).JSON(t, &c)
		if list.TotalCount != int64(len(list.Items)) {
			t.Errorf("total %d for %d items", list.TotalCount, len(list.Items))
		}
		return types, c.Unread
	}
	has := func(types []string, want string) bool {
		for _, t := range types {
			if t == want {
				return true
			}
		}
		return false
	}

	types, unread := notices(member)
	if !has(types, "shared_expense") || unread == 0 {
		t.Fatalf("before muting: %v unread %d", types, unread)
	}

	put(t, s, member, map[string]any{
		"kinds":      []map[string]any{{"kind": "shared_expense", "channels": map[string]bool{"in_app": false}}},
		"quietHours": quiet(true, "23:00", "08:00"),
	}).Status(t, http.StatusOK)
	after, afterUnread := notices(member)
	if has(after, "shared_expense") || afterUnread != unread-1 {
		t.Errorf("after muting: %v unread %d (was %d)", after, afterUnread, unread)
	}

	// Muting only the push keeps the notice in the app.
	put(t, s, member, map[string]any{
		"kinds":      []map[string]any{{"kind": "shared_expense", "channels": map[string]bool{"push": false}}},
		"quietHours": quiet(true, "23:00", "08:00"),
	}).Status(t, http.StatusOK)
	if back, _ := notices(member); !has(back, "shared_expense") {
		t.Errorf("push muted, in-app gone: %v", back)
	}
}
