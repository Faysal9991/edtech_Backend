package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Faysal9991/edtech_Backend/internal/data"
	certificatemodule "github.com/Faysal9991/edtech_Backend/internal/modules/certificate"
	mediamodule "github.com/Faysal9991/edtech_Backend/internal/modules/media"
	notificationmodule "github.com/Faysal9991/edtech_Backend/internal/modules/notification"
	"github.com/Faysal9991/edtech_Backend/internal/platform/cache"
	"github.com/Faysal9991/edtech_Backend/internal/platform/clock"
	"github.com/Faysal9991/edtech_Backend/internal/platform/config"
	"github.com/Faysal9991/edtech_Backend/internal/platform/database"
	platformid "github.com/Faysal9991/edtech_Backend/internal/platform/id"
	platformnotification "github.com/Faysal9991/edtech_Backend/internal/platform/notification"
	"github.com/Faysal9991/edtech_Backend/internal/platform/observability"
	"github.com/Faysal9991/edtech_Backend/internal/platform/queue"
	"github.com/Faysal9991/edtech_Backend/internal/platform/storage"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker stopped", "error", err)
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
	shutdownTrace, err := observability.InitTracing(ctx, cfg.App.Name+"-worker", cfg.Observability.OTLPEndpoint)
	if err != nil {
		return err
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
	store, err := storage.NewMinIO(cfg.Storage)
	if err != nil {
		return err
	}
	jobs := queue.NewClient(cfg.Redis)
	defer jobs.Close()
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
	mediaService := mediamodule.NewService(pool, queries, ids, realClock, store, jobs, cfg)
	certificateService := certificatemodule.NewService(pool, queries, ids, realClock, jobs, store, cfg)
	notificationService := notificationmodule.NewService(queries, ids, realClock, sender, cryptor)
	mux := asynq.NewServeMux()
	decodeID := func(task *asynq.Task, key string) (uuid.UUID, error) {
		var payload map[string]string
		if err := json.Unmarshal(task.Payload(), &payload); err != nil {
			return uuid.Nil, err
		}
		id, err := uuid.Parse(payload[key])
		if err != nil {
			return uuid.Nil, fmt.Errorf("invalid %s: %w", key, err)
		}
		return id, nil
	}
	mux.HandleFunc(queue.TypeMediaProcess, func(ctx context.Context, task *asynq.Task) error {
		id, err := decodeID(task, "media_asset_id")
		if err != nil {
			return err
		}
		return mediaService.Process(ctx, id)
	})
	mux.HandleFunc(queue.TypeCompletionEvaluate, func(ctx context.Context, task *asynq.Task) error {
		id, err := decodeID(task, "enrollment_id")
		if err != nil {
			return err
		}
		_, err = certificateService.Evaluate(ctx, id)
		return err
	})
	mux.HandleFunc(queue.TypeCertificateGenerate, func(ctx context.Context, task *asynq.Task) error {
		id, err := decodeID(task, "certificate_id")
		if err != nil {
			return err
		}
		return certificateService.Generate(ctx, id)
	})
	mux.HandleFunc(queue.TypeOutboxDispatch, func(ctx context.Context, _ *asynq.Task) error { return notificationService.DispatchBatch(ctx, 50) })
	server := asynq.NewServer(asynq.RedisClientOpt{Addr: cfg.Redis.Addr, Password: cfg.Redis.Password, DB: cfg.Redis.DB}, asynq.Config{Concurrency: 10, Queues: map[string]int{"critical": 6, "default": 3, "low": 1}, ShutdownTimeout: cfg.HTTP.ShutdownTimeout, ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
		logger.ErrorContext(ctx, "job failed", "type", task.Type(), "error", err)
	})})
	outboxDone := make(chan struct{})
	go func() {
		defer close(outboxDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := queries.ExpireDueEnrollments(ctx, 500); err != nil {
					logger.WarnContext(ctx, "enrollment expiration batch incomplete", "error", err)
				}
				if err := notificationService.QueueDueReminders(ctx); err != nil {
					logger.WarnContext(ctx, "reminder scheduling incomplete", "error", err)
				}
				if err := notificationService.DispatchBatch(ctx, 50); err != nil {
					logger.WarnContext(ctx, "outbox dispatch batch incomplete", "error", err)
				}
			}
		}
	}()
	errorsCh := make(chan error, 1)
	go func() { logger.Info("worker started"); errorsCh <- server.Run(mux) }()
	select {
	case <-ctx.Done():
		server.Shutdown()
		<-outboxDone
		return nil
	case err := <-errorsCh:
		if errors.Is(err, asynq.ErrServerClosed) {
			return nil
		}
		return err
	}
}
