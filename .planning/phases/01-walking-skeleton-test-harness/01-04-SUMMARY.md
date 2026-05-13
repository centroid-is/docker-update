---
phase: 01-walking-skeleton-test-harness
plan: 04
subsystem: infra
tags: [go, net/http, embed, vite, svelte, playwright, docker-compose, zot, oras, distroless, github-actions]

# Dependency graph
requires:
  - phase: 01-walking-skeleton-test-harness
    provides: state.Store (plan 02), Svelte/Vite UI shell + internal/api/types.go (plan 03), RED smoke test (plan 01)
provides:
  - "internal/api: HTTP server with /healthz, /api/state, embed-backed static handler (strict /assets/* no-fallback; MIME-registered)"
  - "cmd/hmi-update: real main() wiring slog + state.Store + api.Server -> ListenAndServe :8080"
  - "Multi-stage Dockerfile (node:22-alpine -> golang:1.26-alpine -> gcr.io/distroless/static-debian12:nonroot)"
  - "e2e test stack (zot + stub container + hmi-update) via docker compose --wait"
  - "Playwright globalSetup with oras manifest push fixture (reused by Phase 3 mid-test pushes)"
  - "Makefile targets: build, ui, types, check-types, test, e2e, image, clean"
  - ".github/workflows/ci.yml: go vet/test + tygo diff + ui build + e2e (no image push - Phase 8 scope)"
  - "PHASE 1 GATE FLIP: the plan-01 RED smoke test is now GREEN"
affects: [phase-02-docker-events, phase-03-registry-polling, phase-04-actions, phase-05-ui, phase-07-deploy, phase-08-ci-release]

# Tech tracking
tech-stack:
  added:
    - "stdlib net/http ServeMux with Go 1.22+ pattern matching (GET /healthz, GET /api/state)"
    - "//go:embed all:dist + mime.AddExtensionType + http.FileServerFS"
    - "log/slog JSON handler (HMI_UPDATE_LOG_LEVEL env)"
    - "@playwright/test 1.60 globalSetup/globalTeardown pattern"
    - "oras CLI 1.3.x (--plain-http --disable-path-validation)"
    - "ghcr.io/project-zot/zot-linux-amd64:latest as the fake test registry"
    - "gcr.io/distroless/static-debian12:nonroot (UID 65532) runtime image"
  patterns:
    - "Strict /assets/* no-fallback (Pitfall 8) - 404 on miss, never index.html"
    - "Explicit mime.AddExtensionType at init() for distroless environments"
    - "Cache-Control split: /assets/* immutable, /index.html no-cache"
    - "http.Server ReadTimeout=WriteTimeout=10s slow-loris mitigation (T-01-04-02)"
    - "Host-side readiness polling for distroless services that cannot run their own healthcheck"
    - "tygo include_files: types.go to keep server.go internals out of the UI-facing types contract"

key-files:
  created:
    - "internal/api/server.go"
    - "internal/api/handlers.go"
    - "internal/api/static.go"
    - "internal/api/server_test.go"
    - "Dockerfile"
    - "e2e/compose.test.yml"
    - "e2e/zot-config.json"
    - "e2e/playwright.config.ts"
    - "e2e/global-setup.ts"
    - "e2e/global-teardown.ts"
    - "e2e/fixtures/push-image.ts"
    - "e2e/tsconfig.json"
    - "e2e/package-lock.json"
    - ".github/workflows/ci.yml"
  modified:
    - "cmd/hmi-update/main.go - replaced empty stub with real wiring"
    - "Makefile - added build/ui/test/e2e/image/clean (preserved types/check-types)"
    - "tygo.yaml - added include_files: types.go scope so server.go does not leak into TS types"
    - ".gitignore - anchored /hmi-update for the stray go build fallback name"

key-decisions:
  - "Drop compose-side healthcheck on zot and hmi-update; both are distroless and lack wget/curl/sh. Host-side polling in global-setup.ts replaces the gate."
  - "Map host port 15000 -> container 5000 for zot to avoid macOS Control Center (AirPlay Receiver) conflict on dev machines. Overridable via ZOT_HOST_PORT env."
  - "Use tmpfs (not a named volume) for /state in the e2e compose stack so the nonroot UID 65532 can write to it without a chown shell step."
  - "Use --disable-path-validation with oras push: oras >= 1.3 rejects absolute file paths by default; test fixtures intentionally use /tmp payloads."
  - "tygo scoped to types.go via include_files so server.go's exported Server struct does not appear in ui/src/lib/types.d.ts."
  - "Normalize /index.html -> / in static handler to avoid http.FileServerFS's 301 canonicalization redirect (which would otherwise drop the Cache-Control header)."

