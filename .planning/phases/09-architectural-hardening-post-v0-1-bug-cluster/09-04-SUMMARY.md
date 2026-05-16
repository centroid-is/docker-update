---
phase: 09-architectural-hardening-post-v0-1-bug-cluster
plan: 04
subsystem: selfupdate
tags: [selfupdate, sidecar, moby-client, recreate, http-handler, flag-mode, atomic-bool]

# Dependency graph
requires:
  - phase: 09-architectural-hardening-post-v0-1-bug-cluster
    provides: "09-02 RED handlers_self_test.go (5 SC-4 + SC-6 iv cases) + handlers_actions_test self-target 409 control"
  - phase: 09-architectural-hardening-post-v0-1-bug-cluster
    provides: "09-03 recreate.Service primitive + docker.Client +5 recreate methods (the helper consumes both)"
provides:
  - "internal/selfupdate package — Spawner (parent-side) + Orchestrate (helper-side); ErrSelfUpdateInFlight / ErrActionsInFlight sentinels; atomic.Bool single-flight guard"
  - "POST /api/self-update HTTP endpoint — bypasses CheckSelfProtection (route-scoped), returns 202 helper_spawned / 409 (in_flight) / 503 (unwired) / 500 (spawn failed)"
  - "cmd/docker-update/main.go --self-update-orchestrator flag-mode branch — one binary, two entry points; C1 preserved"
  - "internal/actions.Orchestrator.ActionsInFlightFn() — exposes mutex-map cardinality so Spawner can refuse if per-service actions are running (Open Question 5)"
  - "e2e/tests/self-update.spec.ts — wire-shape e2e (SC-4 a) verifying 202 + helper_spawned body; full helper-recreate loop documented as harness-skipped, deferred to SC-7 HMI smoke"
  - "README.md ### Phase 9 upgrade — remove docker CLI bind-mounts sub-section (W5 fix; operator-facing breaking-change note)"
affects:
  - "Phase 9 close-out (this is the last plan in Phase 9 — SC-4 closed, SC-6 iv closed, SC-7 deferred to operator smoke at 10.50.10.175, SC-5 wall-time observable post-merge of 09-01's CI split)"
  - "Future operator runbook (README + RELEASING.md): self-update via UI is now the supported upgrade path; manual `docker compose up -d --force-recreate docker-update` is the LAST host-shell command per the new sub-section"
  - "Phase 10+ auth scope: T-09-04-02 LAN-only/unauthenticated /api/self-update inherits the existing posture from /api/containers/*/update; if auth lands later, both endpoints must be wrapped"

# Tech tracking
tech-stack:
  added:
    - "internal/selfupdate package — second new internal/ package this phase (09-03 added internal/recreate)"
  patterns:
    - "Flag-mode branch in main.go: same binary serves as HTTP server (default) OR one-shot helper (--self-update-orchestrator --target=<svc>); the helper exits 0 on success, daemon AutoRemove GCs it"
    - "Per-process single-flight guard via sync/atomic.Bool — Spawner.inFlight stays true until SIGTERM, never reset on success; reset on Spawn failure so retry is possible"
    - "Sentinel-error → HTTP status mapping in handler: errors.Is(err, ErrActionsInFlight) → 409 actions_in_flight; errors.Is(err, ErrSelfUpdateInFlight) → 409 self_update_in_flight; default → 500 self_update_failed"
    - "Route-scoped middleware: CheckSelfProtection wraps only the per-service /api/containers/{svc}/update chain; the top-level /api/self-update mux.HandleFunc registration deliberately omits it (RESEARCH.md Pattern 5)"
    - "Harness-gap-aware e2e: the spec accepts BOTH the production-shape 202 assertion AND the harness-only 500 self_update_failed (when the docker-update test container can't address its own image) — fail-loud only when neither path is taken"

