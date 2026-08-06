package reporting

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/neoscoder/lms-service/internal/data"
)

type Service struct{ q *data.Queries }

func NewService(q *data.Queries) *Service { return &Service{q: q} }

type Overview struct {
	TotalStudents        int64   `json:"total_students"`
	TotalInstructors     int64   `json:"total_instructors"`
	PublishedCourses     int64   `json:"published_courses"`
	TotalEnrollments     int64   `json:"total_enrollments"`
	ActiveEnrollments    int64   `json:"active_enrollments"`
	CompletedEnrollments int64   `json:"completed_enrollments"`
	CompletionRate       float64 `json:"completion_rate"`
	PendingPayments      int64   `json:"pending_payments"`
	PaidPayments         int64   `json:"paid_payments"`
	FailedPayments       int64   `json:"failed_payments"`
	RefundedPayments     int64   `json:"refunded_payments"`
	GrossRevenueMinor    int64   `json:"gross_revenue_minor"`
	RefundAmountMinor    int64   `json:"refund_amount_minor"`
}

type TeacherOverview struct {
	TotalCourses                int64   `json:"total_courses"`
	PublishedCourses            int64   `json:"published_courses"`
	TotalStudents               int64   `json:"total_students"`
	ActiveEnrollments           int64   `json:"active_enrollments"`
	CompletedEnrollments        int64   `json:"completed_enrollments"`
	AverageCompletionPercentage float64 `json:"average_completion_percentage"`
	AverageQuizPercentage       float64 `json:"average_quiz_percentage"`
	AssignmentSubmissions       int64   `json:"assignment_submissions"`
	GradedAssignments           int64   `json:"graded_assignments"`
}

func Range(from, to *time.Time) (time.Time, time.Time, error) {
	end := time.Now().UTC()
	start := end.AddDate(0, -1, 0)
	if from != nil {
		start = from.UTC()
	}
	if to != nil {
		end = to.UTC()
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, errors.New("report end must be after start")
	}
	if end.Sub(start) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, errors.New("report range cannot exceed 366 days")
	}
	return start, end, nil
}
func (s *Service) Overview(ctx context.Context, orgID uuid.UUID, from, to time.Time) (Overview, error) {
	row, err := s.q.OrganizationOverview(ctx, data.OrganizationOverviewParams{OrganizationID: orgID, FromTime: pgtype.Timestamptz{Time: from, Valid: true}, ToTime: pgtype.Timestamptz{Time: to, Valid: true}})
	if err != nil {
		return Overview{}, err
	}
	rate := 0.0
	total := row.ActiveEnrollments + row.CompletedEnrollments
	if total > 0 {
		rate = float64(row.CompletedEnrollments) / float64(total) * 100
	}
	return Overview{TotalStudents: row.TotalStudents, TotalInstructors: row.TotalInstructors, PublishedCourses: row.PublishedCourses, TotalEnrollments: row.TotalEnrollments, ActiveEnrollments: row.ActiveEnrollments, CompletedEnrollments: row.CompletedEnrollments, CompletionRate: rate, PendingPayments: row.PendingPayments, PaidPayments: row.PaidPayments, FailedPayments: row.FailedPayments, RefundedPayments: row.RefundedPayments, GrossRevenueMinor: row.GrossRevenueMinor, RefundAmountMinor: row.RefundAmountMinor}, nil
}

func (s *Service) TeacherOverview(ctx context.Context, orgID, instructorID uuid.UUID, from, to time.Time) (TeacherOverview, error) {
	row, err := s.q.TeacherOverview(ctx, data.TeacherOverviewParams{OrganizationID: orgID, InstructorID: instructorID, FromTime: pgtype.Timestamptz{Time: from, Valid: true}, ToTime: pgtype.Timestamptz{Time: to, Valid: true}})
	if err != nil {
		return TeacherOverview{}, err
	}
	average, conversionErr := row.AverageCompletionPercentage.Float64Value()
	if conversionErr != nil {
		return TeacherOverview{}, conversionErr
	}
	quizAverage, conversionErr := row.AverageQuizPercentage.Float64Value()
	if conversionErr != nil {
		return TeacherOverview{}, conversionErr
	}
	return TeacherOverview{
		TotalCourses:                row.TotalCourses,
		PublishedCourses:            row.PublishedCourses,
		TotalStudents:               row.TotalStudents,
		ActiveEnrollments:           row.ActiveEnrollments,
		CompletedEnrollments:        row.CompletedEnrollments,
		AverageCompletionPercentage: average.Float64,
		AverageQuizPercentage:       quizAverage.Float64,
		AssignmentSubmissions:       row.AssignmentSubmissions,
		GradedAssignments:           row.GradedAssignments,
	}, nil
}
