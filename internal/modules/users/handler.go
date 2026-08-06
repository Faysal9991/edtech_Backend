package users

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/neoscoder/lms-service/internal/platform/auth"
	"github.com/neoscoder/lms-service/internal/platform/httpx"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

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
	createdAt, cursorID := pgtype.Timestamptz{}, uuid.NullUUID{}
	if cursor != nil {
		createdAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		cursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.service.List(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("status"), r.URL.Query().Get("role"), size, createdAt, cursorID)
	if err != nil {
		userProblem(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"id": row.ID, "email": row.Email, "display_name": row.DisplayName, "status": row.Status, "roles": row.Roles, "email_verified_at": nullableTime(row.EmailVerifiedAt), "created_at": row.CreatedAt.Time, "updated_at": row.UpdatedAt.Time})
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
	row, err := h.service.Get(r.Context(), id)
	if err != nil {
		userProblem(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"id": row.ID, "email": row.Email, "display_name": row.DisplayName, "status": row.Status, "avatar_url": nullableText(row.AvatarUrl), "email_verified_at": nullableTime(row.EmailVerifiedAt), "first_name": row.FirstName.String, "last_name": row.LastName.String, "phone": nullableText(row.Phone), "timezone": row.Timezone.String, "locale": row.Locale.String, "biography": row.Biography.String, "expertise": row.Expertise, "teacher_status": nullableText(row.TeacherStatus), "student_headline": nullableText(row.StudentHeadline), "roles": row.Roles})
}

func (h *Handler) SetStatus(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	var input struct {
		Status string `json:"status"`
	}
	if err = httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	if err = h.service.SetStatus(r.Context(), actor.UserID, id, strings.TrimSpace(input.Status)); err != nil {
		userProblem(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) ReplaceRoles(w http.ResponseWriter, r *http.Request) {
	actor, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	var input struct {
		Roles []string `json:"roles"`
	}
	if err = httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	if err = h.service.ReplaceRoles(r.Context(), actor.UserID, id, input.Roles); err != nil {
		userProblem(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"id": id, "roles": input.Roles})
}

func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.PrincipalFrom(r.Context())
	var input struct {
		DisplayName string   `json:"display_name"`
		FirstName   string   `json:"first_name"`
		LastName    string   `json:"last_name"`
		Phone       string   `json:"phone"`
		Timezone    string   `json:"timezone"`
		Locale      string   `json:"locale"`
		Biography   string   `json:"biography"`
		Expertise   []string `json:"expertise"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	if err := h.service.UpdateProfile(r.Context(), principal.UserID, input.DisplayName, input.FirstName, input.LastName, input.Phone, input.Timezone, input.Locale, input.Biography, input.Expertise); err != nil {
		userProblem(w, r, err)
		return
	}
	row, err := h.service.Get(r.Context(), principal.UserID)
	if err != nil {
		userProblem(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"id": row.ID, "display_name": row.DisplayName, "roles": row.Roles})
}

func userProblem(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Problem(w, r, 404, "Not Found", err.Error())
	case errors.Is(err, ErrSelfLock), errors.Is(err, ErrLastAdmin):
		httpx.Problem(w, r, 409, "Unsafe Account Change", err.Error())
	default:
		httpx.Problem(w, r, 422, "User Request Failed", err.Error())
	}
}

func nullableTime(value pgtype.Timestamptz) any {
	if value.Valid {
		return value.Time.UTC()
	}
	return nil
}
func nullableText(value pgtype.Text) any {
	if value.Valid {
		return value.String
	}
	return nil
}
