package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

type requestIDKey struct{}

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)
	return value
}

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" || len(requestID) > 128 {
			requestID = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID)))
	})
}

func SecureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if r.URL.Path == "/docs" {
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src https://unpkg.com 'unsafe-inline'; style-src https://unpkg.com 'unsafe-inline'; img-src data:; connect-src 'self'; frame-ancestors 'none'")
		} else {
			w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		}
		next.ServeHTTP(w, r)
	})
}

func CORS(origins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[origin] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Organization-ID, X-Request-ID")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (w *responseRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func Logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			logger.InfoContext(r.Context(), "http request", "request_id", RequestID(r.Context()), "method", r.Method, "path", safeLogPath(r.URL.Path), "status", rec.status, "duration_ms", time.Since(started).Milliseconds(), "remote_addr", r.RemoteAddr)
		})
	}
}

func safeLogPath(path string) string {
	// The device-removal endpoint carries the provider token as a path segment
	// for API compatibility. Never write that secret token to access logs.
	if strings.HasPrefix(path, "/api/v1/devices/") {
		return "/api/v1/devices/:token"
	}
	return path
}

func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.ErrorContext(r.Context(), "panic recovered", "request_id", RequestID(r.Context()), "error", recovered, "stack", string(debug.Stack()))
					Problem(w, r, http.StatusInternalServerError, "Internal Server Error", "the request could not be completed")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

var requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "lms_http_request_duration_seconds", Help: "HTTP request latency."}, []string{"method", "route", "status"})
var requestTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "lms_http_requests_total", Help: "HTTP requests."}, []string{"method", "route", "status"})

func RegisterMetrics(reg prometheus.Registerer) { reg.MustRegister(requestDuration, requestTotal) }

// ObserveMetrics records one request using a normalized router pattern rather
// than the raw URL, preventing unbounded labels from path identifiers.
func ObserveMetrics(method, route string, status int, duration time.Duration) {
	if route == "" {
		route = "unknown"
	}
	statusText := http.StatusText(status)
	if statusText == "" {
		statusText = "unknown"
	}
	requestTotal.WithLabelValues(method, route, statusText).Inc()
	requestDuration.WithLabelValues(method, route, statusText).Observe(duration.Seconds())
}

func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unknown"
		}
		status := http.StatusText(rec.status)
		requestTotal.WithLabelValues(r.Method, route, status).Inc()
		requestDuration.WithLabelValues(r.Method, route, status).Observe(time.Since(started).Seconds())
	})
}
