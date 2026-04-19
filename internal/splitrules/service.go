package splitrules

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// householdLookup: interface mínima para validar ownership sin acoplar
// a households.Service. La implementa households.Repository (GetMemberRole,
// IsMember). Inyectada desde main para evitar import cycle.
type householdLookup interface {
	GetMemberRole(ctx context.Context, householdID, userID uuid.UUID) (domain.Role, error)
	IsMember(ctx context.Context, householdID, userID uuid.UUID) (bool, error)
}

type Service struct {
	repo       *Repository
	households householdLookup
}

func NewService(repo *Repository, households householdLookup) *Service {
	return &Service{repo: repo, households: households}
}

// SeedForMemberTx es el hook que households.Repository invoca tras
// agregar un miembro (en CreateWithOwner para el owner, en AddMember
// para el invitado). Default weight=1.0 → división equitativa.
func (s *Service) SeedForMemberTx(ctx context.Context, tx pgx.Tx, householdID, userID uuid.UUID) error {
	return s.repo.UpsertTx(ctx, tx, householdID, userID, 1.0)
}

// List devuelve todas las reglas del hogar (ownership lo valida
// el middleware RequireHouseholdMember aguas arriba).
func (s *Service) List(ctx context.Context, householdID uuid.UUID) ([]domain.SplitRule, error) {
	return s.repo.ListByHousehold(ctx, householdID)
}

// UpdateInput: un peso por userID. El service valida que todos los
// userIDs sean miembros del hogar antes de aplicar.
type UpdateInput struct {
	UserID uuid.UUID
	Weight float64
}

// Update: aplica cambios batch. Solo el owner puede editar split.
// Valida que los userIDs sean miembros y que weight >= 0.
// No requiere enviar todos los miembros — los omitidos quedan como estaban.
// Weight=0 es válido (ese miembro no participa en splits pero sigue siendo
// miembro del hogar a otros efectos).
func (s *Service) Update(ctx context.Context, requesterID, householdID uuid.UUID, items []UpdateInput) ([]domain.SplitRule, error) {
	if err := s.requireOwner(ctx, householdID, requesterID); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, domain.NewValidationError("items", "enviá al menos un peso a actualizar")
	}
	for _, it := range items {
		if it.Weight < 0 {
			return nil, domain.NewValidationError("weight", "no puede ser negativo")
		}
		ok, err := s.households.IsMember(ctx, householdID, it.UserID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, domain.NewValidationError("userId", fmt.Sprintf("%s no es miembro del hogar", it.UserID))
		}
	}
	// Upsert secuencial. Si en algún momento hay contención, envolver en tx.
	out := make([]domain.SplitRule, 0, len(items))
	for _, it := range items {
		rule, err := s.repo.Upsert(ctx, householdID, it.UserID, it.Weight)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

// WeightsForHousehold devuelve un mapa userID → weight listo para consumir
// por expenses.Service al construir shares. Si un miembro no tiene regla
// cargada (raro: el bootstrap lo cubre), se asume weight=1.0.
func (s *Service) WeightsForHousehold(ctx context.Context, householdID uuid.UUID) (map[uuid.UUID]float64, error) {
	rules, err := s.repo.ListByHousehold(ctx, householdID)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]float64, len(rules))
	for _, r := range rules {
		out[r.UserID] = r.Weight
	}
	return out, nil
}

func (s *Service) requireOwner(ctx context.Context, householdID, userID uuid.UUID) error {
	role, err := s.households.GetMemberRole(ctx, householdID, userID)
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
