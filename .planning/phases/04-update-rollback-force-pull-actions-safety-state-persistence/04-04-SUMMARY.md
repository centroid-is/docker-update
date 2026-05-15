---
phase: 04-update-rollback-force-pull-actions-safety-state-persistence
plan: 04
subsystem: api
tags: [http-handlers, action-endpoints, sentinel-dispatch, verify-failed-structured-body, obs-03-noio-guard, main-boot-wiring, api-md, pattern-k, t-04-04-03]

# Dependency graph
requires:
  - phase: 04-update-rollback-force-pull-actions-safety-state-persistence/01
    provides: state.Container ActionInFlight/ActionError fields; poll.UpdateKind action variants
  - phase: 04-update-rollback-force-pull-actions-safety-state-persistence/02
    provides: compose.NewRunner(composePath) + compose.ErrComposeFailed sentinel
  - phase: 04-update-rollback-force-pull-actions-safety-state-persistence/03
    provides: actions.Orchestrator interface (6 methods); ActionResult struct; 7 sentinel errors with documented HTTP mapping; VerifyDetail typed inner error with Unwrap()→ErrVerifyFailed (consumed via errors.As); 7 exported ActionBody* response constants; ValidateServiceName + CheckSafetyLabel; actions.NewOrchestrator constructor
provides:
  - "Three HTTP action endpoints registered via Go 1.22+ method-scoped ServeMux: POST /api/containers/{service}/update, /rollback, /force-pull (with optional ?recreate=true)"
  - "writeActionError dispatcher: errors.Is over compose.ErrComposeFileMoved + 7 action sentinels + the no_previous_digest substring check → documented HTTP status + verbatim-constant body"
  - "writeVerifyFailedBody: SOLE Pattern K exception; errors.As extracts *actions.VerifyDetail; emits structured JSON body shape locked in CONTEXT.md Area 3 (error, reason, exit_code:null, restart_count, running, container_id)"
  - "Server constructor extended to 4-arg api.NewServer(store, dockerClient, composeReader, orchestrator); defensive nil-guard branch returns 503 actionBodyOrchestratorUnwired"
  - "Three Phase 4 env vars wired in main.go (HMI_UPDATE_SELF_SERVICE / HMI_UPDATE_VERIFY_WINDOW_S / HMI_UPDATE_HEALTHCHECK_WINDOW_S) with defaults hmi-update / 15s / 60s"
  - "OBS-03 invariant guard: TestGetState_NoIO injects panickingDockerClient (panics on all 6 docker.Client methods) and runs GET /api/state 100x — proves no docker call leaks into the read path"
  - "API.md repo-root operator reference: 3 endpoints, error code matrix, verify_failed structured body, OBS-01 slog schema, Manual self-upgrade procedure, full Phase 1-4 env var table"
affects: [04-06 e2e specs (hit these endpoints; verify-failed spec asserts structured body shape), Phase 5 UI (consumes /api/state, calls action endpoints), operator workflows (API.md is the field-engineer reference)]

# Tech tracking
tech-stack:
  added: []  # zero new go.mod deps; reuses internal/actions + internal/compose + stdlib net/http, encoding/json, errors, log/slog, strconv, time
  patterns:
    - "Verbatim-constant response body pattern (Pattern K) with EXACTLY ONE locked exception — writeVerifyFailedBody emits structured JSON whose inputs are pre-trimmed by the orchestrator (integers + bool + sha256-format string + reason string with no operator paths in the trim domain). Tracked as T-04-04-03 in threat model."
    - "Cross-package exported response body constants — handler imports actions.ActionBody* rather than redefining; single source of truth removes the wire-shape drift surface"
    - "Sentinel-dispatcher switch with errors.Is + one isNoPreviousDigest substring helper — order-sensitive (compose.ErrComposeFileMoved tested BEFORE actions.ErrComposeFailed because they are distinct error classes with different HTTP statuses)"
    - "errors.As over typed-inner-error pattern — VerifyDetail satisfies the error interface (Error()→Reason) AND Unwrap()→ErrVerifyFailed, so errors.Is dispatches the class and errors.As extracts the structured fields"
    - "Defensive nil-guard at the top of each handler — Plan 04-04 mirrors the WR-03 healthz pattern (dedicated unwired body, not a misleading reuse of another reason string)"
    - "Path-leak guard table-driven test — every error class is exercised with a tempdir-prefixed wrap chain; the assertion bytes.Contains(body, tempDir) catches any echo. Belt-and-braces /private/, /var/folders/, /tmp/ prefix rejection covers macOS + Linux + Docker bind-mount."
    - "OBS-03 panicking-client test pattern — fake docker.Client panics with method name on every call; 100 GETs in tight loop catch any deferred/amortized I/O leak"

