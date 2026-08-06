package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/neoscoder/lms-service/internal/platform/config"
)

var ErrInvalidToken = errors.New("invalid access token")

type AccessClaims struct {
	TokenType string   `json:"typ"`
	Roles     []string `json:"roles,omitempty"`
	jwt.RegisteredClaims
}

// JWTManager validates issuer, audience, lifetime, algorithm, and key id. The
// key-id check makes adding a key ring later a compatible change.
type JWTManager struct {
	issuer, audience, keyID string
	key                     []byte
	ttl                     time.Duration
	now                     func() time.Time
}

func NewJWTManager(cfg config.Auth) *JWTManager {
	return &JWTManager{issuer: cfg.Issuer, audience: cfg.Audience, keyID: cfg.KeyID, key: []byte(cfg.SigningKey), ttl: cfg.AccessTTL, now: time.Now}
}

func (m *JWTManager) IssueAccess(userID uuid.UUID, roles []string) (string, time.Time, error) {
	now := m.now().UTC()
	expires := now.Add(m.ttl)
	claims := AccessClaims{TokenType: "access", Roles: append([]string(nil), roles...), RegisteredClaims: jwt.RegisteredClaims{
		Issuer: m.issuer, Subject: userID.String(), Audience: jwt.ClaimStrings{m.audience},
		ExpiresAt: jwt.NewNumericDate(expires), NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)),
		IssuedAt: jwt.NewNumericDate(now), ID: uuid.NewString(),
	}}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["kid"] = m.keyID
	signed, err := token.SignedString(m.key)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, expires, nil
}

func (m *JWTManager) ParseAccess(raw string) (AccessClaims, error) {
	claims := AccessClaims{}
	token, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 || token.Header["kid"] != m.keyID {
			return nil, ErrInvalidToken
		}
		return m.key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(m.issuer), jwt.WithAudience(m.audience), jwt.WithLeeway(15*time.Second), jwt.WithTimeFunc(m.now))
	if err != nil || token == nil || !token.Valid || claims.TokenType != "access" {
		return AccessClaims{}, ErrInvalidToken
	}
	if _, err := uuid.Parse(claims.Subject); err != nil {
		return AccessClaims{}, ErrInvalidToken
	}
	return claims, nil
}
