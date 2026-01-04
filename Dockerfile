# Stage 1: Build
FROM golang:1.24-alpine AS builder

# Install build dependencies for CGO (required by SQLite)
RUN apk add --no-cache build-base

WORKDIR /src

# Leverage Docker cache for dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build optimized static binary
# -s -w: Omit symbol table and debug information
# -tags musl: Ensure compatibility with alpine's musl libc
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /app/llm-gateway ./cmd

# Stage 2: Final Runtime
FROM alpine:3.21

# Security & Localization
# tzdata: Required for correct log timestamps (e.g. Asia/Shanghai)
# ca-certificates: Required for HTTPS calls to upstream APIs
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S gateway && adduser -S gateway -G gateway

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/llm-gateway .

# Ensure the app has permissions to create/write db and logs
RUN chown -R gateway:gateway /app

USER gateway

# Persistence targets
# gateway.db  - SQLite database (Mount this!)
# gateway.log - Application logs (Ephemeral, auto-rotated by app)

ENV GIN_MODE=release
EXPOSE 8000

# Healthcheck to ensure the service is actually responsive
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8000/health || exit 1

ENTRYPOINT ["./llm-gateway"]