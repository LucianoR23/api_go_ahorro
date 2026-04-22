package paymethods

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// Service concentra las reglas de negocio de bancos/medios de pago/tarjetas.
// Autorización: todo lo que se accede debe pertenecer al caller (owner_user_id).
// El handler pasa el caller; el service valida ownership antes de operar.
type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// ===================== banks =====================

func (s *Service) CreateBank(ctx context.Context, ownerID uuid.UUID, name string) (domain.Bank, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.Bank{}, domain.NewValidationError("name", "no puede estar vacío")
	}

	// "Revive": si ya existe un banco inactivo del mismo owner con ese
	// nombre, lo reactivamos en vez de fallar con conflicto. Preserva el
	// id y el historial de payment_methods que apuntan a él. Si el
	// existente está activo, caemos al INSERT que chocará contra el
	// índice parcial y el repo lo mapea a ErrConflict.
	existing, err := s.repo.GetBankByOwnerAndName(ctx, ownerID, name)
	if err == nil && !existing.IsActive {
		return s.repo.ReactivateBank(ctx, existing.ID)
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.Bank{}, err
	}
	return s.repo.CreateBank(ctx, ownerID, name)
}

func (s *Service) ListBanks(ctx context.Context, ownerID uuid.UUID) ([]domain.Bank, error) {
	return s.repo.ListBanks(ctx, ownerID)
}

func (s *Service) UpdateBankName(ctx context.Context, ownerID, bankID uuid.UUID, name string) (domain.Bank, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.Bank{}, domain.NewValidationError("name", "no puede estar vacío")
	}
	if err := s.requireBankOwner(ctx, ownerID, bankID); err != nil {
		return domain.Bank{}, err
	}
	return s.repo.UpdateBankName(ctx, bankID, name)
}

func (s *Service) SetBankActive(ctx context.Context, ownerID, bankID uuid.UUID, active bool) (domain.Bank, error) {
	if err := s.requireBankOwner(ctx, ownerID, bankID); err != nil {
		return domain.Bank{}, err
	}
	return s.repo.SetBankActive(ctx, bankID, active)
}

// ===================== payment_methods =====================

// CreatePaymentMethodInput agrupa los parámetros de creación.
// Si Kind=credit, CreditCard es obligatoria; para los demás kinds se ignora.
type CreatePaymentMethodInput struct {
	OwnerID            uuid.UUID
	BankID             *uuid.UUID
	Name               string
	Kind               domain.PaymentMethodKind
	AllowsInstallments *bool // solo relevante para wallet; en otros kinds se fuerza
	CreditCard         *CreateCreditCardInput
}

// CreateCreditCardInput: sub-estructura con los datos específicos de la tarjeta.
// CurrentPeriod es opcional: si viene, se persiste un credit_card_periods
// con period_ym derivado del closing_date dentro de la misma tx que el INSERT
// de payment_method + credit_card. NextPeriod idem, también opcional.
type CreateCreditCardInput struct {
	Alias                string
	LastFour             *string
	DefaultClosingDay    int
	DefaultDueDay        int
	DebitPaymentMethodID *uuid.UUID
	CurrentPeriod        *PeriodInput
	NextPeriod           *PeriodInput
}

// PeriodInput: cierre y vencimiento concretos para un mes de una tarjeta.
// period_ym se deriva del ClosingDate ("YYYY-MM").
type PeriodInput struct {
	ClosingDate time.Time
	DueDate     time.Time
}

