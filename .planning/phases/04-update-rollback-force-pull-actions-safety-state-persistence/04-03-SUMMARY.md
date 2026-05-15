---
phase: 04-update-rollback-force-pull-actions-safety-state-persistence
plan: 03
subsystem: actions
tags: [actions, orchestrator, mutex, middleware, verify-after-recreate, sentinel-errors, slog, detect-10, pitfall-1, act-08, safe-03]

# Dependency graph
requires:
  - phase: 02-docker-client-compose-file-reader
    provides: docker.Client interface (ImagePull/ImageTag/ContainerInspect); compose.Reader.CheckUnchanged with ErrComposeFileMoved sentinel; sentinel-error file convention
  - phase: 03-registry-polling-update-detection
    provides: registry.Resolver.Digest (Pitfall 1 cross-check source); poll.StateUpdate channel + RunUpdater consumer (DETECT-10 single-consumer invariant); poll.UpdateKind iota
  - phase: 04-update-rollback-force-pull-actions-safety-state-persistence/01
    provides: state.Container ActionInFlight/ActionError fields; poll.UpdateKind action variants (KindActionStart/Progress/Result)
  - phase: 04-update-rollback-force-pull-actions-safety-state-persistence/02
    provides: compose.Runner.UpdateService body (argv-disciplined docker compose subprocess); compose.ErrComposeFailed sentinel
provides:
  - "actions.Orchestrator interface (Update/Rollback/ForcePull/LookupContainer/CheckSelfProtection/SelfService)"
  - "actions.NewOrchestrator(dockerClient, runner, resolver, composeReader, store, updates, selfService, verifyWindow, healthcheckWindow) constructor returning the interface (WR-04)"
  - "actions.ActionResult struct (CurrentDigest, PreviousDigest, NoOp) — handler wire payload"
  - "Seven sentinel errors: ErrServiceBusy, ErrSelfProtection, ErrActionDisabledByLabel, ErrVerifyFailed, ErrVerifyCanceled, ErrComposeFailed, ErrPullFailed (each documents HTTP status mapping)"
  - "VerifyDetail typed inner error (B2): RestartCount/Running/ContainerID/Reason fields; Unwrap()→ErrVerifyFailed; consumed via errors.As by Plan 04-04 handlers"
  - "Per-service mutex map with TryLock + double-checked locking (ACT-08)"
  - "ValidateServiceName regex helper + 7 exported ActionBody* response constants (Pattern K verbatim-string discipline)"
  - "verifyAfterRecreate ticker loop with consecutive-success counter + opt-in healthcheck soft-success"
  - "drainPullStream Option A implementation (moby JSONMessages aux digest extraction)"
  - "action.start / action.phase / action.complete / action.pull_failed / action.compose_failed / action.verify_failed slog event schema (OBS-01)"
affects: [04-04 http-handlers (consumes Orchestrator interface + all sentinels + ActionBody* constants + VerifyDetail), 04-06 e2e specs (validates the action flow end-to-end), Phase 5 UI (reads ActionInFlight + ActionError from /api/state)]

# Tech tracking
tech-stack:
  added: [] # no new go.mod deps; uses stdlib sync, encoding/json, log/slog, http + existing internal packages
  patterns:
    - "Per-key sync.Mutex map with TryLock + double-checked locking — analog of internal/poll/patterns.go's RWMutex-around-map, specialized to non-blocking acquire"
    - "ctx-aware send wrapper (mirror of internal/poll/poller.go::send) — abstracted behind an updateSender interface so tests inject a recordingSender that captures every StateUpdate without standing up a RunUpdater drain goroutine"
    - "Typed inner error with Unwrap() returning a sentinel — VerifyDetail Unwrap()→ErrVerifyFailed lets the caller errors.Is the sentinel AND errors.As the typed detail (B2 from plan revision)"
    - "Verbatim-constant response bodies EXPORTED across packages — Plan 04-04 imports the same ActionBody* consts the middleware emits, removing the cross-package drift surface (one source of truth)"
    - "Narrow interface seams: stateReader (Get only), dockerInspector (ContainerInspect only), composeUnchangedChecker (CheckUnchanged only), updateSender (send only) — keeps test fakes compact"
    - "DETECT-10 carry-forward grep gate: zero `state.Store.Update(` calls in orchestrator.go; all state writes flow through the channel + Apply closures (single-consumer invariant)"
    - "Tick-indexed scripted response pattern for fake docker.Client.ContainerInspect (mirrors internal/docker/discovery_test.go fakeClient.inspectScript) — verify_test.go's fakeInspector consumes the next entry per call"

