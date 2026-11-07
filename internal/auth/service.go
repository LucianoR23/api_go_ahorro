package auth

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/auth/google"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/users"
)

// userRepository es la interface mínima que el service necesita.
// Definirla acá (y no en el paquete users) es el patrón "interface en el
// consumidor": permite mockear en tests sin acoplar users al mock.
type userRepository interface {
	CreateTx(ctx context.Context, tx pgx.Tx, email, passwordHash, firstName, lastName string) (domain.User, error)
	CreateWithoutPassword(ctx context.Context, email, firstName, lastName string) (domain.User, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	GetCredentialsByEmail(ctx context.Context, email string) (users.Credentials, error)
	UpdateProfile(ctx context.Context, id uuid.UUID, firstName, lastName, email string) (domain.User, error)
	MarkEmailVerifiedTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
	SetPendingEmail(ctx context.Context, id uuid.UUID, email *string) error
}

// googleVerifier valida ID tokens de Google. Interface en el consumidor
// para mockear en tests sin pegarle a Google.
type googleVerifier interface {
	Verify(ctx context.Context, idToken string) (*google.Claims, error)
}

// identityRepository: persistencia de identidades OAuth. Get devuelve
// ErrNotFound si no hay vinculación previa — el service lo usa como señal
// para arrancar el flujo de auto-vincular por email o crear user nuevo.
type identityRepository interface {
	Get(ctx context.Context, provider, subject string) (uuid.UUID, error)
	Create(ctx context.Context, provider, subject string, userID uuid.UUID, email string) error
}

// registerBootstrap creates a new user's defaults (the "Efectivo" payment
// method). Register runs it in the registration's transaction; the Google
// sign-up path still runs it after the user insert and only logs a failure.
type registerBootstrap interface {
	CreateEfectivoFor(ctx context.Context, userID uuid.UUID) (domain.PaymentMethod, error)
	CreateEfectivoForTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (domain.PaymentMethod, error)
}

// inviteAccepter: optional. When set, Register accepts an inviteToken
// inside the registration's transaction; notify runs after the commit.
type inviteAccepter interface {
	AcceptOnRegisterTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, userEmail, token string) (notify func(context.Context), err error)
}

// emailVerifier: opcional. Si viene seteado, Register sin invite dispara
// Issue (manda mail de verificación). Con invite aceptado marcamos
// directamente el email como verificado vía userRepository (el mail de
// invite ya fue recibido por el destinatario, no hace falta doble
// verificación).
type emailVerifier interface {
	Issue(ctx context.Context, userID uuid.UUID) error
	IssueEmailChange(ctx context.Context, userID uuid.UUID, newEmail string) error
}

// Service orquesta register/login. No toca HTTP.
type Service struct {
	db             txBeginner
	repo           userRepository
	tokens         *TokenIssuer
	sessions       *SessionStore
	bootstrap      registerBootstrap
	invites        inviteAccepter
	verifier       emailVerifier
	googleVerifier googleVerifier
	identities     identityRepository
	logger         *slog.Logger
}

// txBeginner starts the registration transaction (the pool in production).
type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

