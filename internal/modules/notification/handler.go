package notification

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	var in api.DeviceTokenWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	row, err := h.s.RegisterToken(r.Context(), p.UserID, in)
	if err != nil {
		httpx.Problem(w, r, 422, "Token Registration Failed", err.Error())
		return
	}
	httpx.JSON(w, 201, map[string]any{"id": row.ID, "platform": row.Platform})
}
func (h *Handler) Remove(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	if token := chi.URLParam(r, "token"); token != "" {
		hash := sha256.Sum256([]byte(token))
		affected, err := h.q.RemoveDeviceTokenByHash(r.Context(), data.RemoveDeviceTokenByHashParams{TokenHash: hex.EncodeToString(hash[:]), UserID: p.UserID})
		if err != nil || affected == 0 {
			httpx.Problem(w, r, 404, "Not Found", "device token not found")
			return
		}
		w.WriteHeader(204)
		return
	}
	id, err := httpx.UUIDParam(r.URL.Query().Get("id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", "id query parameter must be a UUID")
		return
	}
	if err := h.s.RemoveToken(r.Context(), p.UserID, id); err != nil {
		httpx.Problem(w, r, 404, "Not Found", err.Error())
		return
	}
	w.WriteHeader(204)
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
	params := data.ListNotificationsParams{UserID: p.UserID, PageSize: size}
	if cursor != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListNotifications(r.Context(), params)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to list notifications")
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"id": row.ID, "type": row.Type, "title": row.Title, "body": row.Body, "data": row.Data, "read_at": timeValue(row.ReadAt), "created_at": row.CreatedAt.Time})
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.CreatedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}
func timeValue(v pgtype.Timestamptz) any {
	if v.Valid {
		return v.Time
	}
	return nil
}
func (h *Handler) Unread(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	count, err := h.q.NotificationUnreadCount(r.Context(), p.UserID)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to count notifications")
		return
	}
	httpx.JSON(w, 200, map[string]any{"count": count})
}
func (h *Handler) Read(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	row, err := h.q.MarkNotificationRead(r.Context(), data.MarkNotificationReadParams{ID: id, UserID: p.UserID})
	if err != nil {
		httpx.Problem(w, r, 404, "Not Found", "notification not found")
		return
	}
	httpx.JSON(w, 200, map[string]any{"id": row.ID, "read_at": row.ReadAt.Time})
}
func (h *Handler) ReadAll(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	if _, err := h.q.MarkAllNotificationsRead(r.Context(), p.UserID); err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to mark notifications")
		return
	}
	w.WriteHeader(204)
}
