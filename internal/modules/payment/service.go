package payment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	api "github.com/Faysal9991/edtech_Backend/internal/api"
	"github.com/Faysal9991/edtech_Backend/internal/data"
	"github.com/Faysal9991/edtech_Backend/internal/platform/clock"
	"github.com/Faysal9991/edtech_Backend/internal/platform/database"
	platformid "github.com/Faysal9991/edtech_Backend/internal/platform/id"
	"github.com/Faysal9991/edtech_Backend/internal/platform/observability"
	provider "github.com/Faysal9991/edtech_Backend/internal/platform/payment"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var (
	ErrNotFound            = errors.New("order not found")
	ErrForbidden           = errors.New("order access denied")
	ErrIdempotencyConflict = errors.New("idempotency key was already used for another request")
)

type Service struct {
	db       database.Beginner
	q        *data.Queries
	ids      platformid.Generator
	clock    clock.Clock
	provider provider.Provider
}

func NewService(db database.Beginner, q *data.Queries, ids platformid.Generator, c clock.Clock, p provider.Provider) *Service {
	return &Service{db: db, q: q, ids: ids, clock: c, provider: p}
}
func asAPI(o data.Order) api.Order {
	return api.Order{Id: o.ID, Status: o.Status, AmountMinor: o.AmountMinor, Currency: o.Currency}
}

func (s *Service) CreateOrder(ctx context.Context, userID uuid.UUID, email, idempotencyKey string, courseID uuid.UUID) (api.Order, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) < 8 || len(idempotencyKey) > 200 {
		return api.Order{}, errors.New("Idempotency-Key must contain 8 to 200 characters")
	}
	existing, err := s.q.GetOrderByUserIdempotency(ctx, data.GetOrderByUserIdempotencyParams{UserID: userID, IdempotencyKey: idempotencyKey})
	if err == nil {
		if existing.CourseID != courseID {
			return api.Order{}, ErrIdempotencyConflict
		}
		return api.Order{Id: existing.ID, Status: existing.Status, AmountMinor: existing.AmountMinor, Currency: existing.Currency}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return api.Order{}, err
	}
	course, err := s.q.GetCourse(ctx, courseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.Order{}, ErrNotFound
	}
	if err != nil {
		return api.Order{}, err
	}
	if course.Status != "published" || course.IsFree || course.PriceMinor <= 0 {
		return api.Order{}, errors.New("course is not available for purchase")
	}
	if enrollment, e := s.q.GetCourseEnrollment(ctx, data.GetCourseEnrollmentParams{CourseID: courseID, StudentID: userID}); e == nil && (enrollment.Status == "active" || enrollment.Status == "completed") {
		return api.Order{}, errors.New("student is already enrolled")
	}
	var order data.Order
	err = database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		order, err = q.CreateOrder(ctx, data.CreateOrderParams{ID: s.ids.New(), OrganizationID: course.OrganizationID, UserID: userID, AmountMinor: course.PriceMinor, Currency: course.Currency, IdempotencyKey: idempotencyKey})
		if errors.Is(err, pgx.ErrNoRows) {
			existing, lookupErr := q.GetOrderByUserIdempotency(ctx, data.GetOrderByUserIdempotencyParams{UserID: userID, IdempotencyKey: idempotencyKey})
			if lookupErr != nil {
				return lookupErr
			}
			if existing.CourseID != courseID {
				return ErrIdempotencyConflict
			}
			order = data.Order{ID: existing.ID, OrganizationID: existing.OrganizationID, UserID: existing.UserID, Status: existing.Status, AmountMinor: existing.AmountMinor, Currency: existing.Currency, IdempotencyKey: existing.IdempotencyKey, ProviderPaymentIntentID: existing.ProviderPaymentIntentID, CreatedAt: existing.CreatedAt, UpdatedAt: existing.UpdatedAt, PaidAt: existing.PaidAt}
			return nil
		}
		if err != nil {
			return err
		}
		_, err = q.CreateOrderItem(ctx, data.CreateOrderItemParams{ID: s.ids.New(), OrderID: order.ID, CourseID: course.ID, CourseTitle: course.Title, AmountMinor: course.PriceMinor, Currency: course.Currency})
		return err
	})
	_ = email
	if err != nil {
		return api.Order{}, err
	}
	return asAPI(order), nil
}

