package creditperiods

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/auth"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/httpx"
)

type Handler struct {
	svc    *Service
	logger *slog.Logger
	authMW *auth.Middleware
}

func NewHandler(svc *Service, authMW *auth.Middleware, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger, authMW: authMW}
}

// Mount registra rutas bajo /payment-methods/{id}/credit-card/periods/*.
// El {id} es el payment_method_id (más intuitivo para el frontend que
// manejar credit_card_id directo).
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)

		r.Route("/payment-methods/{id}/credit-card/periods", func(r chi.Router) {
			r.Get("/", h.List)
			r.Get("/status", h.Status)
			r.Put("/{ym}", h.Upsert)
			r.Delete("/{ym}", h.Delete)
		})
	})
}

// ===================== DTOs =====================

type periodDTO struct {
	CreditCardID string    `json:"creditCardId"`
	PeriodYM     string    `json:"periodYm"`
	ClosingDate  string    `json:"closingDate"`
	DueDate      string    `json:"dueDate"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type upsertReq struct {
	ClosingDate string `json:"closingDate"`
	DueDate     string `json:"dueDate"`
}

type statusDTO struct {
	NoPeriodsLoaded bool       `json:"noPeriodsLoaded"`
	DueDatePassed   bool       `json:"dueDatePassed"`
	LatestPeriod    *periodDTO `json:"latestPeriod,omitempty"`
	OpenPeriod      openDTO    `json:"openPeriod"`
}

// openDTO: the period open today; loaded is false when its dates come from
// the card's default days (plan 4.3).
type openDTO struct {
	PeriodYM    string `json:"periodYm"`
	ClosingDate string `json:"closingDate"`
	DueDate     string `json:"dueDate"`
	Loaded      bool   `json:"loaded"`
}

// ===================== handlers =====================

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, pmID, err := h.userAndPM(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	list, err := h.svc.List(r.Context(), userID, pmID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := make([]periodDTO, len(list))
	for i, p := range list {
		out[i] = toDTO(p)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) Upsert(w http.ResponseWriter, r *http.Request) {
	userID, pmID, err := h.userAndPM(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	ym := chi.URLParam(r, "ym")
	var req upsertReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	closing, err := time.Parse("2006-01-02", req.ClosingDate)
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("closingDate", "formato debe ser YYYY-MM-DD"))
		return
	}
	due, err := time.Parse("2006-01-02", req.DueDate)
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("dueDate", "formato debe ser YYYY-MM-DD"))
		return
	}
	p, err := h.svc.Upsert(r.Context(), userID, pmID, UpsertInput{
		PeriodYM:    ym,
		ClosingDate: closing,
		DueDate:     due,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(p))
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, pmID, err := h.userAndPM(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	ym := chi.URLParam(r, "ym")
	if err := h.svc.Delete(r.Context(), userID, pmID, ym); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	userID, pmID, err := h.userAndPM(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	st, err := h.svc.Status(r.Context(), userID, pmID, time.Now())
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := statusDTO{
		NoPeriodsLoaded: st.NoPeriodsLoaded,
		DueDatePassed:   st.DueDatePassed,
		OpenPeriod: openDTO{
			PeriodYM: st.OpenPeriod.PeriodYM, ClosingDate: st.OpenPeriod.ClosingDate.Format(time.DateOnly),
			DueDate: st.OpenPeriod.DueDate.Format(time.DateOnly), Loaded: st.OpenPeriod.Loaded,
		},
	}
	if st.LatestPeriod != nil {
		d := toDTO(*st.LatestPeriod)
		out.LatestPeriod = &d
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ===================== helpers =====================

func (h *Handler) userAndPM(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		return uuid.Nil, uuid.Nil, domain.ErrUnauthorized
	}
	pmID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, domain.NewValidationError("id", "no es un UUID válido")
	}
	return userID, pmID, nil
}

func toDTO(p domain.CreditCardPeriod) periodDTO {
	return periodDTO{
		CreditCardID: p.CreditCardID.String(),
		PeriodYM:     p.PeriodYM,
		ClosingDate:  p.ClosingDate.Format("2006-01-02"),
		DueDate:      p.DueDate.Format("2006-01-02"),
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
	}
}

func decodeJSON(r *http.Request, dst any) error {
	return httpx.DecodeJSON(r, dst)
}
