package httpx

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// Códigos ANSI de color. Los navegadores/editores no los interpretan;
// solo los emuladores de terminal (Windows Terminal, iTerm, GitBash, etc.)
const (
	colorReset   = "\x1b[0m"
	colorBold    = "\x1b[1m"
	colorGray    = "\x1b[90m"
	colorRed     = "\x1b[31m"
	colorGreen   = "\x1b[32m"
	colorYellow  = "\x1b[33m"
	colorBlue    = "\x1b[34m"
	colorMagenta = "\x1b[35m"
	colorCyan    = "\x1b[36m"
)

// statusWriter envuelve http.ResponseWriter para capturar el status code.
// net/http no expone el status escrito, así que interceptamos WriteHeader.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	// Si no se llamó WriteHeader, el primer Write implica 200.
	if sw.status == 0 {
		sw.status = http.StatusOK
	}
	n, err := sw.ResponseWriter.Write(b)
	sw.bytes += n
	return n, err
}

// DevRequestLogger: middleware con salida colorida, pensado para dev.
// Formato por línea:
//
//	HH:MM:SS GET /path 200 12.3ms 145B req_id=abc
//
// Colores:
//
//	método:     cyan
//	status 2xx: verde
//	status 3xx: cyan
//	status 4xx: amarillo
//	status 5xx: rojo (+ bold)
//	path:       default
//	latencia:   gris
//
// Respeta la var de entorno NO_COLOR — si está seteada, emite plano.
func DevRequestLogger(next http.Handler) http.Handler {
	noColor := os.Getenv("NO_COLOR") != ""

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		duration := time.Since(start)

		if sw.status == 0 {
			sw.status = http.StatusOK
		}

		statusColor := pickStatusColor(sw.status)
		methodColor := colorCyan

		if noColor {
			fmt.Printf("%s %s %s %d %s %dB\n",
				start.Format("15:04:05"),
				r.Method,
				r.URL.Path,
				sw.status,
				duration.Round(time.Microsecond),
				sw.bytes,
			)
			return
		}

		// El "bold" en 5xx hace que los errores destaquen en un scroll rápido.
		bold := ""
		if sw.status >= 500 {
			bold = colorBold
		}

		fmt.Printf(
			"%s%s%s %s%-6s%s %s %s%s%3d%s %s%s%s %s%dB%s\n",
			colorGray, start.Format("15:04:05"), colorReset,
			methodColor, r.Method, colorReset,
			r.URL.Path,
			bold, statusColor, sw.status, colorReset,
			colorGray, duration.Round(time.Microsecond).String(), colorReset,
			colorGray, sw.bytes, colorReset,
		)
	})
}

func pickStatusColor(status int) string {
	switch {
	case status >= 500:
		return colorRed
	case status >= 400:
		return colorYellow
	case status >= 300:
		return colorCyan
	case status >= 200:
		return colorGreen
	default:
		return colorMagenta // 1xx informational
	}
}
