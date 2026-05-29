package support

import (
	"io"
	"net"
	"net/http"

	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

// proxyError es el shape de error que el front ya entiende ({code, message}).
// Lo usamos solo para errores locales del proxy; las respuestas del servicio
// Soporte se reenvían verbatim (su body de error ya tiene esta forma).
type proxyError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// proxyCall describe la llamada a reenviar a Soporte.
type proxyCall struct {
	method      string
	path        string // path en Soporte, ej. "/tickets" (sin baseURL)
	rawQuery    string // query string a propagar, sin "?"
	body        io.Reader
	contentType string
	contentLen  int64 // -1 si desconocido (chunked)
}

// forward arma la request a Lemy Support inyectando X-App-Key, la identidad
// del reporter y la IP real del cliente; hace el round-trip y copia status +
// body de la respuesta verbatim al cliente (pass-through). Así el proxy no
// duplica los tipos del SDK y queda resiliente a cambios menores del contrato.
func (s *Service) forward(w http.ResponseWriter, r *http.Request, id identity, call proxyCall) {
	u := s.cfg.BaseURL + call.path
	if call.rawQuery != "" {
		u += "?" + call.rawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), call.method, u, call.body)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "support: armar request falló", "error", err, "path", call.path)
		httpx.WriteJSON(w, http.StatusInternalServerError, proxyError{"internal", "error interno"})
		return
	}
	if call.contentLen > 0 {
		req.ContentLength = call.contentLen
	}

	req.Header.Set("X-App-Key", s.cfg.APIKey)
	req.Header.Set("X-Reporter-User-Id", id.userID)
	req.Header.Set("X-Reporter-Email", id.email)
	req.Header.Set("Accept", "application/json")
	if call.contentType != "" {
		req.Header.Set("Content-Type", call.contentType)
	}

	// Reenviamos la IP real del cliente. El rate-limit por IP de Soporte
	// (60/min en POST /tickets) usa X-Forwarded-For; sin esto TODOS los
	// usuarios de Ahorra colapsarían en la IP del API Go y compartirían el
	// bucket. middleware.RealIP ya dejó la IP real en r.RemoteAddr.
	if ip := clientIP(r); ip != "" {
		req.Header.Set("X-Forwarded-For", ip)
	}

	// NB: deliberadamente NO copiamos el header Origin del browser. El
	// origin pinning de Soporte responde 403 si Origin no matchea los
	// allowed_origins de la app; al llamar server-to-server sin Origin,
	// el servicio permite. Registrar la app con allowed_origins vacío.

	resp, err := s.httpClient.Do(req)
	if err != nil {
		// Fallo de transporte (timeout, sin conexión, body excedido). No es
		// culpa del usuario: devolvemos 502 genérico.
		s.logger.ErrorContext(r.Context(), "support: upstream no disponible", "error", err, "path", call.path)
		httpx.WriteJSON(w, http.StatusBadGateway, proxyError{"internal", "servicio de soporte no disponible"})
		return
	}
	defer resp.Body.Close()

	// Un 401 de Soporte = API key inválida o rotada (NO sesión del usuario
	// de Ahorra). Lo logueamos a nivel error para que el equipo lo note —
	// el front igual recibe el 401 tal cual.
	if resp.StatusCode == http.StatusUnauthorized {
		s.logger.ErrorContext(r.Context(), "support: 401 del servicio — revisar SOPORTE_API_KEY (¿rotada?)", "path", call.path)
	}

	// Pass-through: el body de error del servicio ya viene en {code, message}
	// (incluye file_too_large/unsupported_format/rate_limited). Propagamos
	// Content-Type y Retry-After para que el front muestre el backoff.
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		w.Header().Set("Retry-After", ra)
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		s.logger.WarnContext(r.Context(), "support: copia de respuesta falló", "error", err, "path", call.path)
	}
}

// clientIP extrae la IP (sin puerto) de r.RemoteAddr, que middleware.RealIP
// ya normalizó a la IP real del cliente. Si no se puede separar el puerto,
// devuelve el RemoteAddr crudo.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
