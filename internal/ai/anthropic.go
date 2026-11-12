package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// anthropicVersion pins the Messages API version. Bump it on purpose, after
// reading the changelog, never implicitly.
const anthropicVersion = "2026-10-21"

// Anthropic calls the Claude Messages API with structured output. It is a
// small stdlib client on purpose: we use one endpoint, and the official SDK
// would add dependencies and a release every few days to keep up with.
type Anthropic struct {
	key     string
	baseURL string
	client  *http.Client
}

// NewAnthropic: baseURL empty means the public API.
func NewAnthropic(key, baseURL string, client *http.Client) *Anthropic {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	if client == nil {
		client = &http.Client{Timeout: callTimeout}
	}
	return &Anthropic{key: key, baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (a *Anthropic) Name() string { return "anthropic" }

type anthropicSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicBlock struct {
	Type   string           `json:"type"`
	Text   string           `json:"text,omitempty"`
	Source *anthropicSource `json:"source,omitempty"`
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []anthropicBlock `json:"content"`
}

type anthropicThinking struct {
	Type string `json:"type"`
}

type anthropicFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

type anthropicOutputConfig struct {
	Effort string           `json:"effort,omitempty"`
	Format *anthropicFormat `json:"format,omitempty"`
}

type anthropicRequest struct {
	Model        string                `json:"model"`
	MaxTokens    int                   `json:"max_tokens"`
	System       string                `json:"system,omitempty"`
	Messages     []anthropicMessage    `json:"messages"`
	Thinking     *anthropicThinking    `json:"thinking,omitempty"`
	OutputConfig anthropicOutputConfig `json:"output_config"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// thinkingRoom is added to max_tokens when thinking is on, because it counts
// toward the limit.
const thinkingRoom = 2048

func (a *Anthropic) Generate(ctx context.Context, c Call) (Output, error) {
	if a.key == "" {
		return Output{}, &ProviderError{Msg: "ANTHROPIC_API_KEY is not set"}
	}
	content := make([]anthropicBlock, 0, len(c.Parts))
	for _, p := range c.Parts {
		if !p.isMedia() {
			content = append(content, anthropicBlock{Type: "text", Text: p.Text})
			continue
		}
		kind := "image"
		switch {
		case p.MIME == "application/pdf":
			kind = "document"
		case strings.HasPrefix(p.MIME, "image/"):
		default:
			return Output{}, fmt.Errorf("%w: anthropic does not take %s", ErrBadRequest, p.MIME)
		}
		content = append(content, anthropicBlock{Type: kind, Source: &anthropicSource{
			Type: "base64", MediaType: p.MIME, Data: base64.StdEncoding.EncodeToString(p.Data),
		}})
	}

	thinking := c.Thinking
	if thinking == "" {
		thinking = "disabled"
	}
	body := anthropicRequest{
		Model:        c.Model,
		MaxTokens:    c.MaxOutputTokens,
		System:       c.System,
		Messages:     []anthropicMessage{{Role: "user", Content: content}},
		OutputConfig: anthropicOutputConfig{Effort: c.Effort},
	}
	if thinking != "adaptive" {
		body.Thinking = &anthropicThinking{Type: thinking}
	}
	if thinking != "disabled" {
		body.MaxTokens += thinkingRoom
	}
	if len(c.Schema) > 0 {
		body.OutputConfig.Format = &anthropicFormat{Type: "json_schema", Schema: c.Schema}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Output{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(raw))
	if err != nil {
		return Output{}, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", a.key)
	req.Header.Set("anthropic-version", anthropicVersion)

	res, err := a.client.Do(req)
	if err != nil {
		return Output{}, transportError(ctx, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return Output{}, transportError(ctx, err)
	}
	var out anthropicResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return Output{}, &ProviderError{Status: res.StatusCode, Retryable: res.StatusCode >= 500, Msg: fmt.Sprintf("status %d, unreadable body", res.StatusCode)}
	}
	if res.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("status %d", res.StatusCode)
		if out.Error != nil {
			msg += ": " + out.Error.Type + ": " + out.Error.Message
		}
		return Output{}, &ProviderError{Status: res.StatusCode, Retryable: retryableStatus(res.StatusCode), Msg: msg}
	}

	var text strings.Builder
	for _, b := range out.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	return Output{
		Text:         text.String(),
		InputTokens:  out.Usage.InputTokens,
		OutputTokens: out.Usage.OutputTokens,
		Truncated:    out.StopReason == "max_tokens" || out.StopReason == "refusal",
	}, nil
}

// retryableStatus: rate limited, overloaded (529) or a server error.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

func transportError(ctx context.Context, err error) error {
	timeout := errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) && ne.Timeout() {
		timeout = true
	}
	// A dropped connection is worth a second try; a timeout is not, it
	// would only double the wait.
	return &ProviderError{Retryable: ctx.Err() == nil && !timeout, Timeout: timeout, Msg: err.Error()}
}
