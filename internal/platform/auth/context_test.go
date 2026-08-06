package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestPrincipalContextAndRoleMapping(t *testing.T) {
	principal := Principal{UserID: uuid.New(), Email: "student@example.test", Roles: []string{"student"}}
	ctx := WithPrincipal(context.Background(), principal)
	got, ok := PrincipalFrom(ctx)
	if !ok || got.UserID != principal.UserID || got.Email != principal.Email {
		t.Fatalf("principal context mismatch: %#v", got)
	}
	if !HasRole(got.Roles, "student", "teacher") {
		t.Fatal("student role should satisfy the permission mapping")
	}
	if HasRole(got.Roles, "admin", "teacher") {
		t.Fatal("student role must not gain an administrative permission")
	}
}
