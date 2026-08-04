# Phase 1 LMS backend

Production-oriented Go modular monolith for a multi-organization learning management system. Firebase supplies identity; PostgreSQL remains authoritative for users, memberships, roles, courses, learning state, grades, orders, and reports.

## Stack

- Go 1.26, `net/http`, Chi, structured `slog`
- PostgreSQL 16, pgx/v5, sqlc, Goose
- Redis and Asynq for rate limits, idempotent background jobs, and the transactional outbox
- private S3-compatible storage (MinIO locally), signed upload/download URLs, FFmpeg media processing
- Firebase Admin for ID-token verification and FCM
- Stripe PaymentIntents and signed webhooks
- LiveKit participant tokens and signed webhooks
- OpenAPI 3.1 with oapi-codegen models
- Prometheus metrics and OpenTelemetry traces

## Quick start with Docker

Docker Compose does not run migrations implicitly. Run them exactly once before starting application replicas.

```bash
make docker-up
docker compose --profile tools run --rm seed
make smoke-test
```

Services are exposed at API `:8080`, PostgreSQL `:5432`, Redis `:6379`, MinIO `:9000`, and MinIO Console `:9001`.

The checked-in Compose values are local-only. Copy `.env.example` for non-Compose development and replace every placeholder. Never commit `.env` or Firebase service-account files.

## Run without Docker

Start PostgreSQL, Redis, and an S3-compatible service, export configuration, then:

```bash
make migrate-up
make seed
make run-worker
make run-api
```

Development identity tokens use `dev:<email>` only when both `APP_ENV=development|test` and `FAKE_AUTH_ENABLED=true`:

```bash
curl -X POST http://localhost:8080/api/v1/auth/bootstrap \
  -H 'Authorization: Bearer dev:student1@acme.test'

curl http://localhost:8080/api/v1/courses \
  -H 'Authorization: Bearer dev:student1@acme.test'
```

Organization-scoped staff operations also require `X-Organization-ID`. The value is never trusted by itself; authorization middleware resolves an active membership and current PostgreSQL roles for every request.

## Flutter and Firebase

1. Sign in using the FlutterFire Authentication SDK.
2. Retrieve a fresh Firebase ID token.
3. Send `Authorization: Bearer <firebase-id-token>` to `/api/v1/auth/bootstrap`.
4. Keep the returned local UUID and memberships as display state only; the server re-evaluates roles from PostgreSQL.
5. Refresh the Firebase token normally. Do not send a Firebase password to this backend.

Production uses Application Default Credentials. `GOOGLE_APPLICATION_CREDENTIALS` may point to a local credentials file, which must remain outside the repository.

## Development users

The idempotent seed creates:

- `super@lms.local`
- `admin@acme.test`
- `instructor1@acme.test`, `instructor2@acme.test`
- `student1@acme.test` through `student4@acme.test`

It also creates one organization, free and paid courses, content, assessments, a live session, enrollments, progress, and notifications.

## Commands

```text
make tools                 install pinned generators/migration tool
make generate              regenerate sqlc and OpenAPI models
make fmt                   format Go code
make lint                  run go vet and staticcheck
make test                  unit and handler tests
make test-integration      Testcontainers PostgreSQL tests
make test-race             race detector
make migrate-up            apply all pending migrations
make migrate-down-one      roll back exactly one migration
make seed                   idempotent development fixtures
make run-api               API process
make run-worker            Asynq/outbox worker
make docker-up             dependencies, explicit migration, API, worker
make docker-down           stop Compose without deleting volumes
make smoke-test            seeded student flow through certificate verification
```

The smoke flow also creates a paid-course order, replays a signed fake Stripe success webhook, verifies idempotent activation, and exercises certificate PDF upload/download against private object storage.

## API conventions

- JSON requests reject unknown fields and bodies larger than 1 MiB.
- Errors use `application/problem+json` and include the propagated request ID.
- Lists use opaque timestamp-plus-UUID cursors, never unbounded offsets.
- Amounts are integer minor units, for example `10000 BDT`.
- `Idempotency-Key` is mandatory for order creation and retryable purchase flows.
- Raw Stripe and LiveKit webhook bodies/signatures are verified before state changes.
- Large files upload directly to private object storage with short-lived signed URLs.

The contract is [api/openapi.yaml](api/openapi.yaml). Architecture and integration details are under [docs](docs/architecture.md).

## Production first administrator

Apply migrations, create the Firebase user in the trusted Firebase project, and run the one-shot bootstrap command described in [authentication](docs/authentication.md). The command accepts a Firebase UID and email, refuses to run if a super administrator already exists, and never handles a password.

## Health and telemetry

- `/health/live` checks only process liveness.
- `/health/ready` checks PostgreSQL and Redis with a short timeout.
- `/metrics` exposes Prometheus-compatible request and process metrics.
- Set `OTEL_EXPORTER_OTLP_ENDPOINT` to enable batched OTLP/HTTP tracing.

See [deployment](docs/deployment.md) and the [runbook](docs/runbook.md) before operating the service.
