// Package admin serves the superadmin panel (plan 1.11): an overview with
// counts, the accounts with their metadata, and the audit log of every
// account. Following plan §8b it shows aggregates and account metadata only:
// no amounts, no descriptions, nothing from inside a space. Access to a
// person's financial data for support comes later, tied to support tickets.
package admin

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/valuo_api/internal/auth"
	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/httpx"
)

const (
	pageSize     = 50
	activeWindow = 30 * 24 * time.Hour
	monthsShown  = 6
)

// Handler mounts /admin/overview, /admin/users and /admin/audit.
type Handler struct {
	q      *sqlcgen.Queries
	authMW *auth.Middleware
	loc    *time.Location
	now    func() time.Time
	logger *slog.Logger
	ai     AIStatus
}

// NewHandler: loc cuts weeks and months; now is the clock (nil = time.Now).
func NewHandler(pool *pgxpool.Pool, authMW *auth.Middleware, loc *time.Location, now func() time.Time, logger *slog.Logger) *Handler {
	if now == nil {
		now = time.Now
	}
	return &Handler{q: sqlcgen.New(pool), authMW: authMW, loc: loc, now: now, logger: logger}
}

func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Use(h.authMW.RequireSuperadmin)
		r.Get("/admin/overview", h.Overview)
		r.Get("/admin/users", h.Users)
		r.Get("/admin/users/{id}", h.User)
		r.Get("/admin/audit", h.Audit)
		r.Get("/admin/ai", h.AI)
	})
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}

func textPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}

// ---- overview ----

type monthDTO struct {
	Month    string `json:"month"` // YYYY-MM
	Expenses int64  `json:"expenses"`
	Incomes  int64  `json:"incomes"`
}

type overviewDTO struct {
	Accounts struct {
		Total    int64 `json:"total"`
		NewWeek  int64 `json:"newWeek"`
		NewMonth int64 `json:"newMonth"`
		Verified int64 `json:"verified"`
		Active   int64 `json:"active"`
	} `json:"accounts"`
	Households struct {
		Active  int64 `json:"active"`
		Deleted int64 `json:"deleted"`
	} `json:"households"`
	Waitlist  int64      `json:"waitlist"`
	Movements []monthDTO `json:"movements"`
}

