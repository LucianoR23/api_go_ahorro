package httpx

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Health: dos endpoints con semántica distinta.
//
//   /health/live   → proceso vivo. Siempre 200 mientras el binario corre.
//                    Kubernetes / Coolify lo usa para decidir si reiniciar
//                    el container. No chequea dependencias: si Postgres
//                    está caído, el proceso sigue vivo y reiniciarlo no
//                    ayuda — ese evento es problema del otro container.
//
//   /health/ready  → listo para atender tráfico. Chequea que la DB
//                    responda al ping. Si falla, el load balancer saca
//                    esta instancia del pool hasta que vuelva a responder.

func LiveHandler(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ReadyHandler necesita el pool para hacer ping. Lo devuelvemos como
// closure para no meterle estado al paquete httpx.
func ReadyHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			WriteJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "db_unreachable",
			})
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}
