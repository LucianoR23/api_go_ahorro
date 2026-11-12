package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeAPI answers every request with status and body, and keeps the last
// request's headers and decoded body.
type fakeAPI struct {
	srv    *httptest.Server
	header http.Header
	path   string
	body   map[string]any
}

func newFakeAPI(t *testing.T, status int, body string) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.header, f.path = r.Header.Clone(), r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &f.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

var schema = json.RawMessage(`{"type":"object","properties":{"amount":{"type":"number"}},"required":["amount"],"additionalProperties":false}`)

func dig(m map[string]any, path ...any) any {
	var cur any = m
	for _, p := range path {
		switch k := p.(type) {
		case string:
			cur = cur.(map[string]any)[k]
		case int:
			cur = cur.([]any)[k]
		}
	}
	return cur
}

func TestAnthropicRequestAndUsage(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t, 200, `{"content":[{"type":"text","text":"{\"amount\":2500}"}],"stop_reason":"end_turn","usage":{"input_tokens":812,"output_tokens":14}}`)
	a := NewAnthropic("sk-test", f.srv.URL, nil)
	img := []byte{0x89, 'P', 'N', 'G'}
	out, err := a.Generate(context.Background(), Call{
		Model: "claude-haiku-5-5", Effort: "low", System: "Extract the expense.",
		Parts:  []Part{TextPart("café 2500"), MediaPart("image/png", img)},
		Schema: schema, MaxOutputTokens: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != `{"amount":2500}` || out.InputTokens != 812 || out.OutputTokens != 14 || out.Truncated {
		t.Errorf("output = %+v", out)
	}
	if f.path != "/v1/messages" || f.header.Get("x-api-key") != "sk-test" || f.header.Get("anthropic-version") != anthropicVersion {
		t.Errorf("path %s headers %v", f.path, f.header)
	}
	b := f.body
	if b["model"] != "claude-haiku-5-5" || b["max_tokens"] != 300.0 || b["system"] != "Extract the expense." {
		t.Errorf("body = %v", b)
	}
	if dig(b, "thinking", "type") != "disabled" || dig(b, "output_config", "effort") != "low" {
		t.Errorf("thinking/effort = %v / %v", b["thinking"], b["output_config"])
	}
	if dig(b, "output_config", "format", "type") != "json_schema" || dig(b, "output_config", "format", "schema", "required", 0) != "amount" {
		t.Errorf("format = %v", dig(b, "output_config", "format"))
	}
	if dig(b, "messages", 0, "content", 0, "text") != "café 2500" ||
		dig(b, "messages", 0, "content", 1, "type") != "image" ||
		dig(b, "messages", 0, "content", 1, "source", "data") != base64.StdEncoding.EncodeToString(img) {
		t.Errorf("content = %v", dig(b, "messages", 0, "content"))
	}
}

func TestAnthropicPDFAndThinking(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t, 200, `{"content":[{"type":"text","text":"{\"amount\""}],"stop_reason":"max_tokens","usage":{"input_tokens":3000,"output_tokens":600}}`)
	a := NewAnthropic("k", f.srv.URL, nil)
	out, err := a.Generate(context.Background(), Call{
		Model: "claude-sonnet-5-5", Thinking: "adaptive",
		Parts: []Part{MediaPart("application/pdf", []byte("%PDF"))}, Schema: schema, MaxOutputTokens: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated {
		t.Error("max_tokens should mark the answer as cut")
	}
	if dig(f.body, "messages", 0, "content", 0, "type") != "document" {
		t.Errorf("pdf block = %v", dig(f.body, "messages", 0, "content", 0))
	}
	// adaptive: no thinking field, and room for it in max_tokens.
	if _, ok := f.body["thinking"]; ok || f.body["max_tokens"] != float64(600+thinkingRoom) {
		t.Errorf("thinking %v max_tokens %v", f.body["thinking"], f.body["max_tokens"])
	}
}

func TestAnthropicErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status    int
		retryable bool
	}{{529, true}, {429, true}, {500, true}, {400, false}, {401, false}}
	for _, c := range cases {
		f := newFakeAPI(t, c.status, `{"type":"error","error":{"type":"x","message":"nope"}}`)
		_, err := NewAnthropic("k", f.srv.URL, nil).Generate(context.Background(), Call{Model: "m", Parts: []Part{TextPart("x")}, MaxOutputTokens: 10})
		var pe *ProviderError
		if !errors.As(err, &pe) || pe.Retryable != c.retryable || pe.Status != c.status {
			t.Errorf("status %d: err = %v", c.status, err)
		}
	}
	_, err := NewAnthropic("k", "http://unused", nil).Generate(context.Background(), Call{Parts: []Part{MediaPart("audio/webm", []byte{1})}})
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("audio to Claude: err = %v, want ErrBadRequest", err)
	}
	_, err = NewAnthropic("", "http://unused", nil).Generate(context.Background(), Call{Parts: []Part{TextPart("x")}})
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Errorf("no key: err = %v", err)
	}
}

