package auth

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Faysal9991/edtech_Backend/internal/data"
	"github.com/Faysal9991/edtech_Backend/internal/platform/cache"
	"github.com/Faysal9991/edtech_Backend/internal/platform/httpx"
	platformid "github.com/Faysal9991/edtech_Backend/internal/platform/id"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Middleware struct {
	verifier TokenVerifier
	queries  *data.Queries
	ids      platformid.Generator
	limiter  cache.Limiter
	limit    int
	window   time.Duration
}

func NewMiddleware(verifier TokenVerifier, queries *data.Queries, ids platformid.Generator, limiter cache.Limiter, limit int, window time.Duration) *Middleware {
	return &Middleware{verifier: verifier, queries: queries, ids: ids, limiter: limiter, limit: limit, window: window}
}

func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			httpx.Problem(w, r, http.StatusUnauthorized, "Unauthorized", "a Firebase bearer token is required")
			return
		}
		// Limit invalid-token floods before invoking the identity provider. The
		// wider IP allowance avoids penalizing normal users sharing a NAT.
		preAuthLimit := m.limit * 5
		if preAuthLimit < m.limit {
			preAuthLimit = m.limit
		}
		allowed, err := m.limiter.Allow(r.Context(), "auth-ip:"+clientIP(r), preAuthLimit, m.window)
		if err != nil {
			httpx.Problem(w, r, http.StatusServiceUnavailable, "Service Unavailable", "authentication rate limiter is unavailable")
			return
		}
		if !allowed {
			w.Header().Set("Retry-After", "60")
			httpx.Problem(w, r, http.StatusTooManyRequests, "Too Many Requests", "authentication request limit exceeded")
			return
		}
		identity, err := m.verifier.Verify(r.Context(), raw)
		if err != nil {
			httpx.Problem(w, r, http.StatusUnauthorized, "Unauthorized", "the identity token is invalid, expired, revoked, or disabled")
			return
		}
		key := "auth:" + identity.UID + ":" + clientIP(r)
		allowed, err = m.limiter.Allow(r.Context(), key, m.limit, m.window)
		if err != nil {
			httpx.Problem(w, r, http.StatusServiceUnavailable, "Service Unavailable", "authentication rate limiter is unavailable")
			return
		}
		if !allowed {
			w.Header().Set("Retry-After", "60")
			httpx.Problem(w, r, http.StatusTooManyRequests, "Too Many Requests", "authentication request limit exceeded")
			return
		}
		user, err := m.queries.UpsertUserByFirebaseUID(r.Context(), data.UpsertUserByFirebaseUIDParams{ID: m.ids.New(), FirebaseUid: identity.UID, Email: identity.Email, DisplayName: identity.DisplayName})
		if err != nil {
			httpx.Problem(w, r, http.StatusInternalServerError, "Internal Server Error", "unable to bootstrap local identity")
			return
		}
		if user.Status != "active" {
			httpx.Problem(w, r, http.StatusForbidden, "Account Unavailable", "the local user account is suspended or deleted")
			return
		}
		principal := Principal{UserID: user.ID, FirebaseUID: user.FirebaseUid, Email: user.Email, DisplayName: user.DisplayName}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
	})
}

func (m *Middleware) RequireRoles(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFrom(r.Context())
			if !ok {
				httpx.Problem(w, r, http.StatusUnauthorized, "Unauthorized", "authentication context is missing")
				return
			}
			isSuper, err := m.queries.GetSuperAdminRoleForUser(r.Context(), principal.UserID)
			if err != nil {
				httpx.Problem(w, r, http.StatusInternalServerError, "Internal Server Error", "authorization lookup failed")
				return
			}
			if isSuper {
				organizationID, err := organizationFromRequest(r)
				if err != nil {
					httpx.Problem(w, r, http.StatusBadRequest, "Invalid Organization", err.Error())
					return
				}
				ctx := WithMembership(r.Context(), Membership{OrganizationID: organizationID, Roles: []string{"super_admin"}})
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			organizationID, err := organizationFromRequest(r)
			if err != nil {
				httpx.Problem(w, r, http.StatusBadRequest, "Invalid Organization", err.Error())
				return
			}
			membership, err := m.queries.GetActiveMembership(r.Context(), data.GetActiveMembershipParams{UserID: principal.UserID, OrganizationID: organizationID})
			if errors.Is(err, pgx.ErrNoRows) {
				httpx.Problem(w, r, http.StatusForbidden, "Forbidden", "active organization membership is required")
				return
			}
			if err != nil {
				httpx.Problem(w, r, http.StatusInternalServerError, "Internal Server Error", "authorization lookup failed")
				return
			}
			if !HasRole(membership.Roles, roles...) {
				httpx.Problem(w, r, http.StatusForbidden, "Forbidden", "the current organization role does not permit this operation")
				return
			}
			ctx := WithMembership(r.Context(), Membership{ID: membership.ID, OrganizationID: membership.OrganizationID, Roles: membership.Roles})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (m *Middleware) RequireAnyMembership(next http.Handler) http.Handler {
	return m.RequireRoles("organization_admin", "instructor", "student")(next)
}

func (m *Middleware) RequireSuperAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFrom(r.Context())
		if !ok {
			httpx.Problem(w, r, http.StatusUnauthorized, "Unauthorized", "authentication context is missing")
			return
		}
		allowed, err := m.queries.GetSuperAdminRoleForUser(r.Context(), principal.UserID)
		if err != nil {
			httpx.Problem(w, r, http.StatusInternalServerError, "Internal Server Error", "authorization lookup failed")
			return
		}
		if !allowed {
			httpx.Problem(w, r, http.StatusForbidden, "Forbidden", "super administrator role is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) Verifier() TokenVerifier { return m.verifier }

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	returnValue := ""
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		returnValue = parts[1]
	}
	return returnValue, returnValue != ""
}
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func organizationFromRequest(r *http.Request) (uuid.UUID, error) {
	raw := r.Header.Get("X-Organization-ID")
	if raw == "" && strings.Contains(r.URL.Path, "/organizations/") {
		raw = chi.URLParam(r, "id")
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errors.New("a valid organization context is required")
	}
	return id, nil
}
