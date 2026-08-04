package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	firebase "firebase.google.com/go/v4"
	firebaseauth "firebase.google.com/go/v4/auth"
)

var (
	ErrInvalidToken = errors.New("invalid identity token")
	ErrRevokedToken = errors.New("identity token has been revoked")
)

type Identity struct {
	UID           string
	Email         string
	DisplayName   string
	EmailVerified bool
}

type TokenVerifier interface {
	Verify(context.Context, string) (Identity, error)
	RevokeSessions(context.Context, string) error
}

type FirebaseVerifier struct{ client *firebaseauth.Client }

func NewFirebaseVerifier(ctx context.Context, projectID string) (*FirebaseVerifier, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("FIREBASE_PROJECT_ID is required when fake auth is disabled")
	}
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: projectID})
	if err != nil {
		return nil, fmt.Errorf("initialize Firebase app: %w", err)
	}
	client, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("initialize Firebase auth: %w", err)
	}
	return &FirebaseVerifier{client: client}, nil
}

func (v *FirebaseVerifier) Verify(ctx context.Context, raw string) (Identity, error) {
	token, err := v.client.VerifyIDTokenAndCheckRevoked(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	email, _ := token.Claims["email"].(string)
	name, _ := token.Claims["name"].(string)
	verified, _ := token.Claims["email_verified"].(bool)
	if token.UID == "" || email == "" {
		return Identity{}, ErrInvalidToken
	}
	return Identity{UID: token.UID, Email: strings.ToLower(strings.TrimSpace(email)), DisplayName: name, EmailVerified: verified}, nil
}

func (v *FirebaseVerifier) RevokeSessions(ctx context.Context, uid string) error {
	if err := v.client.RevokeRefreshTokens(ctx, uid); err != nil {
		return fmt.Errorf("revoke Firebase refresh tokens: %w", err)
	}
	return nil
}

// DevelopmentVerifier accepts only dev:<email> or dev:<email>:<display-name> tokens.
// Its constructor must only be selected after configuration has rejected production use.
type DevelopmentVerifier struct{}

func (DevelopmentVerifier) Verify(_ context.Context, raw string) (Identity, error) {
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) < 2 || parts[0] != "dev" || !strings.Contains(parts[1], "@") {
		return Identity{}, ErrInvalidToken
	}
	email := strings.ToLower(strings.TrimSpace(parts[1]))
	if email == "" {
		return Identity{}, ErrInvalidToken
	}
	sum := sha256.Sum256([]byte(email))
	name := strings.Split(email, "@")[0]
	if len(parts) == 3 && strings.TrimSpace(parts[2]) != "" {
		name = strings.TrimSpace(parts[2])
	}
	return Identity{UID: "dev-" + hex.EncodeToString(sum[:16]), Email: email, DisplayName: name, EmailVerified: true}, nil
}

func (DevelopmentVerifier) RevokeSessions(context.Context, string) error { return nil }
