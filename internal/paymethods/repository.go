// Package paymethods: repository y servicios del dominio de medios de pago
// (banks + payment_methods + credit_cards). Propiedad del user, no del household.
package paymethods

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// Repository agrupa el acceso a DB de banks, payment_methods y credit_cards.
// Lo combinamos en un solo repo porque las operaciones cruzan las tres tablas
// (ej: crear credit_card requiere tener el payment_method_id).
type Repository struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: sqlcgen.New(pool)}
}

// ===================== banks =====================

func (r *Repository) CreateBank(ctx context.Context, ownerID uuid.UUID, name string) (domain.Bank, error) {
	row, err := r.q.CreateBank(ctx, sqlcgen.CreateBankParams{
		OwnerUserID: ownerID,
		Name:        name,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Bank{}, fmt.Errorf("ya existe un banco con ese nombre: %w", domain.ErrConflict)
		}
		return domain.Bank{}, fmt.Errorf("paymethods.CreateBank: %w", err)
	}
	return bankToDomain(row), nil
}

// GetBankByOwnerAndName busca un banco del user por nombre exacto, sin
// filtrar por is_active. Se usa desde CreateBank para implementar
// "revive": si ya existe uno inactivo con ese nombre, reactivarlo.
// Retorna ErrNotFound si no existe ninguna fila (activa o inactiva).
func (r *Repository) GetBankByOwnerAndName(ctx context.Context, ownerID uuid.UUID, name string) (domain.Bank, error) {
	row, err := r.q.GetBankByOwnerAndName(ctx, sqlcgen.GetBankByOwnerAndNameParams{
		OwnerUserID: ownerID,
		Name:        name,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Bank{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Bank{}, fmt.Errorf("paymethods.GetBankByOwnerAndName: %w", err)
	}
	return bankToDomain(row), nil
}

// ReactivateBank marca is_active=true preservando id/created_at.
func (r *Repository) ReactivateBank(ctx context.Context, id uuid.UUID) (domain.Bank, error) {
	row, err := r.q.ReactivateBank(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Bank{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Bank{}, fmt.Errorf("paymethods.ReactivateBank: %w", err)
	}
	return bankToDomain(row), nil
}

func (r *Repository) GetBank(ctx context.Context, id uuid.UUID) (domain.Bank, error) {
	row, err := r.q.GetBankByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Bank{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Bank{}, fmt.Errorf("paymethods.GetBank: %w", err)
	}
	return bankToDomain(row), nil
}

func (r *Repository) ListBanks(ctx context.Context, ownerID uuid.UUID) ([]domain.Bank, error) {
	rows, err := r.q.ListBanksByOwner(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("paymethods.ListBanks: %w", err)
	}
	out := make([]domain.Bank, len(rows))
	for i, row := range rows {
		out[i] = bankToDomain(row)
	}
	return out, nil
}

func (r *Repository) UpdateBankName(ctx context.Context, id uuid.UUID, name string) (domain.Bank, error) {
	row, err := r.q.UpdateBankName(ctx, sqlcgen.UpdateBankNameParams{ID: id, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Bank{}, domain.ErrNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Bank{}, fmt.Errorf("ya existe un banco con ese nombre: %w", domain.ErrConflict)
		}
		return domain.Bank{}, fmt.Errorf("paymethods.UpdateBankName: %w", err)
	}
	return bankToDomain(row), nil
}

func (r *Repository) SetBankActive(ctx context.Context, id uuid.UUID, active bool) (domain.Bank, error) {
	row, err := r.q.SetBankActive(ctx, sqlcgen.SetBankActiveParams{ID: id, IsActive: active})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Bank{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Bank{}, fmt.Errorf("paymethods.SetBankActive: %w", err)
	}
	return bankToDomain(row), nil
}

// ===================== payment_methods =====================

// CreatePaymentMethod crea un método simple (sin credit_card).
// Para crear un método de kind=credit hay que usar CreateCreditMethod
// que orquesta la tx completa.
func (r *Repository) CreatePaymentMethod(ctx context.Context, p domain.PaymentMethod) (domain.PaymentMethod, error) {
	row, err := r.q.CreatePaymentMethod(ctx, sqlcgen.CreatePaymentMethodParams{
		OwnerUserID:        p.OwnerUserID,
		BankID:             p.BankID,
		Name:               p.Name,
		Kind:               string(p.Kind),
		AllowsInstallments: p.AllowsInstallments,
	})
	if err != nil {
		return domain.PaymentMethod{}, mapPaymentMethodErr(err)
	}
	return pmToDomain(row), nil
}

// CreateCreditMethod crea un payment_method kind=credit + credit_card asociada
// en una sola transacción. Si hay currentPeriod/nextPeriod, también los
// upsertea dentro de la misma tx. Si falla cualquiera, rollback.
func (r *Repository) CreateCreditMethod(
	ctx context.Context,
	pm domain.PaymentMethod,
	cc domain.CreditCard,
	currentPeriod *PeriodInput,
	nextPeriod *PeriodInput,
) (domain.PaymentMethod, domain.CreditCard, []domain.CreditCardPeriod, error) {
	var outPM domain.PaymentMethod
	var outCC domain.CreditCard
	var outPeriods []domain.CreditCardPeriod

	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		qTx := r.q.WithTx(tx)

		pmRow, err := qTx.CreatePaymentMethod(ctx, sqlcgen.CreatePaymentMethodParams{
			OwnerUserID:        pm.OwnerUserID,
			BankID:             pm.BankID,
			Name:               pm.Name,
			Kind:               string(pm.Kind),
			AllowsInstallments: pm.AllowsInstallments,
		})
		if err != nil {
			return mapPaymentMethodErr(err)
		}

		ccRow, err := qTx.CreateCreditCard(ctx, sqlcgen.CreateCreditCardParams{
			PaymentMethodID:      pmRow.ID,
			Alias:                cc.Alias,
			LastFour:             lastFourToPG(cc.LastFour),
			DefaultClosingDay:    int32(cc.DefaultClosingDay),
			DefaultDueDay:        int32(cc.DefaultDueDay),
			DebitPaymentMethodID: cc.DebitPaymentMethodID,
		})
		if err != nil {
			return fmt.Errorf("crear credit_card: %w", err)
		}

		for _, p := range []*PeriodInput{currentPeriod, nextPeriod} {
			if p == nil {
				continue
			}
			row, err := qTx.UpsertCreditCardPeriod(ctx, sqlcgen.UpsertCreditCardPeriodParams{
				CreditCardID: ccRow.ID,
				PeriodYm:     domain.PeriodYMFromDate(p.ClosingDate),
				ClosingDate:  dateToPG(p.ClosingDate),
				DueDate:      dateToPG(p.DueDate),
			})
			if err != nil {
				return fmt.Errorf("upsert credit_card_period: %w", err)
			}
			outPeriods = append(outPeriods, periodToDomain(row))
		}

		outPM = pmToDomain(pmRow)
		outCC = ccToDomain(ccRow)
		return nil
	})
	if err != nil {
		return domain.PaymentMethod{}, domain.CreditCard{}, nil, err
	}
	return outPM, outCC, outPeriods, nil
}

// dateToPG convierte time.Time → pgtype.Date (truncando tiempo).
func dateToPG(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: true}
}

