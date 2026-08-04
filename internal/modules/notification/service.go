package notification

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	api "github.com/Faysal9991/edtech_Backend/internal/api"
	"github.com/Faysal9991/edtech_Backend/internal/data"
	"github.com/Faysal9991/edtech_Backend/internal/platform/clock"
	platformid "github.com/Faysal9991/edtech_Backend/internal/platform/id"
	platformnotification "github.com/Faysal9991/edtech_Backend/internal/platform/notification"
	"github.com/Faysal9991/edtech_Backend/internal/platform/observability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type Cryptor struct{ aead cipher.AEAD }

func NewCryptor(secret string) (*Cryptor, error) {
	if len(secret) < 32 {
		return nil, errors.New("DEVICE_TOKEN_ENCRYPTION_KEY must contain at least 32 characters")
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cryptor{aead: aead}, nil
}
func (c *Cryptor) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}
func (c *Cryptor) Decrypt(encoded string) (string, error) {
	payload, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	if len(payload) < c.aead.NonceSize() {
		return "", errors.New("invalid encrypted token")
	}
	plain, err := c.aead.Open(nil, payload[:c.aead.NonceSize()], payload[c.aead.NonceSize():], nil)
	return string(plain), err
}

type Service struct {
	q       *data.Queries
	ids     platformid.Generator
	clock   clock.Clock
	sender  platformnotification.Sender
	cryptor *Cryptor
}

func NewService(q *data.Queries, ids platformid.Generator, c clock.Clock, sender platformnotification.Sender, cryptor *Cryptor) *Service {
	return &Service{q: q, ids: ids, clock: c, sender: sender, cryptor: cryptor}
}
func (s *Service) RegisterToken(ctx context.Context, userID uuid.UUID, in api.DeviceTokenWrite) (data.DeviceToken, error) {
	if len(strings.TrimSpace(in.Token)) < 20 {
		return data.DeviceToken{}, errors.New("device token is invalid")
	}
	hash := sha256.Sum256([]byte(in.Token))
	encrypted, err := s.cryptor.Encrypt(in.Token)
	if err != nil {
		return data.DeviceToken{}, err
	}
	return s.q.RegisterDeviceToken(ctx, data.RegisterDeviceTokenParams{ID: s.ids.New(), UserID: userID, TokenHash: hex.EncodeToString(hash[:]), EncryptedToken: encrypted, Platform: string(in.Platform)})
}
func (s *Service) RemoveToken(ctx context.Context, userID, id uuid.UUID) error {
	affected, err := s.q.RemoveDeviceToken(ctx, data.RemoveDeviceTokenParams{ID: id, UserID: userID})
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("device token not found")
	}
	return nil
}

func (s *Service) DispatchBatch(ctx context.Context, limit int32) error {
	events, err := s.q.ClaimOutboxEvents(ctx, limit)
	if err != nil {
		return err
	}
	var first error
	for _, event := range events {
		if err := s.dispatch(ctx, event); err != nil {
			delay := time.Duration(1<<min(event.Attempts, 10)) * time.Second
			message := truncate(err.Error(), 500)
			_, _ = s.q.SetOutboxFailed(ctx, data.SetOutboxFailedParams{ID: event.ID, NextAttemptAt: pgtype.Timestamptz{Time: s.clock.Now().Add(delay), Valid: true}, LastError: pgtype.Text{String: message, Valid: true}})
			if first == nil {
				first = err
			}
		} else {
			_, _ = s.q.SetOutboxPublished(ctx, event.ID)
		}
	}
	return first
}
func min(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
func truncate(v string, n int) string {
	if len(v) > n {
		return v[:n]
	}
	return v
}

type eventPayload struct {
	UserID         string            `json:"user_id"`
	OrganizationID string            `json:"organization_id"`
	Title          string            `json:"title"`
	Body           string            `json:"body"`
	Data           map[string]string `json:"data"`
}

func (s *Service) dispatch(ctx context.Context, event data.OutboxEvent) error {
	var payload eventPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return err
	}
	userID, err := uuid.Parse(payload.UserID)
	if err != nil {
		return errors.New("outbox event has invalid user_id")
	}
	title, body := payload.Title, payload.Body
	if title == "" {
		title, body = notificationCopy(event.EventType)
	}
	var org uuid.NullUUID
	if id, e := uuid.Parse(payload.OrganizationID); e == nil {
		org = uuid.NullUUID{UUID: id, Valid: true}
	}
	dedupe := event.DeduplicationKey
	notification, err := s.q.CreateNotification(ctx, data.CreateNotificationParams{ID: s.ids.New(), UserID: userID, OrganizationID: org, Type: event.EventType, Title: title, Body: body, Data: event.Payload, DeduplicationKey: pgtype.Text{String: dedupe, Valid: true}})
	if err != nil {
		return err
	}
	tokens, err := s.q.ListUserDeviceTokens(ctx, userID)
	if err != nil {
		return err
	}
	for _, device := range tokens {
		delivery, err := s.q.CreateNotificationDelivery(ctx, data.CreateNotificationDeliveryParams{ID: s.ids.New(), NotificationID: notification.ID, DeviceTokenID: uuid.NullUUID{UUID: device.ID, Valid: true}})
		if err != nil {
			return err
		}
		token, err := s.cryptor.Decrypt(device.EncryptedToken)
		if err != nil {
			return err
		}
		providerID, sendErr := s.sender.Send(ctx, platformnotification.Message{Token: token, Title: title, Body: body, Data: payload.Data})
		if sendErr != nil {
			observability.NotificationFailures.Inc()
			message := truncate(sendErr.Error(), 500)
			_, _ = s.q.SetNotificationDeliveryResult(ctx, data.SetNotificationDeliveryResultParams{ID: delivery.ID, Status: "failed", NextAttemptAt: pgtype.Timestamptz{Time: s.clock.Now().Add(time.Minute), Valid: true}, ProviderMessageID: pgtype.Text{}, LastError: pgtype.Text{String: message, Valid: true}})
			if strings.Contains(strings.ToLower(message), "registration-token-not-registered") || strings.Contains(strings.ToLower(message), "invalid registration") {
				_, _ = s.q.DeleteDeviceTokenByID(ctx, device.ID)
			}
			continue
		}
		_, _ = s.q.SetNotificationDeliveryResult(ctx, data.SetNotificationDeliveryResultParams{ID: delivery.ID, Status: "sent", NextAttemptAt: pgtype.Timestamptz{}, ProviderMessageID: pgtype.Text{String: providerID, Valid: true}, LastError: pgtype.Text{}})
	}
	return nil
}

