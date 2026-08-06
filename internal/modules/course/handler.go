package course

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
	platformid "github.com/neoscoder/lms-service/internal/platform/id"
)

type Handler struct {
	s   *Service
	q   *data.Queries
	ids platformid.Generator
}

func NewHandler(s *Service, q *data.Queries, ids platformid.Generator) *Handler {
	return &Handler{s: s, q: q, ids: ids}
}
func principal(r *http.Request) auth.Principal { p, _ := auth.PrincipalFrom(r.Context()); return p }
func member(r *http.Request) auth.Membership   { m, _ := auth.MembershipFrom(r.Context()); return m }
func courseRow(v data.Course) map[string]any {
	return map[string]any{"id": v.ID, "organization_id": v.OrganizationID, "category_id": nullable(v.CategoryID), "thumbnail_asset_id": nullable(v.ThumbnailAssetID), "title": v.Title, "slug": v.Slug, "description": v.Description, "language": v.Language, "level": v.Level, "status": v.Status, "is_free": v.IsFree, "price_minor": v.PriceMinor, "currency": v.Currency, "version": v.Version, "published_at": timeValue(v.PublishedAt), "created_at": timeValue(v.CreatedAt), "updated_at": timeValue(v.UpdatedAt)}
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
func handleError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Problem(w, r, 404, "Not Found", err.Error())
	case errors.Is(err, ErrForbidden):
		httpx.Problem(w, r, 403, "Forbidden", err.Error())
	case errors.Is(err, ErrConflict):
		httpx.Problem(w, r, 409, "Version Conflict", err.Error())
	default:
		httpx.Problem(w, r, 422, "Request Failed", err.Error())
	}
}