// periodToDomain mapea sqlc → domain.CreditCardPeriod.
func periodToDomain(p sqlcgen.CreditCardPeriod) domain.CreditCardPeriod {
	return domain.CreditCardPeriod{
		CreditCardID: p.CreditCardID,
		PeriodYM:     p.PeriodYm,
		ClosingDate:  p.ClosingDate.Time,
		DueDate:      p.DueDate.Time,
		CreatedAt:    p.CreatedAt.Time,
		UpdatedAt:    p.UpdatedAt.Time,
	}
}

// GetPaymentMethodByOwnerAndName: equivalente a GetBankByOwnerAndName
// pero para métodos. No filtra por is_active: usada por el flow de
// revive en CreatePaymentMethod.
func (r *Repository) GetPaymentMethodByOwnerAndName(ctx context.Context, ownerID uuid.UUID, name string) (domain.PaymentMethod, error) {
	row, err := r.q.GetPaymentMethodByOwnerAndName(ctx, sqlcgen.GetPaymentMethodByOwnerAndNameParams{
		OwnerUserID: ownerID,
		Name:        name,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PaymentMethod{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.PaymentMethod{}, fmt.Errorf("paymethods.GetPaymentMethodByOwnerAndName: %w", err)
	}
	return pmToDomain(row), nil
}

// ReactivatePaymentMethod marca is_active=true y actualiza bank_id +
// allows_installments. kind es inmutable: el service valida que coincida
// antes de llamar.
func (r *Repository) ReactivatePaymentMethod(ctx context.Context, id uuid.UUID, bankID *uuid.UUID, allowsInstallments bool) (domain.PaymentMethod, error) {
	row, err := r.q.ReactivatePaymentMethod(ctx, sqlcgen.ReactivatePaymentMethodParams{
		ID:                 id,
		BankID:             bankID,
		AllowsInstallments: allowsInstallments,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PaymentMethod{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.PaymentMethod{}, mapPaymentMethodErr(err)
	}
	return pmToDomain(row), nil
}

// ReviveCreditMethod reactiva un payment_method kind=credit inactivo en
// una sola transacción: reactivate + update credit_card + upsert periods.
// El service garantiza que el id corresponde a un PM inactivo del mismo
// owner y con kind=credit, y que los periodos vienen validados.
func (r *Repository) ReviveCreditMethod(
	ctx context.Context,
	pmID uuid.UUID,
	bankID *uuid.UUID,
	allowsInstallments bool,
	cc domain.CreditCard,
	currentPeriod *PeriodInput,
	nextPeriod *PeriodInput,
) (domain.PaymentMethod, domain.CreditCard, []domain.CreditCardPeriod, error) {
	var outPM domain.PaymentMethod
	var outCC domain.CreditCard
	var outPeriods []domain.CreditCardPeriod

	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		qTx := r.q.WithTx(tx)

		pmRow, err := qTx.ReactivatePaymentMethod(ctx, sqlcgen.ReactivatePaymentMethodParams{
			ID:                 pmID,
			BankID:             bankID,
			AllowsInstallments: allowsInstallments,
		})
		if err != nil {
			return mapPaymentMethodErr(err)
		}

		// credit_card row ya existe porque el PM era kind=credit (la crea
		// la tx original). Lo obtenemos para UPDATE by id.
		existingCC, err := qTx.GetCreditCardByPaymentMethodID(ctx, pmID)
		if err != nil {
			return fmt.Errorf("revive: get credit_card: %w", err)
		}

		ccRow, err := qTx.UpdateCreditCard(ctx, sqlcgen.UpdateCreditCardParams{
			ID:                   existingCC.ID,
			Alias:                cc.Alias,
			LastFour:             lastFourToPG(cc.LastFour),
			DefaultClosingDay:    int32(cc.DefaultClosingDay),
			DefaultDueDay:        int32(cc.DefaultDueDay),
			DebitPaymentMethodID: cc.DebitPaymentMethodID,
		})
		if err != nil {
			return fmt.Errorf("revive: update credit_card: %w", err)
		}

		for _, p := range []*PeriodInput{currentPeriod, nextPeriod} {
			if p == nil {
				continue
			}
			row, err := qTx.UpsertCreditCardPeriod(ctx, sqlcgen.UpsertCreditCardPeriodParams{
				CreditCardID: ccRow.ID,
				PeriodYm:     domain.PeriodYMFromDate(p.ClosingDate),
				ClosingDate:  dateToPG(p.ClosingDate),
				DueDate:      dateToPG(p.DueDate),
			})
			if err != nil {
				return fmt.Errorf("revive: upsert credit_card_period: %w", err)
			}
			outPeriods = append(outPeriods, periodToDomain(row))
		}

		outPM = pmToDomain(pmRow)
		outCC = ccToDomain(ccRow)
		return nil
	})
	if err != nil {
		return domain.PaymentMethod{}, domain.CreditCard{}, nil, err
	}
	return outPM, outCC, outPeriods, nil
}

func (r *Repository) GetPaymentMethod(ctx context.Context, id uuid.UUID) (domain.PaymentMethod, error) {
	row, err := r.q.GetPaymentMethodByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PaymentMethod{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.PaymentMethod{}, fmt.Errorf("paymethods.GetPaymentMethod: %w", err)
	}
	return pmToDomain(row), nil
}

func (r *Repository) ListPaymentMethods(ctx context.Context, ownerID uuid.UUID) ([]domain.PaymentMethod, error) {
	rows, err := r.q.ListPaymentMethodsByOwner(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("paymethods.ListPaymentMethods: %w", err)
	}
	out := make([]domain.PaymentMethod, len(rows))
	for i, row := range rows {
		out[i] = pmToDomain(row)
	}
	return out, nil
}

// ListAllPaymentMethods incluye inactivos. Usado por la pantalla de
// configuración para mostrar los "borrados" y permitir revivirlos.
func (r *Repository) ListAllPaymentMethods(ctx context.Context, ownerID uuid.UUID) ([]domain.PaymentMethod, error) {
	rows, err := r.q.ListAllPaymentMethodsByOwner(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("paymethods.ListAllPaymentMethods: %w", err)
	}
	out := make([]domain.PaymentMethod, len(rows))
	for i, row := range rows {
		out[i] = pmToDomain(row)
	}
	return out, nil
}

func (r *Repository) UpdatePaymentMethod(ctx context.Context, p domain.PaymentMethod) (domain.PaymentMethod, error) {
	row, err := r.q.UpdatePaymentMethod(ctx, sqlcgen.UpdatePaymentMethodParams{
		ID:                 p.ID,
		Name:               p.Name,
		BankID:             p.BankID,
		AllowsInstallments: p.AllowsInstallments,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PaymentMethod{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.PaymentMethod{}, mapPaymentMethodErr(err)
	}
	return pmToDomain(row), nil
}

func (r *Repository) SetPaymentMethodActive(ctx context.Context, id uuid.UUID, active bool) (domain.PaymentMethod, error) {
	row, err := r.q.SetPaymentMethodActive(ctx, sqlcgen.SetPaymentMethodActiveParams{ID: id, IsActive: active})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PaymentMethod{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.PaymentMethod{}, fmt.Errorf("paymethods.SetPaymentMethodActive: %w", err)
	}
	return pmToDomain(row), nil
}

func (r *Repository) CountActivePaymentMethods(ctx context.Context, ownerID uuid.UUID) (int64, error) {
	n, err := r.q.CountActivePaymentMethodsByOwner(ctx, ownerID)
	if err != nil {
		return 0, fmt.Errorf("paymethods.CountActivePaymentMethods: %w", err)
	}
	return n, nil
}

// ===================== credit_cards =====================

func (r *Repository) GetCreditCardByPaymentMethod(ctx context.Context, pmID uuid.UUID) (domain.CreditCard, error) {
	row, err := r.q.GetCreditCardByPaymentMethodID(ctx, pmID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CreditCard{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.CreditCard{}, fmt.Errorf("paymethods.GetCreditCard: %w", err)
	}
	return ccToDomain(row), nil
}

func (r *Repository) ListCreditCards(ctx context.Context, ownerID uuid.UUID) ([]domain.PaymentMethodWithCard, error) {
	rows, err := r.q.ListCreditCardsByOwner(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("paymethods.ListCreditCards: %w", err)
	}
	out := make([]domain.PaymentMethodWithCard, len(rows))
	for i, row := range rows {
		pm := pmToDomain(row.PaymentMethod)
		cc := ccToDomain(row.CreditCard)
		out[i] = domain.PaymentMethodWithCard{PaymentMethod: pm, CreditCard: &cc}
	}
	return out, nil
}

func (r *Repository) UpdateCreditCard(ctx context.Context, cc domain.CreditCard) (domain.CreditCard, error) {
	row, err := r.q.UpdateCreditCard(ctx, sqlcgen.UpdateCreditCardParams{
		ID:                   cc.ID,
		Alias:                cc.Alias,
		LastFour:             lastFourToPG(cc.LastFour),
		DefaultClosingDay:    int32(cc.DefaultClosingDay),
		DefaultDueDay:        int32(cc.DefaultDueDay),
		DebitPaymentMethodID: cc.DebitPaymentMethodID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CreditCard{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.CreditCard{}, fmt.Errorf("paymethods.UpdateCreditCard: %w", err)
	}
	return ccToDomain(row), nil
}

// mapPaymentMethodErr centraliza el mapeo de errores de INSERT/UPDATE
// sobre payment_methods (unique por nombre, check de kind/installments).
func mapPaymentMethodErr(err error) error {
	if isUniqueViolation(err) {
		return fmt.Errorf("ya existe un medio de pago con ese nombre: %w", domain.ErrConflict)
	}
	if isCheckViolation(err) {
		return domain.NewValidationError("allowsInstallments", "combinación inválida con kind")
	}
	return fmt.Errorf("paymethods: %w", err)
}
