package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Faysal9991/edtech_Backend/internal/platform/cache"
	platformid "github.com/Faysal9991/edtech_Backend/internal/platform/id"
)

type rejectingVerifier struct{}

type denyLimiter struct{}

func (denyLimiter) Allow(context.Context, string, int, time.Duration) (bool, error) {
	return false, nil
}

func (rejectingVerifier) Verify(context.Context, string) (Identity, error) {
	return Identity{}, errors.New("rejected")
}
func (rejectingVerifier) RevokeSessions(context.Context, string) error { return nil }
func TestAuthenticationRejectsMissingToken(t *testing.T) {
	middleware := NewMiddleware(rejectingVerifier{}, nil, platformid.Secure{}, cache.AllowAllLimiter{}, 10, time.Minute)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	middleware.Authenticate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("next handler must not run") })).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", response.Code)
	}
}
func TestAuthenticationRejectsInvalidToken(t *testing.T) {
	middleware := NewMiddleware(rejectingVerifier{}, nil, platformid.Secure{}, cache.AllowAllLimiter{}, 10, time.Minute)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer invalid")
	response := httptest.NewRecorder()
	middleware.Authenticate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("next handler must not run") })).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", response.Code)
	}
}

func TestAuthenticationRateLimitsBeforeTokenVerification(t *testing.T) {
	middleware := NewMiddleware(rejectingVerifier{}, nil, platformid.Secure{}, denyLimiter{}, 10, time.Minute)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer invalid")
	response := httptest.NewRecorder()
	middleware.Authenticate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("next handler must not run") })).ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d", response.Code)
	}
}

func TestClientIPDoesNotTrustSpoofableForwardedHeader(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.50")
	if got := clientIP(request); got != "192.0.2.10" {
		t.Fatalf("got spoofable client address %q", got)
	}
}
