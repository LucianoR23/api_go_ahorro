package reports

import (
	"context"
	"errors"
	"fmt"
	"github.com/LucianoR23/valuo_api/internal/money"
	"github.com/shopspring/decimal"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/cpi"
	"github.com/LucianoR23/valuo_api/internal/dates"
	"github.com/LucianoR23/valuo_api/internal/domain"
)

// householdLookup: necesitamos baseCurrency para los totales.
type householdLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (domain.Household, error)
}

type Service struct {
	repo       *Repository
	households householdLookup
	cpi        cpiSource
	rates      viewRater
	logger     *slog.Logger
}

// viewRater prices the currencies a report can be shown in (plan 5.6).
type viewRater interface {
	ViewRate(ctx context.Context, currency string, source domain.RateSource) (decimal.Decimal, error)
}

// cpiSource serves INDEC's index (plan 5.4).
type cpiSource interface {
	Series(ctx context.Context) (cpi.Series, error)
}

func NewService(repo *Repository, households householdLookup, cpiSrc cpiSource, rates viewRater, logger *slog.Logger) *Service {
	return &Service{repo: repo, households: households, cpi: cpiSrc, rates: rates, logger: logger}
}

// realSeries is the index for a report in pesos: a space in another base
// currency, or a report in dollars, gets none (ok false).
func (s *Service) realSeries(ctx context.Context, hh domain.Household, cur Currency) (cpi.Series, bool, error) {
	if hh.BaseCurrency != "ARS" || cur.Currency != "ARS" {
		return cpi.Series{}, false, nil
	}
	series, err := s.cpi.Series(ctx)
	if err != nil {
		return cpi.Series{}, false, err
	}
	return series, !series.Empty(), nil
}

// ===================== monthly =====================

type CategoryBreakdownItem struct {
	CategoryID   *uuid.UUID      `json:"categoryId,omitempty"`
	CategoryName string          `json:"categoryName"`
	Total        decimal.Decimal `json:"total"`
	Pct          float64         `json:"pct"` // % sobre spent_this_month
	TxCount      int64           `json:"txCount"`
}

type FixedVariableItem struct {
	FixedTotal    decimal.Decimal `json:"fixedTotal"`
	VariableTotal decimal.Decimal `json:"variableTotal"`
	FixedPct      float64         `json:"fixedPct"`
	VariablePct   float64         `json:"variablePct"`
	FixedCount    int64           `json:"fixedCount"`
	VariableCount int64           `json:"variableCount"`
}

// PayerItem: what one member paid in the month (Inicio · Hogar, plan 1.10b).
type PayerItem struct {
	UserID uuid.UUID       `json:"userId"`
	Total  decimal.Decimal `json:"total"`
}

// Currency: what a report's amounts are in (plan 5.3, 5.6).
type Currency struct {
	// Currency: ARS, or the currency asked for (domain.ViewCurrencies).
	Currency string `json:"currency"`
	// RateSource: the dollar used, or "cripto" for a crypto; empty in pesos.
	RateSource string `json:"rateSource,omitempty"`
	// RateMode: "day" (each movement's day) or "today"; empty in pesos. Only
	// the dollar has "day".
	RateMode string `json:"rateMode,omitempty"`
	// USDRate: today's price of that dollar; only in dollars.
	USDRate *decimal.Decimal `json:"usdRate,omitempty"`
	// Rate: today's pesos per unit of Currency; empty in pesos.
	Rate *decimal.Decimal `json:"rate,omitempty"`
	// Scale: the decimals amounts keep (8 for bitcoin).
	Scale int32 `json:"scale"`
}

// ViewRequest: which currency the caller wants a report in.
type ViewRequest struct {
	// Currency: empty or ARS for pesos, else a domain.ViewCurrencies code.
	Currency string
	Today    bool // the dollar at today's rate instead of each movement's day
	// Real: trends in today's pesos, adjusted by INDEC's index (plan 5.4).
	Real bool
}

// inPesos reports whether the request keeps pesos.
func (r ViewRequest) inPesos() bool { return r.Currency == "" || r.Currency == "ARS" }

// errNoRate: there is no quote for the currency asked for yet.
var errNoRate = domain.NewConflictError("Todavía no tenemos la cotización de esa moneda. Probá en un rato.")

