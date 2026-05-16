---
phase: 09-architectural-hardening-post-v0-1-bug-cluster
plan: 02
subsystem: testing
tags: [tdd, regression-tests, playwright, moby-client, recreate, self-update, bind-mount]

# Dependency graph
requires:
  - phase: 09-architectural-hardening-post-v0-1-bug-cluster
    provides: "Plan 09-01 — CI 2-job split + grep-no-compose static gate + dist/.gitkeep retracked"
provides:
  - "14 RED-first table-tests for the InspectResponse→ContainerCreate translation table (internal/recreate/translate_test.go)"
  - "RED-first regression guard locking compose.ErrComposeFileMoved 412 through the post-socket-only Update path (SC-6 i)"
  - "5 RED-first tests for POST /api/self-update (202 helper-spawned, 503 unwired, CheckSelfProtection bypass, per-service-still-409 control, 409 actions-in-flight)"
  - "RED-first e2e regression guard for `./relative-path` bind-mount resolution (SC-2 b + SC-6 iii)"
  - "Fixture services + repo-tracked bind source dirs mirroring flutter's wayland-socket layout"
affects:
  - "Plan 09-03 (inspect-then-recreate primitive — turns translate_test.go + relative-bind-mount.spec.ts + compose-file-moved tests green)"
  - "Plan 09-04 (self-update endpoint — turns handlers_self_test.go green by adding SelfUpdater interface + Server fields + handleSelfUpdate route)"

# Tech tracking
tech-stack:
  added:
    - "github.com/moby/moby/api/types/container — direct test imports (sole legitimate use outside internal/docker; tests fixture InspectResponse shapes)"
    - "github.com/moby/moby/api/types/network — same rationale (EndpointSettings + NetworkingConfig fixtures)"
    - "github.com/moby/moby/api/types/mount — same rationale (mount.Mount{Type,Source,Target} fixture)"
  patterns:
    - "Cross-plan RED→GREEN handoff: tests in 09-02 commit BEFORE the production code in 09-03/09-04 that turns them green (C4 TDD-first scaled across plans)"
    - "Direct struct-field test injection (handlers_self_test.go): RED-state failure messages name the exact future surface (`Server has no field or method selfUpdater`) so the next plan knows precisely what to add"
    - "Repo-tracked .gitkeep bind source dirs: prevent docker from silently creating an empty dir at compose-up time, which would mask the bug class the test guards"

key-files:
  created:
    - "internal/recreate/translate_test.go (632 lines, 14 test funcs)"
    - "internal/api/handlers_self_test.go (264 lines, 5 test funcs)"
    - "e2e/tests/relative-bind-mount.spec.ts (195 lines, 1 test)"
    - "e2e/test-relative-mount/A/.gitkeep"
    - "e2e/test-relative-mount/B/.gitkeep"
  modified:
    - "internal/actions/orchestrator_test.go (+50 lines — TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly)"
    - "e2e/compose.test.yml (+69 lines — relative-bind-A + relative-bind-B services + depends_on entries)"

key-decisions:
  - "Spec lives under e2e/tests/ (the Playwright testDir) not e2e/ — plan path-spec was simplified; Playwright requires the tests/ subdir for discovery (verified via `playwright test --list`)"
  - "actionsInFlightFn introduced as a Server-side seam in the RED test (not a constructor-arg yet) — Plan 09-04 owns the wiring; the test names the exact field name so 09-04 knows precisely what to add"
  - "TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly is GREEN-from-day-zero by design (the value is regression lock-in across Plan 09-03's deletion of compose.Runner); plan calls this out explicitly in the test's docstring"
  - "Bind directories e2e/test-relative-mount/{A,B}/ tracked via .gitkeep so compose has a real source path; absent source would let docker create an empty dir transparently and mask the relative-path bug"
  - "Test file imports github.com/moby/moby/api/types/{container,network,mount} directly — this is the ONE legitimate exception to the no-moby-imports-outside-internal/docker rule because translate_test.go must fixture realistic InspectResponse shapes that the docker facade does not re-export"

patterns-established:
  - "Per-test SC traceability: every test name + docstring carries the SC# it gates (SC-2 a, SC-6 i/ii/iii/iv) so 09-VALIDATION.md mapping is self-documenting"
  - "Explicit RED-state assertion in test commit: each commit's body lists the precise failure mode (`undefined: Translate`, `undefined: SelfUpdater`, `srv.selfUpdater undefined`) so a reviewer can confirm the RED contract without re-running the test"

