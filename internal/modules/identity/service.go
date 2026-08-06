package identity

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/neoscoder/lms-service/internal/data"
	"github.com/neoscoder/lms-service/internal/platform/auth"
	"github.com/neoscoder/lms-service/internal/platform/config"
	"github.com/neoscoder/lms-service/internal/platform/database"
	platformid "github.com/neoscoder/lms-service/internal/platform/id"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrAccountPending     = errors.New("email verification is required")
	ErrAccountSuspended   = errors.New("account is suspended")
	ErrAccountDisabled    = errors.New("account is disabled")
	ErrAccountLocked      = errors.New("account is temporarily locked")
	ErrEmailExists        = errors.New("an account with this email already exists")
	ErrInvalidToken       = errors.New("token is invalid or expired")
	ErrRefreshReuse       = errors.New("refresh token reuse detected; the session family was revoked")
)

type ClientInfo struct {
	IP        string
	UserAgent string
	RequestID string
}

type User struct {
	ID              uuid.UUID  `json:"id"`
	Email           string     `json:"email"`
	DisplayName     string     `json:"display_name"`
	Status          string     `json:"status"`
	Roles           []string   `json:"roles"`
	EmailVerifiedAt *time.Time `json:"email_verified_at"`
}

type Registration struct {
	User              User   `json:"user"`
	VerificationToken string `json:"-"`
}

type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type Service struct {
	db                  database.Beginner
	q                   *data.Queries
	ids                 platformid.Generator
	hasher              *auth.PasswordHasher
	jwt                 *auth.JWTManager
	authConfig          config.Auth
	defaultOrganization string
	dummyHash           string
}

func NewService(db database.Beginner, q *data.Queries, ids platformid.Generator, hasher *auth.PasswordHasher, jwtManager *auth.JWTManager, authConfig config.Auth, defaultOrganization string) (*Service, error) {
	dummy, err := hasher.Hash("not-a-real-password-constant")
	if err != nil {
		return nil, err
	}
	return &Service{db: db, q: q, ids: ids, hasher: hasher, jwt: jwtManager, authConfig: authConfig, defaultOrganization: defaultOrganization, dummyHash: dummy}, nil
}

func (s *Service) Register(ctx context.Context, email, password, displayName string, info ClientInfo) (Registration, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	parsed, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(parsed.Address, email) || len(email) > 320 {
		return Registration{}, errors.New("a valid email address is required")
	}
	if len(displayName) < 2 || len(displayName) > 120 {
		return Registration{}, errors.New("display_name must contain 2 to 120 characters")
	}
	passwordHash, err := s.hasher.Hash(password)
	if err != nil {
		return Registration{}, err
	}
	rawToken, err := s.ids.Token(32)
	if err != nil {
		return Registration{}, err
	}
	tokenHash := sha256.Sum256([]byte(rawToken))
	var created data.User
	err = database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		created, err = q.CreatePasswordUser(ctx, data.CreatePasswordUserParams{ID: s.ids.New(), Email: email, DisplayName: displayName, PasswordHash: passwordHash})
		if err != nil {
			return err
		}
		studentRole, err := q.GetRoleByCode(ctx, "student")
		if err != nil {
			return err
		}
		if err = q.AssignGlobalRole(ctx, data.AssignGlobalRoleParams{UserID: created.ID, RoleID: studentRole.ID}); err != nil {
			return err
		}
		nameParts := strings.Fields(displayName)
		firstName, lastName := displayName, ""
		if len(nameParts) > 1 {
			firstName, lastName = nameParts[0], strings.Join(nameParts[1:], " ")
		}
		if err = q.CreateUserProfile(ctx, data.CreateUserProfileParams{UserID: created.ID, FirstName: firstName, LastName: lastName}); err != nil {
			return err
		}
		if err = q.CreateStudentProfile(ctx, created.ID); err != nil {
			return err
		}
		organization, err := q.GetOrganizationBySlug(ctx, s.defaultOrganization)
		if err != nil {
			return errors.New("default organization is unavailable; run the seed command")
		}
		membership, err := q.CreateMembership(ctx, data.CreateMembershipParams{ID: s.ids.New(), OrganizationID: organization.ID, UserID: created.ID, Status: "active"})
		if err != nil {
			return err
		}
		if err = q.AssignMembershipRole(ctx, data.AssignMembershipRoleParams{MembershipID: membership.ID, RoleID: studentRole.ID}); err != nil {
			return err
		}
		if err = q.CreateEmailVerificationToken(ctx, data.CreateEmailVerificationTokenParams{ID: s.ids.New(), UserID: created.ID, TokenHash: tokenHash[:], ExpiresAt: timestamptz(time.Now().UTC().Add(s.authConfig.VerificationTTL))}); err != nil {
			return err
		}
		return q.InsertSecurityAudit(ctx, auditParams(s.ids.New(), created.ID, "auth.registered", "user", created.ID, info, map[string]any{"email": email}))
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Registration{}, ErrEmailExists
		}
		return Registration{}, err
	}
	return Registration{User: userDTO(created, []string{"student"}), VerificationToken: rawToken}, nil
}