// CreatePaymentMethod valida y crea un payment_method, incluyendo tarjeta
// si kind=credit (en una sola transacción).
func (s *Service) CreatePaymentMethod(ctx context.Context, in CreatePaymentMethodInput) (domain.PaymentMethodWithCard, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return domain.PaymentMethodWithCard{}, domain.NewValidationError("name", "no puede estar vacío")
	}
	if !in.Kind.IsValid() {
		return domain.PaymentMethodWithCard{}, domain.NewValidationError("kind", "valor inválido")
	}

	// Resolver allows_installments según kind.
	forced, isForced := in.Kind.AllowsInstallmentsForced()
	var allows bool
	switch {
	case isForced:
		allows = forced // ignoramos lo que venga en input
	case in.AllowsInstallments != nil:
		allows = *in.AllowsInstallments
	default:
		allows = false // wallet sin especificar → false por defecto
	}

	// Si hay bank_id, validar que pertenezca al user.
	if in.BankID != nil {
		if err := s.requireBankOwner(ctx, in.OwnerID, *in.BankID); err != nil {
			return domain.PaymentMethodWithCard{}, err
		}
	}

	pm := domain.PaymentMethod{
		OwnerUserID:        in.OwnerID,
		BankID:             in.BankID,
		Name:               in.Name,
		Kind:               in.Kind,
		AllowsInstallments: allows,
	}

	// Detectar match por (owner, name) para implementar "revive". Si
	// hay una fila inactiva del mismo kind, la reactivamos; si hay una
	// activa, dejamos que el INSERT dispare el conflict; si hay
	// inactiva de otro kind, rechazamos con un error claro para evitar
	// corromper historial (expenses linkean al id y asumen el kind).
	existing, existErr := s.repo.GetPaymentMethodByOwnerAndName(ctx, in.OwnerID, in.Name)
	if existErr != nil && !errors.Is(existErr, domain.ErrNotFound) {
		return domain.PaymentMethodWithCard{}, existErr
	}
	reviving := existErr == nil && !existing.IsActive
	if reviving && existing.Kind != in.Kind {
		return domain.PaymentMethodWithCard{}, domain.NewValidationError(
			"name",
			fmt.Sprintf("ya existe un medio de pago inactivo con ese nombre de otro tipo (%s); reactivalo desde la lista o elegí otro nombre", existing.Kind),
		)
	}

	// Rama credit: transacción combinada.
	if in.Kind == domain.KindCredit {
		if in.CreditCard == nil {
			return domain.PaymentMethodWithCard{}, domain.NewValidationError("creditCard", "requerido para kind=credit")
		}
		if err := validateCardInput(*in.CreditCard); err != nil {
			return domain.PaymentMethodWithCard{}, err
		}
		// Validar debit_payment_method_id: mismo owner, no la propia tarjeta.
		// No la propia tarjeta: imposible acá (todavía no existe), lo validamos
		// en Update si el cliente cambia el debit a la tarjeta misma.
		if in.CreditCard.DebitPaymentMethodID != nil {
			if err := s.requirePaymentMethodOwner(ctx, in.OwnerID, *in.CreditCard.DebitPaymentMethodID); err != nil {
				return domain.PaymentMethodWithCard{}, err
			}
		}

		cc := domain.CreditCard{
			Alias:                strings.TrimSpace(in.CreditCard.Alias),
			LastFour:             in.CreditCard.LastFour,
			DefaultClosingDay:    in.CreditCard.DefaultClosingDay,
			DefaultDueDay:        in.CreditCard.DefaultDueDay,
			DebitPaymentMethodID: in.CreditCard.DebitPaymentMethodID,
		}

		// Validar periodos opcionales antes de abrir tx.
		if in.CreditCard.CurrentPeriod != nil {
			if err := validatePeriodInput("currentPeriod", *in.CreditCard.CurrentPeriod); err != nil {
				return domain.PaymentMethodWithCard{}, err
			}
		}
		if in.CreditCard.NextPeriod != nil {
			if err := validatePeriodInput("nextPeriod", *in.CreditCard.NextPeriod); err != nil {
				return domain.PaymentMethodWithCard{}, err
			}
		}

		if reviving {
			outPM, outCC, outPeriods, err := s.repo.ReviveCreditMethod(ctx, existing.ID, in.BankID, allows, cc, in.CreditCard.CurrentPeriod, in.CreditCard.NextPeriod)
			if err != nil {
				return domain.PaymentMethodWithCard{}, err
			}
			return domain.PaymentMethodWithCard{PaymentMethod: outPM, CreditCard: &outCC, Periods: outPeriods}, nil
		}
		outPM, outCC, outPeriods, err := s.repo.CreateCreditMethod(ctx, pm, cc, in.CreditCard.CurrentPeriod, in.CreditCard.NextPeriod)
		if err != nil {
			return domain.PaymentMethodWithCard{}, err
		}
		return domain.PaymentMethodWithCard{PaymentMethod: outPM, CreditCard: &outCC, Periods: outPeriods}, nil
	}

	// Rama no-credit: rechazar credit_card si viene (input inconsistente).
	if in.CreditCard != nil {
		return domain.PaymentMethodWithCard{}, domain.NewValidationError("creditCard", "solo permitido con kind=credit")
	}

	if reviving {
		outPM, err := s.repo.ReactivatePaymentMethod(ctx, existing.ID, in.BankID, allows)
		if err != nil {
			return domain.PaymentMethodWithCard{}, err
		}
		return domain.PaymentMethodWithCard{PaymentMethod: outPM}, nil
	}

	outPM, err := s.repo.CreatePaymentMethod(ctx, pm)
	if err != nil {
		return domain.PaymentMethodWithCard{}, err
	}
	return domain.PaymentMethodWithCard{PaymentMethod: outPM}, nil
}