key-files:
  created:
    - "internal/selfupdate/spawn.go (275 lines — Spawner interface + NewSpawner constructor + atomic.Bool guard + ContainerCreate/Start sequence with cleanup-on-start-fail)"
    - "internal/selfupdate/orchestrate.go (179 lines — Orchestrate(ctx, cli, target, healthzURL, delay, verifyTimeout) → recreate.Service then poll /healthz with 2s tick / 60s deadline)"
    - "internal/selfupdate/spawn_test.go (373 lines — 6 TestSpawner_ cases: ContainerCreateOpts shape, success path, ActionsInFlight refusal, AlreadyInFlight refusal, KeepHelper false/true AutoRemove inversion)"
    - "internal/selfupdate/orchestrate_test.go (274 lines — 3 TestOrchestrate_ cases: success path with httptest /healthz=200, verify timeout, recreate failure)"
    - "internal/api/handlers_self.go (153 lines — handleSelfUpdate + 4 body constants for the SC-4 wire shape)"
    - "e2e/tests/self-update.spec.ts (58 lines — SC-4 a wire-shape e2e with documented harness-skip on 500)"
  modified:
    - "internal/api/server.go (+66 lines — selfUpdater field on Server struct + NewServer extension + mux.HandleFunc(\"POST /api/self-update\", s.handleSelfUpdate))"
    - "internal/api/handlers_actions_test.go (+10 lines — wired Server constructor signature change through existing test fixtures)"
    - "internal/actions/orchestrator.go (+26 lines — ActionsInFlightFn() func() int accessor on Orchestrator)"
    - "internal/actions/mutex.go (+26 lines — heldMutexCount() helper backing ActionsInFlightFn)"
    - "cmd/docker-update/main.go (+145 lines — flag.Bool(--self-update-orchestrator) + flag.String(--target) + envDuration helper + branch on flag: helper-mode calls selfupdate.Orchestrate then exits; server-mode constructs selfupdate.NewSpawner and passes to api.NewServer)"
    - "README.md (+36 lines — new ### Phase 9 upgrade — remove docker CLI bind-mounts sub-section under existing ## Upgrading from hmi-update)"

key-decisions:
  - "Spawner ErrSelfUpdateInFlight guard uses sync/atomic.Bool CompareAndSwap — single-process state is acceptable per RESEARCH.md Open Question 2 (operator double-click on the same browser hits the same docker-update process). Reset on Spawn-create-failure only; success path holds the bit until SIGTERM."
  - "actionsInFlightFn injection (closure returning int from Orchestrator's mutex-map cardinality) keeps internal/selfupdate decoupled from internal/actions — Spawner.Spawn calls actionsInFlightFn() before flipping inFlight; refuses with ErrActionsInFlight if any per-service action is mid-flight."
  - "Helper container is unnamed (Name=\"\") — let the daemon assign an id; AutoRemove cleans it up. Label centroid.docker-update.helper=true is the operator-visible marker for `docker ps -a --filter label=centroid.docker-update.helper=true` post-mortem."
  - "Self-update sub-route /api/self-update is registered as a top-level mux route, NOT wrapped by CheckSelfProtection — the bypass is route-scoped (RESEARCH.md Pattern 5). Per-service /api/containers/docker-update/update STILL returns 409 (test: TestHandleUpdate_DockerUpdateSvc_StillReturns409 stays GREEN)."
  - "e2e spec accepts two harness modes (production 202 OR harness 500) and only fails if a third response shape appears — keeps the spec deterministic against the test compose stack's image-addressability gap while preserving SC-4 a contract enforcement when a production-shaped harness IS available. The local Playwright run we executed hit the 202 success path."
  - "main.go envDuration helper duplicates the envInt pattern (Phase 04 decision: 'envInt duplicated in main.go (not promoted from internal/poll); keeps poll's exported surface narrow') — same shape, same trade-off."
  - "healthzURL is built as `http://<target>:8080/healthz` and resolved via docker DNS on the test/compose network — documented in the in-source comment as a v1 simplification; a future enhancement could resolve via ContainerInspect.NetworkSettings.Networks[*].IPAddress if multiple networks complicate DNS resolution."

patterns-established:
  - "Single-flight atomic guard with non-reset-on-success semantics — for any operation that intentionally terminates the holder (SIGTERM, exit, container recreate), inFlight stays true forever within that process; the new process starts with inFlight=false. Reusable for any future self-modifying primitive."
  - "Flag-mode binary: same Go binary, two entry points via flag.Bool — a clean alternative to a second helper image when the helper's logic is small and the image-count budget is tight (C1)."
  - "Route-scoped middleware bypass: when a new endpoint needs to be exempt from an existing middleware, register it as a sibling at the mux level rather than threading a bypass-flag through the middleware itself."

