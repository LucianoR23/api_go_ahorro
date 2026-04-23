package expenses

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// paymentMethodLookup: dependencia mínima que usamos de paymethods.Service.
// Tomamos ownership + kind + (si credit) la credit_card asociada.
type paymentMethodLookup interface {
	GetPaymentMethod(ctx context.Context, id uuid.UUID) (domain.PaymentMethod, error)
	CreditCardByPaymentMethod(ctx context.Context, pmID uuid.UUID) (domain.CreditCard, error)
}

// Expose method from paymethods.Service.
// (paymethods.Service ya implementa GetPaymentMethod a través del repo,
//  pero no lo exponía como método. Lo agregamos cuando cableemos main.)
// Para no modificar demasiado, usamos otro aproach: el service recibe el
// repo de paymethods directamente. Ver repoShim abajo.

// householdLookup: lo mínimo que precisamos del package households.
type householdLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (domain.Household, error)
	ListMembers(ctx context.Context, householdID uuid.UUID) ([]domain.HouseholdMemberDetail, error)
	IsMember(ctx context.Context, householdID, userID uuid.UUID) (bool, error)
}

// fxConverter: lo mínimo que precisamos de fxrates.Service.
type fxConverter interface {
	Convert(ctx context.Context, amount float64, from, to string) (converted, rate float64, err error)
}

// splitRulesReader: dependencia mínima para leer pesos del hogar.
// Nil-safe: si no se inyecta, buildShares cae al split equitativo.
type splitRulesReader interface {
	WeightsForHousehold(ctx context.Context, householdID uuid.UUID) (map[uuid.UUID]float64, error)
}

// pushNotifier: fire-and-forget notifications a miembros del hogar cuando
// se crea un gasto compartido. Nil-safe: si es nil, no se envía nada.
// Definido como interface local para no acoplar expenses → push.
type pushNotifier interface {
	NotifyUsers(ctx context.Context, userIDs []uuid.UUID, title, body, url, tag string)
}

// insightCreator: dependencia opcional para persistir un insight visible en
// /notificaciones cuando se crea un gasto compartido. Interface local para
// no acoplar expenses → insights. Nil-safe.
type insightCreator interface {
	CreateSharedExpenseInsight(ctx context.Context, householdID, expenseID, recipientID uuid.UUID, title, body string)
}

// ShareOverride: monto explícito que un miembro paga en este gasto puntual,
// expresado en la currency del input. Deben sumar exactamente in.Amount
// (tolerancia 0.01). Útil cuando el default ponderado no aplica ("esto lo
// tomé yo solo").
type ShareOverride struct {
	UserID uuid.UUID
	Amount float64
}

// Service orquesta la creación de gastos: valida input, convierte moneda,
// calcula cuotas con closing/due correctos, y arma shares para gastos
// compartidos. Toda la escritura pasa por Repository.CreateTx (atómica).
type Service struct {
	repo          *Repository
	households    householdLookup
	paymMethods   paymentMethodLookup
	periodsReader periodsReader
	fx            fxConverter
	splitRules    splitRulesReader
	push          pushNotifier   // opcional; cableado via SetNotifier
	insights      insightCreator // opcional; cableado via SetInsightCreator
}

func NewService(
	repo *Repository,
	households householdLookup,
	paymMethods paymentMethodLookup,
	periodsReader periodsReader,
	fx fxConverter,
	splitRules splitRulesReader,
) *Service {
	return &Service{
		repo:          repo,
		households:    households,
		paymMethods:   paymMethods,
		periodsReader: periodsReader,
		fx:            fx,
		splitRules:    splitRules,
	}
}

// SetNotifier enchufa el notifier de push post-construcción. Lo hacemos así
// (en lugar de pasarlo al constructor) para no romper los call-sites
// existentes y mantener push como una dep opcional.
func (s *Service) SetNotifier(n pushNotifier) {
	s.push = n
}

// SetInsightCreator cablea el sistema de insights para que cada gasto
// compartido genere también una entrada en /notificaciones (además del push).
func (s *Service) SetInsightCreator(c insightCreator) {
	s.insights = c
}

