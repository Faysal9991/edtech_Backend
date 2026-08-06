package config

import (
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		App:           App{Environment: "test"},
		HTTP:          HTTP{Port: 8080, ReadTimeout: time.Second, WriteTimeout: time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second},
		Database:      Database{URL: "postgres://test", MaxConns: 1, QueryTimeout: time.Second},
		Auth:          Auth{Issuer: "test", Audience: "clients", KeyID: "v1", SigningKey: "01234567890123456789012345678901", AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour, VerificationTTL: time.Hour, PasswordResetTTL: time.Minute},
		Password:      Password{MemoryKiB: 19 * 1024, Iterations: 2, Parallelism: 1, SaltBytes: 16, KeyBytes: 32},
		Storage:       Storage{Endpoint: "http://storage", PublicEndpoint: "http://storage", Bucket: "test", AccessKey: "access", SecretKey: "0123456789012345", SignedURLTTL: time.Minute},
		Upload:        Upload{MaxVideoBytes: 1, MaxPDFBytes: 1, MaxImageBytes: 1},
		RateLimit:     RateLimit{Requests: 1, Window: time.Second},
		DummyPayment:  DummyPayment{WebhookSecret: "0123456789012345"},
		LiveKit:       LiveKit{URL: "ws://livekit", APIKey: "key", APISecret: "0123456789012345"},
		Notification:  Notification{Provider: "log", EncryptionKey: "12345678901234567890123456789012"},
		Observability: Observability{LogLevel: "info"},
		Worker:        Worker{Concurrency: 1},
	}
}

func TestProductionRejectsDummyPayment(t *testing.T) {
	cfg := validConfig()
	cfg.App.Environment = "production"
	cfg.Storage.Endpoint = "https://storage.example.test"
	cfg.Storage.PublicEndpoint = "https://storage.example.test"
	cfg.LiveKit.URL = "wss://livekit.example.test"
	cfg.URLs.PublicAPI = "https://api.example.test"
	cfg.URLs.CertificateVerify = "https://api.example.test/certificates/verify"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "dummy payment") {
		t.Fatal("dummy payments must never be accepted in production")
	}
}

func TestInvalidNumericConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("S3_ENDPOINT", "http://s3")
	t.Setenv("S3_BUCKET", "bucket")
	t.Setenv("HTTP_PORT", "not-a-number")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid HTTP_PORT error")
	}
}

func TestInvalidEnvironmentIsRejected(t *testing.T) {
	cfg := validConfig()
	cfg.App.Environment = "staging"
	if err := cfg.Validate(); err == nil {
		t.Fatal("unknown APP_ENV must be rejected so production safeguards cannot be bypassed")
	}
}

func TestDatabaseOnlyConfigDoesNotRequireServiceCredentials(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("S3_ENDPOINT", "")
	t.Setenv("S3_BUCKET", "")
	if _, err := LoadDatabase(); err != nil {
		t.Fatalf("database-only command should not require S3 configuration: %v", err)
	}
}

func TestRuntimeConfigRejectsWeakDeviceTokenEncryptionKey(t *testing.T) {
	cfg := validConfig()
	cfg.Notification.EncryptionKey = "too-short"
	if err := cfg.Validate(); err == nil {
		t.Fatal("weak device-token encryption key must be rejected")
	}
}
