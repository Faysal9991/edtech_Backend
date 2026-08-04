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

	"github.com/Faysal9991/edtech_Backend/internal/data"
	assignmentmodule "github.com/Faysal9991/edtech_Backend/internal/modules/assignment"
	certificatemodule "github.com/Faysal9991/edtech_Backend/internal/modules/certificate"
	coursemodule "github.com/Faysal9991/edtech_Backend/internal/modules/course"
	enrollmentmodule "github.com/Faysal9991/edtech_Backend/internal/modules/enrollment"
	liveclassmodule "github.com/Faysal9991/edtech_Backend/internal/modules/liveclass"
	mediamodule "github.com/Faysal9991/edtech_Backend/internal/modules/media"
	notificationmodule "github.com/Faysal9991/edtech_Backend/internal/modules/notification"
	organizationmodule "github.com/Faysal9991/edtech_Backend/internal/modules/organization"
	paymentmodule "github.com/Faysal9991/edtech_Backend/internal/modules/payment"
	quizmodule "github.com/Faysal9991/edtech_Backend/internal/modules/quiz"
	reportingmodule "github.com/Faysal9991/edtech_Backend/internal/modules/reporting"
	"github.com/Faysal9991/edtech_Backend/internal/platform/auth"
	"github.com/Faysal9991/edtech_Backend/internal/platform/cache"
	"github.com/Faysal9991/edtech_Backend/internal/platform/clock"
	"github.com/Faysal9991/edtech_Backend/internal/platform/config"
	"github.com/Faysal9991/edtech_Backend/internal/platform/database"
	"github.com/Faysal9991/edtech_Backend/internal/platform/httpserver"
	"github.com/Faysal9991/edtech_Backend/internal/platform/httpx"
	platformid "github.com/Faysal9991/edtech_Backend/internal/platform/id"
	platformlivekit "github.com/Faysal9991/edtech_Backend/internal/platform/livekit"
	platformnotification "github.com/Faysal9991/edtech_Backend/internal/platform/notification"
	"github.com/Faysal9991/edtech_Backend/internal/platform/observability"
	platformpayment "github.com/Faysal9991/edtech_Backend/internal/platform/payment"
	"github.com/Faysal9991/edtech_Backend/internal/platform/queue"
	"github.com/Faysal9991/edtech_Backend/internal/platform/storage"
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
	logger := observability.Logger(cfg.App.Environment)
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
	defer redisClient.Close()
	queries := data.New(pool)
	ids := platformid.Secure{}
	realClock := clock.Real{}
	var verifier auth.TokenVerifier
	if cfg.App.FakeAuthEnabled {
		verifier = auth.DevelopmentVerifier{}
		logger.Warn("development fake authentication enabled")
	} else {
		verifier, err = auth.NewFirebaseVerifier(ctx, cfg.Firebase.ProjectID)
		if err != nil {
			return err
		}
	}
	objectStore, err := storage.NewMinIO(cfg.Storage)
	if err != nil {
		return err
	}
	jobs := queue.NewClient(cfg.Redis)
	defer jobs.Close()
	liveProvider := platformlivekit.New(cfg.LiveKit.APIKey, cfg.LiveKit.APISecret)
	var paymentProvider platformpayment.Provider
	if cfg.App.Environment == "production" {
		paymentProvider = platformpayment.NewStripe(cfg.Stripe.SecretKey, cfg.Stripe.WebhookSecret)
	} else {
		paymentProvider = platformpayment.NewFakeProvider(cfg.Stripe.WebhookSecret)
	}
	var sender platformnotification.Sender
	if cfg.App.Environment == "production" {
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
	orgService := organizationmodule.NewService(pool, queries, ids, realClock)
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
	authMiddleware := auth.NewMiddleware(verifier, queries, ids, cache.NewRedisLimiter(redisClient), cfg.RateLimit.Requests, cfg.RateLimit.Window)
	httpxRegister()
	closeMetrics := observability.RegisterOperationalMetrics(prometheus.DefaultRegisterer, pool, cfg.Redis)
	defer closeMetrics()
	handler := httpserver.NewRouter(httpserver.Dependencies{Config: cfg, Logger: logger, Auth: authMiddleware, DB: pool, Redis: redisClient, Handlers: httpserver.Handlers{Organization: organizationmodule.NewHandler(orgService, queries, verifier), Course: coursemodule.NewHandler(courseService, queries, ids), Media: mediamodule.NewHandler(mediaService, queries), Enrollment: enrollmentmodule.NewHandler(enrollmentService, queries), Quiz: quizmodule.NewHandler(quizService, queries), Assignment: assignmentmodule.NewHandler(assignmentService, queries), LiveClass: liveclassmodule.NewHandler(liveService, queries), Certificate: certificatemodule.NewHandler(certificateService, queries), Payment: paymentmodule.NewHandler(paymentService, queries), Notification: notificationmodule.NewHandler(notificationService, queries), Reporting: reportingmodule.NewHandler(reportingService, queries)}})
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