func (s *Service) ListPaymentMethods(ctx context.Context, ownerID uuid.UUID) ([]domain.PaymentMethod, error) {
	return s.repo.ListPaymentMethods(ctx, ownerID)
}

// ListAllPaymentMethods: incluye inactivos. Se expone en la pantalla de
// configuración; los demás callers (expenses, incomes, recurring) siguen
// usando ListPaymentMethods para no ofrecer métodos "borrados" al cargar
// movimientos.
func (s *Service) ListAllPaymentMethods(ctx context.Context, ownerID uuid.UUID) ([]domain.PaymentMethod, error) {
	return s.repo.ListAllPaymentMethods(ctx, ownerID)
}

// UpdatePaymentMethod: cambia name/bank/allowsInstallments. kind no.
// Para kind=wallet allowsInstallments es libre; para el resto, si el cliente
// manda un valor distinto del forzado, devolvemos error de validación.
func (s *Service) UpdatePaymentMethod(
	ctx context.Context,
	ownerID, id uuid.UUID,
	name string,
	bankID *uuid.UUID,
	allowsInstallments *bool,
) (domain.PaymentMethod, error) {
	current, err := s.repo.GetPaymentMethod(ctx, id)
	if err != nil {
		return domain.PaymentMethod{}, err
	}
	if current.OwnerUserID != ownerID {
		return domain.PaymentMethod{}, domain.ErrNotFound
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return domain.PaymentMethod{}, domain.NewValidationError("name", "no puede estar vacío")
	}

	if bankID != nil {
		if err := s.requireBankOwner(ctx, ownerID, *bankID); err != nil {
			return domain.PaymentMethod{}, err
		}
	}

	// Resolver allows para el update.
	forced, isForced := current.Kind.AllowsInstallmentsForced()
	allows := current.AllowsInstallments
	if isForced {
		// valor forzado: si el cliente trata de cambiarlo, rechazamos.
		if allowsInstallments != nil && *allowsInstallments != forced {
			return domain.PaymentMethod{}, domain.NewValidationError("allowsInstallments", "no configurable para este kind")
		}
		allows = forced
	} else if allowsInstallments != nil {
		allows = *allowsInstallments
	}

	updated := domain.PaymentMethod{
		ID:                 id,
		Name:               name,
		BankID:             bankID,
		AllowsInstallments: allows,
	}
	return s.repo.UpdatePaymentMethod(ctx, updated)
}

