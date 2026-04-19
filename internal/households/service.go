package households

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// userLookup es la interface mínima que necesitamos del paquete users
// para invitar: buscar por email. Definida acá (interface en el consumidor)
// para no acoplar.
type userLookup interface {
	GetByEmail(ctx context.Context, email string) (domain.User, error)
}

// Monedas soportadas por el sistema (coincide con los fetchers de bluelytics).
// Si agregamos más, el fetcher y el validador se actualizan juntos.
var supportedCurrencies = map[string]struct{}{
	"ARS": {},
	"USD": {},
	"EUR": {},
}

type Service struct {
	repo  *Repository
	users userLookup
}

func NewService(repo *Repository, users userLookup) *Service {
	return &Service{repo: repo, users: users}
}

// Create valida input, normaliza currency y crea el hogar con el caller
// como owner (atómico).
func (s *Service) Create(ctx context.Context, ownerID uuid.UUID, name, baseCurrency string) (domain.Household, error) {
	name = strings.TrimSpace(name)
	baseCurrency = strings.ToUpper(strings.TrimSpace(baseCurrency))

	if name == "" {
		return domain.Household{}, domain.NewValidationError("name", "no puede estar vacío")
	}
	if err := validateCurrency(baseCurrency); err != nil {
		return domain.Household{}, err
	}
	return s.repo.CreateWithOwner(ctx, name, baseCurrency, ownerID)
}

// List devuelve los hogares del user. Sin filtros por ahora.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]domain.Household, error) {
	return s.repo.ListForUser(ctx, userID)
}

// Get devuelve el detalle de un hogar. Asume que el middleware ya
// verificó membresía — si no, esto sería una brecha.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (domain.Household, error) {
	return s.repo.GetByID(ctx, id)
}

// Update requiere rol owner. El service valida, el middleware solo
// valida membresía genérica.
func (s *Service) Update(ctx context.Context, userID, householdID uuid.UUID, name, baseCurrency string) (domain.Household, error) {
	if err := s.requireOwner(ctx, householdID, userID); err != nil {
		return domain.Household{}, err
	}

	name = strings.TrimSpace(name)
	baseCurrency = strings.ToUpper(strings.TrimSpace(baseCurrency))

	if name == "" {
		return domain.Household{}, domain.NewValidationError("name", "no puede estar vacío")
	}
	if err := validateCurrency(baseCurrency); err != nil {
		return domain.Household{}, err
	}
	return s.repo.Update(ctx, householdID, name, baseCurrency)
}

// Delete requiere owner. CASCADE en household_members limpia la membresía.
func (s *Service) Delete(ctx context.Context, userID, householdID uuid.UUID) error {
	if err := s.requireOwner(ctx, householdID, userID); err != nil {
		return err
	}
	return s.repo.Delete(ctx, householdID)
}

// InviteByEmail: owner invita a un user existente. El invitado debe
// estar registrado (registro abierto, no hay invitación pre-cuenta).
// Futuro: agregar flujo de invite link si hace falta que el invitado
// no exista todavía.
func (s *Service) InviteByEmail(ctx context.Context, inviterID, householdID uuid.UUID, email string) (domain.HouseholdMember, error) {
	if err := s.requireOwner(ctx, householdID, inviterID); err != nil {
		return domain.HouseholdMember{}, err
	}

	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return domain.HouseholdMember{}, domain.NewValidationError("email", "no puede estar vacío")
	}

	user, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.HouseholdMember{}, domain.NewValidationError("email", "el usuario no está registrado")
		}
		return domain.HouseholdMember{}, err
	}

	return s.repo.AddMember(ctx, householdID, user.ID, domain.RoleMember)
}

// RemoveMember: owner puede sacar a otros. Además, cualquier member puede
// sacarse a sí mismo (self-leave). Un owner no puede auto-borrarse si es
// el único owner — sino quedaría un hogar sin dueño.
func (s *Service) RemoveMember(ctx context.Context, requesterID, householdID, targetUserID uuid.UUID) error {
	if requesterID == targetUserID {
		// self-leave: chequeamos que no sea el último owner.
		role, err := s.repo.GetMemberRole(ctx, householdID, requesterID)
		if err != nil {
			return err
		}
		if role == domain.RoleOwner {
			return domain.NewValidationError("user", "el owner no puede salir del hogar, transferí la propiedad o borrá el hogar")
		}
	} else {
		// remover a otro: requiere owner.
		if err := s.requireOwner(ctx, householdID, requesterID); err != nil {
			return err
		}
	}
	return s.repo.RemoveMember(ctx, householdID, targetUserID)
}

// ListMembers devuelve los miembros del hogar con su info de user.
// Requiere membresía (se valida en el middleware).
func (s *Service) ListMembers(ctx context.Context, householdID uuid.UUID) ([]domain.HouseholdMemberDetail, error) {
	return s.repo.ListMembers(ctx, householdID)
}

// ===================== helpers internos =====================

func (s *Service) requireOwner(ctx context.Context, householdID, userID uuid.UUID) error {
	role, err := s.repo.GetMemberRole(ctx, householdID, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrForbidden
		}
		return err
	}
	if role != domain.RoleOwner {
		return domain.ErrForbidden
	}
	return nil
}

func validateCurrency(currency string) error {
	if currency == "" {
		return domain.NewValidationError("baseCurrency", "no puede estar vacío")
	}
	if _, ok := supportedCurrencies[currency]; !ok {
		return domain.NewValidationError("baseCurrency", fmt.Sprintf("moneda no soportada: %s (usar ARS, USD, EUR)", currency))
	}
	return nil
}
