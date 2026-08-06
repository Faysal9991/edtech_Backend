package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/neoscoder/lms-service/internal/data"
	assignmentmodule "github.com/neoscoder/lms-service/internal/modules/assignment"
	certificatemodule "github.com/neoscoder/lms-service/internal/modules/certificate"
	coursemodule "github.com/neoscoder/lms-service/internal/modules/course"
	enrollmentmodule "github.com/neoscoder/lms-service/internal/modules/enrollment"
	identitymodule "github.com/neoscoder/lms-service/internal/modules/identity"
	liveclassmodule "github.com/neoscoder/lms-service/internal/modules/liveclass"
	mediamodule "github.com/neoscoder/lms-service/internal/modules/media"
	notificationmodule "github.com/neoscoder/lms-service/internal/modules/notification"
	paymentmodule "github.com/neoscoder/lms-service/internal/modules/payment"
	quizmodule "github.com/neoscoder/lms-service/internal/modules/quiz"
	reportingmodule "github.com/neoscoder/lms-service/internal/modules/reporting"
	usersmodule "github.com/neoscoder/lms-service/internal/modules/users"
	"github.com/neoscoder/lms-service/internal/platform/auth"
	"github.com/neoscoder/lms-service/internal/platform/cache"
	"github.com/neoscoder/lms-service/internal/platform/clock"
	"github.com/neoscoder/lms-service/internal/platform/config"
	"github.com/neoscoder/lms-service/internal/platform/database"
	"github.com/neoscoder/lms-service/internal/platform/httpserver"
	"github.com/neoscoder/lms-service/internal/platform/httpx"
	platformid "github.com/neoscoder/lms-service/internal/platform/id"
	platformlivekit "github.com/neoscoder/lms-service/internal/platform/livekit"
	platformnotification "github.com/neoscoder/lms-service/internal/platform/notification"
	"github.com/neoscoder/lms-service/internal/platform/observability"
	platformpayment "github.com/neoscoder/lms-service/internal/platform/payment"
	"github.com/neoscoder/lms-service/internal/platform/queue"
	"github.com/neoscoder/lms-service/internal/platform/storage"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {
	if err := run(); err != nil {
		slog.Error("API stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := observability.Logger(cfg.App.Environment, cfg.Observability.LogLevel)
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	shutdownTrace, err := observability.InitTracing(ctx, cfg.App.Name, cfg.Observability.OTLPEndpoint)
	if err != nil {
		return fmt.Errorf("initialize tracing: %w", err)
	}
	defer func() { _ = shutdownTrace(context.Background()) }()
	pool, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	redisClient, err := cache.Open(ctx, cfg.Redis)
	if err != nil {
		return err
	}
	defer func() { _ = redisClient.Close() }()
	queries := data.New(pool)
	ids := platformid.Secure{}
	realClock := clock.Real{}
	jwtManager := auth.NewJWTManager(cfg.Auth)
	passwordHasher := auth.NewPasswordHasher(cfg.Password)
	objectStore, err := storage.NewMinIO(cfg.Storage)
	if err != nil {
		return err
	}
	jobs := queue.NewClient(cfg.Redis)
	defer func() { _ = jobs.Close() }()
	liveProvider := platformlivekit.New(cfg.LiveKit.APIKey, cfg.LiveKit.APISecret)
	var paymentProvider platformpayment.Provider
	if cfg.Payment.Provider == "stripe" {
		paymentProvider = platformpayment.NewStripe(cfg.Stripe.SecretKey, cfg.Stripe.WebhookSecret)
	} else {
		paymentProvider = platformpayment.NewFakeProvider(cfg.Stripe.WebhookSecret)
	}
	var sender platformnotification.Sender
	if cfg.Notification.Provider == "fcm" {
		sender, err = platformnotification.NewFCM(ctx, cfg.Firebase.ProjectID)
		if err != nil {
			return err
		}
	} else {
		sender = &platformnotification.FakeSender{}
	}
	cryptor, err := notificationmodule.NewCryptor(cfg.Notification.EncryptionKey)
	if err != nil {
		return err
	}
	identityService, err := identitymodule.NewService(pool, queries, ids, passwordHasher, jwtManager, cfg.Auth, cfg.App.DefaultOrganizationSlug)
	if err != nil {
		return fmt.Errorf("initialize identity service: %w", err)
	}
	usersService := usersmodule.NewService(pool, queries, ids, cfg.App.DefaultOrganizationSlug)
	courseService := coursemodule.NewService(pool, queries, ids)
	mediaService := mediamodule.NewService(pool, queries, ids, realClock, objectStore, jobs, cfg)
	enrollmentService := enrollmentmodule.NewService(pool, queries, ids, realClock, jobs)
	quizService := quizmodule.NewService(pool, queries, ids, realClock, jobs)
	assignmentService := assignmentmodule.NewService(pool, queries, ids, realClock, jobs)
	liveService := liveclassmodule.NewService(pool, queries, ids, realClock, liveProvider, cfg.LiveKit)
	certificateService := certificatemodule.NewService(pool, queries, ids, realClock, jobs, objectStore, cfg)
	paymentService := paymentmodule.NewService(pool, queries, ids, realClock, paymentProvider)
	notificationService := notificationmodule.NewService(queries, ids, realClock, sender, cryptor)
	reportingService := reportingmodule.NewService(queries)
	limiter := cache.NewRedisLimiter(redisClient)
	httpxRegister()
	closeMetrics := observability.RegisterOperationalMetrics(prometheus.DefaultRegisterer, pool, cfg.Redis)
	defer func() { _ = closeMetrics() }()
	handler := httpserver.NewRouter(httpserver.Dependencies{Config: cfg, Logger: logger, JWT: jwtManager, Queries: queries, Limiter: limiter, DB: pool, Redis: redisClient, Handlers: httpserver.Handlers{Identity: identitymodule.NewHandler(identityService, cfg.App.Environment), Users: usersmodule.NewHandler(usersService), Course: coursemodule.NewHandler(courseService, queries, ids), Media: mediamodule.NewHandler(mediaService, queries), Enrollment: enrollmentmodule.NewHandler(enrollmentService, queries), Quiz: quizmodule.NewHandler(quizService, queries), Assignment: assignmentmodule.NewHandler(assignmentService, queries), LiveClass: liveclassmodule.NewHandler(liveService, queries), Certificate: certificatemodule.NewHandler(certificateService, queries), Payment: paymentmodule.NewHandler(paymentService, queries), Notification: notificationmodule.NewHandler(notificationService, queries), Reporting: reportingmodule.NewHandler(reportingService, queries)}})
	server := &http.Server{Addr: fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port), Handler: handler, ReadTimeout: cfg.HTTP.ReadTimeout, ReadHeaderTimeout: cfg.HTTP.ReadTimeout, WriteTimeout: cfg.HTTP.WriteTimeout, IdleTimeout: cfg.HTTP.IdleTimeout}
	errorsCh := make(chan error, 1)
	go func() { logger.Info("API listening", "address", server.Addr); errorsCh <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errorsCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
func httpxRegister() {
	httpx.RegisterMetrics(prometheus.DefaultRegisterer)
	prometheus.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "lms_build_info", Help: "LMS process build information."}, func() float64 { return 1 }))
}
