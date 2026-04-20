package push

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/LucianoR23/api_go_ahorra/internal/auth"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

type Handler struct {
	svc    *Service
	logger *slog.Logger
	authMW *auth.Middleware
}

func NewHandler(svc *Service, authMW *auth.Middleware, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger, authMW: authMW}
}

// Mount registra /push/*. La public key es pública (el cliente la necesita
// antes de suscribirse, por eso está fuera del grupo con auth). El resto
// requiere JWT.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/push/vapid-public-key", h.GetVAPIDPublicKey)

	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Route("/push/subscriptions", func(r chi.Router) {
			r.Post("/", h.Subscribe)
			r.Delete("/", h.Unsubscribe)
		})
	})
}

type vapidKeyResponse struct {
	PublicKey string `json:"publicKey"`
	Enabled   bool   `json:"enabled"`
}

func (h *Handler) GetVAPIDPublicKey(w http.ResponseWriter, r *http.Request) {
	key := h.svc.PublicKey()
	httpx.WriteJSON(w, http.StatusOK, vapidKeyResponse{
		PublicKey: key,
		Enabled:   key != "",
	})
}

// subscribeRequest matchea el shape de PushSubscription.toJSON() del browser.
type subscribeRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

type subscriptionDTO struct {
	ID       string `json:"id"`
	Endpoint string `json:"endpoint"`
}

func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}

	var req subscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("body", "JSON inválido: "+err.Error()))
		return
	}

	sub, err := h.svc.Subscribe(r.Context(), SubscribeInput{
		UserID:    userID,
		Endpoint:  req.Endpoint,
		P256dh:    req.Keys.P256dh,
		Auth:      req.Keys.Auth,
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, subscriptionDTO{
		ID:       sub.ID.String(),
		Endpoint: sub.Endpoint,
	})
}

type unsubscribeRequest struct {
	Endpoint string `json:"endpoint"`
}

func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	var req unsubscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("body", "JSON inválido: "+err.Error()))
		return
	}
	if req.Endpoint == "" {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("endpoint", "requerido"))
		return
	}
	if err := h.svc.Unsubscribe(r.Context(), userID, req.Endpoint); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
