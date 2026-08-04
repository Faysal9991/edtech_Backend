# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine AS build
ARG TARGET=api
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/service ./cmd/${TARGET}

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates tzdata ffmpeg && addgroup -S lms && adduser -S -G lms -u 10001 lms
WORKDIR /app
COPY --from=build /out/service /app/service
COPY --from=build /src/migrations /app/migrations
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/service"]

