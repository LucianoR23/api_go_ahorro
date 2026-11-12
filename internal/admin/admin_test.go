package admin_test

// Superadmin panel (plan 1.11).

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

type overview struct {
	Accounts struct {
		Total, NewWeek, NewMonth, Verified, Active int64
	} `json:"accounts"`
	Households struct {
		Active, Deleted int64
	} `json:"households"`
	Waitlist  int64 `json:"waitlist"`
	Movements []struct {
		Month    string `json:"month"`
		Expenses int64  `json:"expenses"`
		Incomes  int64  `json:"incomes"`
	} `json:"movements"`
}

type userItem struct {
	ID           string  `json:"id"`
	Email        string  `json:"email"`
	VerifiedAt   *string `json:"verifiedAt"`
	LastSeenAt   *string `json:"lastSeenAt"`
	IsSuperadmin bool    `json:"isSuperadmin"`
	Households   int64   `json:"households"`
}

type auditItem struct {
	ID         string  `json:"id"`
	Action     string  `json:"action"`
	UserID     *string `json:"userId"`
	UserEmail  *string `json:"userEmail"`
	ActorEmail *string `json:"actorEmail"`
	Target     string  `json:"target"`
}

type auditPage struct {
	Items      []auditItem `json:"items"`
	NextCursor *string     `json:"nextCursor"`
}

func get(t *testing.T, s *apitest.Server, u apitest.User, path string, v any) {
	t.Helper()
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: path, Token: u.Token}).Status(t, http.StatusOK).JSON(t, v)
}

func TestOnlySuperadmins(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	u := s.Register(t)
	for _, p := range []string{"/admin/overview", "/admin/users", "/admin/users/" + u.ID, "/admin/audit", "/admin/ai"} {
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: p}).Status(t, http.StatusUnauthorized)
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: p, Token: u.Token}).Status(t, http.StatusForbidden)
	}
}

func TestOverview(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	admin := s.Register(t, apitest.RegisterOpts{Verify: true})
	s.MakeSuperadmin(t, admin)
	owner := s.Register(t)
	gone := s.CreateHousehold(t, owner, "Viejo")
	s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/households/" + gone.ID, Token: owner.Token}).Status(t, http.StatusNoContent)
	hh := s.CreateHousehold(t, owner, "Casa")
	s.CreateExpense(t, owner, hh.ID, map[string]any{"paymentMethodId": s.Cash(t, owner).ID, "amount": 4321, "spentAt": "2026-01-05"})
	s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/incomes", Token: owner.Token, Household: hh.ID, Body: map[string]any{
		"amount": 98765, "currency": "ARS", "source": "salary", "description": "Sueldo secreto", "receivedAt": "2026-01-01"}}).
		Status(t, http.StatusCreated)

	var o overview
	res := s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/admin/overview", Token: admin.Token}).Status(t, http.StatusOK)
	res.JSON(t, &o)
	a := o.Accounts
	if a.Total != 2 || a.NewWeek != 2 || a.NewMonth != 2 || a.Verified != 1 || a.Active != 2 {
		t.Errorf("accounts = %+v", a)
	}
	if o.Households.Active != 1 || o.Households.Deleted != 1 {
		t.Errorf("households = %+v", o.Households)
	}
	if len(o.Movements) != 6 {
		t.Fatalf("movements = %+v", o.Movements)
	}
	// Counted by creation (today), not by the spend date in January.
	last := o.Movements[5]
	if last.Month != time.Now().In(time.FixedZone("ART", -3*3600)).Format("2006-01") || last.Expenses != 1 || last.Incomes != 1 {
		t.Errorf("this month = %+v", last)
	}
	// Plan §8b: counts only, never amounts or descriptions.
	if body := string(res.Body); strings.Contains(body, "4321") || strings.Contains(body, "98765") || strings.Contains(body, "Sueldo") {
		t.Errorf("overview leaks financial data: %s", body)
	}
}

