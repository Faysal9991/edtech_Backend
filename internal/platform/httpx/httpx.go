package httpx

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	api "github.com/Faysal9991/edtech_Backend/internal/api"
	"github.com/google/uuid"
)

const maxJSONBody = int64(1 << 20)

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status != http.StatusNoContent {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func Problem(w http.ResponseWriter, r *http.Request, status int, title, detail string, fields ...api.FieldError) {
	w.Header().Set("Content-Type", "application/problem+json")
	p := api.Problem{Type: "about:blank", Title: title, Status: status}
	if detail != "" {
		p.Detail = &detail
	}
	if rid := RequestID(r.Context()); rid != "" {
		p.RequestId = &rid
	}
	if len(fields) > 0 {
		p.Errors = &fields
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

type Cursor struct {
	Time time.Time `json:"t"`
	ID   uuid.UUID `json:"id"`
}

func EncodeCursor(t time.Time, id uuid.UUID) string {
	b, _ := json.Marshal(Cursor{Time: t.UTC(), ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func ParseCursor(raw string) (*Cursor, error) {
	if raw == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New("invalid cursor encoding")
	}
	var c Cursor
	if err := json.Unmarshal(b, &c); err != nil || c.Time.IsZero() || c.ID == uuid.Nil {
		return nil, errors.New("invalid cursor")
	}
	return &c, nil
}

func PageSize(r *http.Request) (int32, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 25, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 100 {
		return 0, errors.New("limit must be between 1 and 100")
	}
	return int32(n), nil
}

func UUIDParam(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errors.New("invalid UUID")
	}
	return id, nil
}
func NormalizeSlug(v string) string { return strings.Trim(strings.ToLower(strings.TrimSpace(v)), "-") }
