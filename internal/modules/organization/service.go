package organization

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	api "github.com/Faysal9991/edtech_Backend/internal/api"
	"github.com/Faysal9991/edtech_Backend/internal/data"
	"github.com/Faysal9991/edtech_Backend/internal/platform/clock"
	"github.com/Faysal9991/edtech_Backend/internal/platform/database"
	platformid "github.com/Faysal9991/edtech_Backend/internal/platform/id"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

var (
	ErrNotFound                = errors.New("organization resource not found")
	ErrInvitationEmailMismatch = errors.New("invitation email does not match authenticated email")
	ErrInvitationUnavailable   = errors.New("invitation is expired or unavailable")
)

type Service struct {
	db    database.Beginner
	q     *data.Queries
	ids   platformid.Generator
	clock clock.Clock
}

func NewService(db database.Beginner, q *data.Queries, ids platformid.Generator, c clock.Clock) *Service {
	return &Service{db: db, q: q, ids: ids, clock: c}
}

func (s *Service) Me(ctx context.Context, userID uuid.UUID) (api.User, error) {
	u, err := s.q.GetUser(ctx, userID)
	if err != nil {
		return api.User{}, err
	}
	memberships, err := s.q.ListUserMemberships(ctx, userID)
	if err != nil {
		return api.User{}, err
	}
	out := api.User{Id: u.ID, Email: openapi_types.Email(u.Email), DisplayName: u.DisplayName, Status: api.UserStatus(u.Status), Memberships: make([]api.Membership, 0, len(memberships))}
	for _, m := range memberships {
		roles := make([]api.MembershipRoles, len(m.Roles))
		for i, role := range m.Roles {
			roles[i] = api.MembershipRoles(role)
		}
		out.Memberships = append(out.Memberships, api.Membership{Id: m.ID, OrganizationId: m.OrganizationID, OrganizationName: m.OrganizationName, Status: m.Status, Roles: roles})
	}
	return out, nil
}

func (s *Service) Create(ctx context.Context, name, slug string) (data.Organization, error) {
	name = strings.TrimSpace(name)
	slug = strings.ToLower(strings.TrimSpace(slug))
	if len(name) < 2 || slug == "" {
		return data.Organization{}, errors.New("name and slug are required")
	}
	return s.q.CreateOrganization(ctx, data.CreateOrganizationParams{ID: s.ids.New(), Name: name, Slug: slug})
}
func (s *Service) Get(ctx context.Context, id uuid.UUID) (data.Organization, error) {
	v, err := s.q.GetOrganization(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return data.Organization{}, ErrNotFound
	}
	return v, err
}

type InvitationResult struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Service) Invite(ctx context.Context, organizationID, actorID uuid.UUID, email, roleCode string) (InvitationResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") {
		return InvitationResult{}, errors.New("valid email is required")
	}
	switch roleCode {
	case "organization_admin", "instructor", "student":
	default:
		return InvitationResult{}, errors.New("invalid invitation role")
	}
	role, err := s.q.GetRoleByCode(ctx, roleCode)
	if err != nil {
		return InvitationResult{}, err
	}
	token, err := s.ids.Token(32)
	if err != nil {
		return InvitationResult{}, err
	}
	sum := sha256.Sum256([]byte(token))
	expires := s.clock.Now().Add(7 * 24 * time.Hour)
	var row data.UserInvitation
	err = database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var err error
		row, err = q.CreateInvitation(ctx, data.CreateInvitationParams{ID: s.ids.New(), OrganizationID: organizationID, Email: email, RoleID: role.ID, TokenHash: hex.EncodeToString(sum[:]), InvitedBy: actorID, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}})
		if err != nil {
			return err
		}
		invitedUser, e := q.GetUserByEmail(ctx, email)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		payload, _ := json.Marshal(map[string]string{"user_id": invitedUser.ID.String(), "organization_id": organizationID.String(), "invitation_id": row.ID.String(), "role": roleCode})
		return q.InsertOutboxEvent(ctx, data.InsertOutboxEventParams{ID: s.ids.New(), AggregateType: "invitation", AggregateID: row.ID, EventType: "invitation.created", Payload: payload, DeduplicationKey: "invitation.created:" + row.ID.String()})
	})
	if err != nil {
		return InvitationResult{}, fmt.Errorf("create invitation: %w", err)
	}
	return InvitationResult{ID: row.ID, Email: row.Email, Role: roleCode, Token: token, ExpiresAt: expires}, nil
}