func NewService(db txBeginner, repo userRepository, tokens *TokenIssuer, sessions *SessionStore, bootstrap registerBootstrap, logger *slog.Logger) *Service {
	return &Service{db: db, repo: repo, tokens: tokens, sessions: sessions, bootstrap: bootstrap, logger: logger}
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

// SetGoogleVerifier + SetIdentityRepository cablean el flow OAuth Google.
// Si alguno de los dos está nil, LoginWithGoogle falla con un error de
// configuración (500) — son obligatorios para que el endpoint funcione.
func (s *Service) SetGoogleVerifier(v googleVerifier)         { s.googleVerifier = v }
func (s *Service) SetIdentityRepository(r identityRepository) { s.identities = r }

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
// The user, their "Efectivo" payment method and an accepted invite are one
// transaction (plan 0.8).
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

	// One transaction (plan 0.8): the user, their "Efectivo" and, with an
	// invite, the membership and the verified email are created together or
	// not at all. An invite that cannot be accepted (expired, used, for
	// another email) does not block the registration: it is tried in a
	// savepoint and the user registers without it, as before.
	var (
		user           domain.User
		inviteAccepted bool
		notifyInviter  func(context.Context)
	)
	hasInvite := s.invites != nil && strings.TrimSpace(inviteToken) != ""
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		// Si el email ya existe, el repo devuelve ErrConflict envuelto; el
		// handler lo mapea a 409.
		user, err = s.repo.CreateTx(ctx, tx, email, hash, firstName, lastName)
		if err != nil {
			return err
		}
		if s.bootstrap != nil {
			if _, err := s.bootstrap.CreateEfectivoForTx(ctx, tx, user.ID); err != nil {
				return fmt.Errorf("auth.Register: bootstrap: %w", err)
			}
		}
		if !hasInvite {
			return nil
		}
		sp, err := tx.Begin(ctx) // savepoint
		if err != nil {
			return err
		}
		notify, err := s.invites.AcceptOnRegisterTx(ctx, sp, user.ID, user.Email, inviteToken)
		if err != nil {
			s.logger.WarnContext(ctx, "aceptar invitación en registro falló", "user_id", user.ID, "error", err)
			return sp.Rollback(ctx)
		}
		if err := sp.Commit(ctx); err != nil {
			return err
		}
		// El invite implica que el email fue recibido: verificado sin mail.
		if err := s.repo.MarkEmailVerifiedTx(ctx, tx, user.ID); err != nil {
			return err
		}
		now := time.Now()
		user.EmailVerifiedAt = &now
		inviteAccepted, notifyInviter = true, notify
		return nil
	})
	if err != nil {
		return AuthResult{}, err
	}

	if inviteAccepted {
		notifyInviter(ctx)
	} else if s.verifier != nil {
		// Registro directo: mandá el mail de verificación. Si falla, el user
		// ya existe y queda creado — puede reintentar.
		if err := s.verifier.Issue(ctx, user.ID); err != nil {
			s.logger.Warn("email verification issue falló en register",
				"user_id", user.ID, "error", err)
		}
	}

	tokens, err := s.issueTokens(ctx, user.ID)
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

	// Cuenta registrada solo con Google → no tiene password_hash. Devolvemos
	// el mismo error genérico para no filtrar el método de autenticación
	// (anti-enumeration). El user que se confunda va a probar "Continuar con
	// Google" y va a poder entrar.
	if !creds.HasPassword {
		return AuthResult{}, domain.NewAuthError("email o contraseña incorrectos")
	}

	if err := VerifyPassword(creds.PasswordHash, password); err != nil {
		return AuthResult{}, domain.NewAuthError("email o contraseña incorrectos")
	}

	tokens, err := s.issueTokens(ctx, creds.User.ID)
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
// A new email is not applied here (plan 0.5): it waits as pending_email and a
// confirmation link goes to the new address; EmailVerificationService.Confirm
// applies it. An address already used by another account is a 409 now, and
// again at confirmation if someone took it meanwhile.
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

	updated := current
	if firstName != current.FirstName || lastName != current.LastName {
		updated, err = s.repo.UpdateProfile(ctx, userID, firstName, lastName, current.Email)
		if err != nil {
			return domain.User{}, err
		}
	}
	if email != current.Email {
		if _, err := s.repo.GetCredentialsByEmail(ctx, email); err == nil {
			return domain.User{}, domain.NewConflictError("ese email ya está registrado en otra cuenta")
		} else if !errors.Is(err, domain.ErrNotFound) {
			return domain.User{}, err
		}
		if s.verifier == nil {
			return domain.User{}, fmt.Errorf("auth.UpdateMe: email verifier no configurado")
		}
		if err := s.repo.SetPendingEmail(ctx, userID, &email); err != nil {
			return domain.User{}, err
		}
		if err := s.verifier.IssueEmailChange(ctx, userID, email); err != nil {
			return domain.User{}, err
		}
		updated.PendingEmail = &email
	}
	return updated, nil
}

// Refresh rotates the session's refresh token and issues a new access token.
// Unknown, expired, revoked or reused tokens are refused (a reused one also
// revokes its session; see SessionStore.Rotate). A deleted user's session
// is revoked too.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	sess, err := s.sessions.Rotate(ctx, refreshToken)
	if err != nil {
		return TokenPair{}, err
	}
	if _, err := s.repo.GetByID(ctx, sess.UserID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			_ = s.sessions.RevokeAll(ctx, sess.UserID)
			return TokenPair{}, domain.NewAuthError("sesión inválida")
		}
		return TokenPair{}, err
	}
	return s.accessFor(sess)
}

// Logout ends the session of the refresh token and returns its user
// (uuid.Nil if the token was unknown; signing out always succeeds).
func (s *Service) Logout(ctx context.Context, refreshToken string) (uuid.UUID, error) {
	if refreshToken == "" {
		return uuid.Nil, nil
	}
	return s.sessions.RevokeByToken(ctx, refreshToken)
}

// UserIDByEmail: the account behind an e-mail, for recording a failed
// sign-in on it (plan 0.17). ok=false when no account has it.
func (s *Service) UserIDByEmail(ctx context.Context, email string) (uuid.UUID, bool) {
	creds, err := s.repo.GetCredentialsByEmail(ctx, normalizeEmail(email))
	if err != nil {
		return uuid.Nil, false
	}
	return creds.User.ID, true
}

// LogoutAll ends every session of the user, the current one included.
func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) error {
	return s.sessions.RevokeAll(ctx, userID)
}

