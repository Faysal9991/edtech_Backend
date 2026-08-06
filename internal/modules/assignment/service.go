package assignment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

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
	ErrNotFound        = errors.New("assignment not found")
	ErrForbidden       = errors.New("assignment access denied")
	ErrSubmissionLimit = errors.New("assignment submission limit reached")
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
func numeric(v float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(fmt.Sprintf("%.2f", v))
	return n
}
func decimal(v pgtype.Numeric) float64 {
	f, _ := v.Float64Value()
	if f.Valid {
		return f.Float64
	}
	return 0
}
func nullableUUID(v *uuid.UUID) uuid.NullUUID {
	if v == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *v, Valid: *v != uuid.Nil}
}
func nullableTime(v *time.Time) pgtype.Timestamptz {
	if v == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: v.UTC(), Valid: true}
}
func value(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func boolValue(v *bool) bool { return v != nil && *v }
func stringsValue(v *[]string) []string {
	if v == nil {
		return []string{}
	}
	return append([]string(nil), (*v)...)
}

func validate(in api.AssignmentWrite) error {
	if strings.TrimSpace(in.Title) == "" || in.MaximumScore <= 0 || in.PassingScore < 0 || in.PassingScore > in.MaximumScore || in.MaximumSubmissions < 1 || in.MaximumSubmissions > 1000 {
		return errors.New("invalid assignment settings")
	}
	return nil
}
func (s *Service) Create(ctx context.Context, orgID, authorID uuid.UUID, in api.AssignmentWrite) (data.Assignment, error) {
	if err := validate(in); err != nil {
		return data.Assignment{}, err
	}
	var assignment data.Assignment
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		assignment, err = q.CreateAssignment(ctx, data.CreateAssignmentParams{ID: s.ids.New(), OrganizationID: orgID, CourseID: in.CourseId, LessonID: nullableUUID(in.LessonId), Title: strings.TrimSpace(in.Title), Instructions: value(in.Instructions), DueAt: nullableTime(in.DueAt), MaximumScore: numeric(float64(in.MaximumScore)), PassingScore: numeric(float64(in.PassingScore)), AllowedFileTypes: stringsValue(in.AllowedFileTypes), MaximumSubmissions: int32(in.MaximumSubmissions), IsRequired: boolValue(in.IsRequired)}) // #nosec G115 -- validated to 1..1000
		if err != nil {
			return err
		}
		return s.replaceAssets(ctx, q, assignment.ID, orgID, authorID, in.AttachmentAssetIds)
	})
	return assignment, err
}

func (s *Service) Update(ctx context.Context, id, authorID uuid.UUID, in api.AssignmentWrite) (data.Assignment, error) {
	if err := validate(in); err != nil {
		return data.Assignment{}, err
	}
	var assignment data.Assignment
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		existing, err := q.GetAssignment(ctx, id)
		if err != nil {
			return err
		}
		assignment, err = q.UpdateAssignment(ctx, data.UpdateAssignmentParams{Title: strings.TrimSpace(in.Title), Instructions: value(in.Instructions), DueAt: nullableTime(in.DueAt), MaximumScore: numeric(float64(in.MaximumScore)), PassingScore: numeric(float64(in.PassingScore)), AllowedFileTypes: stringsValue(in.AllowedFileTypes), MaximumSubmissions: int32(in.MaximumSubmissions), IsRequired: boolValue(in.IsRequired), ID: id}) // #nosec G115 -- validated to 1..1000
		if err != nil || in.AttachmentAssetIds == nil {
			return err
		}
		if err := q.DeleteAssignmentAssets(ctx, id); err != nil {
			return err
		}
		return s.replaceAssets(ctx, q, id, existing.OrganizationID, authorID, in.AttachmentAssetIds)
	})
	return assignment, err
}