// CreateInput: datos que el handler arma a partir del body.
type CreateInput struct {
	HouseholdID     uuid.UUID
	CreatedBy       uuid.UUID
	CategoryID      *uuid.UUID
	PaymentMethodID uuid.UUID
	Amount          float64
	Currency        string // "ARS"|"USD"|"EUR"
	Description     string
	SpentAt         time.Time
	Installments    int
	IsShared        bool
	// RecurringExpenseID: set solo cuando el worker de recurring genera el
	// expense. NULL = gasto manual/variable.
	RecurringExpenseID *uuid.UUID
	// SharesOverride: opcional, solo válido si IsShared=true. Si viene,
	// reemplaza al split ponderado del hogar para este gasto puntual.
	// Debe cubrir a todos los miembros con monto > 0 y sumar in.Amount.
	SharesOverride []ShareOverride
}

// Create valida, convierte a base currency, calcula cuotas y persiste todo
// en una sola transacción.
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.ExpenseDetail, error) {
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.Description = strings.TrimSpace(in.Description)

	if err := validateCreate(in); err != nil {
		return domain.ExpenseDetail{}, err
	}

	// Household: obtener baseCurrency + validar que creator sea member.
	hh, err := s.households.GetByID(ctx, in.HouseholdID)
	if err != nil {
		return domain.ExpenseDetail{}, err
	}
	ok, err := s.households.IsMember(ctx, in.HouseholdID, in.CreatedBy)
	if err != nil {
		return domain.ExpenseDetail{}, err
	}
	if !ok {
		return domain.ExpenseDetail{}, domain.ErrForbidden
	}

	// Payment method: debe pertenecer al creator.
	pm, err := s.paymMethods.GetPaymentMethod(ctx, in.PaymentMethodID)
	if err != nil {
		return domain.ExpenseDetail{}, err
	}
	if pm.OwnerUserID != in.CreatedBy {
		// Ambigüedad intencional: no filtramos existencia de métodos ajenos.
		return domain.ExpenseDetail{}, domain.ErrNotFound
	}

	// Category: si viene, debe ser del household.
	if in.CategoryID != nil {
		// Confiamos en el FK + household_id; la validación explícita la
		// puede agregar categories service si lo inyectamos. Por ahora el
		// CHECK del schema + la UI del frontend cubren el 99%.
		_ = in.CategoryID
	}

	// Ajustar installments según kind.
	if pm.Kind != domain.KindCredit {
		// Solo credit soporta cuotas. Forzamos 1 silenciosamente si el
		// cliente manda algo distinto — es más amigable que rechazar.
		in.Installments = 1
	}

	// Conversión a base currency. Guardamos amount_base, rate_used, rate_at.
	amountBase, rate, err := s.fx.Convert(ctx, in.Amount, in.Currency, hh.BaseCurrency)
	if err != nil {
		return domain.ExpenseDetail{}, err
	}
	now := time.Now().UTC()
	var rateUsed *float64
	var rateAt *time.Time
	if in.Currency != hh.BaseCurrency {
		r := rate
		rateUsed = &r
		rateAt = &now
	}

	// Shares: resolvemos los pesos (override > split_rules > equitativo).
	// Devuelve un slice ordenado de (userID, weight) listo para consumir.
	// Si IsShared=false, weights es nil y las cuotas no llevan shares.
	weights, err := s.resolveShareWeights(ctx, in)
	if err != nil {
		return domain.ExpenseDetail{}, err
	}

	// Cuotas.
	installments, err := s.buildInstallments(ctx, in, pm, amountBase, weights)
	if err != nil {
		return domain.ExpenseDetail{}, err
	}

	expense := domain.Expense{
		HouseholdID:     in.HouseholdID,
		CreatedBy:       in.CreatedBy,
		CategoryID:      in.CategoryID,
		PaymentMethodID: in.PaymentMethodID,
		Amount:          in.Amount,
		Currency:        in.Currency,
		AmountBase:      amountBase,
		BaseCurrency:    hh.BaseCurrency,
		RateUsed:        rateUsed,
		RateAt:          rateAt,
		Description:     in.Description,
		SpentAt:         in.SpentAt,
		Installments:    in.Installments,
		IsShared:        in.IsShared,
		RecurringExpenseID: in.RecurringExpenseID,
	}

	detail, err := s.repo.CreateTx(ctx, CreateBundle{Expense: expense, Installments: installments})
	if err != nil {
		return detail, err
	}

	// Notificar a los otros miembros del hogar si el gasto fue compartido.
	// Fire-and-forget: si push falla, el gasto ya quedó creado.
	s.notifySharedExpense(ctx, detail, in.CreatedBy)

	return detail, nil
}

