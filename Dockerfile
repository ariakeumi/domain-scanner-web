# syntax=docker/dockerfile:1

# ---------- Build stage ----------
FROM golang:1.22-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

WORKDIR /src

# Cache dependencies first for faster rebuilds
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /out/domain-scanner .

# ---------- Runtime stage ----------
FROM alpine:3.20

# ca-certificates: needed for TLS/DNS checks and HTTPS; tzdata: consistent timestamps
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S scanner && adduser -S -G scanner -u 10001 scanner \
    && mkdir -p /app && chown -R scanner:scanner /app

WORKDIR /app

COPY --from=builder --chown=scanner:scanner /out/domain-scanner /usr/local/bin/domain-scanner

USER scanner

EXPOSE 8080

# Health check: the web API responds 200 with JSON on GET /api/scans
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/api/scans || exit 1

ENTRYPOINT ["domain-scanner"]
CMD ["-web", "-addr", "0.0.0.0:8080"]
