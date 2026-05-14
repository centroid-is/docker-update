.PHONY: build ui types check-types test e2e e2e-cron-fast e2e-debug image image-debug clean all

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
#
# HMI_DOCKER_GID is detected at recipe-execution time (not parse time) so a
# developer who starts Docker Desktop AFTER cloning the repo still gets the
# right GID. Detection runs INSIDE an ephemeral alpine container (not on
# the host) because the host-side GID is not the GID a container actually
# sees on the bind-mounted socket:
#   - macOS Docker Desktop: host shows GID 1/20/etc. (HFS forwarder UID),
#     but inside the LinuxKit VM the socket is owned by root:root (GID 0).
#     A host-side `stat` returns the wrong number.
#   - Linux: the docker.sock inside any container is owned by the host
#     docker group GID, which is what we want.
# Running `stat -c %g /var/run/docker.sock` inside `alpine` produces the
# correct in-container GID on both platforms. If docker isn't usable at
# all the var stays unset and the compose default of 65532 surfaces as a
# deterministic EACCES with the Pitfall 9 remediation hint.
e2e:
	cd e2e && npm ci && npx playwright install --with-deps chromium
	export HMI_DOCKER_GID=$$(docker run --rm -v /var/run/docker.sock:/var/run/docker.sock --entrypoint stat alpine -c '%g' /var/run/docker.sock 2>/dev/null) ; \
	  echo "[make e2e] HMI_DOCKER_GID=$${HMI_DOCKER_GID:-<unset; container will hit EACCES>}" ; \
	  docker compose -f e2e/compose.test.yml up -d --wait ; \
	  cd e2e && npx playwright test ; STATUS=$$? ; \
	  cd .. && docker compose -f e2e/compose.test.yml down -v --remove-orphans ; \
	  exit $$STATUS

# End-to-end with the cron-fast override (Plan 03-05). Sets
# HMI_UPDATE_CRON=@every 5s so the Phase 3 detect-*.spec.ts +
# obs-04-redaction.spec.ts flip assertions land within ~10s wall-clock
# per assertion. Total Playwright wall-clock with this override is
# ~3-4min on a dev machine. Use plain `make e2e` for production-cron
# coverage; this target is the acceleration variant.
#
# HMI_DOCKER_GID detection mirrors `make e2e` — the override does not
# touch the user: line, so the same env-var interpolation in the base
# compose.test.yml applies here.
e2e-cron-fast:
	cd e2e && npm ci && npx playwright install --with-deps chromium
	export HMI_DOCKER_GID=$$(docker run --rm -v /var/run/docker.sock:/var/run/docker.sock --entrypoint stat alpine -c '%g' /var/run/docker.sock 2>/dev/null) ; \
	  echo "[make e2e-cron-fast] HMI_DOCKER_GID=$${HMI_DOCKER_GID:-<unset; container will hit EACCES>}" ; \
	  docker compose -f e2e/compose.test.yml -f e2e/compose.test.override.cron-fast.yml up -d --wait ; \
	  cd e2e && npx playwright test ; STATUS=$$? ; \
	  cd .. && docker compose -f e2e/compose.test.yml down -v --remove-orphans ; \
	  exit $$STATUS

# Build the dev-grade multistage container image. Production size hardening
# belongs to Phase 7. The default build passes no GO_TAGS so the resulting
# binary contains no debug routes (T-02-04-02 invariant).
image:
	docker build -t hmi-update:dev .

# Build the dev-grade image with -tags=debug so internal/api/debug_compose.go
# compiles and GET /debug/compose-stat is registered. Used by
# e2e/tests/compose-drift.spec.ts via `make e2e-debug`. Production CI must
# NEVER build with this target — see Phase 7 / Phase 8.
image-debug:
	docker build --build-arg GO_TAGS=debug -t hmi-update:dev-debug .

# End-to-end with the debug-tagged image so compose-drift.spec.ts runs
# affirmatively (it skips on a production binary because /debug/compose-stat
# returns 404 without -tags=debug). The override flips build.args to
# GO_TAGS=debug at compose build time so the same Dockerfile produces both
# variants — no separate Dockerfile maintained.
e2e-debug:
	cd e2e && npm ci && npx playwright install --with-deps chromium
	export HMI_DOCKER_GID=$$(docker run --rm -v /var/run/docker.sock:/var/run/docker.sock --entrypoint stat alpine -c '%g' /var/run/docker.sock 2>/dev/null) ; \
	  echo "[make e2e-debug] HMI_DOCKER_GID=$${HMI_DOCKER_GID:-<unset; container will hit EACCES>}" ; \
	  docker compose -f e2e/compose.test.yml -f e2e/compose.test.override.debug.yml up -d --wait --build ; \
	  cd e2e && npx playwright test ; STATUS=$$? ; \
	  cd .. && docker compose -f e2e/compose.test.yml down -v --remove-orphans ; \
	  exit $$STATUS

# Wipe build artifacts. Does NOT remove .planning/, .git/, or source.
clean:
	rm -rf bin/ internal/api/dist/assets internal/api/dist/index.html \
	  ui/node_modules/ ui/dist/ \
	  e2e/node_modules/ e2e/playwright-report/ e2e/test-results/
