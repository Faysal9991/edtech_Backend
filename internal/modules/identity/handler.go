package identity

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/neoscoder/lms-service/internal/platform/auth"
	"github.com/neoscoder/lms-service/internal/platform/httpx"
)

type Handler struct {
	service     *Service
	exposeToken bool
}

func NewHandler(service *Service, environment string) *Handler {
	return &Handler{service: service, exposeToken: environment == "development" || environment == "test"}
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Problem(w, r, http.StatusBadRequest, "Invalid Request", err.Error())
		return
	}
	result, err := h.service.Register(r.Context(), input.Email, input.Password, input.DisplayName, requestInfo(r))
	if err != nil {
		identityProblem(w, r, err)
		return
	}
	response := map[string]any{"user": result.User, "message": "registration accepted; verify the email before signing in"}
	if h.exposeToken {
		response["verification_token"] = result.VerificationToken
	}
	httpx.JSON(w, http.StatusCreated, response)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Problem(w, r, http.StatusBadRequest, "Invalid Request", err.Error())
		return
	}
	result, err := h.service.Login(r.Context(), input.Email, input.Password, requestInfo(r))
	if err != nil {
		identityProblem(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Problem(w, r, http.StatusBadRequest, "Invalid Request", err.Error())
		return
	}
	result, err := h.service.Refresh(r.Context(), strings.TrimSpace(input.RefreshToken), requestInfo(r))
	if err != nil {
		identityProblem(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		httpx.Problem(w, r, http.StatusUnauthorized, "Unauthorized", "authentication context is missing")
		return
	}
	var input struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Problem(w, r, http.StatusBadRequest, "Invalid Request", err.Error())
		return
	}
	if err := h.service.Logout(r.Context(), strings.TrimSpace(input.RefreshToken), principal.UserID, requestInfo(r)); err != nil {
		identityProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		httpx.Problem(w, r, http.StatusUnauthorized, "Unauthorized", "authentication context is missing")
		return
	}
	if err := h.service.LogoutAll(r.Context(), principal.UserID, requestInfo(r)); err != nil {
		identityProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		httpx.Problem(w, r, http.StatusUnauthorized, "Unauthorized", "authentication context is missing")
		return
	}
	result, err := h.service.Me(r.Context(), principal.UserID)
	if err != nil {
		identityProblem(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, result)
}

func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		httpx.Problem(w, r, http.StatusUnauthorized, "Unauthorized", "authentication context is missing")
		return
	}
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Problem(w, r, http.StatusBadRequest, "Invalid Request", err.Error())
		return
	}
	if err := h.service.ChangePassword(r.Context(), principal.UserID, input.CurrentPassword, input.NewPassword, requestInfo(r)); err != nil {
		identityProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Problem(w, r, http.StatusBadRequest, "Invalid Request", err.Error())
		return
	}
	token, err := h.service.ForgotPassword(r.Context(), input.Email, requestInfo(r))
	if err != nil {
		identityProblem(w, r, err)
		return
	}
	response := map[string]any{"message": "if the account exists, password reset instructions have been issued"}
	if h.exposeToken && token != "" {
		response["reset_token"] = token
	}
	httpx.JSON(w, http.StatusAccepted, response)
}

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Problem(w, r, http.StatusBadRequest, "Invalid Request", err.Error())
		return
	}
	if err := h.service.ResetPassword(r.Context(), strings.TrimSpace(input.Token), input.NewPassword, requestInfo(r)); err != nil {
		identityProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.Problem(w, r, http.StatusBadRequest, "Invalid Request", err.Error())
		return
	}
	if err := h.service.VerifyEmail(r.Context(), strings.TrimSpace(input.Token), requestInfo(r)); err != nil {
		identityProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func identityProblem(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalidCredentials), errors.Is(err, ErrInvalidToken):
		httpx.Problem(w, r, http.StatusUnauthorized, "Authentication Failed", err.Error())
	case errors.Is(err, ErrRefreshReuse):
		httpx.Problem(w, r, http.StatusUnauthorized, "Session Revoked", err.Error())
	case errors.Is(err, ErrAccountPending), errors.Is(err, ErrAccountSuspended), errors.Is(err, ErrAccountDisabled):
		httpx.Problem(w, r, http.StatusForbidden, "Account Unavailable", err.Error())
	case errors.Is(err, ErrAccountLocked):
		w.Header().Set("Retry-After", "900")
		httpx.Problem(w, r, http.StatusTooManyRequests, "Account Locked", err.Error())
	case errors.Is(err, ErrEmailExists):
		httpx.Problem(w, r, http.StatusConflict, "Registration Conflict", err.Error())
	default:
		httpx.Problem(w, r, http.StatusUnprocessableEntity, "Request Failed", err.Error())
	}
}

func requestInfo(r *http.Request) ClientInfo {
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		ip = host
	}
	return ClientInfo{IP: ip, UserAgent: r.UserAgent(), RequestID: httpx.RequestID(r.Context())}
}
