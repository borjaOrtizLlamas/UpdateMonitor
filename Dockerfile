# ── Build stage ───────────────────────────────────────────────────────────────
# TARGETARCH is injected by Docker BuildKit automatically (amd64, arm64, etc.)
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder

ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /app

RUN apk add --no-cache git

# Copy source and resolve dependencies
COPY . .
RUN go mod tidy && go mod download

# Build a statically linked binary for the target platform
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
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
