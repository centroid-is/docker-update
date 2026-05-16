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
# NOT contain the debug route — `strings /out/docker-update | grep
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
#   -ldflags="-X main.<ver>=..." — stamps version vars (cmd/docker-update/main.go).
#   -tags="${GO_TAGS}" — empty by default; production builds MUST exclude the
#     `debug` tag so internal/api/debug_compose.go does NOT compile in
#     (T-02-04-02 invariant: strings <bin> | grep -c compose-stat == 0).
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
    -tags="${GO_TAGS}" \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${SHA} -X main.builtAt=${BUILT_AT}" \
    -o /out/docker-update ./cmd/docker-update

# ---- Stage 3: distroless runtime ----
#
# Base pinned to static-debian12:nonroot (~1.9 MB) — REVERTED from
# base-debian12:nonroot (~22 MB) in Phase 9 (a), 2026-05-16.
#
# Phase 9 (a) socket-only recreate:
#   The 2026-05-15 base-debian12 hotfix was needed because the
#   bind-mounted host docker CLI was dynamically linked (needed
#   /lib64/ld-linux-x86-64.so.2 + libc.so.6 which static-debian12
#   does not ship). Phase 9's internal/recreate primitive replaces
#   the compose.Runner subprocess with a socket-only path; the
#   bind-mounted docker CLI is no longer needed, and the only
#   host-side dependency is /var/run/docker.sock (always was). So
#   static-debian12:nonroot is sufficient again — ~20 MB image shrink
#   that puts the production image comfortably under the SC-3 (b)
#   12 MB budget (CI gate at .github/workflows/ci.yml enforces the
#   ceiling).
#
# What static-debian12:nonroot ships (verified per the distroless
# README):
#   - /etc/ssl/certs/ca-certificates.crt — required for the HTTPS path
#     to ghcr.io and any other public registry. Pitfall 5 (RESEARCH.md):
#     a missing CA bundle would surface as `x509: certificate signed
#     by unknown authority` on the first crane.Digest call. The
#     post-build smoke step in the Phase 9 plan's Task 2 verify gate
#     asserts this file exists in the image.
#   - tzdata
#   - nonroot user (UID 65532)
#
# Migration note (debian13): when distroless promotes
# static-debian13:nonroot to the recommended floor, bump this line
# + record the new digest below in the same commit.
FROM gcr.io/distroless/static-debian12:nonroot

# OCI image labels per https://github.com/opencontainers/image-spec/blob/main/annotations.md
LABEL org.opencontainers.image.title="docker-update"
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

COPY --from=go-builder /out/docker-update /docker-update
EXPOSE 8080

# Clean shutdown: SIGTERM is what `docker stop` and compose recreate send
# by default; main.go's signal.NotifyContext should already handle it.
# Explicit STOPSIGNAL makes the contract un-ambiguous and aligns with the
# "no surprises during recreate" Pitfall 6 surface area.
STOPSIGNAL SIGTERM

USER 65532:65532
ENTRYPOINT ["/docker-update"]
