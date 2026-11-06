// Package waitlist is the landing's waitlist (plan B.5). The contract lives in
// valuo_web/docs/waitlist-contract.md; change it there first.
package waitlist

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

const (
	confirmTTL    = 48 * time.Hour
	resendEvery   = 10 * time.Minute
	sendTimeout   = 20 * time.Second
	statusPending = "pending_confirmation"
)

// ErrTurnstile: a missing, invalid or reused Turnstile token (403).
var ErrTurnstile = errors.New("waitlist: turnstile failed")

// ErrExpired: an unknown, expired or superseded confirmation token (410).
var ErrExpired = errors.New("waitlist: token expired")

type emailSender interface {
	Configured() bool
	SendWithHeaders(ctx context.Context, to []string, subject, html string, headers map[string]string) error
}

// Config: what the service needs from the app config.
type Config struct {
	SiteURL        string   // landing origin: /confirmar, /legal/privacidad
	AllowedHosts   []string // hostnames a Turnstile token may come from
	APIPublicURL   string   // this API's origin, for the unsubscribe link
	Brand          string
	ContactEmail   string
	RequireCaptcha bool   // ENV=prod: without a verifier, every sign-up is refused
	UnsubscribeKey []byte // HMAC key for the stable unsubscribe tokens
}

type Service struct {
	q         *sqlcgen.Queries
	cfg       Config
	turnstile TurnstileVerifier
	sender    emailSender
	logger    *slog.Logger
	now       func() time.Time
}

func NewService(pool *pgxpool.Pool, cfg Config, turnstile TurnstileVerifier, sender emailSender, logger *slog.Logger) *Service {
	return &Service{q: sqlcgen.New(pool), cfg: cfg, turnstile: turnstile, sender: sender, logger: logger, now: time.Now}
}

// SignupInput: the POST /waitlist body, after decoding.
type SignupInput struct {
	Email          string
	Monotributo    bool
	Form           string
	Source         *string
	Medium         *string
	Campaign       *string
	ReferrerPath   *string
	NoticeVersion  string
	TurnstileToken string
	RemoteIP       string
}

// Signup registers the address and sends the confirmation mail (in the
// background, so a new address and a known one answer in about the same
// time). The answer is the same for both (no enumeration).
func (s *Service) Signup(ctx context.Context, in SignupInput) error {
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return err
	}
	if in.Form != "hero" && in.Form != "cta" {
		return domain.NewValidationError("form", "debe ser hero o cta")
	}
	if in.NoticeVersion == "" || len(in.NoticeVersion) > 32 {
		return domain.NewValidationError("notice_version", "requerido, hasta 32 caracteres")
	}
	ref, err := validReferrerPath(in.ReferrerPath)
	if err != nil {
		return err
	}
	if err := s.checkTurnstile(ctx, in.TurnstileToken, in.RemoteIP); err != nil {
		return err
	}

	existing, err := s.q.GetWaitlistEntryByEmail(ctx, email)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		token, hash, err := newToken()
		if err != nil {
			return err
		}
		row, err := s.q.CreateWaitlistEntry(ctx, sqlcgen.CreateWaitlistEntryParams{
			Email: email, Monotributo: in.Monotributo, Form: in.Form,
			Source: text(sanitizeAttribution(in.Source)), Medium: text(sanitizeAttribution(in.Medium)),
			Campaign: text(sanitizeAttribution(in.Campaign)), ReferrerPath: text(ref),
			NoticeVersion: in.NoticeVersion, ConfirmTokenHash: hash,
			ConfirmExpiresAt: pgtype.Timestamptz{Time: s.now().Add(confirmTTL), Valid: true},
		})
		if err != nil {
			return fmt.Errorf("waitlist.Signup: %w", err)
		}
		s.sendConfirmation(row.ID, email, token)
	case err != nil:
		return fmt.Errorf("waitlist.Signup: %w", err)
	case existing.ConfirmedAt.Valid:
		// Already confirmed: nothing to send, same answer.
	case existing.LastSentAt.Valid && s.now().Sub(existing.LastSentAt.Time) < resendEvery:
		// Resent less than 10 minutes ago: no mail flood, same answer.
	default:
		token, hash, err := newToken()
		if err != nil {
			return err
		}
		if err := s.q.RefreshWaitlistEntry(ctx, sqlcgen.RefreshWaitlistEntryParams{
			ID: existing.ID, Monotributo: in.Monotributo, NoticeVersion: in.NoticeVersion,
			ConfirmTokenHash: hash, ConfirmExpiresAt: pgtype.Timestamptz{Time: s.now().Add(confirmTTL), Valid: true},
		}); err != nil {
			return fmt.Errorf("waitlist.Signup: %w", err)
		}
		s.sendConfirmation(existing.ID, email, token)
	}
	return nil
}

