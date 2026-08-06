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
	secret := "dummy-webhook-secret"
	body := []byte(`{"id":"dummy_evt_1","type":"payment.succeeded","payment_id":"dummy_pi_1","order_id":"01900000-0000-7000-8000-000000000001","status":"succeeded","amount_minor":1000,"currency":"BDT"}`)
	now := time.Unix(1700000000, 0)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", now.Unix())
	_, _ = mac.Write(body)
	signature := fmt.Sprintf("t=%d,v1=%s", now.Unix(), hex.EncodeToString(mac.Sum(nil)))
	event, err := provider.NewDummyProvider(secret).ParseWebhook(body, signature, now)
	if err != nil {
		t.Fatal(err)
	}
	if event.AmountMinor != 1000 || event.Currency != "BDT" {
		t.Fatalf("unexpected event: %#v", event)
	}
}
