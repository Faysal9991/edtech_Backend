package course

import (
	"strings"
	"testing"

	api "github.com/neoscoder/lms-service/internal/api"
	"github.com/neoscoder/lms-service/internal/data"
)

func TestValidateWriteMoney(t *testing.T) {
	base := api.CourseWrite{Title: "Go", Description: "Production Go", Language: "en", Level: "beginner", IsFree: true, Currency: "BDT"}
	if err := validateWrite(base); err != nil {
		t.Fatal(err)
	}
	base.PriceMinor = 10
	if err := validateWrite(base); err == nil {
		t.Fatal("free course with price must fail")
	}
	base.IsFree = false
	if err := validateWrite(base); err != nil {
		t.Fatal(err)
	}
}

func TestCoursePublicationValidation(t *testing.T) {
	valid := data.CoursePublishFactsRow{Title: "Production Go", Description: "A complete course", IsFree: true, InstructorCount: 1, ModuleCount: 1, PublishedLessonCount: 1}
	if err := validatePublishFacts(valid); err != nil {
		t.Fatalf("valid course rejected: %v", err)
	}
	invalid := valid
	invalid.PublishedLessonCount = 0
	invalid.UnreadyMediaCount = 1
	err := validatePublishFacts(invalid)
	if err == nil || !strings.Contains(err.Error(), "published lesson") || !strings.Contains(err.Error(), "media") {
		t.Fatalf("expected all publication violations, got %v", err)
	}
	paid := valid
	paid.IsFree = false
	if err := validatePublishFacts(paid); err == nil || !strings.Contains(err.Error(), "price") {
		t.Fatalf("paid course without price should be rejected, got %v", err)
	}
}
