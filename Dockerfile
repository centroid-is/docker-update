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

# ---- Stage 3: runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-builder /out/hmi-update /hmi-update
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/hmi-update"]
