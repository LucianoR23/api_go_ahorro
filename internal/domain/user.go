package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// User es la representación de dominio de un usuario.
//
// Es DISTINTA al struct User de sqlcgen: acá no hay PasswordHash.
// El hash vive en DB y solo lo toca el repository/service de auth
// al validar login — nunca viaja a las capas superiores ni a JSON.
// Por eso es un tipo separado y no reusamos el generado.
type User struct {
	ID              uuid.UUID  `json:"id"`
	Email           string     `json:"email"`
	FirstName       string     `json:"firstName"`
	LastName        string     `json:"lastName"`
	EmailVerifiedAt *time.Time `json:"emailVerifiedAt,omitempty"`
	// IsSuperadmin habilita endpoints /admin/*. Se setea manualmente por DB.
	// Independiente del rol por-hogar: un superadmin es "user normal" en sus
	// hogares y solo obtiene poderes extra en las rutas /admin/*.
	IsSuperadmin bool      `json:"isSuperadmin"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// FullName devuelve "first last" limpio. Si el last está vacío (mononombres),
// devuelve solo el first. Útil para logs, emails, y displays agregados.
func (u User) FullName() string {
	if u.LastName == "" {
		return u.FirstName
	}
	return strings.TrimSpace(u.FirstName + " " + u.LastName)
}
