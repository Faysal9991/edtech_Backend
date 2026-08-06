package assignment

import (
	"testing"

	api "github.com/neoscoder/lms-service/internal/api"
)

func TestValidateScore(t *testing.T) {
	in := api.AssignmentWrite{Title: "Capstone", MaximumScore: 100, PassingScore: 60, MaximumSubmissions: 2}
	if err := validate(in); err != nil {
		t.Fatal(err)
	}
	in.PassingScore = 101
	if err := validate(in); err == nil {
		t.Fatal("passing score above maximum must fail")
	}
}
