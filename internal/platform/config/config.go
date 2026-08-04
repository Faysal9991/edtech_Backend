package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	App           App
	HTTP          HTTP
	Database      Database
	Redis         Redis
	Storage       Storage
	Firebase      Firebase
	LiveKit       LiveKit
	Stripe        Stripe
	URLs          URLs
	Upload        Upload
	RateLimit     RateLimit
	Observability Observability
	Notification  Notification
}

type App struct {
	Environment     string
	Name            string
	FakeAuthEnabled bool
}

type HTTP struct {
	Host            string
	Port            int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	AllowedOrigins  []string
}

type Database struct {
	URL          string
	MaxConns     int32
	MinConns     int32
	QueryTimeout time.Duration
}

type Redis struct {
	Addr     string
	Password string
	DB       int
}

type Storage struct {
	Endpoint       string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	ForcePathStyle bool
	SignedURLTTL   time.Duration
}

type Firebase struct {
	ProjectID string
}

type LiveKit struct {
	URL       string
	APIKey    string
	APISecret string
}

type Stripe struct {
	SecretKey     string
	WebhookSecret string
}

type URLs struct {
	PublicAPI         string
	CertificateVerify string
}

type Upload struct {
	MaxVideoBytes int64
	MaxPDFBytes   int64
	MaxImageBytes int64
}

type RateLimit struct {
	Requests int
	Window   time.Duration
}

type Observability struct {
	OTLPEndpoint string
}
type Notification struct{ EncryptionKey string }

