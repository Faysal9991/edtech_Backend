package id

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/google/uuid"
)

type Generator interface {
	New() uuid.UUID
	Token(bytes int) (string, error)
}

type Secure struct{}

func (Secure) New() uuid.UUID {
	id, err := uuid.NewV7()
	if err != nil {
		panic(fmt.Sprintf("uuidv7 generator failed: %v", err))
	}
	return id
}

func (Secure) Token(size int) (string, error) {
	if size < 16 {
		size = 16
	}
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