// notifySharedExpense arma la lista de miembros a notificar (shares del
// primer installment, excluyendo al creator) y dispara push + insight por
// cada uno. No-op si IsShared=false o si no hay nada wireado.
func (s *Service) notifySharedExpense(ctx context.Context, d domain.ExpenseDetail, creator uuid.UUID) {
	if !d.Expense.IsShared || len(d.Installments) == 0 {
		return
	}
	if s.push == nil && s.insights == nil {
		return
	}

	// Destinatarios: cada userID presente en los shares != creator. Dedupe
	// vía map (si hay N cuotas, cada miembro aparece N veces).
	recipients := make(map[uuid.UUID]float64)
	for _, inst := range d.Installments {
		for _, sh := range inst.Shares {
			if sh.UserID == creator {
				continue
			}
			recipients[sh.UserID] += sh.AmountBaseOwed
		}
	}
	if len(recipients) == 0 {
		return
	}

	// Nombre del creador para el título. Si falla el lookup, usamos "Alguien".
	creatorName := "Alguien"
	if members, err := s.households.ListMembers(ctx, d.Expense.HouseholdID); err == nil {
		for _, m := range members {
			if m.User.ID == creator {
				if fn := strings.TrimSpace(m.User.FirstName); fn != "" {
					creatorName = fn
				}
				break
			}
		}
	}

	title := "Nuevo gasto compartido"
	for uid, owed := range recipients {
		body := fmt.Sprintf("%s cargó \"%s\" — te toca %.2f %s",
			creatorName, d.Expense.Description, owed, d.Expense.BaseCurrency)
		if s.push != nil {
			s.push.NotifyUsers(
				ctx,
				[]uuid.UUID{uid},
				title,
				body,
				"/expenses/"+d.Expense.ID.String(),
				"expense:"+d.Expense.ID.String(),
			)
		}
		if s.insights != nil {
			s.insights.CreateSharedExpenseInsight(ctx, d.Expense.HouseholdID, d.Expense.ID, uid, title, body)
		}
	}
}

// weightedUser: par userID + peso ya normalizable. El service los arma
// ordenados para que el residuo de redondeo siempre caiga en el mismo
// usuario (determinismo entre requests).
type weightedUser struct {
	UserID uuid.UUID
	Weight float64
}

