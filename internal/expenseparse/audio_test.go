package expenseparse

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/ai"
)

func TestAudioType(t *testing.T) {
	for in, want := range map[string]string{
		"audio/webm;codecs=opus": "audio/webm",
		"audio/mp4":              "audio/m4a",
		"audio/ogg; codecs=opus": "audio/ogg",
		"AUDIO/WEBM":             "audio/webm",
		"video/webm":             "",
		"application/json":       "",
		"":                       "",
	} {
		got, ok := AudioType(in)
		if got != want || ok != (want != "") {
			t.Errorf("%q -> %q %v, want %q", in, got, ok, want)
		}
	}
}

var note = []byte("not really opus")

func TestAudioTranscriptIsReadByTheRules(t *testing.T) {
	// The AI hears "2500" but says 3500: the transcript the person sees
	// wins, so a correction typed in the box reads the same.
	f := &fakeAI{answer: `{"intent":"expense","transcript":"  café   2500 con la visa galicia ayer ","amount":3500,
		"currency":"","date":"2026-11-11","installments":0,"payment":"visa galicia","description":"café","shared":false}`}
	c := &fakeCats{id: uuid.New()}
	res, err := newParser(f, c, at(today)).ParseAudio(context.Background(), user, hh, "audio/webm;codecs=opus", note)
	if err != nil {
		t.Fatal(err)
	}
	if res.Transcript != "café 2500 con la visa galicia ayer" || !res.Understood || res.Amount != 2500 ||
		res.PaymentMethodID != visaGalicia.ID || res.Description != "Café" || !res.SpentAt.Equal(day(2026, 11, 11)) ||
		res.CategoryID != c.id || res.Source != SourceAI {
		t.Fatalf("res %+v", res)
	}
	req := f.got[0]
	if req.Task != ai.TaskParseAudio || req.Channel != ai.ChannelApp || req.PromptVersion != "parse_audio.v1" ||
		req.MaxOutputTokens != maxAudioOutputTokens || len(req.Parts) != 2 {
		t.Fatalf("request %+v", req)
	}
	// Only the date and the audio go out, sent as the provider names it.
	if req.Parts[0].Text != "<today>2026-11-12</today>" || req.Parts[1].MIME != "audio/webm" || string(req.Parts[1].Data) != string(note) {
		t.Errorf("parts %+v", req.Parts)
	}
}

func TestAudioAIFillsWhatTheTranscriptLacks(t *testing.T) {
	f := &fakeAI{answer: `{"intent":"expense","transcript":"medio palo de arreglo del auto","amount":500000,
		"currency":"","date":"","installments":0,"payment":"santander","description":"un poema","shared":true}`}
	res, err := newParser(f, nil, at(today)).ParseAudio(context.Background(), user, hh, "audio/mp4", note)
	if err != nil {
		t.Fatal(err)
	}
	// Words not in the transcript are dropped, as for typed sentences.
	if res.Amount != 500000 || !res.Understood || res.PaymentMethodID != uuid.Nil || res.Description != "Medio palo de arreglo del auto" || !res.Shared {
		t.Fatalf("res %+v", res)
	}
	if f.got[0].Parts[1].MIME != "audio/m4a" {
		t.Errorf("mime %q", f.got[0].Parts[1].MIME)
	}
}

func TestAudioNotFinanceKeepsTheTranscript(t *testing.T) {
	f := &fakeAI{answer: `{"intent":"not_finance","transcript":"contame un chiste","amount":0,"currency":"","date":"",
		"installments":0,"payment":"","description":"","shared":false}`}
	res, err := newParser(f, &fakeCats{id: uuid.New()}, at(today)).ParseAudio(context.Background(), user, hh, "audio/ogg", note)
	if err != nil {
		t.Fatal(err)
	}
	if !res.NotFinance || res.Understood || res.Transcript != "contame un chiste" || res.CategoryID != uuid.Nil {
		t.Fatalf("res %+v", res)
	}
}

func TestAudioNotHeard(t *testing.T) {
	for name, f := range map[string]*fakeAI{
		"silence": {answer: `{"intent":"not_finance","transcript":"","amount":0,"currency":"","date":"","installments":0,"payment":"","description":"","shared":false}`},
		"limit":   {err: ai.ErrUserBudget},
		"down":    {err: errors.New("boom")},
		"invalid": {answer: `{"intent":"expense","transcript":"café","amount":-1,"currency":"","date":"","installments":0,"payment":"","description":"","shared":false}`},
		"off":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			res, err := newParser(f, nil, at(today)).ParseAudio(context.Background(), user, hh, "audio/webm", note)
			if err != nil {
				t.Fatal(err)
			}
			if res.Transcript != "" || res.Understood || res.NotFinance {
				t.Fatalf("res %+v", res)
			}
		})
	}
}

func TestAudioLongTranscriptIsCut(t *testing.T) {
	long := strings.Repeat("ñ", 400)
	f := &fakeAI{answer: `{"intent":"expense","transcript":"` + long + `","amount":0,"currency":"","date":"","installments":0,"payment":"","description":"","shared":false}`}
	res, err := newParser(f, nil, at(today)).ParseAudio(context.Background(), user, hh, "audio/webm", note)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(res.Transcript)); n != MaxInputRunes {
		t.Fatalf("transcript has %d runes", n)
	}
}

func TestAudioRejectsOtherTypes(t *testing.T) {
	f := &fakeAI{answer: `{}`}
	if _, err := newParser(f, nil, at(today)).ParseAudio(context.Background(), user, hh, "image/png", note); !errors.Is(err, ErrAudioType) {
		t.Fatalf("err %v", err)
	}
	if len(f.got) != 0 {
		t.Error("AI called")
	}
}

// The amount the rules read clearly is not replaced by the AI's.
func TestMergeKeepsAClearAmount(t *testing.T) {
	res := merge(Result{Fields: Fields{Amount: 2500}, Understood: true}, answer{Amount: 3500}, "café 2500", today, methods)
	if res.Amount != 2500 {
		t.Fatalf("amount %v", res.Amount)
	}
}
