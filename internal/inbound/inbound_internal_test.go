package inbound

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/LucianoR23/valuo_api/internal/expenseparse"
)

var ar = time.FixedZone("ART", -3*3600)

func testKeys(t *testing.T) *Keys {
	t.Helper()
	k, err := NewKeys(bytes.Repeat([]byte("h"), 32), bytes.Repeat([]byte("e"), 32))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestKeys(t *testing.T) {
	if _, err := NewKeys([]byte("short"), bytes.Repeat([]byte("e"), 32)); err == nil {
		t.Error("short HMAC key accepted")
	}
	if _, err := NewKeys(bytes.Repeat([]byte("h"), 32), []byte("short")); err == nil {
		t.Error("short encryption key accepted")
	}
	k := testKeys(t)
	a, b := k.Seal("u_abc"), k.Seal("u_abc")
	if bytes.Equal(a, b) {
		t.Error("Seal is deterministic")
	}
	if got, err := k.Open(a); err != nil || got != "u_abc" {
		t.Errorf("Open = %q, %v", got, err)
	}
	a[len(a)-1] ^= 1
	if _, err := k.Open(a); err == nil {
		t.Error("tampered value opened")
	}
	if !bytes.Equal(k.Hash("x"), k.Hash("x")) || bytes.Equal(k.Hash("x"), k.Hash("y")) {
		t.Error("Hash is not a stable lookup")
	}
	// Signing and indexing use different keys.
	if bytes.Equal(k.Sign("1", "a", "b", nil), k.Hash("1\na\nb\n")) {
		t.Error("webhook and index keys are the same")
	}
	if l := newLocal(); !strings.HasPrefix(l, "u_") || len(l) != 18 || l == newLocal() {
		t.Errorf("newLocal = %q", l)
	}
}

func TestNormalizeSender(t *testing.T) {
	for in, want := range map[string]string{
		"Ana.Perez+caf_=u_x=in.example@GMAIL.com": "anaperez@gmail.com",
		"ana@googlemail.com":                      "ana@gmail.com",
		"ana.perez+x@outlook.com":                 "ana.perez@outlook.com",
		" juan@example.test ":                     "juan@example.test",
	} {
		if got := normalizeSender(in); got != want {
			t.Errorf("normalizeSender(%q) = %q, want %q", in, got, want)
		}
	}
}

const mpPlain = "From: Mercado Pago <info@mercadopago.com>\r\n" +
	"To: ana@gmail.com\r\n" +
	"Subject: =?UTF-8?Q?Le_pagaste_a_Carrefour?=\r\n" +
	"Date: Thu, 12 Nov 2026 21:10:00 -0300\r\n" +
	"Message-ID: <mp-1@mercadopago.com>\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
	"Hola Ana,\r\nLe pagaste $ 15.430,50 a CARREFOUR S.A. con dinero en cuenta.\r\n" +
	"Operaci=C3=B3n 123456789\r\n"

func TestParseMail(t *testing.T) {
	m, err := ParseMail([]byte(mpPlain))
	if err != nil {
		t.Fatal(err)
	}
	if m.From != "info@mercadopago.com" || m.Subject != "Le pagaste a Carrefour" || m.MessageID != "<mp-1@mercadopago.com>" {
		t.Errorf("headers %+v", m)
	}
	if !strings.Contains(m.Text, "Le pagaste $ 15.430,50 a CARREFOUR S.A.") || !strings.Contains(m.Text, "Operación") {
		t.Errorf("text %q", m.Text)
	}

	html := "From: Galicia <avisos@bancogalicia.com.ar>\r\nSubject: Consumo con tu tarjeta\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=b1\r\n\r\n" +
		"--b1\r\nContent-Type: text/html; charset=iso-8859-1\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
		"PGh0bWw+PHN0eWxlPi54e308L3N0eWxlPjxwPkNvbXByYSBkZSAkIDIuNTAwIGVuIEZhcm1hY2l0eTwv\r\ncD48cD5UYXJqZXRhIFZpc2EgQ3LpZGl0bzwvcD48L2h0bWw+\r\n" +
		"--b1--\r\n"
	m, err = ParseMail([]byte(html))
	if err != nil {
		t.Fatal(err)
	}
	if m.Text != "Compra de $ 2.500 en Farmacity\nTarjeta Visa Crédito" {
		t.Errorf("html text %q", m.Text)
	}

	fwd := "From: Ana <ana@gmail.com>\r\nSubject: Fwd: Pagaste\r\nContent-Type: text/plain\r\n\r\n" +
		"---------- Forwarded message ---------\r\nDe: Mercado Pago <info@mercadopago.com>\r\nDate: hoy\r\n\r\nPagaste $ 900 a Kiosco\r\n"
	if m, _ = ParseMail([]byte(fwd)); m.From != "info@mercadopago.com" {
		t.Errorf("forwarded From = %q", m.From)
	}
	if _, err := ParseMail([]byte("not a mail")); err == nil {
		t.Error("garbage parsed")
	}
}

func TestReadRules(t *testing.T) {
	date := time.Date(2026, 11, 12, 21, 10, 0, 0, ar)
	cases := []struct {
		name string
		mail Mail
		want expenseparse.Reading
		src  string
		v    verdict
	}{
		{
			name: "mercado pago payment",
			mail: Mail{From: "info@mercadopago.com", Subject: "Le pagaste a Carrefour", Date: date,
				Text: "Le pagaste $ 15.430,50 a CARREFOUR S.A. con dinero en cuenta."},
			want: expenseparse.Reading{Amount: 15430.5, Currency: "ARS", Date: "2026-11-12", Payment: "mercado pago", Description: "Carrefour"},
			src:  "mercadopago", v: verdictExpense,
		},
		{
			name: "galicia card purchase in installments",
			mail: Mail{From: "avisos@mails.bancogalicia.com.ar", Subject: "Consumo con tu tarjeta",
				Text: "Compra de $ 120.000 en Garbarino\nTarjeta Visa Crédito en 6 cuotas\nFecha: 10/11/26"},
			want: expenseparse.Reading{Amount: 120000, Currency: "ARS", Date: "2026-11-10", Installments: 6, Payment: "visa galicia", Description: "Garbarino"},
			src:  "galicia", v: verdictExpense,
		},
		{
			name: "brubank debit in dollars",
			mail: Mail{From: "no-reply@brubank.com", Subject: "Hiciste una compra",
				Text: "Comercio: Steam\nMonto: US$ 12,99\nVisa Débito"},
			want: expenseparse.Reading{Amount: 12.99, Currency: "USD", Payment: "visa débito", Description: "Steam"},
			src:  "brubank", v: verdictExpense,
		},
		{
			name: "money received is not spent",
			mail: Mail{From: "info@mercadopago.com", Subject: "Recibiste dinero", Text: "Juan te transfirió $ 5.000"},
			src:  "mercadopago", v: verdictNotSpent,
		},
		{
			name: "a summary is not a payment",
			mail: Mail{From: "resumen@santander.com.ar", Subject: "Tu resumen de cuenta ya está disponible", Text: "Total $ 300.000"},
			src:  "santander", v: verdictNotSpent,
		},
		{
			name: "known sender without an amount goes to the AI",
			mail: Mail{From: "info@bbva.com.ar", Subject: "Novedades", Text: "Conocé tu nueva app"},
			src:  "bbva", v: verdictUnknown,
		},
		{
			name: "unknown sender goes to the AI",
			mail: Mail{From: "facturas@edenor.com", Subject: "Tu factura", Text: "Pagaste $ 18.000"},
			v:    verdictUnknown,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, src, v := readRules(c.mail, ar)
			if v != c.v || src != c.src || got != c.want {
				t.Errorf("got %+v %q %v, want %+v %q %v", got, src, v, c.want, c.src, c.v)
			}
		})
	}
}

