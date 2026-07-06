# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/across-api ./cmd/api

FROM alpine:3.20

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 across

COPY --from=builder /out/across-api /app/across-api

USER across
EXPOSE 8080

CMD ["/app/across-api"]