func (s *Service) CreateIntent(ctx context.Context, orderID, userID uuid.UUID, email string) (api.PaymentIntent, error) {
	var order data.Order
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		locked, err := q.GetOrderForUpdate(ctx, orderID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if locked.UserID != userID {
			return ErrForbidden
		}
		if locked.Status == "paid" || locked.Status == "refunded" || locked.Status == "cancelled" {
			return errors.New("order cannot accept a payment")
		}
		order = locked
		return nil
	})
	if err != nil {
		return api.PaymentIntent{}, err
	}
	intent, err := s.provider.CreateIntent(ctx, provider.IntentInput{OrderID: order.ID.String(), AmountMinor: order.AmountMinor, Currency: order.Currency, IdempotencyKey: "order-" + order.ID.String(), CustomerEmail: email})
	if err != nil {
		observability.PaymentFailures.Inc()
		return api.PaymentIntent{}, err
	}
	if _, err = s.q.SetOrderPaymentIntent(ctx, data.SetOrderPaymentIntentParams{ID: order.ID, ProviderPaymentIntentID: pgtype.Text{String: intent.ID, Valid: true}}); err != nil {
		return api.PaymentIntent{}, err
	}
	return api.PaymentIntent{ClientSecret: intent.ClientSecret}, nil
}

func (s *Service) Webhook(ctx context.Context, body []byte, signature string) error {
	event, err := s.provider.ParseWebhook(body, signature, s.clock.Now())
	if err != nil {
		observability.PaymentFailures.Inc()
		return err
	}
	return database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		inserted, err := q.CreatePaymentWebhookEvent(ctx, data.CreatePaymentWebhookEventParams{ID: s.ids.New(), ProviderEventID: event.ID, EventType: event.Type, Payload: body})
		if err != nil {
			return err
		}
		if inserted == 0 {
			return nil
		}
		if event.Type == "charge.refunded" {
			var order data.Order
			orderID, parseErr := uuid.Parse(event.OrderID)
			if parseErr == nil {
				order, err = q.GetOrderForUpdate(ctx, orderID)
			} else if event.PaymentIntentID != "" {
				order, err = q.GetOrderByPaymentIntentForUpdate(ctx, pgtype.Text{String: event.PaymentIntentID, Valid: true})
			} else {
				return nil
			}
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			paymentTx, err := q.GetSuccessfulPaymentForOrder(ctx, order.ID)
			if err != nil {
				return err
			}
			alreadyRefunded, err := q.SumSucceededRefunds(ctx, order.ID)
			if err != nil {
				return err
			}
			cumulativeAmount := event.AmountMinor
			if cumulativeAmount <= 0 {
				cumulativeAmount = order.AmountMinor
			}
			amount := cumulativeAmount - alreadyRefunded
			if amount <= 0 {
				return nil
			}
			if cumulativeAmount > order.AmountMinor || !strings.EqualFold(event.Currency, order.Currency) {
				return errors.New("refund amount or currency does not match order")
			}
			status := "paid"
			if cumulativeAmount == order.AmountMinor {
				status = "refunded"
			}
			if _, err := q.CreateRefund(ctx, data.CreateRefundParams{ID: s.ids.New(), OrderID: order.ID, PaymentTransactionID: paymentTx.ID, ProviderRefundID: event.ID, AmountMinor: amount, Currency: order.Currency, Status: "succeeded", Reason: pgtype.Text{}}); err != nil {
				return err
			}
			_, err = q.SetOrderStatus(ctx, data.SetOrderStatusParams{ID: order.ID, Status: status})
			if err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]string{"user_id": order.UserID.String(), "organization_id": order.OrganizationID.String(), "order_id": order.ID.String()})
			return q.InsertOutboxEvent(ctx, data.InsertOutboxEventParams{ID: s.ids.New(), AggregateType: "order", AggregateID: order.ID, EventType: "refund.succeeded", Payload: payload, DeduplicationKey: "refund.succeeded:" + event.ID})
		}
		if event.PaymentIntentID == "" {
			return nil
		}
		order, err := q.GetOrderByPaymentIntentForUpdate(ctx, pgtype.Text{String: event.PaymentIntentID, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if event.AmountMinor != 0 && (event.AmountMinor != order.AmountMinor || !strings.EqualFold(event.Currency, order.Currency)) {
			return errors.New("provider amount or currency does not match order")
		}
		switch event.Type {
		case "payment_intent.succeeded":
			if order.Status == "paid" {
				return nil
			}
			if _, err = q.CreatePaymentTransaction(ctx, data.CreatePaymentTransactionParams{ID: s.ids.New(), OrderID: order.ID, ProviderTransactionID: event.PaymentIntentID, Kind: "payment", Status: "succeeded", AmountMinor: order.AmountMinor, Currency: order.Currency, FailureCode: pgtype.Text{}}); err != nil {
				return err
			}
			if _, err = q.SetOrderStatus(ctx, data.SetOrderStatusParams{ID: order.ID, Status: "paid"}); err != nil {
				return err
			}
			items, err := tx.Query(ctx, "SELECT course_id FROM order_items WHERE order_id=$1 ORDER BY id", order.ID)
			if err != nil {
				return err
			}
			courseIDs := []uuid.UUID{}
			for items.Next() {
				var courseID uuid.UUID
				if err := items.Scan(&courseID); err != nil {
					items.Close()
					return err
				}
				courseIDs = append(courseIDs, courseID)
			}
			if err := items.Err(); err != nil {
				items.Close()
				return err
			}
			items.Close()
			for _, courseID := range courseIDs {
				course, err := q.GetCourse(ctx, courseID)
				if err != nil {
					return err
				}
				enrollment, err := q.CreateEnrollment(ctx, data.CreateEnrollmentParams{ID: s.ids.New(), OrganizationID: order.OrganizationID, CourseID: courseID, StudentID: order.UserID, Status: "active", Source: "payment", PriceMinorSnapshot: course.PriceMinor, CurrencySnapshot: course.Currency})
				if err != nil {
					return err
				}
				if enrollment.Status != "active" && enrollment.Status != "completed" {
					enrollment, err = q.SetEnrollmentStatus(ctx, data.SetEnrollmentStatusParams{ID: enrollment.ID, Status: "active"})
					if err != nil {
						return err
					}
				}
				payload, _ := json.Marshal(map[string]string{"user_id": order.UserID.String(), "organization_id": order.OrganizationID.String(), "enrollment_id": enrollment.ID.String(), "order_id": order.ID.String()})
				if err := q.InsertOutboxEvent(ctx, data.InsertOutboxEventParams{ID: s.ids.New(), AggregateType: "order", AggregateID: order.ID, EventType: "payment.succeeded", Payload: payload, DeduplicationKey: "payment.succeeded:" + order.ID.String()}); err != nil {
					return err
				}
			}
			return nil
		case "payment_intent.payment_failed", "payment_intent.canceled":
			status := "failed"
			txStatus := "failed"
			if event.Type == "payment_intent.canceled" {
				status = "cancelled"
				txStatus = "cancelled"
			}
			_, err = q.CreatePaymentTransaction(ctx, data.CreatePaymentTransactionParams{ID: s.ids.New(), OrderID: order.ID, ProviderTransactionID: event.PaymentIntentID + ":" + event.ID, Kind: "payment", Status: txStatus, AmountMinor: order.AmountMinor, Currency: order.Currency, FailureCode: pgtype.Text{}})
			if err != nil {
				return err
			}
			_, err = q.SetOrderStatus(ctx, data.SetOrderStatusParams{ID: order.ID, Status: status})
			if err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]string{"user_id": order.UserID.String(), "organization_id": order.OrganizationID.String(), "order_id": order.ID.String()})
			return q.InsertOutboxEvent(ctx, data.InsertOutboxEventParams{ID: s.ids.New(), AggregateType: "order", AggregateID: order.ID, EventType: "payment.failed", Payload: payload, DeduplicationKey: "payment.failed:" + event.ID})
		}
		return nil
	})
}

