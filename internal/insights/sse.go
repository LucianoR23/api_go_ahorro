package insights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/httpx"
)

// memberCheckFn: validación de membresía. Inyectada desde main para no
// importar households acá.
type memberCheckFn func(ctx context.Context, householdID, userID uuid.UUID) (bool, error)

// SSEHandler: subscribe-only stream of new insights. EventSource cannot send
// an Authorization header, so the stream is opened with a single-use ticket
// from POST /insights/stream-ticket (plan 0.9) instead of the access token in
// the URL. Mounted outside the 30 s timeout group: it lives as long as the
// page is open.
type SSEHandler struct {
	hub         *Hub
	repo        *Repository
	tickets     *TicketStore
	memberCheck memberCheckFn
}

func NewSSEHandler(hub *Hub, repo *Repository, tickets *TicketStore, memberCheck memberCheckFn) *SSEHandler {
	return &SSEHandler{hub: hub, repo: repo, tickets: tickets, memberCheck: memberCheck}
}

func (h *SSEHandler) Mount(r chi.Router) {
	r.Get("/insights/stream", h.Stream)
}

// pingEvery keeps proxies (Cloudflare, Traefik) from closing an idle stream.
const pingEvery = 25 * time.Second

// Stream: SSE long-lived. Eventos:
//   - ready            -> stream established
//   - insight.created  -> {insight: insightDTO}
//   - ": ping" comment every 25 s
//
// Query: ?ticket=<single-use ticket> (required). The ticket carries the user
// and the household; membership is checked again here.
func (h *SSEHandler) Stream(w http.ResponseWriter, r *http.Request) {
	ticket := strings.TrimSpace(r.URL.Query().Get("ticket"))
	if ticket == "" {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized", "message": "falta el ticket del stream"})
		return
	}
	userID, householdID, ok := h.tickets.Redeem(ticket)
	if !ok {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized", "message": "ticket inválido o vencido"})
		return
	}
	isMember, err := h.memberCheck(r.Context(), householdID, userID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal", "message": "error interno"})
		return
	}
	if !isMember {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]string{"code": "forbidden", "message": "operación no permitida"})
		return
	}

	// ResponseController reaches the connection through wrapping writers (the
	// request loggers): Flush works in dev too (it answered 500 there), and
	// the server's 30 s WriteTimeout is pushed forward on every write.
	rc := http.NewResponseController(w)
	extend := func() { _ = rc.SetWriteDeadline(time.Now().Add(pingEvery + 15*time.Second)) }
	flush := func() bool { return rc.Flush() == nil }
	extend()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// X-Accel-Buffering=no: deshabilita buffering de nginx para que cada flush
	// llegue al browser. Coolify usa Traefik por default — no bufferea SSE,
	// pero no molesta tenerlo por si alguien mete nginx delante.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if !flush() {
		return
	}

	sub := h.hub.Subscribe(householdID, userID)
	defer sub.Close()

	// Hello inmediato — confirma al cliente que el stream quedó establecido.
	if _, err := fmt.Fprint(w, "event: ready\ndata: {\"ok\":true}\n\n"); err != nil || !flush() {
		return
	}

	ping := time.NewTicker(pingEvery)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			extend()
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || !flush() {
				return
			}
		case ev, ok := <-sub.Ch:
			if !ok {
				return
			}
			ins, err := h.repo.GetByID(r.Context(), ev.InsightID)
			if err != nil {
				if errors.Is(err, domain.ErrNotFound) {
					continue
				}
				continue
			}
			payload, err := json.Marshal(map[string]any{"insight": toDTO(ins)})
			if err != nil {
				continue
			}
			extend()
			if _, err := fmt.Fprintf(w, "event: insight.created\ndata: %s\n\n", payload); err != nil || !flush() {
				return
			}
		}
	}
}