func TestGeminiRequestAndUsage(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t, 200, `{"id":"v1_x","status":"completed","steps":[{"type":"thought"},{"type":"model_output","content":[{"type":"text","text":"{\"amount\":15000}"}]}],
		"usage":{"total_input_tokens":1100,"input_tokens_by_modality":[{"modality":"text","tokens":140},{"modality":"AUDIO","tokens":960}],"total_output_tokens":12,"total_thought_tokens":30}}`)
	g := NewGemini("g-key", f.srv.URL, nil)
	audio := []byte("OggS")
	out, err := g.Generate(context.Background(), Call{
		Model: "gemini-3.5-flash-lite", Effort: "minimal", System: "Extract the expense.",
		Parts: []Part{TextPart("audio:"), MediaPart("audio/ogg", audio)}, Schema: schema, MaxOutputTokens: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != `{"amount":15000}` || out.InputTokens != 1100 || out.AudioTokens != 960 || out.OutputTokens != 42 {
		t.Errorf("output = %+v", out)
	}
	if f.path != "/v1beta/interactions" || f.header.Get("x-goog-api-key") != "g-key" {
		t.Errorf("path %s headers %v", f.path, f.header)
	}
	b := f.body
	if b["store"] != false || b["model"] != "gemini-3.5-flash-lite" || b["system_instruction"] != "Extract the expense." {
		t.Errorf("body = %v", b)
	}
	if dig(b, "generation_config", "thinking_level") != "minimal" || dig(b, "generation_config", "max_output_tokens") != float64(300+geminiThinkingRoom) {
		t.Errorf("generation_config = %v", b["generation_config"])
	}
	if dig(b, "response_format", "mime_type") != "application/json" || dig(b, "response_format", "schema", "type") != "object" {
		t.Errorf("response_format = %v", b["response_format"])
	}
	if dig(b, "input", 1, "type") != "audio" || dig(b, "input", 1, "mime_type") != "audio/ogg" ||
		dig(b, "input", 1, "data") != base64.StdEncoding.EncodeToString(audio) {
		t.Errorf("input = %v", b["input"])
	}
}

func TestGeminiErrors(t *testing.T) {
	t.Parallel()
	f := newFakeAPI(t, 503, `{"error":{"code":503,"message":"overloaded","status":"UNAVAILABLE"}}`)
	_, err := NewGemini("k", f.srv.URL, nil).Generate(context.Background(), Call{Model: "m", Parts: []Part{TextPart("x")}, MaxOutputTokens: 10})
	var pe *ProviderError
	if !errors.As(err, &pe) || !pe.Retryable {
		t.Errorf("503: err = %v", err)
	}
	f = newFakeAPI(t, 200, `{"status":"incomplete","steps":[],"usage":{"total_input_tokens":5,"total_output_tokens":0}}`)
	out, err := NewGemini("k", f.srv.URL, nil).Generate(context.Background(), Call{Model: "m", Parts: []Part{TextPart("x")}, MaxOutputTokens: 10})
	if err != nil || !out.Truncated {
		t.Errorf("incomplete: out %+v err %v", out, err)
	}
	_, err = NewGemini("k", "http://unused", nil).Generate(context.Background(), Call{Parts: []Part{MediaPart("application/pdf", []byte{1})}})
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("pdf to Gemini: err = %v", err)
	}
}
