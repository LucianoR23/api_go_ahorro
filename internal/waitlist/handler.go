package waitlist

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"

	"github.com/LucianoR23/valuo_api/internal/httpx"
)

// maxBody: the contract rejects bodies over 4 KB.
const maxBody = 4 << 10

type Handler struct {
	svc            *Service
	logger         *slog.Logger
	allowedOrigins []string
	limiter        func(http.Handler) http.Handler
}

// NewHandler: limiter nil uses the contract's 5 requests per 10 minutes per
// IP (in memory, like the auth limits: one API instance).
func NewHandler(svc *Service, allowedOrigins []string, limiter func(http.Handler) http.Handler, logger *slog.Logger) *Handler {
	if limiter == nil {
		limiter = httprate.Limit(5, 10*time.Minute,
			httprate.WithKeyFuncs(httprate.KeyByIP),
			httprate.WithLimitHandler(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", strconv.Itoa(int((10 * time.Minute).Seconds())))
				httpx.WriteJSON(w, http.StatusTooManyRequests, httpx.ErrorResponse{
					Code: "rate_limited", Message: "Demasiados intentos. Probá de nuevo en unos minutos."})
			}))
	}
	return &Handler{svc: svc, logger: logger, allowedOrigins: allowedOrigins, limiter: limiter}
}

// CORS for the waitlist routes (contract): the landing's origins only, POST,
// Content-Type, no credentials. The app's CORS (with credentials) never
// applies to these paths.
func (h *Handler) CORS() func(http.Handler) http.Handler {
	return cors.Handler(cors.Options{
		AllowedOrigins:   h.allowedOrigins,
		AllowedMethods:   []string{http.MethodPost},
		AllowedHeaders:   []string{"Content-Type"},
		ExposedHeaders:   []string{"Retry-After"},
		AllowCredentials: false,
		MaxAge:           600,
	})
}

func (h *Handler) Mount(r chi.Router) {
	r.Route("/waitlist", func(r chi.Router) {
		r.Use(httpx.LimitBody(maxBody))
		r.With(h.limiter).Post("/", h.Signup)
		r.With(h.limiter).Post("/confirm", h.Confirm)
		r.Get("/unsubscribe", h.UnsubscribePage)
		r.Post("/unsubscribe", h.Unsubscribe)
	})
}

type signupRequest struct {
	Email          string  `json:"email"`
	Monotributo    *bool   `json:"monotributo"`
	Form           string  `json:"form"`
	Source         *string `json:"source"`
	Medium         *string `json:"medium"`
	Campaign       *string `json:"campaign"`
	ReferrerPath   *string `json:"referrer_path"`
	NoticeVersion  string  `json:"notice_version"`
	TurnstileToken string  `json:"turnstile_token"`
}

func (h *Handler) Signup(w http.ResponseWriter, r *http.Request) {
	var req signupRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if req.Monotributo == nil {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, httpx.ErrorResponse{Code: "validation", Message: "requerido", Field: "monotributo"})
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	err = h.svc.Signup(r.Context(), SignupInput{
		Email: req.Email, Monotributo: *req.Monotributo, Form: req.Form,
		Source: req.Source, Medium: req.Medium, Campaign: req.Campaign, ReferrerPath: req.ReferrerPath,
		NoticeVersion: req.NoticeVersion, TurnstileToken: req.TurnstileToken, RemoteIP: ip,
	})
	if errors.Is(err, ErrTurnstile) {
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Code: "turnstile_failed", Message: "No pudimos verificar que no seas un robot."})
		return
	}
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": statusPending})
}

func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	err := h.svc.Confirm(r.Context(), req.Token)
	if errors.Is(err, ErrExpired) {
		httpx.WriteJSON(w, http.StatusGone, httpx.ErrorResponse{Code: "expired", Message: "El link venció o ya no es válido."})
		return
	}
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "confirmed"})
}

// UnsubscribePage: GET shows a button and never deletes (mail scanners
// prefetch links).
// pageCSP: the unsubscribe pages use inline styles and post a form to
// themselves; nothing else (overrides the API's default-src 'none').
const pageCSP = "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

func (h *Handler) UnsubscribePage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	w.Header().Set("Content-Security-Policy", pageCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if !h.svc.ValidUnsubscribeToken(token) {
		http.Error(w, "Este link no es válido.", http.StatusNotFound)
		return
	}
	_, _ = w.Write([]byte(unsubscribePage(h.svc.cfg.Brand, "?token="+token, false)))
}

// Unsubscribe: the page's button and RFC 8058 one-click both POST here.
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	ok, err := h.svc.Unsubscribe(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if !ok {
		http.Error(w, "link inválido", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Security-Policy", pageCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(unsubscribePage(h.svc.cfg.Brand, "", true)))
}
