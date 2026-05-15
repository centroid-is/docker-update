# syntax=docker/dockerfile:1.7

# ---- Stage 1: build the Svelte bundle ----
FROM node:22-alpine AS ui-builder
WORKDIR /src/ui

# Copy package manifest first for layer caching
COPY ui/package.json ui/package-lock.json* ./
RUN npm ci

# Copy the rest of the UI source and build
COPY ui/ ./
# Vite is configured to emit into ../internal/api/dist (i.e. /src/internal/api/dist)
# We need that directory to exist so the path resolution works inside the container.
RUN mkdir -p /src/internal/api/dist && npm run build

# ---- Stage 2: build the Go binary with embedded UI ----
FROM golang:1.26-alpine AS go-builder
WORKDIR /src

# Cache Go modules
COPY go.mod go.sum* ./
RUN go mod download

# Copy the rest of the source, including the freshly-built UI bundle
COPY . .
COPY --from=ui-builder /src/internal/api/dist /src/internal/api/dist

# GO_TAGS — build-time selection of //go:build tags. Default empty
# produces the production binary (no debug routes). CI / `make image-debug`
# / `e2e/compose.test.override.debug.yml` pass `--build-arg GO_TAGS=debug`
# to compile internal/api/debug_compose.go and register
# GET /debug/compose-stat. T-02-04-02 invariant: the default build MUST
# NOT contain the debug route — `strings /out/hmi-update | grep
# compose-stat` returns 0 matches on production builds.
ARG GO_TAGS=""

# Build a static binary with embedded assets.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
    -tags="${GO_TAGS}" \
    -ldflags="-s -w" \
    -o /out/hmi-update ./cmd/hmi-update

# ---- Stage 3: source docker CLI + docker compose v2 plugin ----
#
# Phase 4 plan 04-06 Task 4 finding: the hmi-update binary shells out to
# `docker compose -f <path> up -d --force-recreate <service>` from inside
# its own container (the docker.sock is bind-mounted; the daemon runs on
# the host). compose.NewRunner does exec.LookPath("docker") at boot and
# log.Fatalfs if the binary is missing — which means Phase 4 actions are
# functionally unreachable unless docker CLI + compose plugin are on PATH
# inside this image.
#
# We stage in the docker CLI and the docker compose plugin from the
# official docker:cli image (Alpine-based, contains both). Distroless
# stays the runtime — docker CLI is just a static binary + the compose
# plugin is a single Go binary; no glibc, no shell, no shared libs needed.
#
# Image-size budget: docker:cli is ~80MB but we only copy the two binaries
# (/usr/local/bin/docker + the compose plugin), keeping the runtime image
# bounded. Phase 7 (DEPLOY-02) will measure the resulting size against the
# <30MB constraint and pivot to a leaner staging if needed (e.g. only the
# subset of the compose plugin's transitive deps actually invoked).
FROM docker:28-cli AS docker-cli-stage

# ---- Stage 4: runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-builder /out/hmi-update /hmi-update

# Docker CLI binary — pinned to /usr/local/bin so exec.LookPath finds it
# via the standard PATH = /usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
# that distroless inherits. Verified at boot via compose.NewRunner's
# exec.LookPath("docker") — fails fast if missing.
COPY --from=docker-cli-stage /usr/local/bin/docker /usr/local/bin/docker

# Docker compose v2 plugin — loaded by docker CLI from one of the standard
# plugin search paths. /usr/local/libexec/docker/cli-plugins is the
# system-wide path docker honours when there is no home directory (the
# nonroot user has none in distroless).
COPY --from=docker-cli-stage /usr/local/libexec/docker/cli-plugins/docker-compose /usr/local/libexec/docker/cli-plugins/docker-compose

EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/hmi-update"]