func (s *Service) replaceAssets(ctx context.Context, q *data.Queries, assignmentID, orgID, ownerID uuid.UUID, assetIDs *[]uuid.UUID) error {
	if assetIDs == nil {
		return nil
	}
	seen := make(map[uuid.UUID]struct{}, len(*assetIDs))
	for _, assetID := range *assetIDs {
		if assetID == uuid.Nil {
			return errors.New("attachment asset ID is invalid")
		}
		if _, duplicate := seen[assetID]; duplicate {
			continue
		}
		seen[assetID] = struct{}{}
		asset, err := q.GetMediaAsset(ctx, assetID)
		if err != nil {
			return errors.New("attachment asset was not found")
		}
		if asset.OrganizationID != orgID || asset.OwnerUserID != ownerID || asset.Status != "ready" || asset.Kind != "assignment" {
			return errors.New("attachment must be an owned, ready assignment asset in this organization")
		}
		if err := q.AddAssignmentAsset(ctx, data.AddAssignmentAssetParams{AssignmentID: assignmentID, MediaAssetID: assetID}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) CreateSubmission(ctx context.Context, assignmentID, studentID uuid.UUID, in api.SubmissionWrite) (data.AssignmentSubmission, error) {
	assignment, err := s.q.GetAssignment(ctx, assignmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return data.AssignmentSubmission{}, ErrNotFound
	}
	if err != nil {
		return data.AssignmentSubmission{}, err
	}
	if assignment.Status != "published" {
		return data.AssignmentSubmission{}, errors.New("assignment is not published")
	}
	enrollment, err := s.q.GetCourseEnrollment(ctx, data.GetCourseEnrollmentParams{CourseID: assignment.CourseID, StudentID: studentID})
	if err != nil || enrollment.Status != "active" {
		return data.AssignmentSubmission{}, ErrForbidden
	}
	var submission data.AssignmentSubmission
	err = database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		number, err := q.NextAssignmentSubmissionNumber(ctx, data.NextAssignmentSubmissionNumberParams{AssignmentID: assignmentID, StudentID: studentID})
		if err != nil {
			return err
		}
		if number > assignment.MaximumSubmissions {
			return ErrSubmissionLimit
		}
		submission, err = q.CreateAssignmentSubmission(ctx, data.CreateAssignmentSubmissionParams{ID: s.ids.New(), AssignmentID: assignmentID, EnrollmentID: enrollment.ID, StudentID: studentID, SubmissionNumber: number, TextContent: value(in.TextContent)})
		if err != nil {
			return err
		}
		return s.addSubmissionAssets(ctx, q, submission.ID, studentID, assignment, in.MediaAssetIds)
	})
	return submission, err
}

func (s *Service) addSubmissionAssets(ctx context.Context, q *data.Queries, submissionID, studentID uuid.UUID, assignment data.Assignment, assetIDs *[]uuid.UUID) error {
	if assetIDs == nil {
		return nil
	}
	seen := map[uuid.UUID]struct{}{}
	for _, assetID := range *assetIDs {
		if _, duplicate := seen[assetID]; duplicate {
			continue
		}
		seen[assetID] = struct{}{}
		asset, err := q.GetMediaAsset(ctx, assetID)
		if err != nil {
			return errors.New("submission asset was not found")
		}
		if asset.OwnerUserID != studentID || asset.OrganizationID != assignment.OrganizationID || asset.Status != "ready" || asset.Kind != "assignment" {
			return errors.New("submission asset is not an owned ready assignment file")
		}
		allowed := false
		for _, mime := range assignment.AllowedFileTypes {
			if mime == asset.ContentType {
				allowed = true
			}
		}
		if len(assignment.AllowedFileTypes) > 0 && !allowed {
			return errors.New("submission asset type is not allowed")
		}
		if err := q.AddSubmissionAsset(ctx, data.AddSubmissionAssetParams{SubmissionID: submissionID, MediaAssetID: assetID}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) UpdateSubmission(ctx context.Context, id, studentID uuid.UUID, in api.SubmissionWrite) (data.AssignmentSubmission, error) {
	var submission data.AssignmentSubmission
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		locked, err := q.GetAssignmentSubmissionForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if locked.StudentID != studentID {
			return ErrForbidden
		}
		assignment, err := q.GetAssignment(ctx, locked.AssignmentID)
		if err != nil {
			return err
		}
		submission, err = q.UpdateAssignmentSubmissionDraft(ctx, data.UpdateAssignmentSubmissionDraftParams{ID: id, TextContent: value(in.TextContent)})
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("only draft or returned submissions are editable")
		}
		if err != nil || in.MediaAssetIds == nil {
			return err
		}
		if err := q.DeleteSubmissionAssets(ctx, id); err != nil {
			return err
		}
		return s.addSubmissionAssets(ctx, q, id, studentID, assignment, in.MediaAssetIds)
	})
	return submission, err
}

