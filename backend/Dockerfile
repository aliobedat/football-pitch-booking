# syntax=docker/dockerfile:1

# ── Builder ──────────────────────────────────────────────────────────────────
# Pinned exactly to the go.mod directive (go 1.26.3). Bump both together.
FROM golang:1.26.3 AS builder

WORKDIR /src

# Dependency layer: cached until go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

# Source layer.
COPY . .

# Static binary: pure-Go deps (pgx), tzdata embedded via time/tzdata in
# internal/timeutil, so no libc, zoneinfo, or other runtime files are needed.
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/server ./cmd/api

# ── Runtime ──────────────────────────────────────────────────────────────────
# distroless/static: no shell, ships CA certificates (Neon TLS), runs as the
# non-root `nonroot` user by default.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /app/server /app/server

# Documentation only — Railway routes on $PORT, which the app reads itself.
EXPOSE 8080

ENTRYPOINT ["/app/server"]
