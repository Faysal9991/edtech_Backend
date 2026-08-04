package reporting

import (
	"testing"
	"time"
)

func TestRangeRejectsUnbounded(t *testing.T) {
	from := time.Now().Add(-400 * 24 * time.Hour)
	to := time.Now()
	if _, _, err := Range(&from, &to); err == nil {
		t.Fatal("expected range limit")
	}
}
