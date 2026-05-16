---
phase: 09-architectural-hardening-post-v0-1-bug-cluster
plan: 03
subsystem: recreate
tags: [moby-client, recreate, socket-only, dockerfile, distroless, ci]

# Dependency graph
requires:
  - phase: 09-architectural-hardening-post-v0-1-bug-cluster
    provides: "09-02 RED tests: 14 translate_test.go cases + post-socket-only compose-file-moved 412 guard + relative-bind-mount e2e fixture/spec"
  - phase: 09-architectural-hardening-post-v0-1-bug-cluster
    provides: "09-01 CI 2-job split + grep-no-compose SC-1 enforcement gate (now actually enforces something real)"
provides:
  - "internal/recreate package — Translate (pure-function 13-row InspectResponse→Create translation) + Service (Stop→Remove→Create→NetworkConnect→Start sequence with failure-mode catalog)"
  - "docker.Client extended +5 methods: ContainerCreate, ContainerRemove, ContainerStart, ContainerStop, NetworkConnect (8→13)"
  - "actions.orchestrator wired to recreate.Service for Update / Rollback / ForcePull(recreate=true); compose.Runner DELETED"
  - "Dockerfile reverted to distroless/static-debian12:nonroot (~20 MB image shrink; final image 4.3 MB)"
  - "docker-compose.example.yml trimmed of /usr/bin/docker + /usr/libexec/docker/cli-plugins bind-mounts"
  - "CI image-size gate tightened from 30 MB → 12 MB at both call sites; Idle-RAM gate untouched"
  - "New slog event class: action.recreate_failed (distinct from action.compose_failed which now only fires from compose.Reader drift)"
affects:
  - "Plan 09-04 (self-update endpoint — consumes recreate.Service as the helper's recreate primitive)"
  - "All future ROADMAP plans — the surviving compose package surface is Reader + sentinels; any new compose-side feature must reason about the post-runner contract"

# Tech tracking
tech-stack:
  added:
    - "internal/recreate package — first new internal/ package since Phase 4; depends only on internal/docker + moby/moby/api/types"
  patterns:
    - "Pure-function translate.Translate: zero env reads, zero daemon I/O — exhaustively unit-testable without fixtures"
    - "Failure-mode catalog with explicit error markers: recreate.Service's Create-fail path embeds the literal 'old GONE' marker so operators (and unit tests) see the unrecoverable boundary in the slog/wrap chain"
    - "Best-effort cleanup of NEW container on NetworkConnect/Start failure: ContainerRemove(newID, Force=true) before returning the wrapped error"
    - "stopHook test seam on fakeDockerClient: replaces the deleted fakeRunner.hook for ACT-08 contention testing (the inside-the-lock invocation that proves the per-service mutex is held mid-action)"

key-files:
  created:
    - "internal/recreate/translate.go (252 lines, 1 exported func + 4 unexported helpers)"
    - "internal/recreate/recreate.go (146 lines, 1 exported func)"
    - "internal/recreate/recreate_test.go (330 lines, 7 TestService_ funcs + recording fakeClient)"
  modified:
    - "internal/docker/client.go (+5 methods on Client interface; 6 new type aliases; doc rewrites)"
    - "internal/docker/moby.go (+5 thin SDK adapters)"
    - "internal/docker/moby_test.go (TestClient_InterfaceMethodCount: 8 → 13)"
    - "internal/docker/_sdk_shape.txt (+15 identifiers + 5 method captures in IDENTIFIER INDEX)"
    - "internal/docker/discovery_test.go (fakeClient +5 stub methods)"
    - "internal/actions/orchestrator.go (Update/Rollback step-9 → recreate.Service; runner field + NewOrchestrator param REMOVED; action.recreate_failed slog event added)"
    - "internal/actions/orchestrator_test.go (fakeDockerClient +5 methods + stopHook; fakeRunner type retained as documentation anchor; TestUpdate_ComposeFailed switched to dc.createErr path)"
    - "internal/api/getstate_noio_test.go (panickingDockerClient +5 panic-on-call stubs — OBS-03 invariant)"
    - "internal/api/handlers_healthz_test.go (fakeClient +5 stub methods)"
    - "internal/compose/errors.go (ErrComposeFailed deprecated; package godoc rewritten)"
    - "cmd/docker-update/main.go (compose.NewRunner construction GONE; actions.NewOrchestrator now 8-arg)"
    - "Dockerfile (FROM base-debian12:nonroot → static-debian12:nonroot)"
    - "docker-compose.example.yml (removed two CLI-delivery bind-mounts + added UPGRADE note)"
    - ".github/workflows/ci.yml (image-size gate 30 MB → 12 MB at two call sites; Idle-RAM gate UNTOUCHED at 30 MiB)"
  deleted:
    - "internal/compose/runner.go (Runner interface + execRunner — gone per RESEARCH.md Open Q4; replaced by recreate.Service)"
    - "internal/compose/runner_test.go (entirely about the deleted Runner — 9 test funcs gone)"

