# Phase 1: Walking Skeleton & Test Harness - Research

**Researched:** 2026-05-13
**Domain:** Greenfield Go + Svelte scaffolding; atomic JSON persistence; embedded SPA; e2e harness with zot fake registry + Playwright globalSetup
**Confidence:** HIGH

## Summary

Phase 1 is a scaffolding phase, not a stack-decision phase. Stack decisions are locked in `.planning/research/STACK.md`, architecture in `ARCHITECTURE.md`, pitfalls in `PITFALLS.md`, and concrete implementation choices in `01-CONTEXT.md`. This research fills only the *implementation-detail* gaps the planner needs to sequence tasks correctly. It assumes the planner already knows what to use; it tells the planner *exactly how* the small load-bearing pieces work so plans are concrete and runnable.

Ten gaps were targeted; nine produced concrete answers, one produced a **correction to upstream research** that the planner must address:

1. **`tygo.yaml` shape** — exact verbatim YAML; canonical CI pattern is `tygo generate && git diff --exit-code` (no native `--check` flag exists). [HIGH]
2. **`renameio/v2` API** — `WriteFile(filename, data, perm, opts...) error`. **Correction:** `renameio.WriteFile` does **NOT** fsync the parent directory — only the file before rename. The planner must address durability either by (a) accepting the kernel-crash window (acceptable for v1; PITFALLS Pitfall 7 mitigation reasons survive on `data=ordered`), or (b) wrapping `renameio.WriteFile` with an explicit `os.Open(dir).Sync()` after the call. [HIGH — verified in source]
3. **`//go:embed all:dist` pattern** — strict `/assets/*` no-fallback handler using `http.FileServerFS(fs.Sub(distFS, "dist"))`; explicit `mime.AddExtensionType` calls in `init()`; index.html `no-cache`, assets `immutable`. [HIGH]
4. **Playwright globalSetup + docker compose up -d --wait** — canonical pattern is to call `docker compose -f ... up -d --wait` from `global-setup.ts`, expose ports via static mapping in the test compose (not dynamic), and poll `/healthz` from setup before returning. [HIGH]
5. **zot 2026 config for tests** — the official `examples/config-minimal.json` is open by default (no `accessControl` ⇒ no auth check ⇒ anonymous pull+push). For Phase 1 fixture: use minimal config, point `rootDirectory` at a tmpfs/scratch path, set log level to `error` to suppress noise. **Note:** `dedupe`/`gc` keys live under `storage` and are `true` by default — disable both for clean test reruns. [HIGH]
6. **`oras` CLI vs Go helper** — `oras push` is the cleaner Phase 1 path; canonical command shape provided below. Fallback to `crane.Push()` documented for Phase 2 if `oras` flakes in CI. [HIGH]
7. **Multi-stage Dockerfile (dev-grade)** — three-stage pattern: `node:22-alpine` → `golang:1.26-alpine` → `gcr.io/distroless/static-debian12:nonroot`. Vite must emit into `internal/api/dist/` so the `//go:embed` directive resolves; verbatim Dockerfile below. [HIGH]
8. **GitHub Actions CI baseline** — minimum-viable pipeline: checkout → setup-go@v5 → setup-node@v4 → `go vet` → `go test` → `make check-types` → `npm --prefix ui ci && npm --prefix ui run build` → `make e2e`. No image build/publish (Phase 8 owns that). [HIGH]
9. **Repo skeleton order of operations** — a strict ordering exists: directory tree → `go.mod` → minimum Go source → `npm init` in ui/ → `vite.config.ts` (outDir) → `embed.FS` source file → first build → Playwright init. Detailed below. [HIGH]
10. **Seven-column empty-table Svelte 5 component** — verbatim Svelte 5 + runes implementation with `Props = { containers: Container[] }` and `colspan="7"` empty-state cell. [HIGH]

**Primary recommendation:** Plans should sequence Phase 1 in the order spelled out in "Repo Skeleton Order of Operations" below. Address the **renameio directory-fsync correction** explicitly in the state-store task (either accept the known limitation or add the wrapper).

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Repo & Module:**
- Repo location: `/Users/jonb/Projects/tmp` is the repository root. Source code (`cmd/`, `internal/`, `ui/`, `e2e/`, `Dockerfile`, `Makefile`, `go.mod`, `.github/`) lives alongside `.planning/`. Directory already `git`-initialized.
- Go module path: `github.com/centroid-is/hmi-update`.
- Go version: `1.26`. Builder image `golang:1.26-alpine`.
- Package layout: `cmd/hmi-update` + `internal/{api,state,docker,registry,poll,compose,actions}` + `ui/` + `e2e/`. `internal/compose` and `internal/actions` are stubbed this phase even though their bodies arrive later.
- Frontend layout: `ui/src/` with Vite emitting directly into `internal/api/dist/` so the embed directive lives next to the HTTP handler.
- TS types contract: `tygo` generates `ui/src/lib/types.d.ts` from `internal/api/types.go`. `make types` regenerates; `make check-types` is the CI fail-on-diff check.

**State Store:**
- Library: `github.com/google/renameio/v2`. Do not hand-roll.
- State path: `./hmi_update_state.json` in working directory; bind-mounted via compose. Same-directory temp file is mandatory (cross-FS rename returns `EXDEV`).
- Schema: `{"version": 1, "containers": {...}}` matching brief §F4 verbatim.
- Concurrency: `state.Store` exposes `Get()` / `Update(func(*State))` with `sync.RWMutex` around in-memory snapshot; persists on every mutating call. Phase 4 owns SIGKILL fault-injection; Phase 1 ships unit test for "corrupted file leaves the file parseable-old or parseable-new" simulating a half-write.

**HTTP Server:**
- Router: stdlib `net/http` `ServeMux` (Go 1.22+ pattern matching). No `chi`.
- Endpoints in Phase 1: `GET /healthz` (200 if state file readable, 503 with remediation otherwise); `GET /api/state` (in-memory snapshot as JSON); `GET /` and `GET /assets/*` (Svelte bundle). Strict `/assets/*` no-fallback (404 on miss, never `index.html`). MIME types registered explicitly via `mime.AddExtensionType` for `.js`, `.css`, `.svg`, `.json`.
- Port: `8080`.

**Frontend (empty shell):**
- Versions: Svelte 5.55 (runes API) + Vite 7 + Tailwind v4.3 + `@tailwindcss/vite` + `vite-plugin-svelte@6`.
- Single page at `/`, no router. Renders placeholder table with seven header columns and empty body.
- Tygo-driven types: page imports `Container` and `State` from `ui/src/lib/types.d.ts` even though not yet used — proves codegen end-to-end.

**Test Stack:**
- Fake registry: `project-zot/zot:v2.1+` (latest stable 2026 line). Mutable tags, OCI-compliant, anonymous-pull-by-default. Configured via `e2e/zot-config.json`.
- Compose file: `e2e/compose.test.yml` brings up `zot` (port 5000 internal, mapped to host random port), `hmi-update` built from local Dockerfile with dev stage target, one `stub-watched-container` (`busybox:latest` retagged into zot as `localhost:5000/centroid-is/stub:latest`) labeled `hmi-update.watch=true`. `docker compose up -d --wait` is the gate.
- Manifest-push fixture: `oras` CLI inside small Node helper called from Playwright `globalSetup`. Fallback: 30-LOC Go helper. Start with `oras`, fall back if it flakes.

**Playwright:**
- Version `@playwright/test@1.60+`.
- `e2e/playwright.config.ts` with `globalSetup: ./global-setup.ts` and `globalTeardown: ./global-teardown.ts`.
- `globalSetup` runs `docker compose -f compose.test.yml up -d --wait`, pushes initial `:latest` manifest into zot, waits on `GET http://localhost:8080/healthz` returning 200.
- `globalTeardown` runs `docker compose -f compose.test.yml down -v`.
- First smoke test (`e2e/tests/smoke.spec.ts`) — written RED FIRST per C4. Asserts `/healthz` 200, `/` renders `<table>` with expected header columns, `/api/state` returns `{"version": 1, "containers": {...}}`.

**UI design contract:** see `01-UI-SPEC.md`. Empty-state row uses `colspan="7"`. Empty-state copy locked: heading `No watched containers yet`; body `Label a service in your compose file with hmi-update.watch=true and it will appear here on the next poll.`

**CI baseline (minimal):**
- `.github/workflows/ci.yml`: `go vet`, `go test ./...`, `make check-types`, `npm --prefix ui run build`, `make e2e`. **Image build and publish belong to Phase 8.**
- e2e job uses `docker/setup-buildx-action`, `docker/setup-compose-action`, then `make e2e`.

**Makefile targets:** `make build`, `make ui`, `make types`, `make check-types`, `make test`, `make e2e`, `make image`, `make clean`. Exact bodies in CONTEXT.md.

**Dockerfile (dev-grade for this phase):**
Multi-stage `node:22-alpine` → `golang:1.26-alpine` → `gcr.io/distroless/static-debian12:nonroot`. Phase 7 owns size/RAM verification and `cc-debian12` fallback. Phase 1 just needs the image to build and run.

### Claude's Discretion

- Linter selection (`golangci-lint` with default config; rules tunable in Phase 8).
- Logger setup beyond stdlib `slog` (level via `HMI_UPDATE_LOG_LEVEL` env var; JSON handler by default).
- Exact zot config file shape.
- Minor naming details inside packages (`state.New` vs `state.Open`).
- Whether `tygo.yaml` lives at repo root or under `internal/api/`.
- Whether empty Svelte page is single component or splits into `App.svelte` + `Table.svelte` — lean toward `App.svelte` + `Table.svelte` so Phase 5 has the seam.

