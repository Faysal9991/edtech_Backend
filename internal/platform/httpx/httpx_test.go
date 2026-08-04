package httpx

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"ok","amount":999}`))
	response := httptest.NewRecorder()
	var body struct {
		Name string `json:"name"`
	}
	if err := DecodeJSON(response, request, &body); err == nil {
		t.Fatal("unknown payment amount field must be rejected")
	}
}
