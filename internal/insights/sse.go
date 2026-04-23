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

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

// sseAuth: EventSource del browser no soporta headers custom (no se le puede
// poner Authorization: Bearer), entonces aceptamos el access token vía query
// param `?access_token=`. El token sigue siendo el mismo JWT corto (15min)
// que ya emite /auth/login — no agregamos un canal nuevo de credenciales.
// HTTPS obligatorio en prod (lo agarra el reverse proxy de Coolify).
type sseAuth interface {
	ParseAccessToken(token string) (uuid.UUID, error)
}

// memberCheckFn: validación de membresía. Inyectada desde main para no
// importar households acá.
type memberCheckFn func(ctx context.Context, householdID, userID uuid.UUID) (bool, error)

// SSEHandler: subscribe-only. NO usa los middlewares de chi para auth porque
// el token viene en query param, no en header.
type SSEHandler struct {
	hub         *Hub
	repo        *Repository
	tokens      sseAuth
	memberCheck memberCheckFn
}

func NewSSEHandler(hub *Hub, repo *Repository, tokens sseAuth, memberCheck memberCheckFn) *SSEHandler {
	return &SSEHandler{hub: hub, repo: repo, tokens: tokens, memberCheck: memberCheck}
}

func (h *SSEHandler) Mount(r chi.Router) {
	r.Get("/insights/stream", h.Stream)
}

// Stream: SSE long-lived. Eventos:
//   * insight.created → {insight: insightDTO}
//   * (línea ": ping") cada 25s para evitar que reverse proxies corten conn idle.
//
// Query params:
//   ?access_token=<JWT>   obligatorio (Bearer del access token)
//   ?householdId=<uuid>   obligatorio
func (h *SSEHandler) Stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming no soportado", http.StatusInternalServerError)
		return
	}

	token := strings.TrimSpace(r.URL.Query().Get("access_token"))
	if token == "" {
		if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
			token = strings.TrimPrefix(hdr, "Bearer ")
		}
	}
	if token == "" {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized", "message": "missing access_token"})
		return
	}
	userID, err := h.tokens.ParseAccessToken(token)
	if err != nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized", "message": "invalid token"})
		return
	}

	hhStr := strings.TrimSpace(r.URL.Query().Get("householdId"))
	if hhStr == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]string{"code": "validation_error", "message": "missing householdId", "field": "householdId"})
		return
	}
	householdID, err := uuid.Parse(hhStr)
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]string{"code": "validation_error", "message": "invalid householdId", "field": "householdId"})
		return
	}

	isMember, err := h.memberCheck(r.Context(), householdID, userID)
	if err != nil {
		httpx.WriteJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal_error", "message": "membership check failed"})
		return
	}
	if !isMember {
		httpx.WriteJSON(w, http.StatusForbidden, map[string]string{"code": "forbidden", "message": "not a member of household"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// X-Accel-Buffering=no: deshabilita buffering de nginx para que cada flush
	// llegue al browser. Coolify usa Traefik por default — no bufferea SSE,
	// pero no molesta tenerlo por si alguien mete nginx delante.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sub := h.hub.Subscribe(householdID, userID)
	defer sub.Close()

	// Hello inmediato — confirma al cliente que el stream quedó establecido.
	if _, err := fmt.Fprint(w, "event: ready\ndata: {\"ok\":true}\n\n"); err != nil {
		return
	}
	flusher.Flush()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
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
			if _, err := fmt.Fprintf(w, "event: insight.created\ndata: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
