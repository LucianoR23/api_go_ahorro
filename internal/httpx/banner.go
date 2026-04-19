package httpx

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// PrintStartupBanner imprime un cuadro estilo Fiber con info útil al arrancar.
// Útil en dev para ver de un vistazo el estado del server.
//
// En prod igual es barato (se imprime una vez), pero se puede condicionar
// con cfg.Env si molesta en logs estructurados.
func PrintStartupBanner(w io.Writer, env, addr string, router chi.Router) {
	routeCount := 0
	_ = chi.Walk(router, func(method, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		routeCount++
		return nil
	})

	lines := []string{
		"┌────────────────────────────────────────────┐",
		fmt.Sprintf("│  %-42s│", "  Ahorra API"),
		"├────────────────────────────────────────────┤",
		fmt.Sprintf("│  env         %-30s│", env),
		fmt.Sprintf("│  listening   %-30s│", "http://localhost"+addr),
		fmt.Sprintf("│  handlers    %-30d│", routeCount),
		"└────────────────────────────────────────────┘",
	}
	fmt.Fprintln(w, strings.Join(lines, "\n"))
}
