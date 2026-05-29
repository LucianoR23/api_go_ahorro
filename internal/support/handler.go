package support

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/LucianoR23/api_go_ahorra/internal/auth"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

// uploadDeadline es el tiempo máximo que le damos a un upload de ticket
// (request + respuesta). Espeja el del servicio Soporte (~3min) y supera los
// ReadTimeout/WriteTimeout cortos del server, vía ResponseController.
const uploadDeadline = 3 * time.Minute

// readTimeout es el corte normal para las rutas de lectura/mensajes (no son
// uploads pesados). Espeja el timeout global del resto del API.
const readTimeout = 30 * time.Second

const (
	// maxTicketBodyBytes es un tope de seguridad contra bodies abusivos.
	// Soporte tiene su propio límite (~62MB) y rechaza con 413 antes de
	// llegar acá; este cap solo corta lo claramente abusivo. Doc §9: el body
	// legítimo máximo es ~70MB (3 × 20MB + slack).
	maxTicketBodyBytes = 75 << 20

	// maxMessageBodyBytes acota el JSON de un mensaje del hilo (body ≤4000
	// chars según doc §9). 1MB es holgado de sobra.
	maxMessageBodyBytes = 1 << 20
)

type Handler struct {
	svc    *Service
	authMW *auth.Middleware
	logger *slog.Logger
}

func NewHandler(svc *Service, authMW *auth.Middleware, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, authMW: authMW, logger: logger}
}

// Mount registra /support/* bajo RequireAuth. No usa X-Household-ID: el
// soporte es por usuario, no por hogar.
//
// El upload (POST /support/tickets) se monta SIN middleware.Timeout: los
// videos tardan más que el corte de 30s y el handler extiende sus propios
// deadlines con ResponseController. Por eso este Mount debe llamarse FUERA
// del grupo con timeout global (ver cmd/api/main.go). Las rutas de lectura/
// mensajes sí aplican el corte de 30s en su propio sub-grupo.
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Post("/support/tickets", h.CreateTicket) // → POST /tickets (multipart, upload)
	})

	r.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(readTimeout))
		r.Use(h.authMW.RequireAuth)
		r.Get("/support/tickets/mine", h.GetMyTickets)         // → GET  /tickets/mine
		r.Get("/support/tickets/{id}", h.GetTicket)            // → GET  /tickets/{id}
		r.Post("/support/tickets/{id}/messages", h.AddMessage) // → POST /tickets/{id}/messages
	})
}

// CreateTicket reenvía el multipart tal cual (streaming, sin bufferear en
// memoria): pasamos r.Body directo y el Content-Type con su boundary.
func (h *Handler) CreateTicket(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identity(w, r)
	if !ok {
		return
	}

	// Los uploads de video (≤20MB) superan los ReadTimeout(15s)/WriteTimeout(30s)
	// del server. ResponseController (Go 1.20+) extiende los deadlines de ESTA
	// conexión por encima de los globales, sin tocar nada app-wide. La defensa
	// anti-slowloris (ReadHeaderTimeout/IdleTimeout) queda intacta. Si algún
	// wrapper del writer no soporta Unwrap, devuelve error y solo logueamos
	// (el upload sigue, sujeto a los timeouts cortos).
	rc := http.NewResponseController(w)
	deadline := time.Now().Add(uploadDeadline)
	if err := rc.SetReadDeadline(deadline); err != nil {
		h.logger.WarnContext(r.Context(), "support: SetReadDeadline no soportado por el writer", "error", err)
	}
	if err := rc.SetWriteDeadline(deadline); err != nil {
		h.logger.WarnContext(r.Context(), "support: SetWriteDeadline no soportado por el writer", "error", err)
	}

	body := http.MaxBytesReader(w, r.Body, maxTicketBodyBytes)
	h.svc.forward(w, r, id, proxyCall{
		method:      http.MethodPost,
		path:        "/tickets",
		body:        body,
		contentType: r.Header.Get("Content-Type"),
		contentLen:  r.ContentLength,
	})
}

func (h *Handler) GetMyTickets(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identity(w, r)
	if !ok {
		return
	}
	h.svc.forward(w, r, id, proxyCall{
		method:   http.MethodGet,
		path:     "/tickets/mine",
		rawQuery: r.URL.RawQuery,
	})
}

func (h *Handler) GetTicket(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identity(w, r)
	if !ok {
		return
	}
	ticketID := chi.URLParam(r, "id")
	if ticketID == "" {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("id", "requerido"))
		return
	}
	h.svc.forward(w, r, id, proxyCall{
		method: http.MethodGet,
		path:   "/tickets/" + ticketID,
	})
}

func (h *Handler) AddMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := h.identity(w, r)
	if !ok {
		return
	}
	ticketID := chi.URLParam(r, "id")
	if ticketID == "" {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("id", "requerido"))
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxMessageBodyBytes)
	h.svc.forward(w, r, id, proxyCall{
		method:      http.MethodPost,
		path:        "/tickets/" + ticketID + "/messages",
		body:        body,
		contentType: "application/json",
		contentLen:  r.ContentLength,
	})
}

// identity valida que el módulo esté habilitado y resuelve la identidad del
// reporter. Devuelve ok=false (y ya escribió la respuesta) si algo falla.
func (h *Handler) identity(w http.ResponseWriter, r *http.Request) (identity, bool) {
	if !h.svc.Enabled() {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, proxyError{"internal", "soporte no está configurado"})
		return identity{}, false
	}
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return identity{}, false
	}
	id, err := h.svc.resolveIdentity(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return identity{}, false
	}
	return id, true
}
