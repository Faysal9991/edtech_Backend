package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type Limiter interface {
	Allow(context.Context, string, int, time.Duration) (bool, error)
}

type RedisLimiter struct{ client *redis.Client }

func NewRedisLimiter(client *redis.Client) *RedisLimiter { return &RedisLimiter{client: client} }

func (l *RedisLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	count, err := redis.NewScript(`local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('PEXPIRE',KEYS[1],ARGV[1]); end; return n`).Run(ctx, l.client, []string{"rate:" + key}, window.Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	return count <= int64(limit), nil
}

type AllowAllLimiter struct{}

func (AllowAllLimiter) Allow(context.Context, string, int, time.Duration) (bool, error) {
	return true, nil
}