func notificationCopy(kind string) (string, string) {
	switch kind {
	case "invitation.created":
		return "You're invited", "An organization invited you to join."
	case "enrollment.activated":
		return "Enrollment active", "Your course enrollment is now active."
	case "payment.succeeded":
		return "Payment successful", "Your payment was confirmed and enrollment activated."
	case "payment.failed":
		return "Payment failed", "Your payment could not be completed."
	case "refund.succeeded":
		return "Refund recorded", "Your payment refund was recorded."
	case "course.published":
		return "Course published", "A new course is available."
	case "live.reminder":
		return "Live class reminder", "Your live class starts soon."
	case "assignment.deadline":
		return "Assignment due soon", "An assignment deadline is approaching."
	case "assignment.graded":
		return "Assignment graded", "Your instructor graded an assignment."
	case "quiz.result":
		return "Quiz result available", "Your quiz result is available."
	case "course.completed":
		return "Course completed", "Congratulations on completing your course."
	case "certificate.ready":
		return "Certificate ready", "Your course certificate is ready to download."
	default:
		return "LMS update", "You have a new learning update."
	}
}

func (s *Service) CreateInApp(ctx context.Context, userID uuid.UUID, kind, title, body, dedupe string, dataValue map[string]string) (data.Notification, error) {
	payload, _ := json.Marshal(dataValue)
	return s.q.CreateNotification(ctx, data.CreateNotificationParams{ID: s.ids.New(), UserID: userID, OrganizationID: uuid.NullUUID{}, Type: kind, Title: title, Body: body, Data: payload, DeduplicationKey: pgtype.Text{String: dedupe, Valid: dedupe != ""}})
}

func (s *Service) QueueDueReminders(ctx context.Context) error {
	liveRows, err := s.q.ListDueLiveReminders(ctx, 500)
	if err != nil {
		return err
	}
	for _, row := range liveRows {
		payload, _ := json.Marshal(eventPayload{UserID: row.UserID.String(), OrganizationID: row.OrganizationID.String(), Title: "Live class reminder", Body: row.Title + " starts soon.", Data: map[string]string{"live_session_id": row.LiveSessionID.String()}})
		if err := s.q.InsertOutboxEvent(ctx, data.InsertOutboxEventParams{ID: s.ids.New(), AggregateType: "live_session", AggregateID: row.LiveSessionID, EventType: "live.reminder", Payload: payload, DeduplicationKey: "live.reminder:" + row.LiveSessionID.String() + ":" + row.UserID.String()}); err != nil {
			return err
		}
	}
	assignmentRows, err := s.q.ListDueAssignmentReminders(ctx, 500)
	if err != nil {
		return err
	}
	for _, row := range assignmentRows {
		payload, _ := json.Marshal(eventPayload{UserID: row.UserID.String(), OrganizationID: row.OrganizationID.String(), Title: "Assignment deadline", Body: row.Title + " is due within 24 hours.", Data: map[string]string{"assignment_id": row.AssignmentID.String()}})
		if err := s.q.InsertOutboxEvent(ctx, data.InsertOutboxEventParams{ID: s.ids.New(), AggregateType: "assignment", AggregateID: row.AssignmentID, EventType: "assignment.deadline", Payload: payload, DeduplicationKey: "assignment.deadline:" + row.AssignmentID.String() + ":" + row.UserID.String()}); err != nil {
			return err
		}
	}
	return nil
}
