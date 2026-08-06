# LMS Service

Phase-1 learning-management backend implemented as a production-oriented Go modular monolith. It exposes a Gin REST API under `/api/v1`, runs durable background work through Asynq/Redis, and keeps PostgreSQL as the system of record.

## Architecture

The commands in `cmd/` are composition roots. Business behavior is grouped into identity/users, courses/content/media, enrollment/progress, quizzes/assignments, live classes, payments, notifications, results/certificates, reports, and audit modules. Module services own authorization and state transitions; HTTP handlers validate/translate; `internal/platform` supplies pgx, Redis, JWT, storage, Stripe/mock payment, FCM/log notification, LiveKit, jobs, logging, metrics, and tracing adapters.

Important invariants are database-enforced: UUIDv7 primary keys, unique active enrollment/progress/submission/certificate/provider-event keys, ordered content positions, check-constrained states, integer minor-unit money, `timestamptz`, row locks for state transitions, and an outbox written in the same transaction as important events. See [implementation plan](docs/IMPLEMENTATION_PLAN.md), [architecture](docs/architecture.md), and [index map](docs/DATABASE_INDEXES.md).

## Prerequisites

- Go 1.26.5+
- Docker Engine with Compose v2
- `make` and `rg` for the development targets
- Optional local PostgreSQL client tools for backup/restore

Pinned generators and verification tools are installed by `make tools`.

## Environment configuration

Copy `.env.example` to `.env` for non-Compose development and replace every placeholder. The application validates configuration at startup. Production rejects the mock payment provider, development JWT keys, missing storage/LiveKit credentials, short signing/encryption keys, and unsafe Argon2/token lifetimes.

The most important required values are `DATABASE_URL`, `REDIS_URL`, `JWT_SIGNING_KEY`, private S3 settings, `DEVICE_TOKEN_ENCRYPTION_KEY`, and provider selections. Do not commit `.env`, Firebase credentials, private keys, or exported data.

## Local startup

The exact local startup sequence is:

```bash
docker compose up -d postgres redis minio minio-init
docker compose --profile tools run --rm migrate up
docker compose --profile tools run --rm seed
docker compose up -d api worker
docker compose ps
curl -fsS http://localhost:8080/health/ready
```

The shorter equivalent is `make compose-up`; that target applies migrations and runs the safe, idempotent development seed before starting the API and worker. API is on `http://localhost:8080`; Swagger UI is `http://localhost:8080/docs`; the OpenAPI document is `/openapi.yaml`; Prometheus metrics are `/metrics`; MinIO console is `http://localhost:9001`.

Compose credentials are local-only and intentionally visible in `docker-compose.yml`. They must never be copied to a deployed environment.

## Administrator seeding

The development/test seed is idempotent and refuses to run in production. It reads credentials from the environment, hashes passwords with Argon2id, verifies the seeded email, assigns global and organization roles, and does not print passwords.

```bash
export APP_ENV=development
export DATABASE_URL='postgres://lms:lms@localhost:5432/lms?sslmode=disable'
export SEED_ADMIN_EMAIL='admin@lms.local'
export SEED_ADMIN_DISPLAY_NAME='Development Administrator'
export SEED_ADMIN_PASSWORD='use-a-long-unique-local-password'
export SEED_DEMO_PASSWORD='use-another-long-demo-password'
make seed
```

For the first administrator in an otherwise empty environment, use `cmd/bootstrap-admin` with `BOOTSTRAP_ADMIN_CONFIRM=CREATE_FIRST_SUPER_ADMIN`, `BOOTSTRAP_EMAIL`, `BOOTSTRAP_PASSWORD`, display name, organization name, and organization slug. It refuses to create a second active super administrator.

## Default development flow

1. `make bootstrap` and edit `.env`.
2. `make compose-up` (migrations and the development seed run automatically).
3. Register through `POST /api/v1/auth/register`; development/test responses include the one-use verification token so local work requires no email credential.
4. Verify, log in, and pass `Authorization: Bearer <access-token>`. Access JWTs are short-lived. Store the refresh token only in a secure client facility and rotate it through `/auth/refresh`.
5. Use `Idempotency-Key` for payment-order creation. Free enrollment is idempotent through the database-enforced `(course_id, student_id)` uniqueness invariant. Use the server-issued upload key only through media intent/completion APIs.
6. Run `make test` while developing and `make verify` before handoff.

## Database migrations

Migrations are canonical single files with explicit Up/Down sections. The migration command executes them with `golang-migrate`, while the format remains directly readable by sqlc and existing tooling.

```bash
make migrate-up
make migrate-down                 # one safe step
make migrate-create NAME=add_feature
go run ./cmd/migrate status
```

Application replicas never run migrations implicitly. Deploy the one-shot migration command before API/worker rollout.

## Generation and tests

```bash
make generate
make format
make vet
make lint
make test
make test-race
make test-integration
make security
make smoke
make verify
```

