package statements

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/dates"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/money"
)

// cards: what the service needs from paymethods (ownership + the card).
type cards interface {
	RequireCreditCardOwner(ctx context.Context, ownerID, pmID uuid.UUID) (uuid.UUID, error)
	CreditCardByPaymentMethod(ctx context.Context, pmID uuid.UUID) (domain.CreditCard, error)
	ListCreditCards(ctx context.Context, ownerID uuid.UUID) ([]domain.PaymentMethodWithCard, error)
}

type Service struct {
	repo  *Repository
	cards cards
	now   func() time.Time
}

func NewService(repo *Repository, cards cards) *Service {
	return &Service{repo: repo, cards: cards, now: time.Now}
}

// SetClock replaces time.Now (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// MaxRangeMonths bounds a statement listing.
const MaxRangeMonths = 36

func (s *Service) card(ctx context.Context, ownerID, pmID uuid.UUID) (domain.CreditCard, error) {
	if _, err := s.cards.RequireCreditCardOwner(ctx, ownerID, pmID); err != nil {
		return domain.CreditCard{}, err
	}
	return s.cards.CreditCardByPaymentMethod(ctx, pmID)
}

func ym(t time.Time) string { return domain.PeriodYMFromDate(t) }

// monthRange: first and last day of the months from..to (billing dates).
func monthRange(from, to time.Time) (time.Time, time.Time) {
	return from, time.Date(to.Year(), to.Month()+1, 0, 0, 0, 0, 0, time.UTC)
}

// calendar resolves period dates for a card from its loaded overrides.
type calendar struct {
	cc        domain.CreditCard
	overrides map[string]domain.CreditCardPeriod
}

func (c calendar) dates(month time.Time) (closing, due time.Time) {
	var o *domain.CreditCardPeriod
	if p, ok := c.overrides[ym(month)]; ok {
		o = &p
	}
	return domain.CardPeriodDates(c.cc, month, o)
}

func (s *Service) calendar(ctx context.Context, cc domain.CreditCard, from, to time.Time) (calendar, error) {
	o, err := s.repo.PeriodOverrides(ctx, cc.ID, ym(from), ym(to))
	if err != nil {
		return calendar{}, err
	}
	return calendar{cc: cc, overrides: o}, nil
}

// openMonth: the closing month of the period open today.
func (s *Service) openMonth(ctx context.Context, cc domain.CreditCard, today time.Time) (time.Time, error) {
	month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	cal, err := s.calendar(ctx, cc, month, month)
	if err != nil {
		return time.Time{}, err
	}
	return domain.OpenPeriodMonth(today, func(m time.Time) time.Time { c, _ := cal.dates(m); return c }), nil
}

// ListResult: OpenPeriodYM is the period open today; the one before it is
// the statement to pay.
type ListResult struct {
	OpenPeriodYM string
	Statements   []domain.CardStatement
}

// List returns the statements between fromYM and toYM (inclusive) that have
// installments, adjustments or a record, plus always the open period and the
// one before it. Empty bounds default to 6 months back and 11 ahead of the
// open period, enough for "Comprometido" (4.2).
func (s *Service) List(ctx context.Context, ownerID, pmID uuid.UUID, fromYM, toYM string) (ListResult, error) {
	cc, err := s.card(ctx, ownerID, pmID)
	if err != nil {
		return ListResult{}, err
	}
	today := dates.DateOf(s.now())
	open, err := s.openMonth(ctx, cc, today)
	if err != nil {
		return ListResult{}, err
	}
	from, to := open.AddDate(0, -6, 0), open.AddDate(0, 11, 0)
	if fromYM != "" {
		if from, err = domain.ParsePeriodYM(fromYM); err != nil {
			return ListResult{}, domain.NewValidationError("from", "formato debe ser YYYY-MM")
		}
	}
	if toYM != "" {
		if to, err = domain.ParsePeriodYM(toYM); err != nil {
			return ListResult{}, domain.NewValidationError("to", "formato debe ser YYYY-MM")
		}
	}
	if to.Before(from) {
		return ListResult{}, domain.NewValidationError("to", "debe ser igual o posterior a from")
	}
	if months(from, to) > MaxRangeMonths {
		return ListResult{}, domain.NewValidationError("to", "el rango no puede superar 36 meses")
	}

	statements, err := s.build(ctx, cc, from, to, today, map[string]bool{ym(open): true, ym(open.AddDate(0, -1, 0)): true})
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{OpenPeriodYM: ym(open), Statements: statements}, nil
}

