package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrInvalidSignature = errors.New("invalid dummy payment webhook signature")

type IntentInput struct {
	OrderID        string
	AmountMinor    int64
	Currency       string
	IdempotencyKey string
	CustomerEmail  string
}

type Intent struct {
	ID           string
	ClientSecret string
	Status       string
}

type Event struct {
	ID              string
	Type            string
	PaymentIntentID string
	OrderID         string
	Status          string
	AmountMinor     int64
	Currency        string
	Raw             json.RawMessage
}

type Provider interface {
	CreateIntent(context.Context, IntentInput) (Intent, error)
	CancelIntent(context.Context, string) error
	ParseWebhook([]byte, string, time.Time) (Event, error)
}

// DummyProvider is an in-process development payment gateway. It preserves
// production-shaped ordering, webhook verification, and idempotency behavior
// without calling a real processor or handling card information.
type DummyProvider struct {
	intents map[string]Intent
	secret  string
	mu      sync.Mutex
}

func NewDummyProvider(webhookSecret string) *DummyProvider {
	return &DummyProvider{intents: map[string]Intent{}, secret: webhookSecret}
}

func (p *DummyProvider) CreateIntent(_ context.Context, in IntentInput) (Intent, error) {
	if in.OrderID == "" || in.AmountMinor <= 0 || strings.TrimSpace(in.Currency) == "" {
		return Intent{}, errors.New("dummy payment intent requires an order, positive amount, and currency")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.intents[in.OrderID]; ok {
		return existing, nil
	}
	intent := Intent{
		ID:           "dummy_pi_" + in.OrderID,
		ClientSecret: "dummy_client_secret_" + in.OrderID,
		Status:       "requires_confirmation",
	}
	p.intents[in.OrderID] = intent
	return intent, nil
}

func (p *DummyProvider) CancelIntent(_ context.Context, id string) error {
	if id == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for orderID, intent := range p.intents {
		if intent.ID == id {
			intent.Status = "cancelled"
			p.intents[orderID] = intent
			return nil
		}
	}
	return nil
}

func (p *DummyProvider) ParseWebhook(payload []byte, signature string, now time.Time) (Event, error) {
	timestamp, signatures, err := parseSignature(signature)
	if err != nil {
		return Event{}, ErrInvalidSignature
	}
	if delta := now.Sub(time.Unix(timestamp, 0)); delta > 5*time.Minute || delta < -5*time.Minute {
		return Event{}, ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, []byte(p.secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(timestamp, 10) + "."))
	_, _ = mac.Write(payload)
	expected := mac.Sum(nil)
	valid := false
	for _, candidate := range signatures {
		decoded, decodeErr := hex.DecodeString(candidate)
		if decodeErr == nil && hmac.Equal(decoded, expected) {
			valid = true
			break
		}
	}
	if !valid {
		return Event{}, ErrInvalidSignature
	}
	return decodeDummyEvent(payload)
}

func parseSignature(raw string) (int64, []string, error) {
	var timestamp int64
	var signatures []string
	for _, part := range strings.Split(raw, ",") {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) != 2 {
			continue
		}
		switch pair[0] {
		case "t":
			timestamp, _ = strconv.ParseInt(pair[1], 10, 64)
		case "v1":
			signatures = append(signatures, pair[1])
		}
	}
	if timestamp == 0 || len(signatures) == 0 {
		return 0, nil, ErrInvalidSignature
	}
	return timestamp, signatures, nil
}

func decodeDummyEvent(payload []byte) (Event, error) {
	var envelope struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		PaymentID   string `json:"payment_id"`
		OrderID     string `json:"order_id"`
		Status      string `json:"status"`
		AmountMinor int64  `json:"amount_minor"`
		Currency    string `json:"currency"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return Event{}, fmt.Errorf("decode dummy payment webhook: %w", err)
	}
	if envelope.ID == "" || envelope.Type == "" || envelope.PaymentID == "" || envelope.OrderID == "" {
		return Event{}, errors.New("dummy payment webhook is missing required fields")
	}
	if envelope.Type != "payment.succeeded" && envelope.Type != "payment.refunded" && envelope.Type != "payment.failed" {
		return Event{}, errors.New("unsupported dummy payment event type")
	}
	return Event{
		ID:              envelope.ID,
		Type:            envelope.Type,
		PaymentIntentID: envelope.PaymentID,
		OrderID:         envelope.OrderID,
		Status:          envelope.Status,
		AmountMinor:     envelope.AmountMinor,
		Currency:        strings.ToUpper(envelope.Currency),
		Raw:             append(json.RawMessage(nil), payload...),
	}, nil
}
