package payment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	provider "github.com/neoscoder/lms-service/internal/platform/payment"
)

func TestSignedWebhook(t *testing.T) {
	secret := "whsec_test"
	body := []byte(`{"id":"evt_1","type":"payment_intent.succeeded","data":{"object":{"id":"pi_1","amount_received":1000,"currency":"bdt","metadata":{"order_id":"x"}}}}`)
	now := time.Unix(1700000000, 0)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", now.Unix())
	_, _ = mac.Write(body)
	signature := fmt.Sprintf("t=%d,v1=%s", now.Unix(), hex.EncodeToString(mac.Sum(nil)))
	event, err := provider.NewFakeProvider(secret).ParseWebhook(body, signature, now)
	if err != nil {
		t.Fatal(err)
	}
	if event.AmountMinor != 1000 || event.Currency != "BDT" {
		t.Fatalf("unexpected event: %#v", event)
	}
}
