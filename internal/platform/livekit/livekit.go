package livekit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	lkauth "github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/webhook"
)

type Grant struct {
	Room         string
	Identity     string
	Name         string
	CanPublish   bool
	CanSubscribe bool
	TTL          time.Duration
}
type WebhookEvent struct {
	ID                  string
	Type                string
	Room                string
	ParticipantIdentity string
	OccurredAt          time.Time
	Raw                 json.RawMessage
}
type Provider interface {
	JoinToken(context.Context, Grant) (string, error)
	ReceiveWebhook(*http.Request) (WebhookEvent, error)
}

type LiveKit struct{ apiKey, apiSecret string }

func New(apiKey, apiSecret string) *LiveKit { return &LiveKit{apiKey: apiKey, apiSecret: apiSecret} }
func (l *LiveKit) JoinToken(_ context.Context, g Grant) (string, error) {
	if g.Room == "" || g.Identity == "" {
		return "", errors.New("room and identity are required")
	}
	ttl := g.TTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	token := lkauth.NewAccessToken(l.apiKey, l.apiSecret).SetIdentity(g.Identity).SetName(g.Name).SetValidFor(ttl).SetVideoGrant(&lkauth.VideoGrant{RoomJoin: true, Room: g.Room, CanPublish: &g.CanPublish, CanSubscribe: &g.CanSubscribe})
	signed, err := token.ToJWT()
	if err != nil {
		return "", fmt.Errorf("sign LiveKit token: %w", err)
	}
	return signed, nil
}
func (l *LiveKit) ReceiveWebhook(r *http.Request) (WebhookEvent, error) {
	event, err := webhook.ReceiveWebhookEvent(r, lkauth.NewSimpleKeyProvider(l.apiKey, l.apiSecret))
	if err != nil {
		return WebhookEvent{}, fmt.Errorf("verify LiveKit webhook: %w", err)
	}
	raw, _ := json.Marshal(event)
	out := WebhookEvent{ID: event.Id, Type: event.Event, OccurredAt: time.Unix(event.CreatedAt, 0).UTC(), Raw: raw}
	if event.Room != nil {
		out.Room = event.Room.Name
	}
	if event.Participant != nil {
		out.ParticipantIdentity = event.Participant.Identity
	}
	return out, nil
}

type FakeProvider struct {
	Secret string
	Events map[string]WebhookEvent
}

func (f *FakeProvider) JoinToken(_ context.Context, g Grant) (string, error) {
	return "fake-token:" + g.Room + ":" + g.Identity, nil
}
func (f *FakeProvider) ReceiveWebhook(r *http.Request) (WebhookEvent, error) {
	id := r.Header.Get("X-Fake-Event-ID")
	event, ok := f.Events[id]
	if !ok {
		return WebhookEvent{}, errors.New("invalid fake webhook")
	}
	return event, nil
}
