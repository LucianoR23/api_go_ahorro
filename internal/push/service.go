package push

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"

	"github.com/google/uuid"
	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// Config: VAPID keys + subject. Generadas una vez con `webpush-go` CLI o con
// el helper cmd/vapidgen. Se inyectan via env en Coolify.
type Config struct {
	PublicKey  string
	PrivateKey string
	Subject    string // "mailto:admin@ahorra.app"
}

// Enabled indica si hay VAPID keys configuradas. Si no, el service no envía
// (pero los endpoints de subscribe siguen funcionando: se guarda el token y
// cuando se configure VAPID empezarán a recibir notifs).
func (c Config) Enabled() bool {
	return strings.TrimSpace(c.PublicKey) != "" &&
		strings.TrimSpace(c.PrivateKey) != "" &&
		strings.TrimSpace(c.Subject) != ""
}

// Payload es lo que va en el body de la notificación. El Service Worker del
// frontend lo parsea y lo pasa a showNotification.
//
// URL: deep-link para el notificationclick handler (ej: "/expenses/{id}").
// Tag: agrupa notifs (un mismo tag reemplaza la anterior).
type Payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"`
	Tag   string `json:"tag,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

// Service envía push notifications. Busca las subs del user, firma con VAPID
// y dispara al provider (FCM/Mozilla/Apple según endpoint).
//
// Nil-safe: Send sobre un Service nil no hace nada (útil para tests y para
// services que no fueron cableados con push).
type Service struct {
	repo   *Repository
	cfg    Config
	logger *slog.Logger
}

func NewService(repo *Repository, cfg Config, logger *slog.Logger) *Service {
	return &Service{repo: repo, cfg: cfg, logger: logger}
}

// PublicKey devuelve la VAPID public key para que el endpoint GET la exponga
// al frontend.
func (s *Service) PublicKey() string {
	if s == nil {
		return ""
	}
	return s.cfg.PublicKey
}

// Subscribe persiste la subscription del browser. Valida shape mínimo.
type SubscribeInput struct {
	UserID    uuid.UUID
	Endpoint  string
	P256dh    string
	Auth      string
	UserAgent string
}

func (s *Service) Subscribe(ctx context.Context, in SubscribeInput) (Subscription, error) {
	in.Endpoint = strings.TrimSpace(in.Endpoint)
	in.P256dh = strings.TrimSpace(in.P256dh)
	in.Auth = strings.TrimSpace(in.Auth)
	if in.Endpoint == "" {
		return Subscription{}, domain.NewValidationError("endpoint", "requerido")
	}
	if in.P256dh == "" {
		return Subscription{}, domain.NewValidationError("keys.p256dh", "requerido")
	}
	if in.Auth == "" {
		return Subscription{}, domain.NewValidationError("keys.auth", "requerido")
	}
	return s.repo.Upsert(ctx, in.UserID, in.Endpoint, in.P256dh, in.Auth, in.UserAgent)
}

// Unsubscribe borra la sub. Idempotente: si no existe, devuelve nil.
func (s *Service) Unsubscribe(ctx context.Context, userID uuid.UUID, endpoint string) error {
	return s.repo.DeleteByEndpoint(ctx, userID, endpoint)
}

// NotifyUsers envía payload a todas las subs de los users dados. Fire-and-forget:
// lanza una goroutine que no bloquea al caller. Si falla, loguea y sigue.
//
// Deduplica userIDs. Si Service es nil o no hay VAPID configurado, no hace nada.
func (s *Service) NotifyUsers(ctx context.Context, userIDs []uuid.UUID, payload Payload) {
	if s == nil {
		return
	}
	if !s.cfg.Enabled() {
		s.logger.DebugContext(ctx, "push: VAPID no configurado, skip", "users", len(userIDs))
		return
	}
	if len(userIDs) == 0 {
		return
	}

	// Dedupe.
	seen := make(map[uuid.UUID]struct{}, len(userIDs))
	uniq := make([]uuid.UUID, 0, len(userIDs))
	for _, id := range userIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}

	// Detach del request ctx para que no se cancele al responder HTTP.
	// Usamos un ctx nuevo con logger info si lo hubiera.
	bg := context.Background()

	go s.deliver(bg, uniq, payload)
}

func (s *Service) deliver(ctx context.Context, userIDs []uuid.UUID, payload Payload) {
	body, err := json.Marshal(payload)
	if err != nil {
		s.logger.ErrorContext(ctx, "push: marshal payload", "error", err)
		return
	}

	opts := &webpush.Options{
		Subscriber:      s.cfg.Subject,
		VAPIDPublicKey:  s.cfg.PublicKey,
		VAPIDPrivateKey: s.cfg.PrivateKey,
		TTL:             86400, // 24h; si el device está offline, descarta tras eso.
	}

	var wg sync.WaitGroup
	for _, uid := range userIDs {
		subs, err := s.repo.ListByUser(ctx, uid)
		if err != nil {
			s.logger.WarnContext(ctx, "push: list subs", "error", err, "user_id", uid)
			continue
		}
		for _, sub := range subs {
			wg.Add(1)
			go func(sub Subscription) {
				defer wg.Done()
				s.sendOne(ctx, sub, body, opts)
			}(sub)
		}
	}
	wg.Wait()
}

func (s *Service) sendOne(ctx context.Context, sub Subscription, body []byte, opts *webpush.Options) {
	ws := &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.P256dh,
			Auth:   sub.Auth,
		},
	}
	resp, err := webpush.SendNotification(body, ws, opts)
	if err != nil {
		s.logger.WarnContext(ctx, "push: send falló",
			"error", err, "user_id", sub.UserID, "endpoint", truncate(sub.Endpoint, 60))
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	// 404 Gone / 410 Gone → la sub ya no es válida, limpiar DB.
	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		if err := s.repo.DeleteByEndpointRaw(ctx, sub.Endpoint); err != nil {
			s.logger.WarnContext(ctx, "push: delete stale sub", "error", err)
		}
		return
	}
	if resp.StatusCode >= 400 {
		s.logger.WarnContext(ctx, "push: provider rechazó",
			"status", resp.StatusCode, "user_id", sub.UserID, "endpoint", truncate(sub.Endpoint, 60))
		return
	}
	_ = s.repo.Touch(ctx, sub.Endpoint)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

