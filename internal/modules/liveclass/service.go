package liveclass

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	api "github.com/neoscoder/lms-service/internal/api"
	"github.com/neoscoder/lms-service/internal/data"
	"github.com/neoscoder/lms-service/internal/platform/clock"
	"github.com/neoscoder/lms-service/internal/platform/config"
	"github.com/neoscoder/lms-service/internal/platform/database"
	platformid "github.com/neoscoder/lms-service/internal/platform/id"
	platformlivekit "github.com/neoscoder/lms-service/internal/platform/livekit"
)

var (
	ErrNotFound  = errors.New("live session not found")
	ErrForbidden = errors.New("live session access denied")
)

type Service struct {
	db       database.Beginner
	q        *data.Queries
	ids      platformid.Generator
	clock    clock.Clock
	provider platformlivekit.Provider
	cfg      config.LiveKit
}

func NewService(db database.Beginner, q *data.Queries, ids platformid.Generator, c clock.Clock, p platformlivekit.Provider, cfg config.LiveKit) *Service {
	return &Service{db: db, q: q, ids: ids, clock: c, provider: p, cfg: cfg}
}
func value(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func (s *Service) Create(ctx context.Context, orgID, actorID uuid.UUID, in api.LiveSessionWrite) (data.LiveSession, error) {
	if strings.TrimSpace(in.Title) == "" || !in.ScheduledEndAt.After(in.ScheduledStartAt) {
		return data.LiveSession{}, errors.New("invalid live session schedule")
	}
	course, err := s.q.GetCourse(ctx, in.CourseId)
	if err != nil {
		return data.LiveSession{}, err
	}
	if course.OrganizationID != orgID {
		return data.LiveSession{}, ErrForbidden
	}
	id := s.ids.New()
	room := fmt.Sprintf("org_%s_course_%s_session_%s", orgID, in.CourseId, id)
	return s.q.CreateLiveSession(ctx, data.CreateLiveSessionParams{ID: id, OrganizationID: orgID, CourseID: in.CourseId, Title: strings.TrimSpace(in.Title), Description: value(in.Description), RoomName: room, ScheduledStartAt: pgtype.Timestamptz{Time: in.ScheduledStartAt.UTC(), Valid: true}, ScheduledEndAt: pgtype.Timestamptz{Time: in.ScheduledEndAt.UTC(), Valid: true}, CreatedBy: actorID})
}

func (s *Service) JoinToken(ctx context.Context, sessionID, userID uuid.UUID, name string, instructor bool) (api.JoinToken, error) {
	session, err := s.q.GetLiveSession(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.JoinToken{}, ErrNotFound
	}
	if err != nil {
		return api.JoinToken{}, err
	}
	if session.Status == "cancelled" || session.Status == "ended" {
		return api.JoinToken{}, errors.New("live session is not joinable")
	}
	if instructor {
		assigned, err := s.q.IsCourseInstructor(ctx, data.IsCourseInstructorParams{CourseID: session.CourseID, InstructorID: userID})
		if err != nil {
			return api.JoinToken{}, err
		}
		if !assigned {
			return api.JoinToken{}, ErrForbidden
		}
	} else {
		enrollment, err := s.q.GetCourseEnrollment(ctx, data.GetCourseEnrollmentParams{CourseID: session.CourseID, StudentID: userID})
		if err != nil || enrollment.Status != "active" {
			return api.JoinToken{}, ErrForbidden
		}
	}
	ttl := time.Hour
	if session.ScheduledEndAt.Valid {
		until := session.ScheduledEndAt.Time.Add(30 * time.Minute).Sub(s.clock.Now())
		if until > 0 && until < ttl {
			ttl = until
		}
	}
	token, err := s.provider.JoinToken(ctx, platformlivekit.Grant{Room: session.RoomName, Identity: userID.String(), Name: name, CanPublish: instructor, CanSubscribe: true, TTL: ttl})
	if err != nil {
		return api.JoinToken{}, err
	}
	expires := s.clock.Now().Add(ttl)
	return api.JoinToken{Token: token, Url: s.cfg.URL, ExpiresAt: expires}, nil
}

func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, status string) (data.LiveSession, error) {
	row, err := s.q.GetLiveSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return data.LiveSession{}, ErrNotFound
	}
	if err != nil {
		return data.LiveSession{}, err
	}
	valid := false
	switch {
	case row.Status == "scheduled" && status == "live":
		valid = true
	case row.Status == "live" && status == "ended":
		valid = true
	case row.Status == "scheduled" && status == "cancelled":
		valid = true
	}
	if !valid {
		return data.LiveSession{}, fmt.Errorf("cannot transition live session from %s to %s", row.Status, status)
	}
	return s.q.SetLiveSessionStatus(ctx, data.SetLiveSessionStatusParams{ID: id, Status: status})
}

func (s *Service) Webhook(ctx context.Context, r *http.Request) error {
	event, err := s.provider.ReceiveWebhook(r)
	if err != nil {
		return err
	}
	return database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		inserted, err := q.CreateLiveWebhookEvent(ctx, data.CreateLiveWebhookEventParams{ID: s.ids.New(), ProviderEventID: event.ID, EventType: event.Type, Payload: event.Raw})
		if err != nil {
			return err
		}
		if inserted == 0 {
			return nil
		}
		session, err := q.GetLiveSessionByRoom(ctx, event.Room)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		userID, err := uuid.Parse(event.ParticipantIdentity)
		if err != nil {
			return nil
		}
		occurred := event.OccurredAt
		if occurred.IsZero() {
			occurred = s.clock.Now()
		}
		switch event.Type {
		case "participant_joined":
			_, err = q.StartAttendanceInterval(ctx, data.StartAttendanceIntervalParams{ID: s.ids.New(), LiveSessionID: session.ID, UserID: userID, ParticipantIdentity: event.ParticipantIdentity, JoinedAt: pgtype.Timestamptz{Time: occurred, Valid: true}})
			return err
		case "participant_left":
			open, err := q.GetOpenAttendanceIntervalForUpdate(ctx, data.GetOpenAttendanceIntervalForUpdateParams{LiveSessionID: session.ID, ParticipantIdentity: event.ParticipantIdentity})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			_, err = q.CloseAttendanceInterval(ctx, data.CloseAttendanceIntervalParams{ID: open.ID, LeftAt: pgtype.Timestamptz{Time: occurred, Valid: true}})
			return err
		}
		return nil
	})
}
