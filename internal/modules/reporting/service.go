package reporting

import (
	"context"
	"errors"
	"time"

	"github.com/Faysal9991/edtech_Backend/internal/data"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type Service struct{ q *data.Queries }

func NewService(q *data.Queries) *Service { return &Service{q: q} }

type Overview struct {
	TotalStudents        int64   `json:"total_students"`
	TotalInstructors     int64   `json:"total_instructors"`
	PublishedCourses     int64   `json:"published_courses"`
	ActiveEnrollments    int64   `json:"active_enrollments"`
	CompletedEnrollments int64   `json:"completed_enrollments"`
	CompletionRate       float64 `json:"completion_rate"`
	GrossRevenueMinor    int64   `json:"gross_revenue_minor"`
	RefundAmountMinor    int64   `json:"refund_amount_minor"`
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
	return Overview{TotalStudents: row.TotalStudents, TotalInstructors: row.TotalInstructors, PublishedCourses: row.PublishedCourses, ActiveEnrollments: row.ActiveEnrollments, CompletedEnrollments: row.CompletedEnrollments, CompletionRate: rate, GrossRevenueMinor: row.GrossRevenueMinor, RefundAmountMinor: row.RefundAmountMinor}, nil
}
