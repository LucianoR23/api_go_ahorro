package balances

import (
	"context"
	"github.com/LucianoR23/api_go_ahorra/internal/money"
	"github.com/shopspring/decimal"
	"sort"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// Amounts are exact decimals (plan 0.7): a pair is settled when its net is
// zero at cents.
// Evita ruido por redondeos acumulados en muchas expenses.

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// HouseholdNet: balance neto del hogar visto desde afuera. Una entrada por
// par con deuda pendiente (Amount > epsilon). El par (A,B) se reporta una
// sola vez: si A debe más a B que B a A, From=A, To=B, Amount=diferencia.
func (s *Service) HouseholdNet(ctx context.Context, householdID uuid.UUID) ([]domain.BalanceRow, error) {
	matrix, settlements, err := s.repo.HouseholdMatrix(ctx, householdID)
	if err != nil {
		return nil, err
	}

	// Key direccional "debtor→creditor" para sumar. Usamos un map con
	// pair{a,b} ordenado alfabéticamente (a.String() < b.String()) para
	// unificar ambas direcciones en un solo par canonical.
	//
	// netAtoB representa cuanto a debe a b neto:
	//   +X = a debe X a b
	//   -X = b debe X a a
	type canonKey struct{ a, b uuid.UUID }
	net := make(map[canonKey]decimal.Decimal)

	addDirectional := func(from, to uuid.UUID, amount decimal.Decimal) {
		if from == to {
			return
		}
		a, b := canonical(from, to)
		if from == a {
			net[canonKey{a, b}] = net[canonKey{a, b}].Add(amount)
		} else {
			net[canonKey{a, b}] = net[canonKey{a, b}].Sub(amount)
		}
	}

	// Shares billed: debtor debe billed_owed a creditor.
	for _, m := range matrix {
		addDirectional(m.Debtor, m.Creditor, m.BilledOwed)
	}
	// Settlements: from ya le pagó paid_total a to → reduce lo que from debe a to.
	for _, s := range settlements {
		addDirectional(s.From, s.To, s.PaidTotal.Neg())
	}

	out := make([]domain.BalanceRow, 0, len(net))
	for k, v := range net {
		v = v.Round(money.AmountScale)
		switch v.Sign() {
		case 0:
			continue
		case 1:
			out = append(out, domain.BalanceRow{From: k.a, To: k.b, Amount: v})
		default:
			out = append(out, domain.BalanceRow{From: k.b, To: k.a, Amount: v.Neg()})
		}
	}

	// Orden determinístico: por (from, to) como string. Sirve para snapshots
	// de tests y para que el frontend no salte filas entre polls.
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From.String() < out[j].From.String()
		}
		return out[i].To.String() < out[j].To.String()
	})
	return out, nil
}

// MyView: balance del user dentro del hogar. Se derivan 3 colecciones:
//   - Owe: creditores a los que el user debe plata (Amount > 0)
//   - OwedToMe: deudores que le deben al user (Amount > 0)
//   - Net: suma algebraica (positivo = el user es acreedor neto)
//
// Útil para el home del frontend ("debés $X a Luis, Pepe te debe $Y").
type MyView struct {
	UserID   uuid.UUID
	Owe      []domain.BalanceRow // from=me, to=creditor
	OwedToMe []domain.BalanceRow // from=debtor, to=me
	Net      decimal.Decimal
}

func (s *Service) MyView(ctx context.Context, householdID, userID uuid.UUID) (MyView, error) {
	net, err := s.HouseholdNet(ctx, householdID)
	if err != nil {
		return MyView{}, err
	}
	view := MyView{UserID: userID}
	for _, b := range net {
		switch userID {
		case b.From:
			view.Owe = append(view.Owe, b)
			view.Net = view.Net.Sub(b.Amount)
		case b.To:
			view.OwedToMe = append(view.OwedToMe, b)
			view.Net = view.Net.Add(b.Amount)
		}
	}
	view.Net = view.Net.Round(money.AmountScale)
	return view, nil
}

// PairNet: balance firmado entre from y to. Positivo → from debe a to;
// Negativo → to debe a from. Lo usa settlements.Service para validar
// que un pago no exceda la deuda actual.
func (s *Service) PairNet(ctx context.Context, householdID, from, to uuid.UUID) (decimal.Decimal, error) {
	if from == to {
		return decimal.Zero, domain.NewValidationError("users", "from y to no pueden ser el mismo user")
	}
	raw, err := s.repo.PairBalance(ctx, householdID, from, to)
	if err != nil {
		return decimal.Zero, err
	}
	// Fórmula del plan: balance = owed_by_from - owed_by_to - settled_fwd + settled_bwd
	balance := raw.OwedByFrom.Sub(raw.OwedByTo).Sub(raw.SettledFwd).Add(raw.SettledBwd)
	return balance.Round(money.AmountScale), nil
}

// canonical ordena dos UUIDs alfabéticamente. Sirve para unificar claves
// (A,B) y (B,A) en un único slot del map.
func canonical(x, y uuid.UUID) (uuid.UUID, uuid.UUID) {
	if x.String() < y.String() {
		return x, y
	}
	return y, x
}
