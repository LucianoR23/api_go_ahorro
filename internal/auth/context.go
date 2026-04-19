package auth

import (
	"context"

	"github.com/google/uuid"
)

// ctxKey es un tipo privado para las keys del context. Esto evita
// colisiones con keys de otros paquetes (dos paquetes podrían usar
// la string "userID" y pisarse mutuamente). Con un tipo privado,
// el compilador garantiza que solo este paquete puede leer/escribir
// en estas keys.
type ctxKey int

const (
	ctxKeyUserID ctxKey = iota
)

// ContextWithUserID devuelve un context derivado con el userID embebido.
// Lo usa el middleware después de validar el JWT.
func ContextWithUserID(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxKeyUserID, userID)
}

// UserIDFrom extrae el userID del context. El bool es false si no estaba
// (ej: endpoint que olvidó aplicar el middleware). Los handlers que
// requieren auth deberían chequear este bool y devolver 500 si falta:
// significaría un bug de wiring, no un error del cliente.
func UserIDFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxKeyUserID).(uuid.UUID)
	return id, ok
}
