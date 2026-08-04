package auth

import (
	"context"

	"github.com/google/uuid"
)

type Principal struct {
	UserID      uuid.UUID
	FirebaseUID string
	Email       string
	DisplayName string
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

type Membership struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Roles          []string
}

type membershipKey struct{}

func WithMembership(ctx context.Context, m Membership) context.Context {
	return context.WithValue(ctx, membershipKey{}, m)
}
func MembershipFrom(ctx context.Context) (Membership, bool) {
	m, ok := ctx.Value(membershipKey{}).(Membership)
	return m, ok
}

func HasRole(roles []string, wanted ...string) bool {
	for _, role := range roles {
		for _, w := range wanted {
			if role == w {
				return true
			}
		}
	}
	return false
}
