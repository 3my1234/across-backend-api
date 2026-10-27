FROM golang:1.27.3-alpine3.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG LAB_BUILD_HISTORY_TESTS=false
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -o /out/api ./cmd/api && CGO_ENABLED=0 go build -o /out/lab ./cmd/capacity-lab && if [ "$LAB_BUILD_HISTORY_TESTS" = true ]; then CGO_ENABLED=0 go test -c -o /out/history-tests ./internal/controllers; fi
FROM alpine:3.23
RUN apk add --no-cache ca-certificates curl
WORKDIR /app
COPY --from=build /out/ /app/
COPY migrations /app/migrations
RUN mkdir -p /app/internal/controllers
CMD ["/app/lab", "run"]