Integration tests use Testcontainers PostgreSQL when `TEST_DATABASE_URL` is absent. Set it to reuse a development server; each test creates an isolated schema. The smoke test exercises authentication, course/enrollment/progress, assessments, LiveKit-token authorization, mock payment, completion, and certificate verification.

## Payment providers

`PAYMENT_PROVIDER=mock` is for development/tests. It uses the same signed, idempotent webhook path and never activates a paid enrollment from a client response. `PAYMENT_PROVIDER=stripe` uses the official Stripe Go SDK; set `STRIPE_SECRET_KEY` and `STRIPE_WEBHOOK_SECRET`. Amount and currency are checked against the locked server order before the transaction marks payment paid, activates enrollment, and inserts its outbox event. Provider event IDs are unique.

## Private media: S3 and MinIO

Set `S3_ENDPOINT`, region, bucket, access key, secret key, and path-style behavior. The bucket must remain private. Clients request a bounded upload intent, upload through a short-lived presigned URL, and complete using the intent ID. The service validates owner, storage key, expected MIME/extension/size/checksum and actual object metadata. Download/stream URLs are issued only after ownership/enrollment/teaching authorization and are never logged.

Local Compose creates the private `lms-private` MinIO bucket. Production can use any compatible private S3 service.

## LiveKit

Set `LIVEKIT_URL`, `LIVEKIT_API_KEY`, and `LIVEKIT_API_SECRET`. Tokens are short-lived and room-scoped. Course teachers receive publish/subscribe grants; active enrolled students receive subscribe-only grants. LiveKit webhooks record join/leave attendance idempotently. Tests inject the local provider and require no hosted LiveKit account.

## Notifications and Firebase

`NOTIFICATION_PROVIDER=log` is the local adapter; it records in-app notifications and delivery state without external credentials. `NOTIFICATION_PROVIDER=fcm` initializes Firebase Admin using Application Default Credentials and `FIREBASE_PROJECT_ID`. Put `GOOGLE_APPLICATION_CREDENTIALS` outside the repository. Push failure never rolls back the business transaction: outbox jobs retry exponentially and enter `dead_letter` after the bounded attempt budget.

## Deployment on Ubuntu

1. Install Docker/Compose and provision managed or host PostgreSQL, Redis, and private S3 storage.
2. Create a root-readable `.env.production` outside source control with TLS-enabled database/Redis URLs and randomly generated keys.
3. Review [production Compose](deploy/docker-compose.production.yml) and [Nginx example](deploy/nginx.conf); replace hostname/certificate paths.
4. Back up the database, run the one-shot `migrate` service, then roll out API and worker.
5. Require `/health/ready` before traffic, scrape `/metrics` privately, and forward JSON logs/traces.

Both images are multi-stage and run as UID 10001. The worker image alone includes FFmpeg. Graceful shutdown stops HTTP/Asynq and closes PostgreSQL, Redis, jobs, and tracing resources.

## Backup and restore

```bash
DATABASE_URL='postgres://...' ./scripts/backup-postgres.sh /secure/backups
DATABASE_URL='postgres://...' RESTORE_CONFIRM=RESTORE_LMS_DATABASE \
  ./scripts/restore-postgres.sh /secure/backups/lms-YYYYMMDDTHHMMSSZ.dump
```

Encrypt backups, test restores regularly, restrict retention access, and restore to a disposable environment before a production recovery. See [deployment runbook](docs/deployment.md).

## Security notes

- Passwords use configurable Argon2id PHC hashes. Access JWTs validate algorithm, key ID, issuer, audience, type, lifetime, and subject.
- Opaque refresh, verification, and reset tokens have at least 256 bits of entropy; PostgreSQL stores only SHA-256 hashes. Refresh rotation revokes the entire family on reuse.
- Redis backs global and authentication-specific rate limits. Security routes fail closed if the limiter is unavailable; ordinary traffic degrades safely.
- RBAC is database-authoritative on every request. Resource services additionally enforce ownership, enrollment, course organization, immutable submissions/attempts, and provider-verified payments.
- Logs exclude authorization headers, passwords, tokens, provider secrets, Firebase keys, sensitive bodies, and presigned credentials. Audit metadata is allowlisted.

## Troubleshooting

- `health/live` succeeds but `health/ready` fails: check PostgreSQL/Redis DNS, TLS, credentials, pool exhaustion, then `docker compose logs api`.
- Login returns account pending: consume the one-use verification token first. Five invalid passwords temporarily lock the account.
- `default organization is unavailable`: run migrations and `make seed`, or make `DEFAULT_ORGANIZATION_SLUG` match an active organization.
- Upload completion fails: compare intent MIME, byte size, checksum, expiry, owner, and the object metadata in MinIO/S3.
- Paid enrollment remains pending: inspect signed webhook delivery and the unique `payment_webhook_events` record; never force activation from a client PaymentIntent status.
- Worker backlog grows: inspect Redis, `outbox_events` retry/dead-letter state, Asynq queues, provider health, and worker memory/FFmpeg limits.