func (s *Service) Login(ctx context.Context, email, password string, info ClientInfo) (Tokens, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	user, err := s.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && user.PasswordHash == "") {
		_, _ = s.hasher.Verify(password, s.dummyHash)
		return Tokens{}, ErrInvalidCredentials
	}
	if err != nil {
		return Tokens{}, err
	}
	if user.LockedUntil.Valid && user.LockedUntil.Time.After(time.Now()) {
		return Tokens{}, ErrAccountLocked
	}
	valid, verifyErr := s.hasher.Verify(password, user.PasswordHash)
	if verifyErr != nil || !valid {
		_, _ = s.q.RecordFailedLogin(ctx, data.RecordFailedLoginParams{LockAfter: 5, LockDuration: pgtype.Interval{Microseconds: (15 * time.Minute).Microseconds(), Valid: true}, ID: user.ID})
		_ = s.q.InsertSecurityAudit(ctx, auditParams(s.ids.New(), user.ID, "auth.login_failed", "user", user.ID, info, map[string]any{"reason": "invalid_credentials"}))
		return Tokens{}, ErrInvalidCredentials
	}
	switch user.Status {
	case "pending":
		return Tokens{}, ErrAccountPending
	case "suspended":
		return Tokens{}, ErrAccountSuspended
	case "disabled":
		return Tokens{}, ErrAccountDisabled
	case "active":
	default:
		return Tokens{}, ErrAccountDisabled
	}
	user, err = s.q.RecordSuccessfulLogin(ctx, user.ID)
	if err != nil {
		return Tokens{}, err
	}
	roles, err := s.q.ListGlobalRoleCodes(ctx, user.ID)
	if err != nil {
		return Tokens{}, err
	}
	tokens, err := s.newSession(ctx, user.ID, roles, info)
	if err == nil {
		_ = s.q.InsertSecurityAudit(ctx, auditParams(s.ids.New(), user.ID, "auth.login_succeeded", "refresh_session", uuid.Nil, info, nil))
	}
	return tokens, err
}

func (s *Service) newSession(ctx context.Context, userID uuid.UUID, roles []string, info ClientInfo) (Tokens, error) {
	raw, err := s.ids.Token(32)
	if err != nil {
		return Tokens{}, err
	}
	access, expires, err := s.jwt.IssueAccess(userID, roles)
	if err != nil {
		return Tokens{}, err
	}
	hash := sha256.Sum256([]byte(raw))
	sessionID := s.ids.New()
	_, err = s.q.CreateRefreshSession(ctx, data.CreateRefreshSessionParams{ID: sessionID, FamilyID: sessionID, UserID: userID, TokenHash: hash[:], ExpiresAt: timestamptz(time.Now().UTC().Add(s.authConfig.RefreshTTL)), IpAddress: info.IP, UserAgent: truncate(info.UserAgent, 512)})
	if err != nil {
		return Tokens{}, err
	}
	return Tokens{AccessToken: access, RefreshToken: raw, TokenType: "Bearer", ExpiresAt: expires}, nil
}

