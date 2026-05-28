// Package google valida ID tokens emitidos por Google Identity Services.
//
// Usamos el flow de ID token (no OAuth code + redirect): el frontend obtiene
// el JWT firmado por Google y nos lo manda. Acá validamos firma (JWKS),
// emisor, audience (nuestro client_id) y expiración con idtoken.Validate.
package google

import (
	"context"
	"fmt"

	"google.golang.org/api/idtoken"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// Claims es lo que extraemos del ID token. Subject es el identificador
// estable por (client_id, cuenta Google) — lo persistimos en
// user_identities.subject. Email + nombre los usamos para
// auto-vincular o crear el user nuevo.
type Claims struct {
	Subject    string
	Email      string
	GivenName  string
	FamilyName string
}

// Verifier valida ID tokens contra el endpoint público de Google.
// idtoken.Validate ya chequea firma (JWKS), iss, aud (== clientID) y exp.
type Verifier struct {
	clientID string
}

func NewVerifier(clientID string) *Verifier {
	return &Verifier{clientID: clientID}
}

// Verify valida el ID token y devuelve los claims útiles. Errores se
// envuelven en domain.ErrUnauthorized vía NewAuthError para que el handler
// devuelva 401 sin exponer detalles del verificador.
func (v *Verifier) Verify(ctx context.Context, rawIDToken string) (*Claims, error) {
	if v.clientID == "" {
		return nil, fmt.Errorf("google verifier: clientID vacío")
	}

	payload, err := idtoken.Validate(ctx, rawIDToken, v.clientID)
	if err != nil {
		return nil, domain.NewAuthError("token de Google inválido")
	}

	// email_verified es bool en los claims de Google. Si por alguna razón
	// Google devolviera un email sin verificar (cuenta corporativa rara),
	// rechazamos: la auto-vinculación por email requiere garantía de
	// propiedad, y sin email_verified no la tenemos.
	emailVerified, _ := payload.Claims["email_verified"].(bool)
	if !emailVerified {
		return nil, domain.NewAuthError("email de Google no verificado")
	}

	email, _ := payload.Claims["email"].(string)
	if email == "" {
		return nil, domain.NewAuthError("token de Google sin email")
	}

	// given_name / family_name son opcionales en los claims (cuentas viejas
	// o configs raras pueden no traerlos). Acá los devolvemos como vengan;
	// el service de auth aplica el fallback "Usuario".
	given, _ := payload.Claims["given_name"].(string)
	family, _ := payload.Claims["family_name"].(string)

	return &Claims{
		Subject:    payload.Subject,
		Email:      email,
		GivenName:  given,
		FamilyName: family,
	}, nil
}
