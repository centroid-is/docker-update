# Phase 4: Update / Rollback / Force-pull Actions, Safety & State Persistence - Context

**Gathered:** 2026-05-15
**Status:** Ready for planning
**Mode:** Smart discuss (autonomous) — 4 grey areas, all accepted as recommended

<domain>
## Phase Boundary

Deliver the headline differentiator — operator-driven per-container Update, Rollback, and Force-pull actions — wired through HTTP handlers, per-service mutex serialization, verify-after-recreate, server-enforced safety labels, self-protection, and SIGKILL-resistant state persistence.

Concretely this phase fills:

1. **`internal/compose.Runner` body** — `os/exec` wrapper around `docker compose -f $HMI_UPDATE_COMPOSE_PATH up -d --force-recreate <service>` with stderr capture into slog and exit-code propagation.
2. **`internal/actions.Orchestrator` body** — three handlers (Update, Rollback, Force-pull) that compose docker.Client + registry.Resolver + compose.Runner + state.Store. Per-service `sync.Mutex` map with `TryLock` semantics for ACT-08.
3. **Three new HTTP endpoints** — `POST /api/containers/:service/update`, `/rollback`, `/force-pull` with strict service-name validation (ACT-10 allowlist regex), middleware-level safety-label + self-protection checks (ACT-09, SAFE-01..03), and idempotency short-circuits (ACT-06, ACT-07).
4. **Verify-after-recreate** — 15-second poll loop on `docker.Inspect(svc)` asserting `State.Running == true` AND `RestartCount` unchanged from pre-action snapshot. Opt-in healthcheck wait via `hmi-update.wait-for-healthy=true` label.
5. **`state.Container` extensions** — `ActionInFlight string` (`"updating"`/`"rolling_back"`/`"force_pulling"`/empty) and `ActionError string` for UI Phase 5 spinner state and error surface.
6. **STATE-04 / STATE-05** — SIGKILL-mid-write fault-injection test asserting `renameio` leaves the file parseable-old or parseable-new; runbook in PROJECT.md documents `chown 65532:65532 hmi_update_state.json` install step.
7. **OBS-01 / OBS-03** — every poll/update/rollback/force-pull emits a structured slog JSON line; `GET /api/state` is memory-only no-I/O.
8. **Self-upgrade documentation** — PROJECT.md gains a "Manual self-upgrade procedure" section (sibling temporary container that recreates hmi-update from outside).

Out of scope for this phase: real UI rendering of Update/Rollback/Force-pull buttons (Phase 5 — UI-01..10), display-blackout UX for `flutter`/`weston` (Phase 6 — UX-01..03), production Dockerfile and image-size verification (Phase 7), full GitHub Actions pipeline (Phase 8).

</domain>

<decisions>
## Implementation Decisions

### Area 1 — Action Handler Architecture

