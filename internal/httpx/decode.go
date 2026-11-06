package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// DecodeJSON reads one JSON value from the request body into dst, rejecting
// unknown fields. Every decoder failure becomes a validation error with a
// Spanish message the app can show as is; the raw Go text ("json: unknown
// field ...") never reaches the client.
func DecodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	return nil
}

func decodeError(err error) error {
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	var tooBig *http.MaxBytesError
	switch {
	case errors.Is(err, io.EOF):
		return domain.NewValidationError("body", "falta el cuerpo del pedido")
	case errors.Is(err, io.ErrUnexpectedEOF), errors.As(err, &syntax):
		return domain.NewValidationError("body", "JSON inválido")
	case errors.As(err, &typ):
		field := typ.Field
		if field == "" {
			field = "body"
		}
		return domain.NewValidationError(field, "tipo de dato incorrecto")
	case errors.As(err, &tooBig):
		return domain.NewValidationError("body", "el pedido es demasiado grande")
	}
	// encoding/json has no typed error for unknown fields.
	if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		return domain.NewValidationError("body", "campo desconocido: "+strings.Trim(name, `"`))
	}
	// Anything else comes from a field's own UnmarshalJSON (a malformed UUID,
	// for example).
	return domain.NewValidationError("body", "algún campo tiene un valor inválido")
}
