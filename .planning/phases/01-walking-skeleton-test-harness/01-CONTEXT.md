# Phase 1: Walking Skeleton & Test Harness - Context

**Gathered:** 2026-05-13
**Status:** Ready for planning
**Mode:** Smart discuss (autonomous) — infrastructure-heavy phase, no scoping debate required; two load-bearing decisions captured from user

<domain>
## Phase Boundary

Produce the minimum end-to-end test harness that lets a Playwright test drive a real `hmi-update` binary inside a real `docker compose` stack and assert on `/api/state` — so every later phase's red test is meaningful. Scope is intentionally narrow: scaffold the repo skeleton, ship an atomic JSON state store with unit tests, stand up the minimum HTTP server with `/healthz` and `/api/state`, embed an empty Svelte 5 + Vite + Tailwind shell via `//go:embed`, wire `tygo` as a CI fail-on-diff check, and bring up an `e2e/compose.test.yml` test stack containing `project-zot/zot` as the fake registry plus one stub watched container that Playwright's `globalSetup` brings up via `docker compose up -d --wait`. Includes the manifest-push fixture (`oras push` or a small Go helper) that lets later phases flip `:latest` mid-test, and the first Playwright smoke test asserting the empty table renders and `/api/state` returns valid JSON.

Out of scope for this phase: real container watching (Phase 2), real registry digest detection (Phase 3), Update/Rollback/Force-pull actions (Phase 4), the real UI surface (Phase 5), display-blackout UX (Phase 6), production Dockerfile and image-size verification (Phase 7), full GitHub Actions pipeline (Phase 8).

</domain>

<decisions>
## Implementation Decisions

