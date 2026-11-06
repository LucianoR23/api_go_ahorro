package waitlist

import (
	"fmt"
	"html"
)

// aaipLegend: the legend Ley 25.326 asks for, word for word as on the landing
// (valuo_web src/content/legal/index.ts).
const aaipLegend = "La AGENCIA DE ACCESO A LA INFORMACIÓN PÚBLICA, en su carácter de Órgano de Control de la Ley N° 25.326, " +
	"tiene la atribución de atender las denuncias y reclamos que interpongan quienes resulten afectados en sus derechos " +
	"por incumplimiento de las normas vigentes en materia de protección de datos personales."

// renderConfirmHTML: the double opt-in mail (contract section 4): one button
// to the landing's /confirmar page, why they got it, how to leave or ask for
// deletion, the AAIP legend. No tracking pixels.
func renderConfirmHTML(brand, confirmURL, unsubscribeURL, privacyURL, contactEmail string) string {
	b := html.EscapeString(brand)
	contact := "respondé este mail"
	if contactEmail != "" {
		e := html.EscapeString(contactEmail)
		contact = fmt.Sprintf(`escribinos a <a href="mailto:%s" style="color:#6e6e73">%s</a>`, e, e)
	}
	return fmt.Sprintf(`<!doctype html>
<html lang="es-AR">
<body style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;background:#f5f5f7;margin:0;padding:24px;color:#1d1d1f">
  <div style="max-width:520px;margin:0 auto;background:#fff;border-radius:12px;padding:32px">
    <h1 style="margin:0 0 16px;font-size:22px">Confirmá tu lugar en la lista de espera</h1>
    <p style="margin:0 0 16px;font-size:15px;line-height:1.5">
      Te anotaste en la lista de espera de %[1]s. Confirmá que este email es tuyo y te avisamos cuando abramos.
    </p>
    <p style="margin:24px 0">
      <a href="%[2]s"
         style="display:inline-block;background:#0a7aff;color:#fff;text-decoration:none;padding:12px 20px;border-radius:8px;font-weight:600">
        Confirmar mi email
      </a>
    </p>
    <p style="margin:16px 0 0;font-size:13px;color:#6e6e73">
      El link vence en 48 horas. Si no fuiste vos, ignorá este mail: sin confirmar, tu email se borra solo a los 30 días.
    </p>
    <hr style="border:none;border-top:1px solid #e5e5ea;margin:24px 0">
    <p style="margin:0 0 8px;font-size:12px;color:#6e6e73;line-height:1.5">
      Recibís este mail porque alguien anotó esta dirección en la lista de espera de %[1]s.
      <a href="%[3]s" style="color:#6e6e73">Darme de baja</a> ·
      <a href="%[4]s" style="color:#6e6e73">Política de privacidad</a>.
      Para pedir que borremos tus datos, %[5]s.
    </p>
    <p style="margin:0;font-size:11px;color:#8e8e93;line-height:1.5">%[6]s</p>
  </div>
</body>
</html>`, b, html.EscapeString(confirmURL), html.EscapeString(unsubscribeURL), html.EscapeString(privacyURL), contact, aaipLegend)
}

// unsubscribePage: the minimal page behind the mail's "Darme de baja" link.
// GET shows a button (mail scanners prefetch links, so GET never deletes);
// the button POSTs.
func unsubscribePage(brand, actionURL string, done bool) string {
	b := html.EscapeString(brand)
	body := fmt.Sprintf(`<h1 style="font-size:20px;margin:0 0 12px">Darte de baja de la lista de espera de %s</h1>
    <p style="font-size:15px;line-height:1.5;margin:0 0 20px">Vamos a borrar tu email de la lista. No te vamos a escribir más.</p>
    <form method="post" action="%s">
      <button type="submit" style="background:#0a7aff;color:#fff;border:0;padding:12px 20px;border-radius:8px;font-weight:600;font-size:15px;cursor:pointer">Darme de baja</button>
    </form>`, b, html.EscapeString(actionURL))
	if done {
		body = fmt.Sprintf(`<h1 style="font-size:20px;margin:0 0 12px">Listo, te diste de baja</h1>
    <p style="font-size:15px;line-height:1.5;margin:0">Borramos tu email de la lista de espera de %s.</p>`, b)
	}
	return `<!doctype html>
<html lang="es-AR">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>Baja</title></head>
<body style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;background:#f5f5f7;margin:0;padding:24px;color:#1d1d1f">
  <main style="max-width:480px;margin:40px auto;background:#fff;border-radius:12px;padding:32px">
    ` + body + `
  </main>
</body>
</html>`
}
