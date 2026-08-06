package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/neoscoder/lms-service/internal/platform/config"
	"golang.org/x/crypto/argon2"
)

var ErrInvalidPasswordHash = errors.New("invalid password hash")

// PasswordHasher stores all Argon2id cost parameters in the PHC string so a
// future login can detect and upgrade older hashes without a flag day.
type PasswordHasher struct{ params config.Password }

func NewPasswordHasher(params config.Password) *PasswordHasher {
	return &PasswordHasher{params: params}
}

func (h *PasswordHasher) Hash(password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", errors.New("password must contain 12 to 1024 bytes")
	}
	salt := make([]byte, h.params.SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Iterations, h.params.MemoryKiB, h.params.Parallelism, h.params.KeyBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.params.MemoryKiB, h.params.Iterations, h.params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func (h *PasswordHasher) Verify(password, encoded string) (bool, error) {
	params, salt, expected, err := parsePHC(encoded)
	if err != nil {
		return false, err
	}
	// The parsed key is bounded to 32..64 bytes immediately below.
	actual := argon2.IDKey([]byte(password), salt, params.Iterations, params.MemoryKiB, params.Parallelism, uint32(len(expected))) // #nosec G115 -- bounded PHC key length
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func parsePHC(encoded string) (config.Password, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return config.Password{}, nil, nil, ErrInvalidPasswordHash
	}
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return config.Password{}, nil, nil, ErrInvalidPasswordHash
	}
	// Bounds prevent a corrupted or attacker-controlled PHC string from forcing
	// unreasonable allocation or CPU work during verification.
	if memory < 19*1024 || memory > 1024*1024 || iterations < 2 || iterations > 12 || parallelism < 1 || parallelism > 32 {
		return config.Password{}, nil, nil, ErrInvalidPasswordHash
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return config.Password{}, nil, nil, ErrInvalidPasswordHash
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) < 32 || len(key) > 64 {
		return config.Password{}, nil, nil, ErrInvalidPasswordHash
	}
	return config.Password{MemoryKiB: memory, Iterations: iterations, Parallelism: parallelism}, salt, key, nil
}