key-files:
  created:
    - internal/api/handlers_actions.go
    - internal/api/handlers_actions_test.go
    - internal/api/getstate_noio_test.go
    - API.md
  modified:
    - internal/api/server.go
    - internal/api/server_test.go
    - internal/api/handlers_healthz_test.go
    - cmd/hmi-update/main.go

key-decisions:
  - "Single-line NewServer constructor signature — the acceptance-criteria grep gate expects the literal `NewServer(store *state.Store, dockerClient docker.Client, composeReader *compose.Reader, orchestrator actions.Orchestrator)`. Multi-line form (Go convention for 4+ args) breaks the grep — reflowed to one line to keep the verifier gate green."
  - "envInt duplicated in main.go (not promoted from internal/poll) — keeps the poll package's exported surface narrow. 04-PATTERNS.md line 931 explicitly condones either path; copy-paste is simpler and the two helpers are identical 6-line functions."
  - "writeActionError ordering: compose.ErrComposeFileMoved is tested BEFORE actions.ErrComposeFailed (compose-layer 412 sentinel is distinct from actions-layer 500 runtime non-zero exit). Without explicit ordering, the chained-sentinel wrap shape could match either via errors.Is — the order encodes the semantics."
  - "isNoPreviousDigest substring check is the contract anchor for the Rollback-specific 400 — the orchestrator emits the exact token `no_previous_digest` surrounded by the wrap chain `actions.Rollback <svc>: no_previous_digest`. A future revision may promote this to a dedicated sentinel; the contract is currently the substring."
  - "Boot log echoes self_service / verify_window / healthcheck_window at startup — operator-actionable visibility at the action-endpoint introduction (mirrors the existing state_path / compose_path echo from Phases 1+2)."
  - "API.md placed at REPO ROOT (not docs/ subdir) — CONTEXT.md Area 1 calls it out as the operator-facing source of truth and the Phase 4 plan registers it as a top-level artifact. Repo-root placement matches CLAUDE.md / PROJECT.md conventions for operator-discoverable docs."

patterns-established:
  - "Pattern: cross-package exported wire-contract constants — Plan 04-03 exported ActionBody*; Plan 04-04 imports + reuses. One source of truth removes drift between middleware emitters and handler responses."
  - "Pattern: errors.As + typed-inner-error for structured response bodies — VerifyDetail Unwrap()→sentinel is the load-bearing primitive. Handler tests errors.Is for class dispatch, errors.As for field extraction. Reusable for any future structured-body error class."
  - "Pattern: panicking-fake injection for invariant proofs — panickingDockerClient panics with method name; works because the test framework recovers panic into Failed status. Reusable for any I/O-vs-no-I/O contract."
  - "Pattern: path-leak guard table-driven test — every error class is a row; the bytes.Contains assertion catches any echo. Extension point for future sensitive-string classes (env var values, tokens, etc.)."

requirements-completed: [ACT-01, ACT-03, ACT-05, ACT-09, ACT-11, SAFE-01, SAFE-02, OBS-01, OBS-03]

# Metrics
duration: 18min
completed: 2026-05-15
---

# Phase 04 Plan 04: HTTP Action Handlers + Server Constructor + main.go Boot Wiring + API.md Summary

