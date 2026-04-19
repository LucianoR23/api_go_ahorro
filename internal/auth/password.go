// Package auth concentra hashing de passwords, emisión/validación
// de JWTs y el service de registro/login. No toca HTTP directamente:
// los handlers viven en un subpaquete (o junto al service) y usan
// estas funciones.
package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost: factor de trabajo. Default de bcrypt es 10.
// Subimos a 12 porque en 2026 el hardware promedio lo soporta bien
// (~250ms por hash) y duplica el costo de un bruteforce.
// Si el servidor va a hashear muchos logins por segundo y se vuelve
// bottleneck, bajar a 11 es aceptable.
const bcryptCost = 12

// HashPassword produce un hash bcrypt listo para guardar en DB.
// bcrypt internamente genera el salt y lo embebe en el string resultante.
func HashPassword(plain string) (string, error) {
	if plain == "" {
		return "", errors.New("auth: password vacío")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("auth.HashPassword: %w", err)
	}
	return string(h), nil
}

// VerifyPassword compara un password en texto plano contra el hash
// guardado. Devuelve nil si matchea, error si no.
//
// Usa comparación en tiempo constante internamente (bcrypt lo implementa)
// para no filtrar información por timing attacks.
func VerifyPassword(hash, plain string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
	if err != nil {
		// No envolvemos con %w: el caller solo necesita saber "no matchea".
		// Exponer el detalle (hash corrupto, versión incompatible) sería
		// información extra sin valor para el flujo de login.
		return errors.New("auth: password inválido")
	}
	return nil
}