patterns-established:
  - "Distroless readiness: when a service image cannot run a healthcheck probe (no shell, no wget, no curl), drop the compose-side healthcheck and poll from the host in globalSetup before any test uses the service."
  - "tmpfs for nonroot writable state in distroless: named volumes inherit root ownership on first create, and there's no shell to chown them. tmpfs accepts uid/gid/mode mount options."
  - "Strict static handler: explicit prefix matching with no SPA fallback for /assets/*. Pitfall 8 regression test asserts 404 on miss."

requirements-completed: [FOUND-03, FOUND-05, FOUND-06, FOUND-07]

# Metrics
duration: ~25min
completed: 2026-05-13
---

# Phase 1 Plan 04: Walking Skeleton & Test Harness — HTTP Server, Test Stack, CI

**HTTP server (`/healthz`, `/api/state`, embed-backed `/` + `/assets/*`) + Playwright-driven docker-compose test stack (zot + stub + hmi-update) + minimal CI; the plan-01 RED smoke is now GREEN.**

## Performance

- **Duration:** ~25 min (Tasks 1-3; Task 4 is a blocking human-verify checkpoint and was intentionally NOT executed by this agent)
- **Started:** 2026-05-13T13:02:00Z (approx, plan-01 baseline)
- **Completed (Tasks 1-3):** 2026-05-13T13:27:23Z
- **Tasks:** 3 of 4 (Task 4 is human-verify, pending operator)
- **Files created:** 14
- **Files modified:** 4

## Accomplishments