// view resolves a request into the query view and what the response says.
// A space kept in another currency is shown in it.
func (s *Service) view(ctx context.Context, hh domain.Household, req ViewRequest) (View, Currency, error) {
	pesos := Currency{Currency: hh.BaseCurrency, Scale: money.AmountScale}
	if req.inPesos() || req.Currency == hh.BaseCurrency {
		return View{}, pesos, nil
	}
	vc, ok := domain.LookupViewCurrency(req.Currency)
	if !ok {
		return View{}, Currency{}, domain.NewValidationError("currency", "moneda desconocida")
	}
	if hh.BaseCurrency != "ARS" {
		return View{}, Currency{}, domain.NewValidationError("currency", "solo se puede ver en otra moneda un espacio en pesos")
	}
	src, err := domain.PickRateSource("", hh.FxSource)
	if err != nil {
		return View{}, Currency{}, err
	}
	rate, err := s.rates.ViewRate(ctx, vc.Code, src)
	if errors.Is(err, domain.ErrNotFound) {
		return View{}, Currency{}, errNoRate
	}
	if err != nil {
		return View{}, Currency{}, err
	}
	rate = rate.Round(4)
	if rate.Sign() <= 0 {
		return View{}, Currency{}, errNoRate
	}
	cur := Currency{Currency: vc.Code, RateSource: string(src), RateMode: "today", Rate: &rate, Scale: vc.Scale}
	if vc.Crypto {
		cur.RateSource = string(domain.RateSourceCripto)
	}
	v := View{Currency: vc.Code, Rate: &rate}
	if vc.Code == "USD" {
		// The dollar keeps both modes (plan 5.3): day by day through SQL, or
		// at today's dollar.
		cur.USDRate = &rate
		v = View{Currency: "USD", Source: &src}
		if req.Today {
			today := dates.Today()
			v.RateDay = &today
		} else {
			cur.RateMode = "day"
		}
	}
	return v, cur, nil
}

type MonthlyReport struct {
	HouseholdID  uuid.UUID `json:"householdId"`
	BaseCurrency string    `json:"baseCurrency"`
	Currency
	Month         string                  `json:"month"` // YYYY-MM
	From          string                  `json:"from"`  // YYYY-MM-DD
	To            string                  `json:"to"`
	Spent         decimal.Decimal         `json:"spentThisMonth"`
	Billed        decimal.Decimal         `json:"billedThisMonth"`
	Due           decimal.Decimal         `json:"dueThisMonth"`
	ByCategory    []CategoryBreakdownItem `json:"byCategory"`
	FixedVariable FixedVariableItem       `json:"fixedVariable"`
	ByPayer       []PayerItem             `json:"byPayer"`
	// Income received in the month (plan 5.3).
	Income decimal.Decimal `json:"incomeThisMonth"`
	// SpentReal: what was spent, in today's pesos (plan 5.4). Only in pesos,
	// for a month older than the last published index, when the index
	// changed since.
	SpentReal *decimal.Decimal `json:"spentRealThisMonth,omitempty"`
	// CPIThrough: the last published month of the index (YYYY-MM), with
	// SpentReal.
	CPIThrough string `json:"cpiThrough,omitempty"`
}