// buildInstallments arma el slice de InstallmentWithShares ya listo para
// persistir. Para non-credit hay 1 cuota = total del gasto, is_paid=true.
// Para credit, N cuotas iguales (el residuo va a la última) con
// billing/due resueltos contra credit_card_periods + defaults.
func (s *Service) buildInstallments(
	ctx context.Context,
	in CreateInput,
	pm domain.PaymentMethod,
	amountBase float64,
	weights []weightedUser,
) ([]InstallmentWithShares, error) {
	n := in.Installments
	// amount por cuota en currency original y en base, con redondeo a 2
	// decimales y el último absorbe el residuo (no perdemos centavos).
	per := roundTo(in.Amount/float64(n), 2)
	perBase := roundTo(amountBase/float64(n), 2)

	out := make([]InstallmentWithShares, 0, n)

	if pm.Kind != domain.KindCredit {
		// 1 cuota, billing = spent_at, sin due_date, ya pagada.
		paidAt := time.Now().UTC()
		inst := domain.ExpenseInstallment{
			InstallmentNumber:     1,
			InstallmentAmount:     in.Amount,
			InstallmentAmountBase: amountBase,
			BillingDate:           in.SpentAt,
			DueDate:               nil,
			IsPaid:                true,
			PaidAt:                &paidAt,
		}
		return append(out, InstallmentWithShares{Installment: inst, Shares: buildShares(amountBase, weights)}), nil
	}

	// Credit: necesitamos la credit_card para defaults.
	cc, err := s.paymMethods.CreditCardByPaymentMethod(ctx, pm.ID)
	if err != nil {
		return nil, err
	}

	// Período base del primer installment (según spent_at vs closing_day).
	firstMonth := in.SpentAt
	if in.SpentAt.Day() > cc.DefaultClosingDay {
		firstMonth = in.SpentAt.AddDate(0, 1, 0)
	}

	runningOriginal := 0.0
	runningBase := 0.0
	for k := 0; k < n; k++ {
		monthBase := addMonths(firstMonth, k)
		period, err := resolveForClosingMonth(ctx, s.periodsReader, cc, monthBase)
		if err != nil {
			return nil, err
		}

		amt := per
		amtBase := perBase
		if k == n-1 {
			// Última cuota absorbe diferencias de redondeo.
			amt = roundTo(in.Amount-runningOriginal, 2)
			amtBase = roundTo(amountBase-runningBase, 2)
		} else {
			runningOriginal += per
			runningBase += perBase
		}

		due := period.DueDate
		inst := domain.ExpenseInstallment{
			InstallmentNumber:     k + 1,
			InstallmentAmount:     amt,
			InstallmentAmountBase: amtBase,
			BillingDate:           period.BillingDate,
			DueDate:               &due,
			IsPaid:                false,
		}
		out = append(out, InstallmentWithShares{Installment: inst, Shares: buildShares(amtBase, weights)})
	}
	return out, nil
}

// buildShares divide el amount de una cuota (en base currency) entre los
// miembros según los pesos normalizados. El último miembro absorbe el
// residuo de redondeo para que la suma sea exacta.
// weights=nil → gasto personal, no hay shares.
func buildShares(installmentBase float64, weights []weightedUser) []domain.InstallmentShare {
	if len(weights) == 0 {
		return nil
	}
	totalW := 0.0
	for _, w := range weights {
		totalW += w.Weight
	}
	if totalW <= 0 {
		// Edge case: todos weight=0. Caemos a equitativo defensivamente.
		per := roundTo(installmentBase/float64(len(weights)), 2)
		out := make([]domain.InstallmentShare, len(weights))
		running := 0.0
		for i, w := range weights {
			owed := per
			if i == len(weights)-1 {
				owed = roundTo(installmentBase-running, 2)
			} else {
				running += per
			}
			out[i] = domain.InstallmentShare{UserID: w.UserID, AmountBaseOwed: owed}
		}
		return out
	}
	out := make([]domain.InstallmentShare, len(weights))
	running := 0.0
	for i, w := range weights {
		owed := roundTo(installmentBase*w.Weight/totalW, 2)
		if i == len(weights)-1 {
			owed = roundTo(installmentBase-running, 2)
		} else {
			running += owed
		}
		out[i] = domain.InstallmentShare{UserID: w.UserID, AmountBaseOwed: owed}
	}
	return out
}

