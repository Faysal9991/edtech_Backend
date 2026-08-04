package config

import (
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		App:          App{Environment: "test"},
		HTTP:         HTTP{Port: 8080, ReadTimeout: time.Second, WriteTimeout: time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second},
		Database:     Database{URL: "postgres://test", MaxConns: 1, QueryTimeout: time.Second},
		Storage:      Storage{Endpoint: "http://storage", Bucket: "test", SignedURLTTL: time.Minute},
		Upload:       Upload{MaxVideoBytes: 1, MaxPDFBytes: 1, MaxImageBytes: 1},
		RateLimit:    RateLimit{Requests: 1, Window: time.Second},
		Notification: Notification{EncryptionKey: "12345678901234567890123456789012"},
	}
}

func TestProductionRejectsFakeAuth(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("FAKE_AUTH_ENABLED", "true")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("S3_ENDPOINT", "http://s3")
	t.Setenv("S3_BUCKET", "bucket")
	if _, err := Load(); err == nil {
		t.Fatal("expected production fake auth to be rejected")
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