func (s *Service) AcceptInvitation(ctx context.Context, userID uuid.UUID, authenticatedEmail, token string) error {
	sum := sha256.Sum256([]byte(token))
	return database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		inv, err := q.GetInvitationByTokenHashForUpdate(ctx, hex.EncodeToString(sum[:]))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvitationUnavailable
		}
		if err != nil {
			return err
		}
		if inv.Status != "pending" || !inv.ExpiresAt.Valid || !inv.ExpiresAt.Time.After(s.clock.Now()) {
			return ErrInvitationUnavailable
		}
		if !strings.EqualFold(inv.Email, authenticatedEmail) {
			return ErrInvitationEmailMismatch
		}
		membership, err := q.CreateMembership(ctx, data.CreateMembershipParams{ID: s.ids.New(), OrganizationID: inv.OrganizationID, UserID: userID, Status: "active"})
		if err != nil {
			return err
		}
		if err := q.AssignMembershipRole(ctx, data.AssignMembershipRoleParams{MembershipID: membership.ID, RoleID: inv.RoleID}); err != nil {
			return err
		}
		_, err = q.AcceptInvitation(ctx, inv.ID)
		return err
	})
}

func (s *Service) UpdateMembership(ctx context.Context, organizationID, membershipID, actorID uuid.UUID, in api.MembershipUpdate) (data.OrganizationMembership, []string, error) {
	status := string(in.Status)
	if status != "active" && status != "suspended" {
		return data.OrganizationMembership{}, nil, errors.New("membership status must be active or suspended")
	}
	if len(in.Roles) == 0 {
		return data.OrganizationMembership{}, nil, errors.New("at least one role is required")
	}
	roleCodes := make([]string, 0, len(in.Roles))
	seen := map[string]struct{}{}
	for _, roleValue := range in.Roles {
		roleCode := string(roleValue)
		if roleCode != "organization_admin" && roleCode != "instructor" && roleCode != "student" {
			return data.OrganizationMembership{}, nil, errors.New("invalid membership role")
		}
		if _, duplicate := seen[roleCode]; duplicate {
			continue
		}
		seen[roleCode] = struct{}{}
		roleCodes = append(roleCodes, roleCode)
	}
	var updated data.OrganizationMembership
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		before, err := q.GetMembershipForUpdate(ctx, data.GetMembershipForUpdateParams{ID: membershipID, OrganizationID: organizationID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		beforeRoles, err := q.ListMembershipRoleCodes(ctx, membershipID)
		if err != nil {
			return err
		}
		for _, role := range beforeRoles {
			if role == "super_admin" {
				return errors.New("super administrator membership cannot be changed through an organization endpoint")
			}
		}
		if before.UserID == actorID && status == "suspended" {
			return errors.New("administrators cannot suspend their own current membership")
		}
		updated, err = q.SetMembershipStatus(ctx, data.SetMembershipStatusParams{Status: status, ID: membershipID, OrganizationID: organizationID})
		if err != nil {
			return err
		}
		if err := q.DeleteMembershipRoles(ctx, membershipID); err != nil {
			return err
		}
		for _, roleCode := range roleCodes {
			role, err := q.GetRoleByCode(ctx, roleCode)
			if err != nil {
				return err
			}
			if err := q.AssignMembershipRole(ctx, data.AssignMembershipRoleParams{MembershipID: membershipID, RoleID: role.ID}); err != nil {
				return err
			}
		}
		beforeJSON, _ := json.Marshal(map[string]any{"membership": before, "roles": beforeRoles})
		afterJSON, _ := json.Marshal(map[string]any{"membership": updated, "roles": roleCodes})
		return q.InsertAuditLog(ctx, data.InsertAuditLogParams{ID: s.ids.New(), OrganizationID: NullUUID(organizationID), ActorUserID: NullUUID(actorID), Action: "organization.membership.updated", ResourceType: "organization_membership", ResourceID: NullUUID(membershipID), BeforeData: beforeJSON, AfterData: afterJSON})
	})
	return updated, roleCodes, err
}

func NullUUID(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil} }