### Deferred Ideas (OUT OF SCOPE)

- **arm64 buildx flip** — V2-ARM64; Phase 7 verifies amd64-only image.
- **Schema migration mechanism** — `version: 2`+ migration logic not needed until a schema change lands. Phase 1 ships `version: 1` literal and a no-op migrator.
- **Real container watching** — Phase 2 (DOCK-01..04).
- **Tag-pattern regex parsing** — Phase 3 (DETECT-08).
- **Pre-action "display may flicker" toast** — Phase 5 (UI-08).
- **`cc-debian12` fallback decision** — Phase 7 (DEPLOY-02).
- **Real-GHCR anonymous-flow smoke job** — Phase 8 (CI-04).
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description (from REQUIREMENTS.md) | Research Support |
|----|-------------|------------------|
| FOUND-01 | Repository scaffolding exists with `cmd/hmi-update`, `internal/{api,state,docker,registry,poll,compose,actions}`, `ui/`, `e2e/`, `Dockerfile`, `Makefile`, `go.mod`, `.github/workflows/` | "Repo Skeleton Order of Operations" section below |
| FOUND-02 | `internal/state` persists versioned schema (`version: 1`, `containers: {...}`) to `./hmi_update_state.json` via `google/renameio/v2`. Unit-tested across corrupted-file, missing-file, schema-bump scenarios | "renameio/v2 API and the directory-fsync correction" section |
| FOUND-03 | HTTP server with `GET /healthz` and `GET /api/state` returning valid JSON, single Go process on port 8080 | Standard stdlib `net/http` `ServeMux`; Pattern 1 below |
| FOUND-04 | Empty Svelte 5 + Vite + Tailwind v4 shell embedded via `//go:embed all:dist`, served at `/`, MIME-aware static handler with strict `/assets/*` no-fallback | "go:embed + Vite + MIME registration" section |
| FOUND-05 | `e2e/compose.test.yml` brings up `project-zot/zot` fake registry + `hmi-update` + one stub watched container; `docker compose up -d --wait` succeeds in CI | "Compose test stack" + "zot configuration" sections |
| FOUND-06 | Playwright `globalSetup` drives `docker compose up -d --wait`; first smoke test asserts table renders and `/api/state` returns valid JSON | "Playwright globalSetup pattern" section |
| FOUND-07 | Manifest-push fixture (`oras push` or Go helper) flips `:latest` in zot mid-test | "Manifest-push fixture" section |
| FOUND-08 | `tygo` generates `ui/src/lib/types.d.ts` from `internal/api/types.go`; `make types` is a CI fail-on-diff check | "tygo configuration" section |
| STATE-01 | All state in `./hmi_update_state.json` (bind-mounted into container). No SQLite, no Mongo, no Redis | Locked by CONTEXT.md and ARCHITECTURE.md |
| STATE-02 | Atomic writes via `google/renameio/v2` (temp file in same directory + rename + directory fsync) — Pitfall 7 prevention | "renameio/v2 API and the directory-fsync correction" section — **planner must address** the fact that renameio does not auto-fsync the directory |
| STATE-03 | Schema field `version: 1` present; service reads state from JSON on boot and resumes (N2 stateless self-restart) | "State schema" section |
</phase_requirements>

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Atomic state persistence | Go backend (`internal/state`) | Filesystem (bind-mounted JSON) | Single-writer in-memory cache; disk is durability backstop only (locked in ARCHITECTURE.md Pattern 1) |
| HTTP routing | Go backend (`internal/api`) | — | stdlib `net/http` ServeMux; no client-side routing |
| Static asset serving | Go backend (`internal/api`, `embed.FS`) | — | Single-binary deployment; assets live inside the binary, not on a CDN |
| UI rendering | Browser (Svelte 5 + Vite bundle) | — | Pure client-side render of HTML table; data fetched from `/api/state` |
| TS type generation | Build-time (Go-side `tygo` reading `internal/api/types.go`) | — | Single source of truth: Go struct definitions |
| Test orchestration | E2E layer (Playwright `globalSetup` driving `docker compose`) | Docker daemon (host) | Real binary in real compose; no mocked HTTP at this layer |
| Fake registry | Test infra (zot container in `compose.test.yml`) | — | Isolated, anonymous, in-memory-ish for ephemerality |
| Manifest push fixture | Test infra (`oras` CLI invoked from Playwright `globalSetup`) | Fallback: Go helper inside compose | Flip `:latest` from outside the SUT |

## Standard Stack

> All stack decisions are locked in `.planning/research/STACK.md` and `01-CONTEXT.md`. This table reproduces only the Phase 1 surface for planner convenience. Do not re-debate these.

### Core (Phase 1 surface)
| Library | Version | Purpose | Status |
|---------|---------|---------|--------|
| Go | 1.26.x | Compiler / runtime | `[VERIFIED: local install go1.26.0 darwin/arm64; locked in STACK.md]` |
| `net/http` (stdlib) | std | HTTP server + router | `[CITED: STACK.md]` |
| `github.com/google/renameio/v2` | v2.x | Atomic file write | `[VERIFIED: github.com/google/renameio source inspection 2026-05-13]` |
| `log/slog` (stdlib) | std | Structured JSON logs | `[CITED: STACK.md]` |
| Svelte | 5.55.x | UI framework | `[CITED: STACK.md]` |
| Vite | 7.x | Bundler | `[CITED: STACK.md]` |
| `@sveltejs/vite-plugin-svelte` | 6.x | Svelte compile glue | `[CITED: STACK.md]` |
| Tailwind CSS | 4.3.x | Styling | `[CITED: STACK.md]` |
| `@tailwindcss/vite` | 4.x | Tailwind v4 Vite plugin | `[CITED: STACK.md]` |
| TypeScript | 5.6+ | UI type safety | `[CITED: STACK.md]` |
| `//go:embed` | std | Embed `dist/**` | `[CITED: STACK.md]` |

### Phase 1 supporting
| Tool | Purpose | Status |
|------|---------|--------|
| `github.com/gzuidhof/tygo` | Go-→-TS type generation (CLI: `tygo`) | `[VERIFIED: github.com/gzuidhof/tygo README 2026-05-13]` |
| `@playwright/test` v1.60+ | E2E tests | `[CITED: STACK.md]` |
| `project-zot/zot` v2.1+ | Fake OCI registry in test stack | `[VERIFIED: github.com/project-zot/zot examples/config-minimal.json 2026-05-13]` |
| `oras` CLI | Push manifests mid-test | `[VERIFIED: oras.land/docs/commands/oras_push/ 2026-05-13]` |
| `gcr.io/distroless/static-debian12:nonroot` | Runtime image | `[CITED: STACK.md]` |

### Stubbed (interfaces declared, no body) in Phase 1
| Interface | Owner Phase | Phase 1 Action |
|-----------|-------------|----------------|
| `internal/docker.Client` | Phase 2 | Empty struct + interface declaration so `internal/api` compiles |
| `internal/registry.Resolver` | Phase 3 | Same |
| `internal/poll.Poller` | Phase 3 | Same |
| `internal/compose.Runner` | Phase 4 | Same |
| `internal/actions.Orchestrator` | Phase 4 | Same |

## Project Constraints (from CLAUDE.md)

CLAUDE.md is GSD-workflow generated and contains the locked stack, conventions, and workflow rules. Phase 1-specific takeaways:

- **C1. One container, one binary** — Phase 1 ships the single-binary scaffold; no sidecars, no extra processes.
- **C2. File-based persistence only** — `./hmi_update_state.json` is the only persistence mechanism. No SQLite, no Mongo, no Redis.
- **C3. Self-contained compose deployment** — the production compose service block is finalized in Phase 7, but Phase 1's `e2e/compose.test.yml` already exercises the bind-mount shape (`docker.sock`, compose file, state file).
- **C4. TDD: verify → implement → verify → implement** — Phase 1's smoke test (`e2e/tests/smoke.spec.ts`) is written RED FIRST before any Go code lands; implementation drives it green. This is the gating ordering constraint for the planner.
- **GSD workflow enforcement** — file edits must go through GSD commands (`/gsd-execute-phase` from the plans). The agent doing the work must not edit files directly.
- **Repo identity** — module path `github.com/centroid-is/hmi-update`; image path `ghcr.io/centroid-is/hmi-update`. Repo will be renamed/pushed later; Phase 1 uses the published name from day one.

## Repo Skeleton Order of Operations

**The biggest risk in this phase is sequencing.** A naive plan that creates everything at once produces red builds because Go, Vite, and Playwright each need certain files present before their first run succeeds. Follow this order. Each numbered step is a *prerequisite* for the steps below it.

1. **Directory tree only** — create empty directories: `cmd/hmi-update/`, `internal/{api,state,docker,registry,poll,compose,actions}/`, `ui/`, `e2e/`, `.github/workflows/`. No files yet. This is purely so `go mod init` and `tygo` can resolve paths.

2. **`go.mod` at repo root** — `go mod init github.com/centroid-is/hmi-update`. Adds `go 1.26` directive. *Must precede any `.go` file* because `go build` needs the module path to resolve imports under `internal/`.

3. **`internal/api/types.go` with stub types** — minimum content: a `State` struct with `Version int` and `Containers map[string]Container`, and an empty `Container` struct. The tygo step needs this file to exist. Stub interfaces for the five non-API packages (`docker.Client`, `registry.Resolver`, `poll.Poller`, `compose.Runner`, `actions.Orchestrator`) each go in their own package as a `type Client interface{}` (or named per the package).

4. **`tygo.yaml` at repo root** — see "tygo configuration" section below. References `github.com/centroid-is/hmi-update/internal/api` and emits to `ui/src/lib/types.d.ts`. *Cannot be created before step 3* because `tygo generate` will fail without the source package.

