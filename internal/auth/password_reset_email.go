package auth

import (
	"fmt"
	"html"
	"time"
)

// renderPasswordResetHTML arma el mail de reset. Mismo estilo inline que
// las invitaciones (ver households/invites_email.go). El link incluye el
// token plano en la query — el backend lo hashea al recibirlo.
func renderPasswordResetHTML(firstName, resetURL string, expiresAt time.Time) string {
	name := html.EscapeString(firstName)
	if name == "" {
		name = "Hola"
	} else {
		name = "Hola " + name
	}
	url := html.EscapeString(resetURL)
	exp := expiresAt.Format("02/01/2006 15:04")
	return fmt.Sprintf(`<!doctype html>
<html>
<body style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;background:#f5f5f7;margin:0;padding:24px;color:#1d1d1f">
  <div style="max-width:520px;margin:0 auto;background:#fff;border-radius:12px;padding:32px">
    <h1 style="margin:0 0 16px;font-size:22px">Restablecer contraseña</h1>
    <p style="margin:0 0 16px;font-size:15px;line-height:1.5">
      %s, recibimos una solicitud para restablecer la contraseña de tu cuenta de Ahorra.
      Hacé clic en el botón para elegir una nueva.
    </p>
    <p style="margin:24px 0">
      <a href="%s"
         style="display:inline-block;background:#0a7aff;color:#fff;text-decoration:none;padding:12px 20px;border-radius:8px;font-weight:600">
        Restablecer contraseña
      </a>
    </p>
    <p style="margin:16px 0 0;font-size:13px;color:#6e6e73">
      Este link expira el %s. Si no pediste restablecer tu contraseña, ignorá este mail — tu cuenta sigue segura.
    </p>
    <p style="margin:24px 0 0;font-size:12px;color:#8e8e93;word-break:break-all">
      ¿No se abre el botón? Copiá y pegá en tu navegador:<br>%s
    </p>
  </div>
</body>
</html>`, name, url, exp, url)
}