requirements-completed: []  # Phase 9 has no formal REQ-IDs (architectural hardening, incident-driven); SCs are the goal-backward anchors

# Metrics
duration: 25min
completed: 2026-05-16
---

# Phase 09 Plan 02: Wave-2 RED Regression Tests Summary

**Five RED-first regression-test commits landed before any production code: 14 translation-table cases (InspectResponse→ContainerCreate), the post-socket-only compose-file-moved 412 guard, 5 self-update endpoint tests, the relative-bind-mount fixture + e2e spec — all per C4 (CLAUDE.md) and the HANDOFF.md TDD-first callout.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-05-16T19:00Z (approx — first commit timestamp)
- **Completed:** 2026-05-16T19:25:38Z
- **Tasks:** 2 (Task 1 single-commit; Task 2 four-commit)
- **Files modified:** 7 (5 created, 2 modified)

## Accomplishments

- 14 table-driven test cases lock in every row of RESEARCH.md Pattern 2's translation table (HostConfig.Binds, Mounts, NetworkMode 4 cases, RestartPolicy empty→"no" normalization, Healthcheck nil-vs-empty tri-state, first-network-in-NetworkingConfig + extras returned separately, short-ID alias filtering, pinned-vs-auto-assigned IP, Config.Image is operator reference not resolved ID, HostConfig.Init pointer tri-state, compose+hmi-update labels pass-through, no COMPOSE_PROJECT_NAME env dependency).
- Post-socket-only compose-file-moved 412 regression guard (`TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly`) pins the invariant across Plan 09-03's deletion of compose.Runner.
- 5 RED tests for POST /api/self-update covering: 202 happy path, 503 nil-guard, CheckSelfProtection bypass, per-service-still-409 control case, 409 actions-in-flight short-circuit — plus the `fakeSpawner` test helper implementing the not-yet-existing `SelfUpdater` interface.
- e2e fixture (2 watched services with `./test-relative-mount/<NAME>` volumes) + spec asserting `HostConfig.Binds[0]` survives a recreate — RED on pre-Phase-9 (compose CLI mis-resolves) and will GREEN once Plan 09-03 ships the inspect-then-recreate primitive.

## Task Commits

Each test artifact was committed atomically per the plan's commit-shape list:

1. **Task 1 — RED translation-table tests** — `1822b02` (test)
2. **Task 2 (A) — RED TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly** — `c83cd40` (test)
3. **Task 2 (B) — RED handlers_self_test.go** — `5faaec2` (test)
4. **Task 2 (C) — e2e compose.test.yml relative-bind-mount fixture** — `2fd0309` (test)
5. **Task 2 (D) — RED e2e/relative-bind-mount.spec.ts** — `2575ae9` (test)

**Plan metadata commit:** appended in the same wave that updates STATE.md + ROADMAP.md (see Self-Check below).

## Files Created/Modified

| File | Status | Purpose |
|------|--------|---------|
| `internal/recreate/translate_test.go` | CREATED (632 LOC) | 14 RED-first test funcs covering every row of the translation table; package `recreate` does not exist yet — that is the RED state per C4 |
| `internal/api/handlers_self_test.go` | CREATED (264 LOC) | 5 RED-first tests for the self-update endpoint contract + fakeSpawner helper. References future surface (SelfUpdater, Server.selfUpdater, actionsInFlightFn, handleSelfUpdate) — Plan 09-04 lands all four |
| `e2e/tests/relative-bind-mount.spec.ts` | CREATED (195 LOC) | E2e regression guard: inspect Binds[0] before + after Update; assert host path unchanged |
| `e2e/test-relative-mount/A/.gitkeep` | CREATED | Repo-tracked empty dir = bind source path A; prevents docker from creating an empty dir transparently |
| `e2e/test-relative-mount/B/.gitkeep` | CREATED | Same for service B |
| `internal/actions/orchestrator_test.go` | MODIFIED (+50 LOC) | Append TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly |
| `e2e/compose.test.yml` | MODIFIED (+69 LOC) | Append relative-bind-A + relative-bind-B services + depends_on entries |

## Validation: RED + GREEN proof

**Per-task verification (09-VALIDATION.md mapping):**

