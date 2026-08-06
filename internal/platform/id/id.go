package id

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type Generator interface {
	New() uuid.UUID
	Token(bytes int) (string, error)
}

type Secure struct{}

var fallbackSequence atomic.Uint64

func (Secure) New() uuid.UUID {
	id, err := uuid.NewV7()
	if err == nil {
		return id
	}
	// ID generation cannot return an error without changing every aggregate
	// boundary. If the operating-system RNG is temporarily unavailable, derive
	// collision-resistant fallback bytes from time, process, and an atomic
	// sequence, then still apply UUIDv7 timestamp/version bits. Secret tokens
	// never use this fallback and always fail closed through Token.
	material := fmt.Sprintf("%d:%d:%d", time.Now().UnixNano(), os.Getpid(), fallbackSequence.Add(1))
	sum := sha256.Sum256([]byte(material))
	fallback, _ := uuid.NewV7FromReader(bytes.NewReader(sum[:]))
	return fallback
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
