package fxrates

import (
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LucianoR23/api_go_ahorra/internal/auth"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

type Handler struct {
	svc    *Service
	authMW *auth.Middleware
	logger *slog.Logger
}

func NewHandler(svc *Service, authMW *auth.Middleware, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, authMW: authMW, logger: logger}
}

// Mount: GET /exchange-rates/current bajo RequireAuth.
// Futuro: /convert?amount=&from=&to= y /history.
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Route("/exchange-rates", func(r chi.Router) {
			r.Get("/current", h.Current)
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
	out := make([]rateDTO, len(rates))
	for i, rt := range rates {
		out[i] = toDTO(rt)
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