| SC | Test | Status | Command (proof) |
|----|------|--------|-----------------|
| SC-2 (a) | `TestTranslate_HostConfig_Binds_AbsoluteAfterDaemonResolution` | RED — `undefined: Translate` | `go test ./internal/recreate/...` |
| SC-2 (b) | `e2e/tests/relative-bind-mount.spec.ts::relative bind-mount resolves to operator host path after update` | RED on pre-9 (compose CLI mis-resolves); not yet runnable in CI but Playwright list confirms discovery | `cd e2e && npx playwright test --list relative-bind-mount.spec.ts` |
| SC-6 (i) | `TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly` | GREEN-from-day-zero (regression lock-in) | `go test ./internal/actions/ -run TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly -race` ✓ |
| SC-6 (ii) | `TestRecreate_NoComposeProjectNameEnvDependency` | RED — `undefined: Translate` | `go test ./internal/recreate/...` |
| SC-6 (iii) | translate_test.go HostConfig.Binds case + e2e spec | RED unit (`undefined: Translate`); RED e2e on pre-9 | (combined) |
| SC-6 (iv) | `TestHandleSelfUpdate_BypassesCheckSelfProtection` + `TestHandleUpdate_DockerUpdateSvc_StillReturns409` | RED — `undefined: SelfUpdater` / `srv.selfUpdater undefined` | `go test ./internal/api/...` |

**Overall gates:**

- `go test ./internal/recreate/...` → FAIL with `undefined: Translate` ✓ (RED confirmed)
- `go test ./internal/api/...` → FAIL with `undefined: SelfUpdater`, `srv.selfUpdater undefined`, `srv.actionsInFlightFn undefined` ✓ (RED confirmed, naming exact missing surface for Plan 09-04)
- `go test ./internal/actions/...` → ok (including new regression guard) ✓
- `go test ./internal/{compose,docker,state,poll,registry}/...` → ok ✓ (no regression in green tests)
- `make grep-no-compose` → PASS ✓ (no new production references to compose CLI; tests are excluded from the gate as designed in Plan 09-01)
- `git diff --name-only HEAD~5 HEAD -- 'internal/**/*.go' | grep -v '_test\.go$'` → empty ✓ (no production code touched)

## Decisions Made

