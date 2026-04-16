# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

# Copy source and resolve dependencies
COPY . .
RUN go mod tidy && go mod download

# Build a statically linked binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /app/updatemonitor ./cmd/server

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

# Copy binary
COPY --from=builder /app/updatemonitor .

# Copy frontend assets (embedded at runtime via filesystem)
COPY --from=builder /app/web ./web

# Default port
EXPOSE 8080

ENTRYPOINT ["/app/updatemonitor"]
