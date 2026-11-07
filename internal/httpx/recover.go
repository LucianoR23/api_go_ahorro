package httpx

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recoverer turns a handler panic into a 500 and logs it at ERROR with the
// stack, so it reaches error monitoring (plan 0.12) through the logger.
// chi's middleware.Recoverer prints to stderr, outside slog. Like chi's, it
// lets http.ErrAbortHandler through: net/http uses it to abort quietly.
func Recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				logger.ErrorContext(r.Context(), "panic en handler",
					"error", fmt.Errorf("panic: %v", rec),
					"method", r.Method,
					"path", r.URL.Path,
					"stack", string(debug.Stack()),
				)
				if r.Header.Get("Connection") != "Upgrade" {
					writeJSON(w, http.StatusInternalServerError, ErrorResponse{Code: "internal", Message: "error interno"})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
