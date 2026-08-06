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

	stripe "github.com/stripe/stripe-go/v82"
)

var ErrInvalidSignature = errors.New("invalid webhook signature")

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

type Stripe struct {
	webhookSecret string
	client        *stripe.Client
}

func NewStripe(secretKey, webhookSecret string) *Stripe {
	return &Stripe{webhookSecret: webhookSecret, client: stripe.NewClient(secretKey)}
}

func (s *Stripe) CreateIntent(ctx context.Context, in IntentInput) (Intent, error) {
	params := &stripe.PaymentIntentCreateParams{
		Amount: stripe.Int64(in.AmountMinor), Currency: stripe.String(strings.ToLower(in.Currency)),
		AutomaticPaymentMethods: &stripe.PaymentIntentCreateAutomaticPaymentMethodsParams{Enabled: stripe.Bool(true)},
		Metadata:                map[string]string{"order_id": in.OrderID},
	}
	if in.CustomerEmail != "" {
		params.ReceiptEmail = stripe.String(in.CustomerEmail)
	}
	params.SetIdempotencyKey(in.IdempotencyKey)
	result, err := s.client.V1PaymentIntents.Create(ctx, params)
	if err != nil {
		return Intent{}, fmt.Errorf("create Stripe payment intent: %w", err)
	}
	if result == nil || result.ID == "" || result.ClientSecret == "" {
		return Intent{}, errors.New("Stripe response missing payment intent data")
	}
	return Intent{ID: result.ID, ClientSecret: result.ClientSecret, Status: string(result.Status)}, nil
}
func (s *Stripe) CancelIntent(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	_, err := s.client.V1PaymentIntents.Cancel(ctx, id, &stripe.PaymentIntentCancelParams{})
	if err != nil {
		return fmt.Errorf("cancel Stripe payment intent: %w", err)
	}
	return nil
}

func (s *Stripe) ParseWebhook(payload []byte, signature string, now time.Time) (Event, error) {
	timestamp, signatures, err := parseStripeSignature(signature)
	if err != nil {
		return Event{}, ErrInvalidSignature
	}
	if delta := now.Sub(time.Unix(timestamp, 0)); delta > 5*time.Minute || delta < -5*time.Minute {
		return Event{}, ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, []byte(s.webhookSecret))
	_, _ = mac.Write([]byte(strconv.FormatInt(timestamp, 10) + "."))
	_, _ = mac.Write(payload)
	expected := mac.Sum(nil)
	valid := false
	for _, candidate := range signatures {
		decoded, err := hex.DecodeString(candidate)
		if err == nil && hmac.Equal(decoded, expected) {
			valid = true
			break
		}
	}
	if !valid {
		return Event{}, ErrInvalidSignature
	}
	return decodeStripeEvent(payload)
}

func parseStripeSignature(raw string) (int64, []string, error) {
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

func decodeStripeEvent(payload []byte) (Event, error) {
	var envelope struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			Object struct {
				ID             string            `json:"id"`
				PaymentIntent  string            `json:"payment_intent"`
				Status         string            `json:"status"`
				AmountReceived int64             `json:"amount_received"`
				Amount         int64             `json:"amount"`
				AmountRefunded int64             `json:"amount_refunded"`
				Currency       string            `json:"currency"`
				Metadata       map[string]string `json:"metadata"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return Event{}, fmt.Errorf("decode Stripe webhook: %w", err)
	}
	if envelope.ID == "" || envelope.Type == "" {
		return Event{}, errors.New("Stripe webhook missing id or type")
	}
	amount := envelope.Data.Object.AmountReceived
	if envelope.Type == "charge.refunded" && envelope.Data.Object.AmountRefunded > 0 {
		amount = envelope.Data.Object.AmountRefunded
	}
	if amount == 0 {
		amount = envelope.Data.Object.Amount
	}
	paymentIntentID := envelope.Data.Object.ID
	if envelope.Type == "charge.refunded" && envelope.Data.Object.PaymentIntent != "" {
		paymentIntentID = envelope.Data.Object.PaymentIntent
	}
	return Event{ID: envelope.ID, Type: envelope.Type, PaymentIntentID: paymentIntentID, OrderID: envelope.Data.Object.Metadata["order_id"], Status: envelope.Data.Object.Status, AmountMinor: amount, Currency: strings.ToUpper(envelope.Data.Object.Currency), Raw: append(json.RawMessage(nil), payload...)}, nil
}

type FakeProvider struct {
	Intents map[string]Intent
	Secret  string
	mu      sync.Mutex
}

func NewFakeProvider(secret string) *FakeProvider {
	return &FakeProvider{Intents: map[string]Intent{}, Secret: secret}
}
func (f *FakeProvider) CreateIntent(_ context.Context, in IntentInput) (Intent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	intent := Intent{ID: "pi_test_" + in.OrderID, ClientSecret: "pi_test_secret", Status: "requires_payment_method"}
	f.Intents[in.OrderID] = intent
	return intent, nil
}
func (f *FakeProvider) CancelIntent(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, intent := range f.Intents {
		if intent.ID == id {
			intent.Status = "canceled"
			f.Intents[key] = intent
			return nil
		}
	}
	return nil
}
func (f *FakeProvider) ParseWebhook(body []byte, sig string, now time.Time) (Event, error) {
	return NewStripe("", f.Secret).ParseWebhook(body, sig, now)
}
