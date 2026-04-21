package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	Create(ctx context.Context, email, passwordHash, firstName, lastName string) (domain.User, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	GetCredentialsByEmail(ctx context.Context, email string) (users.Credentials, error)
	UpdateProfile(ctx context.Context, id uuid.UUID, firstName, lastName, email string) (domain.User, error)
	MarkEmailVerified(ctx context.Context, id uuid.UUID) error
}

// registerBootstrap es lo mínimo que el service necesita para poblar los
// defaults de un user recién creado (Efectivo, y en futuros CPs: hogar
// "Mi hogar" + split_rule + categorías default).
//
// Interface en el consumidor → el concreto vive en paymethods. Si falla,
// logueamos pero no abortamos Register: el user sigue existiendo y puede
// crear el medio de pago manualmente.
type registerBootstrap interface {
	CreateEfectivoFor(ctx context.Context, userID uuid.UUID) (domain.PaymentMethod, error)
}

// inviteAccepter: opcional. Si viene seteado, Register acepta un
// inviteToken y agrega al user al hogar correspondiente. Si falla,
// logueamos pero seguimos — el user ya existe; puede pedir otra invite.
type inviteAccepter interface {
	AcceptOnRegister(ctx context.Context, userID uuid.UUID, userEmail, token string) error
}

// emailVerifier: opcional. Si viene seteado, Register sin invite dispara
// Issue (manda mail de verificación). Con invite aceptado marcamos
// directamente el email como verificado vía userRepository (el mail de
// invite ya fue recibido por el destinatario, no hace falta doble
// verificación).
type emailVerifier interface {
	Issue(ctx context.Context, userID uuid.UUID) error
}

// Service orquesta register/login. No toca HTTP.
type Service struct {
	repo      userRepository
	tokens    *TokenIssuer
	bootstrap registerBootstrap
	invites   inviteAccepter
	verifier  emailVerifier
	logger    *slog.Logger
}

func NewService(repo userRepository, tokens *TokenIssuer, bootstrap registerBootstrap, logger *slog.Logger) *Service {
	return &Service{repo: repo, tokens: tokens, bootstrap: bootstrap, logger: logger}
}

// SetInviteAccepter cablea post-construcción para evitar dependencia
// cíclica con households (que ya depende de users, donde vive auth).
func (s *Service) SetInviteAccepter(a inviteAccepter) {
	s.invites = a
}

