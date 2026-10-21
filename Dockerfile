# syntax=docker/dockerfile:1

FROM golang:1.27.3-alpine3.23 AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG SOURCE_COMMIT=unknown
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X across/backend/internal/buildinfo.Revision=${SOURCE_COMMIT}" -o /out/across-api ./cmd/api \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X across/backend/internal/buildinfo.Revision=${SOURCE_COMMIT}" -o /out/across-worker ./cmd/worker \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X across/backend/internal/buildinfo.Revision=${SOURCE_COMMIT}" -o /out/across-migrate ./cmd/migrate \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X across/backend/internal/buildinfo.Revision=${SOURCE_COMMIT}" -o /out/across-config-check ./cmd/config-check \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X across/backend/internal/buildinfo.Revision=${SOURCE_COMMIT}" -o /out/across-webpush-keys ./cmd/webpush-keys \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X across/backend/internal/buildinfo.Revision=${SOURCE_COMMIT}" -o /out/across-settlements ./cmd/settlements

FROM alpine:3.23

WORKDIR /app

RUN apk add --no-cache ca-certificates curl tzdata ffmpeg && adduser -D -H -u 10001 across

COPY --from=builder /out/across-api /app/across-api
COPY --from=builder /out/across-worker /app/across-worker
COPY --from=builder /out/across-migrate /app/across-migrate
COPY --from=builder /out/across-config-check /app/across-config-check
COPY --from=builder /out/across-webpush-keys /app/across-webpush-keys
COPY --from=builder /out/across-settlements /app/across-settlements
COPY --from=builder /src/migrations /app/migrations

USER across
ENV APP_ENV=production RUN_INLINE_WORKERS=false RUN_MIGRATIONS=false
ADD --chmod=0644 https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem /etc/ssl/certs/aws-rds-global.pem
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=15s --retries=3 CMD curl -fsS http://127.0.0.1:8080/api/v1/health || exit 1

CMD ["/app/across-api"]
