package config

import (
	"errors"
	"fmt"
	"net/url"
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
	Auth          Auth
	Password      Password
	Storage       Storage
	Firebase      Firebase
	LiveKit       LiveKit
	Stripe        Stripe
	Payment       Payment
	URLs          URLs
	Upload        Upload
	RateLimit     RateLimit
	Observability Observability
	Notification  Notification
	Worker        Worker
}

type App struct {
	Environment             string
	Name                    string
	DefaultOrganizationSlug string
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
	URL      string
	Addr     string
	Password string
	DB       int
	TLS      bool
}

type Auth struct {
	Issuer           string
	Audience         string
	KeyID            string
	SigningKey       string
	AccessTTL        time.Duration
	RefreshTTL       time.Duration
	VerificationTTL  time.Duration
	PasswordResetTTL time.Duration
}

type Password struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltBytes   uint32
	KeyBytes    uint32
}

type Storage struct {
	Endpoint       string
	PublicEndpoint string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	ForcePathStyle bool
	SignedURLTTL   time.Duration
}

type Firebase struct {
	ProjectID       string
	CredentialsFile string
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

type Payment struct{ Provider string }

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
	LogLevel     string
}
type Notification struct {
	Provider      string
	EncryptionKey string
}

type Worker struct{ Concurrency int }

