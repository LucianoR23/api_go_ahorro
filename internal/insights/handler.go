package insights

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
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

		r.Route("/insights", func(r chi.Router) {
			r.Get("/", h.List)
			r.Get("/unread-count", h.UnreadCount)
			r.Post("/mark-all-read", h.MarkAllRead)
			r.Post("/generate", h.Generate) // manual trigger para testing
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.Get)
				r.Post("/read", h.MarkRead)
				r.Delete("/", h.Delete)
			})
		})
	})
}

type insightDTO struct {
	ID          string          `json:"id"`
	HouseholdID string          `json:"householdId"`
	UserID      *string         `json:"userId,omitempty"`
	InsightDate string          `json:"insightDate"`
	InsightType string          `json:"insightType"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	Severity    string          `json:"severity"`
	IsRead      bool            `json:"isRead"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
}

type unreadDTO struct {
	Unread int64 `json:"unread"`
}

type generateResp struct {
	Created int `json:"created"`
	Failed  int `json:"failed"`
}

// ===================== handlers =====================

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
	out := make([]insightDTO, len(items))
	for i, ins := range items {
		out[i] = toDTO(ins)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) UnreadCount(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var userFilter *uuid.UUID
	if s := r.URL.Query().Get("userId"); s != "" {
		if u, err := uuid.Parse(s); err == nil {
			userFilter = &u
		}
	}
	n, err := h.svc.CountUnread(r.Context(), householdID, userFilter)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, unreadDTO{Unread: n})
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
	ins, err := h.svc.Get(r.Context(), householdID, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(ins))
}

func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) {
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
	if err := h.svc.MarkRead(r.Context(), householdID, id); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var userFilter *uuid.UUID
	if s := r.URL.Query().Get("userId"); s != "" {
		if u, err := uuid.Parse(s); err == nil {
			userFilter = &u
		}
	}
	if err := h.svc.MarkAllRead(r.Context(), householdID, userFilter); err != nil {
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

// Generate: dispara la generación para este household sin esperar al worker.
// Útil para testing y primer uso del día.
func (h *Handler) Generate(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	at := time.Now()
	if s := r.URL.Query().Get("at"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			at = t
		}
	}
	created, failed := h.svc.GenerateForHousehold(r.Context(), householdID, at)
	httpx.WriteJSON(w, http.StatusOK, generateResp{Created: created, Failed: failed})
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
	if s := strings.TrimSpace(q.Get("userId")); s != "" {
		if id, err := uuid.Parse(s); err == nil {
			f.UserID = &id
		}
	}
	if s := strings.TrimSpace(q.Get("unread")); s != "" {
		b := s == "true" || s == "1"
		f.OnlyUnread = &b
	}
	if s := strings.TrimSpace(q.Get("from")); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			f.From = &t
		}
	}
	if s := strings.TrimSpace(q.Get("to")); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			f.To = &t
		}
	}
	if s := strings.TrimSpace(q.Get("type")); s != "" {
		f.InsightType = &s
	}
	if s := strings.TrimSpace(q.Get("limit")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 200 {
			f.Limit = int32(n)
		}
	}
	if s := strings.TrimSpace(q.Get("offset")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			f.Offset = int32(n)
		}
	}
	return f
}

func toDTO(ins domain.DailyInsight) insightDTO {
	var userID *string
	if ins.UserID != nil {
		s := ins.UserID.String()
		userID = &s
	}
	var meta json.RawMessage
	if len(ins.Metadata) > 0 {
		meta = json.RawMessage(ins.Metadata)
	}
	return insightDTO{
		ID:          ins.ID.String(),
		HouseholdID: ins.HouseholdID.String(),
		UserID:      userID,
		InsightDate: ins.InsightDate.Format("2006-01-02"),
		InsightType: ins.InsightType,
		Title:       ins.Title,
		Body:        ins.Body,
		Severity:    ins.Severity,
		IsRead:      ins.IsRead,
		Metadata:    meta,
		CreatedAt:   ins.CreatedAt,
	}
}
