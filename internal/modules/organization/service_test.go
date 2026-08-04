package organization

import (
	"testing"

	"github.com/google/uuid"
)

func TestNullUUID(t *testing.T) {
	if NullUUID(uuid.Nil).Valid {
		t.Fatal("nil UUID must be invalid")
	}
	if !NullUUID(uuid.New()).Valid {
		t.Fatal("non-nil UUID must be valid")
	}
}
