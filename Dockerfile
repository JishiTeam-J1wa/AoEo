# AoEo AI Gateway
# Multi-stage build for minimal production image.

# ---------------------------------------------------------------------------
# Builder stage
# ---------------------------------------------------------------------------
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Install git for private module fetching (if needed).
RUN apk add --no-cache git

# Download dependencies first (layer caching).
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build.
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o aoeo ./cmd/aoeo

# ---------------------------------------------------------------------------
# Runtime stage
# ---------------------------------------------------------------------------
FROM alpine:3.20

WORKDIR /app

# Install CA certificates for HTTPS outbound calls to AI providers.
RUN apk add --no-cache ca-certificates

COPY --from=builder /build/aoeo /usr/local/bin/aoeo

# Default environment: privacy disabled until explicitly enabled.
ENV AOEO_PRIVACY_ENABLED=false

ENTRYPOINT ["aoeo"]
