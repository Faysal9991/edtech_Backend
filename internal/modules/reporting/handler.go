package reporting

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/neoscoder/lms-service/internal/data"
	"github.com/neoscoder/lms-service/internal/platform/auth"
	"github.com/neoscoder/lms-service/internal/platform/httpx"
)

type Handler struct {
	s *Service
	q *data.Queries
}

func NewHandler(s *Service, q *data.Queries) *Handler { return &Handler{s: s, q: q} }
func parseRange(w http.ResponseWriter, r *http.Request) (time.Time, time.Time, bool) {
	var from, to *time.Time
	if raw := r.URL.Query().Get("from"); raw != "" {
		v, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			httpx.Problem(w, r, 400, "Invalid Range", "from must be RFC3339")
			return time.Time{}, time.Time{}, false
		}
		from = &v
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		v, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			httpx.Problem(w, r, 400, "Invalid Range", "to must be RFC3339")
			return time.Time{}, time.Time{}, false
		}
		to = &v
	}
	start, end, err := Range(from, to)
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Range", err.Error())
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	m, _ := auth.MembershipFrom(r.Context())
	from, to, ok := parseRange(w, r)
	if !ok {
		return
	}
	result, err := h.s.Overview(r.Context(), m.OrganizationID, from, to)
	if err != nil {
		httpx.Problem(w, r, 500, "Report Failed", err.Error())
		return
	}
	httpx.JSON(w, 200, result)
}
func (h *Handler) TeacherOverview(w http.ResponseWriter, r *http.Request) {
	membership, ok := auth.MembershipFrom(r.Context())
	if !ok {
		httpx.Problem(w, r, 403, "Organization Required", "an active organization membership is required")
		return
	}
	principal, _ := auth.PrincipalFrom(r.Context())
	from, to, valid := parseRange(w, r)
	if !valid {
		return
	}
	result, err := h.s.TeacherOverview(r.Context(), membership.OrganizationID, principal.UserID, from, to)
	if err != nil {
		httpx.Problem(w, r, 500, "Report Failed", err.Error())
		return
	}
	httpx.JSON(w, 200, result)
}
func (h *Handler) Enrollments(w http.ResponseWriter, r *http.Request) {
	m, _ := auth.MembershipFrom(r.Context())
	from, to, ok := parseRange(w, r)
	if !ok {
		return
	}
	rows, err := h.q.EnrollmentTrend(r.Context(), data.EnrollmentTrendParams{OrganizationID: m.OrganizationID, CreatedAt: pgtype.Timestamptz{Time: from, Valid: true}, CreatedAt_2: pgtype.Timestamptz{Time: to, Valid: true}})
	if err != nil {
		httpx.Problem(w, r, 500, "Report Failed", err.Error())
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": rows})
}
func (h *Handler) Completions(w http.ResponseWriter, r *http.Request) {
	m, _ := auth.MembershipFrom(r.Context())
	from, to, ok := parseRange(w, r)
	if !ok {
		return
	}
	rows, err := h.q.CompletionByCourse(r.Context(), data.CompletionByCourseParams{FromTime: pgtype.Timestamptz{Time: from, Valid: true}, ToTime: pgtype.Timestamptz{Time: to, Valid: true}, OrganizationID: m.OrganizationID, PageSize: 100})
	if err != nil {
		httpx.Problem(w, r, 500, "Report Failed", err.Error())
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": rows})
}
func (h *Handler) Assessments(w http.ResponseWriter, r *http.Request) {
	m, _ := auth.MembershipFrom(r.Context())
	from, to, ok := parseRange(w, r)
	if !ok {
		return
	}
	rows, err := h.q.AssessmentReport(r.Context(), data.AssessmentReportParams{FromTime: pgtype.Timestamptz{Time: from, Valid: true}, ToTime: pgtype.Timestamptz{Time: to, Valid: true}, OrganizationID: m.OrganizationID, PageSize: 100})
	if err != nil {
		httpx.Problem(w, r, 500, "Report Failed", err.Error())
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": rows})
}
func (h *Handler) LiveAttendance(w http.ResponseWriter, r *http.Request) {
	m, _ := auth.MembershipFrom(r.Context())
	from, to, ok := parseRange(w, r)
	if !ok {
		return
	}
	rows, err := h.q.LiveAttendanceReport(r.Context(), data.LiveAttendanceReportParams{OrganizationID: m.OrganizationID, ScheduledStartAt: pgtype.Timestamptz{Time: from, Valid: true}, ScheduledStartAt_2: pgtype.Timestamptz{Time: to, Valid: true}, Limit: 100})
	if err != nil {
		httpx.Problem(w, r, 500, "Report Failed", err.Error())
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": rows})
}
func (h *Handler) Revenue(w http.ResponseWriter, r *http.Request) {
	m, _ := auth.MembershipFrom(r.Context())
	from, to, ok := parseRange(w, r)
	if !ok {
		return
	}
	rows, err := h.q.RevenueByCourse(r.Context(), data.RevenueByCourseParams{OrganizationID: m.OrganizationID, CreatedAt: pgtype.Timestamptz{Time: from, Valid: true}, CreatedAt_2: pgtype.Timestamptz{Time: to, Valid: true}, Limit: 100})
	if err != nil {
		httpx.Problem(w, r, 500, "Report Failed", err.Error())
		return
	}
	trend, err := h.q.RevenueTrend(r.Context(), data.RevenueTrendParams{OrganizationID: m.OrganizationID, FromTime: pgtype.Timestamptz{Time: from, Valid: true}, ToTime: pgtype.Timestamptz{Time: to, Valid: true}})
	if err != nil {
		httpx.Problem(w, r, 500, "Report Failed", err.Error())
		return
	}
	httpx.JSON(w, 200, map[string]any{"by_course": rows, "trend": trend})
}
