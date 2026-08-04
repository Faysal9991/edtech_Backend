# Deployment

## Release order

1. Build immutable API, worker, and migration images from the same revision.
2. Back up PostgreSQL and verify restore readiness.
3. Run `service up` from the migration image as a single controlled job.
4. Deploy worker, then API replicas.
5. Wait for `/health/ready`, inspect queue failures, and run authenticated smoke checks.

API and worker processes never apply schema changes automatically. Down migrations are present for development rollback, but production rollbacks should normally deploy forward-compatible corrective migrations.

## Required production secrets

- `DATABASE_URL`, Redis password where configured
- S3 access/secret keys
- Firebase ADC workload identity or mounted credentials
- LiveKit API key/secret
- Stripe secret and webhook secret
- device-token encryption key

Keep secrets in the deployment platform secret manager, not images, Compose files, logs, Git, or Terraform output. Use a private S3 bucket with public access blocked and lifecycle rules for abandoned upload keys.

## Sizing

Set `DB_MAX_CONNS` so all API and worker replicas stay below the database connection budget. Begin with API CPU-based autoscaling, a modest worker concurrency, and Redis persistence. Monitor HTTP latency, pool acquisition, outbox age, job retries, payment failures, and notification failures.

## Backups

Use managed PostgreSQL point-in-time recovery plus daily logical verification. Test restoration into an isolated environment. Version object-storage buckets or enable provider replication for certificate and course media. Redis is not authoritative; queues can be reconstructed from pending database state/outbox records.

## External verification checklist

- Firebase: revoked and disabled user tokens fail.
- Stripe: CLI/test-mode signed success, failure, duplicate, and refund webhooks.
- LiveKit: real participant token permissions and signed join/leave webhooks.
- S3: signed PUT, HEAD verification, private signed download, and range request.
- FCM: one Android and one iOS device, invalid-token cleanup, and retry behavior.
- OTLP: traces arrive with API/worker service names and no sensitive attributes.

The repository contains no real integration credentials, so these checks must run in the target environment.
