# LMS Service Phase-1 implementation plan

## Requirements

Deliver a production-oriented Go modular monolith with a Gin REST API under `/api/v1`, an Asynq worker, PostgreSQL/pgx/sqlc persistence, Redis, private S3-compatible media storage, JWT access and rotating refresh authentication, RBAC, learning workflows, payments, notifications, LiveKit integration, reports, audit records, OpenAPI, containers, operational scripts, and meaningful automated verification. During development, payments use a local signed dummy adapter and never require processor credentials.

## Architecture decisions

1. PostgreSQL is authoritative; Redis is an expendable rate-limit/job accelerator. Durable events are inserted into an outbox in the same transaction as business state.
2. UUIDv7 is generated at application boundaries. All timestamps are UTC `timestamptz`; currency amounts are signed-safe `bigint` minor units.
3. Access JWTs are short lived and validated for algorithm, key id, issuer, audience, subject, expiry, and token type. Opaque refresh/reset/verification tokens have at least 256 bits of entropy; only SHA-256 hashes are persisted. Refresh rotation locks the session family and revokes it on reuse.
4. Argon2id parameters are configuration-backed and encoded into PHC strings so they can be upgraded on login/password change.
5. Gin owns routing and middleware. Module HTTP adapters remain thin; application services own authorization, resource ownership, enrollment, state transitions, transaction boundaries, and provider interfaces.
6. Payments use a signed development-only dummy adapter and production startup remains disabled until an approved processor is implemented. Notifications use log or FCM adapters; storage uses MinIO or S3-compatible APIs; LiveKit tokens are locally signed.
7. The pre-existing LMS implementation is preserved where compatible. Its course, content, enrollment, progress, assessment, media, live-class, payment, notification, result/certificate, report, audit, worker, and generated-query foundations are evolved rather than discarded. Incompatible Firebase authentication, Chi routing, and the old module path are replaced.

## Module boundaries

- `internal/modules/identity`: registration, login, token lifecycle, verification, password recovery/change, session revocation, authentication audit.
- `internal/modules/users`: profiles, teacher/student attributes, administrative search/status/role management.
- `internal/modules/course` and `media`: categories, owned course lifecycle, modules/lessons, publication validation, private uploads and access URLs.
- `internal/modules/enrollment`: free/paid enrollment, access checks, progress, completion and completion events.
- `internal/modules/quiz` and `assignment`: authored assessments, student attempts/submissions, immutable submission rules, scoring/grading and analytics.
- `internal/modules/liveclass`: scheduling/state transitions, scoped LiveKit grants and attendance.
- `internal/modules/payment`: provider-neutral orders, verified/idempotent webhooks, transactional enrollment activation, refunds and outbox events.
- `internal/modules/notification`: in-app records, devices, background delivery, retry-safe outbox handling.
- `internal/modules/certificate` and `reporting`: results, unique PDF certificates, safe public verification, bounded aggregate reports.
- `internal/platform`: validated configuration and concrete database/cache/auth/storage/payment/notification/live-class/jobs/logging/observability/HTTP adapters.

## Database design

The normalized schema contains users, global roles/permissions and mappings, profiles, refresh session families, single-use verification/reset tokens, categories, courses, ordered modules/lessons, private media/upload intents, enrollments and lesson progress, quizzes/questions/options/attempts/answers, assignments/submissions/files, live classes/attendance, results/certificates, payment orders/events, devices/notifications/deliveries, outbox events, and audit logs. Foreign keys and state checks protect referential and transition invariants; unique and partial constraints make idempotency and concurrency guarantees database-enforced. Composite indexes follow actual list, ownership, unread, schedule, outbox, payment, and report predicates. `docs/DATABASE_INDEXES.md` maps important indexes to queries; `docs/query-plans.sql` provides representative plans.

## Implementation phases

1. Inventory and baseline the existing repository; establish this plan and repository rules.
2. Correct module/framework/dependency composition and add validated security/provider configuration.
3. Add reversible identity/profile/RBAC migrations and sqlc queries; implement Argon2id, JWT, rotation/reuse detection, rate limiting, and audit.
4. Wire Gin routes for the required public, student, teacher, and admin APIs while retaining proven module services.
5. Close functional/schema gaps in users, courses/content, progress, assessments, live classes, results/certificates, payments, notifications, reports, and audit.
6. Complete OpenAPI, Docker/Compose, migrations/seed, CI, backup/restore, Nginx/deployment, smoke journey, and operator/developer documentation.
7. Run generation, migrations both directions, tests/race/lint/vet/security, images/Compose/health/smoke; review and fix authorization, transactional, performance, concurrency, and secret-handling findings.

## Risks and mitigations

- Concurrent enrollment/payment/completion could duplicate state: enforce unique constraints, row locks, idempotency keys/events, and transactional outbox writes; exercise race-sensitive integration tests.
- Token theft/replay: short access lifetime, hashed refresh material, rotation, family-wide reuse revocation, explicit logout, rate limits, and security audit records.
- Tenant-era data leaking through the single-service API: centralize principal resolution and preserve organization scoping internally; public responses use allowlisted fields only.
- External outages: keep state changes independent of push delivery, retry idempotent jobs with backoff, and use local adapters so credentials do not block development.
- Large lists/reports: deterministic bounded pagination, aggregate SQL, supporting composite indexes, and query-plan fixtures prevent unbounded/N+1 behavior.
- Existing generated code drift: pin generators and require `make generate` plus a clean-diff CI check.

## Definition of done

- Commands, API, and worker compile from a clean checkout; the requested module path and Gin composition are in use.
- Clean migrations apply, one-step rollback validation succeeds where safe, and the seed creates an administrator with no committed password.
- Required endpoints perform real authorized business behavior and return validated JSON/RFC 7807 errors; OpenAPI/Swagger UI describes the route surface.
- Unit, HTTP, repository/Testcontainers, Redis, webhook-idempotency, authorization, invalid-input, and concurrency-sensitive tests pass, including `-race`.
- Docker images build; Compose dependencies, API, and worker become healthy; the smoke journey succeeds with local adapters.
- Formatting, sqlc/OpenAPI generation, vet, golangci-lint, gosec, govulncheck, migration checks, and full security/performance review pass or any genuine external/environmental blocker is recorded precisely.