// Monthly calcula el reporte mensual completo para un household.
// month es "YYYY-MM" en horario local; el rango es [primer día, último día].
func (s *Service) Monthly(ctx context.Context, householdID uuid.UUID, month time.Time, req ViewRequest) (MonthlyReport, error) {
	hh, err := s.households.GetByID(ctx, householdID)
	if err != nil {
		return MonthlyReport{}, err
	}
	v, cur, err := s.view(ctx, hh, req)
	if err != nil {
		return MonthlyReport{}, err
	}
	from, to := monthRange(month)

	spent, err := s.repo.SumSpentAt(ctx, householdID, from, to, v)
	if err != nil {
		return MonthlyReport{}, err
	}
	billed, err := s.repo.SumBilled(ctx, householdID, from, to, v)
	if err != nil {
		return MonthlyReport{}, err
	}
	due, err := s.repo.SumDue(ctx, householdID, from, to, v)
	if err != nil {
		return MonthlyReport{}, err
	}
	cats, err := s.repo.CategoryBreakdown(ctx, householdID, from, to, v)
	if err != nil {
		return MonthlyReport{}, err
	}
	fv, err := s.repo.FixedVariable(ctx, householdID, from, to, v)
	if err != nil {
		return MonthlyReport{}, err
	}
	payers, err := s.repo.ByPayer(ctx, householdID, from, to, v)
	if err != nil {
		return MonthlyReport{}, err
	}
	income, err := s.repo.SumIncomes(ctx, householdID, from, to, v)
	if err != nil {
		return MonthlyReport{}, err
	}
	byPayer := make([]PayerItem, len(payers))
	for i, p := range payers {
		byPayer[i] = PayerItem{UserID: p.UserID, Total: p.Total.Round(cur.Scale)}
	}

	byCat := make([]CategoryBreakdownItem, len(cats))
	for i, c := range cats {
		name := c.CategoryName
		if name == "" {
			name = "Sin categoría"
		}
		pct := pctOf(c.Total, spent)
		byCat[i] = CategoryBreakdownItem{
			CategoryID:   c.CategoryID,
			CategoryName: name,
			Total:        c.Total.Round(cur.Scale),
			Pct:          pct,
			TxCount:      c.TxCount,
		}
	}

	fvItem := FixedVariableItem{
		FixedTotal:    fv.FixedTotal.Round(cur.Scale),
		VariableTotal: fv.VariableTotal.Round(cur.Scale),
		FixedCount:    fv.FixedCount,
		VariableCount: fv.VariableCount,
	}
	total := fv.FixedTotal.Add(fv.VariableTotal)
	fvItem.FixedPct = pctOf(fv.FixedTotal, total)
	fvItem.VariablePct = pctOf(fv.VariableTotal, total)

	rep := MonthlyReport{
		HouseholdID:   householdID,
		BaseCurrency:  hh.BaseCurrency,
		Currency:      cur,
		Month:         month.Format("2006-01"),
		From:          from.Format("2006-01-02"),
		To:            to.Format("2006-01-02"),
		Spent:         spent.Round(cur.Scale),
		Billed:        billed.Round(cur.Scale),
		Due:           due.Round(cur.Scale),
		ByCategory:    byCat,
		FixedVariable: fvItem,
		ByPayer:       byPayer,
		Income:        income.Round(cur.Scale),
	}
	series, ok, err := s.realSeries(ctx, hh, cur)
	if err != nil {
		return MonthlyReport{}, err
	}
	if f := series.Factor(month); ok && !f.Equal(decimal.NewFromInt(1)) {
		spentReal := spent.Mul(f).Round(cur.Scale)
		rep.SpentReal = &spentReal
		rep.CPIThrough = series.Through().Format("2006-01")
	}
	return rep, nil
}

// ===================== trends =====================

type TrendsPoint struct {
	Month      string          `json:"month"` // YYYY-MM
	SpentTotal decimal.Decimal `json:"spentTotal"`
	DueTotal   decimal.Decimal `json:"dueTotal"`
	Income     decimal.Decimal `json:"income"`
	Net        decimal.Decimal `json:"net"` // income - dueTotal
}

type Trends struct {
	HouseholdID  uuid.UUID `json:"householdId"`
	BaseCurrency string    `json:"baseCurrency"`
	Currency
	// Adjust: "cpi" when the amounts are in today's pesos (plan 5.4).
	Adjust string `json:"adjust,omitempty"`
	// CPIThrough: the last published month of the index (YYYY-MM), with
	// Adjust. Later months count as that one.
	CPIThrough string        `json:"cpiThrough,omitempty"`
	Months     int           `json:"months"`
	Points     []TrendsPoint `json:"points"`
}

