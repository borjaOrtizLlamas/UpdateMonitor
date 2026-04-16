# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Cache dependencies first (layer caching)
COPY go.mod ./
RUN go mod tidy && go mod download

# Copy source and build a statically linked binary
COPY . .
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