- **The Phase 1 gate has flipped.** `cd e2e && npx playwright test --grep smoke` reports `1 passed (7.9s)`. The plan-01 RED smoke test is now GREEN — Phase 1's C4 red-first contract is honored.
- HTTP server wired with stdlib net/http: GET /healthz (200 ok / 503 with generic remediation hint), GET /api/state (snapshot from state.Store), embed-backed static handler with strict /assets/* no-fallback and explicit MIME registration (Pitfall 8).
- 10s ReadTimeout/WriteTimeout applied (T-01-04-02 slow-loris mitigation).
- Dockerfile builds cleanly via `docker build .` and produces a working distroless image running as UID 65532.
- Test compose stack reaches a workable state via `docker compose up -d --wait` plus host-side readiness polling for the two distroless services.
- Minimal CI workflow shipped with go vet/test/check-types + ui build + e2e jobs.

## Task Commits

1. **Task 1: HTTP server + handlers + static.go + unit tests + main.go + Makefile completion** — `e2e3fe0` (feat)
2. **Task 2: Test stack (Dockerfile + compose.test.yml + zot-config.json + Playwright infra + oras helper) — drive smoke GREEN** — `d58a21b` (feat)
3. **Task 3: CI workflow (.github/workflows/ci.yml)** — `49a7c47` (chore)

_Task 4 is a `type="checkpoint:human-verify"` gate (C4 manual smoke on an HMI-like stack). NOT EXECUTED by this agent — pending operator approval._

## Files Created/Modified

### Created
- `internal/api/server.go` — Server struct, routes(), Handler(), ListenAndServe with 10s timeouts
- `internal/api/handlers.go` — healthz (200/503 + Content-Type) and getState (json.Encode of state.Store.Get)
- `internal/api/static.go` — //go:embed all:dist, mime.AddExtensionType for .js/.css/.svg/.json, strict /assets/* with immutable cache; /index.html with no-cache
- `internal/api/server_test.go` — 7 unit tests (healthz ok/nil-store, getState empty/populated, strict 404, index cache-control, immutable asset MIME)
- `Dockerfile` — node:22-alpine -> golang:1.26-alpine -> gcr.io/distroless/static-debian12:nonroot
- `e2e/compose.test.yml` — zot + stub-watched-container + hmi-update (host port 15000->5000, tmpfs /state)
- `e2e/zot-config.json` — anonymous-open zot config, dedupe=false, gc=false
- `e2e/playwright.config.ts` — globalSetup + globalTeardown + baseURL + workers:1
- `e2e/global-setup.ts` — compose up --wait + zot /v2/ poll + initial oras push + hmi-update /healthz poll
- `e2e/global-teardown.ts` — compose down -v --remove-orphans
- `e2e/fixtures/push-image.ts` — pushFreshManifest helper (oras --plain-http --disable-path-validation)
- `e2e/tsconfig.json` — strict ES2022/Bundler TS config
- `e2e/package-lock.json` — pinned lockfile for `npm ci`
- `.github/workflows/ci.yml` — three jobs (go / ui / e2e) on push to main and PRs

### Modified
- `cmd/hmi-update/main.go` — empty stub replaced with slog setup, state.NewStore from HMI_UPDATE_STATE_PATH, api.NewServer, ListenAndServe :8080
- `Makefile` — added build, ui, test, e2e, image, clean (kept types, check-types)
- `tygo.yaml` — added `include_files: [types.go]` so server.go's exported types don't leak into the UI types file
- `.gitignore` — anchored `/hmi-update` for the stray go build fallback artifact

## Decisions Made

1. **state.State directly marshalled in getState** (no api.State copy). The two types are json-tag identical by construction (tygo's source-of-truth contract) — copying would be redundant. Documented in handlers.go comment.
2. **Server type unexported from TS** via `include_files: [types.go]` in tygo.yaml — the cleanest fix to "tygo accidentally generates a Server interface" without making the Go type unexported (which would break the server's package surface).
3. **No graceful SIGTERM shutdown in main.go** — Phase 4 owns STATE-04 SIGKILL fault injection; Phase 1's main() is intentionally minimal per the plan's explicit "DO NOT" guidance.
4. **No --healthcheck flag added to the binary** — Open Question 3 resolved in favor of host-side polling (Option a). Phase 7 can add it if operations wants Docker-native health.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] tygo generated a stale TS interface from server.go**
- **Found during:** Task 1 verification (`make check-types`)
- **Issue:** tygo by default scans the entire package; my new server.go added an exported `Server` struct, which tygo emitted as an empty TS interface `Server {}`. That caused `make check-types` to fail because the on-disk types.d.ts no longer matched.
- **Fix:** Added `include_files: [types.go]` to `tygo.yaml` so generation is scoped to the source-of-truth file only. RESEARCH.md anticipated this concern indirectly (the file warning header says "tygo reads to regenerate").
- **Files modified:** `tygo.yaml`
- **Verification:** `make check-types` passes; the on-disk types.d.ts is byte-identical to what tygo regenerates.
- **Committed in:** `e2e3fe0` (Task 1 commit)

**2. [Rule 1 - Bug] http.FileServerFS 301-redirected /index.html -> /, dropping our Cache-Control header**
- **Found during:** Task 1 unit tests (`TestIndexHTMLCacheControl` failed with status 301)
- **Issue:** The RESEARCH.md verbatim handler set `r.URL.Path = "/index.html"` on `/`, then called the file server. FileServerFS sees /index.html and 301-redirects to / for canonicalization, stripping our Cache-Control header from the final response.
- **Fix:** Set `r.URL.Path = "/"` instead. FileServerFS auto-serves index.html for / without a redirect, and our Cache-Control: no-cache header rides on the 200 response.
- **Files modified:** `internal/api/static.go`
- **Verification:** Unit test `TestIndexHTMLCacheControl` passes; Playwright smoke asserts the same header end-to-end.
- **Committed in:** `e2e3fe0` (Task 1 commit)

**3. [Rule 3 - Blocking] Host port 5000 conflict with macOS Control Center**
- **Found during:** Task 2 (`docker compose up`)
- **Issue:** `docker compose up -d --wait` failed with `bind: address already in use` on host port 5000 — macOS Control Center (AirPlay Receiver) binds 5000 by default on macOS Sonoma+.
- **Fix:** Mapped host 15000 -> container 5000. Overridable via `ZOT_HOST_PORT` env. The compose-internal `zot:5000` address is unchanged (only host-side oras pushes use the external port).
- **Files modified:** `e2e/compose.test.yml`, `e2e/fixtures/push-image.ts`, `e2e/global-setup.ts`
- **Verification:** Stack now comes up; `oras push localhost:15000/...` succeeds.
- **Committed in:** `d58a21b` (Task 2 commit)

**4. [Rule 3 - Blocking] zot image is distroless-style; the wget-based healthcheck cannot exec**
- **Found during:** Task 2 (`docker compose up -d --wait` returned dependency-failed-to-start)
- **Issue:** RESEARCH.md's `wget --spider http://localhost:5000/v2/` compose healthcheck failed inside `e2e-zot-1` because the zot image has no wget, no curl, and no shell — only `/usr/local/bin/zot-linux-amd64`. The container stayed Unhealthy forever, blocking `up --wait`.
- **Fix:** Dropped the compose-side healthcheck on zot (and the matching `depends_on: zot: service_healthy` -> `service_started` for hmi-update). Added a host-side `waitForHealth('http://localhost:15000/v2/', 30_000)` call in `global-setup.ts` before the first oras push. Same pattern that RESEARCH.md already prescribed for hmi-update — RESEARCH.md assumption A2 was correct about hmi-update but didn't catch the same issue in zot.
- **Files modified:** `e2e/compose.test.yml`, `e2e/global-setup.ts`
- **Verification:** Stack comes up; smoke test passes; the new poll order (compose up -> zot /v2/ -> oras push -> /healthz) is documented in global-setup.ts.
- **Committed in:** `d58a21b` (Task 2 commit)

**5. [Rule 3 - Blocking] Distroless nonroot cannot write to a fresh named volume**
- **Found during:** Task 2 (hmi-update container exited 1 on first boot)
- **Issue:** hmi-update logged `state.NewStore: create state at /state/hmi_update_state.json: open /state/.hmi_update_state.json...: permission denied`. The named volume `hmi-state` was mounted into `/state` owned by root:root, but the container runs as UID 65532 (nonroot) per distroless convention. There's no shell to chown.
- **Fix:** Switched from a named volume to a `tmpfs` mount with `uid=65532,gid=65532,mode=0755`. Phase 1 tests don't need state durability across teardown anyway. Phase 7 may bind-mount a host path with pre-set ownership for production durability.
- **Files modified:** `e2e/compose.test.yml`
- **Verification:** hmi-update starts cleanly; `/api/state` returns `{"version":1,"containers":{}}`.
- **Committed in:** `d58a21b` (Task 2 commit)

**6. [Rule 3 - Blocking] oras >= 1.3 rejects absolute file paths by default**
- **Found during:** Task 2 (first `npx playwright test` run, globalSetup oras push)
- **Issue:** `Error: absolute file path detected. If it's intentional, use --disable-path-validation flag to skip this check: /tmp/payload-....txt`. oras 1.3 added a defensive path-validation guard that didn't exist when RESEARCH.md was written.
- **Fix:** Added `--disable-path-validation` to the oras push command in `push-image.ts`. Test fixtures intentionally use `/tmp` payloads for ephemeral pushes, so explicit opt-in is the right shape.
- **Files modified:** `e2e/fixtures/push-image.ts`
- **Verification:** `pushFreshManifest` succeeds and returns a sha256 digest; smoke test passes.
- **Committed in:** `d58a21b` (Task 2 commit)

---

**Total deviations:** 6 auto-fixed (1 Rule 1 bug, 5 Rule 3 blocking)
**Impact on plan:** All deviations were infrastructure friction (RESEARCH.md was written at a snapshot; the world drifted in small ways). No architectural change, no scope creep. The compose-healthcheck-on-distroless pattern is now documented and reusable for Phase 7.

## Authentication Gates

None — Phase 1 has no auth surface. zot is configured anonymous-open; the docker daemon is reached via the standard bind-mounted socket.

## Issues Encountered

- Compose healthcheck pattern needed to be reconsidered for distroless service images. Resolved by moving readiness gates host-side; see deviations 4 and 5.
- `npm ci` initially failed in e2e/ because no package-lock.json existed (plan 01 shipped only package.json). Resolved by running `npm install` once to generate the lockfile, then committed it for reproducible `npm ci` in CI.

## TDD Gate Compliance

Plan type is `execute` (not `tdd`), so plan-level gate sequence does not apply. The plan-01 RED smoke test (`e2e/tests/smoke.spec.ts`, committed in `628224b`) is what this plan drives GREEN — that satisfies C4's red-first contract at the phase boundary.

## Next Phase Readiness

**Phase 1 gate status (Tasks 1-3):**
- [x] go vet ./... exits 0
- [x] go test ./... -race exits 0 (all state + api unit tests green)
- [x] make check-types exits 0 (tygo drift detection works)
- [x] docker compose -f e2e/compose.test.yml config exits 0
- [x] `cd e2e && npx playwright test --grep smoke` exits 0 (THE Phase 1 gate; 1 passed in 7.9s)
- [x] python3 yaml-validate ci.yml exits 0
- [ ] Task 4 manual smoke checkpoint — PENDING OPERATOR APPROVAL

**For Phase 2 (DOCK-01..04):**
- `internal/docker.Client` stub is empty (plan 01 created the directory); Phase 2 fills it. No interface contract was set in stone by this plan beyond what plan 01 stubbed.
- `e2e/compose.test.yml` already bind-mounts `/var/run/docker.sock` and `./compose.test.yml:/host/docker-compose.yml:ro` — forward compatibility for DOCK-02 (compose reader) and DOCK-01 (docker client).
- The `hmi-update.watch=true` label is on `stub-watched-container` per plan; Phase 2's DOCK-01 will read it.

**For Phase 5 (UI):**
- The Svelte shell renders a 7-column empty table at /. The Table.svelte seam is in place (plan 03).
- `internal/api/types.go` -> `ui/src/lib/types.d.ts` is the source-of-truth pipeline; tygo `include_files: [types.go]` keeps server-internal types out.

**For Phase 7 (deploy):**
- Dockerfile is dev-grade; size/RAM verification and the `cc-debian12` fallback decision are deferred per CONTEXT.md.
- The `/state` tmpfs in compose.test.yml is intentionally non-durable; production will bind-mount a host path with pre-set ownership.

**For Phase 8 (CI/release):**
- CI workflow ships the check surface (no image push). Adding image build/publish requires a new job; current workflow is the foundation.

## Self-Check: PASSED

All files referenced in this summary exist on disk. All three task commit hashes are present in `git log`. The smoke test runs green end-to-end.
