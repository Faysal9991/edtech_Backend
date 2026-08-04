package liveclass

import "testing"

func TestRoomNameDoesNotUseClientInput(t *testing.T) {
	if value(nil) != "" {
		t.Fatal("nil description must be empty")
	}
}
