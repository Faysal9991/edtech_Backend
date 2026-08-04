package queue

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/Faysal9991/edtech_Backend/internal/platform/config"
	"github.com/hibiken/asynq"
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
	return &AsynqClient{client: asynq.NewClient(asynq.RedisClientOpt{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB})}
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
