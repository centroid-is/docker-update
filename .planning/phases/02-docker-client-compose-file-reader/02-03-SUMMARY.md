---
phase: 02-docker-client-compose-file-reader
plan: 03
subsystem: infra
tags: [go, docker-events, single-consumer, state-mutation, reconnect-backoff, anti-deadlock]

# Dependency graph
requires:
  - phase: 02-docker-client-compose-file-reader
    plan: 01
    provides: docker.Client interface (Ping/ContainerList/ContainerInspect/Events/ImagePull/ImageTag); state.Container schema (ContainerID/Labels/Pinned/Stopped); 9 SDK type aliases including ContainerSummary, ContainerInspect, EventMessage, EventsListOptions

provides:
  - internal/docker.Discoverer struct + NewDiscoverer constructor + Run(ctx) blocking method
  - Boot ContainerList with label=hmi-update.watch=true filter (DOCK-04 boot path)
  - Events subscription filtered to type=container + event=start|die|destroy
  - Exponential reconnect backoff (1s, 2s, 4s, 8s, 16s, 30s-cap); re-runs boot list on every successful reconnect
  - Anti-deadlock-safe upsertFromInspect — ContainerInspect runs BEFORE state.Store.Update closure; never inside it
  - parseImageRef helper — image/tag split with registry-prefix port-colon disambiguation; @sha256: terminator wins (Pinned signal)
  - filterHmiLabels helper — strips non-hmi-update.* labels per T-02-03-01
  - Package-private stateStore interface (Get + Update) — production takes *state.Store; tests inject safeStore / recordingStore wrappers
  - test seam SetSleeperForTest + newDiscovererWithStore for deterministic test injection

affects: [02-04-healthz-upgrade (main.go wiring), 02-05-e2e-discovery-spec, 03-poller (second producer joining the state-mutation surface)]

# Tech tracking
tech-stack:
  added: []  # no new go.mod entries — all stdlib + the moby aliases re-exported from plan 02-01
  patterns:
    - "Single-producer state mutation goroutine — Discoverer.Run is the first long-running goroutine in the codebase; Phase 3's poller will be the second. Both feed state.Store.Update serialized by RWMutex."
    - "Test-only interface seam — package-private stateStore exposed via newDiscovererWithStore lets tests substitute wrappers without exposing the seam in the production NewDiscoverer constructor signature."
    - "Deep-copy snapshot wrapper (safeStore) — works around state.Store.Get returning a shallow snapshot whose inner map shares the reference state.Store.Update mutates. Tests that observe state from a polling goroutine race the discoverer without this wrapper."
    - "Channel-instrumented anti-deadlock test — fakeClient.ContainerInspect parks on a release-channel + asserts via t.Errorf that recordingStore.updateInvoked is still false at inspect-entry. Directly verifies call ordering at the regression site instead of inferring it from downstream consequences."

key-files:
  created:
    - internal/docker/discovery.go (412 lines — Discoverer + bootList + eventsLoop + computeBackoff + drainEvents + handleEvent + upsertFromInspect + markStopped + removeContainer + serviceForContainerID + shortID + parseImageRef + filterHmiLabels + package-private stateStore interface)
    - internal/docker/discovery_test.go (857 lines — fakeClient, safeStore, recordingStore, 10 tests)
  modified: []

