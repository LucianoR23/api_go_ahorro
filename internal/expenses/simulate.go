package expenses

import (
	"context"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/money"
)

// PlannedPurchase: the installments an expense would get, without saving it
// ("¿Me lo puedo comprar?", plan 4.8).
type PlannedPurchase struct {
	PaymentMethod domain.PaymentMethod
	Amount        decimal.Decimal // rounded to cents, in Currency
	Currency      string
	AmountBase    decimal.Decimal // in BaseCurrency, today's rate
	BaseCurrency  string
	RateSource    domain.RateSource // the household's, used for AmountBase
	Installments  []domain.ExpenseInstallment
}

// Plan runs the same checks and installment math as Create (membership, own
// payment method, household category, card periods) and returns the result
// without writing anything. The purchase counts in full for its creator: no
// shares. Description is not needed.
func (s *Service) Plan(ctx context.Context, in CreateInput) (PlannedPurchase, error) {
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.Description = "simulación"
	in.IsShared = false
	in.SharesOverride = nil
	if err := validateCreate(in); err != nil {
		return PlannedPurchase{}, err
	}
	hh, err := s.households.GetByID(ctx, in.HouseholdID)
	if err != nil {
		return PlannedPurchase{}, err
	}
	ok, err := s.households.IsMember(ctx, in.HouseholdID, in.CreatedBy)
	if err != nil {
		return PlannedPurchase{}, err
	}
	if !ok {
		return PlannedPurchase{}, domain.ErrForbidden
	}
	pm, err := s.paymMethods.GetPaymentMethod(ctx, in.PaymentMethodID)
	if err != nil {
		return PlannedPurchase{}, err
	}
	if pm.OwnerUserID != in.CreatedBy {
		return PlannedPurchase{}, domain.ErrNotFound
	}
	if err := s.own.Category(ctx, in.CategoryID, in.HouseholdID); err != nil {
		return PlannedPurchase{}, err
	}
	if pm.Kind != domain.KindCredit {
		in.Installments = 1
	}
	in.Amount = in.Amount.Round(money.AmountScale)
	source, err := domain.PickRateSource(in.RateSource, hh.FxSource)
	if err != nil {
		return PlannedPurchase{}, err
	}
	amountBase, _, err := s.convert(ctx, in.Amount, in.Currency, hh.BaseCurrency, source)
	if err != nil {
		return PlannedPurchase{}, err
	}
	list, err := s.buildInstallments(ctx, in, pm, amountBase, nil)
	if err != nil {
		return PlannedPurchase{}, err
	}
	out := PlannedPurchase{
		PaymentMethod: pm, Amount: in.Amount, Currency: in.Currency,
		AmountBase: amountBase, BaseCurrency: hh.BaseCurrency, RateSource: source,
		Installments: make([]domain.ExpenseInstallment, len(list)),
	}
	for i, x := range list {
		out.Installments[i] = x.Installment
	}
	return out, nil
}