// TrendsByMonth: últimos N meses (incluyendo el actual) de gasto/due/income.
func (s *Service) TrendsByMonth(ctx context.Context, householdID uuid.UUID, months int, at time.Time, req ViewRequest) (Trends, error) {
	if months < 1 {
		months = 6
	}
	if months > 24 {
		months = 24
	}
	hh, err := s.households.GetByID(ctx, householdID)
	if err != nil {
		return Trends{}, err
	}
	v, cur, err := s.view(ctx, hh, req)
	if err != nil {
		return Trends{}, err
	}
	var series cpi.Series
	if req.Real {
		if !req.inPesos() {
			return Trends{}, domain.NewValidationError("adjust", "el ajuste por inflación es solo en pesos")
		}
		if hh.BaseCurrency != "ARS" {
			return Trends{}, domain.NewValidationError("adjust", "el ajuste por inflación es solo para espacios en pesos")
		}
		var ok bool
		series, ok, err = s.realSeries(ctx, hh, cur)
		if err != nil {
			return Trends{}, err
		}
		if !ok {
			return Trends{}, domain.NewConflictError("Todavía no tenemos el índice de inflación. Probá en un rato.")
		}
	}

	// Rango: primer día del mes (at - months + 1) hasta último día del mes de at.
	end := lastOfMonth(at)
	start := firstOfMonth(at.AddDate(0, -(months - 1), 0))

	spentRows, err := s.repo.SpentByMonth(ctx, householdID, start, end, v)
	if err != nil {
		return Trends{}, err
	}
	dueRows, err := s.repo.DueByMonth(ctx, householdID, start, end, v)
	if err != nil {
		return Trends{}, err
	}
	incomeRows, err := s.repo.IncomesByMonth(ctx, householdID, start, end, v)
	if err != nil {
		return Trends{}, err
	}

	spentMap := indexByMonth(spentRows)
	dueMap := indexByMonth(dueRows)
	incomeMap := indexByMonth(incomeRows)

	points := make([]TrendsPoint, 0, months)
	cursor := firstOfMonth(start)
	for k := 0; k < months; k++ {
		key := cursor.Format("2006-01")
		spent := spentMap[key]
		due := dueMap[key]
		income := incomeMap[key]
		if req.Real {
			f := series.Factor(cursor)
			spent, due, income = spent.Mul(f), due.Mul(f), income.Mul(f)
		}
		points = append(points, TrendsPoint{
			Month:      key,
			SpentTotal: spent.Round(cur.Scale),
			DueTotal:   due.Round(cur.Scale),
			Income:     income.Round(cur.Scale),
			Net:        income.Sub(due).Round(cur.Scale),
		})
		cursor = cursor.AddDate(0, 1, 0)
	}

	tr := Trends{
		HouseholdID:  householdID,
		BaseCurrency: hh.BaseCurrency,
		Currency:     cur,
		Months:       months,
		Points:       points,
	}
	if req.Real {
		tr.Adjust = "cpi"
		tr.CPIThrough = series.Through().Format("2006-01")
	}
	return tr, nil
}

// ===================== AI export =====================

// AIExport: estructura compacta optimizada para darle contexto a un LLM.
// No incluye IDs redundantes — solo los números que importan para que el
// modelo haga análisis y recomendaciones.
type AIExport struct {
	HouseholdName string           `json:"householdName"`
	BaseCurrency  string           `json:"baseCurrency"`
	Month         string           `json:"month"`
	Spent         decimal.Decimal  `json:"spent"`
	Billed        decimal.Decimal  `json:"billed"`
	Due           decimal.Decimal  `json:"due"`
	FixedTotal    decimal.Decimal  `json:"fixedTotal"`
	VariableTotal decimal.Decimal  `json:"variableTotal"`
	FixedPct      float64          `json:"fixedPct"`
	TopCategories []AICategoryLine `json:"topCategories"`
	TrendsLast6   []TrendsPoint    `json:"trendsLast6"`
	Prompt        string           `json:"prompt"` // pre-armado listo para pegar en Claude
}

type AICategoryLine struct {
	Name    string          `json:"name"`
	Total   decimal.Decimal `json:"total"`
	Pct     float64         `json:"pct"`
	TxCount int64           `json:"txCount"`
}

