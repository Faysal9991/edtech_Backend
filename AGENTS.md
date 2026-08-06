# LMS Service engineering guide

## Architecture

- Keep this repository a modular monolith. Commands are composition roots; business rules live in module services; PostgreSQL, Redis, object storage, payments, notifications, LiveKit, jobs, logging, and HTTP are adapters.
- Domain/application code must not import Gin or a vendor SDK. Define interfaces beside the consuming service and inject implementations explicitly.
- PostgreSQL is the system of record. Use sqlc/pgx parameterized queries, bounded reads, stable pagination, transactions for multi-step changes, and the outbox for reliable asynchronous side effects.
- Store UTC `timestamptz` values, UUIDv7 identifiers, token hashes rather than plaintext tokens, and money as integer minor units. Never expose assessment answers or secrets.
- Enforce RBAC, ownership, enrollment, and state-transition rules in services. Handlers validate/translate only and return RFC 7807 problems.

## Commands

- `make bootstrap` — install pinned tools, generate code, and prepare local configuration.
- `make generate` / `make format` / `make vet` / `make lint`
- `make test` / `make test-race` / `make test-integration`
- `make migrate-up` / `make migrate-down` / `make seed`
- `make compose-up` / `make compose-down` / `make smoke`
- `make security` / `make verify`

## Change requirements

- Preserve user changes and keep generated sqlc/OpenAPI files separate from handwritten code.
- Add migrations and indexes with the queries they support; make migrations reversible when safe.
- Add focused unit tests for business invariants and integration tests for transaction, concurrency, repository, and HTTP behavior.
- Never commit credentials, development tokens, `.env`, private keys, presigned URLs, or sensitive request bodies. Logs must redact authorization and authentication material.
- Do not leave core TODOs, placeholder handlers, fake success responses, or unowned goroutines. Propagate contexts and close all resources.

## Completion criteria

A change is complete only after generated files are current; formatting, vet, tests, race tests, lint, vulnerability/security checks, migrations, container builds, health checks, and the smoke journey have been run or an exact environmental blocker is documented. Review authorization, transactions, idempotency, N+1 access, indexes, data races, and secret handling before handoff.
