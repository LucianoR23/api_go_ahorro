package exports_test

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"reflect"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/exports"
	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

// Excel exports over HTTP (plan 11.1).

type house struct {
	s             *apitest.Server
	id            string
	owner, member apitest.User
	ownerCash     string
	memberCash    string
}

func newHouse(t *testing.T, s *apitest.Server) house {
	t.Helper()
	owner := s.Register(t, apitest.RegisterOpts{FirstName: "Bea"})
	hh := s.CreateHousehold(t, owner, "")
	member := s.AddMember(t, owner, hh.ID)
	return house{s: s, id: hh.ID, owner: owner, member: member, ownerCash: s.Cash(t, owner).ID, memberCash: s.Cash(t, member).ID}
}

func (h house) do(t testing.TB, u apitest.User, path string) *apitest.Res {
	t.Helper()
	return h.s.Do(t, apitest.Req{Method: http.MethodGet, Path: path, Token: u.Token, Household: h.id})
}

func (h house) post(t testing.TB, u apitest.User, path string, body any, out any) {
	t.Helper()
	res := h.s.Do(t, apitest.Req{Method: http.MethodPost, Path: path, Token: u.Token, Household: h.id, Body: body}).Status(t, http.StatusCreated)
	if out != nil {
		res.JSON(t, out)
	}
}