### Repo & Module
- **Repo location:** `/Users/jonb/Projects/tmp` is the repository root. Source code (`cmd/`, `internal/`, `ui/`, `e2e/`, `Dockerfile`, `Makefile`, `go.mod`, `.github/`) lives alongside `.planning/`. User decision 2026-05-13. The directory is already git-initialized; can be renamed/pushed to `centroid-is/hmi-update` later without restructuring.
- **Go module path:** `github.com/centroid-is/hmi-update` — matches the published image path and the brief's stated future repo location.
- **Go version:** `1.26` (research correction over brief's 1.23 — Go 1.23 went EOL 2026-02-11). Builder image: `golang:1.26-alpine`.
- **Package layout:** `cmd/hmi-update` + `internal/{api,state,docker,registry,poll,compose,actions}` + `ui/` + `e2e/`. ARCHITECTURE.md confirmed the brief's layout with two additions (`internal/compose`, `internal/actions`); both are stubbed in this phase even though their bodies arrive in later phases.
- **Frontend layout:** `ui/src/` with Vite emitting to `ui/dist/`; Go embeds via `//go:embed all:dist` from a sibling path that's copied/symlinked during build. ARCHITECTURE.md recommendation: Vite emits directly into `internal/api/dist/` so the embed directive lives next to the HTTP handler.
- **TypeScript types contract:** `tygo` generates `ui/src/lib/types.d.ts` from `internal/api/types.go`. `make types` is the regenerate command; `make check-types` is the CI fail-on-diff check.

### State Store
- **Library:** `github.com/google/renameio/v2` for atomic writes — handles temp-file-same-dir + rename + directory fsync correctly. Do not hand-roll.
- **State path:** `./hmi_update_state.json` in the working directory; bind-mounted via compose to a host path. Same-directory temp file is mandatory (cross-FS rename returns `EXDEV`).
- **Schema:** `{"version": 1, "containers": {...}}` matching the brief's §F4 schema verbatim.
- **Concurrency:** `state.Store` exposes `Get()` / `Update(func(*State))` with `sync.RWMutex` around an in-memory snapshot; persists on every mutating call. Phase 4 owns the fault-injection SIGKILL test; this phase ships the unit test for "corrupted file leaves the file parseable-old or parseable-new" simulating a half-write.

### HTTP Server
- **Router:** stdlib `net/http` `ServeMux` (Go 1.22+ pattern matching). No `chi`.
- **Endpoints in Phase 1:**
  - `GET /healthz` — returns 200 if state file is readable; 503 with remediation hint otherwise. Phase 2 will add the docker-socket reachability check.
  - `GET /api/state` — returns the in-memory state snapshot as JSON.
  - `GET /` and `GET /assets/*` — serves the embedded Svelte bundle. Strict `/assets/*` no-fallback (404 on miss, never `index.html`). MIME types registered explicitly via `mime.AddExtensionType` for `.js`, `.css`, `.svg`, `.json`.
- **Port:** `8080`.

### Frontend (empty shell)
- **Versions:** Svelte 5.55 (runes API) + Vite 7 + Tailwind v4.3 + `@tailwindcss/vite` + `vite-plugin-svelte@6`.
- **Single page** at `/`, no router. Renders a placeholder table with header columns `container | image:tag | current digest | available digest | previous digest | status | actions` and an empty body.
- **Tygo-driven types:** The page imports `Container` and `State` types from `ui/src/lib/types.d.ts` even though it does not yet use them — proves the codegen pipeline end-to-end.

### Test Stack
- **Fake registry:** `project-zot/zot:v2.1+` (latest stable 2026 line). Mutable tags, OCI-compliant, anonymous-pull-by-default. Configured via `e2e/zot-config.json`.
- **Compose file:** `e2e/compose.test.yml` brings up: `zot` (port 5000 internal, mapped to host random port), `hmi-update` built from local Dockerfile with the dev stage target, and one `stub-watched-container` (a `busybox:latest` retagged into zot as `localhost:5000/centroid-is/stub:latest`) labeled `hmi-update.watch=true`. `docker compose up -d --wait` is the gate.
- **Manifest-push fixture:** Use `oras` CLI inside a small Node helper called from Playwright `globalSetup`. Fallback: a 30-LOC Go helper (`e2e/fakereg/pushmanifest.go`) embedded into the test stack and invoked via `docker exec`. Phase 1 starts with `oras` (simpler); fall back to Go helper if `oras` flakes in CI.

### Playwright
- **Version:** `@playwright/test@1.60+`.
- **Config:** `e2e/playwright.config.ts` with `globalSetup: ./global-setup.ts` and `globalTeardown: ./global-teardown.ts`.
- **`globalSetup`** runs `docker compose -f compose.test.yml up -d --wait`, pushes the initial `:latest` manifest into zot, and waits on `GET http://localhost:8080/healthz` returning 200.
- **`globalTeardown`** runs `docker compose -f compose.test.yml down -v` to reset everything between runs.
- **First smoke test (`e2e/tests/smoke.spec.ts`)** — written RED FIRST per C4, then implementation drives it green. Asserts:
  - `GET /healthz` returns 200
  - `GET /` renders `<table>` with the expected six header columns
  - `GET /api/state` returns `{"version": 1, "containers": {...}}` with the stub container's service name as a key

### UI design contract (per user request)
- **Generate UI-SPEC.md for the empty shell.** Even though Phase 5 owns the real UI, the user requested a thin UI-SPEC now to scaffold Phase 5's work. The spec covers: layout primitives (single-page max-width container, table component shell, header bar with title + future buttons), Tailwind v4 color tokens (zinc/slate baseline, semantic state colors deferred to Phase 5), typography (system font stack), and the empty-table component contract that Phase 5 fills with real rows.

### CI baseline (minimal)
- `.github/workflows/ci.yml` runs on push/PR: `go vet`, `go test ./...`, `make check-types`, `npm --prefix ui run build`, `make e2e`. Image build and publish belong to Phase 8 — this phase ships the *check* surface.
- e2e job uses `docker/setup-buildx-action`, `docker/setup-compose-action`, then `make e2e` (which runs `docker compose -f e2e/compose.test.yml up -d --wait && npx playwright test --config e2e/playwright.config.ts`).

### Makefile targets
- `make build` — `go build -o bin/hmi-update ./cmd/hmi-update`
- `make ui` — `npm --prefix ui ci && npm --prefix ui run build`
- `make types` — `tygo generate` (config: `tygo.yaml` at repo root mapping `internal/api` → `ui/src/lib/types.d.ts`)
- `make check-types` — `make types && git diff --exit-code ui/src/lib/types.d.ts` (CI fail-on-diff)
- `make test` — `go test ./...`
- `make e2e` — bring stack up, `npx playwright test`, tear down on completion
- `make image` — multi-stage docker build (rough version in this phase; production-grade in Phase 7)
- `make clean` — remove `bin/`, `ui/dist/`, `node_modules/`, `.playwright/`

### Dockerfile (dev-grade for this phase)
- Multi-stage: `node:22-alpine` (build Svelte) → `golang:1.26-alpine` (build Go with embedded bundle) → `gcr.io/distroless/static-debian12:nonroot` (run). Phase 7 owns size/RAM verification and the `cc-debian12` fallback decision. Phase 1 just needs the image to *build* and *run* for the test stack.

### Claude's Discretion
- Linter selection (`golangci-lint` with a default config; rules can be tuned in Phase 8).
- Logger setup beyond stdlib `slog` (level via `HMI_UPDATE_LOG_LEVEL` env var; JSON handler by default).
- The exact zot config file shape (anonymous pull, mutable tags, in-memory storage with persistence disabled for tests).
- Minor naming details inside packages (e.g., `state.New` vs `state.Open`).
- Whether to put `tygo.yaml` at repo root or under `internal/api/`.
- Whether the empty Svelte page uses a single component or splits into `App.svelte` + `Table.svelte` (lean toward `App.svelte` + `Table.svelte` so Phase 5 has the seam).

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- None — greenfield repo. Codebase scan turned up only `.planning/`, `hmi-update-brief.md`, and a freshly initialized `.git/`.

### Established Patterns
- None in-repo. Patterns are imported from research:
  - **Atomic writes**: `renameio.WriteFile` (STACK.md, ARCHITECTURE.md, PITFALLS.md Pitfall 7)
  - **Embed strategy**: `//go:embed all:dist` with strict `/assets/*` immutable + `index.html` no-cache (ARCHITECTURE.md, PITFALLS.md Pitfall 8)
  - **Concurrency**: `sync.RWMutex` + single-consumer channel (ARCHITECTURE.md) — Phase 1 only ships the `RWMutex` half; the channel arrives in Phase 3
  - **MIME registration**: explicit `mime.AddExtensionType` at boot (PITFALLS.md Pitfall 8)

### Integration Points
- Phase 1 wires up the *stubs* and *interfaces* that Phases 2–4 fill:
  - `internal/docker.Client` interface — Phase 2 implements
  - `internal/registry.Resolver` interface — Phase 3 implements
  - `internal/poll.Poller` interface — Phase 3 implements
  - `internal/compose.Runner` interface — Phase 4 implements
  - `internal/actions.Orchestrator` interface — Phase 4 implements
- HTTP handlers in `internal/api/handlers.go` reference `state.Store` (real) and the four interfaces above (no-op stubs returning `errors.New("not implemented")` that the smoke test does not exercise).

</code_context>

<specifics>
## Specific Ideas

- The first Playwright smoke test (`e2e/tests/smoke.spec.ts`) is written **red-first** before any Go code lands — per C4. The test asserts the API contract and table rendering only; no business logic. Implementation drives it green.
- Linter selection should not block the phase: pick sensible defaults, document the override pattern, move on.
- The Svelte shell's `Table.svelte` exports `Props = { containers: Container[] }` so Phase 5 has the seam already cut.
- Manual smoke on an HMI-like stack for Phase 1 = `docker compose up -d --wait` on a clean Debian 12 VM (or recent docker desktop) reaches `/healthz` 200 and the empty `/` table.

</specifics>

<deferred>
## Deferred Ideas

- **arm64 buildx flip** — V2-ARM64. Phase 7 verifies amd64-only image; the buildx multi-platform flip is a future-phase concern.
- **Schema migration mechanism** — `version: 2`+ migration logic not needed until a schema change actually lands. Phase 1 ships the `version: 1` literal and a no-op migrator.
- **Real container watching** — Phase 2 (DOCK-01..04).
- **Tag-pattern regex parsing** — Phase 3 (DETECT-08).
- **Pre-action "display may flicker" toast** — Phase 5 (UI-08).
- **`cc-debian12` fallback decision** — Phase 7 (DEPLOY-02) when the docker + compose CLI plugins are actually inside the image.
- **Real-GHCR anonymous-flow smoke job** — Phase 8 (CI-04).

</deferred>