key-decisions:
  - "Discoverer's store field uses a package-private stateStore interface (Get + Update). Production NewDiscoverer takes *state.Store concretely; the test-only newDiscovererWithStore accepts any stateStore implementation. The interface is NOT a Phase 3 extension point — Phase 3's cron poller will continue to use *state.Store directly."
  - "EventMessage shape: ev.Action (events.Action — a string type) for the dispatch switch; ev.Actor.ID for the container ID. Plan 02-01's SDK shape capture surfaced this via api/types/events.Message; no iterator-to-channel adapter needed (the SDK is already channel-shaped via EventsResult{Messages, Err})."
  - "ContainerInspect field paths: insp.Container.Config.Image (image ref) and insp.Container.Config.Labels (full label map). cfg = nil guard handles malformed daemon responses defensively."
  - "ContainerListOptions.Filters constructed as the literal Filters{\"label\": {\"hmi-update.watch=true\": true}} map shape rather than via Filters.Add — both produce the same wire shape, and the literal makes the boot-list contract visually unambiguous at the call site."
  - "EventsListOptions.Filters filters both type=container AND event=start|die|destroy at the SDK layer. The handleEvent switch's default branch is defensive only."
  - "Backoff attempt counter persists across loop iterations on failure. The naive design (reset attempt to 0 after every Events() subscription returns) caps progression at 1s forever because the SDK returns the channel pair synchronously even when the subscription fails — the error fires moments later on errCh. attempt is only reset implicitly when a non-failing drain happens (we never reach the failure path)."
  - "parseImageRef defaults bare refs to tag='latest' (docker CLI implicit behaviour). Pinned refs (with @sha256:) return tag='' so Container.Tag is empty for pinned containers; Pinned bool carries the signal separately. This matches Phase 3 / Phase 5's expectations (the digest poller filters by Pinned; the UI renders 'pinned: opt-out' separately)."
  - "Test 9 ordering: recordingStore.Update flips updateInvoked BEFORE delegating to inner.Update. That timing means a regression that moves ContainerInspect into the Update closure fires the t.Errorf at inspect-entry deterministically — the assertion does not depend on inner.Update having actually mutated the map."

patterns-established:
  - "stateStore interface — narrow seam between Discoverer and *state.Store, package-private, two methods (Get, Update). Production callers see no surface change; tests inject wrappers. Future packages that need similar test seams should follow this pattern (e.g. Phase 3's poller could adopt the same interface)."
  - "safeStore wrapper for test polling — when a production type returns a shallow snapshot with shared map references (state.Store.Get's documented behaviour), tests that poll the snapshot concurrently with a writer must wrap with a deep-copy layer. The wrapper lives in _test.go space and is reusable across test files in the package."
  - "Reconnect-backoff progression as a structured slog event — every reconnect attempt logs discovery.events.reconnect with attempt, backoff_ms, drain_reason; every successful reconnect logs discovery.events.reconnected with attempt. An operator timeline is fully reconstructable from logs."

requirements-completed: [DOCK-04]

# Metrics
duration: ~25min
completed: 2026-05-13
---

# Phase 02 Plan 03: Discoverer Goroutine — Boot List + Events + Reconnect Backoff Summary

**internal/docker.Discoverer ships as the single producer of container state mutations in Phase 2: a boot ContainerList(label=hmi-update.watch=true) seeds state, a long-running Events subscription handles start/die/destroy, and an exponential 1s→30s backoff survives daemon flaps. Anti-deadlock invariant (ContainerInspect strictly before state.Store.Update) verified by a channel-instrumented call-ordering test.**

## Performance

- **Duration:** ~25 min (commit timestamps: 1bc90b6 RED → 6c01803 GREEN)
- **Tasks:** 1 (TDD task split into RED + GREEN per C4)
- **Files:** 2 created (discovery.go + discovery_test.go); 0 modified
- **Tests:** 10 passing under `-race -v` (9 Discoverer + 1 parseImageRef)

## Accomplishments

- **DOCK-04 satisfied at the unit-test level:** A FakeClient feeding one ContainerSummary at boot results in a state.Containers row within 2s of Discoverer.Run starting. A start-event flow inspects + upserts; a die-event flips Stopped while preserving CurrentDigest/Image/Tag; a destroy-event removes the row.
- **Anti-deadlock invariant enforced by Test 9 directly:** the recordingStore wrapper flips an atomic.Bool the instant Update's closure enters; the fakeClient's ContainerInspect asserts via t.Errorf that the atomic is still false at inspect-entry. A future regression that moves ContainerInspect into the Update closure fires the assertion at the call site.
- **Reconnect backoff progression pinned at 1s, 2s, 4s, 8s, 16s, 30s-cap:** Test 7 simulates 10 consecutive synthetic disconnects with a no-op sleeper that records durations; the captured sequence matches expectations exactly. The cap (computed at attempt 6: 1s << 5 = 32s clamped to 30s) is verified at attempts 6-10.
- **Boot list re-runs on reconnect:** Test 8 scripts one disconnect then clean Events channels; ContainerList is called exactly twice (boot + post-reconnect) per CONTEXT.md `<specifics>`.
- **Label filter is the T-02-03-01 mitigation:** Test 6 plants `com.docker.compose.service`, `com.docker.compose.project`, and `org.opencontainers.image.title` alongside three `hmi-update.*` labels; the resulting Container.Labels map has exactly the three hmi-update keys.
- **parseImageRef registry-prefix disambiguation:** TestParseImageRef_RegistryPrefixed covers five branches including the load-bearing `localhost:5000/foo` case (port colon must NOT split into tag=`5000/foo`).
- **No moby SDK leak outside internal/docker:** the package boundary established in plan 02-01 holds — discovery.go imports only `github.com/moby/moby/api/types/events` (for the ActionStart/Die/Destroy constants and the ContainerEventType constant) and the package-local SDK type aliases from client.go. Phase-3/4 packages still depend only on `internal/docker`.