// rows reads the sheet back as text: inline strings and raw values, with
// the header first. Missing cells are "".
func rows(t *testing.T, res *apitest.Res) [][]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(res.Body), int64(len(res.Body)))
	if err != nil {
		t.Fatalf("not an xlsx: %v (%s)", err, res.Body)
	}
	var raw []byte
	for _, f := range zr.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			raw, _ = io.ReadAll(rc)
			_ = rc.Close()
		}
	}
	var ws struct {
		Rows []struct {
			Cells []struct {
				Ref    string `xml:"r,attr"`
				Value  string `xml:"v"`
				Inline string `xml:"is>t"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal(raw, &ws); err != nil {
		t.Fatal(err)
	}
	out := make([][]string, 0, len(ws.Rows))
	for _, r := range ws.Rows {
		width := len(ws.Rows[0].Cells)
		line := make([]string, width)
		for _, c := range r.Cells {
			col := int(c.Ref[0] - 'A')
			if c.Inline != "" {
				line[col] = c.Inline
			} else {
				line[col] = c.Value
			}
		}
		out = append(out, line)
	}
	return out
}

func TestExpensesExport(t *testing.T) {
	s := apitest.New(t)
	h := newHouse(t, s)

	var parent, child struct {
		ID string `json:"id"`
	}
	h.post(t, h.owner, "/categories", map[string]any{"name": "Gatitos"}, &parent)
	h.post(t, h.owner, "/categories", map[string]any{"name": "Alimento", "parentId": parent.ID}, &child)
	if _, err := s.Pool.Exec(t.Context(), `INSERT INTO exchange_rates (currency, source, last_update, rate_avg, rate_buy, rate_sell)
		VALUES ('USD', 'bolsa', now(), 1200, 1190, 1210)`); err != nil {
		t.Fatal(err)
	}

	s.CreateExpense(t, h.owner, h.id, map[string]any{"paymentMethodId": h.ownerCash, "amount": 19450.5,
		"description": "Coto", "spentAt": "2026-11-03", "categoryId": child.ID})
	s.CreateExpense(t, h.owner, h.id, map[string]any{"paymentMethodId": h.ownerCash, "amount": 10, "currency": "USD",
		"description": "Netflix", "spentAt": "2026-11-05"})
	// A member's expense: their payment method stays private.
	s.CreateExpense(t, h.member, h.id, map[string]any{"paymentMethodId": h.memberCash, "amount": 3000,
		"description": "Farmacia", "spentAt": "2026-11-04"})
	// Outside the month, and a draft: neither is exported.
	s.CreateExpense(t, h.owner, h.id, map[string]any{"paymentMethodId": h.ownerCash, "amount": 1, "description": "Octubre", "spentAt": "2026-10-31"})
	draft := s.CreateExpense(t, h.owner, h.id, map[string]any{"paymentMethodId": h.ownerCash, "amount": 1, "description": "Borrador", "spentAt": "2026-11-06"})
	if _, err := s.Pool.Exec(t.Context(), `UPDATE expenses SET status = 'draft' WHERE id = $1`, draft.Expense.ID); err != nil {
		t.Fatal(err)
	}

	res := h.do(t, h.owner, "/exports/expenses.xlsx?from=2026-11-01&to=2026-11-30").Status(t, http.StatusOK)
	if ct := res.Header.Get("Content-Type"); ct != exports.ContentType {
		t.Errorf("content type = %q", ct)
	}
	if cd := res.Header.Get("Content-Disposition"); cd != `attachment; filename="gastos-2026-11.xlsx"` {
		t.Errorf("content disposition = %q", cd)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache control = %q", cc)
	}

	want := [][]string{
		{"Fecha", "Descripción", "Categoría", "Medio de pago", "Monto", "Moneda", "Cotización", "Monto en pesos", "Cuotas", "Tipo", "Compartido", "Cargó"},
		{"46329", "Coto", "Gatitos > Alimento", "Efectivo", "19450.50", "ARS", "", "19450.50", "1", "Variable", "No", "Bea"},
		{"46330", "Farmacia", "Sin categoría", "", "3000.00", "ARS", "", "3000.00", "1", "Variable", "No", h.member.FirstName},
		{"46331", "Netflix", "Sin categoría", "Efectivo", "10.00", "USD", "1210.00", "12100.00", "1", "Variable", "No", "Bea"},
	}
	got := rows(t, res)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sheet:\n got %q\nwant %q", got, want)
	}

	t.Run("the member sees their own payment method", func(t *testing.T) {
		got := rows(t, h.do(t, h.member, "/exports/expenses.xlsx?from=2026-11-01&to=2026-11-30").Status(t, http.StatusOK))
		if got[1][3] != "" || got[2][3] != "Efectivo" {
			t.Errorf("member view = %q", got)
		}
	})

	t.Run("filters match the list", func(t *testing.T) {
		cases := map[string][]string{
			"categoryId=" + parent.ID:         {"Coto"},
			"paymentMethodId=" + h.memberCash: {"Farmacia"},
			"q=net":                           {"Netflix"},
			"kind=fixed":                      {},
			"kind=variable&q=%25":             {},
			"q=" + "f":                        {"Farmacia", "Netflix"},
		}
		for query, wantDesc := range cases {
			got := rows(t, h.do(t, h.owner, "/exports/expenses.xlsx?from=2026-11-01&to=2026-11-30&"+query).Status(t, http.StatusOK))
			desc := []string{}
			for _, r := range got[1:] {
				desc = append(desc, r[1])
			}
			if !reflect.DeepEqual(desc, wantDesc) {
				t.Errorf("%s: %q, want %q", query, desc, wantDesc)
			}
		}
	})

	t.Run("a whole year is named after it", func(t *testing.T) {
		res := h.do(t, h.owner, "/exports/expenses.xlsx?from=2026-01-01&to=2026-12-31").Status(t, http.StatusOK)
		if cd := res.Header.Get("Content-Disposition"); cd != `attachment; filename="gastos-2026.xlsx"` {
			t.Errorf("content disposition = %q", cd)
		}
		if n := len(rows(t, res)); n != 5 {
			t.Errorf("rows = %d, want header + 4", n)
		}
	})

	t.Run("bad requests", func(t *testing.T) {
		for _, q := range []string{
			"to=2026-11-30",
			"from=2026-11-01",
			"from=01/11/2026&to=2026-11-30",
			"from=2026-11-30&to=2026-11-01",
			"from=2026-01-01&to=2027-01-02",
			"from=2026-11-01&to=2026-11-30&kind=other",
			"from=2026-11-01&to=2026-11-30&categoryId=x",
		} {
			h.do(t, h.owner, "/exports/expenses.xlsx?"+q).WantError(t, http.StatusUnprocessableEntity, "validation", "")
		}
	})

	t.Run("only members of the space", func(t *testing.T) {
		outsider := s.Register(t, apitest.RegisterOpts{FirstName: "Otro"})
		res := h.do(t, outsider, "/exports/expenses.xlsx?from=2026-11-01&to=2026-11-30")
		if res.Code != http.StatusForbidden && res.Code != http.StatusNotFound {
			t.Errorf("outsider status = %d", res.Code)
		}
		res = s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/exports/expenses.xlsx?from=2026-11-01&to=2026-11-30", Household: h.id})
		if res.Code != http.StatusUnauthorized {
			t.Errorf("anonymous status = %d", res.Code)
		}
	})
}

func TestIncomesExport(t *testing.T) {
	s := apitest.New(t)
	h := newHouse(t, s)

	h.post(t, h.owner, "/incomes", map[string]any{"amount": 850000, "currency": "ARS", "source": "salary",
		"description": "Sueldo", "receivedAt": "2026-11-05", "paymentMethodId": h.ownerCash}, nil)
	h.post(t, h.member, "/incomes", map[string]any{"amount": 120000, "currency": "ARS", "source": "freelance",
		"description": "Diseño logo", "receivedAt": "2026-11-02", "paymentMethodId": h.memberCash}, nil)
	h.post(t, h.owner, "/incomes", map[string]any{"amount": 1, "currency": "ARS", "source": "other",
		"description": "Diciembre", "receivedAt": "2026-12-01"}, nil)

	res := h.do(t, h.owner, "/exports/incomes.xlsx?from=2026-11-01&to=2026-11-30").Status(t, http.StatusOK)
	if cd := res.Header.Get("Content-Disposition"); cd != `attachment; filename="ingresos-2026-11.xlsx"` {
		t.Errorf("content disposition = %q", cd)
	}
	want := [][]string{
		{"Fecha", "Descripción", "Tipo", "Medio", "Monto", "Moneda", "Cotización", "Monto en pesos", "Recibió"},
		{"46328", "Diseño logo", "Freelance", "", "120000.00", "ARS", "", "120000.00", h.member.FirstName},
		{"46331", "Sueldo", "Salario", "Efectivo", "850000.00", "ARS", "", "850000.00", "Bea"},
	}
	if got := rows(t, res); !reflect.DeepEqual(got, want) {
		t.Errorf("sheet:\n got %q\nwant %q", got, want)
	}

	h.do(t, h.owner, "/exports/incomes.xlsx?from=2026-11-01").WantError(t, http.StatusUnprocessableEntity, "validation", "")
}
