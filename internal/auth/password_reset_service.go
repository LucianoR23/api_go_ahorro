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
	"github.com/LucianoR23/api_go_ahorra/internal/users"
)

// passwordResetRepo es la interface que el service necesita — definida
// en el consumidor para poder mockear en tests.
type passwordResetRepo interface {
	Create(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) (PasswordReset, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (PasswordReset, error)
	MarkUsed(ctx context.Context, id uuid.UUID) error
	InvalidateActiveForUser(ctx context.Context, userID uuid.UUID) error
}

// passwordResetUserRepo: el service solo necesita leer por email/ID y
// actualizar password_hash. Concrete impl vive en internal/users.
type passwordResetUserRepo interface {
	GetByEmail(ctx context.Context, email string) (domain.User, error)
	GetCredentialsByID(ctx context.Context, id uuid.UUID) (users.Credentials, error)
	UpdatePasswordHash(ctx context.Context, id uuid.UUID, newHash string) error
}

// resetEmailSender: mismo shape que inviteEmailSender.
type resetEmailSender interface {
	Configured() bool
	Send(ctx context.Context, to []string, subject, html string) error
}

// PasswordResetService maneja forgot/reset y change-password.
//
// Invariantes:
//   - token plano jamás persiste — solo SHA-256 hex.
//   - RequestReset NO enumera emails: siempre devuelve 204, incluso si el
//     email no existe (anti-enumeration).
//   - Al emitir un token nuevo, invalidamos los anteriores del user.
//   - Al completar un reset, invalidamos cualquier otro token activo del
//     user (por si había varios mails).
type PasswordResetService struct {
	resets     passwordResetRepo
	users      passwordResetUserRepo
	sender     resetEmailSender
	logger     *slog.Logger
	appBaseURL string
	ttl        time.Duration
}

func NewPasswordResetService(
	resets passwordResetRepo,
	users passwordResetUserRepo,
	sender resetEmailSender,
	logger *slog.Logger,
	appBaseURL string,
) *PasswordResetService {
	return &PasswordResetService{
		resets:     resets,
		users:      users,
		sender:     sender,
		logger:     logger,
		appBaseURL: strings.TrimRight(appBaseURL, "/"),
		ttl:        1 * time.Hour,
	}
}

// RequestReset inicia el flujo de olvidé mi contraseña. Siempre devuelve
// nil para no filtrar existencia de emails. Si el email no existe, loguea
// y sale. Si existe, genera token, persiste hash y manda mail.
func (s *PasswordResetService) RequestReset(ctx context.Context, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		// Esto SÍ lo validamos — un email vacío no es "no existe", es mal request.
		return domain.NewValidationError("email", "no puede estar vacío")
	}

	user, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// No filtramos — log interno y respuesta exitosa al cliente.
			s.logger.InfoContext(ctx, "password reset pedido para email inexistente",
				"email", email)
			return nil
		}
		return err
	}

	// Invalidamos resets previos antes de emitir uno nuevo. No es estrictamente
	// necesario (cada token es independiente) pero evita tener N tokens vivos.
	if err := s.resets.InvalidateActiveForUser(ctx, user.ID); err != nil {
		s.logger.WarnContext(ctx, "no se pudo invalidar resets previos", "user_id", user.ID, "error", err)
		// No abortamos: podemos emitir el nuevo igual.
	}

	token, hash, err := generateResetToken()
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(s.ttl)

	if _, err := s.resets.Create(ctx, user.ID, hash, expiresAt); err != nil {
		return err
	}

	resetURL := s.appBaseURL + "/reset-password?token=" + token
	if s.sender != nil && s.sender.Configured() {
		html := renderPasswordResetHTML(user.FirstName, resetURL, expiresAt)
		subject := "Restablecer tu contraseña de Ahorra"
		if err := s.sender.Send(ctx, []string{user.Email}, subject, html); err != nil {
			// No abortamos el request: el token quedó emitido. El user puede
			// reintentar. Logueamos para alertar si el sender está roto.
			s.logger.WarnContext(ctx, "password reset email falló",
				"user_id", user.ID, "error", err)
		}
	} else {
		// En dev logueamos el URL para poder probar sin Resend.
		s.logger.InfoContext(ctx, "password reset creado sin email (sender no configurado)",
			"user_id", user.ID, "reset_url", resetURL)
	}
	return nil
}

// ConfirmReset valida el token y actualiza el password. El email no viene
// en el request: sale del token (el hash resuelve a un único user).
func (s *PasswordResetService) ConfirmReset(ctx context.Context, token, newPassword string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return domain.NewValidationError("token", "no puede estar vacío")
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}

	reset, err := s.resets.GetByTokenHash(ctx, hashResetToken(token))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Mensaje genérico — no distinguimos "no existe" de "ya usado"
			// para no dar pistas a un atacante que prueba tokens.
			return domain.NewAuthError("token inválido o expirado")
		}
		return err
	}
	if reset.UsedAt != nil {
		return domain.NewAuthError("token inválido o expirado")
	}
	if time.Now().After(reset.ExpiresAt) {
		return domain.NewAuthError("token inválido o expirado")
	}

	newHash, err := HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("auth.ConfirmReset: %w", err)
	}

	// Marcamos usado ANTES de actualizar el password: si dos requests
	// llegan con el mismo token, solo uno gana el UPDATE condicional.
	if err := s.resets.MarkUsed(ctx, reset.ID); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return domain.NewAuthError("token inválido o expirado")
		}
		return err
	}

	if err := s.users.UpdatePasswordHash(ctx, reset.UserID, newHash); err != nil {
		return err
	}

	// Invalidamos otros resets activos del user por si hay varios mails
	// flotando. No crítico pero limpio.
	if err := s.resets.InvalidateActiveForUser(ctx, reset.UserID); err != nil {
		s.logger.WarnContext(ctx, "no se pudo limpiar resets del user",
			"user_id", reset.UserID, "error", err)
	}
	s.logger.InfoContext(ctx, "password reseteado", "user_id", reset.UserID)
	return nil
}

// ChangePassword: user logueado cambia su password. Requiere la contraseña
// actual para evitar que alguien con acceso al session token (XSS robando
// access token, p.ej.) cambie la clave sin conocer la actual.
func (s *PasswordResetService) ChangePassword(ctx context.Context, userID uuid.UUID, currentPassword, newPassword string) error {
	if strings.TrimSpace(currentPassword) == "" {
		return domain.NewValidationError("currentPassword", "no puede estar vacío")
	}
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	if currentPassword == newPassword {
		return domain.NewValidationError("newPassword", "debe ser distinta a la actual")
	}

	creds, err := s.users.GetCredentialsByID(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.NewAuthError("sesión inválida")
		}
		return err
	}
	if err := VerifyPassword(creds.PasswordHash, currentPassword); err != nil {
		return domain.NewAuthError("contraseña actual incorrecta")
	}

	newHash, err := HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("auth.ChangePassword: %w", err)
	}
	if err := s.users.UpdatePasswordHash(ctx, userID, newHash); err != nil {
		return err
	}

	// Invalidamos cualquier reset activo: el user ya tiene la clave nueva.
	if err := s.resets.InvalidateActiveForUser(ctx, userID); err != nil {
		s.logger.WarnContext(ctx, "no se pudo limpiar resets activos",
			"user_id", userID, "error", err)
	}
	return nil
}

// ===================== helpers =====================

func generateResetToken() (plain, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("auth.generateResetToken: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	hash = hashResetToken(plain)
	return plain, hash, nil
}

func hashResetToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
