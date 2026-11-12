package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Gemini calls Google's Interactions API, the interface Google recommends
// for new projects since June 2026 (generateContent is legacy). We use it
// for audio only. Like Anthropic, a small stdlib client: the official Go SDK
// brings in gRPC, OpenCensus and Google Cloud auth for one REST call.
//
// Requests go with store=false so Google keeps no interaction state.
type Gemini struct {
	key     string
	baseURL string
	client  *http.Client
}

// NewGemini: baseURL empty means the public API.
func NewGemini(key, baseURL string, client *http.Client) *Gemini {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	if client == nil {
		client = &http.Client{Timeout: callTimeout}
	}
	return &Gemini{key: key, baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (g *Gemini) Name() string { return "gemini" }

type geminiPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
	ThinkingLevel   string `json:"thinking_level,omitempty"`
}

type geminiResponseFormat struct {
	Type     string          `json:"type"`
	MIMEType string          `json:"mime_type"`
	Schema   json.RawMessage `json:"schema,omitempty"`
}

type geminiRequest struct {
	Model             string                 `json:"model"`
	Input             []geminiPart           `json:"input"`
	SystemInstruction string                 `json:"system_instruction,omitempty"`
	GenerationConfig  geminiGenerationConfig `json:"generation_config"`
	ResponseFormat    *geminiResponseFormat  `json:"response_format,omitempty"`
	Store             bool                   `json:"store"`
}

type geminiModality struct {
	Modality string `json:"modality"`
	Tokens   int    `json:"tokens"`
}

type geminiResponse struct {
	Status string `json:"status"`
	Steps  []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"steps"`
	Usage struct {
		TotalInputTokens      int              `json:"total_input_tokens"`
		InputTokensByModality []geminiModality `json:"input_tokens_by_modality"`
		TotalOutputTokens     int              `json:"total_output_tokens"`
		TotalThoughtTokens    int              `json:"total_thought_tokens"`
	} `json:"usage"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// geminiThinkingRoom: thinking tokens share the output limit.
const geminiThinkingRoom = 1024

func (g *Gemini) Generate(ctx context.Context, c Call) (Output, error) {
	if g.key == "" {
		return Output{}, &ProviderError{Msg: "GEMINI_API_KEY is not set"}
	}
	input := make([]geminiPart, 0, len(c.Parts))
	for _, p := range c.Parts {
		if !p.isMedia() {
			input = append(input, geminiPart{Type: "text", Text: p.Text})
			continue
		}
		kind := strings.SplitN(p.MIME, "/", 2)[0]
		if kind != "audio" && kind != "image" {
			return Output{}, fmt.Errorf("%w: gemini route does not take %s", ErrBadRequest, p.MIME)
		}
		input = append(input, geminiPart{Type: kind, Data: base64.StdEncoding.EncodeToString(p.Data), MIMEType: p.MIME})
	}
	body := geminiRequest{
		Model:             c.Model,
		Input:             input,
		SystemInstruction: c.System,
		GenerationConfig: geminiGenerationConfig{
			MaxOutputTokens: c.MaxOutputTokens + geminiThinkingRoom,
			ThinkingLevel:   c.Effort,
		},
	}
	if len(c.Schema) > 0 {
		body.ResponseFormat = &geminiResponseFormat{Type: "text", MIMEType: "application/json", Schema: c.Schema}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Output{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/v1beta/interactions", bytes.NewReader(raw))
	if err != nil {
		return Output{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.key)

	res, err := g.client.Do(req)
	if err != nil {
		return Output{}, transportError(ctx, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return Output{}, transportError(ctx, err)
	}
	var out geminiResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return Output{}, &ProviderError{Status: res.StatusCode, Retryable: res.StatusCode >= 500, Msg: fmt.Sprintf("status %d, unreadable body", res.StatusCode)}
	}
	if res.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("status %d", res.StatusCode)
		if out.Error != nil {
			msg += ": " + out.Error.Status + ": " + out.Error.Message
		}
		return Output{}, &ProviderError{Status: res.StatusCode, Retryable: retryableStatus(res.StatusCode), Msg: msg}
	}

	var text strings.Builder
	for _, s := range out.Steps {
		if s.Type != "model_output" {
			continue
		}
		for _, c := range s.Content {
			if c.Type == "text" {
				text.WriteString(c.Text)
			}
		}
	}
	audio := 0
	for _, m := range out.Usage.InputTokensByModality {
		if strings.EqualFold(m.Modality, "audio") {
			audio += m.Tokens
		}
	}
	return Output{
		Text:         text.String(),
		InputTokens:  out.Usage.TotalInputTokens,
		AudioTokens:  audio,
		OutputTokens: out.Usage.TotalOutputTokens + out.Usage.TotalThoughtTokens,
		Truncated:    out.Status == "incomplete",
	}, nil
}