func TestGmailCode(t *testing.T) {
	if c := gmailCode(Mail{Subject: "(#987654321) Confirmación de reenvío de Gmail"}); c != "987654321" {
		t.Errorf("subject code %q", c)
	}
	if c := gmailCode(Mail{Subject: "Gmail", Text: "Código de confirmación: 1234567"}); c != "1234567" {
		t.Errorf("text code %q", c)
	}
	if c := gmailCode(Mail{Subject: "Hola"}); c != "" {
		t.Errorf("no code %q", c)
	}
}

// TestSignVector pins the webhook signature; workers/inbound-email's test
// checks the same value, so the Worker and the API cannot drift apart.
func TestSignVector(t *testing.T) {
	k, err := NewKeys([]byte("0123456789abcdef0123456789abcdef"), bytes.Repeat([]byte("e"), 32))
	if err != nil {
		t.Fatal(err)
	}
	got := hex.EncodeToString(k.Sign("1763049600", "in+u_abc@in.example.test", "ana@gmail.com", []byte("Subject: hola\r\n\r\ncuerpo")))
	if got != signVector {
		t.Errorf("signature %s, want %s", got, signVector)
	}
}

const signVector = "f177b1952de961cef12086989b66fcb36b6c8c1978ef82931f042afa69590cd7"
