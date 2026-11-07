package households

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

// userLookup es la interface mínima que necesitamos del paquete users
// para invitar: buscar por email. Definida acá (interface en el consumidor)
// para no acoplar.
type userLookup interface {
	GetByEmail(ctx context.Context, email string) (domain.User, error)
}

// categoriesSeeder: lo implementa categories.Repository con SeedDefaultsTx.
// Se usa dentro de la tx de CreateWithOwner para sembrar las 7 categorías
// default del hogar recién creado, de forma atómica.
type categoriesSeeder interface {
	SeedDefaultsTx(ctx context.Context, tx pgx.Tx, householdID uuid.UUID) error
}

// splitRulesSeeder: lo implementa splitrules.Service con SeedForMemberTx.
// Se invoca dentro de la tx al crear un hogar (para el owner) y al sumar
// miembros (para el invitado). Sin él, el split queda incompleto y los
// shares caerían al fallback equitativo.
type splitRulesSeeder interface {
	SeedForMemberTx(ctx context.Context, tx pgx.Tx, householdID, userID uuid.UUID) error
}

// Monedas soportadas por el sistema (coincide con los fetchers de bluelytics).
// Si agregamos más, el fetcher y el validador se actualizan juntos.
var supportedCurrencies = map[string]struct{}{
	"ARS": {},
	"USD": {},
	"EUR": {},
}

// pushNotifier: nil-safe. Notifica al invitado cuando se lo agrega a un hogar.
type pushNotifier interface {
	NotifyUsers(ctx context.Context, userIDs []uuid.UUID, title, body, url, tag string)
}

type Service struct {
	repo       *Repository
	users      userLookup
	categories categoriesSeeder
	splitRules splitRulesSeeder
	push       pushNotifier
}

func NewService(repo *Repository, users userLookup, categories categoriesSeeder, splitRules splitRulesSeeder) *Service {
	return &Service{repo: repo, users: users, categories: categories, splitRules: splitRules}
}

// SetNotifier cablea push post-construcción.
func (s *Service) SetNotifier(n pushNotifier) {
	s.push = n
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

	createHook, memberHook := s.seedHooks()

	// One shared space per user (plan 1.2). The database enforces it too;
	// checking here gives the user the name of the hogar they are in.
	if current, err := s.repo.ActiveSharedForUser(ctx, ownerID); err == nil {
		return domain.Household{}, domain.NewConflictError(
			"Ya sos parte del hogar " + current.Name + ". Para crear otro, primero salí de " + current.Name + ".")
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Household{}, err
	}
	return s.repo.CreateWithOwner(ctx, name, baseCurrency, domain.HouseholdShared, ownerID, createHook, memberHook)
}

// seedHooks returns the hooks that seed a new household inside its
// transaction: default categories and the owner's split rule. Both seeders
// are optional (tests).
func (s *Service) seedHooks() (AfterCreateHook, AfterMemberHook) {
	var createHook AfterCreateHook
	if s.categories != nil {
		createHook = func(ctx context.Context, tx pgx.Tx, householdID uuid.UUID) error {
			return s.categories.SeedDefaultsTx(ctx, tx, householdID)
		}
	}
	var memberHook AfterMemberHook
	if s.splitRules != nil {
		memberHook = func(ctx context.Context, tx pgx.Tx, householdID, userID uuid.UUID) error {
			return s.splitRules.SeedForMemberTx(ctx, tx, householdID, userID)
		}
	}
	return createHook, memberHook
}

// Name and currency of a new "Mi plata". The app shows its own label for the
// personal space; the name is what lists and exports read.
const (
	personalName     = "Mi plata"
	personalCurrency = "ARS"
)

// EnsurePersonalTx creates the user's "Mi plata" inside the caller's
// transaction (registration). Plan 1.2.
func (s *Service) EnsurePersonalTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	createHook, memberHook := s.seedHooks()
	_, err := s.repo.CreateWithOwnerTx(ctx, tx, personalName, personalCurrency, domain.HouseholdPersonal, userID, createHook, memberHook)
	return err
}

// EnsurePersonal returns the user's "Mi plata", creating it when missing:
// accounts from before plan 1.2, or a Google sign-up whose creation failed.
// Safe to call concurrently (the unique index decides; the loser reads).
func (s *Service) EnsurePersonal(ctx context.Context, userID uuid.UUID) (domain.Household, error) {
	if h, err := s.repo.PersonalForUser(ctx, userID); err == nil || !errors.Is(err, domain.ErrNotFound) {
		return h, err
	}
	createHook, memberHook := s.seedHooks()
	h, err := s.repo.CreateWithOwner(ctx, personalName, personalCurrency, domain.HouseholdPersonal, userID, createHook, memberHook)
	if errors.Is(err, domain.ErrConflict) {
		return s.repo.PersonalForUser(ctx, userID)
	}
	return h, err
}

