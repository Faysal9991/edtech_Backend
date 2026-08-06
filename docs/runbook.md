# Operations runbook

## API not ready

Check `/health/live` first. If live but not ready, inspect PostgreSQL and Redis connectivity, TLS, DNS, credentials, and pool limits. Readiness intentionally does not call FCM, the dummy payment adapter, LiveKit, or S3 to avoid turning an optional downstream outage into an API restart loop.

## Migration failure

Stop the rollout, keep the previous API version serving if compatible, and inspect `goose_db_version`. Never delete the version table or run broad destructive SQL. Correct the migration forward and test against a restored production snapshot.

## Payment incident

Do not manually activate enrollment from a client response. During development, locate `payment_webhook_events` and the order, then replay the signed dummy event after fixing the cause; unique event IDs make replay safe. Compare stored amount/currency snapshots before changing state.

## Queue backlog

Inspect Redis/Asynq queue latency and `outbox_events`. Scale workers only after checking whether the downstream provider is throttling. Poison events retain bounded errors and exponential delay. Preserve deduplication keys.

## Media stuck processing

Check asset state/failure reason, worker logs, S3 access, free disk, and FFprobe/FFmpeg availability. Confirm HEAD size/MIME before retrying. Never make a failed asset ready manually without validating the object.

## Certificate failure

Confirm enrollment requirements, certificate uniqueness, QR verification URL, font/PDF rendering, and S3 write access. Re-enqueue the same certificate ID; generation is idempotent and must not create a second certificate.

## Firebase/FCM outage

Authentication remains available because it is first-party and database-backed. Notification outbox rows remain durable; allow backoff and recover when FCM returns.

## Credential exposure

Immediately rotate the affected credential, revoke sessions/tokens where applicable, search structured logs and artifacts, and document scope. FCM tokens, dummy webhook secrets, authorization headers, signed URLs, and service credentials must never appear in logs.

## Useful checks

```bash
curl -fsS http://localhost:8080/health/live
curl -fsS http://localhost:8080/health/ready
curl -fsS http://localhost:8080/metrics | head
go run ./cmd/migrate status
go test ./...
go test -race ./...
```