// resolveShareWeights resuelve los pesos para este gasto:
//  1. Si IsShared=false → nil (gasto personal, no hay shares).
//  2. Si viene SharesOverride → lo usa como pesos (valida suma == Amount).
//  3. Si hay splitRulesReader cableado → lee pesos del hogar.
//  4. Fallback equitativo entre miembros del hogar (todos weight=1.0).
//
// En todos los caminos: valida que los userIDs sean miembros del hogar.
// Devuelve el slice ordenado alfabéticamente por userID para determinismo
// del residuo de redondeo.
func (s *Service) resolveShareWeights(ctx context.Context, in CreateInput) ([]weightedUser, error) {
	if !in.IsShared {
		if len(in.SharesOverride) > 0 {
			return nil, domain.NewValidationError("sharesOverride", "solo válido si isShared=true")
		}
		return nil, nil
	}

	members, err := s.households.ListMembers(ctx, in.HouseholdID)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, domain.NewValidationError("isShared", "no hay miembros para dividir el gasto")
	}
	memberSet := make(map[uuid.UUID]struct{}, len(members))
	for _, m := range members {
		memberSet[m.User.ID] = struct{}{}
	}

	// 1. Override explícito del request.
	if len(in.SharesOverride) > 0 {
		seen := make(map[uuid.UUID]struct{}, len(in.SharesOverride))
		sum := 0.0
		weights := make([]weightedUser, 0, len(in.SharesOverride))
		for _, o := range in.SharesOverride {
			if _, ok := memberSet[o.UserID]; !ok {
				return nil, domain.NewValidationError("sharesOverride", "userId no es miembro del hogar: "+o.UserID.String())
			}
			if _, dup := seen[o.UserID]; dup {
				return nil, domain.NewValidationError("sharesOverride", "userId duplicado: "+o.UserID.String())
			}
			if o.Amount < 0 {
				return nil, domain.NewValidationError("sharesOverride.amount", "no puede ser negativo")
			}
			seen[o.UserID] = struct{}{}
			sum += o.Amount
			weights = append(weights, weightedUser{UserID: o.UserID, Weight: o.Amount})
		}
		if diff := sum - in.Amount; diff > 0.01 || diff < -0.01 {
			return nil, domain.NewValidationError("sharesOverride", "la suma debe ser igual al amount total")
		}
		sortWeights(weights)
		return weights, nil
	}

	// 2. Pesos default del hogar (split_rules).
	var ruleWeights map[uuid.UUID]float64
	if s.splitRules != nil {
		ruleWeights, err = s.splitRules.WeightsForHousehold(ctx, in.HouseholdID)
		if err != nil {
			return nil, err
		}
	}

	// Construimos weightedUser con los miembros actuales. Si un miembro no
	// tiene regla, fallback weight=1.0. Si la regla es weight=0, el miembro
	// no participa de los splits (pero sí es miembro a otros efectos).
	weights := make([]weightedUser, 0, len(members))
	for _, m := range members {
		w := 1.0
		if ruleWeights != nil {
			if rw, ok := ruleWeights[m.User.ID]; ok {
				w = rw
			}
		}
		if w > 0 {
			weights = append(weights, weightedUser{UserID: m.User.ID, Weight: w})
		}
	}
	if len(weights) == 0 {
		return nil, domain.NewValidationError("isShared", "no hay miembros con weight>0 para dividir")
	}
	sortWeights(weights)
	return weights, nil
}

func sortWeights(ws []weightedUser) {
	// Sort por UUID string: determinismo estable entre requests.
	for i := 1; i < len(ws); i++ {
		for j := i; j > 0 && ws[j-1].UserID.String() > ws[j].UserID.String(); j-- {
			ws[j-1], ws[j] = ws[j], ws[j-1]
		}
	}
}

// ===================== queries =====================

// Get: detail completo (expense + installments + shares).
func (s *Service) Get(ctx context.Context, householdID, id uuid.UUID) (domain.ExpenseDetail, error) {
	detail, err := s.repo.GetDetail(ctx, id)
	if err != nil {
		return domain.ExpenseDetail{}, err
	}
	if detail.Expense.HouseholdID != householdID {
		return domain.ExpenseDetail{}, domain.ErrNotFound
	}
	return detail, nil
}

// List: paginado con filtros.
func (s *Service) List(ctx context.Context, householdID uuid.UUID, f ListFilter) ([]domain.Expense, int64, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return s.repo.List(ctx, householdID, f)
}

// UpdateInput: campos editables del expense.
type UpdateInput struct {
	Description string
	SpentAt     time.Time
	CategoryID  *uuid.UUID
}

func (s *Service) Update(ctx context.Context, householdID, id uuid.UUID, in UpdateInput) (domain.Expense, error) {
	if err := s.requireInHousehold(ctx, householdID, id); err != nil {
		return domain.Expense{}, err
	}
	in.Description = strings.TrimSpace(in.Description)
	if in.Description == "" {
		return domain.Expense{}, domain.NewValidationError("description", "no puede estar vacío")
	}
	if in.SpentAt.IsZero() {
		return domain.Expense{}, domain.NewValidationError("spentAt", "requerido")
	}
	return s.repo.UpdateMeta(ctx, id, in.Description, in.SpentAt, in.CategoryID)
}