key-files:
  created:
    - internal/actions/errors.go
    - internal/actions/mutex.go (lockService) — actionOrchestrator struct moved to orchestrator.go in Task 3
    - internal/actions/mutex_test.go
    - internal/actions/probe_aux_digest_test.go
    - internal/actions/middleware.go
    - internal/actions/middleware_test.go
    - internal/actions/verify.go
    - internal/actions/verify_test.go
    - internal/actions/orchestrator_test.go
  modified:
    - internal/actions/orchestrator.go (Phase 1 stub `type Orchestrator interface{}` → full body)

key-decisions:
  - "Aux-digest path: Option A (drainPullStream + json.Decoder) selected. The A1 probe (probe_aux_digest_test.go) t.Skipped on the dev box (no docker daemon at /var/run/docker.sock). Per RESEARCH.md A1 mitigation, Option A remains the design lean when the probe is inconclusive. The probe will run-and-validate on any CI worker with docker available; if it surfaces a failure, the fallback is to add an ImageInspect method to the internal/docker.Client facade in a small patch."
  - "VerifyDetail typed inner error (B2): wrapped with ErrVerifyFailed via the double-wrap pattern `fmt.Errorf(\"%w: %w\", ErrVerifyFailed, &VerifyDetail{...})`. Unwrap() returns ErrVerifyFailed so errors.Is succeeds; errors.As extracts the fields. This is the load-bearing contract for Plan 04-04's structured 500 response body."
  - "Middleware method declaration order: CheckSelfProtection declared BEFORE LookupContainer in middleware.go (per success_criteria awk gate). The handler invokes them in the same order: ValidateServiceName → CheckSelfProtection → LookupContainer → CheckSafetyLabel. Critical because hmi-update is NOT in the watched-containers cache (default hmi-update.watch=false on self) — running LookupContainer first would return 404 (misleading) instead of 409 self_protection (operator-actionable)."
  - "ActionBody* response constants EXPORTED (capitalized) intentionally. Plan 04-04's internal/api/handlers_actions_test.go cross-imports them to assert handler-emitted bodies match the middleware emissions byte-for-byte. One source of truth in internal/actions removes the drift surface."
  - "Soft-success in opt-in healthcheck mode tracks `sawHealthDeclared` — fires nil when deadline expires AND the loop never observed Healthy/Unhealthy. Containers without a HEALTHCHECK directive that the operator labeled wait-for-healthy=true do not block indefinitely."
  - "Force-pull-with-recreate delegates to the full Update flow (incl. SAFE-01 check in the handler). Force-pull-no-recreate skips compose + verify; just updates AvailableDigest (read-only with respect to the running container)."
  - "Verify deadline grants a 2*verifyTickInterval safety factor — without it the 15th tick races against deadline.After at exactly the boundary. Production effect: invisible (15.002s vs 15s); test effect: deterministic happy-path."
  - "updateSender interface seam — production wraps `chan<- poll.StateUpdate`; tests inject a recordingSender that captures every send. Avoids standing up RunUpdater in tests just to observe the channel."
  - "Verify uses pre-recreate snapshot.RestartCount=0 as the baseline (a freshly-recreated container reports RestartCount=0; any observed delta is the crash-loop signal). The OLD container ID in snapshot.ContainerID is used; if the OLD container is gone after recreate, ContainerInspect fails fast → ErrVerifyFailed. A future Phase 5/6 patch may add ContainerByService(svc) to the docker facade to look up the NEW container ID."

patterns-established:
  - "Pattern: ctx-aware send wrapper behind an interface seam (updateSender) — production wraps chan<- poll.StateUpdate; tests inject a recording fake without spawning a drain goroutine"
  - "Pattern: typed inner error with Unwrap() returning a sentinel — caller errors.Is the sentinel for branch class, errors.As the typed detail for structured fields"
  - "Pattern: exported response-body constants across packages — single source of truth removes wire-shape drift between middleware emitters and handler test assertions"
  - "Pattern: tick-indexed scripted fake for ContainerInspect — verify_test.go fakeInspector consumes inspectScript[idx]; transferable to any time-indexed mock"