5. **`go get` the runtime deps** — `go get github.com/google/renameio/v2`. Phase 2/3/4 deps (`moby/moby/client`, `robfig/cron/v3`, `go-containerregistry`) can be added now or later; CONTEXT.md is silent, so defer them to keep the Phase 1 dep tree minimal.

6. **`internal/state/{schema.go,store.go,persist.go}`** — schema (the `version: 1` literal), Store struct with `sync.RWMutex` + in-memory map, persist via `renameio.WriteFile`. **Tests** live in `internal/state/store_test.go` and `persist_test.go`. *Must compile* before step 7 because `internal/api` imports `internal/state`.

7. **`internal/api/{server.go,handlers.go,static.go,types.go}`** — HTTP `ServeMux` wiring, `/healthz` and `/api/state` handlers, embedded static handler (`//go:embed all:dist`). **`static.go` must reference `dist/`** — the embed directive will fail at build time if `internal/api/dist/` doesn't exist yet. Workaround: put a `.gitkeep` file in `internal/api/dist/` so `embed all:dist` resolves to an empty (but valid) FS until Vite emits the real bundle. **This is the trap.** Without it `go build` red-fails on first run.

8. **`cmd/hmi-update/main.go`** — wire `state.Store` + `api.Server`; start HTTP on `:8080`. Now `go build ./cmd/hmi-update` should succeed.

9. **`ui/` initialized with Vite scaffold** — `cd ui && npm create vite@latest . -- --template svelte-ts`. Then `npm install -D tailwindcss@^4 @tailwindcss/vite@^4 @sveltejs/vite-plugin-svelte@^6`. `vite.config.ts` is edited to set `build.outDir: '../internal/api/dist'` (relative to `ui/`) and `build.emptyOutDir: true`. **This must precede `npm run build`** or assets land in the wrong place and `//go:embed` picks up nothing.

10. **`ui/src/App.svelte` and `ui/src/lib/Table.svelte`** — verbatim implementation from UI-SPEC.md. Import `Container` from `./types.d.ts` (which doesn't exist yet — TypeScript will error). Run `make types` to generate `ui/src/lib/types.d.ts`. Now the imports resolve.

11. **First `npm --prefix ui run build`** — emits `internal/api/dist/{index.html, assets/*}`. **Delete the `.gitkeep` from step 7 *after* this build succeeds** so the real bundle is what's embedded. Add a `.gitignore` rule for `internal/api/dist/` so the build output isn't checked in (CI rebuilds on every run).

12. **`go build ./cmd/hmi-update` again** — this time the binary embeds the real Svelte bundle. Verify by running it locally on `:8080` and curling `/` to see the rendered HTML.

13. **`e2e/` directory initialized** — `cd e2e && npm init -y && npm install -D @playwright/test@^1.60.0 && npx playwright install --with-deps chromium`. Create `e2e/playwright.config.ts`, `e2e/global-setup.ts`, `e2e/global-teardown.ts`, `e2e/compose.test.yml`, `e2e/zot-config.json`, `e2e/tests/smoke.spec.ts`. See sections below for verbatim content.

14. **`Dockerfile`** at repo root — see "Dockerfile" section. The test compose builds the image from this file; Phase 1's Dockerfile is dev-grade (works, doesn't need to be size-optimized).

15. **`Makefile`** — see verbatim Makefile in CONTEXT.md.

16. **`.github/workflows/ci.yml`** — see "GitHub Actions CI baseline" section.

17. **First `make e2e` locally** — should produce a green smoke test. *This is the Phase 1 gate.*

**Why this order matters:** Steps 7, 9, 10 are the three places where a naive sequencing produces a red build:
- Step 7 without the `.gitkeep` ⇒ `embed all:dist` fails (no matching files).
- Step 9 without the vite outDir override ⇒ Vite emits to `ui/dist/` and `//go:embed` finds nothing.
- Step 10 without `make types` having been run ⇒ the Svelte file imports a non-existent module.

## Architecture Patterns

### System Architecture Diagram (Phase 1 surface only)

```
                  ┌──────────────────────────────────┐
                  │ Playwright test runner (host)    │
                  │  globalSetup ──► docker compose  │
                  │     │              up -d --wait  │
                  │     ▼                            │
                  │  oras push ──► zot :5000         │
                  │     │                            │
                  │     ▼                            │
                  │  fetch /healthz ─── 200 OK ──►   │
                  │     │                            │
                  │     ▼                            │
                  │  smoke.spec.ts assertions        │
                  └──────────────────────────────────┘
                              │ (HTTP)
                              ▼
   ┌───────────────────────────────────────────────────┐
   │ Docker network (e2e/compose.test.yml)             │
   ├───────────────────────────────────────────────────┤
   │                                                   │
   │  ┌─────────┐    ┌──────────────────┐   ┌───────┐  │
   │  │  zot    │    │  hmi-update      │   │ stub  │  │
   │  │ :5000   │    │  :8080           │   │ busy  │  │
   │  │ (fake   │    │  ┌──────────────┐│   │ box   │  │
   │  │  reg)   │    │  │ ServeMux     ││   │       │  │
   │  └─────────┘    │  ├──────────────┤│   │ labels│  │
   │                 │  │ /healthz     ││   │ watch │  │
   │                 │  │ /api/state   ││   │ =true │  │
   │                 │  │ / + /assets/*││   │       │  │
   │                 │  └──────┬───────┘│   └───────┘  │
   │                 │         │        │              │
   │                 │  ┌──────▼──────┐ │              │
   │                 │  │ state.Store │ │              │
   │                 │  │ RWMutex     │ │              │
   │                 │  │ + map       │ │              │
   │                 │  └──────┬──────┘ │              │
   │                 └─────────┼────────┘              │
   │                           ▼                       │
   │           /state/hmi_update_state.json            │
   │           (bind-mount: tmpfs in tests,            │
   │            host path in prod)                     │
   └───────────────────────────────────────────────────┘
```

### Pattern 1: stdlib net/http ServeMux with Go 1.22+ pattern matching
**What:** Use `http.NewServeMux()` and `mux.HandleFunc("GET /api/state", h)` syntax. No third-party router.
**Why:** STACK.md locks this. ~8 routes across all phases; framework would be overkill.
**Example:**
```go
mux := http.NewServeMux()
mux.HandleFunc("GET /healthz",   srv.healthz)
mux.HandleFunc("GET /api/state", srv.getState)
mux.Handle("GET /assets/", srv.assetsHandler) // strict, no fallback
mux.HandleFunc("GET /",          srv.serveIndex)
http.ListenAndServe(":8080", mux)
```

### Pattern 2: in-memory state + RWMutex + atomic JSON persist (per ARCHITECTURE.md Pattern 1)
**What:** State lives in `state.Store{ state State; mu sync.RWMutex }`. Reads use `RLock`; mutations use `Lock` + persist-on-exit.
**Why:** ARCHITECTURE.md Pattern 1 — small state, high read/write ratio, single process owns the file.
**Phase 1 surface:** `Get() State`, `Update(fn func(*State)) error`. No background poll, no actions — those are Phase 2/3/4.

### Pattern 3: //go:embed all:dist with strict /assets/* no-fallback (per PITFALLS.md Pitfall 8)
See "go:embed + Vite + MIME registration" section below.

### Pattern 4: Playwright globalSetup drives docker compose up -d --wait
See "Playwright globalSetup pattern" section below.

### Anti-Patterns to Avoid (Phase 1 specific)
- **SPA-fallback for `/assets/*`:** never serve `index.html` when an asset is missing — that's the Pitfall 8 MIME trap. 404 on miss.
- **Vite emitting to `ui/dist/`:** the default Vite outDir won't be picked up by the `//go:embed` directive in `internal/api/static.go`. Override to `../internal/api/dist`.
- **Embedding without `.gitkeep`:** `//go:embed all:dist` fails at compile time if the directory has zero files. Step 7 in the order-of-operations addresses this.
- **`tygo.yaml` referencing a non-existent package:** `tygo generate` fails red without a clear error if the `path:` doesn't resolve. Create `internal/api/types.go` first.
- **Mixing the production `state.json` path with the test path:** the test compose must bind-mount the state file to a per-test tmpfs path. If both prod and tests share `./hmi_update_state.json`, running `make e2e` clobbers local dev state.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Atomic JSON write | `os.WriteFile + os.Rename + manual fsync` | `renameio/v2.WriteFile` + a wrapper for dir fsync (see correction below) | Cross-FS rename gotchas, fsync ordering, permission interaction with umask are easy to get wrong (PITFALLS Pitfall 7) |
| Go-to-TypeScript type generation | Hand-written `interface State { ... }` | `tygo` | Hand-rolled types silently drift; Go renames don't propagate (PITFALLS anti-pattern 4) |
| MIME type detection for `.js`/`.css`/`.svg` in distroless | Custom map | `mime.AddExtensionType` calls at init() + `mime.TypeByExtension` lookup in handler | Distroless minimal env may lack the system mime.types file (PITFALLS Pitfall 8) |
| OCI manifest construction for the test fixture | Hand-rolled multipart HTTP upload | `oras push --plain-http localhost:<port>/<repo>:<tag> <file>` | OCI distribution spec has subtle Accept/Content-Type rules; oras handles them |
| Wait-for-service in tests | Custom polling loop | `docker compose up -d --wait` + service `healthcheck:` blocks | Compose v2.20+ blocks until all healthchecks pass natively |
| HTTP router for ~3 routes | Third-party router (chi, gorilla) | stdlib `net/http` ServeMux (Go 1.22+) | Go 1.22 added method-prefix and path-variable matching — no framework needed |
| Cron parsing | `time.Ticker` + custom parser | `robfig/cron/v3` *(Phase 2/3, not Phase 1)* | Already locked; flagged here for completeness so Phase 1 doesn't add a ticker |

