package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// TTLs: tokens cortos + refresh rotativo.
// Access token: 15 min — suficiente para que la PWA opere sin llamar a
// refresh en cada request, pero corto como para que si se filtra dure poco.
// Refresh token: 7 días — balancea UX (no re-loguear diario) con seguridad.
const (
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 7 * 24 * time.Hour
)

// TokenType distingue access de refresh en el claim "typ" del JWT.
// Así un refresh firmado nunca sirve como access token ni viceversa,
// incluso si alguien se confunde de secret.
type TokenType string

const (
	TokenTypeAccess  TokenType = "access"
	TokenTypeRefresh TokenType = "refresh"
)

// Claims es el payload del JWT que emitimos.
// - Subject: user_id en formato UUID string
// - Typ:     access | refresh (para que un refresh no pueda usarse
//            como access aunque se mezclen los secrets)
//
// No incluimos email/name acá: los claims son públicos (están en el
// token), y esos datos los resuelve el handler desde DB si los necesita.
type Claims struct {
	Typ TokenType `json:"typ"`
	jwt.RegisteredClaims
}

// TokenIssuer emite y valida tokens. Se construye una sola vez en arranque
// con los secrets del Config y se comparte (thread-safe: los secrets son
// []byte inmutables).
type TokenIssuer struct {
	accessSecret  []byte
	refreshSecret []byte
	issuer        string
}

// NewTokenIssuer valida que los secrets no estén vacíos y devuelve un
// issuer listo para usar. Los secrets ya fueron validados en config
// (longitud mínima 32), acá solo chequeamos no-vacío por defensa.
func NewTokenIssuer(accessSecret, refreshSecret string) (*TokenIssuer, error) {
	if accessSecret == "" || refreshSecret == "" {
		return nil, errors.New("auth: secrets no pueden estar vacíos")
	}
	if accessSecret == refreshSecret {
		return nil, errors.New("auth: JWT_SECRET y JWT_REFRESH_SECRET deben ser distintos")
	}
	return &TokenIssuer{
		accessSecret:  []byte(accessSecret),
		refreshSecret: []byte(refreshSecret),
		issuer:        "ahorra-api",
	}, nil
}

// IssueAccessToken firma un access token con el secret correspondiente.
func (ti *TokenIssuer) IssueAccessToken(userID uuid.UUID) (string, time.Time, error) {
	return ti.issue(userID, TokenTypeAccess, AccessTokenTTL, ti.accessSecret)
}

// IssueRefreshToken firma un refresh token con su secret separado.
func (ti *TokenIssuer) IssueRefreshToken(userID uuid.UUID) (string, time.Time, error) {
	return ti.issue(userID, TokenTypeRefresh, RefreshTokenTTL, ti.refreshSecret)
}

func (ti *TokenIssuer) issue(userID uuid.UUID, typ TokenType, ttl time.Duration, secret []byte) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(ttl)

	claims := Claims{
		Typ: typ,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    ti.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	// HS256: HMAC-SHA256 con secret simétrico. Para un monolito con 1-2
	// instancias es lo correcto: no necesitamos keypair. Si alguna vez
	// hay que verificar tokens desde un servicio que no debería tener
	// el secret de emisión, migramos a RS256 (asimétrico).
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: firmar token: %w", err)
	}
	return signed, expiresAt, nil
}

// ParseAccessToken valida firma + expiración + tipo y devuelve el user_id.
// Usa el secret de access. Si el token es de tipo refresh, falla.
func (ti *TokenIssuer) ParseAccessToken(tokenStr string) (uuid.UUID, error) {
	return ti.parse(tokenStr, TokenTypeAccess, ti.accessSecret)
}

// ParseRefreshToken es el espejo para el flujo de rotación del refresh.
func (ti *TokenIssuer) ParseRefreshToken(tokenStr string) (uuid.UUID, error) {
	return ti.parse(tokenStr, TokenTypeRefresh, ti.refreshSecret)
}

func (ti *TokenIssuer) parse(tokenStr string, expectedType TokenType, secret []byte) (uuid.UUID, error) {
	claims := &Claims{}
	// El keyFunc valida también el algoritmo — rechazamos cualquier cosa
	// que no sea HMAC (evita el famoso ataque "alg: none" y RS/HS confusion).
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("algoritmo inesperado: %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		// Cualquier error de parse/verify → Unauthorized a nivel dominio.
		// El detalle se puede loggear pero no exponer al cliente.
		return uuid.Nil, fmt.Errorf("%w: %v", domain.ErrUnauthorized, err)
	}
	if !token.Valid {
		return uuid.Nil, domain.ErrUnauthorized
	}
	if claims.Typ != expectedType {
		return uuid.Nil, fmt.Errorf("%w: tipo de token incorrecto", domain.ErrUnauthorized)
	}

	userID, err := uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: subject inválido", domain.ErrUnauthorized)
	}
	return userID, nil
}