## Moby SDK field paths used

Per the plan's `<output>` section, here are the exact field paths consumed (cross-references plan 02-01's SDK shape capture in `internal/docker/_sdk_shape.txt`):

| Plan-skeleton placeholder | Actual moby/moby/client v0.4.1 path |
|---------------------------|-------------------------------------|
| `ev.Action` | `events.Message.Action` (typed as `events.Action` — a string newtype) |
| `ev.Actor.ID` | `events.Message.Actor.ID` (the Actor struct has fields `ID string` and `Attributes map[string]string`) |
| `ev.Type` | `events.Message.Type` (typed as `events.Type` — only set to `events.ContainerEventType` by our SDK-side filter) |
| `insp.Config.Image` | `client.ContainerInspectResult.Container.Config.Image` (string) — note: Config is `*container.Config`, so a nil guard exists |
| `insp.Config.Labels` | `client.ContainerInspectResult.Container.Config.Labels` (map[string]string) |
| `c.ID` (boot list) | `container.Summary.ID` (the api/types/container.Summary struct, aliased as docker.ContainerSummary) |

The action constants used in handleEvent's switch are:
- `events.ActionStart` (`"start"`)
- `events.ActionDie` (`"die"`)
- `events.ActionDestroy` (`"destroy"`)

The event-type constant `events.ContainerEventType` (`"container"`) is used in the EventsListOptions.Filters map.

## Events surface shape — confirmed

Plan 02-01's findings hold: `client.Events(ctx, opts)` returns `EventsResult{Messages <-chan events.Message, Err <-chan error}` already channel-shaped. The mobyClient adapter (plan 02-01) unpacks directly into the `(<-chan EventMessage, <-chan error)` pair the Client interface promises. No iterator-to-channel adapter goroutine needed in Discoverer — `drainEvents` consumes the two channels directly via a `select`.

## parseImageRef tag-default decision

Per the plan's `<output>` section: parseImageRef returns `tag="latest"` for a bare ref like `busybox`. Rationale:
- Matches the docker CLI's implicit `:latest` default.
- Phase 3's digest poller (DETECT-01) builds a manifest-request URL that requires a non-empty tag.
- `tag=""` would force every downstream consumer to add a `tag == "" ? "latest" : tag` guard.

The exception is pinned refs (`@sha256:` terminator): for those, `tag=""` is correct because Container.Pinned carries the signal and Phase 3 skips pinned rows entirely.

## Test polling cadence

Per the plan's `<output>` section: the `eventually` helper uses **10ms tick / 2s deadline** for state observation (Tests 1, 2, 5, 6). Tests 3 and 4 use 2s deadlines as well. Test 7 (reconnect backoff) doesn't poll state — it captures sleep durations via a recording sleeper. Test 8 polls call counts. Test 9 uses a 1s deadline after releasing the inspect block.

**Observed flakiness:** None. The tests were authored to run in <100ms each by injecting no-op sleepers; the longest is Test 3 at ~50ms because the die-event mutation goes through Update's renameio.WriteFile + dir-fsync sequence per state.Store's persistence contract. Under `-race -v` the full suite runs in ~1.5s.

## Test Commits

This was a TDD task; the plan executed as RED → GREEN per C4:

