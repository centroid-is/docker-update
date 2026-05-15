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

# VERSION/SHA/BUILT_AT — stamped into the binary via -ldflags=-X.
# Defaults are "dev" / "unknown" / "unknown" so a no-arg build still
# produces a runnable binary identifying itself as a dev build.
# Set by `make image-prod` from git describe / git rev-parse / date.
# See Makefile image-prod target + Phase 7 CONTEXT.md §2.2 build flags.
ARG VERSION="dev"
ARG SHA="unknown"
ARG BUILT_AT="unknown"

# Build a static binary with embedded assets.
# Flags per Phase 7 CONTEXT.md §2.2:
#   CGO_ENABLED=0 — static binary; required for distroless static-debian12.
#   -trimpath — removes /src/... from stack traces; aids reproducibility.
#   -ldflags="-s -w" — strips debug symbols + DWARF; ~30 % size reduction.
#   -ldflags="-X main.<ver>=..." — stamps version vars (cmd/hmi-update/main.go).
#   -tags="${GO_TAGS}" — empty by default; production builds MUST exclude the
#     `debug` tag so internal/api/debug_compose.go does NOT compile in
#     (T-02-04-02 invariant: strings <bin> | grep -c compose-stat == 0).
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
    -tags="${GO_TAGS}" \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${SHA} -X main.builtAt=${BUILT_AT}" \
    -o /out/hmi-update ./cmd/hmi-update

# ---- Stage 3: distroless runtime ----
#
# Base pinned to debian12 (NOT the unversioned static:nonroot — see
# .planning/research/STACK.md §"Container image" and Phase 7 CONTEXT.md §2.1).
# When migrating to static-debian13:nonroot, capture the new digest in the
# comment below and bump the FROM line in the same commit.
#
# Resolved digest at Phase 7-01 execute time (2026-05-15):
#   sha256:a9329520abc449e3b14d5bc3a6ffae065bdde0f02667fa10880c49b35c109fd1
#
# Phase 4's earlier 4-stage shape baked the host docker CLI + compose plugin
# into the image to satisfy compose.Runner's exec.LookPath("docker") at boot.
# Phase 7 CONTEXT.md §2.3 LOCKED that decision to "bind-mount host docker
# binary" instead (Option A — zero size impact). The production compose
# example (docker-compose.example.yml, Plan 07-02) provides the two
# read-only bind-mounts (/usr/bin/docker + /usr/libexec/docker/cli-plugins)
# that satisfy the LookPath at run time. The image itself no longer ships
# the CLI — this is what unlocks the <30 MB image-size budget (DEPLOY-02).
FROM gcr.io/distroless/static-debian12:nonroot

# OCI image labels per https://github.com/opencontainers/image-spec/blob/main/annotations.md
LABEL org.opencontainers.image.title="hmi-update"
LABEL org.opencontainers.image.description="Per-container Update/Rollback for Centroid HMI compose stacks"
LABEL org.opencontainers.image.source="https://github.com/centroid-is/docker-update"
LABEL org.opencontainers.image.licenses="MIT"
LABEL org.opencontainers.image.vendor="Centroid"
# ARG references at the LABEL layer require the ARG to be re-declared in
# this stage (Docker scoping rule). Re-declare here so VERSION/SHA stamp
# both the binary AND the image metadata.
ARG VERSION="dev"
ARG SHA="unknown"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.revision="${SHA}"

COPY --from=go-builder /out/hmi-update /hmi-update
EXPOSE 8080

# Clean shutdown: SIGTERM is what `docker stop` and compose recreate send
# by default; main.go's signal.NotifyContext should already handle it.
# Explicit STOPSIGNAL makes the contract un-ambiguous and aligns with the
# "no surprises during recreate" Pitfall 6 surface area.
STOPSIGNAL SIGTERM

USER 65532:65532
ENTRYPOINT ["/hmi-update"]
