package assignment

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	api "github.com/neoscoder/lms-service/internal/api"
	"github.com/neoscoder/lms-service/internal/data"
	"github.com/neoscoder/lms-service/internal/platform/auth"
	"github.com/neoscoder/lms-service/internal/platform/httpx"
)

type Handler struct {
	s *Service
	q *data.Queries
}

func NewHandler(s *Service, q *data.Queries) *Handler { return &Handler{s: s, q: q} }
func fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Problem(w, r, 404, "Not Found", err.Error())
	case errors.Is(err, ErrForbidden):
		httpx.Problem(w, r, 403, "Forbidden", err.Error())
	case errors.Is(err, ErrSubmissionLimit):
		httpx.Problem(w, r, 409, "Submission Limit Reached", err.Error())
	default:
		httpx.Problem(w, r, 422, "Assignment Request Failed", err.Error())
	}
}
func assignmentDTO(a data.Assignment) map[string]any {
	return map[string]any{"id": a.ID, "organization_id": a.OrganizationID, "course_id": a.CourseID, "lesson_id": nullable(a.LessonID), "title": a.Title, "instructions": a.Instructions, "due_at": timeValue(a.DueAt), "maximum_score": decimal(a.MaximumScore), "passing_score": decimal(a.PassingScore), "allowed_file_types": a.AllowedFileTypes, "maximum_submissions": a.MaximumSubmissions, "is_required": a.IsRequired, "status": a.Status}
}
func submissionDTO(s data.AssignmentSubmission) map[string]any {
	return map[string]any{"id": s.ID, "assignment_id": s.AssignmentID, "enrollment_id": s.EnrollmentID, "student_id": s.StudentID, "submission_number": s.SubmissionNumber, "status": s.Status, "text_content": s.TextContent, "submitted_at": timeValue(s.SubmittedAt), "created_at": s.CreatedAt.Time, "updated_at": s.UpdatedAt.Time}
}

func (h *Handler) submissionWithAssets(ctx context.Context, submission data.AssignmentSubmission) (map[string]any, error) {
	out := submissionDTO(submission)
	assetIDs, err := h.q.ListSubmissionAssetIDs(ctx, submission.ID)
	if err != nil {
		return nil, err
	}
	out["media_asset_ids"] = assetIDs
	return out, nil
}
func nullable(v uuid.NullUUID) any {
	if v.Valid {
		return v.UUID
	}
	return nil
}
func timeValue(v pgtype.Timestamptz) any {
	if v.Valid {
		return v.Time
	}
	return nil
}
func (h *Handler) manager(r *http.Request, courseID uuid.UUID) bool {
	p, _ := auth.PrincipalFrom(r.Context())
	course, err := h.q.GetCourse(r.Context(), courseID)
	if err != nil {
		return false
	}
	m, ok := auth.MembershipFrom(r.Context())
	if !ok {
		row, e := h.q.GetActiveMembership(r.Context(), data.GetActiveMembershipParams{UserID: p.UserID, OrganizationID: course.OrganizationID})
		if e != nil {
			return false
		}
		m = auth.Membership{ID: row.ID, OrganizationID: row.OrganizationID, Roles: row.Roles}
	}
	if course.OrganizationID != m.OrganizationID {
		return false
	}
	if auth.HasRole(m.Roles, "organization_admin", "super_admin") {
		return true
	}
	if auth.HasRole(m.Roles, "instructor") {
		ok, _ := h.q.IsCourseInstructor(r.Context(), data.IsCourseInstructorParams{CourseID: courseID, InstructorID: p.UserID})
		return ok
	}
	return false
}
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	var in api.AssignmentWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	if !h.manager(r, in.CourseId) {
		fail(w, r, ErrForbidden)
		return
	}
	course, err := h.q.GetCourse(r.Context(), in.CourseId)
	if err != nil {
		fail(w, r, ErrNotFound)
		return
	}
	row, err := h.s.Create(r.Context(), course.OrganizationID, p.UserID, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	response, err := h.withAttachments(r.Context(), row)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 201, response)
}
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	courseID, err := httpx.UUIDParam(r.URL.Query().Get("course_id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Filter", "course_id is required")
		return
	}
	p, _ := auth.PrincipalFrom(r.Context())
	manager := h.manager(r, courseID)
	if !manager {
		enrollment, e := h.q.GetCourseEnrollment(r.Context(), data.GetCourseEnrollmentParams{CourseID: courseID, StudentID: p.UserID})
		if e != nil || (enrollment.Status != "active" && enrollment.Status != "completed") {
			fail(w, r, ErrForbidden)
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
	params := data.ListCourseAssignmentsParams{CourseID: courseID, IncludeUnpublished: manager, PageSize: size}
	if cursor != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListCourseAssignments(r.Context(), params)
	if err != nil {
		fail(w, r, err)
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, assignmentDTO(row))
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.CreatedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	row, err := h.q.GetAssignment(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, r, ErrNotFound)
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	p, _ := auth.PrincipalFrom(r.Context())
	if !h.manager(r, row.CourseID) {
		enrollment, e := h.q.GetCourseEnrollment(r.Context(), data.GetCourseEnrollmentParams{CourseID: row.CourseID, StudentID: p.UserID})
		if e != nil || (enrollment.Status != "active" && enrollment.Status != "completed") || row.Status != "published" {
			fail(w, r, ErrForbidden)
			return
		}
	}
	response, err := h.withAttachments(r.Context(), row)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, response)
}
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	existing, err := h.q.GetAssignment(r.Context(), id)
	if err != nil {
		fail(w, r, ErrNotFound)
		return
	}
	if !h.manager(r, existing.CourseID) {
		fail(w, r, ErrForbidden)
		return
	}
	var in api.AssignmentWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	if err := validate(in); err != nil {
		fail(w, r, err)
		return
	}
	p, _ := auth.PrincipalFrom(r.Context())
	row, err := h.s.Update(r.Context(), id, p.UserID, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	if status := r.URL.Query().Get("status"); status != "" {
		if status != "draft" && status != "published" && status != "archived" {
			httpx.Problem(w, r, 422, "Invalid Status", "status must be draft, published, or archived")
			return
		}
		row, err = h.q.SetAssignmentStatus(r.Context(), data.SetAssignmentStatusParams{ID: id, Status: status})
		if err != nil {
			fail(w, r, err)
			return
		}
	}
	response, err := h.withAttachments(r.Context(), row)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, response)
}

