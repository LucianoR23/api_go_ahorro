package households

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/auth"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

// AdminHandler expone endpoints /admin/households/* para superadmin.
// El middleware auth.RequireSuperadmin gatea todo el grupo.
type AdminHandler struct {
	svc    *Service
	logger *slog.Logger
	authMW *auth.Middleware
}

func NewAdminHandler(svc *Service, authMW *auth.Middleware, logger *slog.Logger) *AdminHandler {
	return &AdminHandler{svc: svc, authMW: authMW, logger: logger}
}

// Mount registra las rutas bajo /admin/households.
func (h *AdminHandler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Use(h.authMW.RequireSuperadmin)

		r.Route("/admin/households", func(r chi.Router) {
			r.Get("/deleted", h.ListDeleted)
			r.Get("/{id}", h.Get)
			r.Post("/{id}/restore", h.Restore)
			r.Delete("/{id}/purge", h.Purge)
		})
	})
}

// adminHouseholdDTO incluye deletedAt (el DTO público lo omite).
type adminHouseholdDTO struct {
	householdDTO
	DeletedAt *string `json:"deletedAt,omitempty"`
}

// adminOwnerDTO: info mínima del owner para que el superadmin pueda
// identificar visualmente el hogar antes de restaurar/purgar.
type adminOwnerDTO struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

// deletedHouseholdDTO: shape del listado /admin/households/deleted.
// El front arma la tabla con id + name + owner para evitar errores.
type deletedHouseholdDTO struct {
	adminHouseholdDTO
	Owner adminOwnerDTO `json:"owner"`
}

func toAdminDTO(h domain.Household) adminHouseholdDTO {
	dto := adminHouseholdDTO{householdDTO: toHouseholdDTO(h)}
	if h.DeletedAt != nil {
		s := h.DeletedAt.Format("2006-01-02T15:04:05Z07:00")
		dto.DeletedAt = &s
	}
	return dto
}

func (h *AdminHandler) ListDeleted(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListDeleted(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := make([]deletedHouseholdDTO, len(list))
	for i, item := range list {
		out[i] = deletedHouseholdDTO{
			adminHouseholdDTO: toAdminDTO(item.Household),
			Owner: adminOwnerDTO{
				ID:        item.Owner.ID.String(),
				Email:     item.Owner.Email,
				FirstName: item.Owner.FirstName,
				LastName:  item.Owner.LastName,
			},
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *AdminHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("id", "no es un UUID válido"))
		return
	}
	hh, err := h.svc.GetAny(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAdminDTO(hh))
}

func (h *AdminHandler) Restore(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("id", "no es un UUID válido"))
		return
	}
	if err := h.svc.Restore(r.Context(), id); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) Purge(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("id", "no es un UUID válido"))
		return
	}
	if err := h.svc.Purge(r.Context(), id); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
