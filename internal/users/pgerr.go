package users

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// isUniqueViolation devuelve true si el error viene de una violación
// de constraint UNIQUE. El código SQLSTATE 23505 es estable entre
// versiones de Postgres y locales, así que es más confiable que
// parsear el mensaje de texto.
//
// Lo mantenemos en un archivo aparte para poder reusarlo en otros
// repositories sin exponerlo fuera del paquete users. Si terminamos
// usándolo en más paquetes, lo movemos a internal/db/ como helper.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