// SetEmailVerifier cablea el service de verificación de email.
func (s *Service) SetEmailVerifier(v emailVerifier) {
	s.verifier = v
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
// Después de crear el user dispara el bootstrap (Efectivo por ahora; en CPs
// futuros sumará "Mi hogar" + split_rule + categorías default). No es
// transaccional con el INSERT del user — si el bootstrap falla, logueamos
// y seguimos: el user puede completar el setup manualmente.
func (s *Service) Register(ctx context.Context, email, password, firstName, lastName, inviteToken string) (AuthResult, error) {
	email = normalizeEmail(email)
	firstName = strings.TrimSpace(firstName)
	lastName = strings.TrimSpace(lastName)

	if err := validateEmail(email); err != nil {
		return AuthResult{}, err
	}
	if err := validatePassword(password); err != nil {
		return AuthResult{}, err
	}
	if err := validateName("firstName", firstName, true); err != nil {
		return AuthResult{}, err
	}
	// lastName opcional: aceptamos vacío (mononombres, apodos).
	if err := validateName("lastName", lastName, false); err != nil {
		return AuthResult{}, err
	}

	hash, err := HashPassword(password)
	if err != nil {
		return AuthResult{}, fmt.Errorf("auth.Register: %w", err)
	}

	user, err := s.repo.Create(ctx, email, hash, firstName, lastName)
	if err != nil {
		// Si el email ya existe, el repo devuelve ErrConflict envuelto.
		// Lo pasamos tal cual para que el handler lo mapee a 409.
		return AuthResult{}, err
	}

	if s.bootstrap != nil {
		if _, err := s.bootstrap.CreateEfectivoFor(ctx, user.ID); err != nil {
			// No abortamos: el user ya existe. Logueamos para detectarlo.
			s.logger.Warn("bootstrap de registro falló, user sin Efectivo",
				"user_id", user.ID, "error", err)
		}
	}

	// Si el registro vino con un invite token, aceptamos acá. Errores
	// no abortan: el user queda creado y puede pedir re-invitación.
	//
	// El invite implica que el email fue recibido → lo marcamos verificado
	// directamente y no disparamos el mail de verificación. Si el invite
	// acceptOnRegister falla, igual el email se queda sin verificar y el
	// user puede usar /auth/resend-verification-email para pedirlo.
	inviteAccepted := false
	hasInvite := s.invites != nil && strings.TrimSpace(inviteToken) != ""
	if hasInvite {
		if err := s.invites.AcceptOnRegister(ctx, user.ID, user.Email, inviteToken); err != nil {
			s.logger.Warn("aceptar invitación en registro falló",
				"user_id", user.ID, "error", err)
		} else {
			inviteAccepted = true
		}
	}
	if inviteAccepted {
		if err := s.repo.MarkEmailVerified(ctx, user.ID); err != nil {
			s.logger.Warn("no se pudo marcar email verificado tras aceptar invite",
				"user_id", user.ID, "error", err)
		} else {
			now := time.Now()
			user.EmailVerifiedAt = &now
		}
	} else if s.verifier != nil {
		// Registro directo: mandá el mail de verificación. Si falla, el user
		// ya existe y queda creado — puede reintentar.
		if err := s.verifier.Issue(ctx, user.ID); err != nil {
			s.logger.Warn("email verification issue falló en register",
				"user_id", user.ID, "error", err)
		}
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

// UpdateMeInput: campos opcionales (punteros → nil == "no cambiar").
// El handler decodifica el JSON y arma este struct.
type UpdateMeInput struct {
	FirstName *string
	LastName  *string
	Email     *string
}

// UpdateMe aplica cambios parciales al perfil. Valida cada campo que
// viene seteado; los que son nil quedan como estaban.
//
// Nota: el cambio de email NO dispara verification todavía (Fase 3).
// Lo que sí hacemos es bloquear por unique constraint — colisión devuelve 409.
func (s *Service) UpdateMe(ctx context.Context, userID uuid.UUID, in UpdateMeInput) (domain.User, error) {
	current, err := s.repo.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.User{}, domain.NewAuthError("sesión inválida")
		}
		return domain.User{}, err
	}

	firstName := current.FirstName
	lastName := current.LastName
	email := current.Email

	if in.FirstName != nil {
		v := strings.TrimSpace(*in.FirstName)
		if err := validateName("firstName", v, true); err != nil {
			return domain.User{}, err
		}
		firstName = v
	}
	if in.LastName != nil {
		v := strings.TrimSpace(*in.LastName)
		if err := validateName("lastName", v, false); err != nil {
			return domain.User{}, err
		}
		lastName = v
	}
	if in.Email != nil {
		v := normalizeEmail(*in.Email)
		if err := validateEmail(v); err != nil {
			return domain.User{}, err
		}
		email = v
	}

	// Short-circuit: si ningún campo cambió, no pegamos UPDATE.
	if firstName == current.FirstName && lastName == current.LastName && email == current.Email {
		return current, nil
	}
	return s.repo.UpdateProfile(ctx, userID, firstName, lastName, email)
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

// validateName chequea que el nombre/apellido sea razonable.
// required=true rechaza el vacío; required=false lo acepta.
// Cota superior 100 es arbitraria pero suficiente para nombres reales
// (el más largo registrado está en el orden de 50 chars).
func validateName(field, value string, required bool) error {
	if value == "" {
		if required {
			return domain.NewValidationError(field, "no puede estar vacío")
		}
		return nil
	}
	if len(value) > 100 {
		return domain.NewValidationError(field, "demasiado largo (máx 100)")
	}
	return nil
}

// Reglas: mínimo 8 caracteres, al menos una mayúscula, una minúscula y un
// número. Caracteres especiales permitidos pero no obligatorios.
func validatePassword(password string) error {
	if len(password) < 8 {
		return domain.NewValidationError("password", "debe tener al menos 8 caracteres")
	}
	if len(password) > 128 {
		// bcrypt tiene un límite duro en 72 bytes; más allá de eso silenciosamente
		// trunca. Cortamos antes para no tener un hash inválido.
		return domain.NewValidationError("password", "demasiado largo (máx 128)")
	}
	var hasUpper, hasLower, hasDigit bool
	for _, r := range password {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
	}
	if !hasUpper {
		return domain.NewValidationError("password", "debe incluir al menos una mayúscula")
	}
	if !hasLower {
		return domain.NewValidationError("password", "debe incluir al menos una minúscula")
	}
	if !hasDigit {
		return domain.NewValidationError("password", "debe incluir al menos un número")
	}
	return nil
}
