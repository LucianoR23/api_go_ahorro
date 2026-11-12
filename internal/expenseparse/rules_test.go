package expenseparse

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	ba, _ = time.LoadLocation("America/Argentina/Buenos_Aires")
	// Thursday 2026-11-12.
	today = time.Date(2026, 11, 12, 0, 0, 0, 0, ba)

	visaGalicia   = Method{ID: uuid.New(), Name: "Visa Galicia", Kind: "credit", Bank: "Galicia", Alias: "Visa Galicia"}
	visaSantander = Method{ID: uuid.New(), Name: "Visa Santander", Kind: "credit", Bank: "Santander", Alias: "Visa"}
	master        = Method{ID: uuid.New(), Name: "Mastercard BBVA", Kind: "credit", Bank: "BBVA", Alias: "Master"}
	debitoGalicia = Method{ID: uuid.New(), Name: "Débito Galicia", Kind: "debit", Bank: "Galicia"}
	efectivo      = Method{ID: uuid.New(), Name: "Efectivo", Kind: "cash"}
	mp            = Method{ID: uuid.New(), Name: "Mercado Pago", Kind: "wallet"}
	methods       = []Method{visaGalicia, visaSantander, master, debitoGalicia, efectivo, mp}
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, ba) }

func TestRules(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Fields
	}{
		{"café 2500", Fields{Amount: 2500, Description: "Café"}},
		{"zapatillas 120 mil en 6 cuotas con master", Fields{Amount: 120000, Installments: 6, PaymentMethodID: master.ID, Description: "Zapatillas"}},
		{"café 2500 visa galicia 3 cuotas ayer", Fields{Amount: 2500, Installments: 3, PaymentMethodID: visaGalicia.ID, SpentAt: day(2026, 11, 11), Description: "Café"}},
		{"súper 15 lucas débito compartido", Fields{Amount: 15000, PaymentMethodID: debitoGalicia.ID, Shared: true, Description: "Súper"}},
		{"Netflix u$s 12,99", Fields{Amount: 12.99, Currency: "USD", Description: "Netflix"}},
		{"cena 45.000 pesos efectivo el lunes", Fields{Amount: 45000, Currency: "ARS", PaymentMethodID: efectivo.ID, SpentAt: day(2026, 11, 9), Description: "Cena"}},
		{"$3.200,50 farmacia 10/11", Fields{Amount: 3200.5, Currency: "ARS", SpentAt: day(2026, 11, 10), Description: "Farmacia"}},
		{"nafta 1.5 palos el 20", Fields{Amount: 1.5e6, SpentAt: day(2026, 10, 20), Description: "Nafta"}},
		{"regalo 80 euros 3 de marzo", Fields{Amount: 80, Currency: "EUR", SpentAt: day(2026, 3, 3), Description: "Regalo"}},
		{"heladera 900k en doce cuotas sin interés", Fields{Amount: 900000, Installments: 12, Description: "Heladera"}},
		{"pago luz 30000 mp", Fields{Amount: 30000, PaymentMethodID: mp.ID, Description: "Pago luz"}},
		{"antes de ayer uber 4500", Fields{Amount: 4500, SpentAt: day(2026, 11, 10), Description: "Uber"}},
		{"verdulería 5000 hoy 25/12", Fields{Amount: 5000, SpentAt: today, Description: "Verdulería 25/12"}},
	} {
		t.Run(c.in, func(t *testing.T) {
			got := parseRules(c.in, today, methods)
			if !got.amountClear {
				t.Fatalf("amount not clear: %+v", got)
			}
			if !got.SpentAt.Equal(c.want.SpentAt) {
				t.Errorf("date = %v, want %v", got.SpentAt, c.want.SpentAt)
			}
			got.SpentAt, c.want.SpentAt = time.Time{}, time.Time{}
			if got.Candidates != nil {
				t.Errorf("candidates %v", got.Candidates)
			}
			if got.Fields.Amount != c.want.Amount || got.Currency != c.want.Currency || got.Installments != c.want.Installments ||
				got.PaymentMethodID != c.want.PaymentMethodID || got.Shared != c.want.Shared || got.Description != c.want.Description {
				t.Errorf("got  %+v\nwant %+v", got.Fields, c.want)
			}
		})
	}
}

