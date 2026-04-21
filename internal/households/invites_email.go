package households

import (
	"fmt"
	"html"
	"time"
)

// renderInviteHTML genera el HTML del mail de invitación. Mantenido simple
// (HTML inline, sin CSS externo) porque los clientes de mail son hostiles.
// Si crece, moverlo a un html/template con assets.
func renderInviteHTML(householdName, acceptURL string, expiresAt time.Time) string {
	hh := html.EscapeString(householdName)
	url := html.EscapeString(acceptURL)
	exp := expiresAt.Format("02/01/2006 15:04")
	return fmt.Sprintf(`<!doctype html>
<html>
<body style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;background:#f5f5f7;margin:0;padding:24px;color:#1d1d1f">
  <div style="max-width:520px;margin:0 auto;background:#fff;border-radius:12px;padding:32px">
    <h1 style="margin:0 0 16px;font-size:22px">Te invitaron a Ahorra</h1>
    <p style="margin:0 0 16px;font-size:15px;line-height:1.5">
      Recibiste una invitación para unirte al hogar <strong>%s</strong>.
      Aceptala para empezar a compartir gastos y presupuestos.
    </p>
    <p style="margin:24px 0">
      <a href="%s"
         style="display:inline-block;background:#0a7aff;color:#fff;text-decoration:none;padding:12px 20px;border-radius:8px;font-weight:600">
        Aceptar invitación
      </a>
    </p>
    <p style="margin:16px 0 0;font-size:13px;color:#6e6e73">
      Este link expira el %s. Si no esperabas esta invitación, ignorá este mail.
    </p>
    <p style="margin:24px 0 0;font-size:12px;color:#8e8e93;word-break:break-all">
      ¿No se abre el botón? Copiá y pegá en tu navegador:<br>%s
    </p>
  </div>
</body>
</html>`, hh, url, exp, url)
}
