package exports

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/auth"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/households"
	"github.com/LucianoR23/valuo_api/internal/httpx"
	"github.com/LucianoR23/valuo_api/internal/xlsx"
)

// ContentType is the media type of an .xlsx file.
const ContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

type Handler struct {
	svc    *Service
	logger *slog.Logger
	authMW *auth.Middleware
	hhMW   *households.Middleware
}

func NewHandler(svc *Service, authMW *auth.Middleware, hhMW *households.Middleware, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger, authMW: authMW, hhMW: hhMW}
}

func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Use(h.hhMW.RequireHouseholdMember)

		r.Get("/exports/expenses.xlsx", h.Expenses)
		r.Get("/exports/incomes.xlsx", h.Incomes)
	})
}

// Expenses: GET /exports/expenses.xlsx?from&to (required) plus the filters
// of GET /expenses: categoryId, paymentMethodId, kind=fixed|variable, q.
func (h *Handler) Expenses(w http.ResponseWriter, r *http.Request) {
	userID, householdID, err := ctxIDs(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	f, err := parseExpenseFilter(r.URL.Query())
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	sheet, err := h.svc.Expenses(r.Context(), householdID, userID, f)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	h.send(w, r, sheet, f.Filename("gastos"))
}

// Incomes: GET /exports/incomes.xlsx?from&to (required).
func (h *Handler) Incomes(w http.ResponseWriter, r *http.Request) {
	userID, householdID, err := ctxIDs(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	p, err := parsePeriod(r.URL.Query())
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	sheet, err := h.svc.Incomes(r.Context(), householdID, userID, p)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	h.send(w, r, sheet, p.Filename("ingresos"))
}

// send builds the whole file before writing, so a failure is still a clean
// error response instead of a truncated download.
func (h *Handler) send(w http.ResponseWriter, r *http.Request, sheet xlsx.Sheet, filename string) {
	var buf bytes.Buffer
	if err := xlsx.Write(&buf, sheet); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.Header().Set("Content-Type", ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := buf.WriteTo(w); err != nil {
		h.logger.WarnContext(r.Context(), "exports: write file", "error", err)
	}
}

func ctxIDs(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		return uuid.Nil, uuid.Nil, domain.ErrUnauthorized
	}
	householdID, ok := households.HouseholdIDFrom(r.Context())
	if !ok {
		return uuid.Nil, uuid.Nil, errors.New("middleware faltante")
	}
	return userID, householdID, nil
}

func parseDate(q url.Values, name string) (time.Time, error) {
	v := q.Get(name)
	if v == "" {
		return time.Time{}, domain.NewValidationError(name, "es obligatorio (YYYY-MM-DD)")
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return time.Time{}, domain.NewValidationError(name, "formato debe ser YYYY-MM-DD")
	}
	return t, nil
}

func parsePeriod(q url.Values) (Period, error) {
	from, err := parseDate(q, "from")
	if err != nil {
		return Period{}, err
	}
	to, err := parseDate(q, "to")
	if err != nil {
		return Period{}, err
	}
	p := Period{From: from, To: to}
	return p, p.Validate()
}

func parseUUID(q url.Values, name string) (*uuid.UUID, error) {
	v := q.Get(name)
	if v == "" {
		return nil, nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil, domain.NewValidationError(name, "no es un UUID válido")
	}
	return &id, nil
}

func parseExpenseFilter(q url.Values) (ExpenseFilter, error) {
	p, err := parsePeriod(q)
	if err != nil {
		return ExpenseFilter{}, err
	}
	f := ExpenseFilter{Period: p}
	if f.CategoryID, err = parseUUID(q, "categoryId"); err != nil {
		return f, err
	}
	if f.PaymentMethodID, err = parseUUID(q, "paymentMethodId"); err != nil {
		return f, err
	}
	switch v := q.Get("kind"); v {
	case "", "fixed", "variable":
		f.Kind = v
	default:
		return f, domain.NewValidationError("kind", "debe ser fixed o variable")
	}
	if v := strings.TrimSpace(q.Get("q")); v != "" {
		if r := []rune(v); len(r) > 100 {
			v = string(r[:100])
		}
		f.Q = v
	}
	return f, nil
}