func (s *Service) Refresh(ctx context.Context, raw string, info ClientInfo) (Tokens, error) {
	if len(raw) < 32 || len(raw) > 256 {
		return Tokens{}, ErrInvalidToken
	}
	oldHash := sha256.Sum256([]byte(raw))
	var output Tokens
	var outcome error
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		session, err := q.GetRefreshSessionForUpdate(ctx, oldHash[:])
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = ErrInvalidToken
			return nil
		}
		if err != nil {
			return err
		}
		if session.RotatedAt.Valid || session.RevokedAt.Valid {
			if err = q.RevokeRefreshFamilyForReuse(ctx, session.FamilyID); err != nil {
				return err
			}
			if err = q.InsertSecurityAudit(ctx, auditParams(s.ids.New(), session.UserID, "auth.refresh_reuse_detected", "refresh_session", session.ID, info, map[string]any{"family_id": session.FamilyID})); err != nil {
				return err
			}
			outcome = ErrRefreshReuse
			return nil
		}
		if !session.ExpiresAt.Valid || !session.ExpiresAt.Time.After(time.Now()) {
			if err = q.RevokeRefreshFamilyForReuse(ctx, session.FamilyID); err != nil {
				return err
			}
			outcome = ErrInvalidToken
			return nil
		}
		user, err := q.GetUser(ctx, session.UserID)
		if err != nil || user.Status != "active" {
			outcome = ErrInvalidToken
			return nil
		}
		roles, err := q.ListGlobalRoleCodes(ctx, user.ID)
		if err != nil {
			return err
		}
		newRaw, err := s.ids.Token(32)
		if err != nil {
			return err
		}
		access, expires, err := s.jwt.IssueAccess(user.ID, roles)
		if err != nil {
			return err
		}
		newHash := sha256.Sum256([]byte(newRaw))
		if err = q.MarkRefreshSessionRotated(ctx, session.ID); err != nil {
			return err
		}
		_, err = q.CreateRefreshSession(ctx, data.CreateRefreshSessionParams{ID: s.ids.New(), FamilyID: session.FamilyID, ParentID: uuid.NullUUID{UUID: session.ID, Valid: true}, UserID: user.ID, TokenHash: newHash[:], ExpiresAt: timestamptz(time.Now().UTC().Add(s.authConfig.RefreshTTL)), IpAddress: info.IP, UserAgent: truncate(info.UserAgent, 512)})
		if err != nil {
			return err
		}
		output = Tokens{AccessToken: access, RefreshToken: newRaw, TokenType: "Bearer", ExpiresAt: expires}
		return q.InsertSecurityAudit(ctx, auditParams(s.ids.New(), user.ID, "auth.refresh_rotated", "refresh_session", session.ID, info, nil))
	})
	if err != nil {
		return Tokens{}, err
	}
	if outcome != nil {
		return Tokens{}, outcome
	}
	return output, nil
}

func (s *Service) Logout(ctx context.Context, raw string, userID uuid.UUID, info ClientInfo) error {
	hash := sha256.Sum256([]byte(raw))
	_, err := s.q.RevokeRefreshSession(ctx, hash[:])
	if err == nil {
		err = s.q.InsertSecurityAudit(ctx, auditParams(s.ids.New(), userID, "auth.logout", "user", userID, info, nil))
	}
	return err
}

func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID, info ClientInfo) error {
	_, err := s.q.RevokeAllRefreshSessions(ctx, userID)
	if err == nil {
		err = s.q.InsertSecurityAudit(ctx, auditParams(s.ids.New(), userID, "auth.logout_all", "user", userID, info, nil))
	}
	return err
}

func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, oldPassword, newPassword string, info ClientInfo) error {
	user, err := s.q.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	valid, err := s.hasher.Verify(oldPassword, user.PasswordHash)
	if err != nil || !valid {
		return ErrInvalidCredentials
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	return database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.SetUserPassword(ctx, data.SetUserPasswordParams{PasswordHash: hash, ID: userID}); err != nil {
			return err
		}
		if _, err := q.RevokeAllRefreshSessions(ctx, userID); err != nil {
			return err
		}
		return q.InsertSecurityAudit(ctx, auditParams(s.ids.New(), userID, "auth.password_changed", "user", userID, info, nil))
	})
}