func (h *Handler) withAttachments(ctx context.Context, assignment data.Assignment) (map[string]any, error) {
	out := assignmentDTO(assignment)
	assetIDs, err := h.q.ListAssignmentAssetIDs(ctx, assignment.ID)
	if err != nil {
		return nil, err
	}
	out["attachment_asset_ids"] = assetIDs
	return out, nil
}
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	row, err := h.q.GetAssignment(r.Context(), id)
	if err != nil || !h.manager(r, row.CourseID) {
		fail(w, r, ErrForbidden)
		return
	}
	n, err := h.q.DeleteAssignment(r.Context(), id)
	if err != nil || n == 0 {
		httpx.Problem(w, r, 409, "Delete Failed", "only a draft assignment may be deleted")
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) CreateSubmission(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	var in api.SubmissionWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	row, err := h.s.CreateSubmission(r.Context(), id, p.UserID, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	response, err := h.submissionWithAssets(r.Context(), row)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 201, response)
}
func (h *Handler) ListSubmissions(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	assignmentID, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	assignment, err := h.q.GetAssignment(r.Context(), assignmentID)
	if err != nil {
		fail(w, r, ErrNotFound)
		return
	}
	params := data.ListAssignmentSubmissionsParams{AssignmentID: assignmentID}
	if !h.manager(r, assignment.CourseID) {
		params.StudentID = uuid.NullUUID{UUID: p.UserID, Valid: true}
	}
	size, err := httpx.PageSize(r)
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Pagination", err.Error())
		return
	}
	params.PageSize = size
	cursor, err := httpx.ParseCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Pagination", err.Error())
		return
	}
	if cursor != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListAssignmentSubmissions(r.Context(), params)
	if err != nil {
		fail(w, r, err)
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, submissionDTO(row))
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.CreatedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}
func (h *Handler) GetSubmission(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	row, err := h.q.GetAssignmentSubmission(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, r, ErrNotFound)
		return
	}
	assignment, err := h.q.GetAssignment(r.Context(), row.AssignmentID)
	if err != nil {
		fail(w, r, err)
		return
	}
	if row.StudentID != p.UserID && !h.manager(r, assignment.CourseID) {
		fail(w, r, ErrForbidden)
		return
	}
	response, err := h.submissionWithAssets(r.Context(), row)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, response)
}
func (h *Handler) UpdateSubmission(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	existing, err := h.q.GetAssignmentSubmission(r.Context(), id)
	if err != nil {
		fail(w, r, ErrNotFound)
		return
	}
	if existing.StudentID != p.UserID {
		fail(w, r, ErrForbidden)
		return
	}
	var in api.SubmissionWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	row, err := h.s.UpdateSubmission(r.Context(), id, p.UserID, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	response, err := h.submissionWithAssets(r.Context(), row)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, response)
}
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	row, err := h.s.Submit(r.Context(), id, p.UserID)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, submissionDTO(row))
}
func (h *Handler) Grade(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	submission, err := h.q.GetAssignmentSubmission(r.Context(), id)
	if err != nil {
		fail(w, r, ErrNotFound)
		return
	}
	assignment, err := h.q.GetAssignment(r.Context(), submission.AssignmentID)
	if err != nil || !h.manager(r, assignment.CourseID) {
		fail(w, r, ErrForbidden)
		return
	}
	var in api.GradeWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	feedback := ""
	if in.Feedback != nil {
		feedback = *in.Feedback
	}
	p, _ := auth.PrincipalFrom(r.Context())
	grade, err := h.s.Grade(r.Context(), id, p.UserID, float64(in.Points), feedback, r.URL.Query().Get("return_for_resubmission") == "true")
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"id": grade.ID, "points": decimal(grade.Points), "percentage": decimal(grade.Percentage), "feedback": grade.Feedback})
}
