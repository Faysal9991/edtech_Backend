package certificate

import (
	"bytes"
	"testing"
	"time"

	platformid "github.com/neoscoder/lms-service/internal/platform/id"
)

func TestRenderPDF(t *testing.T) {
	b, err := renderPDF("Student", "Go Backend", "Acme", "LMS-123", time.Unix(0, 0), "https://example.test/verify/x")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("%PDF")) {
		t.Fatal("expected PDF output")
	}
}

func TestVerificationTokensAreUnique(t *testing.T) {
	generator := platformid.Secure{}
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		token, err := generator.Token(24)
		if err != nil {
			t.Fatal(err)
		}
		if _, duplicate := seen[token]; duplicate {
			t.Fatalf("duplicate cryptographic token generated at iteration %d", i)
		}
		seen[token] = struct{}{}
	}
}