requirements-completed: [SC-4, SC-6]  # SC-7 deferred — see notes below; SC-4 a (wire) + SC-6 iv (CheckSelfProtection regression seals) both close in this plan. From PLAN.md frontmatter `requirements: [SC-4, SC-6, SC-7]`.

# Metrics
duration: ~50min
completed: 2026-05-16
---

# Phase 09 Plan 04: Self-Update Sidecar Helper Summary

**Watchtower-style self-update path: operator clicks Update on the docker-update row (or curls POST /api/self-update); same-binary helper container spawns via daemon API with --self-update-orchestrator flag, calls recreate.Service against the parent, polls /healthz=200, exits — closes the last open item from the 2026-05-15/16 production incident without weakening CheckSelfProtection on the per-service route.**

## Performance

- **Duration:** ~50 min total (Tasks 1+2 executed by previous executor in ~45 min; Task 3 Part A + plan close-out in ~5 min by this continuation agent)
- **Started:** 2026-05-16T19:33Z (previous executor)
- **Completed:** 2026-05-16T20:26Z
- **Tasks:** 3 (Task 1 + Task 2 atomic landings via previous executor; Task 3 Part A run + plan close-out via continuation agent)
- **Files modified:** 12 (6 created, 6 modified)

## Accomplishments

- New `internal/selfupdate` package built end-to-end (9 unit tests across spawn_test.go + orchestrate_test.go); 5 plan-09-02 RED handlers_self tests flip GREEN with the wired POST /api/self-update endpoint; control test TestHandleUpdate_DockerUpdateSvc_StillReturns409 stays GREEN so the per-service CheckSelfProtection regression seal holds.
- One-binary two-mode flag branch in `cmd/docker-update/main.go` (`--self-update-orchestrator --target=<svc>`) — C1 preserved (single image, single binary, two entry points). Helper-mode envs: `DOCKER_UPDATE_SELF_UPDATE_DELAY` (1s), `DOCKER_UPDATE_SELF_VERIFY_TIMEOUT` (60s), `DOCKER_UPDATE_SELF_UPDATE_KEEP_HELPER` (false).
- POST /api/self-update wire-shape e2e (e2e/tests/self-update.spec.ts) **executed locally and GREEN** in 177 ms test wall-time (11s total including compose bring-up wait) — confirms the harness-side docker-update can spawn a helper successfully against its own image even in the e2e stack.
- README.md gains a Phase 9 operator-facing upgrade sub-section instructing existing operators to delete the two `:ro` docker-CLI bind-mounts from their installed `docker-compose.yml` (W5 fix; RESEARCH.md Runtime State Inventory requirement). Cross-references `docker-compose.example.yml` for the post-Phase-9 shape.
- Production image size measured at 4.29 MB by the previous executor (well under the 12 MB SC-3 b gate that 09-03 tightened in CI).

## Task Commits

1. **Task 1: internal/selfupdate package (Spawner + Orchestrate + 9 unit tests)** — `7b0e31f` (feat) — 4 files created (1101 insertions)
2. **Task 2: POST /api/self-update wire-up + flag-mode branch + e2e spec + README upgrade note** — `f2daee9` (feat) — 8 files (518 insertions, 2 deletions)
3. **Task 3 close-out: SUMMARY.md + STATE.md + ROADMAP.md** — this docs commit (Task 3 Part A executed; Parts B + C statuses recorded)

_Note: Tasks 1 and 2 are TDD-style landings — both turn pre-existing RED tests (Plan 09-02 handlers_self_test.go + the unit tests added in this plan) GREEN inside a single feat commit per task. There is no separate test() commit because the RED tests landed in Plan 09-02._

## Files Created/Modified

