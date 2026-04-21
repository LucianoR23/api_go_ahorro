package households

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// inviteEmailSender: el service depende de esta interface (no del
// concreto email.ResendSender) para poder no-opear cuando la API key
// no está configurada en dev.
type inviteEmailSender interface {
	Configured() bool
	Send(ctx context.Context, to []string, subject, html string) error
}

// InvitesService expone los flujos: crear/listar/revocar invitaciones
// (owner) y aceptar (destinatario autenticado).
//
// Invariantes:
//   - token plano nunca persiste — solo su SHA-256.
//   - aceptar es single-use: MarkInviteAccepted usa WHERE accepted_at IS NULL,
//     entonces dos clicks concurrentes con el mismo token → uno gana, otro 409.
//   - un mismo email puede tener una sola invitación activa por hogar
//     (unique parcial en la migration).
type InvitesService struct {
	repo       *InvitesRepository
	households *Repository
	users      userLookup
	splitRules splitRulesSeeder
	sender     inviteEmailSender
	push       pushNotifier
	logger     *slog.Logger

	appBaseURL string        // ej: https://app.ahorra.app  (para el link del mail)
	ttl        time.Duration // default 7 días
}

func NewInvitesService(
	repo *InvitesRepository,
	households *Repository,
	users userLookup,
	splitRules splitRulesSeeder,
	sender inviteEmailSender,
	logger *slog.Logger,
	appBaseURL string,
) *InvitesService {
	return &InvitesService{
		repo:       repo,
		households: households,
		users:      users,
		splitRules: splitRules,
		sender:     sender,
		logger:     logger,
		appBaseURL: strings.TrimRight(appBaseURL, "/"),
		ttl:        7 * 24 * time.Hour,
	}
}

func (s *InvitesService) SetNotifier(n pushNotifier) {
	s.push = n
}

// InviteResult es lo que se devuelve al crear una invitación. El token
// plano solo vuelve acá — después solo se guarda el hash.
type InviteResult struct {
	Invite     domain.HouseholdInvite
	Token      string // plano, one-shot. Frontend puede mostrarlo como fallback.
	AcceptURL  string
	EmailSent  bool
}

// Create emite una invitación: genera token, persiste hash, manda email.
// Si el email ya es miembro del hogar → 409. Si el email ya tiene invite
// activa para este hogar → 409.
func (s *InvitesService) Create(ctx context.Context, inviterID, householdID uuid.UUID, email string) (InviteResult, error) {
	// Solo el owner puede invitar.
	role, err := s.households.GetMemberRole(ctx, householdID, inviterID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return InviteResult{}, domain.ErrForbidden
		}
		return InviteResult{}, err
	}
	if role != domain.RoleOwner {
		return InviteResult{}, domain.ErrForbidden
	}

	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return InviteResult{}, domain.NewValidationError("email", "no puede estar vacío")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return InviteResult{}, domain.NewValidationError("email", "formato inválido")
	}

	// Si ese email ya es user y ya es miembro, rechazamos sin crear fila.
	// Si es user pero no miembro, igual seguimos — el accept lo agrega.
	if existing, err := s.users.GetByEmail(ctx, email); err == nil {
		isMember, err := s.households.IsMember(ctx, householdID, existing.ID)
		if err != nil {
			return InviteResult{}, err
		}
		if isMember {
			return InviteResult{}, fmt.Errorf("%s ya es miembro del hogar: %w", email, domain.ErrConflict)
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return InviteResult{}, err
	}

	token, hash, err := generateToken()
	if err != nil {
		return InviteResult{}, err
	}
	expiresAt := time.Now().Add(s.ttl)

	invite, err := s.repo.Create(ctx, householdID, email, hash, inviterID, expiresAt)
	if err != nil {
		return InviteResult{}, err
	}

	acceptURL := s.appBaseURL + "/invite/accept?token=" + token
	hh, _ := s.households.GetByID(ctx, householdID)

	emailSent := false
	if s.sender != nil && s.sender.Configured() {
		html := renderInviteHTML(hh.Name, acceptURL, expiresAt)
		subject := "Te invitaron a unirte al hogar " + hh.Name + " en Ahorra"
		if err := s.sender.Send(ctx, []string{email}, subject, html); err != nil {
			// No revertimos la invitación si falla el mail: el owner puede
			// copiar el acceptURL manualmente del response.
			s.logger.WarnContext(ctx, "invite email falló",
				"inviteId", invite.ID.String(), "error", err)
		} else {
			emailSent = true
		}
	} else {
		s.logger.InfoContext(ctx, "invite creada sin email (sender no configurado)",
			"inviteId", invite.ID.String())
	}

	return InviteResult{
		Invite:    invite,
		Token:     token,
		AcceptURL: acceptURL,
		EmailSent: emailSent,
	}, nil
}

