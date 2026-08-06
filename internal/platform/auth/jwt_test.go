package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/neoscoder/lms-service/internal/platform/config"
)

func TestJWTManagerValidatesClaimsAlgorithmAndKeyID(t *testing.T) {
	cfg := config.Auth{Issuer: "issuer", Audience: "audience", KeyID: "primary", SigningKey: "01234567890123456789012345678901", AccessTTL: 15 * time.Minute}
	manager := NewJWTManager(cfg)
	userID := uuid.New()
	raw, _, err := manager.IssueAccess(userID, []string{"student"})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := manager.ParseAccess(raw)
	if err != nil || claims.Subject != userID.String() || len(claims.Roles) != 1 {
		t.Fatalf("valid token rejected: claims=%+v err=%v", claims, err)
	}

	wrongKeyID := jwt.NewWithClaims(jwt.SigningMethodHS256, AccessClaims{TokenType: "access", RegisteredClaims: jwt.RegisteredClaims{Issuer: cfg.Issuer, Subject: userID.String(), Audience: jwt.ClaimStrings{cfg.Audience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}})
	wrongKeyID.Header["kid"] = "retired"
	badRaw, err := wrongKeyID.SignedString([]byte(cfg.SigningKey))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.ParseAccess(badRaw); err == nil {
		t.Fatal("token with an unknown key id was accepted")
	}

	none := jwt.NewWithClaims(jwt.SigningMethodNone, AccessClaims{TokenType: "access", RegisteredClaims: jwt.RegisteredClaims{Issuer: cfg.Issuer, Subject: userID.String(), Audience: jwt.ClaimStrings{cfg.Audience}, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}})
	none.Header["kid"] = cfg.KeyID
	noneRaw, err := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.ParseAccess(noneRaw); err == nil {
		t.Fatal("unsigned token was accepted")
	}
}