func months(from, to time.Time) int {
	return (to.Year()-from.Year())*12 + int(to.Month()) - int(from.Month()) + 1
}

// build computes the statements of from..to. A month is included when it
// has activity or is listed in always (when inside the range).
func (s *Service) build(ctx context.Context, cc domain.CreditCard, from, to, today time.Time, always map[string]bool) ([]domain.CardStatement, error) {
	fromYM, toYM := ym(from), ym(to)
	cal, err := s.calendar(ctx, cc, from, to)
	if err != nil {
		return nil, err
	}
	firstDay, lastDay := monthRange(from, to)
	inst, err := s.repo.InstallmentSums(ctx, cc.PaymentMethodID, firstDay, lastDay)
	if err != nil {
		return nil, err
	}
	adj, err := s.repo.AdjustmentSums(ctx, cc.ID, fromYM, toYM)
	if err != nil {
		return nil, err
	}
	records, err := s.repo.Records(ctx, cc.ID, fromYM, toYM)
	if err != nil {
		return nil, err
	}

	totals := map[string]map[string]decimal.Decimal{}
	counts := map[string]int{}
	add := func(p periodSum) {
		if totals[p.PeriodYM] == nil {
			totals[p.PeriodYM] = map[string]decimal.Decimal{}
		}
		totals[p.PeriodYM][p.Currency] = totals[p.PeriodYM][p.Currency].Add(p.Total)
		counts[p.PeriodYM] += p.Count
	}
	for _, p := range inst {
		add(p)
	}
	for _, p := range adj {
		add(p)
	}

	var out []domain.CardStatement
	for m := from; !m.After(to); m = m.AddDate(0, 1, 0) {
		key := ym(m)
		rec, hasRecord := records[key]
		if totals[key] == nil && !hasRecord && !always[key] {
			continue
		}
		closing, due := cal.dates(m)
		out = append(out, domain.CardStatement{
			PeriodYM:         key,
			ClosingDate:      closing,
			DueDate:          due,
			Status:           domain.StatementStatusOn(closing, due, rec.PaidAt, today),
			Totals:           sortedTotals(totals[key]),
			InstallmentCount: counts[key],
			StatementRecord:  rec,
		})
	}
	return out, nil
}

var currencyOrder = map[string]int{"ARS": 0, "USD": 1, "EUR": 2}

func sortedTotals(m map[string]decimal.Decimal) []domain.CurrencyAmount {
	out := make([]domain.CurrencyAmount, 0, len(m))
	for cur, amt := range m {
		out = append(out, domain.CurrencyAmount{Currency: cur, Amount: amt})
	}
	sort.Slice(out, func(i, j int) bool { return currencyOrder[out[i].Currency] < currencyOrder[out[j].Currency] })
	return out
}

// MaxCommittedMonths bounds "Comprometido" (plan 4.2: 1 to 12 months).
const MaxCommittedMonths = 12

