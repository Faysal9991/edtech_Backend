package enrollment

import (
	"errors"
	"net/http"

	api "github.com/Faysal9991/edtech_Backend/internal/api"
	"github.com/Faysal9991/edtech_Backend/internal/data"
	"github.com/Faysal9991/edtech_Backend/internal/platform/auth"
	"github.com/Faysal9991/edtech_Backend/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type Handler struct {
	s *Service
	q *data.Queries
}

func NewHandler(s *Service, q *data.Queries) *Handler { return &Handler{s: s, q: q} }
func dto(e data.Enrollment) map[string]any {
	pct, _ := e.CompletionPercentage.Float64Value()
	return map[string]any{"id": e.ID, "organization_id": e.OrganizationID, "course_id": e.CourseID, "student_id": e.StudentID, "status": e.Status, "source": e.Source, "price_minor_snapshot": e.PriceMinorSnapshot, "currency_snapshot": e.CurrencySnapshot, "completion_percentage": pct.Float64, "enrolled_at": timeValue(e.EnrolledAt), "completed_at": timeValue(e.CompletedAt), "expires_at": timeValue(e.ExpiresAt), "created_at": timeValue(e.CreatedAt), "updated_at": timeValue(e.UpdatedAt)}
}
func timeValue(v pgtype.Timestamptz) any {
	if v.Valid {
		return v.Time
	}
	return nil
}
func fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Problem(w, r, 404, "Not Found", err.Error())
	case errors.Is(err, ErrForbidden):
		httpx.Problem(w, r, 403, "Forbidden", err.Error())
	case errors.Is(err, ErrPaymentRequired):
		httpx.Problem(w, r, 402, "Payment Required", err.Error())
	case errors.Is(err, ErrUnavailable):
		httpx.Problem(w, r, 409, "Enrollment Unavailable", err.Error())
	default:
		httpx.Problem(w, r, 422, "Enrollment Failed", err.Error())
	}
}
func (h *Handler) Enroll(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	courseID, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	result, err := h.s.EnrollFree(r.Context(), courseID, p.UserID)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 201, result)
}
func (h *Handler) AdminEnroll(w http.ResponseWriter, r *http.Request) {
	m, _ := auth.MembershipFrom(r.Context())
	courseID, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	studentID, err := httpx.UUIDParam(r.URL.Query().Get("student_id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Student", "student_id must be a UUID")
		return
	}
	result, err := h.s.AdminEnroll(r.Context(), m.OrganizationID, courseID, studentID)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 201, result)
}
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	size, err := httpx.PageSize(r)
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Pagination", err.Error())
		return
	}
	cursor, err := httpx.ParseCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Pagination", err.Error())
		return
	}
	params := data.ListStudentEnrollmentsParams{StudentID: p.UserID, PageSize: size}
	if cursor != nil {
		params.CursorUpdatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListStudentEnrollments(r.Context(), params)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to list enrollments")
		return
	}
	items := make([]any, 0, len(rows))
	for _, e := range rows {
		pct, _ := e.CompletionPercentage.Float64Value()
		items = append(items, map[string]any{"id": e.ID, "course_id": e.CourseID, "course_title": e.CourseTitle, "course_slug": e.CourseSlug, "status": e.Status, "completion_percentage": pct.Float64, "updated_at": e.UpdatedAt.Time})
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.UpdatedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	row, err := h.s.GetOwned(r.Context(), id, p.UserID, h.privileged(r, id, p.UserID))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, dto(row))
}
func (h *Handler) privileged(r *http.Request, enrollmentID, userID uuid.UUID) bool {
	row, err := h.q.GetEnrollment(r.Context(), enrollmentID)
	if err != nil {
		return false
	}
	membership, err := h.q.GetActiveMembership(r.Context(), data.GetActiveMembershipParams{UserID: userID, OrganizationID: row.OrganizationID})
	if err != nil {
		return false
	}
	if auth.HasRole(membership.Roles, "organization_admin", "super_admin") {
		return true
	}
	if auth.HasRole(membership.Roles, "instructor") {
		assigned, _ := h.q.IsCourseInstructor(r.Context(), data.IsCourseInstructorParams{CourseID: row.CourseID, InstructorID: userID})
		return assigned
	}
	return false
}
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	result, err := h.s.Cancel(r.Context(), id, p.UserID, h.privileged(r, id, p.UserID))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, result)
}
func (h *Handler) UpdateProgress(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	enrollmentID, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	lessonID, err := httpx.UUIDParam(chi.URLParam(r, "lessonId"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Lesson", err.Error())
		return
	}
	var in api.ProgressWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	result, err := h.s.UpdateProgress(r.Context(), enrollmentID, lessonID, p.UserID, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, result)
}
func (h *Handler) Progress(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	if _, err := h.s.GetOwned(r.Context(), id, p.UserID, h.privileged(r, id, p.UserID)); err != nil {
		fail(w, r, err)
		return
	}
	size, err := httpx.PageSize(r)
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Pagination", err.Error())
		return
	}
	cursor, err := httpx.ParseCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Pagination", err.Error())
		return
	}
	params := data.ListEnrollmentProgressParams{EnrollmentID: id, PageSize: size}
	if cursor != nil {
		params.CursorUpdatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListEnrollmentProgress(r.Context(), params)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to load progress")
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"lesson_id": row.LessonID, "lesson_title": row.LessonTitle, "lesson_type": row.LessonType, "state": row.State, "position_seconds": row.LastPositionSeconds, "total_watched_seconds": row.TotalWatchedSeconds, "updated_at": row.UpdatedAt.Time})
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.UpdatedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}
func (h *Handler) Resume(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	row, err := h.s.Resume(r.Context(), p.UserID)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"enrollment_id": row.EnrollmentID, "course_id": row.CourseID, "course_title": row.CourseTitle, "lesson_id": row.LessonID, "lesson_title": row.LessonTitle, "position_seconds": row.LastPositionSeconds, "updated_at": row.UpdatedAt.Time})
}
func (h *Handler) CourseStudents(w http.ResponseWriter, r *http.Request) {
	courseID, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	m, _ := auth.MembershipFrom(r.Context())
	p, _ := auth.PrincipalFrom(r.Context())
	course, err := h.q.GetCourse(r.Context(), courseID)
	if err != nil || course.OrganizationID != m.OrganizationID {
		httpx.Problem(w, r, 404, "Not Found", "course not found")
		return
	}
	if auth.HasRole(m.Roles, "instructor") && !auth.HasRole(m.Roles, "organization_admin", "super_admin") {
		assigned, _ := h.q.IsCourseInstructor(r.Context(), data.IsCourseInstructorParams{CourseID: courseID, InstructorID: p.UserID})
		if !assigned {
			httpx.Problem(w, r, 403, "Forbidden", "instructor is not assigned to this course")
			return
		}
	}
	size, err := httpx.PageSize(r)
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Pagination", err.Error())
		return
	}
	cursor, err := httpx.ParseCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Pagination", err.Error())
		return
	}
	params := data.ListCourseEnrollmentsParams{CourseID: courseID, PageSize: size}
	if cursor != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListCourseEnrollments(r.Context(), params)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to list students")
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"id": row.ID, "student_id": row.StudentID, "email": row.Email, "display_name": row.DisplayName, "status": row.Status, "created_at": row.CreatedAt.Time})
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.CreatedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}