func (s *Service) Submit(ctx context.Context, id, studentID uuid.UUID) (data.AssignmentSubmission, error) {
	var result data.AssignmentSubmission
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		sub, err := q.GetAssignmentSubmissionForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if sub.StudentID != studentID {
			return ErrForbidden
		}
		if sub.Status == "submitted" || sub.Status == "late" || sub.Status == "graded" {
			result = sub
			return nil
		}
		assignment, err := q.GetAssignment(ctx, sub.AssignmentID)
		if err != nil {
			return err
		}
		status := "submitted"
		if assignment.DueAt.Valid && s.clock.Now().After(assignment.DueAt.Time) {
			status = "late"
		}
		result, err = q.SubmitAssignmentSubmission(ctx, data.SubmitAssignmentSubmissionParams{ID: id, Status: status})
		return err
	})
	return result, err
}

func (s *Service) Grade(ctx context.Context, id, graderID uuid.UUID, points float64, feedback string, returnForResubmission bool) (data.Grade, error) {
	var grade data.Grade
	var enrollmentID uuid.UUID
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		sub, err := q.GetAssignmentSubmissionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if sub.Status != "submitted" && sub.Status != "late" && sub.Status != "graded" {
			return errors.New("submission is not ready for grading")
		}
		assignment, err := q.GetAssignment(ctx, sub.AssignmentID)
		if err != nil {
			return err
		}
		maximum := decimal(assignment.MaximumScore)
		if points < 0 || points > maximum {
			return errors.New("grade points exceed assignment maximum")
		}
		percentage := math.Round(points/maximum*10000) / 100
		grade, err = q.UpsertAssignmentGrade(ctx, data.UpsertAssignmentGradeParams{ID: s.ids.New(), AssignmentSubmissionID: uuid.NullUUID{UUID: id, Valid: true}, StudentID: sub.StudentID, GradedBy: graderID, Points: numeric(points), Percentage: numeric(percentage), Feedback: feedback})
		if err != nil {
			return err
		}
		status := "graded"
		if returnForResubmission {
			status = "returned"
		}
		_, err = q.SetAssignmentSubmissionStatus(ctx, data.SetAssignmentSubmissionStatusParams{ID: id, Status: status})
		if err != nil {
			return err
		}
		enrollmentID = sub.EnrollmentID
		payload, _ := json.Marshal(map[string]string{"user_id": sub.StudentID.String(), "organization_id": assignment.OrganizationID.String(), "assignment_id": assignment.ID.String(), "submission_id": sub.ID.String()})
		return q.InsertOutboxEvent(ctx, data.InsertOutboxEventParams{ID: s.ids.New(), AggregateType: "assignment", AggregateID: assignment.ID, EventType: "assignment.graded", Payload: payload, DeduplicationKey: "assignment.graded:" + sub.ID.String() + ":" + status})
	})
	if err == nil && enrollmentID != uuid.Nil {
		_ = s.jobs.Enqueue(queue.TypeCompletionEvaluate, map[string]string{"enrollment_id": enrollmentID.String()})
	}
	return grade, err
}