// Resend reemite una invitación pendiente: genera un nuevo token, rota el
// hash en DB, extiende expires_at y reenvía el mail. El token viejo queda
// inutilizable (su hash ya no existe). Rechaza si la invite no está pendiente.
//
// Solo el owner del hogar puede reenviar.
func (s *InvitesService) Resend(ctx context.Context, callerID, inviteID uuid.UUID) (InviteResult, error) {
	inv, err := s.repo.GetByID(ctx, inviteID)
	if err != nil {
		return InviteResult{}, err
	}
	role, err := s.households.GetMemberRole(ctx, inv.HouseholdID, callerID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return InviteResult{}, domain.ErrForbidden
		}
		return InviteResult{}, err
	}
	if role != domain.RoleOwner {
		return InviteResult{}, domain.ErrForbidden
	}
	if status := statusOf(inv); status != "pending" {
		return InviteResult{}, fmt.Errorf("invitación %s — no se puede reenviar: %w", status, domain.ErrConflict)
	}

	token, hash, err := generateToken()
	if err != nil {
		return InviteResult{}, err
	}
	expiresAt := time.Now().Add(s.ttl)

	refreshed, err := s.repo.RefreshToken(ctx, inviteID, hash, expiresAt)
	if err != nil {
		return InviteResult{}, err
	}

	acceptURL := s.appBaseURL + "/invite/accept?token=" + token
	hh, _ := s.households.GetByID(ctx, inv.HouseholdID)

	emailSent := false
	if s.sender != nil && s.sender.Configured() {
		html := renderInviteHTML(hh.Name, acceptURL, expiresAt)
		subject := "Recordatorio: te invitaron al hogar " + hh.Name + " en Ahorra"
		if err := s.sender.Send(ctx, []string{inv.Email}, subject, html); err != nil {
			s.logger.WarnContext(ctx, "invite resend email falló",
				"inviteId", inviteID.String(), "error", err)
		} else {
			emailSent = true
		}
	} else {
		s.logger.InfoContext(ctx, "invite resend sin email (sender no configurado)",
			"inviteId", inviteID.String())
	}

	return InviteResult{
		Invite:    refreshed,
		Token:     token,
		AcceptURL: acceptURL,
		EmailSent: emailSent,
	}, nil
}

// ListPending devuelve las invitaciones activas del hogar. Requiere owner.
func (s *InvitesService) ListPending(ctx context.Context, userID, householdID uuid.UUID) ([]domain.HouseholdInvite, error) {
	role, err := s.households.GetMemberRole(ctx, householdID, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.ErrForbidden
		}
		return nil, err
	}
	if role != domain.RoleOwner {
		return nil, domain.ErrForbidden
	}
	return s.repo.ListPending(ctx, householdID)
}

// Revoke cancela una invitación pendiente. Requiere owner del hogar al
// que pertenece la invitación.
func (s *InvitesService) Revoke(ctx context.Context, userID, inviteID uuid.UUID) error {
	inv, err := s.repo.GetByID(ctx, inviteID)
	if err != nil {
		return err
	}
	role, err := s.households.GetMemberRole(ctx, inv.HouseholdID, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrForbidden
		}
		return err
	}
	if role != domain.RoleOwner {
		return domain.ErrForbidden
	}
	if inv.AcceptedAt != nil || inv.RevokedAt != nil {
		return fmt.Errorf("invitación ya no está pendiente: %w", domain.ErrConflict)
	}
	_, err = s.repo.Revoke(ctx, inviteID)
	return err
}

// InvitePreview se devuelve al front cuando consulta el token (antes de
// aceptar) — expone info mínima para mostrar "Te invitaron a X" sin pedir
// auth. No revela nada sensible: nombre de hogar y email invitado.
type InvitePreview struct {
	HouseholdID   uuid.UUID `json:"householdId"`
	HouseholdName string    `json:"householdName"`
	Email         string    `json:"email"`
	ExpiresAt     time.Time `json:"expiresAt"`
	Status        string    `json:"status"` // pending | accepted | revoked | expired
}

