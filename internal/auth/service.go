package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/users"
)

// userRepository es la interface mínima que el service necesita.
// Definirla acá (y no en el paquete users) es el patrón "interface en el
// consumidor": permite mockear en tests sin acoplar users al mock.
type userRepository interface {
	Create(ctx context.Context, email, passwordHash, name string) (domain.User, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	GetCredentialsByEmail(ctx context.Context, email string) (users.Credentials, error)
}

// Service orquesta register/login. No toca HTTP.
type Service struct {
	repo   userRepository
	tokens *TokenIssuer
}

func NewService(repo userRepository, tokens *TokenIssuer) *Service {
	return &Service{repo: repo, tokens: tokens}
}

// TokenPair agrupa los dos tokens + sus expiraciones para que el handler
// pueda armar la respuesta JSON y la cookie del refresh.
type TokenPair struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// AuthResult es lo que devuelve login/register al handler: user + tokens.
type AuthResult struct {
	User   domain.User
	Tokens TokenPair
}

// Register crea un user nuevo con password hasheado y devuelve tokens
// listos para la sesión. Valida formato de email y largo mínimo de password.
//
// Nota: en CPs futuros este método va a crear también "Efectivo" como
// payment_method default, el household "Mi hogar" + split_rule, y las
// categorías default. Por ahora sólo el user (las tablas aún no existen).
func (s *Service) Register(ctx context.Context, email, password, name string) (AuthResult, error) {
	email = normalizeEmail(email)
	name = strings.TrimSpace(name)

	if err := validateEmail(email); err != nil {
		return AuthResult{}, err
	}
	if err := validatePassword(password); err != nil {
		return AuthResult{}, err
	}
	if name == "" {
		return AuthResult{}, domain.NewValidationError("name", "no puede estar vacío")
	}

	hash, err := HashPassword(password)
	if err != nil {
		return AuthResult{}, fmt.Errorf("auth.Register: %w", err)
	}

	user, err := s.repo.Create(ctx, email, hash, name)
	if err != nil {
		// Si el email ya existe, el repo devuelve ErrConflict envuelto.
		// Lo pasamos tal cual para que el handler lo mapee a 409.
		return AuthResult{}, err
	}

	tokens, err := s.issueTokens(user.ID)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: user, Tokens: tokens}, nil
}

// Login valida credenciales. Siempre devuelve ErrUnauthorized si algo
// falla (email inexistente o password incorrecto), sin distinguir —
// así no se filtra qué emails están registrados.
func (s *Service) Login(ctx context.Context, email, password string) (AuthResult, error) {
	email = normalizeEmail(email)

	creds, err := s.repo.GetCredentialsByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return AuthResult{}, domain.NewAuthError("email o contraseña incorrectos")
		}
		return AuthResult{}, fmt.Errorf("auth.Login: %w", err)
	}

	if err := VerifyPassword(creds.PasswordHash, password); err != nil {
		return AuthResult{}, domain.NewAuthError("email o contraseña incorrectos")
	}

	tokens, err := s.issueTokens(creds.User.ID)
	if err != nil {
		return AuthResult{}, err
	}
	return AuthResult{User: creds.User, Tokens: tokens}, nil
}

// Me devuelve el user autenticado. Usado por el endpoint GET /me.
// Si el user fue borrado desde que se emitió el token, devuelve
// ErrUnauthorized (el frontend limpia la sesión).
func (s *Service) Me(ctx context.Context, userID uuid.UUID) (domain.User, error) {
	user, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.User{}, domain.NewAuthError("sesión inválida")
		}
		return domain.User{}, err
	}
	return user, nil
}

// Refresh valida el refresh token viejo y emite un par nuevo.
// Esto es la "rotación": cada refresh quema el anterior (implícitamente,
// porque el cliente solo va a usar el último que recibió).
//
// Sin tabla de refresh_tokens no podemos revocar tokens emitidos si un
// atacante roba uno: confiamos en la expiración de 7d + HTTPS + httpOnly
// cookie. Si en producción descubrimos que hace falta revocación, se
// agrega una tabla de "tokens revocados" (jti) — minimalista.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	userID, err := s.tokens.ParseRefreshToken(refreshToken)
	if err != nil {
		return TokenPair{}, err // ya viene envuelto en ErrUnauthorized
	}
	return s.issueTokens(userID)
}

func (s *Service) issueTokens(userID uuid.UUID) (TokenPair, error) {
	access, accessExp, err := s.tokens.IssueAccessToken(userID)
	if err != nil {
		return TokenPair{}, err
	}
	refresh, refreshExp, err := s.tokens.IssueRefreshToken(userID)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken:      access,
		AccessExpiresAt:  accessExp,
		RefreshToken:     refresh,
		RefreshExpiresAt: refreshExp,
	}, nil
}

// normalizeEmail: trim + lowercase. CITEXT en DB igualmente matchea case-insensitive,
// pero persistimos normalizado por prolijidad visual.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func validateEmail(email string) error {
	if email == "" {
		return domain.NewValidationError("email", "no puede estar vacío")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return domain.NewValidationError("email", "formato inválido")
	}
	return nil
}

// Largo mínimo 8. No forzamos mayúsculas/símbolos: NIST SP 800-63B
// recomienda largo > complejidad. El frontend puede sugerir fortaleza visual.
func validatePassword(password string) error {
	if len(password) < 8 {
		return domain.NewValidationError("password", "debe tener al menos 8 caracteres")
	}
	if len(password) > 128 {
		// bcrypt tiene un límite duro en 72 bytes; más allá de eso silenciosamente
		// trunca. Cortamos antes para no tener un hash inválido.
		return domain.NewValidationError("password", "demasiado largo (máx 128)")
	}
	return nil
}