func Load() (Config, error) {
	c := Config{
		App:           App{Environment: value("APP_ENV", "development"), Name: value("APP_NAME", "lms-backend")},
		HTTP:          HTTP{Host: value("HTTP_HOST", "0.0.0.0")},
		Database:      Database{URL: os.Getenv("DATABASE_URL")},
		Redis:         Redis{Addr: value("REDIS_ADDR", "localhost:6379"), Password: os.Getenv("REDIS_PASSWORD")},
		Storage:       Storage{Endpoint: os.Getenv("S3_ENDPOINT"), Region: value("S3_REGION", "us-east-1"), Bucket: os.Getenv("S3_BUCKET"), AccessKey: os.Getenv("S3_ACCESS_KEY"), SecretKey: os.Getenv("S3_SECRET_KEY")},
		Firebase:      Firebase{ProjectID: os.Getenv("FIREBASE_PROJECT_ID")},
		LiveKit:       LiveKit{URL: os.Getenv("LIVEKIT_URL"), APIKey: os.Getenv("LIVEKIT_API_KEY"), APISecret: os.Getenv("LIVEKIT_API_SECRET")},
		Stripe:        Stripe{SecretKey: os.Getenv("STRIPE_SECRET_KEY"), WebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET")},
		URLs:          URLs{PublicAPI: value("PUBLIC_API_URL", "http://localhost:8080"), CertificateVerify: value("CERTIFICATE_VERIFY_BASE_URL", "http://localhost:8080/api/v1/public/certificates/verify")},
		Observability: Observability{OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")},
		Notification:  Notification{EncryptionKey: os.Getenv("DEVICE_TOKEN_ENCRYPTION_KEY")},
	}

	databaseConfig, err := LoadDatabase()
	if err != nil {
		return Config{}, err
	}
	c.Database = databaseConfig
	if c.App.FakeAuthEnabled, err = boolValue("FAKE_AUTH_ENABLED", false); err != nil {
		return Config{}, err
	}
	if c.HTTP.Port, err = intValue("HTTP_PORT", 8080); err != nil {
		return Config{}, err
	}
	if c.HTTP.ReadTimeout, err = durationValue("HTTP_READ_TIMEOUT", 15*time.Second); err != nil {
		return Config{}, err
	}
	if c.HTTP.WriteTimeout, err = durationValue("HTTP_WRITE_TIMEOUT", 30*time.Second); err != nil {
		return Config{}, err
	}
	if c.HTTP.IdleTimeout, err = durationValue("HTTP_IDLE_TIMEOUT", 60*time.Second); err != nil {
		return Config{}, err
	}
	if c.HTTP.ShutdownTimeout, err = durationValue("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second); err != nil {
		return Config{}, err
	}
	c.HTTP.AllowedOrigins = csvValue("CORS_ALLOWED_ORIGINS")
	if c.Redis.DB, err = intValue("REDIS_DB", 0); err != nil {
		return Config{}, err
	}
	if c.Storage.ForcePathStyle, err = boolValue("S3_FORCE_PATH_STYLE", true); err != nil {
		return Config{}, err
	}
	if c.Storage.SignedURLTTL, err = durationValue("SIGNED_URL_TTL", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if c.Upload.MaxVideoBytes, err = int64Value("UPLOAD_MAX_VIDEO_BYTES", 2<<30); err != nil {
		return Config{}, err
	}
	if c.Upload.MaxPDFBytes, err = int64Value("UPLOAD_MAX_PDF_BYTES", 50<<20); err != nil {
		return Config{}, err
	}
	if c.Upload.MaxImageBytes, err = int64Value("UPLOAD_MAX_IMAGE_BYTES", 10<<20); err != nil {
		return Config{}, err
	}
	if c.RateLimit.Requests, err = intValue("RATE_LIMIT_REQUESTS", 120); err != nil {
		return Config{}, err
	}
	if c.RateLimit.Window, err = durationValue("RATE_LIMIT_WINDOW", time.Minute); err != nil {
		return Config{}, err
	}

	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// LoadDatabase reads only the settings needed by database-only commands such
// as migrations, seeding, and one-shot administrator bootstrap.
func LoadDatabase() (Database, error) {
	db := Database{URL: strings.TrimSpace(os.Getenv("DATABASE_URL"))}
	if db.URL == "" {
		return Database{}, errors.New("missing required configuration: DATABASE_URL")
	}
	maxConns, err := intValue("DB_MAX_CONNS", 20)
	if err != nil {
		return Database{}, err
	}
	minConns, err := intValue("DB_MIN_CONNS", 2)
	if err != nil {
		return Database{}, err
	}
	queryTimeout, err := durationValue("DB_QUERY_TIMEOUT", 5*time.Second)
	if err != nil {
		return Database{}, err
	}
	db.MaxConns = int32(maxConns)
	db.MinConns = int32(minConns)
	db.QueryTimeout = queryTimeout
	if db.MinConns < 0 || db.MaxConns < 1 || db.MinConns > db.MaxConns {
		return Database{}, errors.New("DB_MIN_CONNS and DB_MAX_CONNS are inconsistent")
	}
	return db, nil
}

func (c Config) Validate() error {
	var missing []string
	switch c.App.Environment {
	case "development", "test", "production":
	default:
		return errors.New("APP_ENV must be development, test, or production")
	}
	if c.Database.URL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if c.Storage.Endpoint == "" {
		missing = append(missing, "S3_ENDPOINT")
	}
	if c.Storage.Bucket == "" {
		missing = append(missing, "S3_BUCKET")
	}
	if len(c.Notification.EncryptionKey) < 32 {
		missing = append(missing, "DEVICE_TOKEN_ENCRYPTION_KEY (at least 32 characters)")
	}
	if c.Database.MinConns < 0 || c.Database.MaxConns < 1 || c.Database.MinConns > c.Database.MaxConns {
		return errors.New("DB_MIN_CONNS and DB_MAX_CONNS are inconsistent")
	}
	if c.Database.QueryTimeout <= 0 {
		return errors.New("DB_QUERY_TIMEOUT must be positive")
	}
	if c.HTTP.Port < 1 || c.HTTP.Port > 65535 {
		return errors.New("HTTP_PORT must be between 1 and 65535")
	}
	if c.HTTP.ReadTimeout <= 0 || c.HTTP.WriteTimeout <= 0 || c.HTTP.IdleTimeout <= 0 || c.HTTP.ShutdownTimeout <= 0 {
		return errors.New("HTTP timeouts must be positive")
	}
	if c.Storage.SignedURLTTL <= 0 {
		return errors.New("SIGNED_URL_TTL must be positive")
	}
	if c.Upload.MaxVideoBytes < 1 || c.Upload.MaxPDFBytes < 1 || c.Upload.MaxImageBytes < 1 {
		return errors.New("upload size limits must be positive")
	}
	if c.RateLimit.Requests < 1 || c.RateLimit.Window <= 0 {
		return errors.New("RATE_LIMIT_REQUESTS and RATE_LIMIT_WINDOW must be positive")
	}
	if c.App.Environment == "production" && c.App.FakeAuthEnabled {
		return errors.New("FAKE_AUTH_ENABLED is forbidden in production")
	}
	if c.App.Environment == "production" {
		for key, val := range map[string]string{
			"FIREBASE_PROJECT_ID":   c.Firebase.ProjectID,
			"S3_ACCESS_KEY":         c.Storage.AccessKey,
			"S3_SECRET_KEY":         c.Storage.SecretKey,
			"LIVEKIT_URL":           c.LiveKit.URL,
			"LIVEKIT_API_KEY":       c.LiveKit.APIKey,
			"LIVEKIT_API_SECRET":    c.LiveKit.APISecret,
			"STRIPE_SECRET_KEY":     c.Stripe.SecretKey,
			"STRIPE_WEBHOOK_SECRET": c.Stripe.WebhookSecret,
		} {
			if val == "" {
				missing = append(missing, key)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
	return nil
}

func value(k, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return fallback
}
func intValue(k string, fallback int) (int, error) {
	v := os.Getenv(k)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", k, err)
	}
	return n, nil
}
func int64Value(k string, fallback int64) (int64, error) {
	v := os.Getenv(k)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", k, err)
	}
	return n, nil
}
func boolValue(k string, fallback bool) (bool, error) {
	v := os.Getenv(k)
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", k, err)
	}
	return b, nil
}
func durationValue(k string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(k)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", k, err)
	}
	return d, nil
}
func csvValue(k string) []string {
	raw := strings.TrimSpace(os.Getenv(k))
	if raw == "" {
		return nil
	}
	fields := strings.Split(raw, ",")
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if v := strings.TrimSpace(field); v != "" {
			out = append(out, v)
		}
	}
	return out
}
