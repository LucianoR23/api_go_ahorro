// Package audit records security events of accounts (plan 0.17): sign-ins,
// failed sign-ins, password and email changes, sessions, household
// membership and every superadmin action. Users see their own events in
// /seguridad ("Actividad reciente"); a new device triggers an e-mail.
//
// Recording never fails a request: errors are logged and the request goes on.
// Retention: the IP is cleared after 7 days and events go after 12 months
// (Worker); a deleted account's events are deleted with it.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
)

// Actions. Stable strings: the app maps them to Spanish labels.
const (
	LoginSuccess        = "login.success"
	LoginFailed         = "login.failed"
	LoginNewDevice      = "login.new_device"
	Logout              = "logout"
	LogoutAll           = "logout.all"
	PasswordChanged     = "password.changed"
	PasswordReset       = "password.reset"
	EmailChangeRequest  = "email.change_requested"
	EmailChanged        = "email.changed"
	AccountDeleted      = "account.deleted"
	HouseholdJoined     = "household.joined"
	HouseholdMemberGone = "household.member_removed"
	HouseholdRole       = "household.role_changed"
	AdminRestore        = "admin.household_restored"
	AdminPurge          = "admin.household_purged"
	AdminWaitlistExport = "admin.waitlist_exported"
	AdminWaitlistDelete = "admin.waitlist_deleted"
)

// Event: one thing that happened. UserID is the account it happened to
// (nil when the account no longer exists); ActorID is who did it (the user,
// a household owner, or a superadmin). Metadata never holds amounts.
type Event struct {
	UserID   *uuid.UUID
	ActorID  *uuid.UUID
	Action   string
	Target   string
	Metadata map[string]any
}

// Entry: an event as the account owner sees it.
type Entry struct {
	Action    string         `json:"action"`
	Device    string         `json:"device"`
	IP        *string        `json:"ip,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}

// Recorder writes events. A nil *Recorder is a no-op, so handlers can call
// it without checking whether it was wired.
type Recorder struct {
	q      *sqlcgen.Queries
	logger *slog.Logger
	alerts *NewDeviceAlerts
}

func NewRecorder(pool *pgxpool.Pool, logger *slog.Logger) *Recorder {
	return &Recorder{q: sqlcgen.New(pool), logger: logger}
}

// SetNewDeviceAlerts wires the e-mail sent on a sign-in from a new device.
func (r *Recorder) SetNewDeviceAlerts(a *NewDeviceAlerts) {
	if r != nil {
		r.alerts = a
	}
}

// Record stores ev with the request's IP, user agent and device label.
func (r *Recorder) Record(ctx context.Context, req *http.Request, ev Event) {
	if r == nil {
		return
	}
	var ip pgtype.Text
	ua := ""
	if req != nil {
		if host := clientIP(req); host != "" {
			ip = pgtype.Text{String: host, Valid: true}
		}
		ua = req.UserAgent()
		if len(ua) > 300 {
			ua = ua[:300]
		}
	}
	meta := []byte("{}")
	if len(ev.Metadata) > 0 {
		if b, err := json.Marshal(ev.Metadata); err == nil {
			meta = b
		}
	}
	if err := r.q.CreateAuditEvent(ctx, sqlcgen.CreateAuditEventParams{
		UserID: ev.UserID, ActorID: ev.ActorID, Action: ev.Action, Target: ev.Target,
		Ip: ip, UserAgent: ua, Device: DeviceLabel(ua), Metadata: meta,
	}); err != nil {
		r.logger.WarnContext(ctx, "audit: record failed", "action", ev.Action, "error", err)
	}
}

// RecordLogin records a successful sign-in and, when the device was not used
// by this account in the last 90 days (and it is not the first sign-in ever),
// a login.new_device event and the e-mail.
func (r *Recorder) RecordLogin(ctx context.Context, req *http.Request, userID uuid.UUID, method string) {
	if r == nil {
		return
	}
	device := DeviceLabel(req.UserAgent())
	check, err := r.q.AuditDeviceSeenRecently(ctx, sqlcgen.AuditDeviceSeenRecentlyParams{UserID: &userID, Device: device, Days: 90})
	if err != nil {
		r.logger.WarnContext(ctx, "audit: device lookup failed", "error", err)
	}
	r.Record(ctx, req, Event{UserID: &userID, ActorID: &userID, Action: LoginSuccess, Metadata: map[string]any{"method": method}})
	if err == nil && check.AnyLogin && !check.Seen {
		r.Record(ctx, req, Event{UserID: &userID, ActorID: &userID, Action: LoginNewDevice})
		r.alerts.Send(ctx, userID, device, time.Now())
	}
}

// ForUser lists the account's latest events, newest first.
func (r *Recorder) ForUser(ctx context.Context, userID uuid.UUID, limit int32) ([]Entry, error) {
	rows, err := r.q.ListAuditEventsForUser(ctx, sqlcgen.ListAuditEventsForUserParams{UserID: &userID, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, row := range rows {
		e := Entry{Action: row.Action, Device: row.Device, CreatedAt: row.CreatedAt.Time}
		if row.Ip.Valid {
			ip := row.Ip.String
			e.IP = &ip
		}
		if len(row.Metadata) > 2 {
			_ = json.Unmarshal(row.Metadata, &e.Metadata)
		}
		out = append(out, e)
	}
	return out, nil
}

// DeleteForUser removes an account's events (account deletion).
func (r *Recorder) DeleteForUser(ctx context.Context, userID uuid.UUID) error {
	if r == nil {
		return nil
	}
	return r.q.DeleteAuditEventsForUser(ctx, &userID)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// DeviceLabel turns a user agent into "Chrome en Windows" (browser and OS
// only: enough to recognize a device, nothing more).
func DeviceLabel(ua string) string {
	if ua == "" {
		return "Dispositivo desconocido"
	}
	has := func(sub string) bool { return strings.Contains(ua, sub) }
	browser := "Navegador"
	switch {
	case has("Edg/"):
		browser = "Edge"
	case has("OPR/"):
		browser = "Opera"
	case has("Chrome/"):
		browser = "Chrome"
	case has("Firefox/"):
		browser = "Firefox"
	case has("Safari/"):
		browser = "Safari"
	}
	os := ""
	switch {
	case has("Windows"):
		os = "Windows"
	case has("iPhone"), has("iPad"), has("iPod"):
		os = "iOS"
	case has("Mac OS"):
		os = "macOS"
	case has("Android"):
		os = "Android"
	case has("Linux"):
		os = "Linux"
	}
	if os == "" {
		return browser
	}
	return browser + " en " + os
}
