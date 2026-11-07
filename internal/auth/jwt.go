package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

// AccessTokenTTL: 15 min. Long enough for the PWA not to refresh on every
// request, short enough that a leaked access token is useful only briefly.
// The refresh token is not a JWT: it is an opaque server-side session (see
// sessions.go, plan 0.5).
const AccessTokenTTL = 15 * time.Minute

// TokenType distingue access de refresh en el claim "typ" del JWT.
// Así un refresh firmado nunca sirve como access token ni viceversa,
// incluso si alguien se confunde de secret.
type TokenType string

const (
	TokenTypeAccess  TokenType = "access"
	TokenTypeRefresh TokenType = "refresh"
)

// Claims es el payload del JWT que emitimos.
//   - Subject: user_id en formato UUID string
//   - Typ:     access | refresh (para que un refresh no pueda usarse
//     como access aunque se mezclen los secrets)
//
// No incluimos email/name acá: los claims son públicos (están en el
// token), y esos datos los resuelve el handler desde DB si los necesita.
type Claims struct {
	Typ TokenType `json:"typ"`
	// Sid: the server-side session this access token belongs to (plan 0.5),
	// so "sign out the other devices" can keep the current one.
	Sid string `json:"sid,omitempty"`
	jwt.RegisteredClaims
}

// TokenIssuer signs and checks access tokens. Built once at startup and
// shared (the secret is immutable).
type TokenIssuer struct {
	accessSecret []byte
	issuer       string
}

func NewTokenIssuer(accessSecret string) (*TokenIssuer, error) {
	if accessSecret == "" {
		return nil, errors.New("auth: JWT_SECRET no puede estar vacío")
	}
	return &TokenIssuer{accessSecret: []byte(accessSecret), issuer: "valuo-api"}, nil
}

// IssueAccessToken signs an access token for the user and session.
func (ti *TokenIssuer) IssueAccessToken(userID, sessionID uuid.UUID) (string, time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(AccessTokenTTL)
	claims := Claims{
		Typ: TokenTypeAccess,
		Sid: sessionID.String(),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    ti.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	// HS256 with a symmetric secret: right for a monolith that both issues
	// and checks its tokens.
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(ti.accessSecret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: firmar token: %w", err)
	}
	return signed, expiresAt, nil
}

// ParseAccessToken checks signature, expiry and type and returns the user
// and the session (uuid.Nil for tokens issued before sessions existed).
func (ti *TokenIssuer) ParseAccessToken(tokenStr string) (userID, sessionID uuid.UUID, err error) {
	claims, err := ti.parse(tokenStr, TokenTypeAccess, ti.accessSecret)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	userID, err = uuid.Parse(claims.Subject)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("%w: subject inválido", domain.ErrUnauthorized)
	}
	sessionID, _ = uuid.Parse(claims.Sid)
	return userID, sessionID, nil
}

func (ti *TokenIssuer) parse(tokenStr string, expectedType TokenType, secret []byte) (*Claims, error) {
	claims := &Claims{}
	// The keyFunc also checks the algorithm: anything but HMAC is refused
	// (no "alg: none", no RS/HS confusion).
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("algoritmo inesperado: %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		// Details go to logs, never to the client.
		return nil, fmt.Errorf("%w: %v", domain.ErrUnauthorized, err)
	}
	if !token.Valid {
		return nil, domain.ErrUnauthorized
	}
	if claims.Typ != expectedType {
		return nil, fmt.Errorf("%w: tipo de token incorrecto", domain.ErrUnauthorized)
	}
	return claims, nil
}
