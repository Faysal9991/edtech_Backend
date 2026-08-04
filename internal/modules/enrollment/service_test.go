package enrollment

import "testing"

func TestProgressState(t *testing.T) {
	state, err := progressState("video", 100, 89, false, false, 90)
	if err != nil || state != "started" {
		t.Fatalf("unexpected %s %v", state, err)
	}
	state, err = progressState("video", 100, 90, false, false, 90)
	if err != nil || state != "completed" {
		t.Fatalf("unexpected %s %v", state, err)
	}
	if _, err = progressState("video", 100, 0, true, false, 90); err == nil {
		t.Fatal("manual video completion must fail")
	}
	state, err = progressState("pdf", 0, 0, true, false, 90)
	if err != nil || state != "completed" {
		t.Fatalf("manual PDF completion failed: %s %v", state, err)
	}
}
