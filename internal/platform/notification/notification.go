package notification

import (
	"context"
	"fmt"
	"sync"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
)

type Message struct {
	Token string
	Title string
	Body  string
	Data  map[string]string
}
type Sender interface {
	Send(context.Context, Message) (string, error)
}

type FCM struct{ client *messaging.Client }

func NewFCM(ctx context.Context, projectID string) (*FCM, error) {
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: projectID})
	if err != nil {
		return nil, fmt.Errorf("initialize Firebase: %w", err)
	}
	client, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("initialize FCM: %w", err)
	}
	return &FCM{client: client}, nil
}
func (f *FCM) Send(ctx context.Context, m Message) (string, error) {
	id, err := f.client.Send(ctx, &messaging.Message{Token: m.Token, Notification: &messaging.Notification{Title: m.Title, Body: m.Body}, Data: m.Data})
	if err != nil {
		return "", fmt.Errorf("FCM send: %w", err)
	}
	return id, nil
}

type FakeSender struct {
	mu       sync.Mutex
	Messages []Message
	Err      error
}

func (f *FakeSender) Send(_ context.Context, m Message) (string, error) {
	if f.Err != nil {
		return "", f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Messages = append(f.Messages, m)
	return "fake-message", nil
}
