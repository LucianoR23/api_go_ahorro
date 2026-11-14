package inbound_test

// Import by forwarded email (plan 3.5) against the real API and Postgres:
// the Email Worker posts each mail signed; what was read waits as an open
// message until the person confirms or dismisses it. The raw mail is never
// stored.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/LucianoR23/valuo_api/internal/config"
	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/inbound"
	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

var (
	hmacKey = bytes.Repeat([]byte("k"), 32)
	encKey  = bytes.Repeat([]byte("e"), 32)
)

const domain = "in.example.test"

// fakeClaude answers parse_email like the Messages API.
type fakeClaude struct {
	calls  atomic.Int32
	answer string
}

func (f *fakeClaude) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	f.calls.Add(1)
	out, _ := json.Marshal(map[string]any{
		"content":     []map[string]string{{"type": "text", "text": f.answer}},
		"stop_reason": "end_turn",
		"usage":       map[string]int{"input_tokens": 900, "output_tokens": 40},
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

type message struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Status    string  `json:"status"`
	Source    *string `json:"source"`
	Sentence  *string `json:"sentence"`
	Sender    *string `json:"sender"`
	GmailCode *string `json:"gmailCode"`
	Reading   *struct {
		Amount      float64 `json:"amount"`
		Description string  `json:"description"`
	} `json:"reading"`
}

func mail(from, subject, body, id string) []byte {
	return []byte("From: " + from + "\r\nSubject: " + subject + "\r\nMessage-ID: <" + id + ">\r\n" +
		"Date: Thu, 12 Nov 2026 21:10:00 -0300\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n")
}

func TestInboundEmail(t *testing.T) {
	t.Parallel()
	ai := &fakeClaude{answer: `{"intent":"expense","amount":18000,"currency":"ARS","date":"2026-11-10",
		"installments":0,"payment":"","description":"Edenor"}`}
	aiSrv := httptest.NewServer(ai)
	t.Cleanup(aiSrv.Close)
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	s := apitest.New(t, apitest.WithNow(func() time.Time { return now }), apitest.WithConfig(func(c *config.Config) {
		c.InboundDomain = domain
		c.InboundLocalPrefix = "in+"
		c.InboundHMACKey = base64.StdEncoding.EncodeToString(hmacKey)
		c.InboundEncKey = base64.StdEncoding.EncodeToString(encKey)
		c.AIEnabled = true
		c.AnthropicAPIKey = "sk-test"
		c.AnthropicBaseURL = aiSrv.URL
	}))
	keys, err := inbound.NewKeys(hmacKey, encKey)
	if err != nil {
		t.Fatal(err)
	}
	u := s.Register(t, apitest.RegisterOpts{Email: "ana.perez@gmail.com", Verify: true})
	other := s.Register(t)

	var addr struct {
		Address string `json:"address"`
	}
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/inbound/address", Token: u.Token}).Status(t, http.StatusOK).JSON(t, &addr)
	if !strings.HasPrefix(addr.Address, "in+u_") || !strings.HasSuffix(addr.Address, "@"+domain) {
		t.Fatalf("address %q", addr.Address)
	}
	var again struct{ Address string }
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/inbound/address", Token: u.Token}).Status(t, http.StatusOK).JSON(t, &again)
	if again.Address != addr.Address {
		t.Errorf("address changed: %q", again.Address)
	}

	post := func(t *testing.T, to, from string, raw []byte, at time.Time, sig []byte) *apitest.Res {
		t.Helper()
		ts := strconv.FormatInt(at.Unix(), 10)
		if sig == nil {
			sig = keys.Sign(ts, to, from, raw)
		}
		return s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/inbound/email", Body: raw, Header: http.Header{
			"Content-Type":          {"message/rfc822"},
			inbound.HeaderTo:        {to},
			inbound.HeaderFrom:      {from},
			inbound.HeaderTimestamp: {ts},
			inbound.HeaderSignature: {hex.EncodeToString(sig)},
		}})
	}
	// Gmail forwards with the account address plus a tag.
	gmailFrom := "ana.perez+caf_=" + strings.ReplaceAll(addr.Address, "@", "=") + "@gmail.com"
	list := func(t *testing.T, token string) []message {
		t.Helper()
		var out []message
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/inbound/messages", Token: token}).Status(t, http.StatusOK).JSON(t, &out)
		return out
	}

	t.Run("signature, age and size are checked", func(t *testing.T) {
		raw := mail("info@mercadopago.com", "x", "Pagaste $ 1 a X", "sig@x")
		post(t, addr.Address, gmailFrom, raw, now, []byte("bad")).Status(t, http.StatusUnauthorized)
		post(t, addr.Address, gmailFrom, raw, now.Add(-10*time.Minute), nil).Status(t, http.StatusUnauthorized)
		big := append(bytes.Clone(raw), bytes.Repeat([]byte("a"), inbound.MaxMailBytes)...)
		post(t, addr.Address, gmailFrom, big, now, nil).Status(t, http.StatusRequestEntityTooLarge)
		// Unknown address or other domain: accepted and dropped.
		post(t, "in+u_nobody@"+domain, gmailFrom, raw, now, nil).Status(t, http.StatusNoContent)
		post(t, "u_x@other.test", gmailFrom, raw, now, nil).Status(t, http.StatusNoContent)
		if got := list(t, u.Token); len(got) != 0 {
			t.Errorf("messages %+v", got)
		}
	})

	t.Run("the bare token reaches the same person", func(t *testing.T) {
		bare := strings.TrimPrefix(addr.Address, "in+")
		raw := mail("Gmail <forwarding-noreply@google.com>", "(#111111111) Gmail Forwarding Confirmation", "x", "gm-bare@google")
		post(t, strings.ToUpper(bare), "forwarding-noreply@google.com", raw, now, nil).Status(t, http.StatusNoContent)
		got := list(t, u.Token)
		if len(got) != 1 || *got[0].GmailCode != "111111111" {
			t.Fatalf("got %+v", got)
		}
		if _, err := s.Pool.Exec(context.Background(), `DELETE FROM inbound_messages WHERE user_id = $1`, u.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("gmail's confirmation code is shown", func(t *testing.T) {
		raw := mail("Gmail <forwarding-noreply@google.com>", "(#987654321) Confirmación de reenvío de Gmail", "Hola", "gm@google")
		post(t, addr.Address, "forwarding-noreply@google.com", raw, now, nil).Status(t, http.StatusNoContent)
		got := list(t, u.Token)
		if len(got) != 1 || got[0].Kind != "gmail_confirmation" || got[0].GmailCode == nil || *got[0].GmailCode != "987654321" {
			t.Fatalf("got %+v", got)
		}
	})

	var mpID string
	t.Run("a mercado pago mail is read by the rules", func(t *testing.T) {
		raw := mail("Mercado Pago <info@mercadopago.com>", "Le pagaste a Carrefour", "Le pagaste $ 15.430,50 a CARREFOUR S.A.", "mp-1@mp")
		post(t, addr.Address, gmailFrom, raw, now, nil).Status(t, http.StatusNoContent)
		// The Worker retries: the same mail is not listed twice.
		post(t, addr.Address, gmailFrom, raw, now, nil).Status(t, http.StatusNoContent)
		var exp []message
		for _, m := range list(t, u.Token) {
			if m.Kind == "expense" {
				exp = append(exp, m)
			}
		}
		if len(exp) != 1 || exp[0].Status != "pending" || *exp[0].Source != "mercadopago" ||
			*exp[0].Sentence != "Carrefour 15430,50 mercado pago ayer" || exp[0].Reading.Amount != 15430.5 {
			t.Fatalf("got %+v", exp)
		}
		mpID = exp[0].ID
		if ai.calls.Load() != 0 {
			t.Errorf("AI called %d times", ai.calls.Load())
		}
		var n int
		if err := s.Pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM inbound_messages WHERE parsed::text LIKE '%S.A.%' OR parsed::text LIKE '%Le pagaste%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Error("raw mail text stored")
		}
	})

	t.Run("an unknown sender's mail goes to the AI", func(t *testing.T) {
		raw := mail("Edenor <facturas@edenor.com>", "Tu pago", "Registramos tu pago de la factura", "ede-1@edenor")
		post(t, addr.Address, gmailFrom, raw, now, nil).Status(t, http.StatusNoContent)
		if ai.calls.Load() != 1 {
			t.Fatalf("AI calls %d", ai.calls.Load())
		}
		var task, channel string
		if err := s.Pool.QueryRow(context.Background(),
			`SELECT task, channel FROM ai_usage WHERE user_id = $1`, u.ID).Scan(&task, &channel); err != nil {
			t.Fatal(err)
		}
		if task != "parse_email" || channel != "email" {
			t.Errorf("ai_usage %s %s", task, channel)
		}
		found := false
		for _, m := range list(t, u.Token) {
			if m.Source != nil && *m.Source == "ai" && *m.Sentence == "Edenor 18000 10/11" {
				found = true
			}
		}
		if !found {
			t.Errorf("AI reading not listed: %+v", list(t, u.Token))
		}
	})

	t.Run("another sender must be accepted, and never spends AI", func(t *testing.T) {
		calls := ai.calls.Load()
		raw := mail("Edenor <facturas@edenor.com>", "Tu pago", "Pago registrado", "ede-2@edenor")
		post(t, addr.Address, "Otra.Casilla@outlook.com", raw, now, nil).Status(t, http.StatusNoContent)
		if ai.calls.Load() != calls {
			t.Error("unknown sender spent AI")
		}
		var pending *message
		for _, m := range list(t, u.Token) {
			if m.Status == "sender_pending" {
				pending = &m
			}
		}
		if pending == nil || pending.Sender == nil || *pending.Sender != "otra.casilla@outlook.com" {
			t.Fatalf("got %+v", list(t, u.Token))
		}
		// Another person cannot act on it.
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/inbound/messages/" + pending.ID + "/accept-sender", Token: other.Token}).
			Status(t, http.StatusNotFound)
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/inbound/messages/" + pending.ID + "/accept-sender", Token: u.Token}).
			Status(t, http.StatusNoContent)
		// From now on that sender's mails are read with the AI.
		raw = mail("Edenor <facturas@edenor.com>", "Tu pago", "Pago registrado", "ede-3@edenor")
		post(t, addr.Address, "otra.casilla+x@outlook.com", raw, now, nil).Status(t, http.StatusNoContent)
		if ai.calls.Load() != calls+1 {
			t.Errorf("accepted sender not read: %d", ai.calls.Load()-calls)
		}
		var sealed int
		if err := s.Pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM inbound_messages WHERE id = $1 AND sender_enc IS NULL AND status = 'pending'`, pending.ID).Scan(&sealed); err != nil {
			t.Fatal(err)
		}
		if sealed != 1 {
			t.Error("accepted message keeps the sender")
		}
	})

	t.Run("confirm links the expense, dismiss closes", func(t *testing.T) {
		cash := s.Cash(t, u)
		hh := s.PersonalSpace(t, u)
		e := s.CreateExpense(t, u, hh, map[string]any{"amount": 15430.5, "description": "Carrefour", "paymentMethodId": cash.ID, "spentAt": "2026-11-12"})
		otherCash := s.Cash(t, other)
		foreign := s.CreateExpense(t, other, s.PersonalSpace(t, other), map[string]any{"amount": 1, "description": "x", "paymentMethodId": otherCash.ID, "spentAt": "2026-11-12"})

		path := "/inbound/messages/" + mpID + "/confirm"
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: path, Token: u.Token, Body: map[string]any{"expenseId": foreign.Expense.ID}}).
			Status(t, http.StatusUnprocessableEntity)
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: path, Token: u.Token, Body: map[string]any{"expenseId": e.Expense.ID}}).
			Status(t, http.StatusNoContent)
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: path, Token: u.Token, Body: map[string]any{"expenseId": e.Expense.ID}}).
			Status(t, http.StatusNotFound)
		for _, m := range list(t, u.Token) {
			if m.ID == mpID {
				t.Error("confirmed mail still listed")
			}
			if m.Kind == "expense" {
				s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/inbound/messages/" + m.ID + "/dismiss", Token: u.Token}).
					Status(t, http.StatusNoContent)
			}
		}
		for _, m := range list(t, u.Token) {
			if m.Kind == "expense" {
				t.Errorf("still open: %+v", m)
			}
		}
	})

	t.Run("retention drops codes after a day and resolved mails after 30 days", func(t *testing.T) {
		// One open mail that must stay.
		raw := mail("Mercado Pago <info@mercadopago.com>", "Pagaste", "Pagaste $ 500 a Kiosco", "mp-keep@mp")
		post(t, addr.Address, gmailFrom, raw, now, nil).Status(t, http.StatusNoContent)
		ctx := context.Background()
		if _, err := s.Pool.Exec(ctx, `UPDATE inbound_messages SET received_at = received_at - interval '31 days',
			resolved_at = resolved_at - interval '31 days' WHERE user_id = $1`, u.ID); err != nil {
			t.Fatal(err)
		}
		n, err := sqlcgen.New(s.Pool).DeleteOldInboundMessages(ctx, sqlcgen.DeleteOldInboundMessagesParams{
			CodesBefore:    pgtype.Timestamptz{Time: time.Now().Add(-inbound.CodeRetention), Valid: true},
			ResolvedBefore: pgtype.Timestamptz{Time: time.Now().Add(-inbound.ResolvedRetention), Valid: true},
		})
		if err != nil || n == 0 {
			t.Fatalf("deleted %d, %v", n, err)
		}
		var kinds []string
		rows, err := s.Pool.Query(ctx, `SELECT kind || ':' || status FROM inbound_messages WHERE user_id = $1`, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var k string
			_ = rows.Scan(&k)
			kinds = append(kinds, k)
		}
		if len(kinds) != 1 || kinds[0] != "expense:pending" {
			t.Errorf("left %v", kinds)
		}
	})
}

func TestInboundOff(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	u := s.Register(t)
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/inbound/address", Token: u.Token}).Status(t, http.StatusServiceUnavailable)
	s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/inbound/email", Body: []byte("x")}).Status(t, http.StatusServiceUnavailable)
}