key-decisions:
  - "ContainerStopOptions lives on client.* not container.* in moby v0.4.1 — the plan and RESEARCH.md Example 1 both suggested container.StopOptions but the SDK actually exposes client.ContainerStopOptions (the entire option/result family was unified onto client in v0.4.1's reorganization). _sdk_shape.txt captures this explicitly."
  - "NetworkConnectOptions field is EndpointConfig (not EndpointSettings) in moby v0.4.1 — corrected the plan's example code in recreate.go's actual call site."
  - "compose.ErrComposeFailed sentinel retained (not deleted) — marked Deprecated in errors.go. Operators with errors.Is checks against the public sentinel don't break, and the cost of keeping a 1-line var is negligible vs. a backward-compat surprise."
  - "fakeRunner type retained in orchestrator_test.go as documentation anchor even though no compose.Runner interface exists to satisfy. The struct is now a no-op stub kept ONLY because TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly asserts rn.callCount() == 0 as a regression seal (trivially true post-Phase-9 but documents the invariant)."
  - "stopHook seam replaces fakeRunner.hook for ACT-08 contention test — ContainerStop is the first daemon call in recreate.Service's sequence, the moral equivalent of the old runner.UpdateService entry point inside the per-service locked section."
  - "TestUpdate_ComposeFailed_State_ActionError_Set dropped the errors.Is(err, compose.ErrComposeFailed) assertion (it survived as a wrap-chain artifact pre-Phase-9; post-Phase-9 the source-of-error is recreate.Service, never compose). Replaced with errors.Is(err, actions.ErrComposeFailed) — the 500-handler dispatch sentinel — plus a strings.Contains(err.Error(), 'old GONE') assertion for the Pattern 3 unrecoverable-boundary marker."

patterns-established:
  - "Cross-plan RED→GREEN: 14 RED tests from Plan 09-02 → GREEN in Plan 09-03 (translate_test.go). The RED tests named the exact missing surface (`undefined: Translate`); the GREEN landing turns each one over one at a time."
  - "Documentation-anchor empty type pattern (fakeRunner) — retain the type even after its interface is gone, as a regression seal for the 'this primitive is fully gone' invariant"
  - "Per-package SDK-shape capture file (_sdk_shape.txt) — when adding new SDK methods to the facade, append captures + IDENTIFIER INDEX entries in the same commit; the CI drift gate consumes the index"

requirements-completed: []  # Phase 9 has no formal REQ-IDs (architectural hardening, incident-driven); SCs are the goal-backward anchors. SC-1, SC-2 (a), SC-3 (a, c), SC-6 (i, ii, iii) all green at unit-level; SC-2 (b) and SC-3 (b) green at the local docker-build smoke level (CI/e2e pending Task 3 manual run).

# Metrics
duration: 24min
completed: 2026-05-16
---

# Phase 09 Plan 03: Wave-2 GREEN Socket-Only Recreate Summary

**Replaces the deleted `compose.Runner` subprocess with an in-process socket-only recreate primitive (`recreate.Service`) using the existing `moby/moby/client`; reverts the Docker base from `base-debian12:nonroot` (~22 MB) to `static-debian12:nonroot` (~4.3 MB final), trims the docker-CLI bind-mounts from the operator compose example, and tightens the CI image-size gate from 30 MB → 12 MB.**

## Performance

- **Duration:** 24 min
- **Started:** 2026-05-16T19:33:45Z
- **Completed:** 2026-05-16T19:58:01Z
- **Tasks:** 3 (Task 1 + Task 2 atomic landings; Task 3 checkpoint auto-approved per workflow.auto_advance=true)
- **Files modified:** 14 (3 created, 9 modified, 2 deleted)

## Accomplishments

