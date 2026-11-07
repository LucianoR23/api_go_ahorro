package audit

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/dates"
	"github.com/LucianoR23/valuo_api/internal/domain"
)

type emailSender interface {
	Configured() bool
	Send(ctx context.Context, to []string, subject, html string) error
}

type userLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, error)
}

// NewDeviceAlerts e-mails the account owner when a sign-in comes from a
// device not used in the last 90 days (plan 0.17). A nil value is a no-op.
type NewDeviceAlerts struct {
	sender     emailSender
	users      userLookup
	brand      string
	appBaseURL string
	logger     *slog.Logger
}

func NewNewDeviceAlerts(sender emailSender, users userLookup, brand, appBaseURL string, logger *slog.Logger) *NewDeviceAlerts {
	return &NewDeviceAlerts{sender: sender, users: users, brand: brand, appBaseURL: strings.TrimRight(appBaseURL, "/"), logger: logger}
}

// Send never fails the sign-in: problems are logged.
func (a *NewDeviceAlerts) Send(ctx context.Context, userID uuid.UUID, device string, at time.Time) {
	if a == nil || a.sender == nil || !a.sender.Configured() {
		return
	}
	u, err := a.users.GetByID(ctx, userID)
	if err != nil {
		a.logger.WarnContext(ctx, "audit: new-device alert user lookup failed", "error", err)
		return
	}
	subject := "Nuevo inicio de sesión en " + a.brand
	body := renderNewDeviceHTML(a.brand, u.FirstName, device, at.In(dates.Location()), a.appBaseURL+"/seguridad")
	if err := a.sender.Send(ctx, []string{u.Email}, subject, body); err != nil {
		a.logger.WarnContext(ctx, "audit: new-device alert failed", "user_id", userID, "error", err)
	}
}

func renderNewDeviceHTML(brand, firstName, device string, at time.Time, securityURL string) string {
	name := "Hola"
	if firstName != "" {
		name = "Hola " + html.EscapeString(firstName)
	}
	return fmt.Sprintf(`<!doctype html>
<html>
<body style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;background:#f5f5f7;margin:0;padding:24px;color:#1d1d1f">
  <div style="max-width:520px;margin:0 auto;background:#fff;border-radius:12px;padding:32px">
    <h1 style="margin:0 0 16px;font-size:22px">Nuevo inicio de sesión</h1>
    <p style="margin:0 0 16px;font-size:15px;line-height:1.5">
      %[1]s, alguien inició sesión en tu cuenta de %[2]s desde <strong>%[3]s</strong> el %[4]s.
    </p>
    <p style="margin:0 0 16px;font-size:15px;line-height:1.5">
      Si fuiste vos, no tenés que hacer nada. Si no fuiste vos, cambiá tu contraseña y cerrá
      la sesión en todos los dispositivos desde Seguridad.
    </p>
    <p style="margin:24px 0">
      <a href="%[5]s"
         style="display:inline-block;background:#0a7aff;color:#fff;text-decoration:none;padding:12px 20px;border-radius:8px;font-weight:600">
        Ir a Seguridad
      </a>
    </p>
  </div>
</body>
</html>`, name, html.EscapeString(brand), html.EscapeString(device), at.Format("02/01/2006 15:04"), html.EscapeString(securityURL))
}
