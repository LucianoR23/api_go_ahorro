package reports

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/auth"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/households"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

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

		r.Route("/reports", func(r chi.Router) {
			r.Get("/monthly", h.Monthly)
			r.Get("/trends", h.Trends)
			r.Get("/ai-export", h.AIExport)
		})
	})
}

func (h *Handler) Monthly(w http.ResponseWriter, r *http.Request) {
	householdID, err := h.hhCtx(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	month, err := parseMonth(r.URL.Query().Get("month"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	rep, err := h.svc.Monthly(r.Context(), householdID, month)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rep)
}

func (h *Handler) Trends(w http.ResponseWriter, r *http.Request) {
	householdID, err := h.hhCtx(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	months := 6
	if s := r.URL.Query().Get("months"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			months = n
		}
	}
	at := time.Now()
	if s := r.URL.Query().Get("at"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			at = t
		}
	}
	trends, err := h.svc.TrendsByMonth(r.Context(), householdID, months, at)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, trends)
}

func (h *Handler) AIExport(w http.ResponseWriter, r *http.Request) {
	householdID, err := h.hhCtx(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	month, err := parseMonth(r.URL.Query().Get("month"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	exp, err := h.svc.AIExport(r.Context(), householdID, month)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, exp)
}

// ===================== helpers =====================

func (h *Handler) hhCtx(r *http.Request) (uuid.UUID, error) {
	hh, ok := households.HouseholdIDFrom(r.Context())
	if !ok {
		return uuid.Nil, errors.New("middleware faltante")
	}
	return hh, nil
}

// parseMonth: acepta "YYYY-MM" o vacío (→ mes actual).
func parseMonth(s string) (time.Time, error) {
	if s == "" {
		return time.Now(), nil
	}
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return time.Time{}, domain.NewValidationError("month", "formato esperado YYYY-MM")
	}
	return t, nil
}

var _ = chi.URLParam // evitar warning si no se usa