func TestUsers(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	admin := s.Register(t)
	s.MakeSuperadmin(t, admin)
	ana := s.Register(t, apitest.RegisterOpts{Email: "ana.perez@example.com", FirstName: "Ana", LastName: "Pérez", Verify: true})
	s.CreateHousehold(t, ana, "Casa de Ana")
	s.Register(t, apitest.RegisterOpts{Email: "bruno_x@example.com"})

	var list struct {
		Items      []userItem `json:"items"`
		NextOffset *int       `json:"nextOffset"`
	}
	get(t, s, admin, "/admin/users", &list)
	if len(list.Items) != 3 || list.NextOffset != nil {
		t.Fatalf("list = %+v", list)
	}
	if list.Items[2].ID != admin.ID || !list.Items[2].IsSuperadmin {
		t.Errorf("newest first, admin last: %+v", list.Items)
	}

	get(t, s, admin, "/admin/users?q=ANA.P", &list)
	if len(list.Items) != 1 || list.Items[0].ID != ana.ID || list.Items[0].Households != 1 ||
		list.Items[0].VerifiedAt == nil || list.Items[0].LastSeenAt == nil {
		t.Errorf("search by email = %+v", list.Items)
	}
	get(t, s, admin, "/admin/users?q=p%C3%A9rez", &list)
	if len(list.Items) != 1 || list.Items[0].ID != ana.ID {
		t.Errorf("search by name = %+v", list.Items)
	}
	// LIKE wildcards are literal: "_" only matches an underscore.
	get(t, s, admin, "/admin/users?q=o_x", &list)
	if len(list.Items) != 1 || list.Items[0].Email != "bruno_x@example.com" {
		t.Errorf("underscore search = %+v", list.Items)
	}
	get(t, s, admin, "/admin/users?q=%25", &list)
	if len(list.Items) != 0 {
		t.Errorf("%% matched %d accounts", len(list.Items))
	}
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/admin/users?offset=-1", Token: admin.Token}).
		WantError(t, http.StatusUnprocessableEntity, "validation", "offset")

	var detail struct {
		userItem
		HasPassword    bool     `json:"hasPassword"`
		Providers      []string `json:"providers"`
		ActiveSessions int64    `json:"activeSessions"`
		Spaces         []struct {
			Name    string `json:"name"`
			Kind    string `json:"kind"`
			Role    string `json:"role"`
			Members int64  `json:"members"`
		} `json:"spaces"`
	}
	get(t, s, admin, "/admin/users/"+ana.ID, &detail)
	if detail.Email != ana.Email || !detail.HasPassword || len(detail.Providers) != 0 || detail.ActiveSessions != 1 || detail.Households != 1 {
		t.Errorf("detail = %+v", detail)
	}
	if len(detail.Spaces) != 2 || detail.Spaces[0].Kind != "shared" || detail.Spaces[0].Name != "Casa de Ana" ||
		detail.Spaces[0].Role != "owner" || detail.Spaces[1].Kind != "personal" {
		t.Errorf("spaces = %+v", detail.Spaces)
	}
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/admin/users/00000000-0000-0000-0000-000000000000", Token: admin.Token}).
		Status(t, http.StatusNotFound)
}

func TestAudit(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	admin := s.Register(t)
	s.MakeSuperadmin(t, admin)
	owner := s.Register(t)
	s.Login(t, owner.Email, "wrong-password-1!").Status(t, http.StatusUnauthorized)
	gone := s.CreateHousehold(t, owner, "Viejo")
	s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/households/" + gone.ID, Token: owner.Token}).Status(t, http.StatusNoContent)
	s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/admin/households/" + gone.ID + "/restore", Token: admin.Token}).
		Status(t, http.StatusNoContent)

	var all auditPage
	get(t, s, admin, "/admin/audit", &all)
	if len(all.Items) < 4 || all.NextCursor != nil {
		t.Fatalf("all = %+v", all)
	}

	var failed auditPage
	get(t, s, admin, "/admin/audit?group=failed", &failed)
	if len(failed.Items) != 1 || failed.Items[0].Action != "login.failed" || *failed.Items[0].UserEmail != owner.Email {
		t.Errorf("failed = %+v", failed.Items)
	}

	// Superadmin actions carry who did them.
	var adm auditPage
	get(t, s, admin, "/admin/audit?group=admin", &adm)
	if len(adm.Items) != 1 || adm.Items[0].Action != "admin.household_restored" ||
		adm.Items[0].ActorEmail == nil || *adm.Items[0].ActorEmail != admin.Email || adm.Items[0].Target != "household:"+gone.ID {
		t.Errorf("admin = %+v", adm.Items)
	}

	// By account: what happened to it and what it did.
	var mine auditPage
	get(t, s, admin, "/admin/audit?userId="+admin.ID, &mine)
	actions := map[string]bool{}
	for _, e := range mine.Items {
		actions[e.Action] = true
	}
	if !actions["login.success"] || !actions["admin.household_restored"] || actions["login.failed"] {
		t.Errorf("by account = %v", actions)
	}

	for _, q := range []string{"group=nope", "userId=x", "cursor=nope"} {
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/admin/audit?" + q, Token: admin.Token}).
			Status(t, http.StatusUnprocessableEntity)
	}
}

func TestAuditPages(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	admin := s.Register(t)
	s.MakeSuperadmin(t, admin)
	u := s.Register(t)
	for range 55 {
		s.Login(t, u.Email, "wrong-password-1!").Status(t, http.StatusUnauthorized)
	}
	var p1, p2 auditPage
	get(t, s, admin, "/admin/audit?group=failed", &p1)
	if len(p1.Items) != 50 || p1.NextCursor == nil {
		t.Fatalf("page 1: %d items, cursor %v", len(p1.Items), p1.NextCursor)
	}
	get(t, s, admin, "/admin/audit?group=failed&cursor="+*p1.NextCursor, &p2)
	if len(p2.Items) != 5 || p2.NextCursor != nil {
		t.Fatalf("page 2: %d items, cursor %v", len(p2.Items), p2.NextCursor)
	}
	seen := map[string]bool{}
	for _, e := range append(p1.Items, p2.Items...) {
		if seen[e.ID] {
			t.Errorf("event %s on both pages", e.ID)
		}
		seen[e.ID] = true
	}
}
