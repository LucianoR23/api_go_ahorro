package auth

import (
	"fmt"
	"html"
	"time"
)

// renderEmailVerificationHTML arma el mail de "verificá tu email". Mismo
// estilo inline que invites y password reset.
func renderEmailVerificationHTML(brand, firstName, verifyURL string, expiresAt time.Time) string {
	b := html.EscapeString(brand)
	name := html.EscapeString(firstName)
	if name == "" {
		name = "Hola"
	} else {
		name = "Hola " + name
	}
	url := html.EscapeString(verifyURL)
	exp := expiresAt.Format("02/01/2006 15:04")
	return fmt.Sprintf(`<!doctype html>
<html>
<body style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;background:#f5f5f7;margin:0;padding:24px;color:#1d1d1f">
  <div style="max-width:520px;margin:0 auto;background:#fff;border-radius:12px;padding:32px">
    <h1 style="margin:0 0 16px;font-size:22px">Verificá tu email</h1>
    <p style="margin:0 0 16px;font-size:15px;line-height:1.5">
      %[1]s, gracias por registrarte en %[5]s. Confirmá que este email es tuyo
      haciendo clic en el botón de abajo.
    </p>
    <p style="margin:24px 0">
      <a href="%[2]s"
         style="display:inline-block;background:#0a7aff;color:#fff;text-decoration:none;padding:12px 20px;border-radius:8px;font-weight:600">
        Verificar email
      </a>
    </p>
    <p style="margin:16px 0 0;font-size:13px;color:#6e6e73">
      Este link expira el %[3]s. Si no creaste una cuenta en %[5]s, ignorá este mail.
    </p>
    <p style="margin:24px 0 0;font-size:12px;color:#8e8e93;word-break:break-all">
      ¿No se abre el botón? Copiá y pegá en tu navegador:<br>%[4]s
    </p>
  </div>
</body>
</html>`, name, url, exp, url, b)
}

// renderEmailChangeHTML: the mail sent to the NEW address of an email change
// (plan 0.5). The account keeps its old email until this link is used.
func renderEmailChangeHTML(brand, firstName, newEmail, confirmURL string, expiresAt time.Time) string {
	b := html.EscapeString(brand)
	name := html.EscapeString(firstName)
	if name == "" {
		name = "Hola"
	} else {
		name = "Hola " + name
	}
	to := html.EscapeString(newEmail)
	url := html.EscapeString(confirmURL)
	exp := expiresAt.Format("02/01/2006 15:04")
	return fmt.Sprintf(`<!doctype html>
<html>
<body style="font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;background:#f5f5f7;margin:0;padding:24px;color:#1d1d1f">
  <div style="max-width:520px;margin:0 auto;background:#fff;border-radius:12px;padding:32px">
    <h1 style="margin:0 0 16px;font-size:22px">Confirmá tu nuevo email</h1>
    <p style="margin:0 0 16px;font-size:15px;line-height:1.5">
      %[1]s, pediste usar %[2]s como email de tu cuenta de %[6]s. El cambio se
      aplica cuando lo confirmes con el botón de abajo.
    </p>
    <p style="margin:24px 0">
      <a href="%[3]s"
         style="display:inline-block;background:#0a7aff;color:#fff;text-decoration:none;padding:12px 20px;border-radius:8px;font-weight:600">
        Confirmar nuevo email
      </a>
    </p>
    <p style="margin:16px 0 0;font-size:13px;color:#6e6e73">
      Este link expira el %[4]s. Si no pediste este cambio, ignorá este mail: tu cuenta sigue con su email actual.
    </p>
    <p style="margin:24px 0 0;font-size:12px;color:#8e8e93;word-break:break-all">
      ¿No se abre el botón? Copiá y pegá en tu navegador:<br>%[5]s
    </p>
  </div>
</body>
</html>`, name, to, url, exp, url, b)
}
