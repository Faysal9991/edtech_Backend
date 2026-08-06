SHELL := /bin/sh

SQLC_VERSION := v1.31.1
OAPI_CODEGEN_VERSION := v2.8.0
GOLANGCI_LINT_VERSION := v2.12.2
GOSEC_VERSION := v2.28.0
GOVULNCHECK_VERSION := v1.6.0

.PHONY: bootstrap tools generate format fmt lint vet test test-race test-integration security migrate-up migrate-down migrate-create seed run run-api worker run-worker compose-up compose-down docker-up docker-down smoke smoke-test verify

bootstrap: tools
	@test -f .env || cp .env.example .env
	$(MAKE) generate

tools:
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

generate:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION) generate
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) --config api/oapi-codegen.yaml api/openapi.yaml

format:
	gofmt -w $$(rg --files -g '*.go')
fmt: format

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run

vet:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

test-integration:
	go test -tags=integration ./test/integration/...

security:
	go run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) -exclude-generated -quiet ./...
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

migrate-up:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

migrate-create:
	@test -n "$(NAME)" || (echo "NAME is required, e.g. make migrate-create NAME=add_index"; exit 1)
	go run ./cmd/migrate create "$(NAME)"

seed:
	go run ./cmd/seed

run:
	go run ./cmd/api
run-api: run

worker:
	go run ./cmd/worker
run-worker: worker

compose-up:
	docker compose up -d postgres redis minio minio-init
	docker compose --profile tools run --rm migrate up
	docker compose --profile tools run --rm seed
	docker compose up -d api worker
docker-up: compose-up

compose-down:
	docker compose down
docker-down: compose-down

smoke:
	./scripts/smoke-test.sh
smoke-test: smoke

verify: generate format vet test test-race lint security
	git diff --exit-code -- '*.go' 'api/openapi.yaml' 'internal/data' 'internal/api'
	docker build --build-arg TARGET=api -t lms-service-api:verify .
	docker build --build-arg TARGET=worker -t lms-service-worker:verify .