// SetPaymentMethodActive: desactivar/reactivar. Protección: no dejar al user
// sin ningún método activo. Si va a desactivar el último, falla con
// validación clara.
func (s *Service) SetPaymentMethodActive(ctx context.Context, ownerID, id uuid.UUID, active bool) (domain.PaymentMethod, error) {
	if err := s.requirePaymentMethodOwner(ctx, ownerID, id); err != nil {
		return domain.PaymentMethod{}, err
	}
	if !active {
		count, err := s.repo.CountActivePaymentMethods(ctx, ownerID)
		if err != nil {
			return domain.PaymentMethod{}, err
		}
		if count <= 1 {
			return domain.PaymentMethod{}, domain.NewValidationError("isActive", "no podés desactivar tu último medio de pago")
		}
	}
	return s.repo.SetPaymentMethodActive(ctx, id, active)
}

// ===================== credit_cards =====================

func (s *Service) GetCreditCard(ctx context.Context, ownerID, pmID uuid.UUID) (domain.CreditCard, error) {
	if err := s.requirePaymentMethodOwner(ctx, ownerID, pmID); err != nil {
		return domain.CreditCard{}, err
	}
	return s.repo.GetCreditCardByPaymentMethod(ctx, pmID)
}

func (s *Service) ListCreditCards(ctx context.Context, ownerID uuid.UUID) ([]domain.PaymentMethodWithCard, error) {
	return s.repo.ListCreditCards(ctx, ownerID)
}

// UpdateCreditCardInput: campos editables de una tarjeta.
type UpdateCreditCardInput struct {
	Alias                string
	LastFour             *string
	DefaultClosingDay    int
	DefaultDueDay        int
	DebitPaymentMethodID *uuid.UUID
}

func (s *Service) UpdateCreditCard(ctx context.Context, ownerID, pmID uuid.UUID, in UpdateCreditCardInput) (domain.CreditCard, error) {
	if err := s.requirePaymentMethodOwner(ctx, ownerID, pmID); err != nil {
		return domain.CreditCard{}, err
	}
	existing, err := s.repo.GetCreditCardByPaymentMethod(ctx, pmID)
	if err != nil {
		return domain.CreditCard{}, err
	}

	if err := validateCardInput(CreateCreditCardInput{
		Alias:             in.Alias,
		LastFour:          in.LastFour,
		DefaultClosingDay: in.DefaultClosingDay,
		DefaultDueDay:     in.DefaultDueDay,
	}); err != nil {
		return domain.CreditCard{}, err
	}

	if in.DebitPaymentMethodID != nil {
		if *in.DebitPaymentMethodID == pmID {
			return domain.CreditCard{}, domain.NewValidationError("debitPaymentMethodId", "la tarjeta no puede debitarse de sí misma")
		}
		if err := s.requirePaymentMethodOwner(ctx, ownerID, *in.DebitPaymentMethodID); err != nil {
			return domain.CreditCard{}, err
		}
	}

	updated := domain.CreditCard{
		ID:                   existing.ID,
		Alias:                strings.TrimSpace(in.Alias),
		LastFour:             in.LastFour,
		DefaultClosingDay:    in.DefaultClosingDay,
		DefaultDueDay:        in.DefaultDueDay,
		DebitPaymentMethodID: in.DebitPaymentMethodID,
	}
	return s.repo.UpdateCreditCard(ctx, updated)
}

// ===================== bootstrap (usado por auth.Register) =====================

// CreateEfectivoFor crea el payment_method default "Efectivo" para un user
// recién registrado. Expuesto como método separado para que auth.Service lo
// llame durante Register. No es transaccional con el insert del user —
// si falla, el user queda sin Efectivo y puede crearlo manualmente.
func (s *Service) CreateEfectivoFor(ctx context.Context, userID uuid.UUID) (domain.PaymentMethod, error) {
	return s.repo.CreatePaymentMethod(ctx, domain.PaymentMethod{
		OwnerUserID:        userID,
		Name:               "Efectivo",
		Kind:               domain.KindCash,
		AllowsInstallments: false,
	})
}