func (s *Service) ForgotPassword(ctx context.Context, email string, info ClientInfo) (string, error) {
	user, err := s.q.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && user.PasswordHash == "") {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	raw, err := s.ids.Token(32)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(raw))
	err = database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.DeleteActivePasswordResetTokens(ctx, user.ID); err != nil {
			return err
		}
		if err := q.CreatePasswordResetToken(ctx, data.CreatePasswordResetTokenParams{ID: s.ids.New(), UserID: user.ID, TokenHash: hash[:], ExpiresAt: timestamptz(time.Now().UTC().Add(s.authConfig.PasswordResetTTL))}); err != nil {
			return err
		}
		return q.InsertSecurityAudit(ctx, auditParams(s.ids.New(), user.ID, "auth.password_reset_requested", "user", user.ID, info, nil))
	})
	return raw, err
}

func (s *Service) ResetPassword(ctx context.Context, raw, password string, info ClientInfo) error {
	newHash, err := s.hasher.Hash(password)
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(raw))
	var outcome error
	err = database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		userID, err := q.ConsumePasswordResetToken(ctx, hash[:])
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = ErrInvalidToken
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = q.SetUserPassword(ctx, data.SetUserPasswordParams{PasswordHash: newHash, ID: userID}); err != nil {
			return err
		}
		if _, err = q.RevokeAllRefreshSessions(ctx, userID); err != nil {
			return err
		}
		return q.InsertSecurityAudit(ctx, auditParams(s.ids.New(), userID, "auth.password_reset_completed", "user", userID, info, nil))
	})
	if err != nil {
		return err
	}
	return outcome
}

func (s *Service) VerifyEmail(ctx context.Context, raw string, info ClientInfo) error {
	hash := sha256.Sum256([]byte(raw))
	var outcome error
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		userID, err := q.ConsumeEmailVerificationToken(ctx, hash[:])
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = ErrInvalidToken
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = q.VerifyUserEmail(ctx, userID); err != nil {
			return err
		}
		return q.InsertSecurityAudit(ctx, auditParams(s.ids.New(), userID, "auth.email_verified", "user", userID, info, nil))
	})
	if err != nil {
		return err
	}
	return outcome
}

func (s *Service) Me(ctx context.Context, userID uuid.UUID) (User, error) {
	row, err := s.q.GetUser(ctx, userID)
	if err != nil {
		return User{}, err
	}
	roles, err := s.q.ListGlobalRoleCodes(ctx, userID)
	if err != nil {
		return User{}, err
	}
	return userDTO(row, roles), nil
}

func userDTO(row data.User, roles []string) User {
	out := User{ID: row.ID, Email: row.Email, DisplayName: row.DisplayName, Status: row.Status, Roles: roles}
	if row.EmailVerifiedAt.Valid {
		value := row.EmailVerifiedAt.Time.UTC()
		out.EmailVerifiedAt = &value
	}
	return out
}

func timestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func auditParams(id, actorID uuid.UUID, action, resourceType string, resourceID uuid.UUID, info ClientInfo, metadata map[string]any) data.InsertSecurityAuditParams {
	if metadata == nil {
		metadata = map[string]any{}
	}
	payload, _ := json.Marshal(metadata)
	return data.InsertSecurityAuditParams{ID: id, ActorUserID: uuid.NullUUID{UUID: actorID, Valid: actorID != uuid.Nil}, Action: action, ResourceType: resourceType, ResourceID: uuid.NullUUID{UUID: resourceID, Valid: resourceID != uuid.Nil}, RequestID: pgtype.Text{String: info.RequestID, Valid: info.RequestID != ""}, IpAddress: info.IP, UserAgent: truncate(info.UserAgent, 512), Metadata: payload}
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