// Committed sums, for every active card of the user, the statements from the
// card's open period on, over months months starting at the earliest open
// period. Each card also carries its closed statement still to pay.
func (s *Service) Committed(ctx context.Context, ownerID uuid.UUID, months int) (domain.Committed, error) {
	if months < 1 || months > MaxCommittedMonths {
		return domain.Committed{}, domain.NewValidationError("months", "debe estar entre 1 y 12")
	}
	cards, err := s.cards.ListCreditCards(ctx, ownerID)
	if err != nil {
		return domain.Committed{}, err
	}
	today := dates.DateOf(s.now())
	opens := make([]time.Time, len(cards))
	var start time.Time
	for i, c := range cards {
		if opens[i], err = s.openMonth(ctx, *c.CreditCard, today); err != nil {
			return domain.Committed{}, err
		}
		if i == 0 || opens[i].Before(start) {
			start = opens[i]
		}
	}
	if len(cards) == 0 {
		start = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	end := start.AddDate(0, months-1, 0)

	all := map[string]map[string]decimal.Decimal{}
	out := domain.Committed{Cards: make([]domain.CardCommitted, len(cards))}
	for i, c := range cards {
		open, prev := opens[i], opens[i].AddDate(0, -1, 0)
		list, err := s.build(ctx, *c.CreditCard, prev, end, today, map[string]bool{ym(prev): true})
		if err != nil {
			return domain.Committed{}, err
		}
		byYM := make(map[string]domain.CardStatement, len(list))
		for _, st := range list {
			byYM[st.PeriodYM] = st
		}
		card := domain.CardCommitted{Card: c, OpenPeriodYM: ym(open)}
		if st, ok := byYM[ym(prev)]; ok && st.Status != domain.StatementPaid && len(st.Totals) > 0 {
			card.ToPay = &st
		}
		for m := start; !m.After(end); m = m.AddDate(0, 1, 0) {
			pt := domain.PeriodTotals{PeriodYM: ym(m), Totals: []domain.CurrencyAmount{}}
			if !m.Before(open) {
				if st, ok := byYM[ym(m)]; ok {
					pt.Totals = st.Totals
				}
			}
			for _, t := range pt.Totals {
				if all[pt.PeriodYM] == nil {
					all[pt.PeriodYM] = map[string]decimal.Decimal{}
				}
				all[pt.PeriodYM][t.Currency] = all[pt.PeriodYM][t.Currency].Add(t.Amount)
			}
			card.Months = append(card.Months, pt)
		}
		out.Cards[i] = card
	}
	for m := start; !m.After(end); m = m.AddDate(0, 1, 0) {
		out.Months = append(out.Months, domain.PeriodTotals{PeriodYM: ym(m), Totals: sortedTotals(all[ym(m)])})
	}
	return out, nil
}

// InstallmentPlans: the card's expenses in installments still being billed,
// from its open period on ("Cuotas en curso", plan 4.2).
func (s *Service) InstallmentPlans(ctx context.Context, ownerID, pmID uuid.UUID) ([]domain.InstallmentPlan, error) {
	cc, err := s.card(ctx, ownerID, pmID)
	if err != nil {
		return nil, err
	}
	open, err := s.openMonth(ctx, cc, dates.DateOf(s.now()))
	if err != nil {
		return nil, err
	}
	return s.repo.InstallmentPlans(ctx, cc.PaymentMethodID, open)
}

// Reminders: the fixed card reminders of today for every active card of the
// user (plan 4.3): the open period closes tomorrow, or a statement with
// something to pay is due in CardReminderDueDays days and is not paid.
// today is a calendar day (midnight UTC).
func (s *Service) Reminders(ctx context.Context, ownerID uuid.UUID, today time.Time) ([]domain.CardReminder, error) {
	cards, err := s.cards.ListCreditCards(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	tomorrow, dueDay := today.AddDate(0, 0, 1), today.AddDate(0, 0, domain.CardReminderDueDays)
	var out []domain.CardReminder
	for _, c := range cards {
		cc := *c.CreditCard
		open, err := s.openMonth(ctx, cc, today)
		if err != nil {
			return nil, err
		}
		prev := open.AddDate(0, -1, 0)
		list, err := s.build(ctx, cc, prev, open, today, map[string]bool{ym(prev): true, ym(open): true})
		if err != nil {
			return nil, err
		}
		for _, st := range list {
			base := domain.CardReminder{PaymentMethodID: c.ID, CardName: c.Name, PeriodYM: st.PeriodYM}
			if st.PeriodYM == ym(open) && st.ClosingDate.Equal(tomorrow) {
				r := base
				r.Kind, r.Date = domain.CardReminderClosing, st.ClosingDate
				out = append(out, r)
			}
			if st.DueDate.Equal(dueDay) && st.Status != domain.StatementPaid && len(st.Totals) > 0 {
				r := base
				r.Kind, r.Date, r.Totals = domain.CardReminderDue, st.DueDate, st.Totals
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// Detail: one statement with its installments and adjustments.
func (s *Service) Detail(ctx context.Context, ownerID, pmID uuid.UUID, periodYM string) (domain.CardStatementDetail, error) {
	cc, err := s.card(ctx, ownerID, pmID)
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	return s.detail(ctx, cc, periodYM)
}

func (s *Service) detail(ctx context.Context, cc domain.CreditCard, periodYM string) (domain.CardStatementDetail, error) {
	month, err := domain.ParsePeriodYM(periodYM)
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	today := dates.DateOf(s.now())
	list, err := s.build(ctx, cc, month, month, today, map[string]bool{periodYM: true})
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	firstDay, lastDay := monthRange(month, month)
	inst, err := s.repo.Installments(ctx, cc.PaymentMethodID, firstDay, lastDay)
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	adj, err := s.repo.Adjustments(ctx, cc.ID, periodYM)
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	return domain.CardStatementDetail{CardStatement: list[0], Installments: inst, Adjustments: adj}, nil
}

func validAmount(field string, d decimal.Decimal, allowZero bool) (decimal.Decimal, error) {
	if !money.InRange(d) {
		return d, domain.NewValidationError(field, "monto fuera de rango")
	}
	d = d.Round(money.AmountScale)
	if d.Sign() < 0 || (!allowZero && d.Sign() == 0) {
		if allowZero {
			return d, domain.NewValidationError(field, "debe ser >= 0")
		}
		return d, domain.NewValidationError(field, "debe ser > 0")
	}
	return d, nil
}

func validCurrency(field, c string) (string, error) {
	switch c {
	case "ARS", "USD", "EUR":
		return c, nil
	}
	return c, domain.NewValidationError(field, "moneda no soportada")
}

// SetMinimum sets or clears (nil) the minimum payment, in pesos.
func (s *Service) SetMinimum(ctx context.Context, ownerID, pmID uuid.UUID, periodYM string, minimum *decimal.Decimal) (domain.CardStatementDetail, error) {
	cc, err := s.card(ctx, ownerID, pmID)
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	if _, err := domain.ParsePeriodYM(periodYM); err != nil {
		return domain.CardStatementDetail{}, err
	}
	if minimum != nil {
		m, err := validAmount("minimumPayment", *minimum, true)
		if err != nil {
			return domain.CardStatementDetail{}, err
		}
		minimum = &m
	}
	if err := s.repo.SetMinimum(ctx, cc.ID, periodYM, minimum); err != nil {
		return domain.CardStatementDetail{}, err
	}
	return s.detail(ctx, cc, periodYM)
}

// PayInput: Amount nil pays the total; Currency empty means pesos, or the
// statement's only currency.
type PayInput struct {
	Amount   *decimal.Decimal
	Currency string
}

// Pay records the payment of a closed statement and marks all its
// installments paid. A partial amount is recorded as is; the rest is not
// carried to the next statement (that is 4.7).
func (s *Service) Pay(ctx context.Context, ownerID, pmID uuid.UUID, periodYM string, in PayInput) (domain.CardStatementDetail, error) {
	cc, err := s.card(ctx, ownerID, pmID)
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	st, err := s.detail(ctx, cc, periodYM)
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	if st.Status == domain.StatementOpen {
		return domain.CardStatementDetail{}, domain.NewValidationError("periodYm", "el resumen todavía no cerró")
	}
	cur := in.Currency
	if cur == "" {
		cur = "ARS"
		if len(st.Totals) == 1 {
			cur = st.Totals[0].Currency
		}
	}
	if cur, err = validCurrency("currency", cur); err != nil {
		return domain.CardStatementDetail{}, err
	}
	var amount decimal.Decimal
	if in.Amount != nil {
		if amount, err = validAmount("amount", *in.Amount, false); err != nil {
			return domain.CardStatementDetail{}, err
		}
	} else {
		for _, t := range st.Totals {
			if t.Currency == cur {
				amount = t.Amount
			}
		}
		if amount.Sign() <= 0 {
			return domain.CardStatementDetail{}, domain.NewValidationError("amount", "indicá cuánto pagaste")
		}
	}
	month, _ := domain.ParsePeriodYM(periodYM)
	firstDay, lastDay := monthRange(month, month)
	if err := s.repo.SetPayment(ctx, cc, periodYM, firstDay, lastDay, &payment{Amount: amount, Currency: cur, At: s.now()}); err != nil {
		return domain.CardStatementDetail{}, err
	}
	return s.detail(ctx, cc, periodYM)
}

// Unpay clears the payment and marks the statement's installments unpaid.
func (s *Service) Unpay(ctx context.Context, ownerID, pmID uuid.UUID, periodYM string) (domain.CardStatementDetail, error) {
	cc, err := s.card(ctx, ownerID, pmID)
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	month, err := domain.ParsePeriodYM(periodYM)
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	firstDay, lastDay := monthRange(month, month)
	if err := s.repo.SetPayment(ctx, cc, periodYM, firstDay, lastDay, nil); err != nil {
		return domain.CardStatementDetail{}, err
	}
	return s.detail(ctx, cc, periodYM)
}

// AdjustmentInput: Currency empty means pesos.
type AdjustmentInput struct {
	Kind        domain.AdjustmentKind
	Description string
	Amount      decimal.Decimal
	Currency    string
}

func cleanAdjustment(in AdjustmentInput) (adjustmentFields, error) {
	if !in.Kind.Valid() {
		return adjustmentFields{}, domain.NewValidationError("kind", "debe ser interest, tax, fee, credit u other")
	}
	desc := strings.TrimSpace(in.Description)
	if desc == "" {
		return adjustmentFields{}, domain.NewValidationError("description", "requerido")
	}
	if utf8.RuneCountInString(desc) > 80 {
		return adjustmentFields{}, domain.NewValidationError("description", "máximo 80 caracteres")
	}
	amount, err := validAmount("amount", in.Amount, false)
	if err != nil {
		return adjustmentFields{}, err
	}
	cur := in.Currency
	if cur == "" {
		cur = "ARS"
	}
	if cur, err = validCurrency("currency", cur); err != nil {
		return adjustmentFields{}, err
	}
	return adjustmentFields{Kind: in.Kind, Description: desc, Amount: amount, Currency: cur}, nil
}

func (s *Service) CreateAdjustment(ctx context.Context, ownerID, pmID uuid.UUID, periodYM string, in AdjustmentInput) (domain.CardStatementDetail, error) {
	cc, err := s.card(ctx, ownerID, pmID)
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	if _, err := domain.ParsePeriodYM(periodYM); err != nil {
		return domain.CardStatementDetail{}, err
	}
	f, err := cleanAdjustment(in)
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	if _, err := s.repo.CreateAdjustment(ctx, cc.ID, periodYM, f); err != nil {
		return domain.CardStatementDetail{}, err
	}
	return s.detail(ctx, cc, periodYM)
}

func (s *Service) UpdateAdjustment(ctx context.Context, ownerID, pmID uuid.UUID, periodYM string, id uuid.UUID, in AdjustmentInput) (domain.CardStatementDetail, error) {
	cc, err := s.card(ctx, ownerID, pmID)
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	f, err := cleanAdjustment(in)
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	if _, err := s.repo.UpdateAdjustment(ctx, cc.ID, periodYM, id, f); err != nil {
		return domain.CardStatementDetail{}, err
	}
	return s.detail(ctx, cc, periodYM)
}

func (s *Service) DeleteAdjustment(ctx context.Context, ownerID, pmID uuid.UUID, periodYM string, id uuid.UUID) (domain.CardStatementDetail, error) {
	cc, err := s.card(ctx, ownerID, pmID)
	if err != nil {
		return domain.CardStatementDetail{}, err
	}
	if err := s.repo.DeleteAdjustment(ctx, cc.ID, periodYM, id); err != nil {
		return domain.CardStatementDetail{}, err
	}
	return s.detail(ctx, cc, periodYM)
}