- **Handler shape: linear sequence.** Update handler runs:
  1. `mutex.TryLock(service)` — fail fast on collision (ACT-08)
  2. Middleware checks (self-protection ACT-09; safety-label ACT-09/SAFE-01/SAFE-02; service-name validation ACT-10) — actually run *before* mutex acquisition; mutex is the last gate
  3. Idempotency short-circuit: read `state.Get().Containers[svc]`; if `c.CurrentDigest == c.AvailableDigest && c.AvailableDigest != ""`, send `KindActionResult{Phase: "idempotent", NoOp: true}` and return 200 with `{"no_op": true, "current_digest": ..., "previous_digest": ...}`
  4. Send `KindActionStart{Service, Action: "update"}` — state.Container.ActionInFlight = "updating"
  5. `docker pull <image>:<tag>` via `docker.Client.ImagePull` (already exposed by Phase 2; Phase 3 added retry classification but actions don't retry — fail-fast)
  6. Verify pulled `RepoDigests[0]` matches the registry digest fetched in step 3 (Pitfall 1 — never trust local re-hash)
  7. Send `KindActionProgress{Phase: "pulled", NewDigest}` — record `previous_digest = c.CurrentDigest`
  8. `compose.Runner.UpdateService(ctx, service)` → `docker compose -f $HMI_UPDATE_COMPOSE_PATH up -d --force-recreate <service>` via `os/exec` (argv, no shell interpolation)
  9. Verify-after-recreate (15s poll loop — see Area 3)
  10. Send `KindActionResult{Phase: "complete", OldDigest, NewDigest, Err: nil}` — RunUpdater applies via store.Update: clears `ActionInFlight`, sets `CurrentDigest = NewDigest`, sets `PreviousDigest = OldDigest`, clears `ActionError`, clears `UpdateAvailable`
  11. Release mutex; return 200 with `{"current_digest": ..., "previous_digest": ...}`
  - **On any failure step 5–9:** send `KindActionResult{Phase: "failed", Err: ...}` — RunUpdater clears `ActionInFlight`, sets `ActionError = "<phase>_failed: <error>"`; release mutex; return 500 with structured error body.

- **Rollback handler (ACT-03/ACT-04):**
  1. Same mutex + middleware + validation as Update
  2. If `c.PreviousDigest == ""` → 400 `{"error": "no_previous_digest"}`
  3. Idempotency: if `c.CurrentDigest == c.PreviousDigest` → 200 `{"no_op": true}` (ACT-07)
  4. `docker.Client.ImageTag(ctx, image+"@"+c.PreviousDigest, image+":"+c.Tag)` — local re-tag (no registry call; works offline — Acceptance criterion 4)
  5. `compose.Runner.UpdateService(ctx, service)` — same docker compose up -d --force-recreate
  6. Verify-after-recreate (same 15s window)
  7. Send `KindActionResult` — store: swap `CurrentDigest` ↔ `PreviousDigest` (single-slot toggle semantic — second rollback flips back); `UpdateAvailable` re-flips to `true` because registry `:latest` is unchanged

- **Force-pull handler (ACT-05):**
  1. Same mutex + middleware + validation
  2. Default behavior: `docker.Client.ImagePull(ctx, image:tag)` only — no `compose up -d`. Returns 200 with `{"pulled": true, "current_digest": ...}` whether or not the digest changed. Useful for recovering from accidentally-removed local images (F8).
  3. Optional `?recreate=true` query param triggers the full Update flow with the 15s verify window. Documented in API.md.

- **State mutations via channel (DETECT-10 invariant carries forward).** Action handlers do NOT call `state.Store.Update` directly. They send `poll.StateUpdate{Kind: poll.KindActionResult, ...}` on the existing channel; the single consumer goroutine (RunUpdater) applies. New `UpdateKind` values: `KindActionStart`, `KindActionProgress`, `KindActionResult`. Apply closures encapsulate the three field mutations (`ActionInFlight`, `CurrentDigest/PreviousDigest`, `ActionError`).

- **`state.Container` extensions (Phase 4 schema):**
  - `ActionInFlight string` — omitempty; values: `""` (default), `"updating"`, `"rolling_back"`, `"force_pulling"`. UI Phase 5 reads this for per-row spinner.
  - `ActionError string` — omitempty; populated on action failure, e.g., `"verify_failed: container restarted 3 times in 15s"`. Cleared on next successful action of any kind. Surfaces in `/api/state` for UI Phase 5 toast.
  - tygo regenerates `ui/src/lib/types.d.ts`; `make check-types` enforces.

### Area 2 — Per-Service Mutex + Concurrent Action Semantics (ACT-08)

- **Mutex map:** `actions.Orchestrator` holds:
  ```go
  type Orchestrator struct {
      mu       sync.RWMutex       // protects the locks map itself
      locks    map[string]*sync.Mutex
      // ... other fields
  }
  ```
  Lazy creation: first `lockService(svc)` for a given svc creates the entry. Map never shrinks (service set is bounded by compose file contents; entries are tiny). On `TryLock` failure → 409 `{"error": "service_busy", "service": svc}`.

- **TryLock semantics:** `sync.Mutex.TryLock` (Go 1.18+). Non-blocking. No queueing. The frontend (Phase 5) debounces double-clicks; the server is the last line of defense.

- **Cross-service parallelism:** Allowed. Updating `service-a` and `service-b` concurrently is fine — the per-service mutex serializes only same-service collisions. `docker compose up -d --force-recreate <svc>` is service-scoped at the daemon level.

- **Cron-vs-manual race:** Cron poller does NOT take the action mutex. The poll loop only reads digests; the state writes go through `KindFetchResult` channel sends, which are serialized by RunUpdater. A manual action and a cron sweep on the same service can run concurrently; the channel pattern ensures the final state is consistent (last writer wins on a per-field basis since Apply closures only mutate fields they own — `KindFetchResult` writes `AvailableDigest`, `LastPolledAt`; `KindActionResult` writes `CurrentDigest`, `PreviousDigest`, `ActionInFlight`, `ActionError`).

- **No lock acquisition timeout.** TryLock fails immediately → 409. Frontend retry is the operator's responsibility (Phase 5 debounces buttons).

### Area 3 — Verify-After-Recreate (Pitfalls 4 + 12)

- **Verification window:** `verifyDuration = 15s` (constant; configurable via `HMI_UPDATE_VERIFY_WINDOW_S` if operators ever need to tune).
- **Poll loop:** `time.NewTicker(1s)`; on each tick:
  1. `docker.Client.ContainerInspect(ctx, containerID)` — returns full descriptor
  2. Capture `State.Running`, `RestartCount` (and `State.Health.Status` if healthcheck-opt-in)
  3. If `!State.Running` → fail fast: `verify_failed: container not running`
  4. If `RestartCount > preActionSnapshot.RestartCount` → fail fast: `verify_failed: container restarted N times`
  5. If `State.Health.Status == "unhealthy"` (when healthcheck opt-in) → fail fast: `verify_failed: healthcheck unhealthy`
  6. If 15s of consecutive successful ticks → success
- **Healthcheck integration (opt-in):**
  - Label: `hmi-update.wait-for-healthy=true` (on the watched container)
  - When set, ALSO wait for `State.Health.Status == "healthy"` within an extended 60s window (so operator can tune slow-start containers like databases)
  - Documented in PROJECT.md "Container labels reference"
  - For containers without a HEALTHCHECK directive, opting in still treats "no health status reported" as a soft-success after 60s — never blocks indefinitely
- **Verify-failure response shape:**
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
  HTTP 500. State: `ActionError = "verify_failed: ..."`; `ActionInFlight = ""`. Container left in whatever state daemon left it. UI Phase 5 offers Rollback if `PreviousDigest != ""`.
- **Force-pull verify:** Default = no recreate, no verify. Optional `?recreate=true` triggers full Update flow with verify.

### Area 4 — Self-Protection (ACT-09) + Safety Labels (SAFE-01..03)

- **Self-identification:** Env var `HMI_UPDATE_SELF_SERVICE` (default `"hmi-update"`). Action handler middleware:
  ```go
  if r.PathValue("service") == selfService {
      writeError(w, 409, "self_protection", "see PROJECT.md self-upgrade procedure")
      return
  }
  ```
  PROJECT.md "Manual self-upgrade procedure" section documents the sibling-container approach: operator runs a temporary `hmi-update:new` container (manually with `docker run`), which `docker compose up -d --force-recreate hmi-update`s the production one from outside.

- **Safety-label enforcement:** Middleware reads container labels from the cached state (no extra `docker.Inspect` call):
  ```go
  c, ok := state.Get().Containers[svc]
  if !ok {
      writeError(w, 404, "container_not_found", "")
      return
  }
  switch action {
  case "update":
      if c.Labels["hmi-update.allow-update"] == "false" {
          writeError(w, 409, "action_disabled_by_label", `hmi-update.allow-update=false`)
          return
      }
  case "rollback":
      if c.Labels["hmi-update.allow-rollback"] == "false" {
          writeError(w, 409, "action_disabled_by_label", `hmi-update.allow-rollback=false`)
          return
      }
  }
  ```
  Force-pull is NOT governed by safety labels (it's read-only with respect to the running container; just refreshes the local image cache).

- **SAFE-03 — labels are read-only for poll:** Poll loop (`cronPoller.eligibleContainers`) ignores `hmi-update.allow-*` labels entirely. Only the action middleware honors them. `timescaledb` keeps getting digest fetches; only its Update/Rollback buttons are 409'd.

- **Service-name validation (ACT-10):**
  - Router pattern: `r.PathValue("service")` (Go 1.22+ ServeMux)
  - Regex `^[a-zA-Z0-9._-]+$` applied at the action middleware entry; fail-fast 400 on mismatch
  - In-memory lookup against `state.Get().Containers` map (no docker.Inspect — uses cached state); 404 if absent
  - **Argv discipline:** `compose.Runner.UpdateService(ctx, service)` calls `exec.CommandContext("docker", "compose", "-f", composePath, "up", "-d", "--force-recreate", service)`. The service variable is passed as a separate argv element — no string interpolation through `/bin/sh`. Pitfall 13 prevention.

### compose.Runner contract

```go
type Runner interface {
    // UpdateService runs `docker compose -f <path> up -d --force-recreate <service>`.
    // Captures stderr into the returned error wrapping; emits one slog event with
    // event=compose.run cmd=<argv> exit_code=N duration_ms=M.
    UpdateService(ctx context.Context, service string) error

    // ComposePath returns the compose file path used (for diagnostic logging).
    ComposePath() string
}
```

- Concrete impl: `internal/compose/runner.go` body uses `exec.CommandContext` with the docker compose v2 binary path discovery (`exec.LookPath("docker")`).
- Compose file path comes from `compose.Reader` (Phase 2) — reuse the boot snapshot. Action handlers call `composeReader.CheckUnchanged(ctx)` BEFORE invoking the runner; if 412 `ErrComposeFileMoved`, return 412 to the operator with the documented remediation hint.

### Self-upgrade documentation (PROJECT.md)

The Phase 4 commit adds a "Manual self-upgrade procedure" section to PROJECT.md:

```markdown
## Manual self-upgrade procedure

`hmi-update` refuses to recreate itself via the API (ACT-09). To upgrade:

1. On the HMI host: `docker pull ghcr.io/centroid-is/hmi-update:vX.Y.Z`
2. `docker compose -f /opt/centroid/docker-compose.yml up -d --force-recreate hmi-update`
3. Wait ~10s; verify `curl http://localhost:8080/healthz` returns 200.

The HMI's web UI will be unreachable for ~5–15 s during step 2.
The state file (`hmi_update_state.json`) persists across the recreate.
```

### HTTP endpoint contracts

```
POST /api/containers/:service/update
POST /api/containers/:service/rollback
POST /api/containers/:service/force-pull[?recreate=true]
```

All three:
- Path validation: service matches `^[a-zA-Z0-9._-]+$`; container exists in state
- Middleware order (early-return on any failure): self-protection → safety-label → service-name validation
- Idempotency check: ACT-06 (Update + current==upstream) / ACT-07 (Rollback + current==previous) → 200 `{"no_op": true, "current_digest": ...}`
- Per-service `TryLock`; 409 on collision
- Action body (see Area 1)
- Response on success: 200 + JSON body with `{"current_digest": "...", "previous_digest": "..."}` (ACT-11)
- Response on failure: 500 + JSON with `error`, `reason`, and any structured diagnostic fields

### STATE-04 — SIGKILL-mid-write fault injection test

The renameio.WriteFile + dir-fsync pattern (Phase 1) is the load-bearing invariant. Phase 4 ships the **test**:

- `internal/state/store_sigkill_test.go` — table-driven test that uses a `testing.T` `Cleanup` hook + an external helper binary
- Test harness: spawn the helper binary as a subprocess; helper does `state.NewStore` + `Update` in a loop; parent SIGKILLs at random intervals; after each kill, parent verifies the on-disk file is parseable (either prior or new content, never truncated)
- Coverage target: 100 SIGKILL iterations, zero parse errors

### STATE-05 — install-time UID/GID documentation

PROJECT.md "Installation" section gains:

```markdown
## Installation prerequisites

After `docker compose up -d`, the state file may need a one-time chown:

    chown 65532:65532 /opt/centroid/hmi-update/hmi_update_state.json

This grants the distroless `nonroot` UID inside the container write access.
(See Pitfall 9 — same UID/GID pattern as the docker.sock GID interpolation.)
```

### OBS-01 — structured slog schema for actions

Every action emits at minimum:

- On enter: `event=action.start service=foo action=update`
- On each phase (pull, recreate, verify): `event=action.phase service=foo phase=pulled new_digest=sha256:...`
- On exit (success or fail): `event=action.complete service=foo action=update before=sha256:... after=sha256:... exit_code=0 duration_ms=12345`
- On verify-failure: `event=action.verify_failed service=foo restart_count=3 running=false`

No bearer tokens, no Authorization headers, no environment values — the Phase 3 redacting transport + slog `ReplaceAttr` regex already cover this.

### OBS-03 — `GET /api/state` is no-I/O

The existing handler in `internal/api/handlers.go` (Phase 1) already serves from `state.Store.Get()` which is an in-memory snapshot. Phase 4 verifies — adds an explicit no-`os.Stat`, no-`docker.Inspect` test that times the handler under load. Documented in API.md as "GET /api/state is the 5-second UI poll target; memory-only, no I/O."

### File Layout

- `internal/compose/runner.go` — `Runner` interface body + `execRunner` concrete impl
- `internal/compose/runner_test.go` — table-driven tests with a fake `exec.Cmd` (or test seam injecting `commandRunner func(name string, args ...string) *exec.Cmd`)
- `internal/actions/orchestrator.go` — `Orchestrator` interface body + `actionOrchestrator` concrete impl
- `internal/actions/orchestrator_test.go` — table-driven tests with fakes for docker.Client + compose.Runner + state.Store
- `internal/actions/mutex.go` — per-service mutex map + `lockService`/`unlockService` helpers
- `internal/actions/mutex_test.go` — concurrent TryLock contention test (`-race -count=50`)
- `internal/actions/middleware.go` — self-protection, safety-label, service-name validation middleware
- `internal/actions/middleware_test.go` — table-driven for each rejection class
- `internal/actions/verify.go` — verify-after-recreate poll loop
- `internal/actions/verify_test.go` — fake docker.Client returning scripted Inspect responses
- `internal/api/handlers_actions.go` — three new HTTP handlers (update, rollback, force-pull)
- `internal/api/handlers_actions_test.go` — httptest against the orchestrator interface
- `internal/api/server.go` — wire new routes (Go 1.22+ ServeMux: `POST /api/containers/{service}/update` etc.)
- `internal/state/schema.go` — extend `Container` with `ActionInFlight`, `ActionError` (both omitempty)
- `internal/api/types.go` — mirror the schema additions; tygo regen
- `internal/poll/channel.go` — extend `UpdateKind` enum with `KindActionStart`, `KindActionProgress`, `KindActionResult`
- `cmd/hmi-update/main.go` — wire `compose.NewRunner` + `actions.NewOrchestrator` + new HTTP routes; add `HMI_UPDATE_SELF_SERVICE` env read
- `e2e/tests/update-flow.spec.ts` — RED FIRST. ACT-01 + ACT-02 + ACT-11: Update happy path + verify-after-recreate + response shape
- `e2e/tests/rollback-flow.spec.ts` — RED FIRST. ACT-03 + ACT-04: Rollback after Update + offline rollback (network detached)
- `e2e/tests/idempotency.spec.ts` — RED FIRST. ACT-06 + ACT-07: no-op responses
- `e2e/tests/concurrent-actions.spec.ts` — RED FIRST. ACT-08: double-click collision; cross-service parallel
- `e2e/tests/self-protection.spec.ts` — RED FIRST. ACT-09 + Pitfall 6: direct curl to `/api/containers/hmi-update/update` returns 409
- `e2e/tests/safety-labels.spec.ts` — RED FIRST. SAFE-01 + SAFE-02 + SAFE-03: timescaledb refuses Update; still gets polled
- `e2e/tests/restart-persistence.spec.ts` — RED FIRST. ACT-12 + STATE-04: docker compose restart hmi-update preserves digests
- `e2e/tests/verify-failed.spec.ts` — RED FIRST. Verify-after-recreate fails gracefully on crash-looping container
- `internal/state/store_sigkill_test.go` — STATE-04 fault injection (parent/child harness)
- `cmd/sigkillhelper/main.go` — helper binary for STATE-04
- `PROJECT.md` — Self-upgrade procedure + Installation prerequisites + Container labels reference
- `API.md` (NEW) — Documents the three action endpoints, request/response shapes, error codes

### Concurrency Invariants (extended from Phase 3)

- Action handler mutex map (`map[service]*sync.Mutex`) is protected by `sync.RWMutex` on the holding `Orchestrator` struct. Lookups under RLock; new-entry creation under Lock.
- Action handlers send `StateUpdate` on the existing channel (DETECT-10); never call `state.Store.Update` directly.
- Compose runner's `exec.CommandContext` respects ctx cancellation; on SIGTERM the in-flight `docker compose up -d --force-recreate` is killed with SIGTERM (graceful) then SIGKILL (after 10s grace).
- Verify-after-recreate loop respects ctx cancellation; on SIGTERM during verify, the action returns with `verify_canceled` (not verify_failed) — operator can retry after restart.

### Configuration Knobs (env vars introduced this phase)

- `HMI_UPDATE_SELF_SERVICE` — default `"hmi-update"`. The compose service name this process runs as; refuses self-update.
- `HMI_UPDATE_VERIFY_WINDOW_S` — default `15`. Verify-after-recreate poll duration.
- `HMI_UPDATE_HEALTHCHECK_WINDOW_S` — default `60`. Extended window when `hmi-update.wait-for-healthy=true` label is set.

### Claude's Discretion

- Whether to expose `ActionInFlight` as a typed enum or a plain string. Lean string (matches `Notes` precedent; tygo emits as string union).
- Whether to surface `ActionError` with a structured object (`{phase, reason, count}`) or a single string. Lean single string with `phase: reason: detail` shape (`verify_failed: container restarted: 3`) — matches `Notes` convention and avoids tygo complexity.
- The exact mutex map structure: `sync.Map` vs `map+RWMutex`. Lean `map+RWMutex` — explicit, easier to reason about than `sync.Map`'s opaque sharding.
- Whether `compose.Runner.UpdateService` accepts a `dryRun bool` or just always runs. Lean always-runs; the e2e test stack already exercises real recreate. Dry-run is a Phase 7/8 concern if it surfaces.
- Whether to short-circuit verify-after-recreate for force-pull (without recreate). Lean YES — force-pull-no-recreate just calls ImagePull and returns immediately; no verify needed.
- Exact slog event names. Lean dotted convention (`action.start`, `action.phase`, `action.complete`, `action.verify_failed`).
- Whether `actionOrchestrator.lockService` returns a `func()` unlock closure or expects the caller to `defer o.unlockService(svc)`. Lean closure — easier to read; pattern: `unlock := o.lockService(svc); defer unlock()`.

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets

- `internal/state.Store.Update(func(*State))` (Phase 1) — single mutation point; channel pattern (Phase 3) is the only producer for action handlers.
- `internal/state.Container` (Phase 1+2+3) — Phase 4 extends with `ActionInFlight` and `ActionError` (both omitempty).
- `internal/docker.Client` (Phase 2) — already exposes `ImagePull`, `ImageTag`, `ContainerInspect`. All three actions need these; no new methods.
- `internal/compose.Reader.CheckUnchanged` (Phase 2) — actions call this before invoking the runner; `ErrComposeFileMoved` → 412.
- `internal/compose.Runner` interface (Phase 1 stub) — body lands this phase.
- `internal/actions.Orchestrator` interface (Phase 1 stub) — body lands this phase.
- `internal/registry.Resolver.Digest` (Phase 3) — actions call this to fetch the registry digest for the verify step (Pitfall 1 — never trust local re-hash).
- `internal/poll.StateUpdate` (Phase 3) — action handlers extend with new `UpdateKind` values.
- `internal/state.Notes` (Phase 3 review fix WR-10) — exported canonical Note constants; pattern reusable for action-error strings if needed.
- `e2e/compose.test.yml` (Phase 1+2+3) — Phase 4 adds the `hmi-update` service's own watch label + safety label entries (e.g., `timescaledb-stub` already exists from Phase 3 with `tag-pattern` label; Phase 4 adds `hmi-update.allow-update=false` to test SAFE-01).
- `e2e/fixtures/push-image.ts` (Phase 3) — reused to push new manifests mid-test for the Update happy-path spec.

### Established Patterns

- **Atomic writes via `renameio`** (Phase 1) — STATE-04 leans on this exclusively; Phase 4 only adds the test harness.
- **Single-consumer channel for state mutations** (Phase 2+3) — Phase 4 adds a third producer (action handlers) feeding the same channel.
- **Sentinel errors per package** (`compose.ErrComposeFileMoved`, `registry.ErrPermanent/ErrTransient`) — Phase 4 follows: `actions.ErrServiceBusy`, `actions.ErrSelfProtection`, `actions.ErrActionDisabledByLabel`, `actions.ErrVerifyFailed`, `actions.ErrComposeFailed`.
- **Facade-over-SDK** (`internal/docker` over `moby/moby/client`; `internal/registry` over `google/go-containerregistry`) — Phase 4 follows: `internal/compose` facades `os/exec` + `docker compose` CLI.
- **Tygo source-of-truth** — `internal/api/types.go` ↔ `ui/src/lib/types.d.ts`; CI's `make check-types` enforces.
- **Table-driven tests, `t.Errorf` not `t.Fatal` inside goroutines** — Phase 4 carries forward.
- **Red-first Playwright e2e** (Phase 1+2+3) — every functional requirement starts as a failing spec; Phase 4 adds 8 new spec files.
- **Canonical exported note/error literals** (Phase 3 review WR-10 fix) — Phase 4 introduces `actions.Error*` exported error vars in `internal/actions/errors.go`.

### Integration Points

- `cmd/hmi-update/main.go` boot order — Phase 4 adds:
  - `runner := compose.NewRunner(composePath)` (between compose.NewReader and registry.NewResolver)
  - `orchestrator := actions.NewOrchestrator(dockerClient, runner, resolver, composeReader, store, updates, selfService, verifyWindow, healthcheckWindow)`
  - `api.NewServer(store, dockerClient, composeReader, orchestrator)` — constructor signature extended; existing test code updates
- `internal/api/server.go` — new routes (`POST /api/containers/{service}/update`, `/rollback`, `/force-pull`); `r.PathValue("service")` (Go 1.22+ ServeMux).
- `internal/state/schema.go` → `internal/api/types.go` → `ui/src/lib/types.d.ts` triple-edit for `ActionInFlight` + `ActionError`.
- `internal/poll/channel.go` — `UpdateKind` extended with `KindActionStart`, `KindActionProgress`, `KindActionResult`.

</code_context>

<specifics>
## Specific Ideas

- **The 15s verify window is calibrated against Pitfall 4** ("recreate succeeds but container immediately crash-loops"). 15s is long enough that a fast-restart loop is observed (most crash loops hit RestartCount > 0 within 5s); short enough that operator UX is acceptable.
- **The healthcheck opt-in is calibrated against Pitfall 12** ("compose recreate returns 0 but the new container's startup probe hasn't passed"). The 60s extended window handles most production startup probes (databases, message brokers).
- **`previous_digest` is a single slot** (no history). The user explicitly chose this in PROJECT.md "Active Requirements" (F3). Phase 4 honors: after Rollback, the previously-current digest becomes the new `previous_digest`. A second Rollback flips back; a third Rollback flips again. The toggle is the feature.
- **Offline rollback is the load-bearing differentiator from WUD.** ACT-04's e2e test runs with the registry network detached (`docker network disconnect e2e_default zot`). `docker.Client.ImageTag` is a local operation; it succeeds without registry access. `compose up -d --force-recreate` is also offline (uses the local image). The whole flow MUST work with the registry unreachable.
- **STATE-04 is shipped with both renameio AND the parent dir fsync** (Phase 1 RESEARCH.md research correction A5, Option 2). The test in Phase 4 verifies the combined behavior under SIGKILL across the full write path.
- **Self-upgrade documentation is the only way to upgrade hmi-update itself.** The README/PROJECT.md MUST make this clear; operators expect a UI button for it and the explicit 409 + hint string is the user-experience surface.
- **No tests retry across the 15s verify window — they wait the full window deterministically.** Playwright's `expect.poll` retries the API call but the action handler itself blocks for the full 15s. The 30s outer timeout in spec files accommodates: 1s pull + 5s recreate + 15s verify + 5s margin = 26s.
- **STATE-04 fault injection runs ONLY in `make test-sigkill` (NOT `make test`).** The fork/exec/SIGKILL pattern is slow and fragile; the default unit test suite stays fast. Documented in the test file and the README.
</specifics>

<deferred>
## Deferred Ideas

- **Multi-slot rollback history** — `PreviousDigest` is a single slot. V2 may add `History []DigestRecord` if operators ever need to walk back more than one step.
- **Operator-initiated bulk Update (`POST /api/update-all`)** — out of scope. Per-row buttons only for v1. Surface as future request if operators ask.
- **Action audit log** — slog events are the v1 audit trail. A structured /api/action-history endpoint is V2.
- **`hmi-update.wait-for-healthy` precedence over `hmi-update.allow-update`** — if both labels are set inconsistently, allow-update wins (the action never runs; healthcheck is moot). Documented as expected.
- **arm64 builds for the helper binary** (sigkillhelper) — same V2-ARM64 concern as Phase 3. Phase 4 ships amd64-only.
- **Action endpoint authentication** — LAN-only, unauthenticated per CLAUDE.md "Security: LAN-only". Auth lands in V2 if/when HMIs are exposed beyond the field-engineer LAN.
- **Compose runner `dryRun` flag** — Phase 7/8 might want it for image-size verification; v1 doesn't.

</deferred>
</content>
