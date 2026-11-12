package notifprefs

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/LucianoR23/valuo_api/internal/auth"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/httpx"
)

type Handler struct {
	svc      *Service
	authMW   *auth.Middleware
	timezone string
	logger   *slog.Logger
}

func NewHandler(svc *Service, authMW *auth.Middleware, timezone string, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, authMW: authMW, timezone: timezone, logger: logger}
}

// Mount: GET and PUT /me/notification-preferences. Per person, so no
// household header.
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Get("/me/notification-preferences", h.Get)
		r.Put("/me/notification-preferences", h.Put)
	})
}

type kindDTO struct {
	Kind     string          `json:"kind"`
	Channels map[string]bool `json:"channels"`
}

type quietDTO struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

type reminderDTO struct {
	Enabled bool   `json:"enabled"`
	At      string `json:"at"`
}

type prefsDTO struct {
	Kinds      []kindDTO `json:"kinds"`
	QuietHours quietDTO  `json:"quietHours"`
	// DailyReminder: plan 10.2. Left out of a PUT, it keeps its value (an
	// app version from before 10.2 must not turn it off).
	DailyReminder *reminderDTO `json:"dailyReminder,omitempty"`
	// Timezone: the zone quiet hours are read in (read only).
	Timezone string `json:"timezone,omitempty"`
}

func (h *Handler) toDTO(p Prefs) prefsDTO {
	kinds := make([]kindDTO, 0, len(Catalog))
	for _, e := range Catalog {
		ch := make(map[string]bool, len(e.Channels))
		for _, c := range e.Channels {
			ch[string(c)] = p.Allows(e.Kind, c)
		}
		kinds = append(kinds, kindDTO{Kind: string(e.Kind), Channels: ch})
	}
	return prefsDTO{
		Kinds:         kinds,
		QuietHours:    quietDTO{Enabled: p.Quiet.Enabled, Start: p.Quiet.Start.String(), End: p.Quiet.End.String()},
		DailyReminder: &reminderDTO{Enabled: p.Reminder.Enabled, At: p.Reminder.At.String()},
		Timezone:      h.timezone,
	}
}

// fromDTO turns the body into Prefs. A kind or channel left out stays on;
// a missing dailyReminder keeps current's.
func fromDTO(in prefsDTO, current Reminder) (Prefs, error) {
	p := Prefs{Disabled: []string{}}
	for _, k := range in.Kinds {
		for c, on := range k.Channels {
			key := Key(Kind(k.Kind), Channel(c))
			if !validKey(key) {
				return Prefs{}, domain.NewValidationError("kinds", "tipo o canal desconocido: "+key)
			}
			if !on && !slices.Contains(p.Disabled, key) {
				p.Disabled = append(p.Disabled, key)
			}
		}
	}
	start, err := ParseTimeOfDay(in.QuietHours.Start)
	if err != nil {
		return Prefs{}, domain.NewValidationError("quietHours.start", "formato debe ser HH:MM")
	}
	end, err := ParseTimeOfDay(in.QuietHours.End)
	if err != nil {
		return Prefs{}, domain.NewValidationError("quietHours.end", "formato debe ser HH:MM")
	}
	p.Quiet = QuietHours{Enabled: in.QuietHours.Enabled, Start: start, End: end}
	p.Reminder = current
	if in.DailyReminder != nil {
		at, err := ParseTimeOfDay(in.DailyReminder.At)
		if err != nil {
			return Prefs{}, domain.NewValidationError("dailyReminder.at", "formato debe ser HH:MM")
		}
		p.Reminder = Reminder{Enabled: in.DailyReminder.Enabled, At: at}
	}
	return p, nil
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errors.New("middleware faltante"))
		return
	}
	p, err := h.svc.Get(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.toDTO(p))
}

func (h *Handler) Put(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errors.New("middleware faltante"))
		return
	}
	var in prefsDTO
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	current := Defaults().Reminder
	if in.DailyReminder == nil {
		stored, err := h.svc.Get(r.Context(), userID)
		if err != nil {
			httpx.WriteError(w, r, h.logger, err)
			return
		}
		current = stored.Reminder
	}
	p, err := fromDTO(in, current)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	saved, err := h.svc.Save(r.Context(), userID, p)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.toDTO(saved))
}