**Key insight:** Phase 1 is "use the existing libraries correctly, don't build new abstractions." Every gap below is "the library does what you want, here's the exact incantation."

## Runtime State Inventory

> Phase 1 is greenfield scaffolding. No existing runtime state to migrate.

| Category | Items Found | Action Required |
|----------|-------------|-----------------|
| Stored data | None — repo is freshly `git init`'d with only `.planning/`, `CLAUDE.md`, `hmi-update-brief.md`. No databases, no caches. | None |
| Live service config | None — no n8n / Datadog / Tailscale / Cloudflare integrations | None |
| OS-registered state | None — no Task Scheduler entries, no pm2 process names, no launchd plists, no systemd units | None |
| Secrets and env vars | None at the repo level. Phase 1 will introduce `HMI_UPDATE_LOG_LEVEL` (optional) but it's unset by default | None |
| Build artifacts | None — `bin/`, `ui/dist/`, `ui/node_modules/`, `e2e/node_modules/`, `internal/api/dist/` will be created during Phase 1 and must be `.gitignore`'d | Add `.gitignore` entries; verified by Phase 1's first commit having a clean `git status` |

**Verified:** `ls -la /Users/jonb/Projects/tmp/` shows only `.git`, `.planning/`, `CLAUDE.md`, `hmi-update-brief.md`.

## Common Pitfalls

> Project-level pitfalls live in `.planning/research/PITFALLS.md`. The four most-relevant to Phase 1 are summarized here with phase-specific framing; for full context see PITFALLS.md.

### Pitfall A: renameio does NOT auto-fsync the parent directory (correction to upstream research)

**What goes wrong:** `renameio.WriteFile` writes a temp file, fsyncs it, then renames it to the target. It does **not** open the parent directory and fsync it after the rename. On a host crash within seconds of `WriteFile` returning, the rename can be lost — the kernel hasn't necessarily flushed the directory inode to disk.

**Why it happens:** `renameio.WriteFile` calls `PendingFile.CloseAtomicallyReplace()`, which is `Sync()` (file) → `Close()` (file) → `os.Rename()`. No directory open. Verified by inspecting `renameio/tempfile.go` lines 183-210 [VERIFIED: github.com/google/renameio source 2026-05-13]. Also raised in upstream issue #11 (closed without a code change).

**Why the upstream research got it wrong:** `.planning/research/ARCHITECTURE.md` and `PITFALLS.md` both claim renameio "handles temp+rename+dirsync correctly." It does not. `[ASSUMED]` in upstream research; `[VERIFIED: source]` here.

**How to avoid (planner: pick one):**
- **Option 1 (recommended for Phase 1):** Accept the kernel-crash window. On ext4 `data=ordered` (the default), the practical risk is near-zero for a tool whose state mutates only on operator action. The PITFALLS Pitfall 7 prevention story is mostly about *process* crash (covered) and *truncation* (covered). True host-power-loss within seconds of a write is a different threat model and out of scope for an HMI on a UPS. Document this in a code comment on `internal/state/persist.go`.
- **Option 2 (defensive, +1 file open per write):** Wrap `renameio.WriteFile` with explicit dir fsync:
  ```go
  func (s *Store) persist() error {
      data, err := json.MarshalIndent(s.state, "", "  ")
      if err != nil { return err }
      if err := renameio.WriteFile(s.path, data, 0o644); err != nil {
          return err
      }
      // Belt-and-braces durability: fsync the parent directory after rename
      // so the rename is durable across host crash, not just process crash.
      // renameio does NOT do this (see issue #11 closed without fix).
      dir, err := os.Open(filepath.Dir(s.path))
      if err != nil { return nil } // best-effort; don't fail the whole write
      defer dir.Close()
      _ = dir.Sync()
      return nil
  }
  ```

**Warning signs:**
- Atomic-write unit test passes (process-crash mid-write leaves file old-or-new) but host-power-loss tests fail.
- Reading the file immediately after `WriteFile` returns shows correct content; reading after a host crash within ~5 seconds shows the old content.

**Phase to address:** Phase 1 — `internal/state` design. Planner must pick Option 1 or Option 2 and put the rationale in a code comment so reviewers don't try to "fix" it later. Recommendation: Option 2. The extra ~50 µs per write is invisible at this scale and it gives the project the durability story the brief implies.

### Pitfall B: //go:embed dist fails at build time if dist/ is empty or missing
Already covered above in "Repo Skeleton Order of Operations" step 7. Mitigation: ship a `.gitkeep` in `internal/api/dist/` so the embed resolves to an empty FS until Vite emits the real bundle. Remove after first successful `npm run build`. Add `.gitignore` for the directory after that.

### Pitfall C: Vite default outDir is ui/dist/, but the embed directive is in internal/api/
Mitigation: in `ui/vite.config.ts`, set `build.outDir: '../internal/api/dist'` and `build.emptyOutDir: true`. Verified pattern via Tushar Choudhari's "Embed Vite app in a Go Binary" article [CITED: tushar.ch/writing/embed-vite-app-in-go-binary].

### Pitfall D: Distroless may not have a system mime.types file ⇒ wrong Content-Type for .js
PITFALLS Pitfall 8. Mitigation: call `mime.AddExtensionType` in `init()` of `internal/api/static.go` for `.js`, `.css`, `.svg`, `.json`. Verbatim code in "go:embed + Vite + MIME registration" section. Smoke test asserts `Content-Type: application/javascript; charset=utf-8` on `/assets/*.js`.

### Pitfall E: Playwright globalSetup that doesn't wait for /healthz races against the binary's startup
Even with `docker compose up -d --wait`, the `hmi-update` service is "healthy" only when its compose `healthcheck:` block reports healthy. Easiest: make the healthcheck `curl -f http://localhost:8080/healthz`. With that, `--wait` blocks until `/healthz` returns 200. Without the healthcheck in compose, `--wait` returns the moment the container starts — before the Go binary has bound the listener.

## Code Examples

### tygo configuration

**`tygo.yaml`** at repo root:

```yaml
packages:
  - path: "github.com/centroid-is/hmi-update/internal/api"
    output_path: "ui/src/lib/types.d.ts"
    indent: "  "
    type_mappings:
      time.Time: "string /* RFC3339 */"
    frontmatter: |
      // This file is generated by tygo from internal/api/types.go.
      // Do not edit by hand. Run `make types` to regenerate.
```

`[VERIFIED: github.com/gzuidhof/tygo README format 2026-05-13]`

**CI fail-on-diff pattern** — there is **no native `tygo --check` mode** [VERIFIED: tygo README has no such flag 2026-05-13]. The canonical CI pattern is:

```makefile
types:
	tygo generate

check-types: types
	git diff --exit-code ui/src/lib/types.d.ts
```

`git diff --exit-code` returns 0 when there are no differences, 1 when there are — perfect for CI. Wrap in a small message for operator clarity:

```makefile
check-types: types
	@git diff --exit-code ui/src/lib/types.d.ts || \
	  (echo "ERROR: types.d.ts is out of date. Run 'make types' and commit." && exit 1)
```

**Tag respected by tygo:** the `json` struct tag is honored. Example Go struct → TS:

```go
// internal/api/types.go
package api

type Container struct {
    Service         string `json:"service"`
    Image           string `json:"image"`
    Tag             string `json:"tag"`
    CurrentDigest   string `json:"current_digest,omitempty"`
    PreviousDigest  string `json:"previous_digest,omitempty"`
    UpdateAvailable bool   `json:"update_available"`
}

type State struct {
    Version    int                  `json:"version"`
    Containers map[string]Container `json:"containers"`
}
```

generates:

```typescript
// ui/src/lib/types.d.ts (generated)
export interface Container {
  service: string;
  image: string;
  tag: string;
  current_digest?: string;
  previous_digest?: string;
  update_available: boolean;
}

export interface State {
  version: number /* int */;
  containers: { [key: string]: Container };
}
```

**Install:**
```bash
go install github.com/gzuidhof/tygo@latest
```
Verify with `tygo --help`. CI will need `go install` in the workflow before `make check-types` can run.

### renameio/v2 API and the directory-fsync correction

**Verbatim signature** `[VERIFIED: pkg.go.dev/github.com/google/renameio/v2 2026-05-13]`:

```go
func WriteFile(filename string, data []byte, perm os.FileMode, opts ...Option) error
```

**Options of interest for Phase 1:**
- `renameio.WithStaticPermissions(0o644)` — equivalent to `WithPermissions + IgnoreUmask`. Useful if the bind-mounted state file should always be 0644 regardless of the host umask.
- `renameio.IgnoreUmask()` — apply requested perm directly, ignoring process umask. Default in renameio v1 but **changed in v2** so umask is now applied; if you want v1 behavior add this opt.

**The directory-fsync correction:** see Pitfall A above. Recommended Phase 1 pattern with the wrapper:

```go
// internal/state/persist.go
package state

import (
    "encoding/json"
    "os"
    "path/filepath"

    "github.com/google/renameio/v2"
)

// persist writes the in-memory state to disk atomically.
// renameio.WriteFile handles temp-file-in-same-dir + fsync(file) + rename.
// It does NOT fsync the parent directory; we do that explicitly to make the
// rename durable across host crash, not just process crash.
// See renameio issue #11 and our research/PITFALLS.md Pitfall 7.
func (s *Store) persist() error {
    data, err := json.MarshalIndent(s.state, "", "  ")
    if err != nil {
        return err
    }
    if err := renameio.WriteFile(s.path, data, 0o644); err != nil {
        return err
    }
    // Best-effort directory fsync. If this fails the rename is still
    // visible to subsequent opens; we only lose durability across power loss.
    dir, err := os.Open(filepath.Dir(s.path))
    if err != nil {
        return nil
    }
    defer dir.Close()
    _ = dir.Sync()
    return nil
}
```