1. **Task 1 RED: failing tests for Discoverer** — `1bc90b6` (test) — internal/docker/discovery_test.go with all 10 tests. Build verified to fail with `undefined: NewDiscoverer` / `undefined: parseImageRef`.
2. **Task 1 GREEN: Discoverer + helpers + parseImageRef** — `6c01803` (feat) — internal/docker/discovery.go (412 lines); discovery_test.go evolved from RED to add the safeStore wrapper that closes the race-detector flag between state.Store.Get's shallow snapshot and concurrent Update calls.

No REFACTOR commit — the GREEN code already meets the documented quality bar (inline anti-deadlock callouts, threat-model cross-refs in doc comments, structured slog event names per CONTEXT.md "Claude's Discretion").

## Files Created/Modified

**Created:**

- `internal/docker/discovery.go` (412 lines) — Package-doc anchored to ARCHITECTURE.md Pattern 3; Discoverer struct + NewDiscoverer + newDiscovererWithStore (test-only) + SetSleeperForTest + Run + bootList + eventsLoop + drainEvents + handleEvent + upsertFromInspect + markStopped + removeContainer + serviceForContainerID + computeBackoff + shortID + parseImageRef + filterHmiLabels + stateStore interface + composeServiceLabel constant.
- `internal/docker/discovery_test.go` (857 lines) — fakeClient implementing docker.Client (scripted ContainerList / ContainerInspect, hand-driven Events channel pair); safeStore wrapper (deep-copy Get under its own RWMutex); recordingStore wrapper (signals on Update); 10 tests: TestDiscoverer_BootList_PopulatesState, TestDiscoverer_StartEvent_UpsertsContainer, TestDiscoverer_DieEvent_SetsStopped, TestDiscoverer_DestroyEvent_RemovesRow, TestDiscoverer_PinnedDetection, TestDiscoverer_LabelFilter, TestDiscoverer_ReconnectBackoff, TestDiscoverer_ReconnectTriggersBootList, TestDiscoverer_InspectPrecedesUpdate, TestParseImageRef_RegistryPrefixed.

**Modified:** none. Plan 02-01's interface contract was sufficient; no go.mod / go.sum entries changed (the moby/moby/client// indirect marker flips once main.go imports Discoverer in plan 02-04 — cosmetic).

## Deviations from Plan

**[Rule 1 — Bug] Test 9 watcher race.** The plan-skeleton's "second goroutine doesn't block" formulation was already replaced in the plan with a channel-instrumented variant. The plan's variant proposed a watcher goroutine that polls `state.Get().Containers["ordered"]` and flips an atomic when the row appears — this would itself race the discoverer's concurrent Update calls because state.Store.Get returns a shallow snapshot whose inner map is shared. Resolution: introduced a `recordingStore` wrapper that flips the atomic from INSIDE its Update method (race-clean atomic.Bool), eliminating the watcher entirely. The plan's anti-deadlock claim still holds — the wrapper's Update flips BEFORE delegating, so a future regression that moves inspect into the closure trips the test at inspect-entry.

**[Rule 1 — Bug] Backoff `attempt = 0` reset on every loop iteration.** First-draft implementation reset attempt to 0 immediately after `client.Events()` returned the channel pair — defeating the exponential progression because the SDK returns the channels synchronously even when the subscription fails, and the error fires moments later on errCh. Fix: removed the unconditional reset; attempt is only reset implicitly by a non-failing drain.

**[Rule 2 — Critical functionality] safeStore wrapper for tests.** The plan did not anticipate the race-detector flag on tests that read state.Get().Containers concurrently with the Discoverer's Update writes. state.Store.Get's doc comment notes the shared-map shape is safe for the http /api/state handler (which json.Marshals immediately) but explicitly says "If a future caller needs to mutate the returned state, copy the Containers map first." Tests count as a future caller. Added the safeStore test helper that takes a deep copy under its own RWMutex; all 6 race-sensitive tests adopt it via `newDiscovererWithStore(fc, store)`. Rationale documented inline in discovery_test.go.

