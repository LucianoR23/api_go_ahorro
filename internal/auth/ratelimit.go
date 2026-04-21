package auth

import (
	"net/http"
	"time"

	"github.com/go-chi/httprate"
)

// RateLimiter agrupa los middlewares de rate-limit que aplicamos a los
// endpoints de auth. Cada método devuelve un handler middleware compatible
// con chi (func(http.Handler) http.Handler).
//
// Implementación por defecto: in-memory (httprate). Suficiente para un
// solo proceso — si escalamos horizontalmente habría que migrar a Redis.
type RateLimiter interface {
	Login() func(http.Handler) http.Handler
	Register() func(http.Handler) http.Handler
	Refresh() func(http.Handler) http.Handler
	ForgotPassword() func(http.Handler) http.Handler
	ResetPassword() func(http.Handler) http.Handler
	ChangePassword() func(http.Handler) http.Handler
	VerifyEmail() func(http.Handler) http.Handler
	ResendVerification() func(http.Handler) http.Handler
}

// InMemoryRateLimiter implementa RateLimiter con httprate, que usa un
// limitador token-bucket en memoria, keyed por IP (chi.RealIP ya está
// montado a nivel global, así que httprate lee la IP correcta).
type InMemoryRateLimiter struct{}

func NewInMemoryRateLimiter() *InMemoryRateLimiter { return &InMemoryRateLimiter{} }

// Login: 5 intentos por minuto por IP. Endpoint más sensible — brute-force
// de contraseñas. Para agregar granularidad por email habría que keyBy el
// body, cosa que httprate soporta con WithKeyFuncs si se quiere subir.
func (r *InMemoryRateLimiter) Login() func(http.Handler) http.Handler {
	return httprate.Limit(5, time.Minute, httprate.WithKeyFuncs(httprate.KeyByIP))
}

// Register: 5 por hora por IP. Evita creación masiva de cuentas.
func (r *InMemoryRateLimiter) Register() func(http.Handler) http.Handler {
	return httprate.Limit(5, time.Hour, httprate.WithKeyFuncs(httprate.KeyByIP))
}

// Refresh: 30 por minuto por IP. Holgado — el frontend pega seguido
// para mantener la sesión, pero frenamos abuso de cookies robadas.
func (r *InMemoryRateLimiter) Refresh() func(http.Handler) http.Handler {
	return httprate.Limit(30, time.Minute, httprate.WithKeyFuncs(httprate.KeyByIP))
}

// ForgotPassword: 3 por hora por IP. Muy restrictivo — evita spam de
// correos y enumeration attacks.
func (r *InMemoryRateLimiter) ForgotPassword() func(http.Handler) http.Handler {
	return httprate.Limit(3, time.Hour, httprate.WithKeyFuncs(httprate.KeyByIP))
}

// ResetPassword: 10 por 15min por IP. Permite reintentos razonables si el
// user se equivoca escribiendo la contraseña nueva, pero no tanto como
// para probar tokens a bruteforce (el espacio de tokens es 2^256 igual).
func (r *InMemoryRateLimiter) ResetPassword() func(http.Handler) http.Handler {
	return httprate.Limit(10, 15*time.Minute, httprate.WithKeyFuncs(httprate.KeyByIP))
}

// ChangePassword: 10 por hora por IP (usuario logueado, riesgo menor).
func (r *InMemoryRateLimiter) ChangePassword() func(http.Handler) http.Handler {
	return httprate.Limit(10, time.Hour, httprate.WithKeyFuncs(httprate.KeyByIP))
}

// VerifyEmail: 20 por 15min por IP. Es idempotente y el espacio de tokens
// es gigante, pero limitamos para frenar scripts barridos.
func (r *InMemoryRateLimiter) VerifyEmail() func(http.Handler) http.Handler {
	return httprate.Limit(20, 15*time.Minute, httprate.WithKeyFuncs(httprate.KeyByIP))
}

// ResendVerification: 3 por hora por IP. Mismo criterio que ForgotPassword
// — cada request manda mail, así que lo mantenemos muy restrictivo.
func (r *InMemoryRateLimiter) ResendVerification() func(http.Handler) http.Handler {
	return httprate.Limit(3, time.Hour, httprate.WithKeyFuncs(httprate.KeyByIP))
}
