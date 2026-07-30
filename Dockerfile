# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder

RUN apk add --no-cache ca-certificates git

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
    -ldflags="-s -w" \
    -o /out/overseer \
    ./cmd/server

FROM alpine:3.21

RUN apk add --no-cache ca-certificates git tzdata curl su-exec \
    && adduser -D -g '' -u 1000 overseer

COPY --from=builder /out/overseer /usr/local/bin/overseer
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

ENV PORT=8080 \
    DB_PATH=/data/overseer.db \
    WORKSPACE_DIR=/data/workspaces \
    REVIEW_LOG_DIR=/data/reviewlogs

WORKDIR /app
VOLUME ["/data"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -fsS "http://127.0.0.1:${PORT}/api/health" >/dev/null || exit 1

ENTRYPOINT ["/docker-entrypoint.sh"]
