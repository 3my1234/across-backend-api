FROM golang:1.27.3-alpine3.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/across-api ./cmd/api && \
    CGO_ENABLED=0 go build -o /out/across-worker ./cmd/worker && \
    CGO_ENABLED=0 go build -o /out/lab ./cmd/capacity-lab
FROM alpine:3.23
RUN apk add --no-cache ca-certificates curl
WORKDIR /app
COPY --from=build /out/ /app/
COPY migrations /app/migrations
USER 10001
CMD ["/app/across-api"]
