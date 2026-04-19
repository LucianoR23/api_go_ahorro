// Package domain define los tipos y errores de negocio de Ahorra.
//
// Esta capa NO conoce a pgx, chi ni HTTP. Es el vocabulario común entre
// repository, service y handler — cada capa traduce a y desde acá:
//
//	repository:  pgx.ErrNoRows   → domain.ErrNotFound
//	service:     regla de negocio → domain.ErrValidation / ErrConflict
//	handler:     domain.ErrX      → HTTP status + JSON
package domain

import "errors"

// Errores centinela. Las capas externas los comparan con errors.Is y
// los mapean a su representación (HTTP status, log, etc.).
//
// Por qué centinela y no tipos concretos: son más livianos y fáciles
// de comparar. Si alguna vez necesitamos meter metadata (campo inválido,
// valor esperado), envolvemos con un tipo custom que implemente Unwrap
// apuntando al centinela — así errors.Is sigue funcionando.
var (
	// ErrNotFound indica que el recurso pedido no existe o el caller no
	// tiene permiso para verlo (ambiguedad intencional para no filtrar
	// existencia de recursos ajenos).
	ErrNotFound = errors.New("domain: recurso no encontrado")

	// ErrConflict indica una violación de unicidad o estado incompatible.
	// Ej: email ya registrado, intentar cambiar is_shared después de creado.
	ErrConflict = errors.New("domain: conflicto con el estado actual")

	// ErrUnauthorized: el request no trae credenciales válidas (sin JWT,
	// expirado, inválido). Respuesta HTTP: 401.
	ErrUnauthorized = errors.New("domain: no autenticado")

	// ErrForbidden: el caller está autenticado pero no puede operar sobre
	// este recurso (ej: user no es miembro del household). Respuesta: 403.
	ErrForbidden = errors.New("domain: operación no permitida")

	// ErrValidation: input inválido a nivel de reglas de negocio (no de
	// esquema JSON). Respuesta: 400 o 422. Usualmente envuelto con un
	// ValidationError para exponer el campo puntual.
	ErrValidation = errors.New("domain: validación fallida")
)

// ValidationError envuelve ErrValidation con detalle del campo fallido.
// Se devuelve al handler para poder armar un response más útil que
// "validación fallida" — el frontend puede resaltar el input concreto.
//
// Uso:
//
//	return domain.NewValidationError("email", "formato inválido")
//
// Chequeo en handler:
//
//	if errors.Is(err, domain.ErrValidation) {
//	    var vErr *domain.ValidationError
//	    if errors.As(err, &vErr) { ... usa vErr.Field, vErr.Message ... }
//	}
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return "validación fallida en '" + e.Field + "': " + e.Message
}

// Unwrap hace que errors.Is(err, domain.ErrValidation) sea true.
func (e *ValidationError) Unwrap() error {
	return ErrValidation
}

func NewValidationError(field, message string) *ValidationError {
	return &ValidationError{Field: field, Message: message}
}

// AuthError envuelve ErrUnauthorized con un mensaje custom. Se usa cuando
// 401 no alcanza y queremos comunicarle algo más específico al cliente
// (ej: "email o contraseña incorrectos" en login).
//
// IMPORTANTE: mantener ambigüedad en los mensajes de login para no
// filtrar si un email está registrado (user enumeration attack).
//
// Ejemplos OK:
//   - "email o contraseña incorrectos"
//   - "sesión expirada, iniciá sesión de nuevo"
//
// Ejemplos NO:
//   - "el email no existe"
//   - "la contraseña es incorrecta"
type AuthError struct {
	Message string
}

func (e *AuthError) Error() string  { return e.Message }
func (e *AuthError) Unwrap() error  { return ErrUnauthorized }

func NewAuthError(message string) *AuthError {
	return &AuthError{Message: message}
}
