package auth

import (
	"strings"
	"testing"

	"github.com/neoscoder/lms-service/internal/platform/config"
)

func TestPasswordHasherRoundTripAndStrictParsing(t *testing.T) {
	hasher := NewPasswordHasher(config.Password{MemoryKiB: 19 * 1024, Iterations: 2, Parallelism: 1, SaltBytes: 16, KeyBytes: 32})
	encoded, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("unexpected PHC format: %s", encoded)
	}
	valid, err := hasher.Verify("correct horse battery staple", encoded)
	if err != nil || !valid {
		t.Fatalf("correct password rejected: valid=%t err=%v", valid, err)
	}
	valid, err = hasher.Verify("incorrect horse battery staple", encoded)
	if err != nil || valid {
		t.Fatalf("incorrect password accepted: valid=%t err=%v", valid, err)
	}
	if _, err = hasher.Verify("anything", "$argon2id$v=19$m=999999999,t=9,p=1$AAAA$AAAA"); err == nil {
		t.Fatal("unsafe PHC parameters must be rejected")
	}
}

func TestPasswordHasherRejectsWeakAndOversizedPasswords(t *testing.T) {
	hasher := NewPasswordHasher(config.Password{MemoryKiB: 19 * 1024, Iterations: 2, Parallelism: 1, SaltBytes: 16, KeyBytes: 32})
	if _, err := hasher.Hash("too-short"); err == nil {
		t.Fatal("weak password was accepted")
	}
	if _, err := hasher.Hash(strings.Repeat("x", 1025)); err == nil {
		t.Fatal("oversized password was accepted")
	}
}