func (s *Service) AIExport(ctx context.Context, householdID uuid.UUID, month time.Time) (AIExport, error) {
	rep, err := s.Monthly(ctx, householdID, month, ViewRequest{})
	if err != nil {
		return AIExport{}, err
	}
	trends, err := s.TrendsByMonth(ctx, householdID, 6, month, ViewRequest{})
	if err != nil {
		return AIExport{}, err
	}
	hh, err := s.households.GetByID(ctx, householdID)
	if err != nil {
		return AIExport{}, err
	}

	top := rep.ByCategory
	if len(top) > 10 {
		top = top[:10]
	}
	lines := make([]AICategoryLine, len(top))
	for i, c := range top {
		lines[i] = AICategoryLine{Name: c.CategoryName, Total: c.Total, Pct: c.Pct, TxCount: c.TxCount}
	}

	prompt := buildPrompt(hh.Name, rep, trends.Points)

	return AIExport{
		HouseholdName: hh.Name,
		BaseCurrency:  rep.BaseCurrency,
		Month:         rep.Month,
		Spent:         rep.Spent,
		Billed:        rep.Billed,
		Due:           rep.Due,
		FixedTotal:    rep.FixedVariable.FixedTotal,
		VariableTotal: rep.FixedVariable.VariableTotal,
		FixedPct:      rep.FixedVariable.FixedPct,
		TopCategories: lines,
		TrendsLast6:   trends.Points,
		Prompt:        prompt,
	}, nil
}

func buildPrompt(hhName string, rep MonthlyReport, trends []TrendsPoint) string {
	cur := rep.BaseCurrency
	p := fmt.Sprintf(
		"Analizá los gastos del hogar %q correspondientes al mes %s (moneda base %s).\n\n"+
			"Resumen del mes:\n"+
			"- Gastado (spent_at): %s %s\n"+
			"- Resumido en tarjetas (billing_date): %s %s\n"+
			"- A pagar (due_date): %s %s\n"+
			"- Fijos (recurrentes): %s %s (%.1f%%)\n"+
			"- Variables (manuales): %s %s (%.1f%%)\n\n"+
			"Top categorías:\n",
		hhName, rep.Month, cur,
		rep.Spent.StringFixed(2), cur,
		rep.Billed.StringFixed(2), cur,
		rep.Due.StringFixed(2), cur,
		rep.FixedVariable.FixedTotal.StringFixed(2), cur, rep.FixedVariable.FixedPct,
		rep.FixedVariable.VariableTotal.StringFixed(2), cur, rep.FixedVariable.VariablePct,
	)
	for i, c := range rep.ByCategory {
		if i >= 10 {
			break
		}
		p += fmt.Sprintf("- %s: %s %s (%.1f%%, %d tx)\n", c.CategoryName, c.Total.StringFixed(2), cur, c.Pct, c.TxCount)
	}
	p += "\nTrends últimos 6 meses (gasto / a pagar / ingreso / neto):\n"
	for _, t := range trends {
		p += fmt.Sprintf("- %s: %s / %s / %s / %s\n", t.Month, t.SpentTotal.StringFixed(2), t.DueTotal.StringFixed(2), t.Income.StringFixed(2), t.Net.StringFixed(2))
	}
	p += "\nDame: 1) diagnóstico breve, 2) 3 recomendaciones accionables priorizadas, 3) categorías con mayor potencial de ahorro."
	return p
}

// ===================== helpers =====================

func monthRange(t time.Time) (time.Time, time.Time) {
	y, m, _ := t.Date()
	loc := t.Location()
	if loc == nil {
		loc = time.UTC
	}
	first := time.Date(y, m, 1, 0, 0, 0, 0, loc)
	last := first.AddDate(0, 1, -1)
	return first, last
}

func firstOfMonth(t time.Time) time.Time {
	y, m, _ := t.Date()
	loc := t.Location()
	if loc == nil {
		loc = time.UTC
	}
	return time.Date(y, m, 1, 0, 0, 0, 0, loc)
}

func lastOfMonth(t time.Time) time.Time {
	return firstOfMonth(t).AddDate(0, 1, -1)
}

func indexByMonth(rows []MonthRow) map[string]decimal.Decimal {
	out := make(map[string]decimal.Decimal, len(rows))
	for _, r := range rows {
		out[r.Month.Format("2006-01")] = r.Total
	}
	return out
}

// pctOf: part as a percentage of total, rounded to 2 decimals; 0 when total
// is not positive. Computed in decimal, returned as float64 (percentages are
// not money).
func pctOf(part, total decimal.Decimal) float64 {
	if total.Sign() <= 0 {
		return 0
	}
	return part.Div(total).Mul(decimal.NewFromInt(100)).Round(2).InexactFloat64()
}