`[VERIFIED: renameio source github.com/google/renameio tempfile.go CloseAtomicallyReplace 2026-05-13]`

**Unit test pattern for Phase 1 (the "corrupted file leaves the file parseable-old or parseable-new" requirement):**

```go
// internal/state/persist_test.go
func TestPersistAtomicity(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "state.json")

    // Write initial state
    s := NewStore(path)
    s.state = State{Version: 1, Containers: map[string]Container{
        "svc1": {Service: "svc1", Image: "img", Tag: "latest"},
    }}
    if err := s.persist(); err != nil { t.Fatal(err) }

    // Simulate a half-write by directly corrupting the temp file path —
    // but that's not actually testable without process control. The realistic
    // Phase 1 test is: persist 1000 times in a goroutine while another goroutine
    // repeatedly reads the file. Every read must succeed in parsing valid JSON.
    var wg sync.WaitGroup
    stop := make(chan struct{})
    wg.Add(2)
    go func() {
        defer wg.Done()
        for i := 0; i < 1000; i++ {
            s.Update(func(st *State) {
                st.Containers["svc1"] = Container{Service: "svc1", Tag: fmt.Sprintf("v%d", i)}
            })
        }
        close(stop)
    }()
    go func() {
        defer wg.Done()
        for {
            select {
            case <-stop:
                return
            default:
                data, err := os.ReadFile(path)
                if err != nil { continue }
                var st State
                if err := json.Unmarshal(data, &st); err != nil {
                    t.Errorf("readback parsed as invalid JSON: %v\ndata: %s", err, data)
                }
            }
        }
    }()
    wg.Wait()
}
```

The SIGKILL-mid-write fault-injection test is **Phase 4 scope** (STATE-04) and intentionally not in Phase 1.

### go:embed + Vite + MIME registration

**`internal/api/static.go`** (verbatim recommendation):

```go
package api

import (
    "embed"
    "io/fs"
    "mime"
    "net/http"
    "path"
    "strings"
)

//go:embed all:dist
var distFS embed.FS

func init() {
    // Distroless minimal envs may lack a system mime.types file. Register the
    // four extensions we actually serve so the static handler emits correct
    // Content-Type headers. See research/PITFALLS.md Pitfall 8.
    _ = mime.AddExtensionType(".js", "application/javascript; charset=utf-8")
    _ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
    _ = mime.AddExtensionType(".svg", "image/svg+xml")
    _ = mime.AddExtensionType(".json", "application/json; charset=utf-8")
}

// staticHandler serves the embedded Svelte bundle.
// - /assets/* — strict; 404 on miss; long immutable Cache-Control.
// - /          — index.html with no-cache.
// - everything else — 404.
//
// We do NOT fall back to index.html for /assets/* (research/PITFALLS.md
// Pitfall 8 — that's the MIME-type trap that breaks post-upgrade caches).
func newStaticHandler() http.Handler {
    sub, err := fs.Sub(distFS, "dist")
    if err != nil {
        // The //go:embed directive failed — this is a build-time bug, panic
        // is the right response (we'd never start up healthily otherwise).
        panic(err)
    }
    fileServer := http.FileServerFS(sub)
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        clean := path.Clean(r.URL.Path)
        switch {
        case strings.HasPrefix(clean, "/assets/"):
            // Strict static serve. http.FileServerFS returns 404 on miss
            // and sets Content-Type via mime.TypeByExtension.
            w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
            fileServer.ServeHTTP(w, r)
        case clean == "/" || clean == "/index.html":
            w.Header().Set("Cache-Control", "no-cache")
            r.URL.Path = "/index.html"
            fileServer.ServeHTTP(w, r)
        default:
            // No SPA-fallback to index.html — we have no client-side router.
            http.NotFound(w, r)
        }
    })
}
```

**`ui/vite.config.ts`** snippet — the load-bearing line is `build.outDir`:

```typescript
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [svelte(), tailwindcss()],
  base: '/',
  build: {
    outDir: '../internal/api/dist',
    emptyOutDir: true,
  },
});
```

`base: '/'` matches `Vite base config option` per ARCHITECTURE.md "Embedding the Bundle" section. `emptyOutDir: true` makes Vite wipe the target dir before each build (so stale hashed assets don't accumulate).

### Playwright globalSetup pattern

**`e2e/playwright.config.ts`** (verbatim):

```typescript
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  globalSetup: './global-setup.ts',
  globalTeardown: './global-teardown.ts',
  workers: 1,             // serialise — we share one docker stack across tests
  fullyParallel: false,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:8080',
    trace: 'on-first-retry',
  },
});
```

**`e2e/global-setup.ts`** (verbatim):

```typescript
import { execSync } from 'node:child_process';
import { setTimeout as sleep } from 'node:timers/promises';

const COMPOSE = ['docker', 'compose', '-f', 'compose.test.yml'];

async function waitForHealth(url: string, timeoutMs: number) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url);
      if (res.ok) return;
    } catch { /* still starting */ }
    await sleep(500);
  }
  throw new Error(`Healthcheck never returned 200: ${url}`);
}

export default async function globalSetup() {
  // Bring the stack up and block until every service's healthcheck passes.
  execSync([...COMPOSE, 'up', '-d', '--wait'].join(' '), { stdio: 'inherit' });

  // Push the initial :latest manifest into zot so the stub watched-container
  // resolves. The exact command shape is documented in
  // research/PHASE-01/manifest-push fixture section.
  execSync(
    'echo "phase-1-initial" > /tmp/phase1-payload.txt && ' +
    'oras push --plain-http localhost:5000/centroid-is/stub:latest /tmp/phase1-payload.txt:text/plain',
    { stdio: 'inherit' },
  );

  // Belt-and-braces: even though --wait blocks on the hmi-update healthcheck,
  // also confirm /healthz from the host network.
  await waitForHealth('http://localhost:8080/healthz', 30_000);
}
```

**`e2e/global-teardown.ts`** (verbatim):

```typescript
import { execSync } from 'node:child_process';

export default async function globalTeardown() {
  execSync('docker compose -f compose.test.yml down -v --remove-orphans', {
    stdio: 'inherit',
  });
}
```

**Host port mapping:** the simplest pattern is *static* port mapping in `compose.test.yml` (`ports: ["8080:8080"]` for hmi-update, `["5000:5000"]` for zot). Dynamic mapping is more correct for parallel test runs on the same host, but Phase 1's `workers: 1` means we never run two test stacks at once. Stick with static mapping for clarity.

`[VERIFIED: playwright.dev/docs/test-global-setup-teardown 2026-05-13]`

### zot configuration for Phase 1

**`e2e/zot-config.json`** — minimal, anonymous-open, in-memory-ish:

```json
{
  "distSpecVersion": "1.1.1",
  "storage": {
    "rootDirectory": "/tmp/zot",
    "dedupe": false,
    "gc": false
  },
  "http": {
    "address": "0.0.0.0",
    "port": "5000"
  },
  "log": {
    "level": "error"
  }
}
```

**Why these values:**
- `rootDirectory: /tmp/zot` — point at the container's tmpfs / overlay. Per-test reset comes from `docker compose down -v`. `[VERIFIED: github.com/project-zot/zot examples/config-minimal.json 2026-05-13]`
- `dedupe: false` and `gc: false` — disable to avoid background work and to make pushes deterministic for tests. `[VERIFIED: zotregistry.dev/v2.1.4/admin-guide/admin-configuration/ confirms keys exist under storage 2026-05-13]`
- `http.address: 0.0.0.0` — bind on all interfaces inside the container so docker-compose port mapping reaches the listener.
- **No `accessControl` and no `http.auth`** — without either section, zot's minimal example registries allow anonymous pull AND push. This is the de-facto pattern; the smoke test verifies it by `oras push` succeeding without credentials. `[ASSUMED — official docs don't state default explicitly; verified empirically by official example config-minimal.json shipping no auth keys and being labeled "minimal." If `oras push` fails with 401 in the smoke test, add `"accessControl": {"repositories": {"**": {"defaultPolicy": ["read","create"], "anonymousPolicy": ["read","create"]}}}` to the config.]`

**OCI image index support:** zot v2.1+ supports OCI image index push via `oras push --artifact-type` and via `oras manifest push` for full control. Phase 1 only needs single-arch push for the smoke test; Phase 3 will exercise both image index and direct manifest shapes. `[CITED: oras.land/docs/commands/oras_push/ and oras.land/docs/how_to_guides/pushing_and_pulling/]`

**Image tag to use:** `ghcr.io/project-zot/zot-linux-amd64:latest` (or pin to `:v2.1.x` for stability — Phase 1 can stay on `:latest` and Phase 8 hardens to a pinned digest).

### Compose test stack

**`e2e/compose.test.yml`** (verbatim Phase 1 minimum):

```yaml
services:
  zot:
    image: ghcr.io/project-zot/zot-linux-amd64:latest
    ports:
      - "5000:5000"
    volumes:
      - ./zot-config.json:/etc/zot/config.json:ro
    command: ["serve", "/etc/zot/config.json"]
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:5000/v2/"]
      interval: 2s
      timeout: 2s
      retries: 15
      start_period: 2s

  stub-watched-container:
    image: busybox:latest
    command: ["sh", "-c", "while true; do sleep 30; done"]
    labels:
      hmi-update.watch: "true"
    healthcheck:
      test: ["CMD", "true"]
      interval: 5s
      retries: 3

  hmi-update:
    build:
      context: ..
      dockerfile: Dockerfile
    ports:
      - "8080:8080"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./compose.test.yml:/host/docker-compose.yml:ro
      - hmi-state:/state
    environment:
      - HMI_UPDATE_STATE_PATH=/state/hmi_update_state.json
      - HMI_UPDATE_LOG_LEVEL=info
    depends_on:
      zot:
        condition: service_healthy
      stub-watched-container:
        condition: service_started
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost:8080/healthz"]
      interval: 2s
      timeout: 2s
      retries: 15
      start_period: 5s