**[Rule 2 — Critical functionality] package-private stateStore interface.** Introducing the safeStore wrapper required Discoverer to accept the wrapper as well as the production *state.Store. Resolution: extracted a two-method package-private `stateStore` interface; production `NewDiscoverer` continues to take `*state.Store`; the test-only `newDiscovererWithStore` accepts the interface. No production surface change.

None of these deviations changed the plan's behavioural contract — all 10 tests pass with the test logic the plan prescribed; the deviations are implementation-side adjustments to satisfy the `-race` gate.

## Issues Encountered

**1. state.Store.Get returns a shallow snapshot.** The race detector flagged map access in 6 tests on first GREEN run. Documented above as a Rule 2 deviation; resolved with the safeStore wrapper. Pre-existing pattern from Phase 1 — the production /api/state handler is safe because json.Marshal serializes the read, but parallel test polling exposes the shared-map race.

**2. Naive backoff reset defeated progression.** Documented above as a Rule 1 deviation; resolved by deferring the reset to a non-failing drain path.

**3. None other.** The plan's behaviour block was faithful to the SDK shapes captured in plan 02-01. The `events.Action` typed-string field name matched the plan's `ev.Action` placeholder verbatim. The `container.Config` nil guard (insp.Container.Config could in theory be nil from a malformed daemon response) was added defensively — the plan did not call for it but it is a Rule 2 critical-functionality add (panic prevention).

## go test ./internal/docker/... -race -v output (excerpt)

```
=== RUN   TestDiscoverer_BootList_PopulatesState
--- PASS: TestDiscoverer_BootList_PopulatesState (0.03s)
=== RUN   TestDiscoverer_StartEvent_UpsertsContainer
--- PASS: TestDiscoverer_StartEvent_UpsertsContainer (0.03s)
=== RUN   TestDiscoverer_DieEvent_SetsStopped
--- PASS: TestDiscoverer_DieEvent_SetsStopped (0.05s)
=== RUN   TestDiscoverer_DestroyEvent_RemovesRow
--- PASS: TestDiscoverer_DestroyEvent_RemovesRow (0.05s)
=== RUN   TestDiscoverer_PinnedDetection
--- PASS: TestDiscoverer_PinnedDetection (0.02s)
=== RUN   TestDiscoverer_LabelFilter
--- PASS: TestDiscoverer_LabelFilter (0.03s)
=== RUN   TestDiscoverer_ReconnectBackoff
--- PASS: TestDiscoverer_ReconnectBackoff (0.02s)
=== RUN   TestDiscoverer_ReconnectTriggersBootList
--- PASS: TestDiscoverer_ReconnectTriggersBootList (0.03s)
=== RUN   TestDiscoverer_InspectPrecedesUpdate
--- PASS: TestDiscoverer_InspectPrecedesUpdate (0.04s)
=== RUN   TestParseImageRef_RegistryPrefixed
--- PASS: TestParseImageRef_RegistryPrefixed (0.00s)
PASS
ok  	github.com/centroid-is/hmi-update/internal/docker	1.492s
```

The pre-existing plan-02-01 tests (TestMobyClient_SatisfiesClient, TestNewClient_FromEnv_DefaultSocket, TestNewClient_BadDockerHost, TestClient_InterfaceMethodCount) continue to pass — no interface drift introduced.

## slog event names emitted

Per the plan's `<done>` requirement, these structured event names are emitted at the documented points:

- `discovery.boot.start` — Run() entry
- `discovery.boot.list` — after every ContainerList call (boot + post-reconnect re-list); attribute `count`
- `discovery.boot.fail` — bootList error path
- `discovery.event.received` — every event before dispatch; attributes `action`, `container_id` (short form)
- `discovery.events.stream.err` — errCh error from the daemon stream
- `discovery.events.reconnect` — every reconnect attempt; attributes `attempt`, `backoff_ms`, `drain_reason`
- `discovery.events.reconnected` — successful reconnect (attempt>0 entering the next subscription); attribute `attempt`
- `discovery.reconnect.boot.fail` — post-reconnect re-list error
- `discovery.inspect.fail` — ContainerInspect error path
- `discovery.inspect.no-config` — defensive nil-Config guard
- `discovery.event.start.persist` / `discovery.event.die.persist` / `discovery.event.destroy.persist` — state.Store.Update error paths

