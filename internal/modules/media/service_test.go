package media

import (
	"testing"

	api "github.com/Faysal9991/edtech_Backend/internal/api"
	"github.com/Faysal9991/edtech_Backend/internal/platform/config"
)

func TestUploadValidation(t *testing.T) {
	s := &Service{cfg: config.Config{Upload: config.Upload{MaxVideoBytes: 100, MaxPDFBytes: 100, MaxImageBytes: 100}}}
	good := api.UploadIntentWrite{Kind: "video", Filename: "lesson.mp4", ContentType: "video/mp4", SizeBytes: 50}
	if err := s.validate(good); err != nil {
		t.Fatal(err)
	}
	good.Filename = "../lesson.mp4"
	if err := s.validate(good); err == nil {
		t.Fatal("path traversal filename must fail")
	}
	good.Filename = "lesson.pdf"
	if err := s.validate(good); err == nil {
		t.Fatal("mismatched extension must fail")
	}
}
