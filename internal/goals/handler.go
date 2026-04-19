package goals

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
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

		r.Route("/goals", func(r chi.Router) {
			r.Get("/", h.List)
			r.Post("/", h.Create)
			r.Get("/progress", h.ListProgress)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.Get)
				r.Patch("/", h.Update)
				r.Delete("/", h.Delete)
				r.Patch("/active", h.SetActive)
				r.Get("/progress", h.GetProgress)
			})
		})
	})
}

// ===================== DTOs =====================

type createReq struct {
	Scope        string     `json:"scope,omitempty"`
	UserID       *uuid.UUID `json:"userId,omitempty"`
	CategoryID   *uuid.UUID `json:"categoryId,omitempty"`
	GoalType     string     `json:"goalType"`
	TargetAmount float64    `json:"targetAmount"`
	Currency     string     `json:"currency,omitempty"`
	Period       string     `json:"period,omitempty"`
}

type updateReq struct {
	CategoryID   *uuid.UUID `json:"categoryId,omitempty"`
	TargetAmount float64    `json:"targetAmount"`
	Currency     string     `json:"currency,omitempty"`
	Period       string     `json:"period,omitempty"`
}

type setActiveReq struct {
	IsActive bool `json:"isActive"`
}

type goalDTO struct {
	ID           string    `json:"id"`
	HouseholdID  string    `json:"householdId"`
	Scope        string    `json:"scope"`
	UserID       *string   `json:"userId,omitempty"`
	CategoryID   *string   `json:"categoryId,omitempty"`
	GoalType     string    `json:"goalType"`
	TargetAmount float64   `json:"targetAmount"`
	Currency     string    `json:"currency"`
	Period       string    `json:"period"`
	IsActive     bool      `json:"isActive"`
	CreatedAt    time.Time `json:"createdAt"`
}

type progressDTO struct {
	Goal          goalDTO `json:"goal"`
	PeriodStart   string  `json:"periodStart"`
	PeriodEnd     string  `json:"periodEnd"`
	CurrentAmount float64 `json:"currentAmount"`
	TargetAmount  float64 `json:"targetAmount"`
	Percent       float64 `json:"percent"`
	Status        string  `json:"status"`
}

// ===================== handlers =====================

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req createReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	g, err := h.svc.Create(r.Context(), CreateInput{
		HouseholdID:  householdID,
		CreatedBy:    userID,
		Scope:        req.Scope,
		UserID:       req.UserID,
		CategoryID:   req.CategoryID,
		GoalType:     req.GoalType,
		TargetAmount: req.TargetAmount,
		Currency:     req.Currency,
		Period:       req.Period,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toDTO(g))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	f := parseListFilters(r)
	items, err := h.svc.List(r.Context(), householdID, f)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := make([]goalDTO, len(items))
	for i, g := range items {
		out[i] = toDTO(g)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	g, err := h.svc.Get(r.Context(), householdID, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(g))
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req updateReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	g, err := h.svc.Update(r.Context(), householdID, id, UpdateInput{
		CategoryID:   req.CategoryID,
		TargetAmount: req.TargetAmount,
		Currency:     req.Currency,
		Period:       req.Period,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(g))
}

func (h *Handler) SetActive(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req setActiveReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if err := h.svc.SetActive(r.Context(), householdID, id, req.IsActive); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if err := h.svc.Delete(r.Context(), householdID, id); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) GetProgress(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	at := parseAt(r)
	p, err := h.svc.Progress(r.Context(), householdID, id, at)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toProgressDTO(p))
}

func (h *Handler) ListProgress(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	f := parseListFilters(r)
	at := parseAt(r)
	items, err := h.svc.ProgressList(r.Context(), householdID, f, at)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := make([]progressDTO, len(items))
	for i, p := range items {
		out[i] = toProgressDTO(p)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ===================== helpers =====================

func (h *Handler) ctxUserAndHousehold(r *http.Request) (uuid.UUID, uuid.UUID, error) {
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

func parseUUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, domain.NewValidationError(name, "no es un UUID válido")
	}
	return id, nil
}

func parseListFilters(r *http.Request) ListFilters {
	q := r.URL.Query()
	var f ListFilters
	if s := strings.TrimSpace(q.Get("scope")); s != "" {
		f.Scope = &s
	}
	if u := strings.TrimSpace(q.Get("userId")); u != "" {
		if id, err := uuid.Parse(u); err == nil {
			f.UserID = &id
		}
	}
	if a := strings.TrimSpace(q.Get("active")); a != "" {
		b := a == "true" || a == "1"
		f.OnlyActive = &b
	}
	return f
}

func parseAt(r *http.Request) time.Time {
	if s := r.URL.Query().Get("at"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t
		}
	}
	return time.Now()
}

func toDTO(g domain.BudgetGoal) goalDTO {
	var userID, catID *string
	if g.UserID != nil {
		s := g.UserID.String()
		userID = &s
	}
	if g.CategoryID != nil {
		s := g.CategoryID.String()
		catID = &s
	}
	return goalDTO{
		ID:           g.ID.String(),
		HouseholdID:  g.HouseholdID.String(),
		Scope:        g.Scope,
		UserID:       userID,
		CategoryID:   catID,
		GoalType:     g.GoalType,
		TargetAmount: g.TargetAmount,
		Currency:     g.Currency,
		Period:       g.Period,
		IsActive:     g.IsActive,
		CreatedAt:    g.CreatedAt,
	}
}

func toProgressDTO(p domain.BudgetGoalProgress) progressDTO {
	return progressDTO{
		Goal:          toDTO(p.Goal),
		PeriodStart:   p.PeriodStart.Format("2006-01-02"),
		PeriodEnd:     p.PeriodEnd.Format("2006-01-02"),
		CurrentAmount: p.CurrentAmount,
		TargetAmount:  p.TargetAmount,
		Percent:       p.Percent,
		Status:        p.Status,
	}
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return domain.NewValidationError("body", "JSON inválido")
		}
		var unmarshal *json.UnmarshalTypeError
		if errors.As(err, &unmarshal) {
			return domain.NewValidationError(unmarshal.Field, "tipo incorrecto")
		}
		return domain.NewValidationError("body", err.Error())
	}
	return nil
}
