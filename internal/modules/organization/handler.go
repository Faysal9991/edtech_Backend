package organization

import (
	"encoding/json"
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

func NewHandler(s *Service, q *data.Queries) *Handler {
	return &Handler{s: s, q: q}
}
func principal(r *http.Request) (auth.Principal, bool)   { return auth.PrincipalFrom(r.Context()) }
func membership(r *http.Request) (auth.Membership, bool) { return auth.MembershipFrom(r.Context()) }
func organizationDTO(v data.Organization) map[string]any {
	return map[string]any{"id": v.ID, "name": v.Name, "slug": v.Slug, "status": v.Status, "created_at": v.CreatedAt.Time, "updated_at": v.UpdatedAt.Time}
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r)
	if !ok {
		httpx.Problem(w, r, 401, "Unauthorized", "authentication context is missing")
		return
	}
	user, err := h.s.Me(r.Context(), p.UserID)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to load user profile")
		return
	}
	httpx.JSON(w, 200, user)
}
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var in api.OrganizationWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	row, err := h.s.Create(r.Context(), in.Name, in.Slug)
	if err != nil {
		httpx.Problem(w, r, 422, "Validation Failed", err.Error())
		return
	}
	httpx.JSON(w, 201, organizationDTO(row))
}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	row, err := h.s.Get(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		httpx.Problem(w, r, 404, "Not Found", "organization not found")
		return
	}
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to load organization")
		return
	}
	httpx.JSON(w, 200, organizationDTO(row))
}
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	var in api.OrganizationWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	row, err := h.q.UpdateOrganization(r.Context(), data.UpdateOrganizationParams{ID: id, Name: in.Name, Slug: httpx.NormalizeSlug(in.Slug)})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Problem(w, r, 404, "Not Found", "organization not found")
		return
	}
	if err != nil {
		httpx.Problem(w, r, 422, "Update Failed", err.Error())
		return
	}
	httpx.JSON(w, 200, organizationDTO(row))
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
	params := data.ListOrganizationsParams{PageSize: size}
	if cursor != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListOrganizations(r.Context(), params)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to list organizations")
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, organizationDTO(row))
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.CreatedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}

func (h *Handler) Members(w http.ResponseWriter, r *http.Request) {
	orgID, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
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
	params := data.ListOrganizationMembersParams{OrganizationID: orgID, PageSize: size}
	if cursor != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListOrganizationMembers(r.Context(), params)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to list members")
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"id": row.ID, "email": row.Email, "display_name": row.DisplayName, "user_status": row.UserStatus, "membership_id": row.MembershipID, "membership_status": row.MembershipStatus, "roles": row.Roles, "created_at": row.CreatedAt.Time})
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.CreatedAt.Time, last.MembershipID)
	}
	httpx.JSON(w, 200, response)
}

func (h *Handler) UpdateMembership(w http.ResponseWriter, r *http.Request) {
	organizationID, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Organization", err.Error())
		return
	}
	membershipID, err := httpx.UUIDParam(chi.URLParam(r, "membershipId"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Membership", err.Error())
		return
	}
	var in api.MembershipUpdate
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	p, _ := principal(r)
	updated, roles, err := h.s.UpdateMembership(r.Context(), organizationID, membershipID, p.UserID, in)
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Problem(w, r, 404, "Not Found", "membership not found in organization")
	case err != nil:
		httpx.Problem(w, r, 422, "Membership Update Failed", err.Error())
	default:
		httpx.JSON(w, 200, map[string]any{"id": updated.ID, "organization_id": updated.OrganizationID, "user_id": updated.UserID, "status": updated.Status, "roles": roles})
	}
}

func (h *Handler) Invite(w http.ResponseWriter, r *http.Request) {
	orgID, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	p, _ := principal(r)
	var in api.InvitationWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	result, err := h.s.Invite(r.Context(), orgID, p.UserID, string(in.Email), string(in.Role))
	if err != nil {
		httpx.Problem(w, r, 422, "Invitation Failed", err.Error())
		return
	}
	httpx.JSON(w, 201, result)
}
func (h *Handler) Accept(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	var in api.InvitationAccept
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	err := h.s.AcceptInvitation(r.Context(), p.UserID, p.Email, in.Token)
	switch {
	case errors.Is(err, ErrInvitationEmailMismatch):
		httpx.Problem(w, r, 403, "Email Mismatch", err.Error())
	case errors.Is(err, ErrInvitationUnavailable):
		httpx.Problem(w, r, 409, "Invitation Unavailable", err.Error())
	case err != nil:
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to accept invitation")
	default:
		httpx.JSON(w, 200, map[string]any{"accepted": true})
	}
}

func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	if id != p.UserID {
		m, ok := membership(r)
		if !ok || !auth.HasRole(m.Roles, "organization_admin", "super_admin") {
			httpx.Problem(w, r, 403, "Forbidden", "users may only access their own profile")
			return
		}
	}
	user, err := h.s.Me(r.Context(), id)
	if err != nil {
		httpx.Problem(w, r, 404, "Not Found", "user not found")
		return
	}
	httpx.JSON(w, 200, user)
}
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	m, ok := membership(r)
	if !ok {
		httpx.Problem(w, r, 400, "Organization Required", "X-Organization-ID is required")
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
	params := data.ListOrganizationMembersParams{OrganizationID: m.OrganizationID, PageSize: size}
	if cursor != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListOrganizationMembers(r.Context(), params)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to list users")
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"id": row.ID, "email": row.Email, "display_name": row.DisplayName, "status": row.UserStatus, "membership_status": row.MembershipStatus, "roles": row.Roles})
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.CreatedAt.Time, last.MembershipID)
	}
	httpx.JSON(w, 200, response)
}

func (h *Handler) AuditLogs(w http.ResponseWriter, r *http.Request) {
	organizationID, err := httpx.UUIDParam(r.URL.Query().Get("organization_id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Organization", "organization_id must be a UUID")
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
	params := data.ListOrganizationAuditLogsParams{OrganizationID: uuid.NullUUID{UUID: organizationID, Valid: true}, PageSize: size}
	if cursor != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListOrganizationAuditLogs(r.Context(), params)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to list audit logs")
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"id": row.ID, "organization_id": nullableUUIDValue(row.OrganizationID), "actor_user_id": nullableUUIDValue(row.ActorUserID), "actor_email": nullableTextValue(row.ActorEmail), "action": row.Action, "resource_type": row.ResourceType, "resource_id": nullableUUIDValue(row.ResourceID), "request_id": nullableTextValue(row.RequestID), "ip_address": row.IpAddress, "before": nullableJSON(row.BeforeData), "after": nullableJSON(row.AfterData), "created_at": row.CreatedAt.Time})
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.CreatedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}

func nullableUUIDValue(value uuid.NullUUID) any {
	if value.Valid {
		return value.UUID
	}
	return nil
}

func nullableTextValue(value pgtype.Text) any {
	if value.Valid {
		return value.String
	}
	return nil
}

func nullableJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return json.RawMessage(value)
}
