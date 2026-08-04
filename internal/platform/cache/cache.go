package cache

import (
	"context"
	"fmt"

	"github.com/Faysal9991/edtech_Backend/internal/platform/config"
	"github.com/redis/go-redis/v9"
)

func Open(ctx context.Context, cfg config.Redis) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return client, nil
}