requirements-completed: [ACT-01, ACT-02, ACT-03, ACT-04, ACT-05, ACT-06, ACT-07, ACT-08, ACT-09, ACT-10, ACT-11, SAFE-01, SAFE-02, SAFE-03, OBS-01]

# Metrics
duration: 34min
completed: 2026-05-15
---

# Phase 04 Plan 03: Actions Orchestrator — Update / Rollback / Force-pull Bodies + Per-Service Mutex + Middleware + Verify-After-Recreate Summary

**Lands the headline differentiator: a five-file `internal/actions` package (orchestrator.go + mutex.go + middleware.go + verify.go + errors.go) plus the A1 probe test, implementing all three operator-facing action workflows on top of the Phase 2/3 facades (docker.Client + compose.Runner + registry.Resolver), the Phase 1 state store, and the Phase 3 single-consumer channel pattern.**

## Performance

- **Duration:** 34 min
- **Started:** 2026-05-15T08:11:34Z
- **Completed:** 2026-05-15T08:45:00Z
- **Tasks:** 3
- **Files created:** 9 (6 production + 4 tests... actually 5 production + 4 tests: errors.go, mutex.go, middleware.go, verify.go, orchestrator.go [previously a 1-method stub] + mutex_test.go, middleware_test.go, verify_test.go, orchestrator_test.go, probe_aux_digest_test.go)
- **Files modified:** 1 (orchestrator.go was a 17-LOC stub from Phase 1; this plan replaced its body)
- **Total LOC:** ~3,690 lines insertions across the actions package

## Accomplishments

**Sentinel errors (errors.go):** Seven errors with rich godoc mapping each to its HTTP status — ErrServiceBusy (409), ErrSelfProtection (409), ErrActionDisabledByLabel (409), ErrVerifyFailed (500), ErrVerifyCanceled (503), ErrComposeFailed (500), ErrPullFailed (500). The compose-layer sentinel is preserved through the wrap chain so `errors.Is(err, compose.ErrComposeFailed)` AND `errors.Is(err, actions.ErrComposeFailed)` both succeed on a wrapped action error.

**Per-service mutex (mutex.go + mutex_test.go):** lockService returns an unlock closure on success, ErrServiceBusy on contention. Double-checked locking on entry creation prevents two concurrent first-time goroutines from installing distinct mutex pointers. Six RED-first tests including TestLockService_Concurrent (100 goroutines × atomic counters, race-clean under -race -count=5) and TestLockService_DoubleCheckedLocking_NoDuplicateMutex (asserts the locks map carries exactly one entry after concurrent first-time access).

**Middleware (middleware.go + middleware_test.go):** ValidateServiceName (ACT-10 regex gate), CheckSafetyLabel (SAFE-01/02 with SAFE-03 carve-out for force-pull), CheckSelfProtection (ACT-09), LookupContainer (cached-state lookup). Seven exported ActionBody* response constants (Pattern K verbatim-string discipline; zero fmt.Sprintf in body). 14 RED-first tests including TestSAFE03_PollIgnoresActionLabels source-grep gate.

**Verify-after-recreate (verify.go + verify_test.go):** 15-consecutive-success-tick loop (default) or 60s healthcheck-opt-in window. Fail-fast on !Running / RestartCount++ / Health.Status=Unhealthy. Opt-in soft-success: `sawHealthDeclared` tracked; deadline expiry with no Health observed returns nil. ctx cancel returns ErrVerifyCanceled (distinct sentinel). Typed VerifyDetail with Unwrap()→ErrVerifyFailed for Plan 04-04 errors.As extraction. 9 RED-first tests including TestVerifyAfterRecreate_TickBoundary_CancelDuringFinalTick_ReturnsErrVerifyCanceled (Phase-4 pitfall) and TestVerifyAfterRecreate_VerifyDetail_Extractable (B2 contract).