volumes:
  hmi-state:
```

**Notes:**
- `wget --spider` works in busybox; `curl` does not exist in distroless. For the `hmi-update` container's healthcheck, **`wget` is not in distroless either** — so the healthcheck either has to be done from the host side (`docker compose up -d --wait` won't help) or we bake a tiny stub into the image. **Simpler:** drop the `hmi-update` healthcheck from the compose file and rely on `global-setup.ts`'s `waitForHealth` poll to `localhost:8080/healthz`. **Update the snippet above to remove the `hmi-update.healthcheck:` block before shipping.** This is a known distroless limitation; Phase 7 may revisit by switching to `cc-debian12`.
- Or: put the healthcheck in the Dockerfile as a `HEALTHCHECK` instruction using the binary itself (`HEALTHCHECK CMD ["/hmi-update", "healthcheck"]`) — i.e., a flag the main binary handles that exits 0 if it can reach its own listener. This is the canonical distroless workaround. Add `--healthcheck` flag handling to `cmd/hmi-update/main.go`.
- `volumes: hmi-state` is a named volume; `docker compose down -v` wipes it between runs.
- `:latest` tag on `zot-linux-amd64` is fine for Phase 1; pin to a versioned tag in Phase 8.

### Manifest-push fixture

**`oras push` command shape for flipping `:latest`:**

```bash
echo "phase-N-payload-$(date +%s)" > /tmp/payload.txt
oras push --plain-http \
  localhost:5000/centroid-is/stub:latest \
  /tmp/payload.txt:text/plain
```

- `--plain-http` is essential — zot is on HTTP, not HTTPS, in the test stack.
- The new payload has a new digest because the file bytes change. The tag `:latest` mutates to point at the new manifest. `[VERIFIED: oras.land/docs/commands/oras_push/ 2026-05-13]`

**From Playwright (inside a fixture or test):**

```typescript
// e2e/fixtures/push-image.ts
import { execSync } from 'node:child_process';
import { writeFileSync } from 'node:fs';

export function pushFreshManifest(repo: string): string {
  const file = `/tmp/payload-${Date.now()}-${Math.random().toString(36).slice(2)}.txt`;
  writeFileSync(file, `payload-${Date.now()}`);
  const out = execSync(
    `oras push --plain-http localhost:5000/${repo}:latest ${file}:text/plain`,
    { encoding: 'utf8' },
  );
  // oras prints "Pushed [registry] localhost:5000/...  Digest: sha256:..."
  const match = out.match(/Digest:\s+(sha256:[0-9a-f]+)/);
  if (!match) throw new Error(`oras output did not contain a Digest: ${out}`);
  return match[1];
}
```

**Fallback Go helper (if oras flakes in CI):**

```go
// e2e/fakereg/pushmanifest/main.go
package main

import (
    "fmt"
    "os"

    "github.com/google/go-containerregistry/pkg/crane"
)

func main() {
    if len(os.Args) < 2 {
        fmt.Fprintln(os.Stderr, "usage: pushmanifest <repo:tag>")
        os.Exit(2)
    }
    ref := os.Args[1] // e.g. localhost:5000/centroid-is/stub:latest
    // Pull a tiny base image, append a fresh layer with a timestamp,
    // and push as :latest. crane handles the OCI Distribution dance.
    img, err := crane.Pull("alpine:3.20")
    if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
    if err := crane.Push(img, ref, crane.Insecure); err != nil {
        fmt.Fprintln(os.Stderr, err); os.Exit(1)
    }
}
```

Switch from oras → Go helper happens by changing one execSync call in `push-image.ts`. Phase 1 ships oras-first per CONTEXT.md decision.

### Multi-stage Dockerfile (dev-grade for Phase 1)

**`Dockerfile`** at repo root:

```dockerfile
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

# Build a static binary with embedded assets
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" \
    -o /out/hmi-update ./cmd/hmi-update

# ---- Stage 3: runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-builder /out/hmi-update /hmi-update
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/hmi-update"]
```

**Notes:**
- Stage 1 must create `/src/internal/api/dist/` before `npm run build` because Vite's `outDir: '../internal/api/dist'` resolves relative to `ui/vite.config.ts`'s location.
- Stage 2's `COPY --from=ui-builder /src/internal/api/dist /src/internal/api/dist` makes the `//go:embed all:dist` directive succeed.
- `-trimpath` removes absolute paths from the binary; `-s -w` strips debug info.
- Phase 7 owns proper size verification (<30 MB) and the `cc-debian12` fallback decision. Phase 1's Dockerfile is *correct*, not *optimal*.
- "Dev target" pattern: a single Dockerfile is fine for Phase 1. Phase 7 may add a `--target=dev` stage for live-reload, but that's deferred.

`[VERIFIED: distroless README pattern + STACK.md docker section 2026-05-13]`

### GitHub Actions CI baseline

**`.github/workflows/ci.yml`** (verbatim Phase 1 minimum):

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

jobs:
  go:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
          cache: true

      - name: go vet
        run: go vet ./...

      - name: go test
        run: go test ./...

      - name: install tygo
        run: go install github.com/gzuidhof/tygo@latest

      - name: check types are up to date
        run: make check-types

  ui:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: 'npm'
          cache-dependency-path: ui/package-lock.json

      - name: install
        run: npm --prefix ui ci

      - name: build
        run: npm --prefix ui run build

  e2e:
    runs-on: ubuntu-24.04
    needs: [go, ui]
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'

      - uses: actions/setup-node@v4
        with:
          node-version: '22'

      - name: install oras
        run: |
          curl -L https://github.com/oras-project/oras/releases/latest/download/oras_linux_amd64.tar.gz | tar -xz oras
          sudo mv oras /usr/local/bin/

      - name: install playwright browsers
        run: |
          cd e2e
          npm ci
          npx playwright install --with-deps chromium

      - name: run e2e
        run: make e2e

      - name: upload playwright report on failure
        if: failure()
        uses: actions/upload-artifact@v4
        with:
          name: playwright-report
          path: e2e/playwright-report/
          retention-days: 7
```

**Notes:**
- `ubuntu-24.04` runners have Docker + Compose v2 preinstalled (per STACK.md). `[CITED: STACK.md]`
- The `oras` CLI is fetched from the GitHub release. Pin to a specific version in Phase 8.
- **No image build / publish job** — Phase 8 owns that. The `e2e` job builds the image locally via `docker compose build` (implicitly through `docker compose up`) but doesn't push.
- The `needs: [go, ui]` gate is intentional — fail fast on lint/test/build before paying the e2e cost.

`[CITED: STACK.md CI/CD section]`

### Seven-column empty-table Svelte 5 component

**`ui/src/lib/Table.svelte`** (verbatim, Svelte 5 runes API):

```svelte
<script lang="ts">
  import type { Container } from './types';

  type Props = { containers: Container[] };
  let { containers }: Props = $props();
</script>