func (h *Handler) ListCategories(w http.ResponseWriter, r *http.Request) {
	m := member(r)
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
	params := data.ListCategoriesParams{OrganizationID: m.OrganizationID, PageSize: size}
	if cursor != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListCategories(r.Context(), params)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to list categories")
		return
	}
	items := make([]any, 0, len(rows))
	for _, v := range rows {
		items = append(items, map[string]any{"id": v.ID, "name": v.Name, "slug": v.Slug, "description": v.Description})
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.CreatedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}
func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	m := member(r)
	var in api.CategoryWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	description := ""
	if in.Description != nil {
		description = *in.Description
	}
	row, err := h.q.CreateCategory(r.Context(), data.CreateCategoryParams{ID: h.ids.New(), OrganizationID: m.OrganizationID, Name: strings.TrimSpace(in.Name), Slug: httpx.NormalizeSlug(in.Slug), Description: description})
	if err != nil {
		httpx.Problem(w, r, 422, "Category Failed", err.Error())
		return
	}
	httpx.JSON(w, 201, map[string]any{"id": row.ID, "name": row.Name, "slug": row.Slug, "description": row.Description})
}
func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	m := member(r)
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	var in api.CategoryWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	description := ""
	if in.Description != nil {
		description = *in.Description
	}
	row, err := h.q.UpdateCategory(r.Context(), data.UpdateCategoryParams{ID: id, OrganizationID: m.OrganizationID, Name: in.Name, Slug: httpx.NormalizeSlug(in.Slug), Description: description})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Problem(w, r, 404, "Not Found", "category not found in organization")
		return
	}
	if err != nil {
		httpx.Problem(w, r, 422, "Category Failed", err.Error())
		return
	}
	httpx.JSON(w, 200, map[string]any{"id": row.ID, "name": row.Name, "slug": row.Slug, "description": row.Description})
}
func (h *Handler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	m := member(r)
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	n, err := h.q.DeleteCategory(r.Context(), data.DeleteCategoryParams{ID: id, OrganizationID: m.OrganizationID})
	if err != nil {
		httpx.Problem(w, r, 409, "Delete Failed", "category is in use")
		return
	}
	if n == 0 {
		httpx.Problem(w, r, 404, "Not Found", "category not found")
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
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
	params := data.ListPublishedCoursesParams{Search: strings.TrimSpace(r.URL.Query().Get("q")), PageSize: size}
	if raw := r.URL.Query().Get("category_id"); raw != "" {
		id, e := uuid.Parse(raw)
		if e != nil {
			httpx.Problem(w, r, 400, "Invalid Filter", "category_id must be a UUID")
			return
		}
		params.CategoryID = uuid.NullUUID{UUID: id, Valid: true}
	}
	if raw := r.URL.Query().Get("organization_id"); raw != "" {
		id, e := uuid.Parse(raw)
		if e != nil {
			httpx.Problem(w, r, 400, "Invalid Filter", "organization_id must be a UUID")
			return
		}
		params.OrganizationID = uuid.NullUUID{UUID: id, Valid: true}
	}
	if level := r.URL.Query().Get("level"); level != "" {
		params.Level = pgtype.Text{String: level, Valid: true}
	}
	if cursor != nil {
		params.CursorPublishedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListPublishedCourses(r.Context(), params)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to list courses")
		return
	}
	items := make([]any, 0, len(rows))
	for _, v := range rows {
		items = append(items, map[string]any{"id": v.ID, "organization_id": v.OrganizationID, "thumbnail_asset_id": nullable(v.ThumbnailAssetID), "title": v.Title, "slug": v.Slug, "description": v.Description, "language": v.Language, "level": v.Level, "is_free": v.IsFree, "price_minor": v.PriceMinor, "currency": v.Currency, "category_name": v.CategoryName.String, "published_at": timeValue(v.PublishedAt)})
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.PublishedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}

func (h *Handler) ListManaged(w http.ResponseWriter, r *http.Request) {
	m, p := member(r), principal(r)
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
	params := data.ListManagedCoursesParams{OrganizationID: m.OrganizationID, PageSize: size}
	if auth.HasRole(m.Roles, "instructor") && !auth.HasRole(m.Roles, "organization_admin", "super_admin") {
		params.InstructorID = uuid.NullUUID{UUID: p.UserID, Valid: true}
	}
	if cursor != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListManagedCourses(r.Context(), params)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to list managed courses")
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, courseRow(row))
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.CreatedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	m := member(r)
	p := principal(r)
	var in api.CourseWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	row, err := h.s.Create(r.Context(), m.OrganizationID, p.UserID, in)
	if err != nil {
		handleError(w, r, err)
		return
	}
	httpx.JSON(w, 201, row)
}

func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	p := principal(r)
	course, err := h.s.Get(r.Context(), id)
	if err != nil {
		handleError(w, r, err)
		return
	}
	includeDraft := false
	if course.Status != "published" {
		membership, err := h.q.GetActiveMembership(r.Context(), data.GetActiveMembershipParams{UserID: p.UserID, OrganizationID: course.OrganizationID})
		if err == nil {
			if auth.HasRole(membership.Roles, "organization_admin", "super_admin") {
				includeDraft = true
			} else if auth.HasRole(membership.Roles, "instructor") {
				assigned, _ := h.q.IsCourseInstructor(r.Context(), data.IsCourseInstructorParams{CourseID: id, InstructorID: p.UserID})
				includeDraft = assigned
			}
		}
	}
	detail, err := h.s.Detail(r.Context(), id, includeDraft)
	if err != nil {
		handleError(w, r, err)
		return
	}
	httpx.JSON(w, 200, detail)
}

func (h *Handler) PublicDetailBySlug(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if slug == "" || len(slug) > 180 {
		httpx.Problem(w, r, 400, "Invalid Slug", "a valid course slug is required")
		return
	}
	course, err := h.q.GetPublishedCourseBySlug(r.Context(), slug)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Problem(w, r, 404, "Not Found", "published course not found")
		return
	}
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to load course")
		return
	}
	detail, err := h.s.Detail(r.Context(), course.ID, false)
	if err != nil {
		handleError(w, r, err)
		return
	}
	httpx.JSON(w, 200, detail)
}
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	m, p := member(r), principal(r)
	if err := h.s.CanManage(r.Context(), id, p.UserID, m.OrganizationID, m.Roles); err != nil {
		handleError(w, r, err)
		return
	}
	var in api.CourseWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	row, err := h.s.Update(r.Context(), id, m.OrganizationID, p.UserID, in)
	if err != nil {
		handleError(w, r, err)
		return
	}
	httpx.JSON(w, 200, row)
}
func (h *Handler) SetStatus(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
		if err != nil {
			httpx.Problem(w, r, 400, "Invalid ID", err.Error())
			return
		}
		m, p := member(r), principal(r)
		if err := h.s.CanManage(r.Context(), id, p.UserID, m.OrganizationID, m.Roles); err != nil {
			handleError(w, r, err)
			return
		}
		row, err := h.s.SetStatus(r.Context(), id, p.UserID, status)
		if err != nil {
			handleError(w, r, err)
			return
		}
		httpx.JSON(w, 200, row)
	}
}

