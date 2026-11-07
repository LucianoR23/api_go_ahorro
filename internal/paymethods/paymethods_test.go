package paymethods_test

// Characterization tests of banks, payment methods and credit cards
// (item 0.21d). All of them are per user, not per household.

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

type bankJSON struct {
	ID          string `json:"id"`
	OwnerUserID string `json:"ownerUserId"`
	Name        string `json:"name"`
	IsActive    bool   `json:"isActive"`
}

func do(t testing.TB, s *apitest.Server, method, path string, u apitest.User, body any) *apitest.Res {
	t.Helper()
	return s.Do(t, apitest.Req{Method: method, Path: path, Token: u.Token, Body: body})
}

func createBank(t testing.TB, s *apitest.Server, u apitest.User, name string) bankJSON {
	t.Helper()
	var b bankJSON
	do(t, s, http.MethodPost, "/banks", u, map[string]string{"name": name}).Status(t, http.StatusCreated).JSON(t, &b)
	return b
}

func listPMs(t testing.TB, s *apitest.Server, u apitest.User, query string) []apitest.PaymentMethod {
	t.Helper()
	var list []apitest.PaymentMethod
	do(t, s, http.MethodGet, "/payment-methods"+query, u, nil).Status(t, http.StatusOK).JSON(t, &list)
	return list
}

func card(name string) map[string]any {
	return map[string]any{
		"name": name, "kind": "credit",
		"creditCard": map[string]any{
			"alias": name, "lastFour": "4321", "defaultClosingDay": 25, "defaultDueDay": 5,
			"currentPeriod": map[string]string{"closingDate": "2026-11-26", "dueDate": "2026-12-07"},
			"nextPeriod":    map[string]string{"closingDate": "2026-12-24", "dueDate": "2027-01-05"},
		},
	}
}

func TestBanks(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("create, rename, deactivate, activate", func(t *testing.T) {
		u := s.Register(t)
		b := createBank(t, s, u, "  Galicia ")
		if b.Name != "Galicia" || !b.IsActive || b.OwnerUserID != u.ID {
			t.Errorf("bank = %+v", b)
		}
		var got bankJSON
		do(t, s, http.MethodPatch, "/banks/"+b.ID, u, map[string]string{"name": "Banco Galicia"}).Status(t, http.StatusOK).JSON(t, &got)
		if got.Name != "Banco Galicia" {
			t.Errorf("rename = %+v", got)
		}
		do(t, s, http.MethodPost, "/banks/"+b.ID+"/deactivate", u, nil).Status(t, http.StatusOK).JSON(t, &got)
		if got.IsActive {
			t.Errorf("still active")
		}
		do(t, s, http.MethodPost, "/banks/"+b.ID+"/activate", u, nil).Status(t, http.StatusOK).JSON(t, &got)
		if !got.IsActive {
			t.Errorf("not reactivated")
		}
	})

	t.Run("creating an inactive bank's name revives it", func(t *testing.T) {
		u := s.Register(t)
		b := createBank(t, s, u, "Santander")
		do(t, s, http.MethodPost, "/banks/"+b.ID+"/deactivate", u, nil).Status(t, http.StatusOK)
		again := createBank(t, s, u, "Santander")
		if again.ID != b.ID || !again.IsActive {
			t.Errorf("revive = %+v, want %s active", again, b.ID)
		}
	})

	t.Run("duplicate active name is a conflict", func(t *testing.T) {
		u := s.Register(t)
		createBank(t, s, u, "BBVA")
		do(t, s, http.MethodPost, "/banks", u, map[string]string{"name": "BBVA"}).WantError(t, http.StatusConflict, "conflict", "")
	})

	t.Run("banks are private to their owner", func(t *testing.T) {
		a, b := s.Register(t), s.Register(t)
		bank := createBank(t, s, a, "Macro")
		var list []bankJSON
		do(t, s, http.MethodGet, "/banks", b, nil).Status(t, http.StatusOK).JSON(t, &list)
		if len(list) != 0 {
			t.Errorf("b sees %+v", list)
		}
		do(t, s, http.MethodPatch, "/banks/"+bank.ID, b, map[string]string{"name": "x"}).WantError(t, http.StatusNotFound, "not_found", "")
		do(t, s, http.MethodPost, "/banks/"+bank.ID+"/deactivate", b, nil).WantError(t, http.StatusNotFound, "not_found", "")
		// Same name for another user is fine.
		createBank(t, s, b, "Macro")
	})

	t.Run("validation", func(t *testing.T) {
		u := s.Register(t)
		b := createBank(t, s, u, "Nación")
		do(t, s, http.MethodPost, "/banks", u, map[string]string{"name": " "}).WantError(t, http.StatusUnprocessableEntity, "validation", "name")
		do(t, s, http.MethodPatch, "/banks/"+b.ID, u, map[string]string{"name": ""}).WantError(t, http.StatusUnprocessableEntity, "validation", "name")
		do(t, s, http.MethodPatch, "/banks/nope", u, map[string]string{"name": "x"}).WantError(t, http.StatusUnprocessableEntity, "validation", "id")
		do(t, s, http.MethodPost, "/banks", u, map[string]string{"name": "x", "color": "red"}).WantError(t, http.StatusUnprocessableEntity, "validation", "body")
	})
}

