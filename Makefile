.PHONY: tools generate fmt lint test test-integration test-race migrate-up migrate-down-one seed run-api run-worker docker-up docker-down smoke-test

tools:
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0
	go install github.com/pressly/goose/v3/cmd/goose@v3.27.3

generate:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate
	go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 --config api/oapi-codegen.yaml api/openapi.yaml

fmt:
	gofmt -w $$(rg --files -g '*.go')

lint:
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...

test:
	go test ./...

test-integration:
	go test -tags=integration ./test/integration/...

test-race:
	go test -race ./...

migrate-up:
	go run ./cmd/migrate up

migrate-down-one:
	go run ./cmd/migrate down-one

seed:
	go run ./cmd/seed

run-api:
	go run ./cmd/api

run-worker:
	go run ./cmd/worker

docker-up:
	docker compose up -d postgres redis minio minio-init
	docker compose --profile tools run --rm migrate up
	docker compose up -d api worker

docker-down:
	docker compose down

smoke-test:
	./scripts/smoke-test.sh
