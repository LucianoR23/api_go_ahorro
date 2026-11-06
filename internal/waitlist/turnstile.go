package waitlist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TurnstileVerifier checks a Cloudflare Turnstile token and returns the
// hostname the widget ran on. Tests use a fake.
type TurnstileVerifier interface {
	Verify(ctx context.Context, token, remoteIP string) (hostname string, ok bool, err error)
}

// CloudflareTurnstile calls siteverify. Cloudflare rejects a reused token
// ("timeout-or-duplicate"), which makes each token single use.
type CloudflareTurnstile struct {
	secret string
	http   *http.Client
}

// siteverifyURL is fixed: the API only ever calls Cloudflare here.
const siteverifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

func NewCloudflareTurnstile(secret string) *CloudflareTurnstile {
	return &CloudflareTurnstile{
		secret: secret,
		http:   &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *CloudflareTurnstile) Verify(ctx context.Context, token, remoteIP string) (string, bool, error) {
	form := url.Values{"secret": {c.secret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, siteverifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("turnstile: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Success  bool   `json:"success"`
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", false, fmt.Errorf("turnstile: decode: %w", err)
	}
	return out.Hostname, out.Success, nil
}