func TestBareNetworkPicksNothing(t *testing.T) {
	got := parseRules("café 2500 visa", today, methods)
	if got.PaymentMethodID != uuid.Nil || !slices.Contains(got.Candidates, visaGalicia.ID) ||
		!slices.Contains(got.Candidates, visaSantander.ID) || len(got.Candidates) != 2 {
		t.Fatalf("got %+v", got.Fields)
	}
	if got.Description != "Café" {
		t.Errorf("description %q", got.Description)
	}
	// One Visa only: no doubt.
	got = parseRules("café 2500 visa", today, []Method{visaGalicia, master})
	if got.PaymentMethodID != visaGalicia.ID {
		t.Fatalf("got %+v", got.Fields)
	}
	// The bank alone, with two methods of that bank.
	got = parseRules("café 2500 galicia", today, methods)
	if got.PaymentMethodID != uuid.Nil || len(got.Candidates) != 2 {
		t.Fatalf("got %+v", got.Fields)
	}
}

func TestUnclearAmount(t *testing.T) {
	for _, in := range []string{
		"dos mil quinientos de café",
		"café",
		"2 cafés 1500 y 800",
	} {
		if got := parseRules(in, today, methods); got.amountClear {
			t.Errorf("%q: amount %v", in, got.Amount)
		}
	}
	// An explicit one wins over a bare number.
	got := parseRules("2 cafés 3 mil", today, methods)
	if !got.amountClear || got.Amount != 3000 || got.Description != "2 cafés" {
		t.Errorf("got %+v", got.Fields)
	}
}

func TestDates(t *testing.T) {
	for in, want := range map[string]time.Time{
		"x 1 jueves":     day(2026, 11, 5), // today is Thursday: a week ago
		"x 1 el viernes": day(2026, 11, 6),
		"x 1 el 12":      today,
		"x 1 el 13":      day(2026, 10, 13),
		"x 1 31/12":      day(2025, 12, 31),
		"x 1 1/1/2026":   day(2026, 1, 1),
		"x 1 anteayer":   day(2026, 11, 10),
	} {
		if got := parseRules(in, today, nil); !got.SpentAt.Equal(want) {
			t.Errorf("%q: %v, want %v", in, got.SpentAt, want)
		}
	}
	for _, in := range []string{"x 1 31/02", "x 1 1/1/2027", "x 1 el 31 cuotas"} {
		if got := parseRules(in, day(2026, 3, 5), nil); !got.SpentAt.IsZero() {
			t.Errorf("%q: date %v", in, got.SpentAt)
		}
	}
}

func TestGenericWordsDoNotPickAMethod(t *testing.T) {
	got := parseRules("cuenta del gas 9000", today, []Method{{ID: uuid.New(), Name: "Cuenta DNI", Kind: "wallet", Bank: "Banco Provincia"}})
	if got.PaymentMethodID != uuid.Nil || got.Description != "Cuenta del gas" {
		t.Fatalf("got %+v", got.Fields)
	}
}

func TestCardNetworkNamesTheCard(t *testing.T) {
	galicia := Method{ID: uuid.New(), Name: "Galicia", Kind: "credit", Bank: "Galicia", Alias: "Galicia", Network: "mastercard"}
	other := Method{ID: uuid.New(), Name: "Naranja X", Kind: "credit", Alias: "Naranja X", Network: "other"}
	got := parseRules("libros 7000 master", today, []Method{galicia, other, efectivo})
	if got.PaymentMethodID != galicia.ID || got.Description != "Libros" {
		t.Fatalf("got %+v", got.Fields)
	}
}