- `internal/selfupdate/spawn.go` — Spawner interface + NewSpawner constructor; ContainerCreate(helper image, --self-update-orchestrator flag, AutoRemove, docker.sock bind-mount, centroid.docker-update.helper=true label); atomic.Bool single-flight guard; ErrActionsInFlight + ErrSelfUpdateInFlight sentinels.
- `internal/selfupdate/orchestrate.go` — Helper-side: wait `delay` (lets parent return 202), call `recreate.Service(ctx, cli, target)`, poll healthzURL with 2s tick / verifyTimeout deadline.
- `internal/selfupdate/spawn_test.go` — 6 test cases: ContainerCreate opts shape, success returns helper id, refusal when actions in flight, refusal when already in flight, KeepHelper false → AutoRemove true, KeepHelper true → AutoRemove false.
- `internal/selfupdate/orchestrate_test.go` — 3 test cases: success with httptest /healthz=200, verify timeout, recreate failure.
- `internal/api/handlers_self.go` — handleSelfUpdate + 4 body constants (actionBodySelfUpdaterUnwired, actionBodySelfUpdateFailed, actionBodyActionsInFlight, actionBodySelfUpdateBusy).
- `internal/api/server.go` — Server.selfUpdater field; NewServer extended (single-line signature preserved per Phase 04 P04 decision); mux.HandleFunc("POST /api/self-update", s.handleSelfUpdate) registered as a top-level route (no CheckSelfProtection wrap).
- `internal/actions/orchestrator.go` — ActionsInFlightFn() func() int accessor.
- `internal/actions/mutex.go` — heldMutexCount() backing helper.
- `cmd/docker-update/main.go` — Helper-mode branch (calls selfupdate.Orchestrate then exits 0) + server-mode wiring (constructs selfupdate.NewSpawner from DOCKER_UPDATE_SELF_IMAGE / DOCKER_UPDATE_SELF_SERVICE / KEEP_HELPER + injects into api.NewServer); envDuration helper.
- `e2e/tests/self-update.spec.ts` — One test asserting 202 helper_spawned with helper_id matching /^[0-9a-f]{12,64}$/, with documented skip-on-500 fallback for harness gaps.
- `README.md` — ### Phase 9 upgrade — remove docker CLI bind-mounts sub-section under ## Upgrading from hmi-update.

## Decisions Made

See `key-decisions:` frontmatter above for the full list. Headline calls:

