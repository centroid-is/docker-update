.PHONY: build ui types check-types test e2e image clean all

BIN := bin/hmi-update

# Default target: build everything an operator would want from a clean tree.
all: ui build

# Compile the Go binary. The embedded UI must be built first via `make ui`
# (or the Dockerfile multistage) — otherwise //go:embed all:dist picks up
# only the .gitkeep placeholder.
build:
	go build -o $(BIN) ./cmd/hmi-update

# Build the Svelte/Vite bundle into internal/api/dist (via ui/vite.config.ts
# outDir). Idempotent; safe to run on every CI invocation.
ui:
	npm --prefix ui ci && npm --prefix ui run build

# Regenerate ui/src/lib/types.d.ts from internal/api/types.go.
types:
	tygo generate

# CI fail-on-diff: regenerate, then refuse to proceed if there is drift.
# Per RESEARCH.md tygo has no --check flag; git diff --exit-code is the canonical pattern.
check-types: types
	@git diff --exit-code ui/src/lib/types.d.ts || \
	  (echo "ERROR: types.d.ts is out of date. Run 'make types' and commit." && exit 1)

# Run Go unit tests with the race detector. Fast (<5s) — no docker needed.
test:
	go test ./... -race

# End-to-end: install Playwright deps, bring up the test compose stack via
# `up -d --wait`, run the smoke suite, tear down (even on failure).
e2e:
	cd e2e && npm ci && npx playwright install --with-deps chromium
	docker compose -f e2e/compose.test.yml up -d --wait
	cd e2e && npx playwright test ; STATUS=$$? ; \
	  docker compose -f e2e/compose.test.yml down -v --remove-orphans ; \
	  exit $$STATUS

# Build the dev-grade multistage container image. Production size hardening
# belongs to Phase 7.
image:
	docker build -t hmi-update:dev .

# Wipe build artifacts. Does NOT remove .planning/, .git/, or source.
clean:
	rm -rf bin/ internal/api/dist/assets internal/api/dist/index.html \
	  ui/node_modules/ ui/dist/ \
	  e2e/node_modules/ e2e/playwright-report/ e2e/test-results/
