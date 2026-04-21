// Package email expone un cliente mínimo sobre la API de Resend
// (https://resend.com/docs/api-reference/emails/send-email). Se usa
// como sender genérico: reports lo inyecta para el email mensual,
// invites para los links de invitación, etc.
package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ResendSender es un cliente HTTP fino. No usamos el SDK oficial para
// mantener el árbol de dependencias chico: es un POST con JSON.
type ResendSender struct {
	apiKey string
	from   string
	http   *http.Client
}

// NewResendSender arma un sender con el from "Nombre <mail@dominio>".
// Si apiKey == "" el sender queda "no configurado" y Send devuelve error.
// El caller puede chequear Configured() antes para hacer no-op en dev.
func NewResendSender(apiKey, from string) *ResendSender {
	return &ResendSender{
		apiKey: apiKey,
		from:   from,
		http:   &http.Client{Timeout: 15 * time.Second},
	}
}

// Configured: true si tiene API key. Los workers/services lo chequean
// para hacer no-op cuando Resend no está configurado (útil en dev).
func (s *ResendSender) Configured() bool {
	return s != nil && s.apiKey != ""
}

type resendPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

// Send POST a https://api.resend.com/emails. Devuelve error si el
// status no es 2xx, con el body del response para diagnosticar.
func (s *ResendSender) Send(ctx context.Context, to []string, subject, html string) error {
	if !s.Configured() {
		return errors.New("resend: api key no configurada")
	}
	if len(to) == 0 {
		return errors.New("resend: lista de destinatarios vacía")
	}

	body, err := json.Marshal(resendPayload{From: s.from, To: to, Subject: subject, HTML: html})
	if err != nil {
		return fmt.Errorf("resend: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("resend: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("resend: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("resend: status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}
