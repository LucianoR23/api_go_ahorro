package categories

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// Service: validaciones de input + autorización por hogar.
// La pertenencia al hogar la valida el middleware RequireHouseholdMember
// ANTES de llegar acá, así que este service asume householdID autorizado.
type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

const (
	defaultIcon  = "💰"
	defaultColor = "#2E75B6"
	maxNameLen   = 40
)

// Create valida y crea una categoría bajo el hogar indicado.
func (s *Service) Create(ctx context.Context, householdID uuid.UUID, name, icon, color string) (domain.Category, error) {
	name = strings.TrimSpace(name)
	icon = strings.TrimSpace(icon)
	color = strings.TrimSpace(color)

	if name == "" {
		return domain.Category{}, domain.NewValidationError("name", "no puede estar vacío")
	}
	if len([]rune(name)) > maxNameLen {
		return domain.Category{}, domain.NewValidationError("name", "máximo 40 caracteres")
	}
	if icon == "" {
		icon = defaultIcon
	}
	if color == "" {
		color = defaultColor
	}
	return s.repo.Create(ctx, householdID, name, icon, color)
}

// List devuelve las categorías del hogar.
func (s *Service) List(ctx context.Context, householdID uuid.UUID) ([]domain.Category, error) {
	return s.repo.ListByHousehold(ctx, householdID)
}

// Update requiere que la categoría pertenezca al hogar del caller —
// sino devolvemos NotFound (para no filtrar existencia cross-hogar).
func (s *Service) Update(ctx context.Context, householdID, categoryID uuid.UUID, name, icon, color string) (domain.Category, error) {
	if err := s.requireCategoryInHousehold(ctx, householdID, categoryID); err != nil {
		return domain.Category{}, err
	}

	name = strings.TrimSpace(name)
	icon = strings.TrimSpace(icon)
	color = strings.TrimSpace(color)

	if name == "" {
		return domain.Category{}, domain.NewValidationError("name", "no puede estar vacío")
	}
	if len([]rune(name)) > maxNameLen {
		return domain.Category{}, domain.NewValidationError("name", "máximo 40 caracteres")
	}
	if icon == "" {
		icon = defaultIcon
	}
	if color == "" {
		color = defaultColor
	}
	return s.repo.Update(ctx, categoryID, name, icon, color)
}

// Delete elimina la categoría. ON DELETE SET NULL en expenses preserva
// los gastos históricos sin categoría.
func (s *Service) Delete(ctx context.Context, householdID, categoryID uuid.UUID) error {
	if err := s.requireCategoryInHousehold(ctx, householdID, categoryID); err != nil {
		return err
	}
	return s.repo.Delete(ctx, categoryID)
}

// SeedDefaults expone el seeding transaccional para que households lo use.
// No se llama desde el handler.
func (s *Service) Seeder() *Repository { return s.repo }

func (s *Service) requireCategoryInHousehold(ctx context.Context, householdID, categoryID uuid.UUID) error {
	cat, err := s.repo.GetByID(ctx, categoryID)
	if err != nil {
		return err
	}
	if cat.HouseholdID != householdID {
		// No filtramos "existe pero no es tuya": devolvemos NotFound.
		return domain.ErrNotFound
	}
	return nil
}