func (s *Service) checkTurnstile(ctx context.Context, token, ip string) error {
	if s.turnstile == nil {
		if s.cfg.RequireCaptcha {
			s.logger.ErrorContext(ctx, "waitlist: TURNSTILE_SECRET_KEY missing with ENV=prod, sign-up refused")
			return ErrTurnstile
		}
		return nil
	}
	if token == "" {
		return ErrTurnstile
	}
	host, ok, err := s.turnstile.Verify(ctx, token, ip)
	if err != nil {
		s.logger.WarnContext(ctx, "waitlist: turnstile verify failed", "error", err)
		return ErrTurnstile
	}
	if !ok || !hostAllowed(host, s.cfg.AllowedHosts) {
		return ErrTurnstile
	}
	return nil
}

func hostAllowed(host string, allowed []string) bool {
	for _, h := range allowed {
		if h == host {
			return true
		}
	}
	return false
}

// Confirm marks the entry confirmed. Confirming twice answers the same; an
// unknown, expired or superseded token is ErrExpired.
func (s *Service) Confirm(ctx context.Context, token string) error {
	if token == "" {
		return ErrExpired
	}
	row, err := s.q.GetWaitlistEntryByConfirmHash(ctx, hashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrExpired
	}
	if err != nil {
		return fmt.Errorf("waitlist.Confirm: %w", err)
	}
	if row.ConfirmedAt.Valid {
		return nil
	}
	if !row.ConfirmExpiresAt.Valid || s.now().After(row.ConfirmExpiresAt.Time) {
		return ErrExpired
	}
	return s.q.ConfirmWaitlistEntry(ctx, row.ID)
}

// Unsubscribe deletes the entry of a valid unsubscribe token (idempotent:
// an already deleted entry is fine). ok=false for a forged token.
func (s *Service) Unsubscribe(ctx context.Context, token string) (bool, error) {
	id, ok := s.parseUnsubscribeToken(token)
	if !ok {
		return false, nil
	}
	if _, err := s.q.DeleteWaitlistEntry(ctx, id); err != nil {
		return false, fmt.Errorf("waitlist.Unsubscribe: %w", err)
	}
	return true, nil
}

// ValidUnsubscribeToken reports whether the token is genuine (GET page).
func (s *Service) ValidUnsubscribeToken(token string) bool {
	_, ok := s.parseUnsubscribeToken(token)
	return ok
}

// unsubscribe tokens are stable per entry (every mail carries one that keeps
// working): base64url(id || HMAC(key, "waitlist-unsubscribe:" || id)[:16]).
func (s *Service) unsubscribeToken(id uuid.UUID) string {
	mac := hmac.New(sha256.New, s.cfg.UnsubscribeKey)
	mac.Write([]byte("waitlist-unsubscribe:"))
	mac.Write(id[:])
	return base64.RawURLEncoding.EncodeToString(append(id[:], mac.Sum(nil)[:16]...))
}

func (s *Service) parseUnsubscribeToken(token string) (uuid.UUID, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return uuid.Nil, false
	}
	id, err := uuid.FromBytes(raw[:16])
	if err != nil {
		return uuid.Nil, false
	}
	want, _ := base64.RawURLEncoding.DecodeString(s.unsubscribeToken(id))
	return id, hmac.Equal(raw[16:], want[16:])
}

func (s *Service) sendConfirmation(id uuid.UUID, email, token string) {
	if s.sender == nil || !s.sender.Configured() {
		s.logger.Info("waitlist: confirmation not sent (no mail provider)")
		return
	}
	confirmURL := s.cfg.SiteURL + "/confirmar#token=" + url.QueryEscape(token)
	unsubscribeURL := s.cfg.APIPublicURL + "/waitlist/unsubscribe?token=" + s.unsubscribeToken(id)
	html := renderConfirmHTML(s.cfg.Brand, confirmURL, unsubscribeURL, s.cfg.SiteURL+"/legal/privacidad", s.cfg.ContactEmail)
	headers := map[string]string{
		// RFC 8058 one-click: mail clients POST "List-Unsubscribe=One-Click" here.
		"List-Unsubscribe":      "<" + unsubscribeURL + ">",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		if err := s.sender.SendWithHeaders(ctx, []string{email}, "Confirmá tu lugar en la lista de espera de "+s.cfg.Brand, html, headers); err != nil {
			s.logger.Warn("waitlist: confirmation mail failed", "error", err)
		}
	}()
}

func newToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("waitlist: token: %w", err)
	}
	t := base64.RawURLEncoding.EncodeToString(b)
	return t, hashToken(t), nil
}

func hashToken(t string) []byte {
	h := sha256.Sum256([]byte(t))
	return h[:]
}

func text(p *string) pgtype.Text {
	if p == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *p, Valid: true}
}

// Cleanup deletes unconfirmed entries older than 30 days (daily job).
func (s *Service) Cleanup(ctx context.Context) (int64, error) {
	return s.q.DeleteStaleWaitlistEntries(ctx)
}
