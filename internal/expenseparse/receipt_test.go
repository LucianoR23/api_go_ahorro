package expenseparse

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/ai"
)

// jpeg starts like a real JPEG so the sniff lets it through.
var jpeg = append([]byte("\xFF\xD8\xFF\xE0\x00\x10JFIF\x00"), make([]byte, 64)...)

func TestImageType(t *testing.T) {
	for in, want := range map[string]string{
		"image/jpeg":            "image/jpeg",
		"IMAGE/PNG":             "image/png",
		"image/webp; q=1":       "image/webp",
		"image/gif":             "",
		"image/heic":            "",
		"application/pdf":       "",
		"text/html":             "",
		"":                      "",
		"image/jpeg; charset=x": "image/jpeg",
	} {
		got, ok := ImageType(in)
		if got != want || ok != (want != "") {
			t.Errorf("%q -> %q %v, want %q", in, got, ok, want)
		}
	}
}

func TestReceiptRejectsABodyThatIsNotTheImageItClaims(t *testing.T) {
	f := &fakeAI{answer: `{}`}
	_, err := newParser(f, nil, at(today)).ParseReceipt(context.Background(), user, hh, "image/jpeg", []byte("<html>hola</html>"))
	if !errors.Is(err, ErrImageType) || len(f.got) != 0 {
		t.Fatalf("err %v, calls %d", err, len(f.got))
	}
	if _, err := newParser(f, nil, at(today)).ParseReceipt(context.Background(), user, hh, "image/png", jpeg); !errors.Is(err, ErrImageType) {
		t.Fatalf("png label on a jpeg: %v", err)
	}
}

func TestReceiptTicketIsWrittenAsASentence(t *testing.T) {
	f := &fakeAI{answer: `{"intent":"expense","amount":15430.5,"currency":"ARS","date":"2026-11-11",
		"installments":0,"payment":"visa galicia","description":" Carrefour  Express "}`}
	c := &fakeCats{id: uuid.New()}
	res, err := newParser(f, c, at(today)).ParseReceipt(context.Background(), user, hh, "image/jpeg", jpeg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Transcript != "Carrefour Express 15430,50 visa galicia ayer" || !res.Understood || res.Amount != 15430.5 ||
		res.Currency != "ARS" || res.PaymentMethodID != visaGalicia.ID || res.Description != "Carrefour Express" ||
		!res.SpentAt.Equal(day(2026, 11, 11)) || res.CategoryID != c.id || res.Source != SourceAI || res.Shared {
		t.Fatalf("res %+v", res)
	}
	req := f.got[0]
	if req.Task != ai.TaskParseReceipt || req.Channel != ai.ChannelApp || req.PromptVersion != "parse_receipt.v1" ||
		req.MaxOutputTokens != maxOutputTokens || len(req.Parts) != 2 {
		t.Fatalf("request %+v", req)
	}
	if req.Parts[0].Text != "<today>2026-11-12</today>" || req.Parts[1].MIME != "image/jpeg" || len(req.Parts[1].Data) != len(jpeg) {
		t.Errorf("parts %+v", req.Parts)
	}

	// The sentence typed back into the box reads the same without the AI.
	again, err := newParser(nil, nil, at(today)).Parse(context.Background(), user, hh, res.Transcript)
	if err != nil {
		t.Fatal(err)
	}
	if again.Amount != res.Amount || again.PaymentMethodID != res.PaymentMethodID || !again.SpentAt.Equal(res.SpentAt) ||
		again.Description != res.Description {
		t.Errorf("typed back %+v", again)
	}
}

func TestReceiptSentence(t *testing.T) {
	for _, tc := range []struct {
		a    answer
		want string
	}{
		{answer{Amount: 20000, Description: "Juan Pérez", Payment: "mercado pago", Date: "2026-11-12"}, "Juan Pérez 20000 mercado pago"},
		{answer{Amount: 50, Currency: "USD", Description: "Amazon", Date: "2026-03-05"}, "Amazon u$s 50 5/3"},
		{answer{Amount: 12.99, Currency: "EUR", Description: "Bar", Date: "2025-12-30"}, "Bar 12,99 euros 30/12/2025"},
		{answer{Amount: 90000, Installments: 3, Description: "Frávega", Payment: "visa"}, "Frávega 90000 en 3 cuotas visa"},
		{answer{Amount: 1000, Installments: 1, Date: "2026-12-01"}, "1000"},
		{answer{}, ""},
	} {
		if got := receiptSentence(tc.a, today); got != tc.want {
			t.Errorf("%+v -> %q, want %q", tc.a, got, tc.want)
		}
	}
}

func TestReceiptNotFinanceAndFailures(t *testing.T) {
	f := &fakeAI{answer: `{"intent":"not_finance","amount":0,"currency":"","date":"","installments":0,"payment":"","description":""}`}
	res, err := newParser(f, nil, at(today)).ParseReceipt(context.Background(), user, hh, "image/jpeg", jpeg)
	if err != nil || !res.NotFinance || res.Transcript != "" {
		t.Fatalf("not finance: %+v %v", res, err)
	}
	// A provider failure or an answer out of range leaves the form to the person.
	for _, f := range []*fakeAI{
		{err: ai.ErrRateLimited},
		{answer: `{"intent":"expense","amount":-5,"currency":"","date":"","installments":0,"payment":"","description":"x"}`},
	} {
		res, err := newParser(f, nil, at(today)).ParseReceipt(context.Background(), user, hh, "image/jpeg", jpeg)
		if err != nil || res.Understood || res.Transcript != "" {
			t.Errorf("%+v: %+v %v", f, res, err)
		}
	}
	// Nothing read: no call when the AI is off or the body is empty.
	if res, err := newParser(nil, nil, at(today)).ParseReceipt(context.Background(), user, hh, "image/jpeg", jpeg); err != nil || res.Understood {
		t.Errorf("AI off: %+v %v", res, err)
	}
}
