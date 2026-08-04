package course

import (
	"testing"

	api "github.com/Faysal9991/edtech_Backend/internal/api"
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