func Load() (Config, error) {
	storageEndpoint := os.Getenv("S3_ENDPOINT")
	environment := value("APP_ENV", "development")
	c := Config{
		App:           App{Environment: environment, Name: value("APP_NAME", "lms-service"), DefaultOrganizationSlug: value("DEFAULT_ORGANIZATION_SLUG", "lms")},
		HTTP:          HTTP{Host: value("HTTP_HOST", "0.0.0.0")},
		Database:      Database{URL: os.Getenv("DATABASE_URL")},
		Redis:         Redis{URL: os.Getenv("REDIS_URL"), Addr: value("REDIS_ADDR", "localhost:6379"), Password: os.Getenv("REDIS_PASSWORD")},
		Auth:          Auth{Issuer: value("JWT_ISSUER", "lms-service"), Audience: value("JWT_AUDIENCE", "lms-clients"), KeyID: value("JWT_KEY_ID", "v1"), SigningKey: os.Getenv("JWT_SIGNING_KEY")},
		Storage:       Storage{Endpoint: storageEndpoint, PublicEndpoint: value("S3_PUBLIC_ENDPOINT", storageEndpoint), Region: value("S3_REGION", "us-east-1"), Bucket: os.Getenv("S3_BUCKET"), AccessKey: os.Getenv("S3_ACCESS_KEY"), SecretKey: os.Getenv("S3_SECRET_KEY")},
		Firebase:      Firebase{ProjectID: os.Getenv("FIREBASE_PROJECT_ID"), CredentialsFile: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")},
		LiveKit:       LiveKit{URL: os.Getenv("LIVEKIT_URL"), APIKey: os.Getenv("LIVEKIT_API_KEY"), APISecret: os.Getenv("LIVEKIT_API_SECRET")},
		Stripe:        Stripe{SecretKey: os.Getenv("STRIPE_SECRET_KEY"), WebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET")},
		Payment:       Payment{Provider: value("PAYMENT_PROVIDER", "mock")},
		URLs:          URLs{PublicAPI: value("PUBLIC_API_URL", "http://localhost:8080"), CertificateVerify: value("CERTIFICATE_VERIFY_BASE_URL", "http://localhost:8080/api/v1/public/certificates/verify")},
		Observability: Observability{OTLPEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"), LogLevel: value("LOG_LEVEL", defaultLogLevel(environment))},
		Notification:  Notification{Provider: value("NOTIFICATION_PROVIDER", "log"), EncryptionKey: os.Getenv("DEVICE_TOKEN_ENCRYPTION_KEY")},
	}

	databaseConfig, err := LoadDatabase()
	if err != nil {
		return Config{}, err
	}
	c.Database = databaseConfig
	if c.Auth.AccessTTL, err = durationValue("JWT_ACCESS_TTL", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if c.Auth.RefreshTTL, err = durationValue("REFRESH_TOKEN_TTL", 30*24*time.Hour); err != nil {
		return Config{}, err
	}
	if c.Auth.VerificationTTL, err = durationValue("EMAIL_VERIFICATION_TTL", 24*time.Hour); err != nil {
		return Config{}, err
	}
	if c.Auth.PasswordResetTTL, err = durationValue("PASSWORD_RESET_TTL", 30*time.Minute); err != nil {
		return Config{}, err
	}
	if c.Password.MemoryKiB, err = uint32Value("ARGON2_MEMORY_KIB", 64*1024); err != nil {
		return Config{}, err
	}
	if c.Password.Iterations, err = uint32Value("ARGON2_ITERATIONS", 3); err != nil {
		return Config{}, err
	}
	parallelism, err := intValue("ARGON2_PARALLELISM", 2)
	if err != nil || parallelism < 1 || parallelism > 255 {
		return Config{}, errors.New("ARGON2_PARALLELISM must be between 1 and 255")
	}
	c.Password.Parallelism = uint8(parallelism)
	if c.Password.SaltBytes, err = uint32Value("ARGON2_SALT_BYTES", 16); err != nil {
		return Config{}, err
	}
	if c.Password.KeyBytes, err = uint32Value("ARGON2_KEY_BYTES", 32); err != nil {
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
	if c.Redis.URL != "" {
		parsed, parseErr := url.Parse(c.Redis.URL)
		if parseErr != nil || (parsed.Scheme != "redis" && parsed.Scheme != "rediss") || parsed.Host == "" {
			return Config{}, errors.New("REDIS_URL must be a valid redis:// or rediss:// URL")
		}
		c.Redis.Addr = parsed.Host
		c.Redis.TLS = parsed.Scheme == "rediss"
		if password, ok := parsed.User.Password(); ok {
			c.Redis.Password = password
		}
		if path := strings.TrimPrefix(parsed.Path, "/"); path != "" {
			db, parseErr := strconv.Atoi(path)
			if parseErr != nil || db < 0 {
				return Config{}, errors.New("REDIS_URL database must be a non-negative integer")
			}
			c.Redis.DB = db
		}
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
	if c.Worker.Concurrency, err = intValue("WORKER_CONCURRENCY", 10); err != nil {
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
	maxConns, err := int32Value("DB_MAX_CONNS", 20)
	if err != nil {
		return Database{}, err
	}
	minConns, err := int32Value("DB_MIN_CONNS", 2)
	if err != nil {
		return Database{}, err
	}
	queryTimeout, err := durationValue("DB_QUERY_TIMEOUT", 5*time.Second)
	if err != nil {
		return Database{}, err
	}
	db.MaxConns = maxConns
	db.MinConns = minConns
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
	if len(c.Auth.SigningKey) < 32 {
		missing = append(missing, "JWT_SIGNING_KEY (at least 32 characters)")
	}
	if c.Storage.Endpoint == "" {
		missing = append(missing, "S3_ENDPOINT")
	}
	if !validURL(c.Storage.Endpoint, "http", "https") || !validURL(c.Storage.PublicEndpoint, "http", "https") {
		return errors.New("S3_ENDPOINT and S3_PUBLIC_ENDPOINT must be absolute HTTP(S) URLs")
	}
	if c.Storage.Bucket == "" {
		missing = append(missing, "S3_BUCKET")
	}
	if c.Storage.AccessKey == "" || c.Storage.SecretKey == "" {
		missing = append(missing, "S3_ACCESS_KEY", "S3_SECRET_KEY")
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
	if c.Auth.Issuer == "" || c.Auth.Audience == "" || c.Auth.KeyID == "" || c.Auth.AccessTTL <= 0 || c.Auth.AccessTTL > time.Hour || c.Auth.RefreshTTL <= c.Auth.AccessTTL || c.Auth.VerificationTTL <= 0 || c.Auth.PasswordResetTTL <= 0 {
		return errors.New("JWT issuer, audience, key id, and secure token lifetimes are required")
	}
	if c.Password.MemoryKiB < 19*1024 || c.Password.Iterations < 2 || c.Password.Parallelism < 1 || c.Password.SaltBytes < 16 || c.Password.KeyBytes < 32 {
		return errors.New("Argon2id parameters are below the supported security minimum")
	}
	if c.Payment.Provider != "mock" && c.Payment.Provider != "stripe" {
		return errors.New("PAYMENT_PROVIDER must be mock or stripe")
	}
	if len(c.Stripe.WebhookSecret) < 16 {
		missing = append(missing, "STRIPE_WEBHOOK_SECRET (at least 16 characters, including for signed mock webhooks)")
	}
	if c.Notification.Provider != "log" && c.Notification.Provider != "fcm" {
		return errors.New("NOTIFICATION_PROVIDER must be log or fcm")
	}
	if c.Worker.Concurrency < 1 || c.Worker.Concurrency > 256 {
		return errors.New("WORKER_CONCURRENCY must be between 1 and 256")
	}
	switch c.Observability.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return errors.New("LOG_LEVEL must be debug, info, warn, or error")
	}
	if !validURL(c.LiveKit.URL, "ws", "wss") || c.LiveKit.APIKey == "" || len(c.LiveKit.APISecret) < 16 {
		missing = append(missing, "LIVEKIT_URL", "LIVEKIT_API_KEY", "LIVEKIT_API_SECRET (at least 16 characters)")
	}
	if c.App.Environment == "production" {
		if !validURL(c.Storage.Endpoint, "https") || !validURL(c.Storage.PublicEndpoint, "https") {
			return errors.New("production S3 endpoints must use HTTPS")
		}
		if !validURL(c.LiveKit.URL, "wss") {
			return errors.New("production LIVEKIT_URL must use WSS")
		}
		if !validURL(c.URLs.PublicAPI, "https") || !validURL(c.URLs.CertificateVerify, "https") {
			return errors.New("production public API and certificate verification URLs must use HTTPS")
		}
		if c.Payment.Provider == "mock" {
			return errors.New("PAYMENT_PROVIDER=mock is forbidden in production")
		}
		if c.Payment.Provider == "stripe" && (c.Stripe.SecretKey == "" || c.Stripe.WebhookSecret == "") {
			missing = append(missing, "STRIPE_SECRET_KEY", "STRIPE_WEBHOOK_SECRET")
		}
		if c.Notification.Provider == "fcm" && c.Firebase.ProjectID == "" {
			missing = append(missing, "FIREBASE_PROJECT_ID")
		}
		if strings.Contains(strings.ToLower(c.Auth.SigningKey), "change-me") || strings.Contains(strings.ToLower(c.Auth.SigningKey), "local-only") {
			return errors.New("JWT_SIGNING_KEY uses a forbidden development value")
		}
		for name, secret := range map[string]string{"DEVICE_TOKEN_ENCRYPTION_KEY": c.Notification.EncryptionKey, "S3_SECRET_KEY": c.Storage.SecretKey, "LIVEKIT_API_SECRET": c.LiveKit.APISecret} {
			lower := strings.ToLower(secret)
			if strings.Contains(lower, "change-me") || strings.Contains(lower, "local-only") || strings.Contains(lower, "replace") {
				return fmt.Errorf("%s uses a forbidden development value", name)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
	return nil
}

func validURL(raw string, schemes ...string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	for _, scheme := range schemes {
		if parsed.Scheme == scheme {
			return true
		}
	}
	return false
}

func defaultLogLevel(environment string) string {
	if environment == "production" {
		return "info"
	}
	return "debug"
}

func uint32Value(k string, fallback uint32) (uint32, error) {
	v := os.Getenv(k)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", k, err)
	}
	return uint32(n), nil
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
func int32Value(k string, fallback int32) (int32, error) {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return fallback, nil
	}
	var n int32
	if _, err := fmt.Sscan(v, &n); err != nil || strconv.FormatInt(int64(n), 10) != v {
		if err == nil {
			err = errors.New("must be a base-10 32-bit integer")
		}
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