func (h *Handler) AssignInstructor(w http.ResponseWriter, r *http.Request) {
	courseID, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	var in api.InstructorAssignment
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	m, p := member(r), principal(r)
	if !auth.HasRole(m.Roles, "organization_admin", "super_admin") {
		httpx.Problem(w, r, 403, "Forbidden", "only organization administrators assign instructors")
		return
	}
	if err := h.s.AssignInstructor(r.Context(), courseID, in.InstructorId, p.UserID, m.OrganizationID); err != nil {
		handleError(w, r, err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) RemoveInstructor(w http.ResponseWriter, r *http.Request) {
	courseID, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	instructorID, err := httpx.UUIDParam(r.URL.Query().Get("instructor_id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", "instructor_id must be a UUID")
		return
	}
	m := member(r)
	course, err := h.s.Get(r.Context(), courseID)
	if err != nil || course.OrganizationID != m.OrganizationID {
		httpx.Problem(w, r, 404, "Not Found", "course not found")
		return
	}
	n, err := h.q.RemoveCourseInstructor(r.Context(), data.RemoveCourseInstructorParams{CourseID: courseID, InstructorID: instructorID})
	if err != nil || n == 0 {
		httpx.Problem(w, r, 404, "Not Found", "instructor assignment not found")
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) ListContent(w http.ResponseWriter, r *http.Request) { h.Detail(w, r) }
func (h *Handler) CreateModule(w http.ResponseWriter, r *http.Request) {
	courseID, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	m, p := member(r), principal(r)
	if err := h.s.CanManage(r.Context(), courseID, p.UserID, m.OrganizationID, m.Roles); err != nil {
		handleError(w, r, err)
		return
	}
	var in api.ModuleWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	row, err := h.s.CreateModule(r.Context(), courseID, in)
	if err != nil {
		handleError(w, r, err)
		return
	}
	httpx.JSON(w, 201, map[string]any{"id": row.ID, "course_id": row.CourseID, "title": row.Title, "description": row.Description, "position": row.Position})
}
func (h *Handler) UpdateModule(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	course, err := h.q.GetCourseByModule(r.Context(), id)
	if err != nil {
		httpx.Problem(w, r, 404, "Not Found", "module not found")
		return
	}
	m, p := member(r), principal(r)
	if err := h.s.CanManage(r.Context(), course.ID, p.UserID, m.OrganizationID, m.Roles); err != nil {
		handleError(w, r, err)
		return
	}
	var in api.ModuleWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	row, err := h.s.UpdateModule(r.Context(), id, in)
	if err != nil {
		handleError(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"id": row.ID, "title": row.Title, "description": row.Description, "position": row.Position})
}
func (h *Handler) DeleteModule(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	course, err := h.q.GetCourseByModule(r.Context(), id)
	if err != nil {
		httpx.Problem(w, r, 404, "Not Found", "module not found")
		return
	}
	m, p := member(r), principal(r)
	if err := h.s.CanManage(r.Context(), course.ID, p.UserID, m.OrganizationID, m.Roles); err != nil {
		handleError(w, r, err)
		return
	}
	n, err := h.q.DeleteModule(r.Context(), id)
	if err != nil || n == 0 {
		httpx.Problem(w, r, 404, "Not Found", "module not found")
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) CreateLesson(w http.ResponseWriter, r *http.Request) {
	moduleID, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	course, err := h.q.GetCourseByModule(r.Context(), moduleID)
	if err != nil {
		httpx.Problem(w, r, 404, "Not Found", "module not found")
		return
	}
	m, p := member(r), principal(r)
	if err := h.s.CanManage(r.Context(), course.ID, p.UserID, m.OrganizationID, m.Roles); err != nil {
		handleError(w, r, err)
		return
	}
	var in api.LessonWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	row, err := h.s.CreateLesson(r.Context(), moduleID, in)
	if err != nil {
		handleError(w, r, err)
		return
	}
	httpx.JSON(w, 201, lessonDTO(row, false))
}
func (h *Handler) UpdateLesson(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	course, err := h.q.GetCourseByLesson(r.Context(), id)
	if err != nil {
		httpx.Problem(w, r, 404, "Not Found", "lesson not found")
		return
	}
	m, p := member(r), principal(r)
	if err := h.s.CanManage(r.Context(), course.ID, p.UserID, m.OrganizationID, m.Roles); err != nil {
		handleError(w, r, err)
		return
	}
	var in api.LessonWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	row, err := h.s.UpdateLesson(r.Context(), id, in)
	if err != nil {
		handleError(w, r, err)
		return
	}
	httpx.JSON(w, 200, lessonDTO(row, false))
}
func (h *Handler) DeleteLesson(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	course, err := h.q.GetCourseByLesson(r.Context(), id)
	if err != nil {
		httpx.Problem(w, r, 404, "Not Found", "lesson not found")
		return
	}
	m, p := member(r), principal(r)
	if err := h.s.CanManage(r.Context(), course.ID, p.UserID, m.OrganizationID, m.Roles); err != nil {
		handleError(w, r, err)
		return
	}
	n, err := h.q.DeleteLesson(r.Context(), id)
	if err != nil || n == 0 {
		httpx.Problem(w, r, 404, "Not Found", "lesson not found")
		return
	}
	w.WriteHeader(204)
}
func lessonDTO(v data.Lesson, includeBody bool) map[string]any {
	out := map[string]any{"id": v.ID, "module_id": v.ModuleID, "title": v.Title, "description": v.Description, "lesson_type": v.LessonType, "media_asset_id": nullable(v.MediaAssetID), "position": v.Position, "is_preview": v.IsPreview, "is_required": v.IsRequired, "is_published": v.IsPublished, "duration_seconds": nil}
	if v.DurationSeconds.Valid {
		out["duration_seconds"] = v.DurationSeconds.Int32
	}
	if includeBody {
		out["body"] = v.Body
	}
	return out
}
func (h *Handler) GetLesson(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	lesson, err := h.q.GetLesson(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Problem(w, r, 404, "Not Found", "lesson not found")
		return
	}
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to load lesson")
		return
	}
	course, err := h.q.GetCourseByLesson(r.Context(), id)
	if err != nil {
		httpx.Problem(w, r, 404, "Not Found", "lesson not found")
		return
	}
	p := principal(r)
	allowed := lesson.IsPreview && lesson.IsPublished && course.Status == "published"
	if !allowed {
		if enrollment, e := h.q.GetCourseEnrollment(r.Context(), data.GetCourseEnrollmentParams{CourseID: course.ID, StudentID: p.UserID}); e == nil && (enrollment.Status == "active" || enrollment.Status == "completed") {
			allowed = true
		}
	}
	if !allowed {
		if membership, e := h.q.GetActiveMembership(r.Context(), data.GetActiveMembershipParams{UserID: p.UserID, OrganizationID: course.OrganizationID}); e == nil {
			if auth.HasRole(membership.Roles, "organization_admin", "super_admin") {
				allowed = true
			} else if auth.HasRole(membership.Roles, "instructor") {
				allowed, _ = h.q.IsCourseInstructor(r.Context(), data.IsCourseInstructorParams{CourseID: course.ID, InstructorID: p.UserID})
			}
		}
	}
	if !allowed {
		httpx.Problem(w, r, 403, "Forbidden", "preview, active enrollment, or course staff access is required")
		return
	}
	httpx.JSON(w, 200, lessonDTO(lesson, true))
}
