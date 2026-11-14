package inbound

import (
	"crypto/hmac"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/auth"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/expenseparse"
	"github.com/LucianoR23/valuo_api/internal/httpx"
)

// MaxMailBytes caps one mail; the Worker rejects bigger ones before posting.
// Notifications are a few KB; this stays under the 1 MB JSON cap.
const MaxMailBytes = 768 << 10

// MaxSkew is how old a signed request may be.
const MaxSkew = 5 * time.Minute

// Headers the Worker sends with the raw mail as the body.
const (
	HeaderTo        = "X-Inbound-To"
	HeaderFrom      = "X-Inbound-From"
	HeaderTimestamp = "X-Inbound-Timestamp"
	HeaderSignature = "X-Inbound-Signature"
)

type Handler struct {
	svc    *Service // nil when INBOUND_* is not configured
	authMW *auth.Middleware
	now    func() time.Time
	logger *slog.Logger
}

func NewHandler(svc *Service, authMW *auth.Middleware, now func() time.Time, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, authMW: authMW, now: now, logger: logger}
}

// Mount: the Worker's webhook (signed, no session) and the app's routes.
// Without configuration every route answers 503 and the app hides it.
func (h *Handler) Mount(r chi.Router) {
	r.Post("/inbound/email", h.Receive)
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Get("/inbound/address", h.Address)
		r.Get("/inbound/messages", h.List)
		r.Post("/inbound/messages/{id}/accept-sender", h.AcceptSender)
		r.Post("/inbound/messages/{id}/dismiss", h.Dismiss)
		r.Post("/inbound/messages/{id}/confirm", h.Confirm)
	})
}

func (h *Handler) off(w http.ResponseWriter) bool {
	if h.svc == nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, httpx.ErrorResponse{Code: "inbound_off", Message: "importar por mail no está disponible"})
		return true
	}
	return false
}

// Receive: the body is the raw mail. 204 for every valid signature, also
// when the address is unknown, so the Worker learns nothing about users.
func (h *Handler) Receive(w http.ResponseWriter, r *http.Request) {
	if h.off(w) {
		return
	}
	to, from := r.Header.Get(HeaderTo), r.Header.Get(HeaderFrom)
	ts := r.Header.Get(HeaderTimestamp)
	sig, err := hex.DecodeString(r.Header.Get(HeaderSignature))
	if to == "" || ts == "" || err != nil || len(sig) == 0 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || h.now().Sub(time.Unix(sec, 0)).Abs() > MaxSkew {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, MaxMailBytes+1))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if len(raw) > MaxMailBytes {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	if !hmac.Equal(sig, h.svc.Keys().Sign(ts, to, from, raw)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if err := h.svc.Receive(r.Context(), to, from, raw); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Address(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	addr, err := h.svc.Address(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"address": addr})
}

type messageDTO struct {
	ID         uuid.UUID             `json:"id"`
	Kind       string                `json:"kind"`
	Status     string                `json:"status"`
	Source     *string               `json:"source"`
	Reading    *expenseparse.Reading `json:"reading"`
	Sentence   *string               `json:"sentence"`
	Sender     *string               `json:"sender"`
	GmailCode  *string               `json:"gmailCode"`
	ReceivedAt time.Time             `json:"receivedAt"`
}

func opt(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	msgs, err := h.svc.List(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := make([]messageDTO, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageDTO{
			ID: m.ID, Kind: m.Kind, Status: m.Status, Source: opt(m.Source), Reading: m.Reading,
			Sentence: opt(m.Sentence), Sender: opt(m.Sender), GmailCode: opt(m.GmailCode), ReceivedAt: m.Received,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) AcceptSender(w http.ResponseWriter, r *http.Request) {
	h.act(w, r, func(userID, id uuid.UUID) error { return h.svc.AcceptSender(r.Context(), userID, id) })
}

func (h *Handler) Dismiss(w http.ResponseWriter, r *http.Request) {
	h.act(w, r, func(userID, id uuid.UUID) error { return h.svc.Dismiss(r.Context(), userID, id) })
}

func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	h.act(w, r, func(userID, id uuid.UUID) error {
		var in struct {
			ExpenseID uuid.UUID `json:"expenseId"`
		}
		if err := httpx.DecodeJSON(r, &in); err != nil {
			return err
		}
		if in.ExpenseID == uuid.Nil {
			return domain.NewValidationError("expenseId", "es obligatorio")
		}
		return h.svc.Confirm(r.Context(), userID, id, in.ExpenseID)
	})
}

func (h *Handler) act(w http.ResponseWriter, r *http.Request, fn func(userID, id uuid.UUID) error) {
	userID, ok := h.user(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.ErrNotFound)
		return
	}
	if err := fn(userID, id); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) user(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if h.off(w) {
		return uuid.Nil, false
	}
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errors.New("middleware faltante"))
		return uuid.Nil, false
	}
	return userID, true
}
