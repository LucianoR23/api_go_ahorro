package waitlist

import (
	"encoding/csv"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LucianoR23/valuo_api/internal/audit"
	"github.com/LucianoR23/valuo_api/internal/auth"
	"github.com/LucianoR23/valuo_api/internal/httpx"
)

// AdminHandler: superadmin operations on the waitlist (contract section 5),
// every one written to the audit log.
type AdminHandler struct {
	svc    *Service
	authMW *auth.Middleware
	audit  *audit.Recorder
	logger *slog.Logger
}

func NewAdminHandler(svc *Service, authMW *auth.Middleware, rec *audit.Recorder, logger *slog.Logger) *AdminHandler {
	return &AdminHandler{svc: svc, authMW: authMW, audit: rec, logger: logger}
}

func (h *AdminHandler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Use(h.authMW.RequireSuperadmin)
		r.Get("/admin/waitlist.csv", h.Export)
		r.Delete("/admin/waitlist", h.Delete)
	})
}

func (h *AdminHandler) record(r *http.Request, action string, meta map[string]any) {
	actor, _ := auth.UserIDFrom(r.Context())
	h.audit.Record(r.Context(), r, audit.Event{ActorID: &actor, Action: action, Target: "waitlist", Metadata: meta})
}

// Export: the confirmed entries as CSV (B.5 "Admin export").
func (h *AdminHandler) Export(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.q.ListConfirmedWaitlistEntries(r.Context())
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	h.record(r, audit.AdminWaitlistExport, map[string]any{"rows": len(rows)})
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="waitlist-`+time.Now().Format("2006-01-02")+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"email", "monotributo", "form", "source", "medium", "campaign", "referrer_path", "notice_version", "created_at", "confirmed_at"})
	for _, e := range rows {
		mono := "false"
		if e.Monotributo {
			mono = "true"
		}
		_ = cw.Write([]string{
			csvSafe(e.Email), mono, e.Form, csvSafe(e.Source.String), csvSafe(e.Medium.String), csvSafe(e.Campaign.String),
			csvSafe(e.ReferrerPath.String), csvSafe(e.NoticeVersion),
			e.CreatedAt.Time.UTC().Format(time.RFC3339), e.ConfirmedAt.Time.UTC().Format(time.RFC3339),
		})
	}
	cw.Flush()
}

// csvSafe neutralizes spreadsheet formulas (CSV injection): a cell starting
// with = + - @ gets a leading apostrophe.
func csvSafe(s string) string {
	if s != "" && (s[0] == '=' || s[0] == '+' || s[0] == '-' || s[0] == '@' || s[0] == '\t' || s[0] == '\r') {
		return "'" + s
	}
	return s
}

// Delete removes an address on request (Ley 25.326: within 5 business days).
// 204 even if it was not on the list.
func (h *AdminHandler) Delete(w http.ResponseWriter, r *http.Request) {
	email, err := normalizeEmail(r.URL.Query().Get("email"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	n, err := h.svc.q.DeleteWaitlistEntryByEmail(r.Context(), email)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	// The address itself is not written to the audit log (it was just erased).
	h.record(r, audit.AdminWaitlistDelete, map[string]any{"deleted": n})
	if n == 0 {
		h.logger.InfoContext(r.Context(), "waitlist delete: address not on the list")
	}
	w.WriteHeader(http.StatusNoContent)
}