func TestPaymentMethods(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("new users start with Efectivo", func(t *testing.T) {
		u := s.Register(t)
		list := listPMs(t, s, u, "")
		if len(list) != 1 || list[0].Name != "Efectivo" || list[0].Kind != "cash" || list[0].AllowsInstallments || !list[0].IsActive {
			t.Errorf("payment methods = %+v", list)
		}
	})

	t.Run("allowsInstallments is forced by kind except for wallets", func(t *testing.T) {
		u := s.Register(t)
		yes, no := true, false
		cases := []struct {
			kind  string
			input *bool
			want  bool
		}{
			{"debit", &yes, false},
			{"transfer", &yes, false},
			{"cash", nil, false},
			{"wallet", nil, false},
			{"wallet", &yes, true},
			{"wallet", &no, false},
		}
		for i, tc := range cases {
			body := map[string]any{"name": tc.kind + string(rune('A'+i)), "kind": tc.kind}
			if tc.input != nil {
				body["allowsInstallments"] = *tc.input
			}
			if pm := s.CreatePaymentMethod(t, u, body); pm.AllowsInstallments != tc.want {
				t.Errorf("%s with %v: allowsInstallments = %v, want %v", tc.kind, tc.input, pm.AllowsInstallments, tc.want)
			}
		}
	})

	t.Run("credit card with both periods", func(t *testing.T) {
		u := s.Register(t)
		bank := createBank(t, s, u, "Galicia")
		debit := s.CreatePaymentMethod(t, u, map[string]any{"name": "Débito Galicia", "kind": "debit", "bankId": bank.ID})
		body := card("Visa Galicia")
		body["bankId"] = bank.ID
		body["creditCard"].(map[string]any)["debitPaymentMethodId"] = debit.ID

		pm := s.CreatePaymentMethod(t, u, body)
		if pm.Kind != "credit" || !pm.AllowsInstallments || pm.BankID == nil || *pm.BankID != bank.ID {
			t.Errorf("credit pm = %+v", pm)
		}
		cc := pm.CreditCard
		if cc == nil || cc.Alias != "Visa Galicia" || cc.LastFour == nil || *cc.LastFour != "4321" ||
			cc.DefaultClosingDay != 25 || cc.DefaultDueDay != 5 || cc.DebitPaymentMethodID == nil || *cc.DebitPaymentMethodID != debit.ID {
			t.Errorf("credit card = %+v", cc)
		}
		if len(pm.Periods) != 2 || pm.Periods[0].PeriodYM != "2026-11" || pm.Periods[1].PeriodYM != "2026-12" ||
			pm.Periods[0].DueDate != "2026-12-07" {
			t.Errorf("periods = %+v", pm.Periods)
		}

		var got struct {
			Alias string `json:"alias"`
		}
		do(t, s, http.MethodGet, "/payment-methods/"+pm.ID+"/credit-card", u, nil).Status(t, http.StatusOK).JSON(t, &got)
		if got.Alias != "Visa Galicia" {
			t.Errorf("get card = %+v", got)
		}
	})

	t.Run("credit card validation", func(t *testing.T) {
		u := s.Register(t)
		mutate := func(f func(cc map[string]any)) map[string]any {
			b := card("Mastercard")
			f(b["creditCard"].(map[string]any))
			return b
		}
		cases := map[string]struct {
			body  map[string]any
			field string
		}{
			"no card data":        {map[string]any{"name": "C", "kind": "credit"}, "creditCard"},
			"card on a debit":     {map[string]any{"name": "D", "kind": "debit", "creditCard": card("x")["creditCard"]}, "creditCard"},
			"empty alias":         {mutate(func(cc map[string]any) { cc["alias"] = " " }), "alias"},
			"closing day 0":       {mutate(func(cc map[string]any) { cc["defaultClosingDay"] = 0 }), "defaultClosingDay"},
			"due day 32":          {mutate(func(cc map[string]any) { cc["defaultDueDay"] = 32 }), "defaultDueDay"},
			"lastFour too short":  {mutate(func(cc map[string]any) { cc["lastFour"] = "123" }), "lastFour"},
			"lastFour not digits": {mutate(func(cc map[string]any) { cc["lastFour"] = "12a4" }), "lastFour"},
			"no current period":   {mutate(func(cc map[string]any) { delete(cc, "currentPeriod") }), "creditCard.currentPeriod"},
			"bad closing date": {mutate(func(cc map[string]any) {
				cc["currentPeriod"] = map[string]string{"closingDate": "26/11/2026", "dueDate": "2026-12-07"}
			}), "currentPeriod.closingDate"},
			"due before closing": {mutate(func(cc map[string]any) {
				cc["nextPeriod"] = map[string]string{"closingDate": "2026-12-24", "dueDate": "2026-12-01"}
			}), "nextPeriod.dueDate"},
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				do(t, s, http.MethodPost, "/payment-methods", u, tc.body).WantError(t, http.StatusUnprocessableEntity, "validation", tc.field)
			})
		}
	})

	t.Run("other validation", func(t *testing.T) {
		u := s.Register(t)
		do(t, s, http.MethodPost, "/payment-methods", u, map[string]any{"name": " ", "kind": "cash"}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "name")
		do(t, s, http.MethodPost, "/payment-methods", u, map[string]any{"name": "X", "kind": "crypto"}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "kind")
		do(t, s, http.MethodPost, "/payment-methods", u, map[string]any{"name": "Efectivo", "kind": "cash"}).
			WantError(t, http.StatusConflict, "conflict", "")
	})

	t.Run("update: rename, bank, installments", func(t *testing.T) {
		u := s.Register(t)
		bank := createBank(t, s, u, "ICBC")
		w := s.CreatePaymentMethod(t, u, map[string]any{"name": "MP", "kind": "wallet", "bankId": bank.ID})

		var got apitest.PaymentMethod
		do(t, s, http.MethodPatch, "/payment-methods/"+w.ID, u, map[string]any{"name": " Mercado Pago ", "allowsInstallments": true}).
			Status(t, http.StatusOK).JSON(t, &got)
		if got.Name != "Mercado Pago" || !got.AllowsInstallments {
			t.Errorf("update = %+v", got)
		}
		// PATCH replaces bankId: omitting it clears the bank.
		if got.BankID != nil {
			t.Errorf("bankId = %v, want cleared when omitted", *got.BankID)
		}

		cc := s.CreatePaymentMethod(t, u, card("Amex"))
		do(t, s, http.MethodPatch, "/payment-methods/"+cc.ID, u, map[string]any{"name": "Amex", "allowsInstallments": false}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "allowsInstallments")
		do(t, s, http.MethodPatch, "/payment-methods/"+cc.ID, u, map[string]any{"name": ""}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "name")
	})

	t.Run("deactivate, list with inactive, revive by name", func(t *testing.T) {
		u := s.Register(t)
		cash := s.Cash(t, u)
		do(t, s, http.MethodPost, "/payment-methods/"+cash.ID+"/deactivate", u, nil).
			WantError(t, http.StatusUnprocessableEntity, "validation", "isActive")

		d := s.CreatePaymentMethod(t, u, map[string]any{"name": "Débito", "kind": "debit"})
		do(t, s, http.MethodPost, "/payment-methods/"+d.ID+"/deactivate", u, nil).Status(t, http.StatusOK)
		if n := len(listPMs(t, s, u, "")); n != 1 {
			t.Errorf("active list has %d, want only Efectivo", n)
		}
		if n := len(listPMs(t, s, u, "?includeInactive=true")); n != 2 {
			t.Errorf("full list has %d, want 2", n)
		}

		revived := s.CreatePaymentMethod(t, u, map[string]any{"name": "Débito", "kind": "debit"})
		if revived.ID != d.ID || !revived.IsActive {
			t.Errorf("revive = %+v, want %s active", revived, d.ID)
		}
		do(t, s, http.MethodPost, "/payment-methods/"+d.ID+"/deactivate", u, nil).Status(t, http.StatusOK)
		do(t, s, http.MethodPost, "/payment-methods", u, map[string]any{"name": "Débito", "kind": "transfer"}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "name")
		do(t, s, http.MethodPost, "/payment-methods/"+d.ID+"/activate", u, nil).Status(t, http.StatusOK)
	})

	t.Run("reviving a credit card replaces its data", func(t *testing.T) {
		u := s.Register(t)
		old := s.CreatePaymentMethod(t, u, card("Naranja"))
		do(t, s, http.MethodPost, "/payment-methods/"+old.ID+"/deactivate", u, nil).Status(t, http.StatusOK)
		body := card("Naranja")
		body["creditCard"].(map[string]any)["defaultClosingDay"] = 10
		revived := s.CreatePaymentMethod(t, u, body)
		if revived.ID != old.ID || !revived.IsActive || revived.CreditCard == nil || revived.CreditCard.DefaultClosingDay != 10 {
			t.Errorf("revived card = %+v", revived)
		}
	})

	t.Run("update credit card", func(t *testing.T) {
		u := s.Register(t)
		cc := s.CreatePaymentMethod(t, u, card("Cabal"))
		debit := s.CreatePaymentMethod(t, u, map[string]any{"name": "Cuenta", "kind": "debit"})
		path := "/payment-methods/" + cc.ID + "/credit-card"

		var got struct {
			Alias                string  `json:"alias"`
			LastFour             *string `json:"lastFour"`
			DefaultClosingDay    int     `json:"defaultClosingDay"`
			DebitPaymentMethodID *string `json:"debitPaymentMethodId"`
		}
		do(t, s, http.MethodPatch, path, u, map[string]any{"alias": " Cabal Oro ", "defaultClosingDay": 20, "defaultDueDay": 1,
			"debitPaymentMethodId": debit.ID}).Status(t, http.StatusOK).JSON(t, &got)
		if got.Alias != "Cabal Oro" || got.DefaultClosingDay != 20 ||
			got.DebitPaymentMethodID == nil || *got.DebitPaymentMethodID != debit.ID {
			t.Errorf("updated card = %+v", got)
		}
		if got.LastFour == nil {
			t.Errorf("lastFour 4321 cleared by a PATCH that did not mention it")
		}
		var again struct {
			DebitPaymentMethodID *string `json:"debitPaymentMethodId"`
		}
		do(t, s, http.MethodPatch, path, u, map[string]any{"alias": "Cabal Oro", "defaultClosingDay": 21, "defaultDueDay": 1}).
			Status(t, http.StatusOK).JSON(t, &again)
		if again.DebitPaymentMethodID == nil {
			t.Errorf("debit account unlinked by an unrelated edit")
		}
		var cleared struct {
			Alias                string  `json:"alias"`
			LastFour             *string `json:"lastFour"`
			DebitPaymentMethodID *string `json:"debitPaymentMethodId"`
		}
		do(t, s, http.MethodPatch, path, u, map[string]any{"lastFour": nil, "debitPaymentMethodId": nil}).
			Status(t, http.StatusOK).JSON(t, &cleared)
		if cleared.LastFour != nil || cleared.DebitPaymentMethodID != nil || cleared.Alias != "Cabal Oro" {
			t.Errorf("null did not clear only lastFour and the debit account: %+v", cleared)
		}
		do(t, s, http.MethodPatch, path, u, map[string]any{}).WantError(t, http.StatusUnprocessableEntity, "validation", "body")
		do(t, s, http.MethodPatch, path, u, map[string]any{"defaultClosingDay": 40}).WantError(t, http.StatusUnprocessableEntity, "validation", "defaultClosingDay")
		do(t, s, http.MethodPatch, path, u, map[string]any{"alias": "x", "defaultClosingDay": 20, "defaultDueDay": 1,
			"debitPaymentMethodId": cc.ID}).WantError(t, http.StatusUnprocessableEntity, "validation", "debitPaymentMethodId")
		do(t, s, http.MethodPatch, path, u, map[string]any{"alias": "", "defaultClosingDay": 20, "defaultDueDay": 1}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "alias")
		do(t, s, http.MethodGet, "/payment-methods/"+debit.ID+"/credit-card", u, nil).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("payment methods are private to their owner", func(t *testing.T) {
		a, b := s.Register(t), s.Register(t)
		aBank := createBank(t, s, a, "Supervielle")
		aCard := s.CreatePaymentMethod(t, a, card("Visa A"))
		aDebit := s.CreatePaymentMethod(t, a, map[string]any{"name": "Débito A", "kind": "debit"})

		for _, pm := range listPMs(t, s, b, "?includeInactive=true") {
			if pm.OwnerUserID != b.ID {
				t.Errorf("b sees %+v", pm)
			}
		}
		for _, r := range []struct{ method, path string }{
			{http.MethodPatch, "/payment-methods/" + aCard.ID},
			{http.MethodPost, "/payment-methods/" + aCard.ID + "/deactivate"},
			{http.MethodPost, "/payment-methods/" + aCard.ID + "/activate"},
			{http.MethodGet, "/payment-methods/" + aCard.ID + "/credit-card"},
			{http.MethodPatch, "/payment-methods/" + aCard.ID + "/credit-card"},
		} {
			var body any
			if r.method == http.MethodPatch {
				if strings.HasSuffix(r.path, "/credit-card") {
					body = map[string]any{"alias": "x", "defaultClosingDay": 1, "defaultDueDay": 1}
				} else {
					body = map[string]any{"name": "x"}
				}
			}
			do(t, s, r.method, r.path, b, body).WantError(t, http.StatusNotFound, "not_found", "")
		}
		// Referencing a's bank or debit account from b's payment methods.
		do(t, s, http.MethodPost, "/payment-methods", b, map[string]any{"name": "Robo", "kind": "debit", "bankId": aBank.ID}).
			WantError(t, http.StatusNotFound, "not_found", "")
		steal := card("Visa B")
		steal["creditCard"].(map[string]any)["debitPaymentMethodId"] = aDebit.ID
		do(t, s, http.MethodPost, "/payment-methods", b, steal).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("requires auth", func(t *testing.T) {
		for _, r := range []apitest.Req{
			{Method: http.MethodGet, Path: "/banks"},
			{Method: http.MethodPost, Path: "/banks", Body: map[string]string{"name": "x"}},
			{Method: http.MethodGet, Path: "/payment-methods"},
			{Method: http.MethodPost, Path: "/payment-methods", Body: map[string]string{"name": "x", "kind": "cash"}},
		} {
			s.Do(t, r).WantError(t, http.StatusUnauthorized, "unauthorized", "")
		}
	})
}