- Plan 09-02's 14 RED translate_test.go cases all GREEN; the 412 regression guard (TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly) stays GREEN through the rewire; `make grep-no-compose` continues to PASS (and now actually enforces something — internal/recreate/ existed only as a test file before this plan).
- `internal/recreate/` package built end-to-end: 14 translate cases + 7 service failure-mode cases (happy, Stop fails, Remove fails, Create fails with "old GONE" marker, NetworkConnect fails with cleanup, Start fails with cleanup, no-container).
- `docker.Client` interface grown from 8 → 13 methods (5 recreate primitives + 6 new type aliases); SDK-shape capture file updated in lockstep with the IDENTIFIER INDEX block; all 4 in-repo fakeClient stub types updated to satisfy the new interface.
- compose.Runner DELETED entirely (runner.go + runner_test.go gone); compose.Reader preserved per RESEARCH.md Open Q4; orchestrator's compose-file-moved 412 path lives on through the rewire.
- Dockerfile reverted to `gcr.io/distroless/static-debian12:nonroot` — local image build measures 4.3 MB (well under the 12 MB SC-3 b budget; the CI gate at both call sites now enforces 12 MB).
- New observability surface: `action.recreate_failed` slog event class distinguishes the new socket-only failure mode from `action.compose_failed` (which now only fires from compose.Reader drift). Operators can grep the two classes apart for forensics.

## Task Commits

Each task was committed atomically:

