package cache

import (
	"context"
	"crypto/tls"
	"fmt"

	"github.com/neoscoder/lms-service/internal/platform/config"
	"github.com/redis/go-redis/v9"
)

func Open(ctx context.Context, cfg config.Redis) (*redis.Client, error) {
	options := &redis.Options{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB}
	if cfg.TLS {
		options.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return client, nil
}