// List devuelve los hogares del user, "Mi plata" primero (la crea si falta).
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]domain.Household, error) {
	if _, err := s.EnsurePersonal(ctx, userID); err != nil {
		return nil, err
	}
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
	if err := s.requireShared(ctx, householdID); err != nil {
		return err
	}
	return s.repo.Delete(ctx, householdID)
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

// TransferOwnership: el current owner cede el rol a otro miembro. Atómico
// (demote + promote en una tx). Invariantes:
//   - caller es el owner actual del hogar
//   - target es miembro existente del hogar (y distinto del caller)
//
// No aceptamos "crear un segundo owner" — el modelo actual es single-owner.
func (s *Service) TransferOwnership(ctx context.Context, callerID, householdID, targetUserID uuid.UUID) error {
	if callerID == targetUserID {
		return domain.NewValidationError("userId", "no podés transferir la propiedad a vos mismo")
	}
	if err := s.requireOwner(ctx, householdID, callerID); err != nil {
		return err
	}
	targetRole, err := s.repo.GetMemberRole(ctx, householdID, targetUserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.NewValidationError("userId", "el usuario no es miembro del hogar")
		}
		return err
	}
	if targetRole == domain.RoleOwner {
		// No debería pasar en el modelo single-owner, pero defendemos.
		return fmt.Errorf("el usuario ya es owner: %w", domain.ErrConflict)
	}
	return s.repo.TransferOwnership(ctx, householdID, callerID, targetUserID)
}

// ListMembers devuelve los miembros del hogar con su info de user.
// Requiere membresía (se valida en el middleware).
func (s *Service) ListMembers(ctx context.Context, householdID uuid.UUID) ([]domain.HouseholdMemberDetail, error) {
	return s.repo.ListMembers(ctx, householdID)
}

// ===================== admin-only =====================
//
// Estos métodos NO chequean ownership: son disparados desde endpoints
// /admin/* que ya pasan por auth.RequireSuperadmin. Nunca deben invocarse
// desde rutas públicas.

// ListDeleted devuelve todos los hogares soft-deleted con su owner actual.
// Admin-only.
func (s *Service) ListDeleted(ctx context.Context) ([]domain.HouseholdWithOwner, error) {
	return s.repo.ListDeleted(ctx)
}

// GetAny devuelve cualquier hogar, incluso soft-deleted. Admin-only.
func (s *Service) GetAny(ctx context.Context, id uuid.UUID) (domain.Household, error) {
	return s.repo.GetByIDIncludingDeleted(ctx, id)
}

// Restore limpia deleted_at. Admin-only. Idempotente: si el hogar ya está
// activo no hace nada (la query filtra WHERE deleted_at IS NOT NULL).
func (s *Service) Restore(ctx context.Context, id uuid.UUID) error {
	h, err := s.repo.GetByIDIncludingDeleted(ctx, id)
	if err != nil {
		return err
	}
	if h.DeletedAt == nil {
		return domain.NewValidationError("id", "el hogar no está borrado")
	}
	return s.repo.Restore(ctx, id)
}

// Purge hace el DELETE físico con CASCADE. Admin-only. Para evitar purgar
// por accidente un hogar vivo, exigimos que esté soft-deleted primero.
func (s *Service) Purge(ctx context.Context, id uuid.UUID) error {
	h, err := s.repo.GetByIDIncludingDeleted(ctx, id)
	if err != nil {
		return err
	}
	if h.DeletedAt == nil {
		return domain.NewValidationError("id", "hay que soft-delete primero (DELETE /households/{id}) antes de purgar")
	}
	return s.repo.Purge(ctx, id)
}

// ===================== helpers internos =====================

// requireShared rejects operations that only make sense in a "Hogar" (delete,
// invite) when the target is "Mi plata" (plan 1.2).
func (s *Service) requireShared(ctx context.Context, householdID uuid.UUID) error {
	h, err := s.repo.GetByID(ctx, householdID)
	if err != nil {
		return err
	}
	if h.Kind == domain.HouseholdPersonal {
		return domain.NewValidationError("household", "Mi plata es solo tuya: no se puede borrar ni compartir")
	}
	return nil
}

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
