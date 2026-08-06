# Deployment

## Release order

1. Build immutable API, worker, and migration images from one revision.
2. Back up PostgreSQL and verify restore readiness.
3. Run the migration image as one controlled job.
4. Deploy worker, then API replicas.
5. Require `/health/ready`, inspect queues/outbox, and run the authenticated smoke journey.

API and worker processes never apply schema changes. Down migrations support local validation; production rollbacks should normally use a forward-compatible corrective migration.

## Required production configuration

- TLS PostgreSQL and Redis connection details
- randomly generated JWT signing and device-token encryption keys
- private S3 credentials plus internal and client-visible endpoints when they differ
- LiveKit URL and API credentials
- Stripe secret and webhook secret
- Firebase ADC/workload identity only when `NOTIFICATION_PROVIDER=fcm`

Production validation rejects mock payment, missing required credentials, development key markers, weak token lifetimes, and unsafe Argon2 parameters. Store secrets outside Git, images, Compose files, logs, and infrastructure outputs.

## Operations

Keep the aggregate API/worker pool size below PostgreSQL’s connection budget. Start with bounded worker concurrency and Redis persistence. Monitor HTTP latency/status, database pool acquisition, outbox age/dead letters, queue retries, payment failures, and notification failures.

Use `scripts/backup-postgres.sh` for encrypted-at-rest logical backup workflows and `scripts/restore-postgres.sh` with its explicit confirmation guard. Test restores in isolation. Redis is not authoritative; pending database/outbox state can reconstruct work.

## External verification checklist

- Stripe test-mode success, failure, replay, and refund webhooks
- LiveKit publisher/subscriber grants and signed attendance webhooks
- S3 signed PUT, HEAD verification, private download, and range requests
- FCM Android/iOS delivery, invalid-token cleanup, retry, and dead letter
- OTLP export with no sensitive attributes

Local completion does not require these credentials: mock payment, log notification, local MinIO, and locally signed LiveKit tokens cover the automated path.
