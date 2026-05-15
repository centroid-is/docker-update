# Phase 4: Update / Rollback / Force-pull Actions, Safety & State Persistence - Research

**Researched:** 2026-05-15
**Domain:** Per-container action orchestration (Docker pull/tag + Compose recreate), per-service mutex, verify-after-recreate, SIGKILL-resistant state persistence, server-enforced safety labels
**Confidence:** HIGH (all locked decisions in 04-CONTEXT.md are implementation-level; verified against existing Phase 1–3 code, moby/moby/client v0.4.1 surface, Go stdlib docs, and Docker Compose v2 manuals)

## Summary

Phase 4 is the load-bearing differentiator of the project. The architectural envelope is fully constrained by 04-CONTEXT.md (all four grey areas accepted as recommended), Phase 3's single-consumer channel pattern, and Phase 2's docker/compose facade layout. **No new architectural decisions remain** — the research below fills in implementation-level detail for every grey area the planner needs.

Concretely:

1. **Compose runner shape:** `exec.CommandContext` with argv-separated service name (Pitfall 13 prevention), per-call stdout/stderr capture into structured slog, exit-code propagation via `*exec.ExitError.ExitCode()`, and `cmd.WaitDelay = 10*time.Second` to grant SIGTERM→SIGKILL grace on ctx cancellation.
2. **Image pull verify:** Two viable paths. Recommended path is **parsing the `aux` field from the moby v0.4.1 `ImagePullResponse.JSONMessages` iterator** (the SDK extracts `Docker-Content-Digest` from the registry response and surfaces it in the stream). Fallback path is **adding `ImageInspect` to the docker facade** for `RepoDigests[0]` verification after the pull stream completes. The planner picks one — research recommends the JSONMessages path for purity (no new facade method) but flags that the fallback closes the same gap if SDK shape proves brittle.
3. **Per-service mutex map:** `sync.Mutex.TryLock` (Go 1.18+) inside a struct-level `sync.RWMutex` for map access. Double-checked locking on entry creation. `lockService` returns an `unlock func()` closure for `defer`-friendly call sites.
4. **Verify-after-recreate poll loop:** `time.NewTicker(1*time.Second)`, fail-fast on any tick's anomaly, require 15 consecutive successful ticks (default), opt-in 60s healthcheck window. Ctx cancellation returns a separate `ErrVerifyCanceled` sentinel (distinct from `ErrVerifyFailed`).
5. **SIGKILL fault-injection harness:** Parent-child `os/exec` pattern with build-tagged helper binary at `cmd/sigkillhelper/main.go`. Parent test (`internal/state/store_sigkill_test.go`) gated by `//go:build sigkill_test` so default `go test ./...` stays fast. 100 iterations × randomized 1–50ms delays.
6. **Self-protection / safety-label / service-name middleware:** Three stacked HTTP middleware layers in `internal/actions/middleware.go`. All read from `state.Store.Get()` (cached, no docker.Inspect). Service-name regex `^[a-zA-Z0-9._-]+$` compiled once at boot.
7. **Action endpoint slog schema:** Dotted event names (`action.start`, `action.phase`, `action.complete`, `action.verify_failed`, `action.compose_failed`, `action.pull_failed`) with standard fields (`service`, `action`, `before`, `after`, `exit_code`, `duration_ms`). Compose stderr is included as `stderr_snippet` (last 4096 bytes — full content captured to slog but truncated to avoid pathological size).
8. **e2e fixtures:** 8 new Playwright specs + `disconnect-network.ts` fixture for offline-rollback (ACT-04 Acceptance criterion 4). Plus the SIGKILL Go test which is NOT a Playwright spec.

**Primary recommendation:** Decompose Phase 4 into **six plans** along the file-layout boundaries already in CONTEXT.md:
1. Schema additions + UpdateKind enum extensions + tygo regen.
2. `internal/compose.Runner` body + tests (foundational; everyone downstream depends on it).
3. `internal/actions` package: orchestrator + mutex + middleware + verify + errors. **Largest plan.**
4. HTTP handlers + router wiring + main.go boot order extension + slog event schema.
5. STATE-04 SIGKILL fault-injection test harness (independent — can run in parallel with 1–4).
6. 8 RED-first Playwright e2e specs + `disconnect-network.ts` fixture + cron-fast override extension. Gates the phase.

## User Constraints (from CONTEXT.md)

### Locked Decisions

The 04-CONTEXT.md has all 4 grey areas already accepted as recommended. Locked decisions verbatim:

**Area 1 — Action Handler Architecture (locked):**
- Linear sequence: middleware → mutex.TryLock → idempotency check → ActionStart → pull/tag → verify pulled digest → ActionProgress → compose.Runner.UpdateService → verify-after-recreate → ActionResult → release mutex → 200 response.
- Rollback: same flow but `docker.Client.ImageTag(ctx, image+"@"+c.PreviousDigest, image+":"+c.Tag)` replaces pull; rollback works OFFLINE.
- Force-pull default: `docker.Client.ImagePull` only, no `compose up -d`. Optional `?recreate=true` query param triggers the full Update flow.
- State mutations via channel (DETECT-10 invariant carries forward). Three new `UpdateKind` values: `KindActionStart`, `KindActionProgress`, `KindActionResult`.
- `state.Container.ActionInFlight string` + `ActionError string` schema extensions (both omitempty).

**Area 2 — Per-Service Mutex (locked):**
- `actions.Orchestrator` holds `mu sync.RWMutex` + `locks map[string]*sync.Mutex`. Lazy creation. `TryLock` non-blocking; 409 on collision. Cross-service parallelism allowed. Cron-vs-manual race resolved by channel pattern + per-field Apply closures.

**Area 3 — Verify-After-Recreate (locked):**
- `verifyDuration = 15s` (env: `HMI_UPDATE_VERIFY_WINDOW_S`). 1-second ticker. Fail-fast on `!State.Running`, `RestartCount > snapshot`, `State.Health.Status == "unhealthy"`. Healthcheck opt-in via `hmi-update.wait-for-healthy=true` label with extended 60s window (env: `HMI_UPDATE_HEALTHCHECK_WINDOW_S`).

**Area 4 — Self-Protection + Safety Labels (locked):**
- Env var `HMI_UPDATE_SELF_SERVICE` (default `"hmi-update"`).
- Middleware order: self-protection → safety-label → service-name validation (regex `^[a-zA-Z0-9._-]+$`) → mutex → idempotency → action body.
- SAFE-03: poll loop ignores `hmi-update.allow-*` labels; only action middleware honors them.
- Argv discipline: `exec.CommandContext("docker", "compose", "-f", composePath, "up", "-d", "--force-recreate", service)`.

### Claude's Discretion

Per CONTEXT.md `<decisions>` lower section. Research leans verbatim with the CONTEXT.md recommendations:
- `ActionInFlight` / `ActionError` as plain `string` (omitempty) — matches `Notes` precedent.
- Single-string `ActionError` format: `"<phase>_failed: <reason>"` (e.g. `"verify_failed: container restarted 3 times in 15s"`).
- `map + RWMutex` over `sync.Map` for the mutex map.
- `compose.Runner.UpdateService` always runs (no `dryRun` flag).
- Force-pull-no-recreate skips verify-after-recreate entirely.
- Slog event names: `action.start`, `action.phase`, `action.complete`, `action.verify_failed`, `action.compose_failed`, `action.pull_failed`.
- `lockService` returns `func()` unlock closure (used as `unlock := o.lockService(svc); defer unlock()`).

### Deferred Ideas (OUT OF SCOPE)

Verbatim from CONTEXT.md `<deferred>`:
- Multi-slot rollback history (`History []DigestRecord`).
- Bulk Update endpoint (`POST /api/update-all`).
- Action audit log endpoint (`/api/action-history`).
- Label precedence cross-rules between `hmi-update.wait-for-healthy` and `hmi-update.allow-update`.
- arm64 builds for `sigkillhelper`.
- Action endpoint authentication.
- Compose runner `dryRun` flag.

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| ACT-01 | POST /api/containers/:service/update: pull → verify digest → record previous → compose up -d --force-recreate → verify recreate ≤15s | §Compose Runner body, §Action Orchestrator flow, §Verify-after-recreate poll loop |
| ACT-02 | Update completes within 30s; new digest visible in UI/state; previous_digest set | §Action endpoint slog schema (duration_ms), §State mutation via channel |
| ACT-03 | POST /api/containers/:service/rollback: ImageTag local re-tag → compose up -d --force-recreate → verify | §Rollback flow + ImageTag API surface (already wired in Phase 2 facade) |
| ACT-04 | Rollback works OFFLINE (registry network detached) | §Offline-rollback test fixture (disconnect-network.ts) |
| ACT-05 | POST /api/containers/:service/force-pull (re-pull even when matched); optional ?recreate=true | §Force-pull handler shape |
| ACT-06 | Update on already-:latest returns 200 with no_op:true | §Idempotency short-circuit (early-return before mutex) |
| ACT-07 | Rollback to current digest returns 200 no_op:true | Same pattern as ACT-06 |
| ACT-08 | Per-service mutex serializes; 409 on collision | §Per-service mutex map + TryLock semantics |
| ACT-09 | Self-protection: refuse self-update with 409 | §Self-protection middleware + PROJECT.md self-upgrade procedure |
| ACT-10 | Service-name regex `^[a-zA-Z0-9._-]+$` + in-memory map lookup | §Service-name validation middleware |
| ACT-11 | Action response body includes current_digest + previous_digest | §Action response schema |
| ACT-12 | `docker compose restart hmi-update` preserves digests + rollback targets | §Restart-persistence e2e spec (relies on Phase 1 atomic write + tygo state shape) |
| SAFE-01 | hmi-update.allow-update=false → 409 on update | §Safety-label middleware |
| SAFE-02 | hmi-update.allow-rollback=false → 409 on rollback | §Safety-label middleware |
| SAFE-03 | allow-update=false containers still polled (read-only); only action surface disabled | §Poll loop ignores allow-* labels (verify by code-grep in eligibleContainers) |
| STATE-04 | SIGKILL-mid-write fault injection leaves file parseable (old OR new) | §SIGKILL fault-injection test harness |
| STATE-05 | State file UID/GID matches container nonroot (65532); install runbook documents chown | §PROJECT.md "Installation prerequisites" snippet |
| OBS-01 | Every poll/update/rollback/force-pull logs container, before/after digests, exit code, duration as structured slog JSON | §Action endpoint slog event schema |
| OBS-03 | GET /api/state is memory-only, no I/O | §/api/state no-I/O test pattern (Phase 1 already satisfies; Phase 4 adds explicit guard test) |

## Architectural Responsibility Map

The Phase 4 capabilities sort cleanly into the API/Backend tier — there is no browser or CDN work in this phase.

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| HTTP route registration + middleware chain | API / Backend | — | `internal/api/server.go` + `internal/actions/middleware.go`; Go 1.22+ stdlib `ServeMux` with `r.PathValue("service")`. |
| Action orchestration (pull → recreate → verify) | API / Backend | — | `internal/actions/orchestrator.go`; pure Go business logic. |
| Per-service mutex map | API / Backend | — | `internal/actions/mutex.go`; in-memory only. |
| State persistence (Container.ActionInFlight/Error) | Database / Storage | API / Backend | Existing `state.Store` (renameio atomic write); Phase 4 extends schema fields only. |
| `docker compose up -d --force-recreate` invocation | API / Backend | OS subprocess | `internal/compose/runner.go` via `os/exec`; subprocess to host `docker` CLI. |
| Image pull / tag operations | API / Backend | Docker daemon | `internal/docker/client.go` facade over `moby/moby/client` (already wired in Phase 2). |
| Container inspection (verify-after-recreate) | API / Backend | Docker daemon | `docker.Client.ContainerInspect` (already on facade). |
| Slog action events | API / Backend | — | OBS-01: structured JSON to stdout; redacting handler already installed in Phase 3. |
| SIGKILL fault-injection test | Process / OS | API / Backend | Parent test + helper binary; exercises filesystem atomicity. |
| 8 Playwright e2e specs | API / Backend (tested) | Docker daemon | Run against the e2e compose stack (zot + hmi-update + watched stubs). |

**No misassignments to guard against.** Phase 4 has no UI or browser work (Phase 5 owns that); the `ActionInFlight`/`ActionError` schema fields are surfaced via `/api/state` but the UI rendering lives in Phase 5.

## Project Constraints (from CLAUDE.md)

CLAUDE.md directives load-bearing for Phase 4:

- **C1 (one container, one binary):** Phase 4 must not add a sidecar. `os/exec` to host `docker compose` is allowed (the CLI is bundled in the runtime image — Phase 7's concern, but assumed available now).
- **C2 (file-based persistence only):** State mutations go through `state.Store.Update` (renameio). The new `ActionInFlight`/`ActionError` fields persist via the same path.
- **C4 (TDD: verify → implement → verify → implement):** Every ACT-* requirement starts as a RED Playwright spec. Phase 4 ships 8 new specs.
- **Security: LAN-only, unauthenticated:** No auth middleware in Phase 4. Service-name regex validation is the only input-validation surface (Pitfall 13).
- **Security: server-enforced safety labels:** SAFE-01..03 require server-side 409 — UI hiding is Phase 5's job.
- **Tech stack — Backend: Go 1.23+ (actual 1.26), `net/http` stdlib router, `moby/moby/client`, `log/slog`:** Phase 4 binds to all four. No new dependencies are required.
- **GSD Workflow Enforcement:** This research is part of a GSD planning phase; downstream plans must enter via `/gsd-execute-phase`.

## Standard Stack

### Core (all already in go.mod / package.json)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `os/exec` (stdlib) | std | Subprocess for `docker compose up -d --force-recreate <svc>` | Go 1.20+ Cancel/WaitDelay fields give clean SIGTERM→SIGKILL handoff. |
| `sync.Mutex.TryLock` (stdlib) | Go 1.18+ | Per-service serialization; non-blocking 409 on contention | Native, no library; the only viable shape for ACT-08 ("return immediately on collision, don't queue"). |
| `time.NewTicker` (stdlib) | std | 1-second poll for verify-after-recreate | Trivial; combined with ctx.Done() in a select. |
| `github.com/moby/moby/client@v0.4.1` | locked | `ImagePull` returns `ImagePullResponse` with `JSONMessages` + `Wait` methods (already on facade); `ImageTag(ctx, ImageTagOptions{Source, Target})` (already on facade); `ContainerInspect` returns `container.InspectResponse` with `State.Running`, `RestartCount`, `Health.Status` | Already wired; no facade additions strictly required for Phase 4 (one optional addition: `ImageInspect` if we choose RepoDigests verify path). |
| `github.com/google/renameio/v2@v2.0.2` | locked | Atomic state writes — STATE-04 test verifies the existing infrastructure | Already wired in Phase 1. No additions. |
| `log/slog` (stdlib) | std | Structured JSON events for OBS-01 | Already wired in Phase 3 with redacting handler. Phase 4 adds new event names. |

**Verified versions** (from `/Users/jonb/Projects/tmp/go.mod` line-by-line):
- `github.com/moby/moby/client v0.4.1` — `[VERIFIED: go.mod line 8]`
- `github.com/moby/moby/api v1.54.2` — `[VERIFIED: go.mod line 7]`
- `github.com/google/renameio/v2 v2.0.2` — `[VERIFIED: go.mod line 6]`
- `github.com/google/go-containerregistry v0.20.8` — `[VERIFIED: go.mod line 5]`
- `go 1.26` — `[VERIFIED: go.mod line 3]`

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `@playwright/test` | already in `e2e/package.json` | 8 new specs + offline-rollback fixture | Already wired via Phase 1 globalSetup. |
| `node:child_process` | Node stdlib | `disconnect-network.ts` fixture invokes `docker network disconnect` | Same pattern as `push-image.ts`. |

### Alternatives Considered (and rejected — already locked in CONTEXT.md)

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `sync.Mutex.TryLock` + RWMutex on map | `sync.Map` | `sync.Map` is opaque-sharded, hard to reason about. CONTEXT.md leans `map + RWMutex`. |
| `os/exec` for compose | `github.com/docker/compose/v2` Go SDK | Compose SDK drags BuildKit/containerd transitive deps (30+ MB), violates STACK.md decision §3. |
| Parse ImagePull JSONMessages for digest | `ImageInspect` after pull to read `RepoDigests[0]` | Both work. JSONMessages is cleaner (no new facade method); ImageInspect is more explicit. See §"Image Pull + Digest Verify" for the recommendation. |
| `time.NewTicker(1s)` for verify | `time.After` in a for loop | Ticker is the idiomatic Go 1.23+ pattern; allows clean Stop() on ctx cancel. |

**Installation:** No new packages required. All dependencies already in `go.mod` / `e2e/package.json`.

## Architecture Patterns

### System Architecture Diagram — Phase 4 Action Flow

```
Browser (POST /api/containers/{service}/update)
   │
   ▼
internal/api/handlers_actions.go
   │ (mux.HandleFunc("POST /api/containers/{service}/update", ...))
   │
   ▼
[Middleware chain — order critical]
   1. ServiceName validation regex     -> 400 on mismatch
   2. State lookup (in-memory)         -> 404 on container_not_found
   3. SelfProtection check             -> 409 self_protection
   4. SafetyLabel check                -> 409 action_disabled_by_label
   │
   ▼
actions.Orchestrator.Update(ctx, service)
   │
   ├─► composeReader.CheckUnchanged(ctx)        -> 412 ErrComposeFileMoved
   │
   ├─► unlock := orchestrator.lockService(svc)  -> 409 service_busy on TryLock failure
   │   defer unlock()
   │
   ├─► state.Store.Get() — read current digest, available digest
   │   IDEMPOTENCY: if CurrentDigest == AvailableDigest, return 200 no_op:true
   │
   ├─► send KindActionStart -> state.Container.ActionInFlight = "updating"
   │   slog.Info("action.start", service=foo, action=update)
   │
   ├─► docker.Client.ImagePull(ctx, image+":"+tag, ImagePullOptions{})
   │   ├─► drain progress stream via JSONMessages iterator
   │   ├─► capture final digest from `aux` field (or fallback: ImageInspect.RepoDigests[0])
   │   └─► VERIFY: pulled digest == registry digest (registry.Resolver.Digest call)
   │   slog.Info("action.phase", service=foo, phase=pulled, new_digest=sha256:...)
   │   ON FAILURE -> KindActionResult{Phase: "failed", Err: ErrPullFailed} -> 500
   │
   ├─► send KindActionProgress{Phase: "pulled", NewDigest}
   │   previousDigestSnapshot = c.CurrentDigest
   │
   ├─► compose.Runner.UpdateService(ctx, service)
   │   ├─► exec.CommandContext("docker", "compose", "-f", composePath,
   │   │                       "up", "-d", "--force-recreate", service)
   │   ├─► cmd.WaitDelay = 10s
   │   ├─► capture stdout/stderr into separate buffers
   │   └─► exit code != 0 -> ErrComposeFailed
   │   slog.Info("action.phase", service=foo, phase=recreated, exit_code=0, duration_ms=N)
   │   ON FAILURE -> KindActionResult{Phase: "failed", Err: ErrComposeFailed} -> 500
   │
   ├─► actions.verifyAfterRecreate(ctx, service, containerID, snapshot)
   │   ├─► time.NewTicker(1*time.Second), deadline = 15s (or 60s healthcheck opt-in)
   │   ├─► each tick: docker.Client.ContainerInspect(ctx, containerID)
   │   ├─► assert State.Running && RestartCount unchanged
   │   ├─► (opt-in) assert State.Health.Status != "unhealthy"
   │   └─► require 15 consecutive successful ticks
   │   slog.Info("action.phase", service=foo, phase=verified, ticks=15)
   │   ON FAILURE -> KindActionResult{Phase: "failed", Err: ErrVerifyFailed} -> 500
   │
   ├─► send KindActionResult{Phase: "complete", OldDigest, NewDigest, Err: nil}
   │   -> state.Container.CurrentDigest = NewDigest
   │   -> state.Container.PreviousDigest = OldDigest
   │   -> state.Container.ActionInFlight = ""
   │   -> state.Container.ActionError = ""
   │   -> state.Container.UpdateAvailable = false
   │
   └─► slog.Info("action.complete", service=foo, action=update,
                 before=sha256:..., after=sha256:..., exit_code=0, duration_ms=N)
       defer unlock()  (releases mutex)
       return 200 + {"current_digest": ..., "previous_digest": ...}
```

**File-to-implementation mapping:** see the Component Responsibilities table below; the diagram traces only the data flow.

### Component Responsibilities

| Component | File | Responsibility |
|-----------|------|----------------|
| Action HTTP handlers | `internal/api/handlers_actions.go` | Three handlers (update, rollback, force-pull); parse `r.PathValue("service")`; invoke middleware chain; delegate to orchestrator. |
| Action orchestrator | `internal/actions/orchestrator.go` | Linear sequence per Update/Rollback/Force-pull; sends StateUpdate messages on the existing channel. |
| Per-service mutex map | `internal/actions/mutex.go` | `lockService(svc) func()` returns unlock closure; `RWMutex` protects map. |
| Middleware | `internal/actions/middleware.go` | Three middleware: self-protection, safety-label, service-name validation. Compose into a single chain at route registration. |
| Verify loop | `internal/actions/verify.go` | 1-second ticker, 15s default window, 60s healthcheck opt-in window, ctx-aware cancel. |
| Sentinel errors | `internal/actions/errors.go` | `ErrServiceBusy`, `ErrSelfProtection`, `ErrActionDisabledByLabel`, `ErrVerifyFailed`, `ErrVerifyCanceled`, `ErrComposeFailed`, `ErrPullFailed`. |
| Compose runner | `internal/compose/runner.go` | `Runner.UpdateService(ctx, service) error` via `exec.CommandContext`. |
| State schema additions | `internal/state/schema.go` | `Container.ActionInFlight`, `Container.ActionError` (both omitempty). |
| Channel UpdateKind enum | `internal/poll/channel.go` | Three new constants: `KindActionStart`, `KindActionProgress`, `KindActionResult`. |
| API types mirror | `internal/api/types.go` | Mirror the two new fields; tygo regen. |
| Boot wiring | `cmd/hmi-update/main.go` | Construct `compose.NewRunner(composePath)`, then `actions.NewOrchestrator(...)` with all deps, then pass to `api.NewServer(...)`. |
| SIGKILL test helper | `cmd/sigkillhelper/main.go` | Loop write to state.Store; parent SIGKILLs. |
| SIGKILL test parent | `internal/state/store_sigkill_test.go` | Spawns helper, sends SIGKILL at randomized intervals, verifies file parseable. Gated `//go:build sigkill_test`. |

### Pattern 1: Compose Runner — `exec.CommandContext` with argv, stderr capture, WaitDelay

**What:** Subprocess-invoke `docker compose -f $HMI_UPDATE_COMPOSE_PATH up -d --force-recreate <service>` with strict argv separation (Pitfall 13 prevention). Capture stdout/stderr into separate buffers, propagate exit code, attach to slog event. Set `cmd.WaitDelay = 10*time.Second` so ctx cancellation gives the child a 10-second SIGTERM grace before the runtime sends SIGKILL.

**When to use:** Every Update / Rollback / Force-pull-with-recreate call.

**Example (canonical body for `internal/compose/runner.go`):**

```go
// Source: stdlib os/exec docs https://pkg.go.dev/os/exec#Cmd
// + Go 1.20+ Cancel/WaitDelay fields (https://github.com/golang/go/issues/50436)

package compose

import (
    "bytes"
    "context"
    "fmt"
    "log/slog"
    "os/exec"
    "time"
)

// ExecRunner is the production Runner that shells out to `docker compose`.
// The dockerBin field is resolved once at construction via exec.LookPath
// so the runner fails fast at boot if the docker CLI is missing
// (rather than at first Update click).
type ExecRunner struct {
    composePath string
    dockerBin   string
}

func NewRunner(composePath string) (Runner, error) {
    bin, err := exec.LookPath("docker")
    if err != nil {
        return nil, fmt.Errorf("compose.NewRunner: docker CLI not found in PATH (need docker compose plugin v2.20+): %w", err)
    }
    return &ExecRunner{composePath: composePath, dockerBin: bin}, nil
}

func (r *ExecRunner) ComposePath() string { return r.composePath }

// UpdateService runs `docker compose -f <path> up -d --force-recreate <service>`.
// Argv discipline: the service name is a separate argv element — never
// interpolated into a shell string. Pitfall 13 prevention; the upstream
// middleware (internal/actions/middleware.go) has already validated the
// service name against the allowlist regex, but defense in depth.
func (r *ExecRunner) UpdateService(ctx context.Context, service string) error {
    start := time.Now()
    args := []string{
        "compose", "-f", r.composePath,
        "up", "-d", "--force-recreate", service,
    }
    cmd := exec.CommandContext(ctx, r.dockerBin, args...)

    // WaitDelay grants the child process 10s of SIGTERM grace before the
    // runtime sends SIGKILL on ctx cancel. docker compose's own
    // stop_grace_period default is 10s, so 10s here matches behavior the
    // operator already expects.
    //
    // The default Cancel func (os.Process.Kill — SIGKILL on Unix) is too
    // abrupt for compose; override to SIGTERM via cmd.Cancel.
    // Source: https://pkg.go.dev/os/exec#Cmd Cancel + WaitDelay
    cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
    cmd.WaitDelay = 10 * time.Second

    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    err := cmd.Run()
    exitCode := 0
    if cmd.ProcessState != nil {
        exitCode = cmd.ProcessState.ExitCode()
    }
    elapsed := time.Since(start)

    // Truncate stderr to a reasonable size for slog (compose output can
    // run several KB on a failed recreate). 4096 bytes captures the
    // last error message; the full content is available in the
    // returned error for the API handler to surface partially.
    stderrSnippet := stderr.String()
    if len(stderrSnippet) > 4096 {
        stderrSnippet = "...[truncated]..." + stderrSnippet[len(stderrSnippet)-4096:]
    }

    if err != nil {
        slog.Error("compose.run",
            "service", service,
            "exit_code", exitCode,
            "duration_ms", elapsed.Milliseconds(),
            "err", err,
            "stderr_snippet", stderrSnippet,
        )
        return fmt.Errorf("compose.UpdateService %s: exit %d: %w: %s",
            service, exitCode, err, stderrSnippet)
    }

    slog.Info("compose.run",
        "service", service,
        "exit_code", exitCode,
        "duration_ms", elapsed.Milliseconds(),
    )
    return nil
}
```

**Confidence:** HIGH on the structure (verified against [pkg.go.dev/os/exec](https://pkg.go.dev/os/exec) and [docker compose up docs](https://docs.docker.com/reference/cli/docker/compose/up/)). `[VERIFIED: pkg.go.dev/os/exec]` `[CITED: docs.docker.com/reference/cli/docker/compose/up/]`

### Pattern 2: Image Pull + Digest Verify

**What:** After `docker.Client.ImagePull(ctx, ref, opts)` returns the `ImagePullResponse` (an `io.ReadCloser` carrying the JSON progress stream), drain the stream and extract the digest from the SDK's `aux` JSON field. Compare against the registry digest (fetched via `registry.Resolver.Digest`) — they MUST match.

**Why this verifies Pitfall 1:** The docker daemon, when pulling, reads the `Docker-Content-Digest` response header from the registry and surfaces it in the pull progress's `aux` field. The registry resolver (Phase 3) ALSO reads `Docker-Content-Digest` via `crane.Digest`. Both come from the same authoritative source. If they don't match, something is deeply broken (man-in-the-middle, registry inconsistency); abort.

**Two implementation options:**

**Option A (RECOMMENDED): Parse digest from `JSONMessages` iterator.**

The moby v0.4.1 `ImagePullResponse` interface exposes `JSONMessages(ctx) iter.Seq2[jsonstream.Message, error]`. The terminal message carries an `aux` field with the digest (`{"ID":"sha256:abcdef..."}`).

```go
// Source: pkg.go.dev/github.com/moby/moby/client ImagePullResponse
// + go doc github.com/moby/moby/client v0.4.1 ImagePullResponse
//
// The facade keeps the return as io.ReadCloser (see internal/docker/moby.go
// line 217). For Phase 4 we either:
//   (a) Add a thin internal helper that re-asserts the interface back to
//       ImagePullResponse and iterates JSONMessages.
//   (b) Parse the JSON stream by hand via json.Decoder over the
//       io.ReadCloser body (matches the docker CLI's own pull progress
//       parsing path).
//
// (b) is recommended — avoids re-importing the SDK in internal/actions and
// keeps the facade narrow.

import (
    "encoding/json"
    "io"
)

// jsonMessage matches the docker pull progress wire format.
// Source: https://pkg.go.dev/github.com/moby/moby/pkg/jsonmessage
type jsonMessage struct {
    Status string          `json:"status,omitempty"`
    ID     string          `json:"id,omitempty"`
    Error  string          `json:"error,omitempty"`
    Aux    json.RawMessage `json:"aux,omitempty"`
}

// auxDigest carries the final-pull digest in the aux field.
// Source: docker daemon pulls emit `{"ID":"sha256:..."}` in aux on success.
type auxDigest struct {
    ID     string `json:"ID,omitempty"`
    Digest string `json:"Digest,omitempty"`
}

func drainPullStream(rc io.ReadCloser) (digest string, err error) {
    defer rc.Close()
    dec := json.NewDecoder(rc)
    for {
        var msg jsonMessage
        if err := dec.Decode(&msg); err != nil {
            if err == io.EOF {
                break
            }
            return "", fmt.Errorf("drain pull stream: %w", err)
        }
        if msg.Error != "" {
            return "", fmt.Errorf("docker pull stream error: %s", msg.Error)
        }
        if len(msg.Aux) > 0 {
            var aux auxDigest
            if err := json.Unmarshal(msg.Aux, &aux); err == nil {
                if aux.Digest != "" {
                    digest = aux.Digest
                } else if aux.ID != "" {
                    digest = aux.ID
                }
            }
        }
    }
    if digest == "" {
        return "", errors.New("docker pull stream ended without aux digest")
    }
    return digest, nil
}
```

**Option B (FALLBACK): Add `ImageInspect` to the facade and read `RepoDigests[0]` after pull completes.**

Requires extending `internal/docker/Client` interface and `mobyClient` impl. The doc on the existing interface explicitly warns: "Adding a seventh method requires a coordinated edit of the reflect-based method-count guard in moby_test.go" — so this is a small but deliberate change. The advantage is that `RepoDigests` is the operator-visible field (`docker inspect` reports it), so the verify path is grep-able from the host.

```go
// internal/docker/client.go — add a 7th method
ImageInspect(ctx context.Context, ref string) (image.InspectResponse, error)

// In actions/orchestrator.go after ImagePull drains:
ii, err := dockerClient.ImageInspect(ctx, ref)
if err != nil { ... }
if len(ii.RepoDigests) == 0 {
    return fmt.Errorf("image %s has no RepoDigests after pull (pull may have failed silently)", ref)
}
// RepoDigests entries are formatted "image@sha256:...". Extract digest portion.
parts := strings.SplitN(ii.RepoDigests[0], "@", 2)
if len(parts) != 2 { ... }
pulledDigest := parts[1]
```

**Recommendation:** **Option A** (drain JSONMessages, parse aux digest). Keeps facade narrow at 6 methods; matches the Phase 1 doc comment ("Adding a seventh method requires..."); aligns with the SDK's design intent. If Option A proves fragile in CI (the `aux` shape varies across SDK versions), the planner can pivot to Option B; the test surface is the same.

**Cross-check with registry digest (Pitfall 1):**

```go
// After drainPullStream succeeds:
registryDigest, err := resolver.Digest(ctx, image+":"+tag)
if err != nil { ... }
if pulledDigest != registryDigest {
    return fmt.Errorf("pulled digest %s does not match registry digest %s (Pitfall 1)",
        pulledDigest, registryDigest)
}
```

**Confidence:** HIGH on the moby SDK shape (verified via `internal/docker/_sdk_shape.txt` line 59 — `ImagePullResponse` interface with `JSONMessages` method). MEDIUM on the exact `aux` field schema — the moby/moby/pkg/jsonmessage docs confirm the shape but the SDK has refactored it; the planner should write a small probe test that round-trips a real pull against zot before committing to Option A. `[CITED: pkg.go.dev/github.com/moby/moby/client ImagePullResponse]` `[VERIFIED: internal/docker/_sdk_shape.txt:59-74]`

### Pattern 3: ImageTag for Rollback — Local Re-tag, Offline-Capable

**What:** `docker.Client.ImageTag(ctx, image+"@"+previousDigest, image+":"+tag)` retags a local image. This is **local-only** — no registry call, works with the registry unreachable. The Phase 4 acceptance criterion 4 (ACT-04 offline rollback) hinges on this.

**Example (already in `internal/docker/moby.go` line 229):**

```go
// Source: internal/docker/moby.go:225-234 (already implemented in Phase 2)
func (m *mobyClient) ImageTag(ctx context.Context, src, dst string) error {
    if _, err := m.c.ImageTag(ctx, client.ImageTagOptions{Source: src, Target: dst}); err != nil {
        return fmt.Errorf("docker.ImageTag %s -> %s: %w", src, dst, err)
    }
    return nil
}
```

**Rollback flow (in `internal/actions/orchestrator.go`):**

```go
// Locked in CONTEXT.md Area 1 — Rollback handler:
//   1. middleware + mutex + idempotency (same as Update)
//   2. If c.PreviousDigest == "" -> 400 no_previous_digest
//   3. ImageTag(ctx, image+"@"+c.PreviousDigest, image+":"+c.Tag) — local
//   4. compose.Runner.UpdateService(ctx, service) — local image cache hit
//   5. verifyAfterRecreate (15s)
//   6. State swap: CurrentDigest <-> PreviousDigest (single-slot toggle)
//   7. UpdateAvailable re-flips to true (registry :latest unchanged)
```

**Error class for "image not found locally":** The moby SDK exposes `client.IsErrNotFound` (verified via `_sdk_shape.txt` — `IsErrConnectionFailed` is exposed, but the full IsErr* family isn't in the captured shape; check explicitly with `errors.Is`). For rollback, a "previous image not in local cache" error means the local image was pruned — surface 500 with a clear message:

```go
if err := dockerClient.ImageTag(ctx, src, dst); err != nil {
    // moby SDK error class check; substring fallback in case the typed
    // path doesn't unwrap cleanly (same belt-and-braces pattern as
    // handlers.go looksLikeSocketEACCES).
    if strings.Contains(err.Error(), "No such image") {
        return ErrPreviousImageGone // -> 500 with clear remediation
    }
    return err
}
```

**Confidence:** HIGH (existing implementation in Phase 2). `[VERIFIED: internal/docker/moby.go:229]`

### Pattern 4: Per-Service Mutex Map with Double-Checked Locking

**What:** `actions.Orchestrator` holds a `sync.RWMutex` protecting a `map[string]*sync.Mutex`. `lockService(svc)` first tries the map under RLock (the fast path for already-registered services), then escalates to Lock for new-entry creation with double-check.

**Why double-checked:** Two concurrent requests for a previously-unseen service would otherwise create two separate mutex instances under the RLock-then-Lock window. Double-check: re-read inside the write lock to catch the race.

**Example:**

```go
// Source: locked in CONTEXT.md Area 2; pattern verified against
// stdlib sync semantics: https://pkg.go.dev/sync

package actions

import "sync"

type Orchestrator struct {
    mu    sync.RWMutex
    locks map[string]*sync.Mutex
    // ... other fields (dockerClient, runner, resolver, store, updates, etc.)
}

// lockService attempts to acquire the per-service mutex without blocking.
// On success returns an unlock closure for `defer unlock()`. On
// contention returns (nil, ErrServiceBusy) so the caller can map to
// HTTP 409.
//
// Implementation notes:
//   - Fast path: RLock, map lookup, TryLock; release RLock before
//     potentially Locking for entry creation (avoid lock upgrade).
//   - Slow path: Lock for new-entry creation with double-check (another
//     goroutine may have created the entry between the RUnlock and the
//     Lock).
//   - The mutex map never shrinks (services are bounded by the compose
//     file's service count). Entries are tiny (a *sync.Mutex header).
func (o *Orchestrator) lockService(svc string) (func(), error) {
    // Fast path: read existing mutex under RLock.
    o.mu.RLock()
    m, ok := o.locks[svc]
    o.mu.RUnlock()

    if !ok {
        // Slow path: create the entry under Lock with double-check.
        o.mu.Lock()
        m, ok = o.locks[svc]
        if !ok {
            m = &sync.Mutex{}
            o.locks[svc] = m
        }
        o.mu.Unlock()
    }

    // TryLock is the load-bearing primitive — non-blocking, 409 on
    // contention. (sync.Mutex.TryLock was added in Go 1.18.)
    if !m.TryLock() {
        return nil, ErrServiceBusy
    }
    return m.Unlock, nil
}

// Usage at the handler / orchestrator entry point:
//   unlock, err := o.lockService(svc)
//   if err != nil {
//       return writeJSONError(w, 409, "service_busy", svc)
//   }
//   defer unlock()
//   // ... action body ...
```

**Concurrency invariant:** The mutex is held for the entire action body (pull + recreate + verify, possibly ~30 seconds). A second request for the same service during that window returns 409 immediately. Cross-service parallelism is preserved because each service has its own mutex.

**Test pattern (concurrent TryLock contention test in `mutex_test.go`):**

```go
// go test -race -count=50 ./internal/actions/...

func TestLockService_Concurrent(t *testing.T) {
    o := &Orchestrator{locks: map[string]*sync.Mutex{}}
    var wg sync.WaitGroup
    acquired := atomic.Int32{}
    rejected := atomic.Int32{}

    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            unlock, err := o.lockService("svc-a")
            if err != nil {
                rejected.Add(1)
                return
            }
            acquired.Add(1)
            // Hold briefly so others see contention
            time.Sleep(time.Microsecond)
            unlock()
        }()
    }
    wg.Wait()

    if acquired.Load() < 1 || rejected.Load() < 1 {
        t.Errorf("expected mix of acquired+rejected; got %d/%d",
            acquired.Load(), rejected.Load())
    }
}
```

**Confidence:** HIGH. `sync.Mutex.TryLock` semantics verified against [pkg.go.dev/sync](https://pkg.go.dev/sync) — non-blocking, returns false on contention or starvation mode, "A successful call to Mutex.TryLock is equivalent to a call to Lock." `[CITED: pkg.go.dev/sync Mutex.TryLock]`

### Pattern 5: Verify-After-Recreate Poll Loop

**What:** After `compose up -d --force-recreate` returns exit 0, poll `docker.Client.ContainerInspect(ctx, containerID)` once per second for up to 15 seconds (default; configurable via `HMI_UPDATE_VERIFY_WINDOW_S`). Each tick checks `State.Running == true` AND `RestartCount == snapshot.RestartCount`. Require 15 consecutive successful ticks before declaring success.

**Why a "consecutive successful ticks" rule rather than "no failure for N seconds":** A crash-looping container can flip in and out of `Running == true` within a tick window. Requiring 15 *consecutive* successes guarantees the container has stayed up for the full window.

**Healthcheck opt-in:** When the watched container has label `hmi-update.wait-for-healthy=true`, ALSO wait for `State.Health.Status == "healthy"` within an extended 60s window. Containers without a HEALTHCHECK directive still treat "no health status reported" as soft-success after 60s — never block indefinitely.

**Example (canonical body for `internal/actions/verify.go`):**

```go
// Source: locked in CONTEXT.md Area 3; pattern verified against
// pkg.go.dev/github.com/moby/moby/api/types/container InspectResponse
// (State, RestartCount, Health fields).

package actions

import (
    "context"
    "errors"
    "fmt"
    "log/slog"
    "time"

    "github.com/centroid-is/hmi-update/internal/docker"
)

const (
    defaultVerifyWindow      = 15 * time.Second
    defaultHealthcheckWindow = 60 * time.Second
    verifyTickInterval       = 1 * time.Second
)

// verifySnapshot captures pre-action state for comparison during the
// verify loop. Restart count is read from container.InspectResponse;
// healthcheckOptIn is derived from the container's label set
// (hmi-update.wait-for-healthy=true).
type verifySnapshot struct {
    ContainerID       string
    RestartCount      int
    HealthcheckOptIn  bool
    VerifyWindow      time.Duration
    HealthcheckWindow time.Duration
}

// verifyAfterRecreate polls ContainerInspect once per second. Requires
// `consecutiveTarget` successful ticks (= window / tickInterval). Fail-
// fast on any anomaly; return ErrVerifyCanceled on ctx cancel.
func (o *Orchestrator) verifyAfterRecreate(ctx context.Context, snap verifySnapshot) error {
    ticker := time.NewTicker(verifyTickInterval)
    defer ticker.Stop()

    deadline := time.Now().Add(snap.VerifyWindow)
    if snap.HealthcheckOptIn {
        deadline = time.Now().Add(snap.HealthcheckWindow)
    }

    target := int(snap.VerifyWindow / verifyTickInterval) // 15 for default
    consecutive := 0

    for {
        select {
        case <-ctx.Done():
            return ErrVerifyCanceled

        case <-ticker.C:
            if time.Now().After(deadline) {
                if snap.HealthcheckOptIn && consecutive == 0 {
                    // Healthcheck opt-in soft-success: no health status
                    // reported within 60s -> treat as success (don't
                    // block indefinitely).
                    return nil
                }
                return fmt.Errorf("%w: did not reach %d consecutive healthy ticks within window",
                    ErrVerifyFailed, target)
            }

            insp, err := o.dockerClient.ContainerInspect(ctx, snap.ContainerID)
            if err != nil {
                if errors.Is(err, context.Canceled) {
                    return ErrVerifyCanceled
                }
                // Container disappeared (compose down + up race?) - fail-fast
                return fmt.Errorf("%w: ContainerInspect failed: %v",
                    ErrVerifyFailed, err)
            }

            // Defensive nil-check; moby types pointer-nest State+Health
            if insp.Container.State == nil {
                return fmt.Errorf("%w: container state nil", ErrVerifyFailed)
            }

            // Fast-fail conditions (per CONTEXT.md Area 3):
            if !insp.Container.State.Running {
                return fmt.Errorf("%w: container not running", ErrVerifyFailed)
            }
            if insp.Container.RestartCount > snap.RestartCount {
                return fmt.Errorf("%w: container restarted %d times (was %d)",
                    ErrVerifyFailed,
                    insp.Container.RestartCount-snap.RestartCount,
                    snap.RestartCount)
            }
            if snap.HealthcheckOptIn && insp.Container.State.Health != nil {
                switch insp.Container.State.Health.Status {
                case "unhealthy":
                    return fmt.Errorf("%w: healthcheck unhealthy", ErrVerifyFailed)
                case "healthy":
                    // Healthcheck opt-in counts a "healthy" status as
                    // unconditional success — skip the consecutive-ticks
                    // requirement (the healthcheck IS the readiness signal).
                    slog.Info("action.phase",
                        "service", "??", // caller injects via slog.With
                        "phase", "verified",
                        "ticks", consecutive,
                        "health", "healthy")
                    return nil
                    // "starting" / "" -> keep polling
                }
            }

            // Healthy tick — increment consecutive counter.
            consecutive++
            if consecutive >= target {
                slog.Info("action.phase",
                    "phase", "verified",
                    "ticks", consecutive)
                return nil
            }
        }
    }
}
```

**Verify-failure response shape (locked in CONTEXT.md Area 3):**

```json
{
  "error": "verify_failed",
  "reason": "container restarted 3 times in 15s",
  "exit_code": null,
  "restart_count": 3,
  "running": false,
  "container_id": "abc123def456"
}
```

HTTP 500. State writes: `ActionError = "verify_failed: ..."`, `ActionInFlight = ""`. UI Phase 5 offers Rollback if `PreviousDigest != ""`.

**Confidence:** HIGH. `container.InspectResponse` shape verified via `_sdk_shape.txt`; `State.Running` / `RestartCount` / `Health` are standard moby API fields. `[VERIFIED: internal/docker/_sdk_shape.txt:25-50]` `[CITED: pkg.go.dev/github.com/moby/moby/api/types/container InspectResponse]`

### Pattern 6: STATE-04 SIGKILL Fault-Injection Test Harness

**What:** A fork-exec test that spawns a helper binary which writes state in a tight loop, then the parent test sends SIGKILL at randomized 1–50ms intervals. After each kill, the parent re-opens the state file and asserts it parses cleanly (either prior content or new content — never truncated).

**Why this validates STATE-04:** The renameio + parent-dir-fsync pattern (already in `internal/state/persist.go`) is the *theoretical* atomic-write contract. The SIGKILL test is the *empirical* verification.

**Why a separate helper binary (not in-process):** SIGKILL kills the entire process. If we ran the writer in-process, the parent test would die too. Forking is the only way to send SIGKILL "from outside."

**Why a build tag (`//go:build sigkill_test`):** This test is slow (~5–10 seconds × 100 iterations) and OS-coupled. Default `go test ./...` should not run it; it gates `make test-sigkill` only.

**Example — parent test (`internal/state/store_sigkill_test.go`):**

```go
//go:build sigkill_test
// +build sigkill_test

// To run: go test -tags=sigkill_test -run TestSIGKILL ./internal/state/...
// Documented in README + the test file's own doc comment.

package state_test

import (
    "encoding/json"
    "math/rand"
    "os"
    "os/exec"
    "path/filepath"
    "syscall"
    "testing"
    "time"

    "github.com/centroid-is/hmi-update/internal/state"
)

const sigkillIterations = 100

func TestSIGKILLDuringWrite(t *testing.T) {
    // Build the helper binary once at test entry.
    tmpDir := t.TempDir()
    helperBin := filepath.Join(tmpDir, "sigkillhelper")
    statePath := filepath.Join(tmpDir, "state.json")

    cmd := exec.Command("go", "build", "-o", helperBin, "../../cmd/sigkillhelper")
    if out, err := cmd.CombinedOutput(); err != nil {
        t.Fatalf("build helper: %v: %s", err, out)
    }

    rng := rand.New(rand.NewSource(time.Now().UnixNano()))

    for i := 0; i < sigkillIterations; i++ {
        // Spawn helper; it writes to statePath in a loop.
        helper := exec.Command(helperBin, statePath)
        if err := helper.Start(); err != nil {
            t.Fatalf("iter %d: start helper: %v", i, err)
        }

        // Random delay 1-50ms then SIGKILL.
        delay := time.Duration(1+rng.Intn(50)) * time.Millisecond
        time.Sleep(delay)
        _ = helper.Process.Signal(syscall.SIGKILL)
        _ = helper.Wait() // reap; ignore exit code (SIGKILL = -9)

        // Verify: open the file, parse, expect either prior content
        // or new content — never an unmarshal error.
        data, err := os.ReadFile(statePath)
        if err != nil {
            // File doesn't exist yet on first iteration if SIGKILL beat the helper's first write.
            // Treat empty as acceptable for iteration 0 only.
            if i == 0 && os.IsNotExist(err) {
                continue
            }
            t.Fatalf("iter %d (delay %v): read state: %v", i, delay, err)
        }
        if len(data) == 0 {
            // Empty file is acceptable per state.NewStore contract (treated as "no state yet").
            continue
        }
        var st state.State
        if err := json.Unmarshal(data, &st); err != nil {
            t.Fatalf("iter %d (delay %v): file CORRUPTED after SIGKILL:\n  err: %v\n  data: %q",
                i, delay, err, string(data))
        }
        // Optional stronger check: assert state.Version == 1
        if st.Version != state.SchemaVersion {
            t.Fatalf("iter %d: unexpected version %d in parsed state", i, st.Version)
        }
    }

    t.Logf("PASSED %d SIGKILL iterations with zero corruption", sigkillIterations)
}
```

**Example — helper binary (`cmd/sigkillhelper/main.go`):**

```go
// Helper binary spawned by internal/state/store_sigkill_test.go.
// Writes incrementing state values in a tight loop until SIGKILLed.
// arm64 build deferred (V2-ARM64); amd64-only per CLAUDE.md "Platform".

package main

import (
    "fmt"
    "os"
    "time"

    "github.com/centroid-is/hmi-update/internal/state"
)

func main() {
    if len(os.Args) != 2 {
        fmt.Fprintln(os.Stderr, "usage: sigkillhelper <state-path>")
        os.Exit(2)
    }
    statePath := os.Args[1]
    store, err := state.NewStore(statePath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "NewStore: %v\n", err)
        os.Exit(1)
    }

    counter := 0
    for {
        counter++
        // Update with a small but distinct payload so each iteration
        // produces a fresh on-disk file. The Service field embeds the
        // counter so a corrupted partial-write would manifest as a
        // truncated JSON document.
        if err := store.Update(func(st *state.State) {
            st.Containers["svc"] = state.Container{
                Service:       "svc",
                Image:         "test/image",
                Tag:           "latest",
                CurrentDigest: fmt.Sprintf("sha256:%064d", counter),
            }
        }); err != nil {
            // Persist error -> log to stderr but keep looping. The parent
            // test's contract is that *some* write completed before
            // SIGKILL; we want the loop to keep producing fresh writes.
            fmt.Fprintf(os.Stderr, "Update %d: %v\n", counter, err)
        }
        // Brief sleep so the parent's SIGKILL has a chance to land
        // mid-write rather than between writes.
        time.Sleep(100 * time.Microsecond)
    }
}
```

**Makefile target (suggested):**

```makefile
.PHONY: test-sigkill
test-sigkill:
	go test -tags=sigkill_test -count=1 -run TestSIGKILL ./internal/state/...
```

**Confidence:** HIGH. Pattern is standard Go test idiom (fork-and-kill helper); the renameio + dirsync underpinning is already verified by Phase 1's TestPersistAtomicity. `[CITED: github.com/google/renameio README]` `[VERIFIED: internal/state/persist.go:36-58]`

### Pattern 7: Middleware Stack — Self-Protection + Safety-Label + Service-Name

**What:** Three HTTP middleware layers compose around the three action handlers. Order is critical (locked in CONTEXT.md Area 4):

1. **Service-name validation** (regex `^[a-zA-Z0-9._-]+$` compiled once at boot) → 400 on mismatch
2. **State lookup** (in-memory `state.Store.Get().Containers[svc]`) → 404 on container_not_found
3. **Self-protection** (compare `r.PathValue("service")` to `HMI_UPDATE_SELF_SERVICE` env, default `"hmi-update"`) → 409 self_protection
4. **Safety-label** (per-action: update reads `Labels["hmi-update.allow-update"]`, rollback reads `Labels["hmi-update.allow-rollback"]`; force-pull is exempt) → 409 action_disabled_by_label

**Why this order:** Service-name first because it's cheap and pure (no state access). State lookup next so subsequent middleware can branch on Container fields. Self-protection before safety-label because the self-protection 409 takes precedence over any label config (an operator-set `allow-update=true` on `hmi-update` itself must NOT override the server-side refuse).

**Example (canonical body for `internal/actions/middleware.go`):**

```go
package actions

import (
    "encoding/json"
    "net/http"
    "regexp"

    "github.com/centroid-is/hmi-update/internal/state"
)

// serviceNameRegex matches the locked ACT-10 allowlist.
// Source: CONTEXT.md Area 4 + PITFALLS.md Pitfall 13.
// Compiled once at boot via init(); Compile error here is a programmer
// error and fails the test suite immediately.
var serviceNameRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// Action discriminates which safety label to consult.
type Action string

const (
    ActionUpdate    Action = "update"
    ActionRollback  Action = "rollback"
    ActionForcePull Action = "force-pull"
)

// ValidateServiceName returns the path-parameter service name after
// regex validation, or "" + writes 400 to w.
//
// This is the OUTER middleware — runs first on every action endpoint.
func validateServiceName(w http.ResponseWriter, r *http.Request) (string, bool) {
    svc := r.PathValue("service")
    if !serviceNameRegex.MatchString(svc) {
        writeJSONError(w, http.StatusBadRequest, "invalid_service_name",
            "service name must match ^[a-zA-Z0-9._-]+$")
        return "", false
    }
    return svc, true
}

// lookupContainer returns the Container snapshot from in-memory state.
// 404 if absent.
func (o *Orchestrator) lookupContainer(w http.ResponseWriter, svc string) (state.Container, bool) {
    snapshot := o.store.Get()
    c, ok := snapshot.Containers[svc]
    if !ok {
        writeJSONError(w, http.StatusNotFound, "container_not_found", svc)
        return state.Container{}, false
    }
    return c, true
}

// checkSelfProtection writes 409 and returns false if svc matches
// HMI_UPDATE_SELF_SERVICE (default "hmi-update"). The self-service
// name is captured at Orchestrator construction (main.go reads the env).
func (o *Orchestrator) checkSelfProtection(w http.ResponseWriter, svc string) bool {
    if svc == o.selfService {
        writeJSONError(w, http.StatusConflict, "self_protection",
            "see PROJECT.md 'Manual self-upgrade procedure'")
        return false
    }
    return true
}

// checkSafetyLabel applies SAFE-01 / SAFE-02 (force-pull is exempt — SAFE-03).
func checkSafetyLabel(w http.ResponseWriter, c state.Container, action Action) bool {
    switch action {
    case ActionUpdate:
        if c.Labels["hmi-update.allow-update"] == "false" {
            writeJSONError(w, http.StatusConflict, "action_disabled_by_label",
                "hmi-update.allow-update=false")
            return false
        }
    case ActionRollback:
        if c.Labels["hmi-update.allow-rollback"] == "false" {
            writeJSONError(w, http.StatusConflict, "action_disabled_by_label",
                "hmi-update.allow-rollback=false")
            return false
        }
    case ActionForcePull:
        // SAFE-03: force-pull is read-only with respect to the running
        // container (just refreshes the local image cache). Not gated
        // by safety labels.
    }
    return true
}

func writeJSONError(w http.ResponseWriter, status int, code, detail string) {
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(map[string]string{
        "error":  code,
        "detail": detail,
    })
}
```

**SAFE-03 verification (poll loop ignores allow-* labels):** The verify is a *code-grep test* — assert that `internal/poll/poller.go::eligibleContainers` does NOT branch on `Labels["hmi-update.allow-update"]` or `Labels["hmi-update.allow-rollback"]`. The Phase 3 implementation already complies (verified at `internal/poll/poller.go:308-324` — only `Pinned`, `Stopped`, and `Image == ""` are filters). The Phase 4 test pins this contract with a `grep`-based assertion:

```go
func TestSAFE03_PollIgnoresActionLabels(t *testing.T) {
    // Sanity: read internal/poll/poller.go and confirm no reference to
    // hmi-update.allow-update or hmi-update.allow-rollback in
    // eligibleContainers' implementation.
    data, _ := os.ReadFile("../../internal/poll/poller.go")
    if bytes.Contains(data, []byte("hmi-update.allow-")) {
        t.Errorf("SAFE-03 violation: poll/poller.go references hmi-update.allow-* — should only be in actions/middleware.go")
    }
}
```

**Confidence:** HIGH (locked in CONTEXT.md Area 4; pattern matches Phase 2 healthz handler structure).

### Pattern 8: HTTP Handler Wiring (Go 1.22+ ServeMux)

**What:** Three new routes using Go 1.22+ method-scoped path patterns with `{service}` path variable.

**Example (canonical body for `internal/api/handlers_actions.go` + route registration in `server.go`):**

```go
// server.go — add to routes()
func (s *Server) routes() {
    s.mux.HandleFunc("GET /healthz", s.healthz)
    s.mux.HandleFunc("GET /api/state", s.getState)
    // Phase 4 action endpoints (ACT-01..05).
    s.mux.HandleFunc("POST /api/containers/{service}/update", s.handleUpdate)
    s.mux.HandleFunc("POST /api/containers/{service}/rollback", s.handleRollback)
    s.mux.HandleFunc("POST /api/containers/{service}/force-pull", s.handleForcePull)
    s.mux.Handle("/", newStaticHandler())
}

// handlers_actions.go
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
    svc, ok := validateServiceName(w, r)
    if !ok { return }
    c, ok := s.orchestrator.LookupContainer(w, svc)
    if !ok { return }
    if !s.orchestrator.CheckSelfProtection(w, svc) { return }
    if !actions.CheckSafetyLabel(w, c, actions.ActionUpdate) { return }

    // Delegate to orchestrator; orchestrator handles mutex, idempotency,
    // pull, recreate, verify, state mutations.
    result, err := s.orchestrator.Update(r.Context(), svc)
    if err != nil {
        s.orchestrator.WriteActionError(w, svc, err)
        return
    }
    s.orchestrator.WriteActionResult(w, result)
}
```

**Note on `internal/api/server.go` constructor signature change:** `NewServer` currently takes `(store, dockerClient, composeReader)`. Phase 4 adds the orchestrator: `NewServer(store, dockerClient, composeReader, orchestrator)`. All existing test wiring must update. Phase 1's existing `TestServer_NewServer_*` tests need a 4th argument (can pass a no-op orchestrator).

**Confidence:** HIGH (Go 1.22+ routing verified at [go.dev/blog/routing-enhancements](https://go.dev/blog/routing-enhancements); existing Phase 2 pattern `GET /healthz` already uses method-scoped routes).

### Anti-Patterns to Avoid

- **Holding the per-service mutex while pulling**: The mutex IS held during pull (this is intentional — concurrent same-service pull doesn't conflict at the daemon, but we want the state writes serialized). Don't optimize this away; the locked design is correct.
- **`state.Store.Update` inside the action body for intermediate progress**: Wrong. Use the channel + `KindActionStart`/`KindActionProgress` so DETECT-10's single-consumer invariant carries forward. Direct `store.Update` would create a second writer and the invariant breaks.
- **Calling `compose up -d --force-recreate` without `CheckUnchanged` first**: The compose file may have been replaced atomically (Pitfall 10). Always check `composeReader.CheckUnchanged(ctx)` before the runner call; return 412 on `ErrComposeFileMoved`.
- **Trusting `docker compose up` exit 0 as success**: Pitfall 12 — exit 0 means "compose accepted the spec," not "the container is healthy." Always run the 15-second verify-after-recreate loop.
- **Letting verify-after-recreate block forever**: The healthcheck opt-in has a hard 60s ceiling. Even with no health status reported, the loop returns soft-success after 60s rather than blocking the operator's button.
- **Reading docker labels via `ContainerInspect` in middleware**: Middleware reads from `state.Store.Get()` (in-memory cache populated by Phase 2's discoverer). Docker inspect in the hot path of every POST is wasteful and creates a window where the cache is stale relative to docker. OBS-03 explicitly demands `GET /api/state` is no-I/O; the same discipline applies to the action middleware.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Atomic file write under crash | Hand-rolled tmp+rename+fsync | `github.com/google/renameio/v2` (already wired) + the existing dir-fsync wrapper in `internal/state/persist.go` | Phase 1 already ships this. Phase 4 only adds the SIGKILL test. |
| Per-service serialization | Channel-based job queue with worker per service | `map[string]*sync.Mutex` + `TryLock` | Simpler; non-blocking 409 is exactly what ACT-08 wants. |
| Docker pull progress parsing | Hand-rolled streaming HTTP client | `docker.Client.ImagePull` (moby SDK; already on facade) + JSON-decode the stream | moby v0.4.1 returns a typed `ImagePullResponse`. The pull stream's `aux` field IS the digest source (DETECT-02 invariant: never re-hash the body). |
| Registry digest fetch for verify | Hand-rolled HTTP HEAD + Bearer-token flow | `registry.Resolver.Digest` (Phase 3; already wired with redacting transport) | Phase 3 closed Pitfalls 1, 2, 3. Don't re-open them. |
| `docker compose up -d --force-recreate` invocation | `github.com/docker/compose/v2` Go SDK | `exec.CommandContext` via `internal/compose.Runner` | Compose SDK drags BuildKit/containerd (~30 MB) — blows the image-size budget (CLAUDE.md C1). |
| Cron-vs-manual race resolution | Distributed lock / etcd / second mutex | Existing channel pattern (DETECT-10) + per-field Apply closures | Single-consumer invariant means last-writer-wins per field; cron's KindFetchResult writes AvailableDigest/LastPolledAt; actions' KindActionResult writes CurrentDigest/PreviousDigest/ActionInFlight/ActionError. They don't overlap. |
| Container health detection | Custom HTTP probe / TCP poke | `ContainerInspect.State.Health.Status` (moby SDK; native Docker HEALTHCHECK) | Operators already configure HEALTHCHECK in their compose files; we honor it. |
| Self-update of `hmi-update` | Ephemeral orchestrator container (Watchtower-style) | 409 self_protection + documented manual procedure | Out of scope for v1 (CLAUDE.md C1); the manual procedure is 3 commands in a shell. |

**Key insight:** Every line of code added to Phase 4 must justify itself against an existing primitive in Phases 1–3. The architecture is fully decided; this phase is purely glue. The temptation to "improve" any of the locked-in patterns (e.g., make ActionInFlight a typed enum, or use sync.Map, or add a job queue) should be resisted — the planner picks the boring, verifiable shape every time.

## Runtime State Inventory

> **Not applicable to this phase.** Phase 4 is greenfield (new package `internal/actions`, new fields on existing structs, new HTTP routes). No renames, no migrations.

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | None — Phase 4 adds two new fields to `state.Container`; existing on-disk Phase 3 state files load cleanly with `ActionInFlight=""` and `ActionError=""` (both `omitempty`). Forward-compat verified by the existing TestPhase3SchemaFields_ForwardCompat_Phase2OnDisk pattern; Phase 4 should add a sibling `TestPhase4SchemaFields_ForwardCompat_Phase3OnDisk`. | Test file addition only. |
| Live service config | None — Phase 4 does not change compose service definitions in the production deployment. The e2e `compose.test.yml` gains a `hmi-update.allow-update=false` label on `timescaledb-stub` for the SAFE-01 test (already present per `e2e/compose.test.yml:79-89` — verify and adjust if absent). | e2e compose file: add `hmi-update.allow-update: "false"` to `timescaledb-stub` labels (if not already present). |
| OS-registered state | None. | — |
| Secrets/env vars | Three new env vars introduced this phase: `HMI_UPDATE_SELF_SERVICE` (default `"hmi-update"`), `HMI_UPDATE_VERIFY_WINDOW_S` (default `15`), `HMI_UPDATE_HEALTHCHECK_WINDOW_S` (default `60`). All have working defaults; no existing env consumers break. | Document in PROJECT.md "Configuration Knobs" section. |
| Build artifacts | None — `cmd/sigkillhelper` is a NEW binary that builds via `go build` from source; nothing stale carries over. | First build of helper happens inside the SIGKILL test (`go build -o tmpDir/sigkillhelper ./cmd/sigkillhelper`); ephemeral, not packaged into the production image. |

**Verified:** Phase 4 introduces no runtime state migrations.

## Common Pitfalls

### Pitfall A: Exit code 0 from `docker compose up -d` does NOT mean the container is healthy

**What goes wrong:** Operator clicks Update, the runner gets exit 0, the API returns 200, the UI shows green. Two seconds later the operator notices the screen is broken / service unreachable. Pitfall 12 from PITFALLS.md.

**Why it happens:** `docker compose up -d` is fire-and-forget. Exit 0 means "Docker accepted the spec," nothing about runtime health.

**How to avoid:** The 15-second verify-after-recreate loop (Pattern 5 above) closes this. Every Update / Rollback action MUST verify before reporting success.

**Warning signs:** Update returns 200 but the operator reports stale behavior. State file says new digest but `docker ps` shows `Restarting (1) 5 seconds ago`.

### Pitfall B: `compose up -d --force-recreate` doesn't pull; silently uses stale local image

**What goes wrong:** PITFALLS.md Pitfall 4. The locked design separates `docker pull` (step 5 in CONTEXT.md Area 1) from `docker compose up -d --force-recreate` (step 8). If the pull silently fails (network 5xx, daemon timeout), compose recreates against the OLD local image. State file would record the new digest as `CurrentDigest`, but the running container is stale.

**Why it happens:** Compose treats `image:` as a target; if a local tag matches, it uses it. `pull_policy` defaults to `missing` — present tags are not re-pulled.

**How to avoid:** Phase 4's Pattern 2 (Image Pull + Digest Verify) — after `ImagePull` returns, parse the `aux` digest from the progress stream AND cross-check against `registry.Resolver.Digest`. If they don't match, abort BEFORE the compose recreate. Verify-after-recreate (Pattern 5) is the second line of defense.

**Warning signs:** Same as Pitfall A.

### Pitfall C: SIGKILL during state write leaves the file truncated

**What goes wrong:** PITFALLS.md Pitfall 7. STATE-04 requirement.

**Why it happens:** `os.WriteFile` is not atomic; truncate-then-write leaves a window where the file is 0 bytes.

**How to avoid:** Phase 1 already shipped `renameio.WriteFile` + parent-directory `fsync`. Phase 4's STATE-04 test (Pattern 6) is the empirical verification that this works under SIGKILL.

**Warning signs:** `docker logs hmi-update` shows "decode state: unexpected EOF" after a crash. The mtime updated but the file is 0 bytes.

### Pitfall D: Display blackout when recreating `flutter` / `weston` containers

**What goes wrong:** PITFALLS.md Pitfall 5. Recreating the container that draws the HMI screen blacks out the operator's display for 5–30s.

**How to avoid:** Out of scope for Phase 4 — Phase 6 (UX-01..03) makes the explicit product decision. Phase 4 must, however, *log the wall-clock duration of the stop→running gap* so operator-visible downtime is measurable (the slog event schema includes `duration_ms`, which captures this).

**Warning signs:** Field engineer reports "screen went black for ages."

### Pitfall E: Concurrent updates from double-click or cron-vs-manual race

**What goes wrong:** PITFALLS.md Pitfall 11. Operator double-clicks; two POSTs fire; state writes race.

**How to avoid:** Per-service `sync.Mutex.TryLock` (Pattern 4). Returns 409 immediately on contention; UI re-enables button on response (Phase 5's job).

**Warning signs:** `previous_digest == current_digest` after a sequence of clicks. Two near-simultaneous "Updated X" log entries.

### Pitfall F: Self-update kills the orchestrator mid-recreate

**What goes wrong:** PITFALLS.md Pitfall 6. `hmi-update` can't recreate itself — the calling process dies mid-recreate, the compose CLI subprocess is reparented, the result is undefined (zombie container or no container).

**How to avoid:** Server-side 409 self_protection (Pattern 7). Documented manual procedure in PROJECT.md.

**Warning signs:** `hmi-update` container disappears from `docker ps` after a self-update attempt; the operator has to SSH in.

### Pitfall G: Compose-file inode drift between boot and action

**What goes wrong:** PITFALLS.md Pitfall 10. Operator edits the compose file with an atomic-save editor mid-day; the inode changes; `hmi-update`'s cached compose path may point at a stale inode.

**How to avoid:** Already handled in Phase 2 — `compose.Reader.CheckUnchanged(ctx)` (verified at `internal/compose/reader.go:159`). Phase 4 MUST call this at the start of every Update/Rollback (before mutex acquisition) and return 412 on `ErrComposeFileMoved`.

**Warning signs:** Update succeeds but the wrong service is recreated, or compose errors "service X not found in compose file."

### Pitfall H: SSRF / path traversal via service-name parameter

**What goes wrong:** PITFALLS.md Pitfall 13. Malicious LAN client probes `POST /api/containers/../../../etc/passwd/update`.

**How to avoid:** Pattern 7 — service-name regex `^[a-zA-Z0-9._-]+$` + in-memory map lookup. Never construct shell strings with operator input; argv discipline in the compose runner (Pattern 1).

**Warning signs:** 4xx rates climb suddenly from unexpected source IPs. Requests for service names not in the compose file.

## Runtime Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `docker` CLI (with compose v2 plugin) | `internal/compose.Runner` via `os/exec` | ✓ (e2e environment has it; production HMIs are Centroid field machines with docker preinstalled per CLAUDE.md C3) | v2.20+ documented in PROJECT.md for `--wait` and stable `compose ps --format=json` (Phase 4 doesn't use `--wait`, but flag stability matters) | — (compose v2 is a hard requirement; surface boot-time failure via `exec.LookPath("docker")` returning ENOENT) |
| Docker daemon at `/var/run/docker.sock` | `internal/docker.Client` + compose CLI | ✓ (Phase 2 wired this via DOCK-01..03) | — | Existing `/healthz` distinguishes EACCES / missing / unreachable (Pitfall 9). |
| moby/moby/client v0.4.1 | All docker interactions | ✓ (go.mod) | v0.4.1 | — |
| renameio/v2 v2.0.2 | State atomic write | ✓ (go.mod) | v2.0.2 | — |
| Go 1.22+ ServeMux | Method-scoped routes + PathValue | ✓ (go.mod: go 1.26) | go 1.26 | — |
| sync.Mutex.TryLock | Per-service mutex | ✓ (Go 1.18+, our Go 1.26 has it) | Go 1.18+ | — |

**Missing dependencies with no fallback:** None.

**Missing dependencies with fallback:** None.

**Verification command (for plan-checker):**

```bash
# At Phase 4 boot, this would surface a clear failure:
go build -o /tmp/hmi-update ./cmd/hmi-update && \
  HMI_UPDATE_SELF_SERVICE=hmi-update \
  HMI_UPDATE_COMPOSE_PATH=/path/to/compose.yml \
  /tmp/hmi-update
# Expected: log.Fatalf "compose.NewRunner: docker CLI not found in PATH..."
```

## Validation Architecture

> Workflow nyquist_validation may be set/absent — including this section as part of standard discipline.

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` (table-driven) + `@playwright/test` (existing) |
| Config file | `e2e/playwright.config.ts` (exists); Go tests use stdlib `testing` |
| Quick run command | `go test -race ./internal/actions/... ./internal/compose/... ./internal/state/...` |
| Full suite command | `make e2e-cron-fast` (Playwright against compose stack) |
| SIGKILL test | `make test-sigkill` (build-tagged; not in default `make test`) |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| ACT-01 | Update happy path: pull → verify → recreate → verify-after-recreate | e2e | `npx playwright test update-flow.spec.ts` | ❌ Wave 0 |
| ACT-02 | Update completes ≤30s; new digest visible in state | e2e (assertion within update-flow.spec.ts) | same | ❌ Wave 0 |
| ACT-03 | Rollback flow: ImageTag → recreate → verify | e2e | `npx playwright test rollback-flow.spec.ts` | ❌ Wave 0 |
| ACT-04 | Offline rollback (docker network disconnect zot) | e2e (rollback-flow.spec.ts test 2) | same + `disconnect-network.ts` fixture | ❌ Wave 0 |
| ACT-05 | Force-pull default vs `?recreate=true` | e2e | `npx playwright test force-pull.spec.ts` | ❌ Wave 0 (or merge into update-flow.spec.ts) |
| ACT-06 | Update on already-:latest returns no_op | e2e | `npx playwright test idempotency.spec.ts` | ❌ Wave 0 |
| ACT-07 | Rollback to current digest returns no_op | e2e (idempotency.spec.ts test 2) | same | ❌ Wave 0 |
| ACT-08 | Double-click → 409 | e2e | `npx playwright test concurrent-actions.spec.ts` | ❌ Wave 0 |
| ACT-08 | Concurrent same-service from Go (race-clean) | unit | `go test -race -count=50 ./internal/actions/... -run TestLockService_Concurrent` | ❌ Wave 0 |
| ACT-09 | Self-protection 409 | e2e | `npx playwright test self-protection.spec.ts` | ❌ Wave 0 |
| ACT-10 | Service-name regex 400 | unit + e2e | `go test ./internal/actions/... -run TestValidateServiceName` + spec | ❌ Wave 0 |
| ACT-11 | Action response includes current_digest + previous_digest | e2e (assertion within update-flow.spec.ts + rollback-flow.spec.ts) | same | ❌ Wave 0 |
| ACT-12 | Restart-persistence: `docker compose restart hmi-update` preserves state | e2e | `npx playwright test restart-persistence.spec.ts` | ❌ Wave 0 |
| SAFE-01 | Update 409 for `hmi-update.allow-update=false` | e2e | `npx playwright test safety-labels.spec.ts` | ❌ Wave 0 |
| SAFE-02 | Rollback 409 for `hmi-update.allow-rollback=false` | e2e (safety-labels.spec.ts test 2) | same | ❌ Wave 0 |
| SAFE-03 | Poll still ticks for safety-locked containers | e2e + grep-test | `safety-labels.spec.ts` + `go test ./internal/actions/... -run TestSAFE03` | ❌ Wave 0 |
| STATE-04 | SIGKILL-mid-write leaves file parseable | unit (build-tagged) | `go test -tags=sigkill_test ./internal/state/... -run TestSIGKILL` | ❌ Wave 0 |
| STATE-05 | UID/GID install documentation | manual (verified in PROJECT.md presence) | `grep -q "chown 65532" PROJECT.md` | ❌ Wave 0 |
| OBS-01 | Structured slog events for every action | unit | `go test ./internal/actions/... -run TestSlog_ActionEventSchema` | ❌ Wave 0 |
| OBS-03 | `GET /api/state` is no-I/O | unit | `go test ./internal/api/... -run TestGetState_NoIO` (using a fake docker.Client that panics on any call) | ❌ Wave 0 |
| Verify-failed branch | Verify-after-recreate fails on crash-loop | e2e | `npx playwright test verify-failed.spec.ts` | ❌ Wave 0 |

### Sampling Rate

- **Per task commit:** `go test -race ./internal/actions/... ./internal/compose/... ./internal/state/...` (~15s)
- **Per wave merge:** `make e2e-cron-fast` (~3–5 min)
- **Phase gate:** Full suite green + `make test-sigkill` passes 100 iterations + manual smoke note in SMOKE.md (CLAUDE.md C4 "TDD-first, manual smoke before done")
- **Before `/gsd-verify-work`:** All of the above green

### Wave 0 Gaps

The following test files do NOT exist yet and MUST land in Wave 0 of each plan before implementation:

- [ ] `internal/actions/orchestrator_test.go` — covers ACT-01..05, ACT-06/07 idempotency, ACT-09 self-protection
- [ ] `internal/actions/mutex_test.go` — covers ACT-08 (race-clean)
- [ ] `internal/actions/middleware_test.go` — covers ACT-09, ACT-10, SAFE-01..03
- [ ] `internal/actions/verify_test.go` — covers verify-after-recreate happy path + verify-failed
- [ ] `internal/compose/runner_test.go` — covers compose CLI invocation + stderr capture
- [ ] `internal/state/store_sigkill_test.go` + `cmd/sigkillhelper/main.go` — covers STATE-04 (build-tagged)
- [ ] `internal/api/handlers_actions_test.go` — covers HTTP-layer routing and error mapping
- [ ] `internal/api/getstate_noio_test.go` — covers OBS-03 (uses a fake docker.Client that panics on Inspect)
- [ ] 8 e2e specs: `e2e/tests/update-flow.spec.ts`, `rollback-flow.spec.ts`, `idempotency.spec.ts`, `concurrent-actions.spec.ts`, `self-protection.spec.ts`, `safety-labels.spec.ts`, `restart-persistence.spec.ts`, `verify-failed.spec.ts`
- [ ] `e2e/fixtures/disconnect-network.ts` — the offline-rollback helper

## Code Examples

### Disconnect-Network Fixture for Offline Rollback (ACT-04)

```typescript
// e2e/fixtures/disconnect-network.ts
//
// ACT-04: Rollback MUST work with the registry network detached. This
// fixture wraps `docker network disconnect` so the rollback-flow spec
// can simulate an offline HMI.
//
// Pattern matches push-image.ts (execSync child_process). The compose
// stack network name is `e2e_default` by default (compose project name
// "e2e" + suffix "_default") — derive from `docker compose ps --format=json`
// if it varies on different runners.

import { execSync } from 'node:child_process';

/**
 * Identify the compose network name. The compose project is named after
 * the directory (`e2e`), so the default network is `e2e_default`. We
 * derive it dynamically via `docker compose config --format json` to
 * survive environment differences (e.g. CI may set COMPOSE_PROJECT_NAME).
 */
function getComposeNetwork(): string {
  // Quick approach: read `docker network ls` output and find one whose
  // name ends with "_default" and contains "e2e".
  const networks = execSync(`docker network ls --format '{{.Name}}'`, {
    encoding: 'utf8',
  });
  const match = networks.split('\n').find((n) => /e2e.*_default$/.test(n));
  if (!match) {
    throw new Error(
      `Could not find e2e compose network in:\n${networks}\nIs the stack up?`,
    );
  }
  return match;
}

/**
 * Disconnect the zot service from the compose network. After this call,
 * hmi-update can still talk to the docker daemon, but the registry is
 * unreachable. ImagePull will fail; ImageTag (local re-tag) will succeed.
 */
export function disconnectZotFromNetwork(): void {
  const net = getComposeNetwork();
  execSync(`docker network disconnect ${net} zot`, { stdio: 'inherit' });
}

/**
 * Re-connect the zot service. Used in test cleanup to restore the
 * stack for subsequent tests.
 */
export function reconnectZot(): void {
  const net = getComposeNetwork();
  execSync(`docker network connect ${net} zot`, { stdio: 'inherit' });
}
```

**Usage in rollback-flow.spec.ts (sketch):**

```typescript
import { disconnectZotFromNetwork, reconnectZot } from '../fixtures/disconnect-network';

test('rollback-flow: ACT-04 rollback works with registry detached', async ({ request }) => {
  // 1. Push fresh manifest, wait for update_available
  // 2. POST /update — establishes previous_digest
  // 3. Disconnect zot from network
  disconnectZotFromNetwork();
  try {
    // 4. POST /rollback — must succeed (ImageTag is local)
    const resp = await request.post('/api/containers/stub-watched-container/rollback');
    expect(resp.ok()).toBe(true);
    const body = await resp.json();
    expect(body.current_digest).toBeTruthy();
    // 5. State assertion: current/previous have swapped
  } finally {
    reconnectZot(); // always restore
  }
});
```

**Source:** Pattern derived from `e2e/fixtures/push-image.ts` (existing) and `docker network disconnect` semantics ([docs.docker.com/reference/cli/docker/network/disconnect](https://docs.docker.com/reference/cli/docker/network/disconnect/)).

### Restart-Persistence Spec (ACT-12)

```typescript
// e2e/tests/restart-persistence.spec.ts (ACT-12)
//
// `docker compose restart hmi-update` re-execs the binary; main.go re-runs
// state.NewStore at boot; the in-memory snapshot is re-seeded from disk.
// After restart, /api/state must show the same containers + digests +
// previous_digests.

import { execSync } from 'node:child_process';
import { setTimeout as sleep } from 'node:timers/promises';
import { expect, test } from '@playwright/test';
import { pushFreshManifest } from '../fixtures/push-image';

test('restart-persistence: ACT-12 digests + previous_digest survive docker compose restart hmi-update', async ({ request }) => {
  // Setup: do an Update so we have a non-empty previous_digest
  pushFreshManifest('centroid-is/stub');
  await sleep(10_000); // wait for cron flip
  const updResp = await request.post('/api/containers/stub-watched-container/update');
  expect(updResp.ok()).toBe(true);
  const updBody = await updResp.json();
  const currentDigest = updBody.current_digest;
  const previousDigest = updBody.previous_digest;
  expect(currentDigest).toMatch(/^sha256:/);
  expect(previousDigest).toMatch(/^sha256:/);

  // Restart hmi-update; the existing test pattern (compose-drift.spec.ts:afterAll)
  // already uses `docker compose restart` and polls /healthz.
  execSync('docker compose -f compose.test.yml restart hmi-update', { stdio: 'inherit' });

  // Poll /healthz until 200
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    try {
      const h = await request.get('/healthz');
      if (h.ok()) break;
    } catch {}
    await sleep(500);
  }

  // Verify state is preserved
  const stateResp = await request.get('/api/state');
  expect(stateResp.ok()).toBe(true);
  const state = await stateResp.json();
  expect(state.containers['stub-watched-container'].current_digest).toBe(currentDigest);
  expect(state.containers['stub-watched-container'].previous_digest).toBe(previousDigest);
});
```

**Source:** Pattern derived from `e2e/tests/compose-drift.spec.ts` afterAll (already uses `docker compose restart`).

### Action Slog Event Schema (OBS-01)

```go
// internal/actions/orchestrator.go — slog event constants
//
// Standard event names per OBS-01. Dotted convention matches Phase 3's
// existing events (registry.fetch, poll.sweep.end, etc.). The exact
// spelling is part of the operability contract; downstream tooling
// (Datadog parsers, log dashboards) will key on these strings.

const (
    slogActionStart         = "action.start"
    slogActionPhase         = "action.phase"
    slogActionComplete      = "action.complete"
    slogActionVerifyFailed  = "action.verify_failed"
    slogActionComposeFailed = "action.compose_failed"
    slogActionPullFailed    = "action.pull_failed"
)

// Standard fields (always present; some may be empty):
//   - service        string (the compose service name)
//   - action         string ("update" / "rollback" / "force_pull")
//   - before         string (sha256:... pre-action CurrentDigest)
//   - after          string (sha256:... post-action CurrentDigest; empty on failure)
//   - exit_code      int    (compose process exit code; 0 on success; -1 on no-compose-call paths)
//   - duration_ms    int64  (end-to-end wall-clock for the action)
//   - phase          string (for action.phase: "pulled" / "recreated" / "verified")
//   - err            error  (only on failure events)
//   - restart_count  int    (only on action.verify_failed)
//   - running        bool   (only on action.verify_failed)
//
// Redaction: Phase 3's newRedactingHandler (cmd/hmi-update/main.go:90)
// catches accidental Bearer/Basic strings. compose stderr does NOT
// contain auth headers (docker compose talks to the local daemon, not
// to a registry), so the redacting transport isn't strictly required
// for runner output — but the slog handler-level regex still applies
// belt-and-braces.
```

### Phase-4-Specific Pitfall: Verify Loop Tick Boundary

When the consecutive-tick counter is at `target-1` and a tick completes successfully, the loop returns success. But if ctx is canceled in the same select cycle, ctx.Done() may be selected first depending on Go's pseudo-random select. This is fine — ctx cancellation should preempt the success path. The test should explicitly verify this: spawn a verify goroutine, cancel ctx at tick 14, expect `ErrVerifyCanceled` (not `nil`).

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Hand-rolled `os.WriteFile` + `os.Rename` | `renameio.WriteFile` + parent-directory `fsync` | Phase 1 (2026-05-13) | Atomic, durable across host reboot. Phase 4 just adds the SIGKILL test on top. |
| Single global mutex for action handlers | Per-service mutex map with `TryLock` | Phase 4 (this phase) | Cross-service parallelism enabled. ACT-08 satisfied. |
| Hand-rolled docker pull progress parsing | moby v0.4.1 `ImagePullResponse.JSONMessages` iterator | Phase 4 (this phase) | Less code; SDK shape is the trust root for Pitfall 1 verify. |
| `time.After` in verify loops | `time.NewTicker` + select { ctx.Done() } | Go 1.20+ idiom | Predictable ticks; explicit cancellation. |

**Deprecated/outdated:**
- `github.com/docker/docker/client` — replaced by `github.com/moby/moby/client` in Phase 2.
- Compose Go SDK (`github.com/docker/compose/v2` library) — rejected at the architecture phase; we use the CLI via `os/exec`.
- Hand-rolled Bearer-token registry auth — replaced by `go-containerregistry` crane in Phase 3.

## Self-Upgrade Documentation (PROJECT.md addition)

The CONTEXT.md specifies this section verbatim. Copying it here so the planner can paste-and-commit:

```markdown
## Manual self-upgrade procedure

`hmi-update` refuses to recreate itself via the API (ACT-09). To upgrade:

1. On the HMI host: `docker pull ghcr.io/centroid-is/hmi-update:vX.Y.Z`
2. `docker compose -f /opt/centroid/docker-compose.yml up -d --force-recreate hmi-update`
3. Wait ~10s; verify `curl http://localhost:8080/healthz` returns 200.

The HMI's web UI will be unreachable for ~5–15 s during step 2.
The state file (`hmi_update_state.json`) persists across the recreate.

## Installation prerequisites

After `docker compose up -d`, the state file may need a one-time chown:

    chown 65532:65532 /opt/centroid/hmi-update/hmi_update_state.json

This grants the distroless `nonroot` UID inside the container write access.
(See Pitfall 9 — same UID/GID pattern as the docker.sock GID interpolation.)

## Container labels reference

| Label | Purpose | Default behavior if absent |
|-------|---------|----------------------------|
| `hmi-update.watch=true` | Mark a container as watched | Not watched |
| `hmi-update.tag-pattern=<regex>` | Constrain upstream tag candidacy | Any tag matches (`.*`) |
| `hmi-update.allow-update=false` | Server refuses Update for this container (SAFE-01) | Update allowed |
| `hmi-update.allow-rollback=false` | Server refuses Rollback for this container (SAFE-02) | Rollback allowed |
| `hmi-update.wait-for-healthy=true` | Extend verify-after-recreate to wait for `State.Health.Status == "healthy"` (60s window) | 15s consecutive-Running window |

## Configuration knobs (env vars)

| Variable | Default | Purpose |
|----------|---------|---------|
| `HMI_UPDATE_STATE_PATH` | `./hmi_update_state.json` | State file path |
| `HMI_UPDATE_COMPOSE_PATH` | (required) | Path to bind-mounted docker-compose.yml |
| `HMI_UPDATE_CRON` | `0 * * * *` | Cron schedule for digest polling |
| `HMI_UPDATE_LOG_LEVEL` | `info` | slog level |
| `HMI_UPDATE_REGISTRY_TIMEOUT_S` | `10` | Per-registry-call timeout |
| `HMI_UPDATE_POLL_CONCURRENCY` | `4` | Max concurrent crane.Digest calls per tick |
| `HMI_UPDATE_REGISTRY_INSECURE` | (unset) | E2E-only: enable plain HTTP for registry |
| `HMI_UPDATE_DOCKER_HOST` | `/var/run/docker.sock` | Docker socket path |
| `HMI_UPDATE_SELF_SERVICE` | `hmi-update` | (Phase 4 NEW) Compose service name this process is running as; refuses self-update |
| `HMI_UPDATE_VERIFY_WINDOW_S` | `15` | (Phase 4 NEW) Verify-after-recreate poll duration |
| `HMI_UPDATE_HEALTHCHECK_WINDOW_S` | `60` | (Phase 4 NEW) Extended window when `hmi-update.wait-for-healthy=true` |
```

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | moby v0.4.1 `ImagePullResponse.JSONMessages` aux field carries `{"ID":"sha256:..."}` or `{"Digest":"sha256:..."}` for the final-pull digest | §Pattern 2 Image Pull + Digest Verify | If the aux shape is different in v0.4.1, Option A (drain JSONMessages) fails — pivot to Option B (add `ImageInspect` to facade). Mitigation: write a probe test against real zot before committing. |
| A2 | `exec.CommandContext` + `cmd.Cancel = SIGTERM` + `cmd.WaitDelay = 10s` produces the expected SIGTERM→SIGKILL handoff for `docker compose up -d --force-recreate` | §Pattern 1 Compose Runner | If WaitDelay races docker compose's own stop_grace_period, the parent may SIGKILL a compose process that's still tearing down a container. Mitigation: 10s grace matches docker compose default; test under `make e2e-cron-fast` with a deliberately slow-stop container. |
| A3 | `container.InspectResponse.State.Health.Status` is the canonical field for healthcheck status (values: `starting`, `healthy`, `unhealthy`, `none`) | §Pattern 5 Verify Loop | Assumed standard moby semantics. If the SDK version's Health field shape differs, the test will surface it immediately. |
| A4 | The e2e compose network name follows the pattern `<dir>_default` (i.e. `e2e_default` for our repo) | §Disconnect-Network Fixture | If COMPOSE_PROJECT_NAME is set or compose changes its default naming, the fixture's regex (`/e2e.*_default$/`) catches it. |
| A5 | The Phase 3 channel cap (64) is sufficient for the Phase 4 third producer (action handlers) | §Architecture Patterns concurrency | Actions fire on operator clicks (~rare); the channel won't backpressure. If it does, the symptom would be an action goroutine blocked on a send — observable in goroutine dumps. |
| A6 | `state.Store.Get()` returns a snapshot whose Container map can be read by middleware without deadlocking against the Apply closure invoked by `RunUpdater` | §Pattern 7 Middleware | Verified: `store.Get()` takes RLock; `store.Update` takes Lock. Different lock kinds, no upgrade needed. Middleware never holds the read past the snapshot return. |
| A7 | The 8 e2e specs can complete within the Playwright default 30s per-test timeout (verify=15s + recreate=5s + pull=1s + slack=9s) | §Validation Architecture | Acceptance criterion 3 demands recreate ≤30s; if pull takes longer (large layer fetch), bump the per-spec timeout via `test.setTimeout(60_000)`. The 8th spec (verify-failed) may need a longer timeout — explicitly. |
| A8 | `docker network disconnect` from inside a Playwright test (host context) successfully blocks the hmi-update container's pull attempt | §Disconnect-Network Fixture | Verified by docker semantics — once disconnected, the container can't route to the registry. The test must restore via reconnect even on Playwright failure (try/finally). |

**Mitigation for A1:** Before committing Phase 4 plan 04-03 (the actions package body), write a single-spec probe test in `internal/docker/moby_test.go` that calls ImagePull against zot and asserts the aux field shape. If Option A's shape isn't reliable, switch to Option B (ImageInspect facade addition) in plan 04-03 before the orchestrator depends on it.

## Open Questions

1. **Should `ImagePull` be wrapped to surface the aux digest directly?**
   - **What we know:** The facade currently returns `io.ReadCloser`. The pull stream parsing logic (§Pattern 2 Option A) lives in `internal/actions`.
   - **What's unclear:** Is it cleaner to push the JSON parsing into `internal/docker` as a new method `ImagePullDigest(ctx, ref) (string, error)` that drains internally?
   - **Recommendation:** Keep facade narrow (no new method); parse in `internal/actions`. Reason: the facade's role is to be a thin SDK adapter — parsing protocol-level details belongs upstream. If Option B (ImageInspect) is picked, that's the natural place for a facade addition.

2. **Should the verify-after-recreate test fixture include a deliberately crashing container?**
   - **What we know:** ACT-12 covers happy-path restart-persistence. The new `verify-failed.spec.ts` covers the fail-path.
   - **What's unclear:** Is the existing `stub-watched-container` (busybox sleep loop) suitable? Or do we need a new fixture container that intentionally exits non-zero on start?
   - **Recommendation:** Add a new compose service `crash-loop-stub` in `compose.test.yml` with `command: ["sh", "-c", "exit 1"]` and `restart: unless-stopped`. The verify-failed spec then targets THIS container's update endpoint and asserts the 500 with `error: verify_failed`.

3. **Slog event for compose stderr — full content or truncated?**
   - **What we know:** Compose stderr on a failed recreate can be several KB.
   - **What's unclear:** Truncate to 4096 bytes in slog (as research recommends) or log the full content?
   - **Recommendation:** Truncate to 4096 with `...[truncated]...` marker. Full content is in the error returned to the API handler. Operators who need the full output can re-run the compose command by hand (it's printed in the slog event's `cmd` field).

4. **Is the orchestrator a separate `actions.Orchestrator` interface, or does the concrete struct expose its methods directly?**
   - **What we know:** Phase 1 declared `type Orchestrator interface{}` (empty stub at `internal/actions/orchestrator.go:17`).
   - **What's unclear:** Does the interface need a method-bearing contract for testing?
   - **Recommendation:** Follow the Phase 3 `Resolver`/`Poller` pattern — define a method-bearing interface and return it from `NewOrchestrator`. The `api.Server` consumes the interface, tests inject fakes. The concrete struct (e.g. `actionOrchestrator`) is unexported.

5. **Force-pull `?recreate=true` — should it require the same safety-label check as Update?**
   - **What we know:** CONTEXT.md Area 1 specifies "force-pull with recreate triggers the full Update flow." CONTEXT.md Area 4 SAFE-03 specifies "force-pull is NOT governed by safety labels."
   - **What's unclear:** If force-pull `?recreate=true` is "the full Update flow," does the Update flow's safety-label check apply?
   - **Recommendation:** **Yes** — force-pull `?recreate=true` IS a recreate operation; SAFE-01's intent ("don't recreate this container") applies. Phase 4 plan 04-04 should explicitly call this out: force-pull without `?recreate` skips safety-label; force-pull with `?recreate` applies the Update label check.

## Security Domain

Per CLAUDE.md "Security: LAN-only, unauthenticated" + the existing project posture from Phase 1–3.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | LAN-only, unauthenticated by design (CLAUDE.md). |
| V3 Session Management | no | Stateless HTTP. |
| V4 Access Control | yes | Safety labels (SAFE-01..03) + self-protection (ACT-09) are server-enforced. |
| V5 Input Validation | yes | Service-name regex `^[a-zA-Z0-9._-]+$` (ACT-10); compose argv separation (Pitfall 13 prevention). |
| V6 Cryptography | no | No new crypto in Phase 4. Phase 3's `redactingTransport` continues to handle registry token redaction. |
| V7 Error Handling | yes | Structured error responses (action codes); no path/env leak in error bodies (Phase 2's healthz precedent). |
| V9 Communications | yes (inherited) | Phase 3 enforces HTTPS for registry calls in production; Phase 4 doesn't change this. |

### Known Threat Patterns for Go HTTP + Docker Subprocess Stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Path traversal in `{service}` parameter | Tampering | Regex `^[a-zA-Z0-9._-]+$` at middleware entry; in-memory map lookup (no filesystem / no shell). |
| Shell injection via service-name interpolation | Tampering | `exec.CommandContext("docker", "compose", ..., service)` — argv, never shell. |
| State write race between concurrent producers | Tampering | Single-consumer channel pattern (DETECT-10); per-field Apply closures. |
| Self-update killing the orchestrator mid-flight | Denial of Service | Server-side 409 self_protection; documented manual procedure. |
| Operator-readable error bodies leaking internal paths | Information Disclosure | Errors map to verbatim codes (`verify_failed`, `compose_failed`, etc.); detailed paths/env values go to slog only, never to the wire (Phase 2 precedent — T-01-04-03). |
| Stale state surviving across upgrades | Repudiation | renameio + parent-dir-fsync (Phase 1); SIGKILL test verifies (STATE-04). |
| Bearer-token leak in slog | Information Disclosure | Phase 3 `newRedactingHandler` regex catches Bearer/Basic strings; compose stderr does NOT carry registry auth (compose talks to local daemon only). |
| Action endpoint accessible to LAN clients | Tampering | Accepted per CLAUDE.md "LAN-only, unauthenticated." Document the trust model in PROJECT.md. |

## Sources

### Primary (HIGH confidence)

- [pkg.go.dev/sync](https://pkg.go.dev/sync) — `Mutex.TryLock` semantics: non-blocking, returns boolean; "A successful call to TryLock is equivalent to a call to Lock." `[VERIFIED]`
- [pkg.go.dev/os/exec](https://pkg.go.dev/os/exec) — `Cmd.Cancel` + `Cmd.WaitDelay` (Go 1.20+) for SIGTERM grace; `exec.CommandContext` for ctx-aware subprocess. `[VERIFIED]`
- [pkg.go.dev/github.com/moby/moby/client](https://pkg.go.dev/github.com/moby/moby/client) — `ImagePullResponse` interface (`io.ReadCloser` + `JSONMessages` + `Wait`); `ImageTag(ctx, ImageTagOptions{Source, Target})` shape; `ContainerInspect` returns `container.InspectResponse`. `[VERIFIED]` — also captured locally at `internal/docker/_sdk_shape.txt:59-74, 25-50`.
- [pkg.go.dev/github.com/moby/moby/pkg/jsonmessage](https://pkg.go.dev/github.com/moby/moby/pkg/jsonmessage) — Pull progress JSON message shape (`Status`, `ID`, `Aux`, `Error`). `[CITED]`
- [docs.docker.com/reference/cli/docker/compose/up](https://docs.docker.com/reference/cli/docker/compose/up/) — `--force-recreate` semantics; exit code 1 on error, 0 on success or SIGINT/SIGTERM-induced clean stop. `[CITED]`
- [docs.docker.com/reference/cli/docker/network/disconnect](https://docs.docker.com/reference/cli/docker/network/disconnect/) — Network disconnect/reconnect semantics for offline test fixture. `[CITED]`
- [go.dev/blog/routing-enhancements](https://go.dev/blog/routing-enhancements) — Go 1.22+ `ServeMux` method-scoped routing with `r.PathValue`. `[CITED]`
- [github.com/google/renameio](https://github.com/google/renameio) — Atomic-write pattern with `WriteFile`. `[VERIFIED via existing code: internal/state/persist.go:41]`
- [internal/docker/_sdk_shape.txt](file:///Users/jonb/Projects/tmp/internal/docker/_sdk_shape.txt) — Canonical record of `moby/moby/client v0.4.1` API surface, captured 2026-05-13. `[VERIFIED]`

### Secondary (MEDIUM confidence)

- [github.com/golang/go/issues/50436](https://github.com/golang/go/issues/50436) — `Cmd.Cancel` + `WaitDelay` design rationale (Go 1.20). `[CITED]`
- [github.com/moby/moby/discussions/45101](https://github.com/moby/moby/discussions/45101) — Docker daemon SIGTERM→SIGKILL 10s grace default behavior. `[CITED]`
- [a Journey With Go: TryLock function](https://medium.com/a-journey-with-go/go-story-of-trylock-function-a69ef6dbb410) — TryLock Go 1.18 introduction notes. `[CITED]`

### Tertiary (LOW confidence; flagged in Assumptions Log)

- The exact JSON shape of `aux` in ImagePullResponse's JSONMessages stream (Assumption A1) — needs a probe test against real zot before committing to Option A. The moby SDK doc doesn't pin the shape across versions.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries are already in go.mod, locked at specific versions. No new dependencies.
- Architecture: HIGH — fully constrained by CONTEXT.md (4 grey areas accepted) + Phase 3's established patterns.
- Pitfalls: HIGH — PITFALLS.md Pitfalls 4, 6, 7, 11, 12, 13 directly inform Phase 4 design; Phase 4 also closes new Pitfall A (verify-after-recreate is the ONLY way to know recreate "worked").
- Concrete code patterns: HIGH on structure (verified against existing Phase 1–3 code shape); MEDIUM on one detail (Assumption A1 — pull progress aux shape) which requires a single probe test.

**Research date:** 2026-05-15
**Valid until:** 2026-06-15 (30 days; project is stable in pacing, moby SDK pinned, Go 1.26 stable).

## RESEARCH COMPLETE