All event names follow lowercase dotted convention per CONTEXT.md "Claude's Discretion."

## Threat Model Coverage

| Threat ID | Status | Evidence |
|-----------|--------|----------|
| T-02-03-01 (Tampering — container labels) | mitigated | filterHmiLabels accepts ONLY keys with the hmi-update. prefix; verified by TestDiscoverer_LabelFilter which plants `com.docker.compose.*` and `org.opencontainers.*` and asserts they are stripped. |
| T-02-03-02 (DoS — event-stream burst) | mitigated (foundation) | drainEvents processes one event at a time; the SDK's channel is the buffer. Phase 2 doesn't ship a back-pressure metric; if a sustained burst surfaces in production, Phase 3 will add one. |
| T-02-03-03 (Information disclosure — slog logs identifiers) | accepted | container_id (short, 12 chars) and service name appear in `docker ps` output already; logging is operational telemetry. |
| T-02-03-04 (EoP — daemon access) | accepted | Read-only enumeration + inspect in Phase 2; the daemon socket access is intended. |
| T-02-03-05 (Repudiation — silent reconnect) | mitigated | Every reconnect attempt emits `discovery.events.reconnect` with attempt + backoff_ms + drain_reason; every successful reconnect emits `discovery.events.reconnected` with attempt. Operator timeline reconstructable from slog. |

## Next Phase Readiness

**Ready for plan 02-04 (healthz upgrade + main.go wiring):**
- The Discoverer constructor signature is final: `NewDiscoverer(client docker.Client, store *state.Store) *Discoverer`. Plan 02-04's main.go will add a goroutine: `go discoverer.Run(ctx)`.
- The events subscription consumes ctx for cancellation — graceful shutdown deferred to Phase 4 STATE-04 as documented in CONTEXT.md "### Lifecycle & Wiring".

**Ready for plan 02-05 (e2e discovery spec):**
- The unit-test foundation is in place. Plan 02-05's `e2e/tests/discovery.spec.ts` will assert that `/api/state` contains `stub-watched-container` within 60s of `docker compose up -d` — that's the DOCK-04 e2e proof.

**Ready for Phase 3 (poller — the second producer):**
- Discoverer's anti-deadlock pattern (inspect-then-update) becomes the model for the poller: digest-fetch happens OUTSIDE state.Store.Update; the closure body is a pure map mutation.
- The single-consumer pattern from ARCHITECTURE.md Pattern 3 stays implicit (no channel) until Phase 3 introduces the second producer; if poller-vs-discovery interleaving needs a serialization seam at that point, a channel materializes between them.

**Blockers/concerns introduced:** None. The package-private `stateStore` interface is internal to internal/docker; no Phase-3 surface depends on it.

## Self-Check: PASSED

Verified files exist:
- `internal/docker/discovery.go` — FOUND
- `internal/docker/discovery_test.go` — FOUND

Verified commits exist (per `git log --oneline -5`):
- `1bc90b6` — FOUND (test commit, RED phase)
- `6c01803` — FOUND (feat commit, GREEN phase)

Verified gates pass:
- `go build ./...` — exit 0
- `go vet ./...` — exit 0
- `go test ./... -race` — all packages PASS
- `go test ./internal/docker/... -race -run 'TestDiscoverer|TestParseImageRef'` — 10/10 PASS
- Plan-02-01 interface tests (`TestMobyClient_SatisfiesClient`, `TestClient_InterfaceMethodCount`) still PASS — no interface drift

## TDD Gate Compliance

- RED commit: `1bc90b6` — test(02-03): adds failing tests; build verified to fail with `undefined: NewDiscoverer` / `undefined: parseImageRef`.
- GREEN commit: `6c01803` — feat(02-03): drives all 10 tests to pass under -race.
- REFACTOR commit: not present — GREEN code is at the documented quality bar (anti-deadlock inline callouts, threat-model cross-refs, structured slog event-name catalog, package-private stateStore interface seam). REFACTOR is optional per execute-plan.md.

---
*Phase: 02-docker-client-compose-file-reader*
*Completed: 2026-05-13*
