package payment

import (
	"errors"
	"io"
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
func fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Problem(w, r, 404, "Not Found", err.Error())
	case errors.Is(err, ErrForbidden):
		httpx.Problem(w, r, 403, "Forbidden", err.Error())
	case errors.Is(err, ErrIdempotencyConflict):
		httpx.Problem(w, r, 409, "Idempotency Conflict", err.Error())
	default:
		httpx.Problem(w, r, 422, "Payment Request Failed", err.Error())
	}
}
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	var in api.OrderWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	result, err := h.s.CreateOrder(r.Context(), p.UserID, p.Email, r.Header.Get("Idempotency-Key"), in.CourseId)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 201, result)
}
func (h *Handler) GetOrder(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	privileged := false
	if order, loadErr := h.q.GetOrder(r.Context(), id); loadErr == nil {
		if membership, membershipErr := h.q.GetActiveMembership(r.Context(), data.GetActiveMembershipParams{UserID: p.UserID, OrganizationID: order.OrganizationID}); membershipErr == nil {
			privileged = auth.HasRole(membership.Roles, "organization_admin", "super_admin")
		}
	}
	result, err := h.s.GetOwned(r.Context(), id, p.UserID, privileged)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, result)
}
func (h *Handler) CancelOrder(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	result, err := h.s.Cancel(r.Context(), id, p.UserID)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, result)
}
func (h *Handler) CreateIntent(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	result, err := h.s.CreateIntent(r.Context(), id, p.UserID, p.Email)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, result)
}
func (h *Handler) ListOrders(w http.ResponseWriter, r *http.Request) {
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
	params := data.ListUserOrdersParams{UserID: p.UserID, PageSize: size}
	if cursor != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListUserOrders(r.Context(), params)
	if err != nil {
		fail(w, r, err)
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"id": row.ID, "status": row.Status, "amount_minor": row.AmountMinor, "currency": row.Currency, "created_at": row.CreatedAt.Time})
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.CreatedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}
func (h *Handler) ListPayments(w http.ResponseWriter, r *http.Request) {
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
	params := data.ListUserPaymentsParams{UserID: p.UserID, PageSize: size}
	if cursor != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListUserPayments(r.Context(), params)
	if err != nil {
		fail(w, r, err)
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"id": row.ID, "order_id": row.OrderID, "kind": row.Kind, "status": row.Status, "amount_minor": row.AmountMinor, "currency": row.Currency, "created_at": row.CreatedAt.Time})
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.CreatedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}
func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Webhook", "unable to read webhook payload")
		return
	}
	if err := h.s.Webhook(r.Context(), body, r.Header.Get("Stripe-Signature")); err != nil {
		httpx.Problem(w, r, 400, "Invalid Webhook", err.Error())
		return
	}
	w.WriteHeader(204)
}