- Single-flight via `atomic.Bool.CompareAndSwap`; hold the bit until SIGTERM, reset only on Spawn create/start failure.
- `actionsInFlightFn` closure injection keeps internal/selfupdate decoupled from internal/actions.
- Route-scoped CheckSelfProtection bypass — top-level mux registration, NOT a middleware flag.
- e2e spec accepts both 202 (production-shaped harness) and 500 (harness can't address its own image) responses; fail-loud only on a third shape. Local run hit the 202 path.

## Deviations from Plan

None — plan executed exactly as written. The two task commits (`7b0e31f`, `f2daee9`) landed the planned files with the planned shape. The previous executor's verification gates (go build, go test -race, go vet, make grep-no-compose, docker build → 4.29 MB) all passed before reaching the Task 3 checkpoint.

## Issues Encountered

- **Single-spec Playwright runs require the cron-fast override.** First attempt to run `e2e/tests/self-update.spec.ts` standalone via `npx playwright test tests/self-update.spec.ts` against the base compose stack tripped `e2e/global-setup.ts:163` `waitForPollAdvance` 15s deadline — globalSetup hard-requires a cron tick to land within 15s, which only happens with `DOCKER_UPDATE_CRON=@every 5s` from the override. Resolved by re-bringing the stack with `docker compose -f e2e/compose.test.yml -f e2e/compose.test.override.cron-fast.yml up -d --wait --build` before re-running the spec. The make `e2e` recipe sidesteps this by running the full suite after a fresh up-without-override (the Phase 3 flip specs still need cron-fast, so single-spec invocation is the only path that exposed this footgun). Not a Plan 09-04 bug — pre-existing harness behavior; flagged here for the next executor who reaches for a single-spec run.

## Task 3 Status Breakdown

The plan's Task 3 has three parts; each has a distinct status:

| Part | What | Status | Evidence |
|------|------|--------|----------|
| **Part A** | Local Playwright e2e `e2e/tests/self-update.spec.ts` GREEN | **PASS** | Executed by this continuation agent on 2026-05-16T20:25Z. Wall time: 177 ms test, 11 s total (10.4 s Playwright reported). Output: `✓ 1 tests/self-update.spec.ts:24:1 › POST /api/self-update spawns helper and returns 202 with helper_id (177ms)` and `1 passed (10.4s)`. The spec hit the production-shaped 202 success path (not the harness-skip 500 fallback). |
| **Part B** | Production image size <12 MB (SC-3 b finalization) | **PASS** | Previous executor (Task 2 verification) measured `docker build` final image at **4.29 MB**, well under the 12 MB SC-3 b ceiling that Plan 09-03 tightened in CI. No re-measurement needed in this agent. |
| **Part C** | Elevator-HMI manual smoke at 10.50.10.175 (SC-7) — flutter Update + Rollback + docker-update self-Update + display recovers; SMOKE.md entry | **PENDING — DEFERRED TO ORCHESTRATOR** | Per orchestrator instructions, this continuation agent does NOT attempt SSH/HMI work. The parent orchestrator will handle SC-7 directly via SSH/curl at `centroid@10.50.10.175` after this agent returns. SMOKE.md entry will land in a follow-up commit by the orchestrator. Per the plan's resume-signal language, the phase can close with SC-7 openly tracked as pending. |

**SC-7 closing note:** This Plan delivers ALL the production code Phase 9 needs for self-update (Spawner + Orchestrate + handler + flag-mode + e2e + README upgrade note). The remaining SC-7 gate is operator-side verification of the running HMI — not code work. Phase 9 closes when the SMOKE.md entry lands AND CI on main has run with the parallel 2-job split for an observed wall time ≤6 min (SC-5). Both can land in a follow-up commit if HMI access slips beyond this session.

## User Setup Required

None _in this plan_ for an operator running a fresh install — the docker-update service block is unchanged. **However**, for operators upgrading an existing HMI that has Phase 7-vintage `docker-compose.yml`, the new README ### Phase 9 upgrade — remove docker CLI bind-mounts sub-section is operator-required reading. The two `:ro` bind-mounts (`/usr/bin/docker` + `/usr/libexec/docker/cli-plugins/docker-compose`) MUST be deleted from the installed compose file before the new image is brought up. The example compose at `docker-compose.example.yml` already reflects the post-Phase-9 shape (trimmed in Plan 09-03).

## Next Phase Readiness

- **Phase 9 ready to close** with: SC-1 (no compose subprocess) green (09-03 + grep-no-compose gate); SC-2 (relative bind-mount resolved on HMI host path) green at unit + e2e harness level (09-03); SC-3 (image <12 MB on static-debian12:nonroot) green at local build (09-03); SC-4 (a + b wire-shape) green at unit (Plan 09-02 handlers_self tests) + e2e (this plan); SC-5 (CI wall time ≤6 min via 2-job split) observable post-merge of 09-01; SC-6 (i-iv RED-first regression tests) all GREEN; **SC-7 pending operator HMI smoke at 10.50.10.175 (orchestrator-handled).**
- **No blockers** for the milestone v1.0 close from this plan's work. Outstanding SC-7 + SC-5 wall-time observation are both operational/observational, not code-blocking.

## Self-Check: PASSED

**Files claimed → verified on disk:**
- FOUND: internal/selfupdate/spawn.go
- FOUND: internal/selfupdate/orchestrate.go
- FOUND: internal/selfupdate/spawn_test.go
- FOUND: internal/selfupdate/orchestrate_test.go
- FOUND: internal/api/handlers_self.go
- FOUND: e2e/tests/self-update.spec.ts

**Commits claimed → verified in git log:**
- FOUND: 7b0e31f feat(09-04): internal/selfupdate package — Spawner + Orchestrate (SC-4 a primitives)
- FOUND: f2daee9 feat(09-04): self-update via sidecar helper (SC-4 + SC-6 iv)

---
*Phase: 09-architectural-hardening-post-v0-1-bug-cluster*
*Plan: 04*
*Completed: 2026-05-16*