- **Spec path:** `e2e/tests/relative-bind-mount.spec.ts` (under Playwright's `testDir: './tests'`), not the simplified `e2e/relative-bind-mount.spec.ts` shown in the plan's acceptance criteria. Verified via `playwright test --list`. This is a path-spec refinement, not a deviation — the spec file MUST live where Playwright discovers it.
- **`actionsInFlightFn` field:** introduced as a Server-side seam in the RED test (not yet a NewServer constructor arg). Plan 09-04 owns the wiring; the test names the exact field name so the next plan executor knows precisely what to add. The fakeSpawner helper similarly references the not-yet-existing `SelfUpdater` interface so the compile error is precise.
- **TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly is GREEN-from-day-zero by design** — the value is regression lock-in across Plan 09-03's deletion of compose.Runner. The test's docstring documents this expectation so a future reader doesn't think "why is a RED-named test GREEN?". This mirrors the Phase-6 weston-warning.spec.ts pattern (contract-RED, GREEN-from-day-zero, no GREEN feat follow-on).
- **Repo-tracked bind dirs** (`.gitkeep`): without them, compose would silently create an empty dir at compose-up time and the relative-path bug would never surface at the daemon layer — masking the bug the test guards.
- **Direct moby/moby/api/types import in test code:** translate_test.go imports `container`, `network`, and `mount` packages directly. This is the ONE legitimate exception to the "no moby imports outside internal/docker" CI gate (Plan 02-01) because the tests must fixture realistic `InspectResponse` shapes, and the `internal/docker` facade only re-exports result wrapper types, not the constituent shapes. Documented in the test file's package-level godoc.

## Deviations from Plan

None — plan executed exactly as written. All five commits landed in the exact shapes the plan dictated, the RED-state failure messages match the plan's predictions, and all acceptance criteria gates pass.

The only minor adjustment was the spec file's directory placement (`e2e/tests/` not `e2e/`); see Decisions Made above.

## Issues Encountered

None.

## TDD Gate Compliance

This plan is the RED half of a cross-plan RED→GREEN cycle. Per the plan's design:

- **RED commits** land here (Plan 09-02) with commit-shape `test(09-02): ...`.
- **GREEN commits** land in Plan 09-03 (recreate primitive — turns translate_test.go + the 412 guard + the e2e spec green) and Plan 09-04 (self-update endpoint — turns handlers_self_test.go green).

C4 compliance:

| Gate | This plan | Future plan |
|------|-----------|-------------|
| RED commit before GREEN | ✓ 5 `test(09-02): ...` commits | Plan 09-03 + 09-04 land `feat(09-03): ...` / `feat(09-04): ...` |
| Tests are RED on production | ✓ Verified: `undefined: Translate`, `undefined: SelfUpdater`, etc. | Plan 09-03/04 verify GREEN via the same commands |
| No squash across the RED→GREEN boundary | Each test commit stands alone with its own message | (preserved by Plan 09-03/04 commit shapes) |

## Next Plan Readiness

**Plan 09-03 executor: read this section before starting.**

The RED commits in this plan unblock Plan 09-03's GREEN commits. Plan 09-03 MUST run the test suite after each implementation step and confirm tests flip from RED to GREEN one-by-one (per C4 RED→GREEN→REFACTOR). Suggested flip order:

1. Create `internal/recreate/translate.go` with the `Translate` function signature from RESEARCH.md Pattern 2 — this alone makes `internal/recreate/translate_test.go` COMPILE. Each individual test case then surfaces a specific behavior to implement.
2. Implement the 14 translation-table cases ONE AT A TIME, running `go test ./internal/recreate/ -run TestTranslate_<one-case>` after each. Watch the test transition from RED to GREEN.
3. Once translate.go is fully green, implement `internal/recreate/recreate.go::Service` (the Stop→Remove→Create+SameLabels→NetworkConnect→Start primitive) per RESEARCH.md Pattern 1 + Pattern 3.
4. Wire the new primitive into `internal/actions/orchestrator.go::inspectAndVerify` or equivalent — replacing `o.runner.UpdateService`.
5. Run `make e2e-cron-fast` and watch `e2e/tests/relative-bind-mount.spec.ts` flip RED→GREEN. This is the integration-level GREEN gate.

Plan 09-04 executor (self-update endpoint):

1. Add `SelfUpdater` interface to `internal/api` (per RESEARCH.md Example 2 — shape: `Spawn(ctx) (helperID, err)`).
2. Add `Server.selfUpdater SelfUpdater` field.
3. Add `Server.actionsInFlightFn func() int` field (or equivalent — the test pins the name `actionsInFlightFn`; if Plan 09-04 chooses a different name, this test must be updated in lockstep).
4. Add `handleSelfUpdate(w, r)` method per RESEARCH.md Example 2.
5. Register the route in `Server.routes()`: `s.mux.HandleFunc("POST /api/self-update", s.handleSelfUpdate)`.
6. Run `go test ./internal/api/ -run "TestHandleSelfUpdate_"` — each of the 4 self-update tests should transition RED→GREEN.

---

## Self-Check: PASSED

Verified:
- `internal/recreate/translate_test.go` exists with 14 `^func Test` funcs ✓
- `internal/api/handlers_self_test.go` exists with 5 `^func Test` funcs ✓
- `e2e/tests/relative-bind-mount.spec.ts` exists with 1 `test(...)` body ✓
- `e2e/compose.test.yml` contains 5 `./test-relative-mount` references (2 volume entries + 3 in explanatory comments) — ≥ 2 required ✓
- `e2e/test-relative-mount/{A,B}/.gitkeep` both exist ✓
- All 5 commits visible in `git log --oneline -7` with `test(09-02):` prefix ✓
  - `1822b02` test(09-02): add RED translation-table tests (13 cases for SC-6 ii + SC-2 a)
  - `c83cd40` test(09-02): add RED test TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly (SC-6 i)
  - `5faaec2` test(09-02): add RED handlers_self_test.go (SC-4 + SC-6 iv)
  - `2fd0309` test(09-02): add e2e/compose.test.yml relative-bind-mount fixture (SC-2 b)
  - `2575ae9` test(09-02): add RED e2e/relative-bind-mount.spec.ts (SC-2 b + SC-6 iii)
- `git diff --name-only HEAD~5 HEAD -- 'internal/**/*.go' | grep -v '_test\.go$'` returned empty ✓ (no production code touched)

---

*Phase: 09-architectural-hardening-post-v0-1-bug-cluster*
*Completed: 2026-05-16*
