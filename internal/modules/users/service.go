package users

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/neoscoder/lms-service/internal/data"
	"github.com/neoscoder/lms-service/internal/platform/database"
	platformid "github.com/neoscoder/lms-service/internal/platform/id"
)

var (
	ErrNotFound  = errors.New("user not found")
	ErrSelfLock  = errors.New("administrators cannot suspend or disable their own account")
	ErrLastAdmin = errors.New("at least one administrator role is required")
)

type Service struct {
	db                  database.Beginner
	q                   *data.Queries
	ids                 platformid.Generator
	defaultOrganization string
}

func NewService(db database.Beginner, q *data.Queries, ids platformid.Generator, defaultOrganization string) *Service {
	return &Service{db: db, q: q, ids: ids, defaultOrganization: defaultOrganization}
}

func (s *Service) List(ctx context.Context, search, status, role string, size int32, cursorTime pgtype.Timestamptz, cursorID uuid.NullUUID) ([]data.ListAdminUsersRow, error) {
	if size < 1 || size > 100 {
		return nil, errors.New("limit must be between 1 and 100")
	}
	statusParam, roleParam := pgtype.Text{}, pgtype.Text{}
	if status != "" {
		if !allowed(status, "pending", "active", "suspended", "disabled") {
			return nil, errors.New("invalid status filter")
		}
		statusParam = pgtype.Text{String: status, Valid: true}
	}
	if role != "" {
		if !allowed(role, "student", "teacher", "admin") {
			return nil, errors.New("invalid role filter")
		}
		roleParam = pgtype.Text{String: role, Valid: true}
	}
	return s.q.ListAdminUsers(ctx, data.ListAdminUsersParams{Status: statusParam, RoleCode: roleParam, Search: strings.TrimSpace(search), CursorCreatedAt: cursorTime, CursorID: cursorID, PageSize: size})
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (data.GetUserProfileRow, error) {
	row, err := s.q.GetUserProfile(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return data.GetUserProfileRow{}, ErrNotFound
	}
	return row, err
}

func (s *Service) SetStatus(ctx context.Context, actorID, userID uuid.UUID, status string) error {
	if !allowed(status, "pending", "active", "suspended", "disabled") {
		return errors.New("status must be pending, active, suspended, or disabled")
	}
	if actorID == userID && (status == "suspended" || status == "disabled") {
		return ErrSelfLock
	}
	return database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		before, err := q.GetUser(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err = q.SetUserStatus(ctx, data.SetUserStatusParams{Status: status, ID: userID}); err != nil {
			return err
		}
		if status != "active" {
			if _, err = q.RevokeAllRefreshSessions(ctx, userID); err != nil {
				return err
			}
		}
		metadata, _ := json.Marshal(map[string]string{"before": before.Status, "after": status})
		return q.InsertSecurityAudit(ctx, data.InsertSecurityAuditParams{ID: s.ids.New(), ActorUserID: uuid.NullUUID{UUID: actorID, Valid: true}, Action: "admin.user.status_changed", ResourceType: "user", ResourceID: uuid.NullUUID{UUID: userID, Valid: true}, Metadata: metadata})
	})
}

func (s *Service) ReplaceRoles(ctx context.Context, actorID, userID uuid.UUID, roles []string) error {
	roles = unique(roles)
	if len(roles) == 0 {
		return errors.New("at least one role is required")
	}
	for _, role := range roles {
		if !allowed(role, "student", "teacher", "admin") {
			return errors.New("roles may contain only student, teacher, or admin")
		}
	}
	if actorID == userID && !contains(roles, "admin") {
		return ErrLastAdmin
	}
	return database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.GetUser(ctx, userID); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		organization, err := q.GetOrganizationBySlug(ctx, s.defaultOrganization)
		if err != nil {
			return err
		}
		membership, err := q.CreateMembership(ctx, data.CreateMembershipParams{ID: s.ids.New(), OrganizationID: organization.ID, UserID: userID, Status: "active"})
		if err != nil {
			return err
		}
		if err = q.DeleteGlobalRoles(ctx, userID); err != nil {
			return err
		}
		if err = q.DeleteMembershipRoles(ctx, membership.ID); err != nil {
			return err
		}
		for _, roleCode := range roles {
			globalRole, err := q.GetRoleByCode(ctx, roleCode)
			if err != nil {
				return err
			}
			if err = q.AssignGlobalRole(ctx, data.AssignGlobalRoleParams{UserID: userID, RoleID: globalRole.ID, AssignedBy: uuid.NullUUID{UUID: actorID, Valid: true}}); err != nil {
				return err
			}
			legacyCode := map[string]string{"admin": "organization_admin", "teacher": "instructor", "student": "student"}[roleCode]
			legacyRole, err := q.GetRoleByCode(ctx, legacyCode)
			if err != nil {
				return err
			}
			if err = q.AssignMembershipRole(ctx, data.AssignMembershipRoleParams{MembershipID: membership.ID, RoleID: legacyRole.ID}); err != nil {
				return err
			}
			switch roleCode {
			case "student":
				if err = q.CreateStudentProfile(ctx, userID); err != nil {
					return err
				}
			case "teacher":
				if err = q.CreateTeacherProfile(ctx, userID); err != nil {
					return err
				}
			}
		}
		if _, err = q.RevokeAllRefreshSessions(ctx, userID); err != nil {
			return err
		}
		metadata, _ := json.Marshal(map[string]any{"roles": roles})
		return q.InsertSecurityAudit(ctx, data.InsertSecurityAuditParams{ID: s.ids.New(), ActorUserID: uuid.NullUUID{UUID: actorID, Valid: true}, Action: "admin.user.roles_replaced", ResourceType: "user", ResourceID: uuid.NullUUID{UUID: userID, Valid: true}, Metadata: metadata})
	})
}

func (s *Service) UpdateProfile(ctx context.Context, userID uuid.UUID, displayName, firstName, lastName, phone, timezone, locale, biography string, expertise []string) error {
	displayName, firstName, lastName = strings.TrimSpace(displayName), strings.TrimSpace(firstName), strings.TrimSpace(lastName)
	if len(displayName) < 2 || len(displayName) > 120 || len(firstName) > 100 || len(lastName) > 100 {
		return errors.New("profile name values are invalid")
	}
	if timezone == "" {
		timezone = "UTC"
	}
	if locale == "" {
		locale = "en"
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return errors.New("timezone must be an IANA timezone")
	}
	return database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.UpdateUserDisplayName(ctx, data.UpdateUserDisplayNameParams{DisplayName: displayName, ID: userID}); err != nil {
			return err
		}
		phoneValue := pgtype.Text{String: strings.TrimSpace(phone), Valid: strings.TrimSpace(phone) != ""}
		if err := q.UpdateUserProfile(ctx, data.UpdateUserProfileParams{FirstName: firstName, LastName: lastName, Phone: phoneValue, Timezone: timezone, Locale: locale, UserID: userID}); err != nil {
			return err
		}
		if containsRole, _ := q.UserHasGlobalRole(ctx, data.UserHasGlobalRoleParams{UserID: userID, RoleCode: "teacher"}); containsRole {
			if err := q.UpsertTeacherProfile(ctx, data.UpsertTeacherProfileParams{UserID: userID, Biography: strings.TrimSpace(biography), Expertise: unique(expertise)}); err != nil {
				return err
			}
		}
		return nil
	})
}

func allowed(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}
func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func unique(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
