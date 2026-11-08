package categories

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/domain"
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
	defaultIcon  = "tag"
	defaultColor = "#2E75B6"
	maxNameLen   = 40
	maxIconLen   = 32
)

// iconKey is a Lucide icon key: lowercase words joined by hyphens. The API
// checks the format only; the app shows its default icon for a key it does
// not know, so the list lives in one place.
var iconKey = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// CreateInput is a new category. ParentID makes it a subcategory.
type CreateInput struct {
	Name     string
	Icon     string
	Color    string
	ParentID *uuid.UUID
}

// UpdateInput replaces name, icon and color (empty icon and color fall back
// to the defaults). The parent changes only when SetParent is true; a nil
// ParentID then makes it top-level.
type UpdateInput struct {
	Name      string
	Icon      string
	Color     string
	SetParent bool
	ParentID  *uuid.UUID
}

// Create valida y crea una categoría bajo el hogar indicado.
func (s *Service) Create(ctx context.Context, householdID uuid.UUID, in CreateInput) (domain.Category, error) {
	fields, err := cleanFields(in.Name, in.Icon, in.Color)
	if err != nil {
		return domain.Category{}, err
	}
	if in.ParentID != nil {
		if err := s.checkParent(ctx, householdID, *in.ParentID); err != nil {
			return domain.Category{}, err
		}
	}
	fields.ParentID = in.ParentID
	return s.repo.Create(ctx, householdID, fields)
}

// List devuelve las categorías del hogar.
func (s *Service) List(ctx context.Context, householdID uuid.UUID) ([]domain.Category, error) {
	return s.repo.ListByHousehold(ctx, householdID)
}

// Update requiere que la categoría pertenezca al hogar del caller —
// sino devolvemos NotFound (para no filtrar existencia cross-hogar).
func (s *Service) Update(ctx context.Context, householdID, categoryID uuid.UUID, in UpdateInput) (domain.Category, error) {
	cat, err := s.categoryInHousehold(ctx, householdID, categoryID)
	if err != nil {
		return domain.Category{}, err
	}
	fields, err := cleanFields(in.Name, in.Icon, in.Color)
	if err != nil {
		return domain.Category{}, err
	}
	fields.ParentID = cat.ParentID
	if in.SetParent {
		if in.ParentID != nil {
			if *in.ParentID == categoryID {
				return domain.Category{}, domain.NewValidationError("parentId", "no puede ser la misma categoría")
			}
			if err := s.checkParent(ctx, householdID, *in.ParentID); err != nil {
				return domain.Category{}, err
			}
			hasChildren, err := s.repo.HasChildren(ctx, categoryID)
			if err != nil {
				return domain.Category{}, err
			}
			if hasChildren {
				return domain.Category{}, domain.NewValidationError("parentId", "tiene subcategorías, no puede ser una")
			}
		}
		fields.ParentID = in.ParentID
	}
	return s.repo.Update(ctx, categoryID, fields)
}

// Delete elimina la categoría. ON DELETE SET NULL en expenses preserva
// los gastos históricos sin categoría, and its subcategories become
// top-level categories.
func (s *Service) Delete(ctx context.Context, householdID, categoryID uuid.UUID) error {
	if _, err := s.categoryInHousehold(ctx, householdID, categoryID); err != nil {
		return err
	}
	return s.repo.Delete(ctx, categoryID)
}

// SeedDefaults expone el seeding transaccional para que households lo use.
// No se llama desde el handler.
func (s *Service) Seeder() *Repository { return s.repo }

func cleanFields(name, icon, color string) (CategoryFields, error) {
	name = strings.TrimSpace(name)
	icon = strings.TrimSpace(icon)
	color = strings.TrimSpace(color)

	if name == "" {
		return CategoryFields{}, domain.NewValidationError("name", "no puede estar vacío")
	}
	if len([]rune(name)) > maxNameLen {
		return CategoryFields{}, domain.NewValidationError("name", "máximo 40 caracteres")
	}
	if icon == "" {
		icon = defaultIcon
	}
	if len(icon) > maxIconLen || !iconKey.MatchString(icon) {
		return CategoryFields{}, domain.NewValidationError("icon", "no es un ícono válido")
	}
	if color == "" {
		color = defaultColor
	}
	return CategoryFields{Name: name, Icon: icon, Color: color}, nil
}

// checkParent: the parent is a top-level category of the same household.
func (s *Service) checkParent(ctx context.Context, householdID, parentID uuid.UUID) error {
	parent, err := s.repo.GetByID(ctx, parentID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && parent.HouseholdID != householdID) {
		return domain.NewValidationError("parentId", "no existe")
	}
	if err != nil {
		return err
	}
	if parent.ParentID != nil {
		return domain.NewValidationError("parentId", "una subcategoría no puede tener subcategorías")
	}
	return nil
}

func (s *Service) categoryInHousehold(ctx context.Context, householdID, categoryID uuid.UUID) (domain.Category, error) {
	cat, err := s.repo.GetByID(ctx, categoryID)
	if err != nil {
		return domain.Category{}, err
	}
	if cat.HouseholdID != householdID {
		// No filtramos "existe pero no es tuya": devolvemos NotFound.
		return domain.Category{}, domain.ErrNotFound
	}
	return cat, nil
}
