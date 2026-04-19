// Package httpx centraliza utilidades HTTP transversales: respuestas
// de error, helpers de encoding JSON, middlewares comunes.
//
// El objetivo es que los handlers concretos no repitan el boilerplate
// de mapear dominio→status y armar el JSON: pasan un error y listo.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// ErrorResponse es la forma estable del JSON de error que ve el frontend.
// Mantenerla consistente permite al cliente parsear sin casos especiales.
//
//	code:    string corto y estable (ej: "not_found", "validation")
//	message: descripción humana (i18n futura a nivel frontend)
//	field:   solo presente en errores de validación de un campo puntual
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// WriteError inspecciona el error, lo clasifica contra los centinelas de
// dominio, y escribe la respuesta HTTP apropiada. Si el error no matchea
// ningún centinela, cae en 500 y se loggea como interno.
//
// El logger se inyecta para que el handler pueda agregar request_id/user_id
// en el contexto antes de llamar. No lo sacamos del request directamente
// para mantener este paquete independiente de cómo se setea el logging.
func WriteError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeJSON(w, http.StatusNotFound, ErrorResponse{
			Code:    "not_found",
			Message: "recurso no encontrado",
		})

	case errors.Is(err, domain.ErrConflict):
		writeJSON(w, http.StatusConflict, ErrorResponse{
			Code:    "conflict",
			Message: err.Error(),
		})

	case errors.Is(err, domain.ErrUnauthorized):
		// Si el service envolvió con AuthError, usamos su mensaje.
		// Sino, fallback al genérico para casos donde solo llega el centinela
		// (ej: middleware de JWT con token expirado).
		msg := "autenticación requerida"
		var authErr *domain.AuthError
		if errors.As(err, &authErr) {
			msg = authErr.Message
		}
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{
			Code:    "unauthorized",
			Message: msg,
		})

	case errors.Is(err, domain.ErrForbidden):
		writeJSON(w, http.StatusForbidden, ErrorResponse{
			Code:    "forbidden",
			Message: "operación no permitida",
		})

	case errors.Is(err, domain.ErrValidation):
		// Intentamos extraer detalle del campo. Si el error no es del tipo
		// ValidationError (p. ej. el service devolvió ErrValidation pelado),
		// respondemos igual pero sin field.
		var vErr *domain.ValidationError
		resp := ErrorResponse{Code: "validation", Message: err.Error()}
		if errors.As(err, &vErr) {
			resp.Field = vErr.Field
			resp.Message = vErr.Message
		}
		writeJSON(w, http.StatusUnprocessableEntity, resp)

	default:
		// Error no clasificado: no exponemos el mensaje crudo al cliente
		// (puede tener detalles internos / pgx stack). Logueamos el real
		// y devolvemos un mensaje genérico.
		logger.ErrorContext(r.Context(), "error interno no manejado",
			"error", err,
			"method", r.Method,
			"path", r.URL.Path,
		)
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{
			Code:    "internal",
			Message: "error interno",
		})
	}
}

// WriteJSON es un helper para respuestas exitosas. Setea content-type y
// encodea. Si falla el encode, loguea (pero el status ya se envió).
func WriteJSON(w http.ResponseWriter, status int, body any) {
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// Si el encode falla no hay mucho que hacer: el status ya se envió y
	// el body queda truncado. El cliente se entera por deserialización.
	_ = json.NewEncoder(w).Encode(body)
}