// issueTokens opens a new session (sign-in) and returns its tokens.
func (s *Service) issueTokens(ctx context.Context, userID uuid.UUID) (TokenPair, error) {
	sess, err := s.sessions.Create(ctx, userID, userAgentFrom(ctx))
	if err != nil {
		return TokenPair{}, err
	}
	return s.accessFor(sess)
}

func (s *Service) accessFor(sess Session) (TokenPair, error) {
	access, accessExp, err := s.tokens.IssueAccessToken(sess.UserID, sess.ID)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken:      access,
		AccessExpiresAt:  accessExp,
		RefreshToken:     sess.RefreshToken,
		RefreshExpiresAt: sess.ExpiresAt,
	}, nil
}

// LoginWithGoogle valida el ID token y resuelve uno de tres caminos:
//
//  1. Identidad ya vinculada (provider=google, subject=X) → login directo.
//  2. Sin identidad pero el email ya existe en users → auto-vinculamos
//     y logueamos. Google nos garantiza email_verified=true, así que
//     marcamos email_verified_at si todavía estaba NULL.
//  3. Email nuevo → creamos user sin password + bootstrap (Efectivo).
//
// isNew=true solo en el caso 3. El frontend lo usa como hint para redirigir
// directo a /onboarding sin esperar al AuthGate.
func (s *Service) LoginWithGoogle(ctx context.Context, idToken string) (AuthResult, bool, error) {
	if s.googleVerifier == nil || s.identities == nil {
		return AuthResult{}, false, fmt.Errorf("auth.LoginWithGoogle: googleVerifier o identityRepository no configurado")
	}

	claims, err := s.googleVerifier.Verify(ctx, idToken)
	if err != nil {
		return AuthResult{}, false, err
	}
	email := normalizeEmail(claims.Email)

	// 1) Identidad ya vinculada → login directo.
	userID, err := s.identities.Get(ctx, "google", claims.Subject)
	if err == nil {
		user, err := s.repo.GetByID(ctx, userID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				// User soft-deleted pero la identidad sigue (no la borramos
				// en SoftDelete a propósito — ver migration). Tratamos como
				// sesión inválida.
				return AuthResult{}, false, domain.NewAuthError("cuenta no disponible")
			}
			return AuthResult{}, false, err
		}
		tokens, err := s.issueTokens(ctx, user.ID)
		if err != nil {
			return AuthResult{}, false, err
		}
		return AuthResult{User: user, Tokens: tokens}, false, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return AuthResult{}, false, err
	}

	// 2) The email already has an account without this identity: link it,
	// but only if that account proved it owns the mailbox. Otherwise anyone
	// could register the victim's email first, wait, and own the account the
	// day the victim signs in with Google (pre-hijacking, plan 0.5). The owner
	// signs in with the password (or resets it, which verifies the email) and
	// Google works from then on.
	existing, err := s.repo.GetCredentialsByEmail(ctx, email)
	if err == nil {
		if existing.User.EmailVerifiedAt == nil {
			return AuthResult{}, false, domain.NewConflictError(
				"Ya existe una cuenta con este email sin verificar. Ingresá con tu contraseña " +
					"(o recuperala con \"Olvidé mi contraseña\") y verificá tu email; después vas a poder entrar con Google.")
		}
		if err := s.identities.Create(ctx, "google", claims.Subject, existing.User.ID, claims.Email); err != nil {
			return AuthResult{}, false, err
		}
		tokens, err := s.issueTokens(ctx, existing.User.ID)
		if err != nil {
			return AuthResult{}, false, err
		}
		return AuthResult{User: existing.User, Tokens: tokens}, false, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return AuthResult{}, false, err
	}

	// 3) Email nuevo → crear user + bootstrap.
	given := strings.TrimSpace(claims.GivenName)
	family := strings.TrimSpace(claims.FamilyName)
	if given == "" {
		// Google no siempre manda given_name (cuentas viejas o configs
		// raras). Sin fallback el INSERT explotaría por validación de
		// frontend; con esto el user puede editar después en su perfil.
		given = "Usuario"
	}
	user, err := s.repo.CreateWithoutPassword(ctx, email, given, family)
	if err != nil {
		return AuthResult{}, false, err
	}
	if err := s.identities.Create(ctx, "google", claims.Subject, user.ID, claims.Email); err != nil {
		return AuthResult{}, false, err
	}
	if s.bootstrap != nil {
		if _, err := s.bootstrap.CreateEfectivoFor(ctx, user.ID); err != nil {
			// Mismo criterio que Register: no abortamos, el user ya existe.
			s.logger.Warn("bootstrap falló para user de Google", "user_id", user.ID, "error", err)
		}
	}

	tokens, err := s.issueTokens(ctx, user.ID)
	if err != nil {
		return AuthResult{}, false, err
	}
	return AuthResult{User: user, Tokens: tokens}, true, nil
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
