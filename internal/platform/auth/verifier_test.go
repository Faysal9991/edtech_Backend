package auth

import (
	"context"
	"testing"
)

func TestDevelopmentVerifier(t *testing.T) {
	v := DevelopmentVerifier{}
	got, err := v.Verify(context.Background(), "dev:student@example.com:Student Name")
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "student@example.com" || got.DisplayName != "Student Name" || got.UID == "" {
		t.Fatalf("unexpected identity: %#v", got)
	}
	if _, err := v.Verify(context.Background(), "invalid"); err == nil {
		t.Fatal("expected invalid token error")
	}
}
