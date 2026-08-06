package liveclass

import (
	"errors"
	"net/http"
	"strings"

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
	default:
		httpx.Problem(w, r, 422, "Live Session Failed", err.Error())
	}
}
func dto(v data.LiveSession) map[string]any {
	return map[string]any{"id": v.ID, "organization_id": v.OrganizationID, "course_id": v.CourseID, "title": v.Title, "description": v.Description, "status": v.Status, "scheduled_start_at": v.ScheduledStartAt.Time, "scheduled_end_at": v.ScheduledEndAt.Time, "started_at": timeValue(v.StartedAt), "ended_at": timeValue(v.EndedAt)}
}
func timeValue(v pgtype.Timestamptz) any {
	if v.Valid {
		return v.Time
	}
	return nil
}
func (h *Handler) manager(r *http.Request, session data.LiveSession) bool {
	p, _ := auth.PrincipalFrom(r.Context())
	membership, err := h.q.GetActiveMembership(r.Context(), data.GetActiveMembershipParams{UserID: p.UserID, OrganizationID: session.OrganizationID})
	if err != nil {
		return false
	}
	if auth.HasRole(membership.Roles, "organization_admin", "super_admin") {
		return true
	}
	if auth.HasRole(membership.Roles, "instructor") {
		assigned, _ := h.q.IsCourseInstructor(r.Context(), data.IsCourseInstructorParams{CourseID: session.CourseID, InstructorID: p.UserID})
		return assigned
	}
	return false
}
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	m, _ := auth.MembershipFrom(r.Context())
	p, _ := auth.PrincipalFrom(r.Context())
	var in api.LiveSessionWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	course, err := h.q.GetCourse(r.Context(), in.CourseId)
	if err != nil || course.OrganizationID != m.OrganizationID {
		fail(w, r, ErrForbidden)
		return
	}
	if auth.HasRole(m.Roles, "instructor") && !auth.HasRole(m.Roles, "organization_admin", "super_admin") {
		assigned, _ := h.q.IsCourseInstructor(r.Context(), data.IsCourseInstructorParams{CourseID: in.CourseId, InstructorID: p.UserID})
		if !assigned {
			fail(w, r, ErrForbidden)
			return
		}
	}
	row, err := h.s.Create(r.Context(), m.OrganizationID, p.UserID, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 201, dto(row))
}
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	m, _ := auth.MembershipFrom(r.Context())
	statuses := []string{"scheduled", "live"}
	if raw := r.URL.Query().Get("status"); raw != "" {
		statuses = strings.Split(raw, ",")
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
	params := data.ListLiveSessionsParams{OrganizationID: m.OrganizationID, Statuses: statuses, PageSize: size}
	if cursor != nil {
		params.CursorScheduledAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListLiveSessions(r.Context(), params)
	if err != nil {
		fail(w, r, err)
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, dto(row))
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.ScheduledStartAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	row, err := h.q.GetLiveSession(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, r, ErrNotFound)
		return
	}
	p, _ := auth.PrincipalFrom(r.Context())
	allowed := h.manager(r, row)
	if !allowed {
		enrollment, e := h.q.GetCourseEnrollment(r.Context(), data.GetCourseEnrollmentParams{CourseID: row.CourseID, StudentID: p.UserID})
		allowed = e == nil && (enrollment.Status == "active" || enrollment.Status == "completed")
	}
	if !allowed {
		fail(w, r, ErrForbidden)
		return
	}
	httpx.JSON(w, 200, dto(row))
}
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	existing, err := h.q.GetLiveSession(r.Context(), id)
	if err != nil {
		fail(w, r, ErrNotFound)
		return
	}
	if !h.manager(r, existing) {
		fail(w, r, ErrForbidden)
		return
	}
	var in api.LiveSessionWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	if !in.ScheduledEndAt.After(in.ScheduledStartAt) {
		httpx.Problem(w, r, 422, "Invalid Schedule", "scheduled_end_at must be after scheduled_start_at")
		return
	}
	row, err := h.q.UpdateLiveSession(r.Context(), data.UpdateLiveSessionParams{ID: id, Title: strings.TrimSpace(in.Title), Description: value(in.Description), ScheduledStartAt: pgtype.Timestamptz{Time: in.ScheduledStartAt.UTC(), Valid: true}, ScheduledEndAt: pgtype.Timestamptz{Time: in.ScheduledEndAt.UTC(), Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Problem(w, r, 409, "Session Locked", "only scheduled sessions can be updated")
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, dto(row))
}
func (h *Handler) SetStatus(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
		if err != nil {
			httpx.Problem(w, r, 400, "Invalid ID", err.Error())
			return
		}
		row, err := h.q.GetLiveSession(r.Context(), id)
		if err != nil {
			fail(w, r, ErrNotFound)
			return
		}
		if !h.manager(r, row) {
			fail(w, r, ErrForbidden)
			return
		}
		updated, err := h.s.SetStatus(r.Context(), id, status)
		if err != nil {
			fail(w, r, err)
			return
		}
		httpx.JSON(w, 200, dto(updated))
	}
}
func (h *Handler) JoinToken(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	session, err := h.q.GetLiveSession(r.Context(), id)
	if err != nil {
		fail(w, r, ErrNotFound)
		return
	}
	assigned, _ := h.q.IsCourseInstructor(r.Context(), data.IsCourseInstructorParams{CourseID: session.CourseID, InstructorID: p.UserID})
	result, err := h.s.JoinToken(r.Context(), id, p.UserID, p.DisplayName, assigned)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, result)
}
func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := h.s.Webhook(r.Context(), r); err != nil {
		httpx.Problem(w, r, 400, "Invalid Webhook", err.Error())
		return
	}
	w.WriteHeader(204)
}
