package enrollment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	api "github.com/neoscoder/lms-service/internal/api"
	"github.com/neoscoder/lms-service/internal/data"
	"github.com/neoscoder/lms-service/internal/platform/clock"
	"github.com/neoscoder/lms-service/internal/platform/database"
	platformid "github.com/neoscoder/lms-service/internal/platform/id"
	"github.com/neoscoder/lms-service/internal/platform/queue"
)

var (
	ErrNotFound        = errors.New("enrollment not found")
	ErrForbidden       = errors.New("enrollment access denied")
	ErrPaymentRequired = errors.New("course requires payment")
	ErrUnavailable     = errors.New("course is not available for enrollment")
)

type Service struct {
	db    database.Beginner
	q     *data.Queries
	ids   platformid.Generator
	clock clock.Clock
	jobs  queue.Client
}

func NewService(db database.Beginner, q *data.Queries, ids platformid.Generator, c clock.Clock, jobs queue.Client) *Service {
	return &Service{db: db, q: q, ids: ids, clock: c, jobs: jobs}
}

func asAPI(e data.Enrollment) api.Enrollment {
	pct, _ := e.CompletionPercentage.Float64Value()
	value := float32(0)
	if pct.Valid {
		value = float32(pct.Float64)
	}
	return api.Enrollment{Id: e.ID, CourseId: e.CourseID, StudentId: e.StudentID, Status: api.EnrollmentStatus(e.Status), CompletionPercentage: value}
}

func (s *Service) EnrollFree(ctx context.Context, courseID, studentID uuid.UUID) (api.Enrollment, error) {
	course, err := s.q.GetCourse(ctx, courseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.Enrollment{}, ErrNotFound
	}
	if err != nil {
		return api.Enrollment{}, err
	}
	if course.Status != "published" {
		return api.Enrollment{}, ErrUnavailable
	}
	if !course.IsFree {
		return api.Enrollment{}, ErrPaymentRequired
	}
	existing, err := s.q.GetCourseEnrollment(ctx, data.GetCourseEnrollmentParams{CourseID: courseID, StudentID: studentID})
	if err == nil {
		if existing.Status == "active" || existing.Status == "completed" {
			return asAPI(existing), nil
		}
		if existing.Status == "pending_payment" {
			return api.Enrollment{}, errors.New("a payment enrollment already exists")
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return api.Enrollment{}, err
	}
	var row data.Enrollment
	err = database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		row, err = q.CreateEnrollment(ctx, data.CreateEnrollmentParams{ID: s.ids.New(), OrganizationID: course.OrganizationID, CourseID: courseID, StudentID: studentID, Status: "active", Source: "free", PriceMinorSnapshot: 0, CurrencySnapshot: course.Currency})
		if err != nil {
			return err
		}
		if row.Status != "active" && row.Status != "completed" {
			if row.Status == "pending_payment" {
				return errors.New("a payment enrollment already exists")
			}
			row, err = q.SetEnrollmentStatus(ctx, data.SetEnrollmentStatusParams{ID: row.ID, Status: "active"})
			if err != nil {
				return err
			}
		}
		payload, _ := json.Marshal(map[string]string{"user_id": studentID.String(), "organization_id": course.OrganizationID.String(), "enrollment_id": row.ID.String(), "course_id": courseID.String()})
		return q.InsertOutboxEvent(ctx, data.InsertOutboxEventParams{ID: s.ids.New(), AggregateType: "enrollment", AggregateID: row.ID, EventType: "enrollment.activated", Payload: payload, DeduplicationKey: "enrollment.activated:" + row.ID.String()})
	})
	if err != nil {
		return api.Enrollment{}, err
	}
	return asAPI(row), nil
}

func (s *Service) AdminEnroll(ctx context.Context, organizationID, courseID, studentID uuid.UUID) (api.Enrollment, error) {
	course, err := s.q.GetCourse(ctx, courseID)
	if err != nil {
		return api.Enrollment{}, err
	}
	if course.OrganizationID != organizationID {
		return api.Enrollment{}, ErrForbidden
	}
	isStudent, err := s.q.HasOrganizationRole(ctx, data.HasOrganizationRoleParams{OrganizationID: organizationID, UserID: studentID, RoleCode: "student"})
	if err != nil {
		return api.Enrollment{}, err
	}
	if !isStudent {
		return api.Enrollment{}, errors.New("student must have an active student membership in the organization")
	}
	var row data.Enrollment
	err = database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		row, err = q.CreateEnrollment(ctx, data.CreateEnrollmentParams{ID: s.ids.New(), OrganizationID: course.OrganizationID, CourseID: courseID, StudentID: studentID, Status: "active", Source: "admin", PriceMinorSnapshot: course.PriceMinor, CurrencySnapshot: course.Currency})
		if err != nil {
			return err
		}
		if row.Status != "active" && row.Status != "completed" {
			row, err = q.SetEnrollmentStatus(ctx, data.SetEnrollmentStatusParams{ID: row.ID, Status: "active"})
			if err != nil {
				return err
			}
		}
		payload, _ := json.Marshal(map[string]string{"user_id": studentID.String(), "organization_id": course.OrganizationID.String(), "enrollment_id": row.ID.String(), "course_id": courseID.String()})
		return q.InsertOutboxEvent(ctx, data.InsertOutboxEventParams{ID: s.ids.New(), AggregateType: "enrollment", AggregateID: row.ID, EventType: "enrollment.activated", Payload: payload, DeduplicationKey: "enrollment.activated:" + row.ID.String()})
	})
	if err != nil {
		return api.Enrollment{}, err
	}
	return asAPI(row), nil
}
func (s *Service) GetOwned(ctx context.Context, id, userID uuid.UUID, privileged bool) (data.Enrollment, error) {
	row, err := s.q.GetEnrollment(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return data.Enrollment{}, ErrNotFound
	}
	if err != nil {
		return data.Enrollment{}, err
	}
	if !privileged && row.StudentID != userID {
		return data.Enrollment{}, ErrForbidden
	}
	return row, nil
}
func (s *Service) Cancel(ctx context.Context, id, userID uuid.UUID, privileged bool) (api.Enrollment, error) {
	row, err := s.GetOwned(ctx, id, userID, privileged)
	if err != nil {
		return api.Enrollment{}, err
	}
	if row.Status == "completed" {
		return api.Enrollment{}, errors.New("completed enrollment cannot be cancelled")
	}
	updated, err := s.q.SetEnrollmentStatus(ctx, data.SetEnrollmentStatusParams{ID: id, Status: "cancelled"})
	if err != nil {
		return api.Enrollment{}, err
	}
	return asAPI(updated), nil
}

