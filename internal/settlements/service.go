package settlements

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// householdLookup: interface mínima del paquete households. Lo usamos para
// validar membresía de ambos lados (from/to) y para leer base_currency del
// hogar (la moneda del pago debe matchear).
type householdLookup interface {
	IsMember(ctx context.Context, householdID, userID uuid.UUID) (bool, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.Household, error)
}

// balanceReader: interface del paquete balances. Nos da el saldo firmado
// entre from y to para validar que el pago no exceda la deuda.
// Positivo = from debe a to; negativo = to debe a from.
type balanceReader interface {
	PairNet(ctx context.Context, householdID, from, to uuid.UUID) (float64, error)
}

type Service struct {
	repo       *Repository
	households householdLookup
	balances   balanceReader
}

func NewService(repo *Repository, households householdLookup, balances balanceReader) *Service {
	return &Service{repo: repo, households: households, balances: balances}
}

// CreateInput: payload del endpoint POST /settlements. El handler parsea
// fechas y amounts antes de armar esto.
type CreateInput struct {
	HouseholdID uuid.UUID
	FromUser    uuid.UUID
	ToUser      uuid.UUID
	Amount      float64
	Note        *string
	PaidAt      time.Time
}

// Create valida y registra un pago. Reglas:
//   - from != to
//   - amount > 0
//   - from y to deben ser miembros del hogar
//   - amount <= deuda_actual(from → to) + epsilon (no sobre-pagar)
//
// La moneda queda fijada a household.base_currency — no se acepta override.
// Si el frontend quiere registrar un pago en otra moneda, debe convertirlo
// antes (nosotros no aplicamos fx acá para no confundir el libro de deudas).
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.SettlementPayment, error) {
	if in.FromUser == in.ToUser {
		return domain.SettlementPayment{}, domain.NewValidationError("toUser", "from y to no pueden ser el mismo user")
	}
	if in.Amount <= 0 {
		return domain.SettlementPayment{}, domain.NewValidationError("amount", "debe ser mayor a cero")
	}
	if in.PaidAt.IsZero() {
		in.PaidAt = time.Now()
	}
	if in.Note != nil {
		trimmed := strings.TrimSpace(*in.Note)
		if trimmed == "" {
			in.Note = nil
		} else {
			in.Note = &trimmed
		}
	}

	// Membership check de ambos lados.
	okFrom, err := s.households.IsMember(ctx, in.HouseholdID, in.FromUser)
	if err != nil {
		return domain.SettlementPayment{}, err
	}
	if !okFrom {
		return domain.SettlementPayment{}, domain.NewValidationError("fromUser", "no es miembro del hogar")
	}
	okTo, err := s.households.IsMember(ctx, in.HouseholdID, in.ToUser)
	if err != nil {
		return domain.SettlementPayment{}, err
	}
	if !okTo {
		return domain.SettlementPayment{}, domain.NewValidationError("toUser", "no es miembro del hogar")
	}

	// Moneda fija = base_currency del hogar.
	h, err := s.households.GetByID(ctx, in.HouseholdID)
	if err != nil {
		return domain.SettlementPayment{}, err
	}

	// Validación clave: no se puede pagar más de lo que se debe.
	// Tolerancia 1¢ para evitar rebotes por redondeo.
	balance, err := s.balances.PairNet(ctx, in.HouseholdID, in.FromUser, in.ToUser)
	if err != nil {
		return domain.SettlementPayment{}, err
	}
	if balance <= 0 {
		return domain.SettlementPayment{}, domain.NewValidationError("amount", "from no tiene deuda con to")
	}
	if in.Amount > balance+0.01 {
		return domain.SettlementPayment{}, domain.NewValidationError("amount", fmt.Sprintf("excede la deuda actual (%.2f %s)", balance, h.BaseCurrency))
	}

	return s.repo.Create(ctx, CreateParams{
		HouseholdID:  in.HouseholdID,
		FromUser:     in.FromUser,
		ToUser:       in.ToUser,
		AmountBase:   in.Amount,
		BaseCurrency: h.BaseCurrency,
		Note:         in.Note,
		PaidAt:       in.PaidAt,
	})
}

// Get devuelve el settlement validando que pertenezca al hogar del caller.
// Esto evita que alguien acceda settlements de otros hogares por ID.
func (s *Service) Get(ctx context.Context, householdID, id uuid.UUID) (domain.SettlementPayment, error) {
	sp, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return domain.SettlementPayment{}, err
	}
	if sp.HouseholdID != householdID {
		return domain.SettlementPayment{}, domain.ErrNotFound
	}
	return sp, nil
}

// List delega al repo. householdID ya está verificado por el middleware.
// El handler pasa los filtros ya parseados.
func (s *Service) List(ctx context.Context, f ListFilter) ([]domain.SettlementPayment, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	return s.repo.List(ctx, f)
}

// Delete: cualquier miembro del hogar puede borrar (es corrección de error,
// no un flujo crítico). Futuro: restringir a creador/owner si hace ruido.
// Verifica que el settlement pertenezca al hogar antes de borrar.
func (s *Service) Delete(ctx context.Context, householdID, id uuid.UUID) error {
	sp, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if sp.HouseholdID != householdID {
		return domain.ErrNotFound
	}
	return s.repo.Delete(ctx, id)
}
