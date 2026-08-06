# Deployment

## Release order

1. Build immutable API, worker, and migration images from one revision.
2. Back up PostgreSQL and verify restore readiness.
3. Run the migration image as one controlled job.
4. Deploy worker, then API replicas.
5. Require `/health/ready`, inspect queues/outbox, and run the authenticated smoke journey.

API and worker processes never apply schema changes. Down migrations support local validation; production rollbacks should normally use a forward-compatible corrective migration.

## Production gate

Production startup is intentionally disabled while payments use the dummy
adapter. Before a release, implement and verify an approved processor behind
the existing payment-provider interface, then restore production configuration
for its credentials and signed webhooks.

## Required production configuration after that gate

- TLS PostgreSQL and Redis connection details
- randomly generated JWT signing and device-token encryption keys
- private S3 credentials plus internal and client-visible endpoints when they differ
- LiveKit URL and API credentials
- approved payment-processor credentials and webhook verification secret
- Firebase ADC/workload identity only when `NOTIFICATION_PROVIDER=fcm`

Production validation currently rejects the dummy payment adapter. It also rejects missing required credentials, development key markers, weak token lifetimes, and unsafe Argon2 parameters. Store secrets outside Git, images, Compose files, logs, and infrastructure outputs.

## Operations

Keep the aggregate API/worker pool size below PostgreSQL’s connection budget. Start with bounded worker concurrency and Redis persistence. Monitor HTTP latency/status, database pool acquisition, outbox age/dead letters, queue retries, payment failures, and notification failures.

Use `scripts/backup-postgres.sh` for encrypted-at-rest logical backup workflows and `scripts/restore-postgres.sh` with its explicit confirmation guard. Test restores in isolation. Redis is not authoritative; pending database/outbox state can reconstruct work.

## External verification checklist

- replacement processor sandbox success, failure, replay, and refund webhooks
- LiveKit publisher/subscriber grants and signed attendance webhooks
- S3 signed PUT, HEAD verification, private download, and range requests
- FCM Android/iOS delivery, invalid-token cleanup, retry, and dead letter
- OTLP export with no sensitive attributes

Local completion does not require payment credentials: the signed dummy adapter, log notification, local MinIO, and locally signed LiveKit tokens cover the automated path.
