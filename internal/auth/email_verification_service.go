package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// Interfaces del consumidor — el service no acopla concretos.
type emailVerificationRepo interface {
	Create(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time, newEmail *string) (EmailVerification, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (EmailVerification, error)
	MarkUsed(ctx context.Context, id uuid.UUID) error
	InvalidateActiveForUser(ctx context.Context, userID uuid.UUID) error
}

type emailVerificationUserRepo interface {
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	MarkEmailVerified(ctx context.Context, id uuid.UUID) error
	ApplyEmailChange(ctx context.Context, id uuid.UUID, newEmail string) (domain.User, error)
}

type emailVerificationSender interface {
	Configured() bool
	Send(ctx context.Context, to []string, subject, html string) error
}

// EmailVerificationService emite, valida y reenvía tokens de verificación
// de email. Se invoca desde Register (cuando NO hay invite) y desde un
// endpoint público /auth/verify-email.
//
// Invariantes:
//   - token plano jamás persiste — solo SHA-256 hex.
//   - single-use vía MarkUsed con UPDATE condicional.
//   - Issue invalida los activos previos (último mail == único válido).
//   - Idempotente al marcar users.email_verified_at (no pisa si ya estaba).
type EmailVerificationService struct {
	brand      string // product name in subject and body
	repo       emailVerificationRepo
	users      emailVerificationUserRepo
	sender     emailVerificationSender
	logger     *slog.Logger
	appBaseURL string
	ttl        time.Duration
}

func NewEmailVerificationService(
	repo emailVerificationRepo,
	users emailVerificationUserRepo,
	sender emailVerificationSender,
	logger *slog.Logger,
	appBaseURL string,
	brand string,
) *EmailVerificationService {
	return &EmailVerificationService{
		brand:      brand,
		repo:       repo,
		users:      users,
		sender:     sender,
		logger:     logger,
		appBaseURL: strings.TrimRight(appBaseURL, "/"),
		ttl:        24 * time.Hour,
	}
}

// Issue emite un token de verificación para el user y manda el mail.
// Si el user ya está verificado, no hace nada (idempotente). Los errores
// de envío no abortan el caller — el token queda emitido y puede reintentar
// vía /auth/resend-verification-email.
func (s *EmailVerificationService) Issue(ctx context.Context, userID uuid.UUID) error {
	if s == nil {
		return nil
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.EmailVerifiedAt != nil {
		return nil
	}

	if err := s.repo.InvalidateActiveForUser(ctx, userID); err != nil {
		s.logger.WarnContext(ctx, "no se pudo invalidar verificaciones previas",
			"user_id", userID, "error", err)
	}

	token, hash, err := generateVerificationToken()
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(s.ttl)
	if _, err := s.repo.Create(ctx, userID, hash, expiresAt, nil); err != nil {
		return err
	}

	verifyURL := s.appBaseURL + "/verify-email?token=" + token
	if s.sender != nil && s.sender.Configured() {
		html := renderEmailVerificationHTML(s.brand, u.FirstName, verifyURL, expiresAt)
		subject := "Verificá tu email en " + s.brand
		if err := s.sender.Send(ctx, []string{u.Email}, subject, html); err != nil {
			s.logger.WarnContext(ctx, "email verification mail falló",
				"user_id", userID, "error", err)
		}
	} else {
		s.logger.InfoContext(ctx, "email verification creado sin sender",
			"user_id", userID, "verify_url", verifyURL)
	}
	return nil
}

// Confirm valida el token y marca el user como verificado.
// Mensaje genérico para no distinguir "no existe" de "ya usado" (mismo
// patrón que password reset).
func (s *EmailVerificationService) Confirm(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return domain.NewValidationError("token", "no puede estar vacío")
	}

	ev, err := s.repo.GetByTokenHash(ctx, hashVerificationToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.NewAuthError("token inválido o expirado")
		}
		return err
	}
	if ev.UsedAt != nil || time.Now().After(ev.ExpiresAt) {
		return domain.NewAuthError("token inválido o expirado")
	}

	if err := s.repo.MarkUsed(ctx, ev.ID); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.NewAuthError("token inválido o expirado")
		}
		return err
	}

	if ev.NewEmail != nil {
		// An email change: the new address is proven, so it becomes the
		// account's email (409 if another account took it meanwhile).
		if _, err := s.users.ApplyEmailChange(ctx, ev.UserID, *ev.NewEmail); err != nil {
			return err
		}
		s.logger.InfoContext(ctx, "email cambiado", "user_id", ev.UserID)
		return nil
	}
	if err := s.users.MarkEmailVerified(ctx, ev.UserID); err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "email verificado", "user_id", ev.UserID)
	return nil
}

// IssueEmailChange sends the confirmation link for an email change to the
// NEW address (plan 0.5). Earlier pending links of the user stop working.
func (s *EmailVerificationService) IssueEmailChange(ctx context.Context, userID uuid.UUID, newEmail string) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.repo.InvalidateActiveForUser(ctx, userID); err != nil {
		s.logger.WarnContext(ctx, "no se pudo invalidar verificaciones previas",
			"user_id", userID, "error", err)
	}
	token, hash, err := generateVerificationToken()
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(s.ttl)
	if _, err := s.repo.Create(ctx, userID, hash, expiresAt, &newEmail); err != nil {
		return err
	}
	confirmURL := s.appBaseURL + "/verify-email?token=" + token
	if s.sender != nil && s.sender.Configured() {
		html := renderEmailChangeHTML(s.brand, u.FirstName, newEmail, confirmURL, expiresAt)
		if err := s.sender.Send(ctx, []string{newEmail}, "Confirmá tu nuevo email en "+s.brand, html); err != nil {
			s.logger.WarnContext(ctx, "email change mail falló", "user_id", userID, "error", err)
		}
	} else {
		s.logger.InfoContext(ctx, "email change creado sin sender", "user_id", userID)
	}
	return nil
}

// Resend reemite un token para el user autenticado. Si ya está verificado
// devuelve ErrConflict (nada que hacer).
func (s *EmailVerificationService) Resend(ctx context.Context, userID uuid.UUID) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.EmailVerifiedAt != nil {
		return fmt.Errorf("email ya verificado: %w", domain.ErrConflict)
	}
	return s.Issue(ctx, userID)
}

// ===================== helpers =====================

func generateVerificationToken() (plain, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("auth.generateVerificationToken: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	hash = hashVerificationToken(plain)
	return plain, hash, nil
}

// hashVerificationToken: SHA-256 hex (mismo esquema que invites/resets).
func hashVerificationToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
