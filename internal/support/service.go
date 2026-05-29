// Package support es un proxy stateless hacia el servicio centralizado
// Lemy Support (https://soporte.lemydev.com). El front de Ahorra no puede
// hablar con Soporte directamente porque la API key es server-side only;
// tampoco hay un backend Next donde meterla. Por eso el proxy vive acá, en
// el API Go: valida el JWT propio, inyecta X-App-Key + identidad del
// reporter, y pega a Soporte por HTTP crudo (ver integracion.md §13).
//
// No usa el SDK @lemydev/client-ts (es TypeScript). No tiene tablas ni
// repo propio: lo único que lee de la DB es el email del reporter (vía
// users), porque el JWT solo trae el userID.
package support

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// UserEmailLookup resuelve el email del reporter. El JWT solo guarda el
// userID (Subject), pero Soporte exige X-Reporter-Email (obligatorio y
// validado con formato). users.Repository satisface esta interfaz.
type UserEmailLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, error)
}

// Config concentra lo que el módulo necesita del entorno. Si APIKey está
// vacía el módulo queda deshabilitado (los endpoints responden 503), igual
// que VAPID/Resend — así el API levanta en dev sin credenciales.
type Config struct {
	BaseURL string
	APIKey  string
}

func (c Config) Enabled() bool { return c.APIKey != "" }

// Service hace el proxy. httpClient tiene un timeout amplio porque los
// uploads de video (≤20MB) pueden tardar; el servicio Soporte maneja hasta
// ~3min del lado del upload.
type Service struct {
	cfg        Config
	httpClient *http.Client
	users      UserEmailLookup
	logger     *slog.Logger
}

func NewService(cfg Config, users UserEmailLookup, logger *slog.Logger) *Service {
	return &Service{
		cfg:        cfg,
		users:      users,
		httpClient: &http.Client{Timeout: 3 * time.Minute},
		logger:     logger,
	}
}

func (s *Service) Enabled() bool { return s.cfg.Enabled() }

// identity son los datos del reporter para los headers X-Reporter-*.
type identity struct {
	userID string
	email  string
}

// resolveIdentity arma la identidad del reporter a partir del userID del
// JWT, completando el email desde la DB.
func (s *Service) resolveIdentity(ctx context.Context, userID uuid.UUID) (identity, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return identity{}, err
	}
	return identity{userID: userID.String(), email: u.Email}, nil
}