func (s *Service) UpdateProgress(ctx context.Context, enrollmentID, lessonID, userID uuid.UUID, in api.ProgressWrite) (api.Progress, error) {
	if in.PositionSeconds < 0 || in.PositionSeconds > math.MaxInt32 || in.WatchedSecondsDelta < 0 || in.WatchedSecondsDelta > 120 {
		return api.Progress{}, errors.New("invalid progress values")
	}
	enrollment, err := s.GetOwned(ctx, enrollmentID, userID, false)
	if err != nil {
		return api.Progress{}, err
	}
	if enrollment.Status != "active" {
		return api.Progress{}, errors.New("active enrollment is required")
	}
	lesson, err := s.q.GetLesson(ctx, lessonID)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.Progress{}, ErrNotFound
	}
	if err != nil {
		return api.Progress{}, err
	}
	course, err := s.q.GetCourseByLesson(ctx, lessonID)
	if err != nil {
		return api.Progress{}, err
	}
	if course.ID != enrollment.CourseID {
		return api.Progress{}, ErrForbidden
	}
	if lesson.DurationSeconds.Valid && in.PositionSeconds > int(lesson.DurationSeconds.Int32)+5 {
		return api.Progress{}, errors.New("position exceeds lesson duration")
	}
	existing, err := s.q.GetLessonProgress(ctx, data.GetLessonProgressParams{EnrollmentID: enrollmentID, LessonID: lessonID})
	existingFound := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return api.Progress{}, err
	}
	total := in.WatchedSecondsDelta
	if existingFound {
		total += int(existing.TotalWatchedSeconds)
	}
	if total > math.MaxInt32 {
		return api.Progress{}, errors.New("total watched time exceeds the supported range")
	}
	durationCap := int32(math.MaxInt32)
	if lesson.LessonType == "video" && lesson.DurationSeconds.Valid {
		durationCap = lesson.DurationSeconds.Int32
		if total > int(durationCap) {
			total = int(durationCap)
		}
	}
	manual := in.ManualComplete != nil && *in.ManualComplete
	threshold := 90.0
	if value, conversion := course.CompletionVideoThreshold.Float64Value(); conversion == nil && value.Valid {
		threshold = value.Float64
	}
	state, stateErr := progressState(lesson.LessonType, int(lesson.DurationSeconds.Int32), total, manual, existingFound && existing.State == "completed", threshold)
	if stateErr != nil {
		return api.Progress{}, stateErr
	}
	row, err := s.q.UpsertLessonProgress(ctx, data.UpsertLessonProgressParams{ID: s.ids.New(), EnrollmentID: enrollmentID, LessonID: lessonID, State: state, LastPositionSeconds: int32(in.PositionSeconds), TotalWatchedSeconds: int32(total), DurationCap: durationCap}) // #nosec G115 -- both values are bounded above
	if err != nil {
		return api.Progress{}, err
	}
	transitioned := state == "completed" && (!existingFound || !existing.CompletedAt.Valid)
	if transitioned {
		facts, calcErr := s.q.CalculateLessonCompletion(ctx, data.CalculateLessonCompletionParams{EnrollmentID: enrollmentID, CourseID: enrollment.CourseID})
		if calcErr == nil && facts.RequiredCount > 0 {
			percentage := math.Round(float64(facts.CompletedCount)/float64(facts.RequiredCount)*10000) / 100
			_, _ = s.q.UpdateEnrollmentCompletionPercentage(ctx, data.UpdateEnrollmentCompletionPercentageParams{ID: enrollmentID, CompletionPercentage: numeric(percentage)})
			_ = s.jobs.Enqueue(queue.TypeCompletionEvaluate, map[string]string{"enrollment_id": enrollmentID.String()})
		}
	}
	return api.Progress{LessonId: lessonID, State: api.ProgressState(row.State), PositionSeconds: int(row.LastPositionSeconds), TotalWatchedSeconds: int(row.TotalWatchedSeconds)}, nil
}

func numeric(v float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(fmt.Sprintf("%.2f", v))
	return n
}
func progressState(kind string, duration, total int, manual, alreadyComplete bool, threshold float64) (string, error) {
	if alreadyComplete {
		return "completed", nil
	}
	if manual {
		if kind == "video" {
			return "", errors.New("video lessons cannot be manually completed")
		}
		return "completed", nil
	}
	if kind == "video" && duration > 0 && float64(total) >= float64(duration)*threshold/100 {
		return "completed", nil
	}
	return "started", nil
}

func (s *Service) Resume(ctx context.Context, userID uuid.UUID) (data.ResumeLearningRow, error) {
	row, err := s.q.ResumeLearning(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return data.ResumeLearningRow{}, ErrNotFound
	}
	return row, err
}
