package httpx

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	request := httptest.NewRequestWithContext(context.Background(), "POST", "/", strings.NewReader(`{"name":"ok","amount":999}`))
	response := httptest.NewRecorder()
	var body struct {
		Name string `json:"name"`
	}
	if err := DecodeJSON(response, request, &body); err == nil {
		t.Fatal("unknown payment amount field must be rejected")
	}
}

func TestLoggingRedactsDeviceTokenPath(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/devices/sensitive-fcm-registration-token", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if strings.Contains(output.String(), "sensitive-fcm-registration-token") {
		t.Fatal("device token leaked into request log")
	}
	if !strings.Contains(output.String(), "/api/v1/devices/:token") {
		t.Fatal("expected normalized device route in request log")
	}
}
