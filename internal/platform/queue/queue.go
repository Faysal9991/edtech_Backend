package queue

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/neoscoder/lms-service/internal/platform/config"
)

const (
	TypeMediaProcess        = "media:process"
	TypeCompletionEvaluate  = "completion:evaluate"
	TypeCertificateGenerate = "certificate:generate"
	TypeOutboxDispatch      = "outbox:dispatch"
)

type Client interface {
	Enqueue(string, any, ...asynq.Option) error
}

type AsynqClient struct{ client *asynq.Client }

func NewClient(cfg config.Redis) *AsynqClient {
	return &AsynqClient{client: asynq.NewClient(RedisClientOpt(cfg))}
}

func RedisClientOpt(cfg config.Redis) asynq.RedisClientOpt {
	option := asynq.RedisClientOpt{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB}
	if cfg.TLS {
		option.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return option
}

func (c *AsynqClient) Close() error { return c.client.Close() }

func (c *AsynqClient) Enqueue(kind string, payload any, opts ...asynq.Option) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	defaults := []asynq.Option{asynq.MaxRetry(12), asynq.Timeout(5 * time.Minute)}
	if _, err := c.client.Enqueue(asynq.NewTask(kind, b), append(defaults, opts...)...); err != nil {
		return fmt.Errorf("enqueue %s: %w", kind, err)
	}
	return nil
}

type MemoryClient struct{ Tasks []string }

func (c *MemoryClient) Enqueue(kind string, _ any, _ ...asynq.Option) error {
	c.Tasks = append(c.Tasks, kind)
	return nil
}