// ===================== helpers internos =====================

func (s *Service) requireBankOwner(ctx context.Context, ownerID, bankID uuid.UUID) error {
	b, err := s.repo.GetBank(ctx, bankID)
	if err != nil {
		return err
	}
	if b.OwnerUserID != ownerID {
		// Ambigüedad intencional: devolvemos NotFound en vez de Forbidden
		// para no filtrar existencia de recursos ajenos.
		return domain.ErrNotFound
	}
	return nil
}

// RequireCreditCardOwner valida ownership y que el pm sea una tarjeta de
// crédito. Devuelve el credit_card_id real para poder operar contra
// credit_card_periods. Uso: creditperiods handler/service.
func (s *Service) RequireCreditCardOwner(ctx context.Context, ownerID, pmID uuid.UUID) (uuid.UUID, error) {
	if err := s.requirePaymentMethodOwner(ctx, ownerID, pmID); err != nil {
		return uuid.Nil, err
	}
	cc, err := s.repo.GetCreditCardByPaymentMethod(ctx, pmID)
	if err != nil {
		return uuid.Nil, err
	}
	return cc.ID, nil
}

// CreditCardByPaymentMethod: exposed getter (delega al repo). Útil para
// el service de creditperiods, que necesita los default_closing_day /
// default_due_day para calcular status/fallback.
func (s *Service) CreditCardByPaymentMethod(ctx context.Context, pmID uuid.UUID) (domain.CreditCard, error) {
	return s.repo.GetCreditCardByPaymentMethod(ctx, pmID)
}

// GetPaymentMethod: exposed getter para que expenses.Service valide kind
// + ownership antes de crear un gasto.
func (s *Service) GetPaymentMethod(ctx context.Context, pmID uuid.UUID) (domain.PaymentMethod, error) {
	return s.repo.GetPaymentMethod(ctx, pmID)
}

func (s *Service) requirePaymentMethodOwner(ctx context.Context, ownerID, pmID uuid.UUID) error {
	pm, err := s.repo.GetPaymentMethod(ctx, pmID)
	if err != nil {
		return err
	}
	if pm.OwnerUserID != ownerID {
		return domain.ErrNotFound
	}
	return nil
}

// validatePeriodInput: closing/due válidos y due >= closing.
func validatePeriodInput(field string, p PeriodInput) error {
	if p.ClosingDate.IsZero() {
		return domain.NewValidationError(field+".closingDate", "requerido")
	}
	if p.DueDate.IsZero() {
		return domain.NewValidationError(field+".dueDate", "requerido")
	}
	if p.DueDate.Before(p.ClosingDate) {
		return domain.NewValidationError(field+".dueDate", "debe ser >= closingDate")
	}
	return nil
}

// validateCardInput: checks comunes para create y update de credit_card.
func validateCardInput(in CreateCreditCardInput) error {
	if strings.TrimSpace(in.Alias) == "" {
		return domain.NewValidationError("alias", "no puede estar vacío")
	}
	if in.DefaultClosingDay < 1 || in.DefaultClosingDay > 31 {
		return domain.NewValidationError("defaultClosingDay", "debe estar entre 1 y 31")
	}
	if in.DefaultDueDay < 1 || in.DefaultDueDay > 31 {
		return domain.NewValidationError("defaultDueDay", "debe estar entre 1 y 31")
	}
	if in.LastFour != nil && *in.LastFour != "" {
		lf := *in.LastFour
		if len(lf) != 4 {
			return domain.NewValidationError("lastFour", "debe tener 4 dígitos")
		}
		for _, c := range lf {
			if c < '0' || c > '9' {
				return domain.NewValidationError("lastFour", "solo dígitos")
			}
		}
	}
	return nil
}
