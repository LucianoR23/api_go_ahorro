package balances

import (
	"context"
	"math"
	"sort"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/db"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// epsilon: balances por debajo de 1 centavo se consideran saldados.
// Evita ruido por redondeos acumulados en muchas expenses.
const epsilon = 0.005

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
	net := make(map[canonKey]float64)

	addDirectional := func(from, to uuid.UUID, amount float64) {
		if from == to {
			return
		}
		a, b := canonical(from, to)
		if from == a {
			net[canonKey{a, b}] += amount
		} else {
			net[canonKey{a, b}] -= amount
		}
	}

	// Shares billed: debtor debe billed_owed a creditor.
	for _, m := range matrix {
		addDirectional(m.Debtor, m.Creditor, m.BilledOwed)
	}
	// Settlements: from ya le pagó paid_total a to → reduce lo que from debe a to.
	for _, s := range settlements {
		addDirectional(s.From, s.To, -s.PaidTotal)
	}

	out := make([]domain.BalanceRow, 0, len(net))
	for k, v := range net {
		v = db.RoundTo(v, 2)
		if math.Abs(v) < epsilon {
			continue
		}
		if v > 0 {
			out = append(out, domain.BalanceRow{From: k.a, To: k.b, Amount: v})
		} else {
			out = append(out, domain.BalanceRow{From: k.b, To: k.a, Amount: -v})
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
	Net      float64
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
			view.Net -= b.Amount
		case b.To:
			view.OwedToMe = append(view.OwedToMe, b)
			view.Net += b.Amount
		}
	}
	view.Net = db.RoundTo(view.Net, 2)
	return view, nil
}

// PairNet: balance firmado entre from y to. Positivo → from debe a to;
// Negativo → to debe a from. Lo usa settlements.Service para validar
// que un pago no exceda la deuda actual.
func (s *Service) PairNet(ctx context.Context, householdID, from, to uuid.UUID) (float64, error) {
	if from == to {
		return 0, domain.NewValidationError("users", "from y to no pueden ser el mismo user")
	}
	raw, err := s.repo.PairBalance(ctx, householdID, from, to)
	if err != nil {
		return 0, err
	}
	// Fórmula del plan: balance = owed_by_from - owed_by_to - settled_fwd + settled_bwd
	balance := raw.OwedByFrom - raw.OwedByTo - raw.SettledFwd + raw.SettledBwd
	return db.RoundTo(balance, 2), nil
}

// canonical ordena dos UUIDs alfabéticamente. Sirve para unificar claves
// (A,B) y (B,A) en un único slot del map.
func canonical(x, y uuid.UUID) (uuid.UUID, uuid.UUID) {
	if x.String() < y.String() {
		return x, y
	}
	return y, x
}
