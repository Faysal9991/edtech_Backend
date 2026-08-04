package observability

import (
	"github.com/Faysal9991/edtech_Backend/internal/platform/config"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

var PaymentFailures = prometheus.NewCounter(prometheus.CounterOpts{Name: "lms_payment_failures_total", Help: "Payment provider and processing failures."})
var NotificationFailures = prometheus.NewCounter(prometheus.CounterOpts{Name: "lms_notification_failures_total", Help: "FCM and notification delivery failures."})

func RegisterOperationalMetrics(reg prometheus.Registerer, pool *pgxpool.Pool, redisCfg config.Redis) func() error {
	reg.MustRegister(PaymentFailures, NotificationFailures)
	reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "lms_db_pool_acquired_connections", Help: "Currently acquired PostgreSQL connections."}, func() float64 { return float64(pool.Stat().AcquiredConns()) }), prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "lms_db_pool_idle_connections", Help: "Currently idle PostgreSQL connections."}, func() float64 { return float64(pool.Stat().IdleConns()) }), prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "lms_db_pool_max_connections", Help: "Configured PostgreSQL pool maximum."}, func() float64 { return float64(pool.Stat().MaxConns()) }))
	inspector := asynq.NewInspector(asynq.RedisClientOpt{Addr: redisCfg.Addr, Password: redisCfg.Password, DB: redisCfg.DB})
	reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "lms_queue_pending_tasks", Help: "Pending tasks in the default Asynq queue."}, func() float64 {
		info, err := inspector.GetQueueInfo("default")
		if err != nil {
			return -1
		}
		return float64(info.Pending)
	}), prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "lms_queue_active_tasks", Help: "Active tasks in the default Asynq queue."}, func() float64 {
		info, err := inspector.GetQueueInfo("default")
		if err != nil {
			return -1
		}
		return float64(info.Active)
	}), prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "lms_queue_retry_tasks", Help: "Retry tasks in the default Asynq queue."}, func() float64 {
		info, err := inspector.GetQueueInfo("default")
		if err != nil {
			return -1
		}
		return float64(info.Retry)
	}))
	return inspector.Close
}
