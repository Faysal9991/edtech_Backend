package certificate

import (
	"errors"
	"net/http"

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
	params := data.ListUserCertificatesParams{StudentID: p.UserID, PageSize: size}
	if cursor != nil {
		params.CursorIssuedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListUserCertificates(r.Context(), params)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to list certificates")
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"id": row.ID, "course_id": row.CourseID, "certificate_number": row.CertificateNumber, "status": row.Status, "issued_at": row.IssuedAt.Time})
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.IssuedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	url, err := h.s.DownloadURL(r.Context(), id, p.UserID)
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Problem(w, r, 404, "Not Found", err.Error())
	case errors.Is(err, ErrForbidden):
		httpx.Problem(w, r, 403, "Forbidden", err.Error())
	case err != nil:
		httpx.Problem(w, r, 409, "Certificate Unavailable", err.Error())
	default:
		http.Redirect(w, r, url, http.StatusFound)
	}
}
func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	result, err := h.s.Verify(r.Context(), chi.URLParam(r, "code"))
	if errors.Is(err, ErrNotFound) {
		httpx.Problem(w, r, 404, "Not Found", "certificate not found")
		return
	}
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to verify certificate")
		return
	}
	httpx.JSON(w, 200, result)
}
func (h *Handler) Results(w http.ResponseWriter, r *http.Request) {
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
	params := data.MyResultsParams{StudentID: p.UserID, PageSize: size}
	if cursor != nil {
		params.CursorUpdatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.MyResults(r.Context(), params)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to load results")
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		completion, _ := row.CompletionPercentage.Float64Value()
		quiz, _ := row.QuizAverage.Float64Value()
		assignment, _ := row.AssignmentAverage.Float64Value()
		items = append(items, map[string]any{"enrollment_id": row.EnrollmentID, "course_id": row.CourseID, "course_title": row.CourseTitle, "status": row.Status, "completion_percentage": completion.Float64, "quiz_average": quiz.Float64, "assignment_average": assignment.Float64, "completed_at": row.CompletedAt.Time})
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.UpdatedAt.Time, last.EnrollmentID)
	}
	httpx.JSON(w, 200, response)
}