1. **Task 1: internal/recreate package + docker.Client +5 methods** — `438811e` (feat)
2. **Task 2: orchestrator rewire + Runner deletion + Dockerfile revert + compose example trim + ci.yml gate tighten + main.go signature update** — `ce115e3` (feat — 6 sub-edits in one commit per the plan's coupling requirement)
3. **Task 3: human-verify checkpoint** — auto-approved per `workflow.auto_advance: true`; no code commit produced (verification gate only).

**Plan metadata:** to-be-appended (this commit, after SUMMARY+STATE+ROADMAP land).

## Files Created/Modified

| File | Status | Purpose |
|------|--------|---------|
| `internal/recreate/translate.go` | CREATED (252 LOC) | Pure-function InspectResponse → (Config, HostConfig, NetworkingConfig, extras) per RESEARCH.md Pattern 2 13-row translation table; 6 gotchas handled (RestartPolicy empty→"no", Healthcheck nil-vs-empty tri-state, first-network-in-NetworkingConfig, short-ID alias filter, daemon-auto-IP guard, Init pointer-tri-state) |
| `internal/recreate/recreate.go` | CREATED (146 LOC) | Service() composes Stop → Remove → Create → NetworkConnect-extras → Start per Pattern 1 + Pattern 3 failure-mode catalog; "old GONE" marker on Create-fail unrecoverable boundary |
| `internal/recreate/recreate_test.go` | CREATED (330 LOC) | 7 TestService_ funcs covering happy-path + 5 failure modes + 1 absent-service guard; recording fakeClient with stopHook seam |
| `internal/docker/client.go` | MODIFIED (+5 methods, +6 type aliases) | Client interface 8→13; package + interface godoc updated |
| `internal/docker/moby.go` | MODIFIED (+5 adapters) | Thin SDK wrappers; consistent error-prefix pattern matches existing methods |
| `internal/docker/moby_test.go` | MODIFIED | TestClient_InterfaceMethodCount: pinned value 8 → 13 |
| `internal/docker/_sdk_shape.txt` | MODIFIED (+IDENTIFIER INDEX entries) | 5 new methods + 15 option/result identifiers; CI drift gate input |
| `internal/docker/discovery_test.go` | MODIFIED (+5 stub methods) | fakeClient satisfies the grown interface |
| `internal/actions/orchestrator.go` | MODIFIED | runner field GONE; recreate.Service replaces runner.UpdateService in Update + Rollback; ForcePull(recreate=true) delegates to Update so it inherits the new path; action.recreate_failed slog event added; NewOrchestrator signature shrunk to 8 args |
| `internal/actions/orchestrator_test.go` | MODIFIED | fakeDockerClient +5 methods + stopHook + 4 failure-injection knobs + 4 call-tracking slices; fakeRunner type retained as documentation anchor; TestUpdate_ComposeFailed switched to dc.createErr; TestForcePull_WithRecreate_FullUpdateFlow + TestUpdate_HappyPath assertions switched from rn.callCount() to dc.createCalls; TestOrchestrator_LockHeldThroughVerify hook migrated to dc.stopHook |
| `internal/api/getstate_noio_test.go` | MODIFIED (+5 panic stubs) | panickingDockerClient still panics on EVERY method including the 5 new ones (OBS-03 invariant: GET /api/state is read-only) |
| `internal/api/handlers_healthz_test.go` | MODIFIED (+5 stub methods) | fakeClient satisfies the grown interface |
| `internal/compose/errors.go` | MODIFIED | ErrComposeFailed marked Deprecated; package godoc rewritten to reflect Reader-only post-Phase-9 surface |
| `internal/compose/runner.go` | DELETED | Runner interface + execRunner subprocess body — replaced by recreate.Service |
| `internal/compose/runner_test.go` | DELETED | 9 test funcs all about the deleted Runner |
| `cmd/docker-update/main.go` | MODIFIED | compose.NewRunner construction step REMOVED; actions.NewOrchestrator call updated to 8-arg signature; boot-order godoc updated |
| `Dockerfile` | MODIFIED | FROM gcr.io/distroless/base-debian12:nonroot → gcr.io/distroless/static-debian12:nonroot; comment rewritten with Phase 9 (a) rationale + Pitfall 5 CA-cert note |
| `docker-compose.example.yml` | MODIFIED | Two CLI-delivery bind-mount lines removed; UPGRADE note appended for operators with pre-Phase-9 installs |
| `.github/workflows/ci.yml` | MODIFIED | Image-size gate tightened 30 MB → 12 MB at lines ~138-147 and ~209-213; step name updated "DEPLOY-02 — <30 MB" → "SC-3 b — <12 MB"; banner + error message + success log all coherent at 12 MB; Idle-RAM gate at lines ~220-269 UNTOUCHED (DEPLOY-03 RAM cap stays at 30 MiB — different gate) |

## Validation: Plan 09-02 RED → GREEN transitions

| Test | Pre-Plan-09-03 state | Post-Plan-09-03 state |
|------|---------------------|----------------------|
| `TestTranslate_HostConfig_Binds_AbsoluteAfterDaemonResolution` (SC-2 a) | RED — `undefined: Translate` | GREEN |
| `TestTranslate_HostConfig_Mounts_PassThrough` | RED — `undefined: Translate` | GREEN |
| `TestTranslate_HostConfig_NetworkMode_PassThrough` (4 sub-cases) | RED — `undefined: Translate` | GREEN |
| `TestTranslate_HostConfig_RestartPolicy_EmptyNameNormalizedToNo` | RED — `undefined: Translate` | GREEN |
| `TestTranslate_Config_Healthcheck_PreservesNilVsEmptyDistinction` (3 sub-cases) | RED — `undefined: Translate` | GREEN |
| `TestTranslate_NetworkSettings_FirstNetworkInNetworkingConfig` | RED — `undefined: Translate` | GREEN |
| `TestTranslate_NetworkSettings_ExtraNetworksReturnedSeparately` | RED — `undefined: Translate` | GREEN |
| `TestTranslate_FiltersShortIDAlias` | RED — `undefined: Translate` | GREEN |
| `TestTranslate_NetworkSettings_PinnedIPPreserved` | RED — `undefined: Translate` | GREEN |
| `TestTranslate_NetworkSettings_AutoAssignedIPNotRePinned` | RED — `undefined: Translate` | GREEN |
| `TestTranslate_Config_Image_NotImageID` | RED — `undefined: Translate` | GREEN |
| `TestTranslate_HostConfig_Init_PointerTriState` (3 sub-cases) | RED — `undefined: Translate` | GREEN |
| `TestTranslate_Config_Labels_PreservesComposeAndHmiUpdateNamespaces` | RED — `undefined: Translate` | GREEN |
| `TestRecreate_NoComposeProjectNameEnvDependency` (SC-6 ii) | RED — `undefined: Translate` | GREEN |
| `TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly` (SC-6 i) | GREEN-from-day-zero (regression lock-in) | STILL GREEN |
| `e2e/tests/relative-bind-mount.spec.ts` (SC-2 b + SC-6 iii) | RED on pre-Phase-9 | GREEN at unit-level companion (TestTranslate_HostConfig_Binds_*); e2e run pending Task 3 manual smoke |
| `internal/api/handlers_self_test.go` (SC-4 + SC-6 iv) | RED — `undefined: SelfUpdater` | STILL RED — Plan 09-04 owns the GREEN landing for this set |

## Image-size measurement

- **Pre-Phase-9 (base-debian12:nonroot):** ~26 MB (CI runner; under the prior 30 MB DEPLOY-02 budget)
- **Post-Phase-9 (static-debian12:nonroot, local docker build):** 4.3 MB (4,483,013 bytes via `docker image inspect`)
- **CI gate ceiling (post-Phase-9):** 12 MB (SC-3 b)
- **Headroom:** ~7.7 MB before tripping the gate; comfortable buffer for future UI bundle growth (current UI dist ~80 KB gzipped)

## Decisions Made

See the `key-decisions` frontmatter block. Five key choices:
1. SDK option types live on `client.*` (not `container.*` as RESEARCH.md Example 1 suggested) — corrected in client.go's type alias block + _sdk_shape.txt capture.
2. `NetworkConnectOptions` field is `EndpointConfig` (not `EndpointSettings`) — corrected in recreate.go's call site.
3. `compose.ErrComposeFailed` retained as a Deprecated public sentinel rather than deleted — operator-facing API stability over internal cleanliness.
4. `fakeRunner` type retained as a documentation anchor in orchestrator_test.go — encodes the "runner is fully gone" invariant via a no-op stub.
5. `stopHook` test seam replaces `fakeRunner.hook` for the ACT-08 contention test — ContainerStop is the first inside-the-lock daemon call in recreate.Service.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `ContainerStopOptions` location in moby SDK v0.4.1**
- **Found during:** Task 1, adding the type alias block to internal/docker/client.go
- **Issue:** RESEARCH.md Example 1 and the plan's action text suggested `ContainerStopOptions = container.StopOptions`, but `go doc github.com/moby/moby/api/types/container StopOptions` returns `no symbol StopOptions in package`. The type lives at `client.ContainerStopOptions`.
- **Fix:** Aliased all 5 new option types from `client.*`: `ContainerCreateOptions`, `ContainerRemoveOptions`, `ContainerStartOptions`, `ContainerStopOptions`, `NetworkConnectOptions`. Verified each via `go doc`.
- **Files modified:** internal/docker/client.go, internal/docker/_sdk_shape.txt
- **Verification:** `go build ./internal/docker/...` succeeds; `TestClient_InterfaceMethodCount` GREEN.
- **Committed in:** `438811e` (Task 1 commit)

**2. [Rule 1 - Bug] `NetworkConnectOptions.EndpointConfig` field name**
- **Found during:** Task 1, writing recreate.Service's NetworkConnect call site
- **Issue:** RESEARCH.md Pattern 1's code skeleton used `EndpointSettings: eps` as the field name; the actual SDK field is `EndpointConfig *network.EndpointSettings`.
- **Fix:** Used `EndpointConfig: eps` in recreate.go.
- **Files modified:** internal/recreate/recreate.go
- **Verification:** Translate tests for extras-network still GREEN (NetworkConnect call site compiles + behaves as expected).
- **Committed in:** `438811e` (Task 1 commit)

**3. [Rule 1 - Bug] `EndpointSettings.IPAddress` is `netip.Addr`, not `string`**
- **Found during:** Task 1, writing translate.scrubEndpoint
- **Issue:** First implementation assigned `out.IPAddress = ""` (string). The actual field type in moby v0.4.1 is `netip.Addr` (typed network address). Compilation error.
- **Fix:** Assigned `out.IPAddress = netip.Addr{}` (zero value as the typed "unset" sentinel); added `net/netip` import + a comment explaining the typed-zero pattern.
- **Files modified:** internal/recreate/translate.go
- **Verification:** TestTranslate_NetworkSettings_AutoAssignedIPNotRePinned GREEN.
- **Committed in:** `438811e` (Task 1 commit)

**4. [Rule 1 - Bug] `scrubEndpoint` return-type mismatch**
- **Found during:** Task 1, second compile error after fix #3
- **Issue:** `return out` returned a value of type `network.EndpointSettings`; the function signature returns `*network.EndpointSettings`.
- **Fix:** `return &out`.
- **Files modified:** internal/recreate/translate.go
- **Verification:** Build succeeds.
- **Committed in:** `438811e` (Task 1 commit)

**5. [Rule 3 - Blocking] Pre-existing fakeClient/panickingDockerClient stubs blocking compile**
- **Found during:** Task 2, post-orchestrator rewire `go vet ./...`
- **Issue:** Adding 5 methods to the `docker.Client` interface broke 4 in-repo fakeClient stub types (internal/docker/discovery_test.go, internal/api/getstate_noio_test.go, internal/api/handlers_healthz_test.go, internal/actions/orchestrator_test.go).
- **Fix:** Added the 5 missing stub methods to each fake. For panickingDockerClient (api/getstate_noio_test.go), the stubs PANIC per the existing convention (OBS-03 invariant: GET /api/state must not invoke any docker.Client method). For the others, the stubs return zero values (the test fixtures never exercise the recreate path).
- **Files modified:** all four test files listed above.
- **Verification:** `go build ./... && go test ./internal/{actions,compose,docker,recreate,poll,registry,state}/... -race` → ok.
- **Committed in:** `438811e` (discovery_test.go) + `ce115e3` (the rest, lockstep with the orchestrator-test fakeDockerClient extension).

**6. [Rule 1 - Bug] Plan's verify-gate `ls /etc/ssl/certs/ca-certificates.crt` command fails on distroless**
- **Found during:** Task 2 final verification
- **Issue:** The plan's automated verify block runs `docker run --rm --entrypoint '' docker-update:phase9-smoke ls /etc/ssl/certs/ca-certificates.crt` — but distroless/static-debian12:nonroot has NO `ls` binary (it ships ONLY the application binary + tzdata + CA certs + nonroot user). Command fails with "exec: ls: executable file not found in $PATH".
- **Fix:** Substituted an equivalent verification: `docker create docker-update:phase9-smoke` + `docker export | tar -tf` and grepped for `etc/ssl/certs/ca-certificates.crt`. The file is present — verified.
- **Files modified:** none (verification-only)
- **Verification:** docker export confirms `etc/ssl/certs/ca-certificates.crt` exists in the image.
- **Committed in:** N/A (verification step; not a code change)
- **Note for future plan-checkers:** Pitfall 5 in RESEARCH.md is real (static-debian12 DOES ship CA certs), but the plan's automated verify gate that asserts it via `ls` is broken because distroless ships nothing else. The correct invariant check is via `docker export` or a `crane.Digest` smoke against a real registry from inside the running binary.

---

**Total deviations:** 6 auto-fixed (5 Rule 1 bugs in plan/research text vs. actual SDK shapes; 1 Rule 3 blocking interface-compatibility on pre-existing test fakes).

**Impact on plan:** All six fixes are mechanical adaptations of the plan's intent to the SDK's actual shape. The 4 SDK-shape bugs (Rule 1 fixes #1-4) are the same class of issue documented in STATE.md as "Phase 02: SDK alias container.Summary not client.ContainerSummary — moby/moby/client v0.4.1 reorganised result types" — pattern repeats in v0.4.1 for the v9 additions. The plan's substantive intent is fully met: socket-only recreate works, all RED tests turn GREEN, image shrinks, CI gate tightens, drift detection survives.

## Issues Encountered

None — every issue surfaced as a deviation (Rule 1 or Rule 3) and was auto-fixed without requiring an architectural decision (Rule 4).

## User Setup Required

None - no external service configuration required.

For operators with pre-Phase-9 docker-update installs: when pulling the post-Phase-9 image, edit the on-HMI docker-compose.yml to delete the two `:ro` bind-mounts for the host docker binary and its cli-plugins directory. The post-Phase-9 image runs entirely off the docker socket (no host-side CLI dependency). README upgrade note is a follow-up (tracked at the phase level — Plan 09-04 / downstream docs PR).

## Next Plan Readiness

**Plan 09-04 executor: read this section before starting.**

What Plan 09-03 ships that Plan 09-04 builds on:

1. **`internal/recreate/recreate.Service`** — the helper's recreate primitive. Plan 09-04's `SelfUpdater.Spawn` implementation creates a one-shot helper container that calls `recreate.Service(ctx, cli, "docker-update")` to recreate the parent. The function is in place and unit-tested; just import + call.
2. **`docker.Client` 13-method interface** — Plan 09-04 needs no further interface growth for the self-update helper (Spawn uses ContainerCreate + ContainerStart which are already in place).
3. **`actions.NewOrchestrator` 8-arg signature** — Plan 09-04's wiring needs to match. The orchestrator no longer takes a runner; the helper-spawn surface lives on a NEW interface (Plan 09-04 owns `SelfUpdater` and `Server.selfUpdater`).
4. **`internal/api/handlers_self_test.go`** — still RED with `undefined: SelfUpdater`, `srv.selfUpdater undefined`, `srv.actionsInFlightFn undefined`. These 4 RED symbols name the exact surface Plan 09-04 must add. The tests use a `fakeSpawner` helper that implements the not-yet-existing `SelfUpdater` interface.
5. **CheckSelfProtection in actions/middleware.go is UNTOUCHED.** Plan 09-04 routes around it via the new `/api/self-update` endpoint (Pattern 5 in RESEARCH.md — separate endpoint, not header/route signal). The per-service `/api/containers/{svc}/update` endpoint still 409s on `svc == "docker-update"` (TestHandleUpdate_DockerUpdateSvc_StillReturns409 is the regression seal).

Suggested Plan 09-04 flip order:
1. Add `SelfUpdater` interface to internal/api per RESEARCH.md Example 2.
2. Add `Server.selfUpdater SelfUpdater` field.
3. Add `Server.actionsInFlightFn func() int` field.
4. Add `handleSelfUpdate(w, r)` per RESEARCH.md Example 2.
5. Register `POST /api/self-update` in routes().
6. Land the same-binary `--self-update-orchestrator` flag mode in cmd/docker-update/main.go (Pattern 4).
7. Run `go test ./internal/api/ -run "TestHandleSelfUpdate_"` — each RED test flips GREEN one at a time.

## TDD Gate Compliance

This plan is the GREEN half of the cross-plan RED→GREEN cycle started in Plan 09-02. Per the plan's design:

| Gate | Plan 09-02 (RED) | Plan 09-03 (GREEN — this plan) |
|------|-----------------|-------------------------------|
| RED commit before GREEN | ✓ 5 `test(09-02): ...` commits | (consumed) |
| Tests RED on production | ✓ `undefined: Translate` etc. | (now GREEN — proven by `go test ./internal/recreate/... -race`) |
| GREEN commit lands implementation | (deferred to 09-03) | ✓ `438811e` + `ce115e3` |
| No squash across RED→GREEN boundary | ✓ Each test commit alone | ✓ feat commits alone, distinct from the 09-02 test commits |

C4 compliance: full RED → GREEN cycle complete for the SC-2 / SC-6 (i, ii, iii) goal-backward anchors. SC-4 + SC-6 (iv) remain RED — Plan 09-04 owns the GREEN landing.

---

## Self-Check: PASSED

Verified:
- `internal/recreate/translate.go` exists ✓
- `internal/recreate/recreate.go` exists ✓
- `internal/recreate/recreate_test.go` exists with 7 `^func TestService_` funcs ✓
- `internal/compose/runner.go` does NOT exist ✓
- `internal/compose/runner_test.go` does NOT exist ✓
- `internal/compose/reader.go` STILL exists ✓ (drift detection preserved per Open Q4)
- All commits visible: `438811e` (Task 1 feat), `ce115e3` (Task 2 feat) ✓
- `go build ./...` exits 0 ✓
- `go test ./internal/{actions,compose,docker,recreate,poll,registry,state}/... -race` exits 0 ✓
- `make grep-no-compose` exits 0 ✓
- `docker build -t docker-update:phase9-smoke .` exits 0; final image 4.3 MB (<12 MB SC-3 b budget) ✓
- `docker export` of the new image contains `etc/ssl/certs/ca-certificates.crt` ✓ (Pitfall 5 satisfied)
- Dockerfile FROM line: `gcr.io/distroless/static-debian12:nonroot` ✓
- `docker-compose.example.yml` has no `/usr/bin/docker` or `/usr/libexec/docker/cli-plugins` references (code OR comments) ✓
- `.github/workflows/ci.yml` image-size gate occurrences of 12000000 ≥ 2, "12 MB budget" ≥ 1, zero "30000000" or "30 MB budget" remaining ✓
- `.github/workflows/ci.yml` Idle-RAM gate `30 * 1024` arithmetic at lines ~264-269 UNTOUCHED ✓
- Plan 09-02 RED tests verified GREEN (all 14 translate cases pass; SC-6 i guard still passes) ✓

Plan 09-04's RED set (`internal/api/handlers_self_test.go`) remains RED as expected — cross-plan handoff working as designed.

---

*Phase: 09-architectural-hardening-post-v0-1-bug-cluster*
*Completed: 2026-05-16*
