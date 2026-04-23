package insights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Event: payload que viaja del LISTEN al SSE handler.
//   * UserID nil  → insight household-scoped (visible para cualquier miembro).
//   * UserID set  → solo para ese user.
// Los suscriptores filtran localmente.
type Event struct {
	InsightID   uuid.UUID  `json:"insightId"`
	HouseholdID uuid.UUID  `json:"householdId"`
	UserID      *uuid.UUID `json:"userId,omitempty"`
	InsightType string     `json:"insightType"`
}

// Subscription: lo que el SSE handler obtiene del Hub.
type Subscription struct {
	HouseholdID uuid.UUID
	UserID      uuid.UUID
	Ch          chan Event
	close       func()
}

// Close: idempotente, libera el slot del Hub.
func (s *Subscription) Close() { s.close() }

// Hub: registry in-process de suscriptores. El Listener publica eventos vía
// Publish(), los SSE handlers leen de Ch. Buffer grande pero finito: si el
// consumer se atrasa, dropeamos el evento más viejo (mejor que bloquear todo
// el listener). Para multi-instancia, este Hub se sigue alimentando del mismo
// canal de Postgres, así que cada réplica entrega a sus propios clientes.
type Hub struct {
	mu      sync.RWMutex
	nextID  atomic.Uint64
	clients map[uint64]*Subscription
	logger  *slog.Logger
}

func NewHub(logger *slog.Logger) *Hub {
	return &Hub{clients: map[uint64]*Subscription{}, logger: logger}
}

// Subscribe registra a un cliente para recibir eventos. El caller DEBE llamar
// sub.Close() al cortar (defer en el SSE handler).
func (h *Hub) Subscribe(householdID, userID uuid.UUID) *Subscription {
	id := h.nextID.Add(1)
	sub := &Subscription{
		HouseholdID: householdID,
		UserID:      userID,
		Ch:          make(chan Event, 16),
	}
	sub.close = sync.OnceFunc(func() {
		h.mu.Lock()
		delete(h.clients, id)
		h.mu.Unlock()
		close(sub.Ch)
	})
	h.mu.Lock()
	h.clients[id] = sub
	h.mu.Unlock()
	return sub
}

// Publish entrega el evento a todos los suscriptores que apliquen
// (household match + user filter). Non-blocking: si un canal está lleno,
// dropeamos para ese cliente y logueamos.
func (h *Hub) Publish(ev Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, sub := range h.clients {
		if sub.HouseholdID != ev.HouseholdID {
			continue
		}
		if ev.UserID != nil && *ev.UserID != sub.UserID {
			continue
		}
		select {
		case sub.Ch <- ev:
		default:
			if h.logger != nil {
				h.logger.Warn("insights hub: subscriber lento, evento dropeado",
					"userId", sub.UserID.String(), "insightId", ev.InsightID.String())
			}
		}
	}
}

// ===================== Postgres LISTEN/NOTIFY =====================

const channelName = "insights_new"

// Notify emite un pg_notify con el payload del evento. Lo llama el service
// inmediatamente después de un Create exitoso. Pool corto: tomamos una conn,
// hacemos NOTIFY (no requiere transacción), liberamos. Si falla, logueamos
// y seguimos — el insight ya quedó persistido, lo peor que pasa es que la
// UI tarde 60s en verlo (polling de fallback).
func Notify(ctx context.Context, pool *pgxpool.Pool, ev Event) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("insights.Notify marshal: %w", err)
	}
	if _, err := pool.Exec(ctx, "SELECT pg_notify($1, $2)", channelName, string(payload)); err != nil {
		return fmt.Errorf("insights.Notify exec: %w", err)
	}
	return nil
}

// Listener: goroutine de larga vida con una conn dedicada al canal
// `insights_new`. Convierte cada NOTIFY en Hub.Publish. Reconnect automático
// con backoff: si la conn se cae (DB restart, network), reintenta.
type Listener struct {
	pool   *pgxpool.Pool
	hub    *Hub
	logger *slog.Logger
}

func NewListener(pool *pgxpool.Pool, hub *Hub, logger *slog.Logger) *Listener {
	return &Listener{pool: pool, hub: hub, logger: logger}
}

// Start arranca el listener en background. Devuelve un cancel para shutdown
// ordenado (alineado con el patrón de los workers existentes).
func (l *Listener) Start(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	go l.run(ctx)
	return cancel
}

func (l *Listener) run(ctx context.Context) {
	backoff := time.Second
	for {
		if err := l.loopOnce(ctx); err != nil {
			if errors.Is(err, context.Canceled) {
				l.logger.Info("insights listener: detenido")
				return
			}
			l.logger.Warn("insights listener: conn cayó, reintento",
				"error", err, "backoff", backoff.String())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		return
	}
}

func (l *Listener) loopOnce(ctx context.Context) error {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN "+channelName); err != nil {
		return fmt.Errorf("LISTEN: %w", err)
	}
	l.logger.Info("insights listener: escuchando", "channel", channelName)

	for {
		notif, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var ev Event
		if jerr := json.Unmarshal([]byte(notif.Payload), &ev); jerr != nil {
			l.logger.Warn("insights listener: payload inválido",
				"payload", notif.Payload, "error", jerr)
			continue
		}
		l.hub.Publish(ev)
	}
}