<table class="w-full border border-zinc-200 rounded-md overflow-hidden">
  <thead class="bg-zinc-100 border-b border-zinc-200">
    <tr>
      <th class="px-4 py-2 text-left text-sm font-semibold text-zinc-700">container</th>
      <th class="px-4 py-2 text-left text-sm font-semibold text-zinc-700">image:tag</th>
      <th class="px-4 py-2 text-left text-sm font-semibold text-zinc-700 font-mono text-xs">current digest</th>
      <th class="px-4 py-2 text-left text-sm font-semibold text-zinc-700 font-mono text-xs">available digest</th>
      <th class="px-4 py-2 text-left text-sm font-semibold text-zinc-700 font-mono text-xs">previous digest</th>
      <th class="px-4 py-2 text-left text-sm font-semibold text-zinc-700">status</th>
      <th class="px-4 py-2 text-left text-sm font-semibold text-zinc-700">actions</th>
    </tr>
  </thead>
  <tbody>
    {#if containers.length === 0}
      <tr>
        <td colspan="7" class="px-4 py-8 text-center text-sm text-zinc-500 italic">
          <p class="font-medium not-italic text-zinc-700 mb-2">No watched containers yet</p>
          <p>Label a service in your compose file with <code class="font-mono text-xs bg-zinc-100 px-1 py-0.5 rounded">hmi-update.watch=true</code> and it will appear here on the next poll.</p>
        </td>
      </tr>
    {:else}
      {#each containers as c (c.service)}
        <tr>
          <td class="px-4 py-2 text-sm">{c.service}</td>
          <td class="px-4 py-2 text-sm">{c.image}:{c.tag}</td>
          <td class="px-4 py-2 font-mono text-xs">{c.current_digest ?? ''}</td>
          <td class="px-4 py-2 font-mono text-xs">{c.update_available ? '...' : ''}</td>
          <td class="px-4 py-2 font-mono text-xs">{c.previous_digest ?? ''}</td>
          <td class="px-4 py-2 text-sm">{c.update_available ? 'update-available' : 'up-to-date'}</td>
          <td class="px-4 py-2 text-sm"><!-- Phase 5 --></td>
        </tr>
      {/each}
    {/if}
  </tbody>
</table>
```

**`ui/src/App.svelte`** (verbatim):

```svelte
<script lang="ts">
  import { onMount } from 'svelte';
  import Table from './lib/Table.svelte';
  import type { State } from './lib/types';

  let containers = $state<State['containers'] extends infer T ? (T extends Record<string, infer C> ? C[] : never) : never>([]);

  onMount(async () => {
    try {
      const res = await fetch('/api/state');
      if (res.ok) {
        const s: State = await res.json();
        containers = Object.values(s.containers ?? {});
      }
    } catch {
      // empty list is fine for Phase 1
    }
  });
</script>

<header class="bg-zinc-100 border-b border-zinc-200 px-6 py-4">
  <div class="max-w-screen-xl mx-auto flex items-center justify-between">
    <h1 class="text-2xl font-semibold tracking-tight">hmi-update</h1>
  </div>
</header>

<main class="max-w-screen-xl mx-auto px-6 py-8">
  <Table containers={containers} />
</main>
```

**`ui/src/app.css`** (Tailwind v4 entry):

```css
@import "tailwindcss";

@theme {
  /* Reserved for Phase 5 — semantic state colors. */
}

body {
  font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
}
```

The page imports `Container` and `State` from the generated `./lib/types.d.ts` (per UI-SPEC.md), satisfying the "tygo proves the codegen pipeline end-to-end" requirement.

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `http.FileServer(http.FS(sub))` | `http.FileServerFS(sub)` | Go 1.22 | Cleaner API; ARCHITECTURE.md uses the new form |
| `wait-for-it.sh` polling scripts | `docker compose up -d --wait` + service `healthcheck:` blocks | Compose v2.20+ | Native; ~89% reduction in flake per docker/compose-healthcheck guidance |
| `chi` / `gorilla/mux` for small APIs | stdlib `net/http` ServeMux with method-path patterns | Go 1.22 | One fewer dep; same expressiveness for <20 routes |
| Hand-written TS types matching Go structs | `tygo generate` from `internal/api/types.go` | tygo stable since 2023 | Drift is now compile-time visible |
| `os.WriteFile` + `os.Rename` + manual fsync | `renameio/v2.WriteFile` + (caller-side) dir fsync wrapper | renameio v2 (2023) | One library call; but **caller still owns dir fsync** (correction above) |

**Deprecated for this phase:**
- `tailwind.config.js` — Tailwind v4 moved to config-in-CSS via `@theme { ... }`. `[CITED: STACK.md]`
- `postcss-import` and `autoprefixer` — replaced by the `@tailwindcss/vite` plugin.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | zot with no `accessControl` and no `http.auth` section allows anonymous pull AND push | zot configuration | LOW — verified empirically by the official `examples/config-minimal.json` shipping no auth keys and being labeled minimal. If wrong, smoke test's `oras push` fails fast with a 401 and we add an `accessControl` block. |
| A2 | Distroless `static-debian12:nonroot` does not include `wget` or `curl`, so the `hmi-update` compose healthcheck must use `--healthcheck` flag on the binary itself | Compose test stack | LOW — well-documented distroless limitation. If wrong, the simpler `wget --spider` healthcheck works. |
| A3 | `oras push --plain-http` against zot v2.1+ on HTTP works without auth headers | Manifest-push fixture | LOW — standard oras behavior against an open HTTP registry. |
| A4 | tygo has no built-in `--check` / `--verify` flag and `git diff --exit-code` is the canonical CI pattern | tygo configuration | LOW — README and CHANGELOG mention no such flag as of 2026-05-13 [VERIFIED]. |
| A5 | Phase 1 can accept the host-power-loss durability window without dir fsync, OR the recommended wrapper adds it cheaply | renameio/v2 directory-fsync correction | MEDIUM — this is a real correction to upstream research. Planner should pick a side and document the rationale in code. Recommendation: ship Option 2 (wrapper). |
| A6 | `docker compose up -d --wait` with a service `healthcheck:` block that probes `/v2/` on zot is sufficient to gate Playwright globalSetup on zot being ready for `oras push` | Playwright globalSetup pattern | LOW — `wget --spider http://localhost:5000/v2/` returns 200 only when zot is serving. Standard pattern. |
| A7 | `ghcr.io/project-zot/zot-linux-amd64:latest` is the right image path for the 2026 stable line | zot configuration | LOW — this is the documented image per project-zot README. Phase 8 should pin to a specific version. |
| A8 | The `:latest` mutability and OCI image index push support on zot v2.1+ are stable and used by tests in Phase 3 | zot configuration / manifest fixture | LOW — both are core OCI Distribution v1.1 features and zot is conformant per its README and v2.1 release notes. Phase 2/3 will exercise both shapes; if either fails, fall back to a Go helper using `crane`. |

**Material assumption flagged for discuss-phase / user confirmation:** A5 (renameio dir-fsync wrapper Yes/No). The planner should pick one and include the rationale in a code comment so reviewers don't try to "fix" it later.

## Open Questions

1. **Does the `e2e/compose.test.yml` need a `docker-compose.yml`-on-host bind mount for Phase 1?**
   - What we know: Phase 1 doesn't exercise compose-as-actuator (that's Phase 4 ACT-01). The bind mount `./compose.test.yml:/host/docker-compose.yml:ro` is in the snippet above for *forward compatibility* — Phase 2's DOCK-02 (compose reader) needs it.
   - Recommendation: include the bind mount in Phase 1 even though no code reads it. Phase 2 doesn't have to re-edit compose.test.yml.

2. **Should Phase 1 include a `golangci-lint` config file or defer to Phase 8?**
   - What we know: CONTEXT.md explicitly puts linter config in "Claude's Discretion." CI-01 (Phase 8) calls for full lint + tygo diff + unit + frontend + image build + e2e + publish.
   - Recommendation: ship a minimal `.golangci.yml` in Phase 1 with sensible defaults (`gofmt`, `govet`, `staticcheck`) so the workflow shape is right from day one. Phase 8 hardens it.

3. **Does the `cmd/hmi-update/main.go` need a `--healthcheck` flag for the distroless HEALTHCHECK pattern?**
   - What we know: Distroless lacks `wget` and `curl`. Compose-side healthchecks require *something* that can probe HTTP. Options: (a) drop compose healthcheck on `hmi-update`, rely on globalSetup polling; (b) add a `--healthcheck` flag that probes its own listener and exits.
   - Recommendation: do (a) for Phase 1 to keep the binary small. Phase 7 can add the flag if the operations team wants Docker-native health.

4. **What's the right `.gitignore` from day one?**
   - Need at minimum: `bin/`, `internal/api/dist/`, `ui/dist/`, `ui/node_modules/`, `e2e/node_modules/`, `e2e/playwright-report/`, `e2e/test-results/`, `.DS_Store`. Phase 1 plan should include a task to create this.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|-------------|-----------|---------|----------|
| Go | Build the binary, run unit tests | ✓ | 1.26.0 darwin/arm64 | — |
| Node.js | Build Svelte bundle; run Playwright | ✓ | v25.6.1 | — (note: Phase 1 CI uses Node 22; local 25 is fine for dev) |
| npm / npx | Vite, Playwright init | ✓ | npm via npx 11.9.0 | — |
| Docker | Test stack | ✓ | 29.4.1 | — |
| Docker Compose v2 | Test stack lifecycle | ✓ (bundled with Docker 29) | (v2 via plugin) | — |
| Make (GNU) | Build orchestration | ✓ | 3.81 | — |
| Git | Version control, `git diff --exit-code` for tygo CI | ✓ | available | — |
| oras CLI | Push manifests in Playwright fixture | ✗ | — | Install via `brew install oras` locally; download from GitHub releases in CI; Go helper using `crane.Push()` if oras flakes |
| tygo CLI | Type generation | ✗ | — | `go install github.com/gzuidhof/tygo@latest` (run as part of Phase 1 onboarding and CI) |
| crane CLI | Optional alt to oras | ✗ | — | Not needed for Phase 1; Phase 3 may add |
| Playwright browsers (chromium) | E2E tests | ✗ | — | `npx playwright install --with-deps chromium` (run during `cd e2e && npm install` or first `make e2e`) |

**Missing dependencies with no fallback:** None — every missing tool has a documented install path.

**Missing dependencies with fallback:**
- **oras CLI** — install in CI workflow (snippet above). Locally, `brew install oras` on macOS (the dev environment is `darwin/arm64`). If CI/local install ever flakes, drop in the 30-LOC Go helper using `crane.Push()`.
- **tygo CLI** — `go install` it. Include the install step in CI (workflow snippet above does this) and document in README.
- **Playwright chromium** — `npx playwright install` is idempotent and ~150 MB cached in `~/.cache/ms-playwright/`.

## Validation Architecture

> nyquist_validation is not set in `.planning/config.json` (file doesn't exist at time of research). Treating as enabled (default behavior per agent spec).

### Test Framework

| Property | Value |
|----------|-------|
| Framework (Go) | `testing` stdlib + Go 1.26 |
| Framework (E2E) | `@playwright/test` v1.60+ |
| Config file (Go) | none — stdlib `go test ./...` |
| Config file (E2E) | `e2e/playwright.config.ts` (created in Wave 0) |
| Quick run command (Go) | `go test ./internal/state/... -count=1` |
| Quick run command (E2E) | `cd e2e && npx playwright test --grep smoke` |
| Full suite command | `make test && make e2e` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| FOUND-01 | Repo scaffolding present | structure check | `test -d cmd/hmi-update && test -d internal/state && test -f go.mod` | ❌ Wave 0 — could be a shell test in CI |
| FOUND-02 | Atomic state persistence + parse-old-or-new under contention | unit | `go test ./internal/state/... -run TestPersist -count=10` | ❌ Wave 0 (`internal/state/persist_test.go`) |
| FOUND-03 | `/healthz` 200, `/api/state` valid JSON | smoke (Playwright) | `cd e2e && npx playwright test smoke.spec.ts` | ❌ Wave 0 (`e2e/tests/smoke.spec.ts`) |
| FOUND-04 | Svelte shell embedded + served + correct MIME | smoke (Playwright) | same as FOUND-03 (assertions on /, /assets/*.js Content-Type) | ❌ Wave 0 |
| FOUND-05 | Test stack reaches healthy via `up -d --wait` | infra (Playwright globalSetup) | `cd e2e && npx playwright test` (any test triggers it) | ❌ Wave 0 (`compose.test.yml`, `zot-config.json`) |
| FOUND-06 | globalSetup + first smoke assertions | smoke (Playwright) | `cd e2e && npx playwright test` | ❌ Wave 0 |
| FOUND-07 | `oras push` flips `:latest` mid-test | smoke (Playwright) — Phase 1 only verifies the *initial push* succeeds; "mid-test flip" is exercised in Phase 3 | manual: `oras push --plain-http localhost:5000/centroid-is/stub:latest /tmp/x.txt:text/plain` | ❌ Wave 0 (initial push verified by `pushFreshManifest` helper) |
| FOUND-08 | `tygo generate` produces matching `types.d.ts`; CI fails on diff | unit | `make check-types` (≡ `make types && git diff --exit-code ui/src/lib/types.d.ts`) | ❌ Wave 0 (`tygo.yaml`, `internal/api/types.go`, Makefile) |
| STATE-01 | All state in single JSON file | unit / structural | `grep -rn "sqlite\|mongo\|redis" --include='*.go' . | wc -l` returns 0 | optional one-liner |
| STATE-02 | Atomic writes via `renameio/v2` | unit | `go test ./internal/state/... -count=10` (contention test from FOUND-02) | ❌ Wave 0 |
| STATE-03 | `version: 1` schema; boot reads JSON | unit | `go test ./internal/state/... -run TestLoadAndPersist` | ❌ Wave 0 |

### Sampling Rate

- **Per task commit:** `go test ./internal/state/...` (fast, <2 s)
- **Per wave merge:** `make test && cd e2e && npx playwright test --grep smoke`
- **Phase gate:** `make test && make e2e` green; manual smoke on local Docker stack confirms `/healthz` 200 and empty table at `/` before `/gsd-verify-work`

### Wave 0 Gaps

- [ ] `internal/state/persist_test.go` — covers FOUND-02, STATE-02, STATE-03 (atomic write under contention; load-and-persist round trip; version: 1 schema)
- [ ] `internal/api/server_test.go` — light unit cover for `/healthz` and `/api/state` handlers (fast, no Playwright)
- [ ] `e2e/tests/smoke.spec.ts` — Phase 1 smoke (FOUND-03/04/05/06)
- [ ] `e2e/playwright.config.ts`, `e2e/global-setup.ts`, `e2e/global-teardown.ts` — Playwright infra (FOUND-06)
- [ ] `e2e/compose.test.yml`, `e2e/zot-config.json` — test stack (FOUND-05)
- [ ] `e2e/fixtures/push-image.ts` — oras helper (FOUND-07)
- [ ] `tygo.yaml`, `internal/api/types.go`, `Makefile` targets `types` and `check-types` — type contract (FOUND-08)
- [ ] `.github/workflows/ci.yml` — CI minimal pipeline

*(None of these exist yet — the entire phase is greenfield.)*

## Security Domain

> `security_enforcement` is not explicitly set; treating as enabled per agent default.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | LAN-only, unauthenticated v1 per N5 / DEPLOY-07. v2 may add. |
| V3 Session Management | no | No sessions in v1 |
| V4 Access Control | partial | Server-enforced safety labels (`allow-update=false`) — but those are Phase 4. Phase 1 has no actions to gate. |
| V5 Input Validation | yes — minimal in Phase 1 | Phase 1's `/healthz` and `/api/state` take no inputs. Service-name validation regex is Phase 3/4 scope. |
| V6 Cryptography | no | No crypto in v1 (no auth, no encrypted storage) |
| V7 Error Handling | yes | Phase 1: `/healthz` returns 503 with a remediation hint, not a stack trace. Don't echo internal paths in `/api/state` error responses. |
| V8 Data Protection | partial | State file is `0o644` on host — readable by host group/world. Acceptable per N5. Document. |
| V9 Communication | no | LAN-only HTTP. No TLS in v1. |
| V12 File and Resource | partial | `//go:embed` bundle is in-binary; no path traversal possible. `/assets/*` is a strict prefix served via `http.FileServerFS` — Go stdlib handles path normalization. |
| V14 Configuration | yes | Phase 1: `HMI_UPDATE_STATE_PATH` and `HMI_UPDATE_LOG_LEVEL` env vars validated at boot. |

### Known Threat Patterns for {Phase 1 surface}

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Path traversal via `/assets/../../etc/passwd` | Tampering | `http.FileServerFS` calls `path.Clean` and rejects `..` traversal; no custom path concatenation in `static.go` |
| Slow-loris on `/api/state` | DoS | stdlib `http.Server` has `ReadTimeout`/`WriteTimeout` — set both to 10s in `server.go` |
| State file disk-fill via writes from many concurrent updates | DoS | Phase 1 has no operator-triggered writes (Phase 4 adds them); state file size for the v1 schema is bounded at ~10 KB |
| Embedded-bundle content leak via `index.html` injection | XSS | Vite bundle is build-time only; no user-controlled HTML reaches the response |
| Bearer token leak in logs | Information Disclosure | OBS-04 (Phase 3) audits this. Phase 1 logs no secrets because there are no secrets to log. |

**Phase 1 security checklist:**
- [ ] HTTP server `ReadTimeout` and `WriteTimeout` set
- [ ] `/healthz` 503 response does not echo internal file paths
- [ ] No reflected user input in any Phase 1 endpoint
- [ ] State file written with `0o644` (locked by CONTEXT.md / STACK.md)
- [ ] No `os.Exec`, no shell, no template rendering — nothing to inject into

## Sources

### Primary (HIGH confidence)
- `.planning/research/STACK.md` — locked technology choices and versions (validated 2026-05-07–2026-05-13)
- `.planning/research/ARCHITECTURE.md` — locked patterns (in-memory + RWMutex, atomic write, embed strategy, MIME registration)
- `.planning/research/PITFALLS.md` — Pitfalls 7 (atomic writes) and 8 (embed + MIME + cache) directly applicable
- `.planning/phases/01-walking-skeleton-test-harness/01-CONTEXT.md` — user-locked decisions
- `.planning/phases/01-walking-skeleton-test-harness/01-UI-SPEC.md` — verbatim empty-state copy, seven-column header, MIME contract, cache contract
- [renameio v2 source — github.com/google/renameio tempfile.go](https://github.com/google/renameio/blob/master/tempfile.go) — verified `CloseAtomicallyReplace` does not fsync parent directory
- [renameio v2 — pkg.go.dev](https://pkg.go.dev/github.com/google/renameio/v2) — verified `WriteFile` signature
- [renameio issue #11 — github.com/google/renameio/issues/11](https://github.com/google/renameio/issues/11) — confirms dir-fsync is caller responsibility
- [tygo README — github.com/gzuidhof/tygo](https://github.com/gzuidhof/tygo) — verified `tygo.yaml` keys and absence of `--check` flag
- [zot examples/config-minimal.json — github.com/project-zot/zot](https://github.com/project-zot/zot/blob/main/examples/config-minimal.json) — verified minimal config shape
- [oras push docs — oras.land/docs/commands/oras_push](https://oras.land/docs/commands/oras_push/) — verified `--plain-http` flag
- [Playwright globalSetup docs — playwright.dev/docs/test-global-setup-teardown](https://playwright.dev/docs/test-global-setup-teardown) — verified `globalSetup` and `globalTeardown` patterns
- [Distroless static-debian12 — github.com/GoogleContainerTools/distroless](https://github.com/GoogleContainerTools/distroless) — verified `:nonroot` (UID 65532) and lack of `wget`/`curl`

### Secondary (MEDIUM confidence)
- [Tushar Choudhari, "Embed Vite app in a Go Binary"](https://www.tushar.ch/writing/embed-vite-app-in-go-binary) — Vite `outDir` → Go `//go:embed` pattern
- [Michael Stapelberg, "Atomically writing files in Go"](https://michael.stapelberg.ch/posts/2017-01-28-golang_atomically_writing/) — temp+rename+dirsync canonical pattern
- [zot configuration docs — zotregistry.dev](https://zotregistry.dev/v2.1.10/articles/authn-authz/) — anonymous policy structure (does not state default-no-config behavior explicitly)

### Tertiary (LOW confidence)
- None — every assumed claim is tagged in the Assumptions Log with risk assessment.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — entirely inherited from STACK.md (already verified by upstream research)
- Architecture patterns: HIGH — inherited from ARCHITECTURE.md; one correction (renameio dir-fsync) verified in source
- Pitfalls: HIGH — Pitfalls 7 and 8 are well-documented; the renameio correction has been verified in upstream source
- tygo / zot / oras / Playwright globalSetup specifics: HIGH — verified against current 2026 official docs
- Repo skeleton ordering: HIGH — derived from first principles + verified by `go mod`, Vite `outDir`, `//go:embed` semantics
- Renameio dir-fsync correction: HIGH — verified in github.com/google/renameio source

**Research date:** 2026-05-13
**Valid until:** 2026-06-13 (30 days; nothing in Phase 1 is on a fast-moving release cadence)
