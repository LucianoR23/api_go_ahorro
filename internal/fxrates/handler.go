package fxrates

import (
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LucianoR23/valuo_api/internal/auth"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/httpx"
)

type Handler struct {
	svc    *Service
	authMW *auth.Middleware
	logger *slog.Logger
}

func NewHandler(svc *Service, authMW *auth.Middleware, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, authMW: authMW, logger: logger}
}

// Mount: GET /exchange-rates/current and /history, signed in.
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Route("/exchange-rates", func(r chi.Router) {
			r.Get("/current", h.Current)
			r.Get("/history", h.History)
		})
	})
}

type rateDTO struct {
	Currency   string    `json:"currency"`
	Source     string    `json:"source"`
	RateAvg    float64   `json:"rateAvg"`
	RateBuy    float64   `json:"rateBuy"`
	RateSell   float64   `json:"rateSell"`
	LastUpdate time.Time `json:"lastUpdate"`
	FetchedAt  time.Time `json:"fetchedAt"`
	// Stale: no upstream confirmed it in 24 h (plan 5.2).
	Stale bool `json:"stale"`
}

type historyPointDTO struct {
	Day        string    `json:"day"` // YYYY-MM-DD
	RateAvg    float64   `json:"rateAvg"`
	RateBuy    float64   `json:"rateBuy"`
	RateSell   float64   `json:"rateSell"`
	LastUpdate time.Time `json:"lastUpdate"`
}

type historyDTO struct {
	Currency string            `json:"currency"`
	Source   string            `json:"source"`
	Points   []historyPointDTO `json:"points"`
}

// History: GET /exchange-rates/history?currency=USD&source=bolsa&days=30.
// currency defaults to USD and source to bolsa (MEP).
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	currency := q.Get("currency")
	if currency == "" {
		currency = "USD"
	}
	source := domain.RateSource(q.Get("source"))
	if source == "" {
		source = domain.DefaultRateSource
	}
	days := 0
	if v := q.Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			httpx.WriteError(w, r, h.logger, domain.NewValidationError("days", "debe ser un número"))
			return
		}
		days = n
	}
	points, err := h.svc.History(r.Context(), currency, source, days, time.Now())
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := historyDTO{Currency: currency, Source: string(source), Points: make([]historyPointDTO, len(points))}
	for i, p := range points {
		out.Points[i] = historyPointDTO{
			Day: p.Day.Format(time.DateOnly), RateAvg: p.Rate.RateAvg, RateBuy: p.Rate.RateBuy, RateSell: p.Rate.RateSell,
			LastUpdate: p.Rate.LastUpdate,
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) Current(w http.ResponseWriter, r *http.Request) {
	rates := h.svc.Current()
	// Orden estable para que el frontend no vea reordenamientos.
	sort.Slice(rates, func(i, j int) bool {
		if rates[i].Currency != rates[j].Currency {
			return rates[i].Currency < rates[j].Currency
		}
		return rates[i].Source < rates[j].Source
	})
	now := time.Now()
	out := make([]rateDTO, len(rates))
	for i, rt := range rates {
		out[i] = toDTO(rt)
		out[i].Stale = rt.Stale(now)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func toDTO(r domain.ExchangeRate) rateDTO {
	return rateDTO{
		Currency:   r.Currency,
		Source:     string(r.Source),
		RateAvg:    r.RateAvg,
		RateBuy:    r.RateBuy,
		RateSell:   r.RateSell,
		LastUpdate: r.LastUpdate,
		FetchedAt:  r.FetchedAt,
	}
}
