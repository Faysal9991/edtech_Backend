package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neoscoder/lms-service/internal/platform/cache"
	"github.com/neoscoder/lms-service/internal/platform/config"
)

func testRouter() http.Handler {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return NewRouter(Dependencies{
		Config:  config.Config{HTTP: config.HTTP{AllowedOrigins: []string{"https://app.example.test"}}, RateLimit: config.RateLimit{Requests: 100, Window: time.Minute}},
		Logger:  logger,
		Limiter: cache.AllowAllLimiter{},
	})
}

func TestPublicHealthAndEmbeddedOpenAPI(t *testing.T) {
	router := testRouter()
	for _, test := range []struct {
		path, contains string
	}{
		{path: "/health/live", contains: `"status":"alive"`},
		{path: "/openapi.yaml", contains: "/api/v1/auth/login:"},
		{path: "/docs", contains: "SwaggerUIBundle"},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.contains) {
			t.Fatalf("GET %s: status=%d body=%q", test.path, response.Code, response.Body.String())
		}
		if response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("X-Request-ID") == "" {
			t.Fatalf("GET %s omitted security/request headers", test.path)
		}
	}
}

func TestProtectedRouteRejectsMissingJWT(t *testing.T) {
	response := httptest.NewRecorder()
	testRouter().ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/student/enrollments", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/problem+json") {
		t.Fatalf("expected problem details, got %q", contentType)
	}
}

func TestUnknownRouteAndMethodUseProblemDetails(t *testing.T) {
	for _, test := range []struct {
		method, path string
		status       int
	}{{http.MethodGet, "/missing", http.StatusNotFound}, {http.MethodDelete, "/health/live", http.StatusMethodNotAllowed}} {
		response := httptest.NewRecorder()
		testRouter().ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), test.method, test.path, nil))
		if response.Code != test.status || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/problem+json") {
			t.Fatalf("%s %s: status=%d type=%q", test.method, test.path, response.Code, response.Header().Get("Content-Type"))
		}
	}
}