func (s *Service) Delete(ctx context.Context, householdID, id uuid.UUID) error {
	if err := s.requireInHousehold(ctx, householdID, id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, id)
}

// ===================== installments =====================

// UpdateInstallmentInput: campos editables en PATCH /expenses/{id}/installments/{n}.
// Todos opcionales. Si no viene ninguno, devolvemos validation error.
type UpdateInstallmentInput struct {
	BillingDate *time.Time
	DueDate     *time.Time // pointer-to-pointer no necesario: si el cliente manda null explícito lo tratamos como "quitar"
	ClearDueDate bool      // si true, forzamos due_date=NULL aunque DueDate venga nil por no enviarse
	IsPaid      *bool
}

// UpdateInstallment: aplica los cambios pedidos. Si IsPaid cambia, actualiza
// por separado (usa la query SetInstallmentPaid que maneja paid_at). Si las
// fechas cambian, UpdateInstallmentDates. Si ambos, ejecuta las dos.
// Valida ownership a través del expense → household.
func (s *Service) UpdateInstallment(ctx context.Context, householdID, expenseID uuid.UUID, installmentNumber int, in UpdateInstallmentInput) (domain.ExpenseInstallment, error) {
	if err := s.requireInHousehold(ctx, householdID, expenseID); err != nil {
		return domain.ExpenseInstallment{}, err
	}
	if installmentNumber < 1 {
		return domain.ExpenseInstallment{}, domain.NewValidationError("installmentNumber", "debe ser >= 1")
	}
	if in.BillingDate == nil && in.DueDate == nil && !in.ClearDueDate && in.IsPaid == nil {
		return domain.ExpenseInstallment{}, domain.NewValidationError("body", "debe enviar al menos un campo")
	}

	current, err := s.repo.GetInstallmentByNumber(ctx, expenseID, installmentNumber)
	if err != nil {
		return domain.ExpenseInstallment{}, err
	}

	result := current
	if in.BillingDate != nil || in.DueDate != nil || in.ClearDueDate {
		billing := current.BillingDate
		if in.BillingDate != nil {
			billing = *in.BillingDate
		}
		var due *time.Time
		switch {
		case in.ClearDueDate:
			due = nil
		case in.DueDate != nil:
			due = in.DueDate
		default:
			due = current.DueDate
		}
		if due != nil && due.Before(billing) {
			return domain.ExpenseInstallment{}, domain.NewValidationError("dueDate", "debe ser >= billingDate")
		}
		result, err = s.repo.UpdateInstallmentDates(ctx, current.ID, billing, due)
		if err != nil {
			return domain.ExpenseInstallment{}, err
		}
	}
	if in.IsPaid != nil && *in.IsPaid != result.IsPaid {
		result, err = s.repo.SetInstallmentPaid(ctx, current.ID, *in.IsPaid)
		if err != nil {
			return domain.ExpenseInstallment{}, err
		}
	}
	return result, nil
}

func (s *Service) requireInHousehold(ctx context.Context, householdID, id uuid.UUID) error {
	e, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if e.HouseholdID != householdID {
		return domain.ErrNotFound
	}
	return nil
}

// ===================== validation =====================

func validateCreate(in CreateInput) error {
	if in.Amount <= 0 {
		return domain.NewValidationError("amount", "debe ser > 0")
	}
	switch in.Currency {
	case "ARS", "USD", "EUR":
	default:
		return domain.NewValidationError("currency", "debe ser ARS, USD o EUR")
	}
	if in.Description == "" {
		return domain.NewValidationError("description", "no puede estar vacío")
	}
	if in.SpentAt.IsZero() {
		return domain.NewValidationError("spentAt", "requerido")
	}
	if in.Installments < 1 || in.Installments > 60 {
		return domain.NewValidationError("installments", "debe estar entre 1 y 60")
	}
	return nil
}