**Wires Plan 04-03's orchestrator into the HTTP surface AND the boot sequence: three new HTTP handlers consuming the seven sentinel errors via a single writeActionError dispatcher with one locked structured-body exception (writeVerifyFailedBody), Server.NewServer extended to 4-arg with a defensive nil-guard, main.go boot order extended with compose.NewRunner + three new env vars + actions.NewOrchestrator + the 4-arg api.NewServer, an OBS-03 no-I/O invariant guard test using a panicking docker.Client, and a repo-root API.md documenting all three endpoints with error code matrices, the locked verify-failed body shape, the OBS-01 slog schema, and the operator self-upgrade procedure.**

## Performance

- **Duration:** ~18 min
- **Tasks:** 2 (Task 1: handlers + Server + OBS-03; Task 2: main.go + API.md)
- **Files created:** 4 (handlers_actions.go, handlers_actions_test.go, getstate_noio_test.go, API.md)
- **Files modified:** 4 (server.go, server_test.go, handlers_healthz_test.go, cmd/hmi-update/main.go)
- **Total LOC:** ~1,740 insertions across 8 files

## Accomplishments

**Three HTTP action handlers (handlers_actions.go, 360 LOC):** Each handler follows the locked middleware chain — `ValidateServiceName → CheckSelfProtection → LookupContainer → CheckSafetyLabel → orchestrator.<Action>`. CheckSelfProtection runs BEFORE LookupContainer (B1 invariant from Plan 04-03 review) because hmi-update is NOT in the watched-containers cache by default — running LookupContainer first would return 404 (misleading) instead of 409 self_protection (operator-actionable). The force-pull handler applies the Update safety-label check ONLY when `?recreate=true` is set (SAFE-03 carve-out for default; SAFE-01 applies on recreate per RESEARCH OQ#5).

**writeActionError dispatcher:** One switch over `errors.Is` covering all 7 action sentinels + compose.ErrComposeFileMoved + the substring-based no_previous_digest detection. Order is load-bearing: compose.ErrComposeFileMoved is tested BEFORE actions.ErrComposeFailed (distinct sentinels, distinct HTTP statuses 412 vs 500). Default branch logs `handlers_actions.unknown_error` via slog so an unrecognized sentinel surfaces in operator-readable logs.

**writeVerifyFailedBody (Pattern K's sole exception):** Uses `errors.As(err, &detail)` to extract `*actions.VerifyDetail` from the orchestrator's double-wrapped `fmt.Errorf("%w: %w", ErrVerifyFailed, &VerifyDetail{...})`. Emits the structured body shape locked in CONTEXT.md Area 3 lines 102-112 (`error`, `reason`, `exit_code:null`, `restart_count`, `running`, `container_id`). Inputs are pre-trimmed by the orchestrator (Reason is `fmt.Sprintf` over integers + duration; no operator paths in the trim domain). Tracked as T-04-04-03 in the threat model. Safety net: if the wrap chain does NOT carry a `*VerifyDetail` (orchestrator invariant violation), falls back to a verbatim constant body and logs `handlers_actions.verify_failed_missing_detail`.

**Server constructor (server.go):** Extended to 4-arg `NewServer(store, dockerClient, composeReader, orchestrator)`. Reflowed to a single-line signature so the verifier's literal-grep gate passes. Routes added: `POST /api/containers/{service}/{update,rollback,force-pull}` via Go 1.22+ method-scoped ServeMux patterns. Defensive nil-guard: a nil orchestrator returns 503 with a dedicated body (`actionBodyOrchestratorUnwired`); production main.go log.Fatalf's on NewOrchestrator errors so this branch is only reachable via test wiring.

**Test coverage (handlers_actions_test.go, 766 LOC):** 21 RED-first tests with a single fakeOrchestrator (per-service script maps + invocation recorders). Covers happy paths (update / rollback / force-pull default / force-pull recreate), every error-to-status branch (400 / 404 / 409 / 412 / 500 / 503), the verify-failed structured body field-by-field assertion, the SAFE-03 carve-out (force-pull-no-recreate exempt from labels), the SAFE-01 application (force-pull-recreate honors allow-update), the orchestrator-unwired defensive guard, the route registration smoke test, and the path-leak guard for EVERY error class.

**OBS-03 invariant guard (getstate_noio_test.go, 100 LOC):** panickingDockerClient implements docker.Client with all 6 methods panicking with diagnostic messages naming which method was called. TestGetState_NoIO seeds a state.Store with one container, wires the panicking client, and runs GET /api/state 100x in tight loop. The 100x amplifier catches any deferred or amortized I/O. If anyone adds a docker call reachable from GET /api/state, the test fails on iteration 1 with the panic message naming the leaked method.

**main.go boot wiring (cmd/hmi-update/main.go):** Inserts step 4.11 (compose.NewRunner after compose.NewReader, before registry transport setup), step 5.8 (reads HMI_UPDATE_SELF_SERVICE default "hmi-update", HMI_UPDATE_VERIFY_WINDOW_S default 15s, HMI_UPDATE_HEALTHCHECK_WINDOW_S default 60s), step 5.9 (actions.NewOrchestrator wiring all 9 dependencies — dockerClient, runner, resolver, composeReader, store, updates, selfService, verifyWindow, healthcheckWindow), and updates step 6 to the 4-arg api.NewServer signature. envInt helper duplicated locally (matches internal/poll/poller.go convention). New imports: strconv, time, internal/actions. Boot log echoes the three new env values for operator visibility.

**API.md (repo root, 312 LOC):** Operator-facing reference covering endpoints at a glance, service-name allowlist (ACT-10), middleware chain (load-bearing order), all three POST endpoints with request/response shape + error code matrix + the locked verify_failed body, GET /api/state (OBS-03 contract), the OBS-01 slog event schema (7 event names + required fields), the Manual self-upgrade procedure (verbatim from CONTEXT.md), the full Phase 1-4 env-var configuration table (11 vars), and a container labels reference (6 labels). Threat model notes section flags T-04-04-03 as the sole Pattern K exception.

## Task Commits

Each task was committed atomically:

1. **Task 1: HTTP action handlers + 4-arg Server + OBS-03 no-I/O guard** — `edfe931` (feat)
2. **Task 2: main.go boot wiring + API.md operator-facing reference** — `6a74361` (feat)

**Plan metadata commit:** will follow this SUMMARY.md.

## Error-to-HTTP-Status Mapping (full table — load-bearing for Plan 04-06 e2e specs)

| Error class                              | HTTP | Body source                                    | Notes                                                                            |
| ----------------------------------------- | ---- | ---------------------------------------------- | -------------------------------------------------------------------------------- |
| Invalid service name regex                | 400  | `actions.ActionBodyInvalidServiceName`         | Written by `actions.ValidateServiceName` middleware (before any handler logic)   |
| `isNoPreviousDigest(err)` (substring)     | 400  | `actionBodyNoPreviousDigest` (handler-owned)   | Rollback-specific; orchestrator emits the literal token in the error string      |
| Container not in cached state             | 404  | `actions.ActionBodyContainerNotFound`          | Handler writes directly after `LookupContainer` returns false                    |
| `actions.ErrServiceBusy`                  | 409  | `actions.ActionBodyServiceBusy`                | per-service mutex contention (ACT-08)                                            |
| `actions.ErrSelfProtection` (rare path)   | 409  | `actions.ActionBodySelfProtection`             | Middleware `CheckSelfProtection` normally writes directly; sentinel kept for future programmatic consumers |
| `actions.ErrActionDisabledByLabel` (rare) | 409  | `actions.ActionBodyActionDisabledUpdate`       | Middleware `CheckSafetyLabel` normally writes directly                           |
| Self-protection (middleware-direct)       | 409  | `actions.ActionBodySelfProtection`             | `CheckSelfProtection` writes BEFORE handler reaches orchestrator                |
| `allow-update=false` label                | 409  | `actions.ActionBodyActionDisabledUpdate`       | `CheckSafetyLabel(ActionUpdate)` writes directly                                 |
| `allow-rollback=false` label              | 409  | `actions.ActionBodyActionDisabledRollback`     | `CheckSafetyLabel(ActionRollback)` writes directly                               |
| `compose.ErrComposeFileMoved`             | 412  | `actions.ActionBodyComposeFileMoved`           | Compose file inode/mtime/size drifted from boot (Pitfall 10)                     |
| `actions.ErrPullFailed`                   | 500  | `actionBodyPullFailed` (handler-owned)         | "see logs for action.pull_failed" — never echoes pull stderr/url                 |
| `actions.ErrComposeFailed`                | 500  | `actionBodyComposeFailed` (handler-owned)      | "see logs for action.compose_failed" — never echoes stderr (T-04-04-04)          |
| `actions.ErrVerifyFailed`                 | 500  | **STRUCTURED** via `writeVerifyFailedBody`     | `errors.As(err, &detail)` extracts `*VerifyDetail`; emits CONTEXT Area 3 shape   |
| Default (unknown sentinel)                | 500  | `actionBodyInternal` (handler-owned)           | Logs via `handlers_actions.unknown_error` slog event                             |
| Server orchestrator nil (test-only)       | 503  | `actionBodyOrchestratorUnwired` (handler-owned) | Defensive guard; production main.go log.Fatalf's so unreachable normally        |
| `actions.ErrVerifyCanceled`               | 503  | `actionBodyVerifyCanceled` (handler-owned)     | ctx cancellation during verify (SIGTERM, request abandon)                        |

## writeVerifyFailedBody — Pattern K Exception (T-04-04-03)

The structured-body shape is the SOLE exception to Pattern K (verbatim-constant response bodies) in handlers_actions.go. The body is locked by CONTEXT.md Area 3 lines 102-112:

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

**Why accepted:** Inputs are typed integer fields (`RestartCount`), a `bool` (`Running`), a sha256-format string (`ContainerID`), and a `Reason` string pre-trimmed by the orchestrator. The orchestrator constructs `Reason` via `fmt.Sprintf("container restarted %d times in %s", delta, snap.VerifyWindow)` (verify.go lines 270, 223, 238, 258, 284) — no operator paths in the trim domain. The handler uses `errors.As(err, &detail)` to extract the typed inner error and `json.NewEncoder(w).Encode` of a hard-coded struct literal — no `fmt.Sprintf` of operator-influenced strings on the wire path.

**Path-leak guard proof:** `TestHandleUpdate_VerifyFailed_500_StructuredBody` asserts:
- Field-by-field shape match (`error == "verify_failed"`, `reason`, `restart_count`, `running`, `container_id`, `exit_code == nil`)
- `bytes.Contains(rec.Body.Bytes(), []byte(t.TempDir())) == false` — explicit tempdir-prefix rejection

`TestHandleActions_PathLeakGuard` runs the verify_failed branch (along with every other error class) with a tempdir-prefixed wrap chain and asserts the body does NOT echo the tempdir prefix, `/private/`, `/var/folders/`, or `/tmp/`. This is the load-bearing T-01-04-03 invariant applied to every action error class.

## Server Constructor 4-Arg Signature Impact

| Caller                          | Change                                              |
| -------------------------------- | -------------------------------------------------- |
| `internal/api/server_test.go::newTestServer`         | 4th arg: `nil` (exercises defensive nil-guard)     |
| `internal/api/server_test.go::newTestServerWithContainer` | 4th arg: `nil`                                |
| `internal/api/handlers_healthz_test.go::TestHealthzScenarios` | 4th arg: `nil` (line 218 updated)        |
| `internal/api/handlers_actions_test.go::newOrchestratorTestServer` | 4th arg: `fake *fakeOrchestrator`     |
| `internal/api/getstate_noio_test.go::TestGetState_NoIO` | 4th arg: `nil`                                |
| `cmd/hmi-update/main.go::main` step 6                | 4th arg: production `orchestrator`                 |

All Phase 1-3 tests pass with `nil` orchestrator via the defensive nil-guard. The only callers that wire a non-nil orchestrator are: (a) production main.go and (b) the dedicated handlers_actions_test.go fake-orchestrator path.

## main.go Boot Order with Phase 4 Additions

```
1.   slog handler (HMI_UPDATE_LOG_LEVEL)
2.   state.NewStore(HMI_UPDATE_STATE_PATH)
3.   docker.NewClient(ctx)
4.   compose.NewReader(HMI_UPDATE_COMPOSE_PATH)
4.11 compose.NewRunner(composePath)                 ← Phase 4 plan 04-02 wiring
4.5  registry.NewRedactingTransport
4.6  registry.NewResolver(transport)
4.7  slog.Info("registry.authn", "keychain", "anonymous")
4.8  poll.NewPatterns
4.9  updates := make(chan poll.StateUpdate, 64)
4.10 go poll.RunUpdater(ctx, updates, store)
5.   docker.NewDiscoverer + go discoverer.Run(ctx)
5.5  cronExpr := getenv("HMI_UPDATE_CRON", "0 * * * *")
5.6  poll.NewPoller(cronExpr, resolver, patterns, store, updates)
5.7  go poller.Run(ctx)
5.8  selfService / verifyWindow / healthcheckWindow env reads  ← Phase 4 Plan 04-04
5.9  actions.NewOrchestrator(dockerClient, runner, resolver,    ← Phase 4 Plan 04-04
                            composeReader, store, updates,
                            selfService, verifyWindow,
                            healthcheckWindow)
6.   api.NewServer(store, dockerClient, composeReader,          ← 4-arg signature
                   orchestrator).ListenAndServe(":8080")
```

## API.md Table of Contents

1. **Endpoints at a glance** — 5-row summary table (healthz / state / update / rollback / force-pull) — operator-facing
2. **Service-name allowlist (ACT-10)** — regex + 400 response shape — operator-facing
3. **Middleware chain (load-bearing order)** — pinned source-grep gate — operator-facing
4. **POST /api/containers/{service}/update** — full endpoint contract — operator-facing
5. **POST /api/containers/{service}/rollback** — full endpoint contract — operator-facing
6. **POST /api/containers/{service}/force-pull** — full endpoint contract with SAFE-03 carve-out + SAFE-01 application — operator-facing
7. **GET /api/state** — 5-second UI poll target; OBS-03 contract — operator-facing
8. **Slog event schema (OBS-01)** — 7-row table; bearer-token redaction note — operator-facing (debugging)
9. **Manual self-upgrade procedure** — verbatim 3-step procedure from CONTEXT.md — operator-facing
10. **Configuration knobs** — full Phase 1-4 env var table (11 vars) — operator-facing
11. **Container labels reference (excerpted)** — 6-label table; cross-ref to PROJECT.md — operator-facing
12. **Threat model notes** — T-04-04-03 verify_failed body; path-leak guard tests — developer-facing

## Decisions Made

1. **Single-line NewServer signature** — Reflowed from multi-line to single-line so the literal grep gate passes. Go convention permits both; the grep gate's expectation is the contract anchor.
2. **envInt helper duplicated locally** — 04-PATTERNS.md condones either path; copy-paste keeps the poll package's exported surface narrow.
3. **writeActionError ordering: compose.ErrComposeFileMoved before actions.ErrComposeFailed** — Distinct sentinels (412 vs 500), distinct semantics (pre-action drift vs runtime exit). Order encodes the dispatch precedence.
4. **isNoPreviousDigest substring check (not a sentinel)** — Orchestrator emits the literal token in the wrapped error string; substring is the contract anchor. A future revision may promote to a dedicated sentinel.
5. **API.md at repo root** — Matches CLAUDE.md / PROJECT.md placement convention for operator-discoverable docs.
6. **Boot log echoes self_service / verify_window / healthcheck_window** — operator-actionable visibility at the action-endpoint introduction.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking issue] NewServer signature was multi-line; grep gate expected single-line**
- **Found during:** Task 1 acceptance-criteria audit
- **Issue:** The plan's acceptance criterion contains a literal grep `grep -F 'NewServer(store *state.Store, dockerClient docker.Client, composeReader *compose.Reader, orchestrator actions.Orchestrator)' internal/api/server.go` which expects a single-line signature. The existing NewServer was formatted across 6 lines (Go convention for 4+ args).
- **Fix:** Reflowed the signature to a single line. Build, tests, and vet remain green. The godoc block above the signature is unchanged.
- **Files modified:** internal/api/server.go (1 functional change)
- **Verification:** grep gate now returns 1; `go test ./internal/api/... -race -count=2` passes.
- **Committed in:** edfe931 (Task 1)

### Authentication Gates

None encountered. The boot wiring exercises `actions.NewOrchestrator` which has no auth path; the test suite runs in-process with no daemon or registry access.

### Out-of-scope deferrals

- The hmi-update-brief.md file is untracked at the repo root (pre-existing). NOT committed by this plan; out of scope.
- The .planning/phases/06-display-blackout-ux-checkpoint/ directory contains untracked planning artifacts for a future phase. NOT committed by this plan; out of scope.

## Test Coverage

- **Race-clean:** `go test ./internal/api/... -race -count=2` passes (2× iterations) in 3.3s.
- **Whole-repo regression:** `go test ./... -race -count=1` passes for all 9 packages (cmd/hmi-update, internal/{actions,api,compose,docker,poll,registry,state}).
- **Vet-clean:** `go vet ./...` produces zero warnings.
- **Build-clean:** `go build ./...` produces a working binary.
- **Grep gates:** All 13 success-criteria grep gates pass (counts ≥ expected on every gate).

## Open Notes for Plan 04-06 (e2e specs)

1. **update-flow.spec.ts** — POST `/api/containers/stub-watched-container/update`. Response shape: `{current_digest:"sha256:...", previous_digest:"sha256:..."}` on success; `{no_op:true, current_digest, previous_digest}` on idempotency (ACT-06).

2. **rollback-flow.spec.ts** — Two tests in one file: online rollback (ACT-03) and offline rollback (ACT-04 — detach zot via fixtures/disconnect-network.ts). Response: digests swapped vs Update. Idempotency (ACT-07) returns `no_op:true`.

3. **idempotency.spec.ts** — ACT-06/07 no-op response shapes.

4. **concurrent-actions.spec.ts** — Double-click → 409 `service_busy` (ACT-08). Cross-service parallel works (per-service mutex serializes only same-service collisions).

5. **self-protection.spec.ts** — POST `/api/containers/hmi-update/update` → 409 `self_protection` body `actions.ActionBodySelfProtection`. ACT-09 + CheckSelfProtection-BEFORE-LookupContainer invariant.

6. **safety-labels.spec.ts** — SAFE-01 (`hmi-update.allow-update=false` → 409 `action_disabled_by_label`); SAFE-02 (`hmi-update.allow-rollback=false` → 409); SAFE-03 (poll still ticks for safety-locked containers — Phase 5 UI shows them as up-to-date / behind).

7. **verify-failed.spec.ts** — RECREATE a crash-looping container; expect 500 with the LOCKED structured body shape:
   ```json
   {
     "error": "verify_failed",
     "reason": "container restarted N times in 15s",
     "exit_code": null,
     "restart_count": N,
     "running": false,
     "container_id": "..."
   }
   ```
   Field-by-field assertion via `JSON.parse(response.body)`. The test must NOT assume the wrap chain echoes any operator-supplied path.

8. **restart-persistence.spec.ts** — ACT-12 + STATE-04: `docker compose restart hmi-update` preserves the state file's digests across the recreate.

The verify-failed spec is the load-bearing assertion for the T-04-04-03 Pattern K exception. If a future regression echoes a path into the `reason` field, this spec catches it at the e2e layer (in addition to the unit-level path-leak guard).

## Known Stubs

None. The handler bodies are complete; all error branches map to documented HTTP statuses; all wire-contract bodies are verbatim constants or the one locked structured-body exception. The orchestrator dependency is real (actions.Orchestrator from Plan 04-03); main.go wires the production constructors; tests inject fakes exclusively at the orchestrator interface (no stubbed-out functionality reaches the wire).

## Self-Check

Files claimed exist:

- `internal/api/handlers_actions.go` — FOUND
- `internal/api/handlers_actions_test.go` — FOUND
- `internal/api/getstate_noio_test.go` — FOUND
- `internal/api/server.go` — FOUND (modified)
- `internal/api/server_test.go` — FOUND (modified)
- `internal/api/handlers_healthz_test.go` — FOUND (modified)
- `cmd/hmi-update/main.go` — FOUND (modified)
- `API.md` — FOUND

Commits exist:

- `edfe931` (Task 1) — FOUND in git log
- `6a74361` (Task 2) — FOUND in git log

## Self-Check: PASSED