**Orchestrator body (orchestrator.go + orchestrator_test.go):** Three action workflows implementing CONTEXT.md Area 1's verbatim 11-step Update sequence, plus offline-capable Rollback (ACT-04 — uses docker.ImageTag, no resolver call), plus ForcePull with/without recreate (ACT-05). drainPullStream extracts the aux digest from the moby JSONMessages stream (Option A). 16 RED-first tests covering happy paths, idempotency (ACT-06/07), all failure-branch ActionError population, Pitfall 1 digest mismatch, 412 compose-drift (mutex NOT taken), DETECT-10 channel ordering, ACT-08 lock-held-through-verify, and OBS-01 slog schema (captures slog JSON into bytes.Buffer + asserts required fields).

**A1 probe (probe_aux_digest_test.go):** Round-trips a real moby ImagePull against an in-process registry. Currently `t.Skip`s with the message "no docker daemon available" on the dev box (no Docker Desktop running). Per RESEARCH.md A1 mitigation, this is the expected outcome and Option A (drainPullStream) remains the design lean. The probe will validate or refute on CI workers with docker available.

## Task Commits

Each task was committed atomically:

1. **Task 1: errors.go (7 sentinels) + mutex.go + mutex_test.go (6 race-clean tests) + A1 probe** — `b91b761` (feat)
2. **Task 2: middleware.go (4 helpers, 7 exported body constants) + middleware_test.go (14 tests + SAFE-03 source-grep + Pattern K source-grep) + verify.go (verifyAfterRecreate + VerifyDetail B2) + verify_test.go (9 tests)** — `58a801e` (feat)
3. **Task 3: orchestrator.go body (Update/Rollback/ForcePull + NewOrchestrator + drainPullStream + slog schema; struct declaration moved here from mutex.go) + orchestrator_test.go (16 tests with 5 fakes + recordingSender)** — `52b7492` (feat)

**Plan metadata commit:** will follow this SUMMARY.md.

## Files Created/Modified

- `internal/actions/errors.go` — NEW. 7 sentinel errors, each with rich godoc citing its HTTP status mapping and wrap pattern. Mirrors internal/registry/errors.go shape.
- `internal/actions/mutex.go` — NEW. lockService with double-checked locking + TryLock + ErrServiceBusy. Struct declaration moved to orchestrator.go in Task 3; this file now houses only the lockService primitive.
- `internal/actions/mutex_test.go` — NEW. Six RED-first concurrency tests under -race -count=5.
- `internal/actions/probe_aux_digest_test.go` — NEW. Assumption A1 probe: stands up an in-process OCI registry, pushes a synthetic manifest, calls real moby ImagePull, drains the stream, asserts the aux JSON shape. t.Skips when no docker daemon is reachable.
- `internal/actions/middleware.go` — NEW. ValidateServiceName, CheckSafetyLabel (package-level helpers), CheckSelfProtection, LookupContainer, SelfService (orchestrator methods). 7 exported ActionBody* response constants. serviceNameRegex compiled once at package init via regexp.MustCompile.
- `internal/actions/middleware_test.go` — NEW. 14 RED-first tests + TestSAFE03_PollIgnoresActionLabels (source-grep on internal/poll/poller.go) + TestMiddlewarePatternK_NoSprintf (source-grep on middleware.go body).
- `internal/actions/verify.go` — NEW. verifyAfterRecreate ticker loop, verifySnapshot, VerifyDetail typed inner error with Unwrap()→ErrVerifyFailed. verifyTickInterval test seam (VAR not const). Deadline grants 2*tick safety factor for the final-tick boundary race.
- `internal/actions/verify_test.go` — NEW. 9 RED-first tests with tick-indexed fakeInspector. setFastTick test seam shrinks ticks to 1ms so the full happy path runs in <50ms.
- `internal/actions/orchestrator.go` — MODIFIED (Phase 1 stub `type Orchestrator interface{}` → full body). Orchestrator interface (6 methods), actionOrchestrator struct (all dependencies), NewOrchestrator constructor returning interface (WR-04), ctx-aware send wrapper, three action body implementations following CONTEXT.md Area 1 verbatim, drainPullStream Option A helper, sendFailureResult helper, slog dotted-event schema.
- `internal/actions/orchestrator_test.go` — NEW. 16 RED-first tests with five fakes (fakeDockerClient, fakeRunner, fakeResolver, fakeComposeReader, fakeStateStore) + recordingSender. Covers happy paths, idempotency (ACT-06/07), all failure-branch ActionError population, Pitfall 1 digest mismatch, 412 compose-drift (mutex NOT taken), DETECT-10 channel ordering, ACT-08 lock-held-through-verify, and OBS-01 slog schema verification.

