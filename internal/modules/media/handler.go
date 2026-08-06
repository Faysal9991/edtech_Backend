package media

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
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
func (h *Handler) CreateIntent(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	m, _ := auth.MembershipFrom(r.Context())
	var in api.UploadIntentWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	result, err := h.s.CreateIntent(r.Context(), m.OrganizationID, p.UserID, in)
	if err != nil {
		httpx.Problem(w, r, 422, "Upload Intent Failed", err.Error())
		return
	}
	httpx.JSON(w, 201, result)
}
func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	asset, err := h.s.Complete(r.Context(), id, p.UserID)
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Problem(w, r, 404, "Not Found", err.Error())
	case errors.Is(err, ErrForbidden):
		httpx.Problem(w, r, 403, "Forbidden", err.Error())
	case errors.Is(err, ErrInvalidObject):
		httpx.Problem(w, r, 409, "Object Verification Failed", err.Error())
	case err != nil:
		httpx.Problem(w, r, 422, "Completion Failed", err.Error())
	default:
		httpx.JSON(w, 202, map[string]any{"id": asset.ID, "status": asset.Status})
	}
}
func (h *Handler) AccessURL(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	asset, err := h.q.GetMediaAsset(r.Context(), id)
	if err != nil {
		httpx.Problem(w, r, 404, "Not Found", "media not found")
		return
	}
	privileged := false
	if membership, e := h.q.GetActiveMembership(r.Context(), data.GetActiveMembershipParams{UserID: p.UserID, OrganizationID: asset.OrganizationID}); e == nil {
		privileged = auth.HasRole(membership.Roles, "organization_admin", "super_admin")
	}
	url, err := h.s.AccessURL(r.Context(), id, p.UserID, privileged)
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Problem(w, r, 404, "Not Found", err.Error())
	case errors.Is(err, ErrForbidden):
		httpx.Problem(w, r, 403, "Forbidden", err.Error())
	case err != nil:
		httpx.Problem(w, r, 409, "Media Unavailable", err.Error())
	default:
		httpx.JSON(w, 200, map[string]any{"url": url})
	}
}