func (s *Service) GetOwned(ctx context.Context, id, userID uuid.UUID, privileged bool) (api.Order, error) {
	row, err := s.q.GetOrder(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.Order{}, ErrNotFound
	}
	if err != nil {
		return api.Order{}, err
	}
	if !privileged && row.UserID != userID {
		return api.Order{}, ErrForbidden
	}
	return asAPI(row), nil
}
func (s *Service) Cancel(ctx context.Context, id, userID uuid.UUID) (api.Order, error) {
	order, err := s.q.GetOrder(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.Order{}, ErrNotFound
	}
	if err != nil {
		return api.Order{}, err
	}
	if order.UserID != userID {
		return api.Order{}, ErrForbidden
	}
	if order.Status == "paid" || order.Status == "refunded" {
		return api.Order{}, errors.New("paid orders must use the refund workflow")
	}
	if order.Status == "cancelled" {
		return asAPI(order), nil
	}
	if order.ProviderPaymentIntentID.Valid {
		if err := s.provider.CancelIntent(ctx, order.ProviderPaymentIntentID.String); err != nil {
			observability.PaymentFailures.Inc()
			return api.Order{}, err
		}
	}
	var updated data.Order
	err = database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		locked, err := q.GetOrderForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if locked.Status == "paid" || locked.Status == "refunded" {
			return errors.New("order completed while cancellation was in progress")
		}
		updated, err = q.SetOrderStatus(ctx, data.SetOrderStatusParams{ID: id, Status: "cancelled"})
		return err
	})
	if err != nil {
		return api.Order{}, err
	}
	return asAPI(updated), nil
}