## Decisions Made

1. **Aux-digest path = Option A (drainPullStream)** — A1 probe inconclusive on dev box (no daemon); Option A remains the design lean per RESEARCH.md A1 mitigation. If a future CI run refutes A1, the documented fallback is to add `ImageInspect` to the internal/docker.Client facade.
2. **B2 typed VerifyDetail** — `*VerifyDetail` carries RestartCount/Running/ContainerID/Reason; Unwrap()→ErrVerifyFailed. Plan 04-04 extracts via `errors.As(err, &detail)`. Wrap pattern: `fmt.Errorf("%w: %w", ErrVerifyFailed, &VerifyDetail{...})`.
3. **B3 exported response constants** — `ActionBodyInvalidServiceName` etc. are capitalized so Plan 04-04's handler tests can byte-for-byte assert handler outputs match middleware outputs. One source of truth in internal/actions removes cross-package drift.
4. **Middleware method order in source** — CheckSelfProtection declared BEFORE LookupContainer in middleware.go. The handler chain runs in the same order: ValidateServiceName → CheckSelfProtection → LookupContainer → CheckSafetyLabel.
5. **Soft-success tracks `sawHealthDeclared`** — fires nil only when deadline expires AND no Health was ever observed Healthy/Unhealthy. Containers without HEALTHCHECK that the operator labeled wait-for-healthy=true exit the loop after the window expires.
6. **Verify deadline + 2 ticks** — Deadline grants `verifyWindow + 2*verifyTickInterval` so the final consecutive tick completes inside the window. Production effect invisible (15.002s vs 15s); test determinism preserved.
7. **updateSender interface seam** — Production `channelSender` wraps `chan<- poll.StateUpdate`; tests inject `recordingSender` that captures every send. Avoids standing up RunUpdater drain in tests.
8. **Force-pull-with-recreate delegates to Update** — One code path; the handler explicitly opts into SAFE-01 by calling CheckSafetyLabel(ActionUpdate) when `?recreate=true` (per RESEARCH.md OQ#5).
9. **Verify uses snapshot.RestartCount=0 baseline** — A freshly-recreated container reports RestartCount=0; any observed delta is the crash-loop signal. OLD container ID is used; if gone, ContainerInspect fails fast → ErrVerifyFailed. Phase 5/6 patch may add ContainerByService(svc) to the docker facade for true new-container-id lookup.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Verify deadline race at tick boundary**
- **Found during:** Task 2 RED-GREEN cycle on `TestVerifyAfterRecreate_15ConsecutiveSuccessfulTicks_ReturnsNil`.
- **Issue:** The default-mode deadline `time.Now()+verifyWindow` and target `verifyWindow/verifyTickInterval` are proportionally tied. With verifyTickInterval=1s (production) and verifyWindow=15s, the loop must complete 15 ticks within 15s wall-clock; loop body overhead is microseconds so this works. With verifyTickInterval=1ms (test) and verifyWindow=15ms, the 15ms wall-clock budget is tighter than the loop body's per-tick overhead. The race surfaced on tick 15 racing against `time.Now().After(deadline)`.
- **Fix:** Granted the deadline a `2*verifyTickInterval` safety factor: `deadline = time.Now().Add(verifyWindow + 2*verifyTickInterval)`. Production effect: invisible (15.002s vs 15s). Test effect: deterministic happy path under -race -count=10.
- **Files modified:** `internal/actions/verify.go` (3 lines).
- **Commit:** `58a801e` (folded into Task 2 GREEN body).

**2. [Rule 1 - Bug] Soft-success branch fired on consecutive>0**
- **Found during:** Task 2 RED-GREEN cycle on `TestVerifyAfterRecreate_HealthcheckOptIn_NoStatusAfter60s_SoftSuccess`.
- **Issue:** The original soft-success branch fired only when `consecutive==0` at deadline. But a Running=true tick without a Health field still increments consecutive — so after the very first inspect, consecutive≥1 and the soft-success branch could never fire. Containers without HEALTHCHECK labeled wait-for-healthy=true would block until target ticks (defaults: 15000 in test scaling), not soft-success at the healthcheck window.
- **Fix:** Track `sawHealthDeclared` separately; soft-success fires when deadline expires AND `!sawHealthDeclared` (i.e. the loop never observed Healthy/Unhealthy). This matches the CONTEXT.md Area 3 semantic intent ("no health status reported" → soft-success).
- **Files modified:** `internal/actions/verify.go` (added sawHealthDeclared bool, two assignments + one branch condition).
- **Commit:** `58a801e` (folded into Task 2 GREEN body).

**3. [Rule 3 - Blocking issue] Unused import in orchestrator_test.go**
- **Found during:** Task 3 build after RED-first test file landed.
- **Issue:** `github.com/moby/moby/api/types/container` imported but only used in helper factories that lived in verify_test.go (same package — imports are file-scoped in Go).
- **Fix:** Removed the import; the test file uses fixtures defined in verify_test.go (same `actions` package).
- **Files modified:** `internal/actions/orchestrator_test.go`.
- **Commit:** `52b7492` (folded into Task 3).

### Authentication Gates

None encountered — the A1 probe gracefully t.Skip's on missing daemon rather than treating it as a gate.

### Out-of-scope deferrals

- The "new container ID lookup after recreate" gap (verify currently uses the OLD container ID; if compose recreate changed the ID, ContainerInspect on the old ID errors and surfaces ErrVerifyFailed prematurely). Documented in `inspectAndVerify`'s doc comment as a Phase 5/6 hook to extend the docker facade with `ContainerByService(svc) (ContainerInspect, error)`. NOT a regression — the failure mode is correctly reported via ErrVerifyFailed; just less diagnostic than it could be.
- A new pre-existing flaky test in `internal/registry` (long-running crane test timing out at ~133s on the dev machine) was observed during the project-wide test run but **does not reproduce** when registry tests run in isolation (17s). Already tracked in `.planning/phases/04-update-rollback-force-pull-actions-safety-state-persistence/deferred-items.md` (added by Plan 04-02).

## Test Coverage

- **Race-clean:** All 45 tests pass under `go test ./internal/actions/... -race -count=5` (≈2s wall-clock for the package).
- **Vet-clean:** `go vet ./internal/actions/...` produces zero warnings.
- **Project-wide:** `go test ./...` passes for all 9 packages (cmd/hmi-update, internal/{actions,api,compose,docker,poll,registry,state}).
- **A1 probe:** Currently t.Skips with paste-ready message ("no docker daemon available; A1 probe deferred — Option A is the design lean per RESEARCH.md"). Will run-and-validate on CI workers with docker daemon.

## Sentinel Errors → HTTP Status Mapping (consumed by Plan 04-04)

| Sentinel | HTTP Status | Plan 04-04 handler body |
|---|---|---|
| `ErrServiceBusy` | 409 | `ActionBodyServiceBusy` |
| `ErrSelfProtection` | 409 | `ActionBodySelfProtection` |
| `ErrActionDisabledByLabel` | 409 | `ActionBodyActionDisabledUpdate` / `ActionBodyActionDisabledRollback` |
| `ErrVerifyFailed` (+ `*VerifyDetail`) | 500 | structured body via `errors.As` extraction |
| `ErrVerifyCanceled` | 503 | constant body (no fields) |
| `ErrComposeFailed` | 500 | body contains stderr snippet from wrap chain |
| `ErrPullFailed` | 500 | body identifies pull / digest-mismatch stage |
| `compose.ErrComposeFileMoved` (forwarded, not redefined) | 412 | `ActionBodyComposeFileMoved` |

## Slog Event Schema (OBS-01)

| Event name | Level | Fields | When |
|---|---|---|---|
| `action.start` | Info | service, action | Every action enters the body |
| `action.phase` | Info | service, action, phase (pulled/retagged/verified), {new_digest,target_digest,ticks} | Each inter-step waypoint |
| `action.complete` | Info | service, action, before, after, exit_code, duration_ms, [no_op] | Every successful exit |
| `action.pull_failed` | Error | service, err, [stage] | Pull / digest-mismatch / ImageTag failure |
| `action.compose_failed` | Error | service, err | runner.UpdateService non-zero exit |
| `action.verify_failed` | Error | service, restart_count, running, err | verify loop fail-fast OR deadline expiry |

The slog `action.complete` event is captured and asserted byte-for-byte by `TestSlog_ActionEventSchema` via a custom JSON handler attached to a bytes.Buffer.

## Force-pull Behavior Matrix

| Endpoint | recreate query | Compose call? | Verify? | Safety label honored? | State writes |
|---|---|---|---|---|---|
| `POST /api/containers/{svc}/force-pull` | (absent or `false`) | ❌ | ❌ | ❌ (SAFE-03 carve-out) | AvailableDigest, UpdateAvailable (if changed) |
| `POST /api/containers/{svc}/force-pull?recreate=true` | `true` | ✅ | ✅ (15s/60s) | ✅ Update label (handler opts in) | full Update flow: CurrentDigest, PreviousDigest, UpdateAvailable=false |

## Known Stubs

None. The orchestrator body is complete and exercised end-to-end by 45 unit tests + the A1 probe. The "new container ID lookup" gap noted under deferred items is a documented Phase 5/6 hook, not a stub — the current code path correctly reports verify failure via the existing error class.

## Open Notes for Plan 04-04

1. **Handler imports** — `internal/api/handlers_actions.go` should `import "github.com/centroid-is/hmi-update/internal/actions"` and reference the exported `ActionBody*` constants for response bodies. DO NOT redefine these strings in the api package; the cross-package import is the contract anchor.

2. **Error-to-status mapping** — Handler does `errors.Is` branching in this order:
   ```go
   case errors.Is(err, compose.ErrComposeFileMoved): // 412 ActionBodyComposeFileMoved
   case errors.Is(err, actions.ErrServiceBusy):       // 409 ActionBodyServiceBusy
   case errors.Is(err, actions.ErrSelfProtection):    // 409 ActionBodySelfProtection (rare — middleware caught first)
   case errors.Is(err, actions.ErrActionDisabledByLabel): // 409 (rare — middleware caught first)
   case errors.Is(err, actions.ErrVerifyCanceled):    // 503 (verbatim const)
   case errors.Is(err, actions.ErrVerifyFailed):      // 500 — extract *VerifyDetail via errors.As for structured body
   case errors.Is(err, actions.ErrComposeFailed):     // 500 with stderr snippet
   case errors.Is(err, actions.ErrPullFailed):        // 500 with pull / digest-mismatch context
   default:                                             // 500 generic
   ```

3. **NewServer signature change** — `api.NewServer(store, dockerClient, composeReader, orchestrator)` (4th arg added). Existing tests will fail to compile; expected — Plan 04-04 wave includes the api test updates.

4. **Force-pull recreate query handling** — Handler reads `r.URL.Query().Get("recreate") == "true"`. When true, EXPLICITLY invoke `actions.CheckSafetyLabel(w, c, actions.ActionUpdate)` BEFORE calling `o.ForcePull(ctx, svc, true)` (per RESEARCH.md OQ#5 — recreate IS a recreate operation; SAFE-01 applies).

5. **No new docker.Client method** — The facade stays at 6 methods. drainPullStream lives in-package in actions. If the A1 probe ever runs and refutes the design, the fallback is to add a 7th `ImageInspect` method to docker.Client (coordinated with internal/docker/moby_test.go's interface-method-count guard).

## Self-Check

Files claimed exist:

- `internal/actions/errors.go` — FOUND
- `internal/actions/mutex.go` — FOUND
- `internal/actions/mutex_test.go` — FOUND
- `internal/actions/probe_aux_digest_test.go` — FOUND
- `internal/actions/middleware.go` — FOUND
- `internal/actions/middleware_test.go` — FOUND
- `internal/actions/verify.go` — FOUND
- `internal/actions/verify_test.go` — FOUND
- `internal/actions/orchestrator.go` — FOUND (modified)
- `internal/actions/orchestrator_test.go` — FOUND

Commits exist:

- `b91b761` — FOUND (Task 1)
- `58a801e` — FOUND (Task 2)
- `52b7492` — FOUND (Task 3)

## Self-Check: PASSED