// Inspect busca una invitación por token plano (le pegamos SHA-256 y
// buscamos por hash). Público: el frontend lo llama con el token de la URL
// antes de pedirle al user que se loguee/registre.
func (s *InvitesService) Inspect(ctx context.Context, token string) (InvitePreview, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return InvitePreview{}, domain.NewValidationError("token", "vacío")
	}
	inv, err := s.repo.GetByTokenHash(ctx, hashToken(token))
	if err != nil {
		return InvitePreview{}, err
	}
	hh, err := s.households.GetByID(ctx, inv.HouseholdID)
	if err != nil {
		return InvitePreview{}, err
	}
	return InvitePreview{
		HouseholdID:   inv.HouseholdID,
		HouseholdName: hh.Name,
		Email:         inv.Email,
		ExpiresAt:     inv.ExpiresAt,
		Status:        statusOf(inv),
	}, nil
}

// Accept aplica la invitación a un user autenticado. Valida que el email
// del invite matchee el del user (anti-forwarding: no podés reenviar el
// link a otra cuenta y entrar gratis).
func (s *InvitesService) Accept(ctx context.Context, userID uuid.UUID, token string) (domain.HouseholdMember, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return domain.HouseholdMember{}, domain.NewValidationError("token", "vacío")
	}

	inv, err := s.repo.GetByTokenHash(ctx, hashToken(token))
	if err != nil {
		return domain.HouseholdMember{}, err
	}
	if status := statusOf(inv); status != "pending" {
		return domain.HouseholdMember{}, fmt.Errorf("invitación %s: %w", status, domain.ErrConflict)
	}

	// Validar email del user autenticado == email invitado.
	u, err := s.users.GetByEmail(ctx, inv.Email)
	if err != nil {
		return domain.HouseholdMember{}, err
	}
	if u.ID != userID {
		// No filtramos "ese token es de otro user": devolvemos Forbidden.
		return domain.HouseholdMember{}, domain.ErrForbidden
	}

	var memberHook AfterMemberHook
	if s.splitRules != nil {
		memberHook = func(ctx context.Context, tx pgx.Tx, hID, uID uuid.UUID) error {
			return s.splitRules.SeedForMemberTx(ctx, tx, hID, uID)
		}
	}

	_, member, err := s.repo.AcceptAndAddMember(ctx, inv.ID, userID, memberHook)
	if err != nil {
		return domain.HouseholdMember{}, err
	}

	if s.push != nil {
		hh, _ := s.households.GetByID(ctx, inv.HouseholdID)
		s.push.NotifyUsers(
			ctx,
			[]uuid.UUID{userID},
			"Te uniste a un hogar",
			"Ahora sos miembro de "+hh.Name,
			"/households/"+inv.HouseholdID.String(),
			"household-invite:"+inv.HouseholdID.String(),
		)
	}
	return member, nil
}

// AcceptOnRegister es la variante usada desde /auth/register: el user
// recién creado acaba de confirmar su email al llegar vía link, entonces
// saltamos el chequeo estricto de match exacto y solo validamos que el
// email del registro === email del invite.
//
// Corre típicamente en el mismo request que creó el user, pero fuera de
// la tx del INSERT users (consistente con el bootstrap existente: si falla,
// logueamos y el user queda creado sin hogar; puede reintentar manualmente).
func (s *InvitesService) AcceptOnRegister(ctx context.Context, userID uuid.UUID, userEmail, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil // no invite, no-op
	}
	inv, err := s.repo.GetByTokenHash(ctx, hashToken(token))
	if err != nil {
		return err
	}
	if status := statusOf(inv); status != "pending" {
		return fmt.Errorf("invitación %s: %w", status, domain.ErrConflict)
	}
	if !strings.EqualFold(strings.TrimSpace(inv.Email), strings.TrimSpace(userEmail)) {
		return domain.ErrForbidden
	}

	var memberHook AfterMemberHook
	if s.splitRules != nil {
		memberHook = func(ctx context.Context, tx pgx.Tx, hID, uID uuid.UUID) error {
			return s.splitRules.SeedForMemberTx(ctx, tx, hID, uID)
		}
	}
	_, _, err = s.repo.AcceptAndAddMember(ctx, inv.ID, userID, memberHook)
	return err
}

// ===================== helpers =====================

// generateToken: 32 bytes random → base64url sin padding (~43 chars).
// Devuelve también el SHA-256 hex que guardamos en DB.
func generateToken() (plain, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("invites.generateToken: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	hash = hashToken(plain)
	return plain, hash, nil
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func statusOf(inv domain.HouseholdInvite) string {
	switch {
	case inv.RevokedAt != nil:
		return "revoked"
	case inv.AcceptedAt != nil:
		return "accepted"
	case time.Now().After(inv.ExpiresAt):
		return "expired"
	default:
		return "pending"
	}
}