func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	now := h.now().In(h.loc)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, h.loc)
	c, err := h.q.AdminOverviewCounts(r.Context(), sqlcgen.AdminOverviewCountsParams{
		WeekStart:   ts(now.Add(-7 * 24 * time.Hour)),
		MonthStart:  ts(monthStart),
		ActiveSince: ts(now.Add(-activeWindow)),
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	months, err := h.q.AdminMovementsByMonth(r.Context(), sqlcgen.AdminMovementsByMonthParams{
		Tz:        h.loc.String(),
		FromMonth: pgtype.Date{Time: monthStart.AddDate(0, -(monthsShown - 1), 0), Valid: true},
		ToMonth:   pgtype.Date{Time: monthStart, Valid: true},
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var out overviewDTO
	out.Accounts.Total, out.Accounts.NewWeek, out.Accounts.NewMonth = c.Accounts, c.NewWeek, c.NewMonth
	out.Accounts.Verified, out.Accounts.Active = c.Verified, c.Active
	out.Households.Active, out.Households.Deleted = c.Households, c.DeletedHouseholds
	out.Waitlist = c.Waitlist
	out.Movements = make([]monthDTO, len(months))
	for i, m := range months {
		out.Movements[i] = monthDTO{Month: m.Month.Time.Format("2006-01"), Expenses: m.Expenses, Incomes: m.Incomes}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ---- accounts ----

type userDTO struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	FirstName    string     `json:"firstName"`
	LastName     string     `json:"lastName"`
	CreatedAt    time.Time  `json:"createdAt"`
	VerifiedAt   *time.Time `json:"verifiedAt"`
	OnboardedAt  *time.Time `json:"onboardedAt"`
	DeletedAt    *time.Time `json:"deletedAt"`
	LastSeenAt   *time.Time `json:"lastSeenAt"`
	IsSuperadmin bool       `json:"isSuperadmin"`
	Households   int64      `json:"households"`
}

// likeQuery lower-cases s and escapes LIKE wildcards (backslash is the
// default escape in Postgres).
func likeQuery(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (h *Handler) Users(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) > 100 {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("q", "demasiado largo"))
		return
	}
	offset := 0
	if s := r.URL.Query().Get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 100_000 {
			httpx.WriteError(w, r, h.logger, domain.NewValidationError("offset", "inválido"))
			return
		}
		offset = n
	}
	rows, err := h.q.AdminListUsers(r.Context(), sqlcgen.AdminListUsersParams{
		Q: likeQuery(q), Off: int32(offset), Lim: pageSize + 1,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	more := len(rows) > pageSize
	if more {
		rows = rows[:pageSize]
	}
	items := make([]userDTO, len(rows))
	for i, u := range rows {
		items[i] = userDTO{
			ID: u.ID, Email: u.Email, FirstName: u.FirstName, LastName: u.LastName,
			CreatedAt: u.CreatedAt.Time.UTC(), VerifiedAt: timePtr(u.EmailVerifiedAt),
			OnboardedAt: timePtr(u.OnboardedAt), DeletedAt: timePtr(u.DeletedAt),
			LastSeenAt: timePtr(u.LastSeenAt), IsSuperadmin: u.IsSuperadmin, Households: u.Households,
		}
	}
	var next *int
	if more {
		n := offset + pageSize
		next = &n
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextOffset": next})
}

type spaceDTO struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	Role      string     `json:"role"`
	JoinedAt  time.Time  `json:"joinedAt"`
	DeletedAt *time.Time `json:"deletedAt"`
	Members   int64      `json:"members"`
}

type userDetailDTO struct {
	userDTO
	PendingEmail   *string    `json:"pendingEmail"`
	HasPassword    bool       `json:"hasPassword"`
	Providers      []string   `json:"providers"`
	ActiveSessions int64      `json:"activeSessions"`
	Spaces         []spaceDTO `json:"spaces"`
}

func (h *Handler) User(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("id", "no es un UUID válido"))
		return
	}
	u, err := h.q.AdminGetUser(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteError(w, r, h.logger, domain.ErrNotFound)
		return
	}
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	providers, err := h.q.AdminUserProviders(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	spaces, err := h.q.AdminUserHouseholds(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := userDetailDTO{
		userDTO: userDTO{
			ID: u.ID, Email: u.Email, FirstName: u.FirstName, LastName: u.LastName,
			CreatedAt: u.CreatedAt.Time.UTC(), VerifiedAt: timePtr(u.EmailVerifiedAt),
			OnboardedAt: timePtr(u.OnboardedAt), DeletedAt: timePtr(u.DeletedAt),
			LastSeenAt: timePtr(u.LastSeenAt), IsSuperadmin: u.IsSuperadmin,
		},
		PendingEmail:   textPtr(u.PendingEmail),
		HasPassword:    u.HasPassword,
		Providers:      providers,
		ActiveSessions: u.ActiveSessions,
		Spaces:         make([]spaceDTO, len(spaces)),
	}
	for i, s := range spaces {
		out.Spaces[i] = spaceDTO{
			ID: s.ID, Name: s.Name, Kind: s.Kind, Role: s.Role, JoinedAt: s.JoinedAt.Time.UTC(),
			DeletedAt: timePtr(s.DeletedAt), Members: s.Members,
		}
		if s.Kind == "shared" && !s.DeletedAt.Valid {
			out.Households++
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ---- audit ----

// groups maps the panel's filter to action patterns.
var groups = map[string][]string{
	"":          nil,
	"sessions":  {"login.%", "logout%"},
	"failed":    {"login.failed"},
	"account":   {"password.%", "email.%", "account.%"},
	"household": {"household.%"},
	"admin":     {"admin.%"},
}

type auditDTO struct {
	ID         uuid.UUID      `json:"id"`
	Action     string         `json:"action"`
	UserID     *uuid.UUID     `json:"userId"`
	UserEmail  *string        `json:"userEmail"`
	ActorID    *uuid.UUID     `json:"actorId"`
	ActorEmail *string        `json:"actorEmail"`
	Target     string         `json:"target"`
	Device     string         `json:"device"`
	IP         *string        `json:"ip,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"createdAt"`
}

// Cursors are "<RFC3339Nano>_<id>" of the last event of the previous page.
func cursorOf(e auditDTO) string {
	return e.CreatedAt.Format(time.RFC3339Nano) + "_" + e.ID.String()
}

func parseCursor(s string) (time.Time, uuid.UUID, bool) {
	at, id, ok := strings.Cut(s, "_")
	if !ok {
		return time.Time{}, uuid.Nil, false
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	return t, u, true
}

func (h *Handler) Audit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	patterns, ok := groups[q.Get("group")]
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("group", "filtro desconocido"))
		return
	}
	params := sqlcgen.AdminListAuditEventsParams{Patterns: patterns, Lim: pageSize + 1}
	if params.Patterns == nil {
		params.Patterns = []string{}
	}
	if s := q.Get("userId"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			httpx.WriteError(w, r, h.logger, domain.NewValidationError("userId", "no es un UUID válido"))
			return
		}
		params.UserID = &id
	}
	if s := q.Get("cursor"); s != "" {
		at, id, ok := parseCursor(s)
		if !ok {
			httpx.WriteError(w, r, h.logger, domain.NewValidationError("cursor", "inválido"))
			return
		}
		params.BeforeAt, params.BeforeID = ts(at), &id
	}
	rows, err := h.q.AdminListAuditEvents(r.Context(), params)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	more := len(rows) > pageSize
	if more {
		rows = rows[:pageSize]
	}
	items := make([]auditDTO, len(rows))
	for i, e := range rows {
		items[i] = auditDTO{
			ID: e.ID, Action: e.Action, UserID: e.UserID, UserEmail: textPtr(e.UserEmail),
			ActorID: e.ActorID, ActorEmail: textPtr(e.ActorEmail), Target: e.Target, Device: e.Device,
			IP: textPtr(e.Ip), CreatedAt: e.CreatedAt.Time.UTC(),
		}
		if len(e.Metadata) > 0 {
			var m map[string]any
			if json.Unmarshal(e.Metadata, &m) == nil && len(m) > 0 {
				items[i].Metadata = m
			}
		}
	}
	var next *string
	if more {
		c := cursorOf(items[len(items)-1])
		next = &c
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": next})
}
