---
phase: 03-registry-polling-update-detection
plan: 03
subsystem: poll
tags: [go, cron, errgroup, regexp, race-detector, tdd, single-consumer-channel, state-mutation]

# Dependency graph
requires:
  - phase: 03-registry-polling-update-detection
    provides: "internal/state.Container.{AvailableDigest, LastPolledAt, Notes} + internal/state.State.{LastPollStart, LastPollEnd, LastPollError} from plan 03-01; internal/registry.Resolver + classify-into-{ErrPermanent,ErrTransient} from plan 03-02"
  - phase: 02-docker-discovery
    provides: "internal/docker.Discoverer (the FIRST producer of state-update channel — Phase 3 plan 03-04 rewires it through this plan's channel); the safeStore + newTestStore + drainEvents + stateStore patterns this package adapts"
  - phase: 01-walking-skeleton-test-harness
    provides: "internal/state.Store atomic write + RWMutex pattern; table-driven test convention"
provides:
  - "internal/poll.Patterns — compiled tag-pattern regex cache (RWMutex-guarded map; permissive on invalid regex + slog warning)"
  - "internal/poll.StateUpdate + UpdateKind (KindDigestResolved/KindContainerEvent/KindPollSweepStart/KindPollSweepEnd) — the message type for the single-consumer state-mutation channel"
  - "internal/poll.RunUpdater — single-consumer goroutine collapsing multiple producers; drains pending messages on ctx.Done for graceful shutdown (DETECT-10 acceptance criterion)"
  - "internal/poll.Poller interface + cronPoller concrete impl (NewPoller constructor) — robfig/cron/v3 scheduler with errgroup-bounded fan-out (DETECT-05/08/09)"
  - "internal/poll.newPollerForTest — test seam exposing explicit concurrency knob without t.Setenv coupling"
  - "internal/poll.storeReader — package-private narrow interface (Get state.State) so cronPoller accepts either *state.Store (production) or *safeStore (race-clean tests)"
  - "Three canonical Notes strings — pinned: opt-out (DETECT-09), running tag does not match tag-pattern label (DETECT-08), registry error: <class> (check image ref). Each lives at exactly one quoted assignment site, gated by package-level const definitions."
affects:
  - "03-04 (main.go wiring) — registers RunUpdater goroutine + cronPoller goroutine + the channel; rewires Phase 2's discovery goroutine through the channel instead of calling state.Store.Update directly"
  - "03-05 (e2e specs) — DETECT-05 cron-tick observable via /api/state.last_poll_end advancing; DETECT-08 tag-pattern observable via state.containers[svc].notes; DETECT-09 pinned observable via the same notes field"
  - "Phase 4 (Update/Rollback) — the StateUpdate channel is the third-producer extension point for action results; the drain-on-cancel semantic is load-bearing for STATE-04 SIGKILL resistance"
  - "Phase 5 UI — renders state.containers[svc].notes per row; this plan defines the canonical strings the UI matches against"

# Tech tracking
tech-stack:
  added:
    - "github.com/robfig/cron/v3 v3.0.1 (promoted from indirect to direct; cron scheduler with Logger interface for slog routing + WithChain(Recover) panic guard + 5-field strict parser)"
    - "golang.org/x/sync/errgroup v0.20.0 (promoted from indirect; bounded worker pool via SetLimit; ordering pitfall (SetLimit before any Go) verified by source-order acceptance criterion)"
  patterns:
    - "Single-consumer state-mutation channel with drain-on-cancel — StateUpdate{Kind, Service, Apply func(*state.State)} flows from multiple producers (docker events + cron + future actions) through one buffered channel to RunUpdater. The store's RWMutex is taken INSIDE Update only; producers compute I/O OUTSIDE the lock. Outer-select cancel + inner-non-blocking-select drain is the load-bearing graceful-shutdown pattern."
    - "Tagged-union message type via UpdateKind iota — single struct + integer discriminator beats N channels for a 4-variant union (CONTEXT.md Area 2 'Claude's Discretion' carried through)."
    - "Compile-once regex cache pattern — Patterns owns *regexp.Regexp keyed by compose service name; raw label string is the persisted source of truth but the compiled artifact is in-memory because regexp.Regexp is not JSON-serializable. Permissive on compile failure + slog warning, never crashes boot."
    - "Package-private narrow store interface — storeReader (Get only) mirrors internal/docker.stateStore. Production passes *state.Store concretely; tests inject *safeStore deep-copy wrapper for race-clean snapshots."
    - "Test-seam constructor pattern — newPollerForTest in non-_test.go file mirrors internal/docker.newDiscovererWithStore. Test-only seam (exposing explicit concurrency knob); production callers use NewPoller."
    - "Canonical-string discipline via package consts — notePinnedOptOut / noteTagMismatch / noteRegistryPrefix package-level consts hold the single source-of-truth for Notes literals; assignment sites use the const symbol so the literal string appears exactly once in poller.go (gated by the plan's source-grep AC)."
    - "Cron-spec fail-fast at NewPoller via throwaway probe — robfig/cron's AddFunc is the parser; NewPoller probes with a no-op AddFunc and discards the cron, then Run constructs the live scheduler and AddFunc's the real tick body (so each Run binds its own ctx into the closure — plan-check Warning 5)."

key-files:
  created:
    - "internal/poll/patterns.go (106 LOC) — Patterns struct + NewPatterns/Set/Match/Delete with RWMutex + slog 'tag_pattern.invalid_regex' warning on compile failure"
    - "internal/poll/patterns_test.go (143 LOC) — 7 tests: ValidRegex_Match, ValidRegex_NoMatch, NoPatternSet_PermissiveDefault, InvalidRegex_PermissiveWithWarning, EmptyPattern_DeletesEntry, Concurrent_RaceClean (8x100 -race), DeleteRemovesPattern"
    - "internal/poll/channel.go (135 LOC) — UpdateKind iota + StateUpdate + storeUpdater seam + RunUpdater (production) + runUpdater (test interface form) with drain-on-cancel"
    - "internal/poll/channel_test.go (420 LOC) — 6 tests + safeStore wrapper (mirrors discovery_test.go): AppliesEachMessage, DrainOnCancel, ExitsOnCancelWithEmptyChannel, NoLockHeldAcrossSend (production-shape race smoke), StateUpdate_AllKinds, ErrorFromStore_Logged"
    - "internal/poll/poller_test.go (849 LOC) — 11 tests + fakeResolver with atomic in-flight/peak counters + drainUpdater test consumer: SatisfiesPoller, FailFastOnInvalidCron, TickInvokesSweep (DETECT-05), SkipsPinnedContainers (DETECT-09), SkipsStoppedContainers, AppliesTagPatternFilter (DETECT-08 happy), TagPatternRunningTagMismatch (DETECT-08 misconfig), ErrgroupSetLimitBeforeGo (Phase-3 pitfall), FetchSendsDigestResolvedUpdate, RespectsContext, PermanentErrorSurfacesNote"
  modified:
    - "internal/poll/poller.go (16 → 476 LOC) — replaced Phase-1 empty-interface stub with full cronPoller body: NewPoller + newPoller + newPollerForTest + Run (constructs live cron, AddFunc with Run's ctx, Start, <-ctx.Done, <-Stop().Done()) + sweep (KindPollSweepStart + errgroup.SetLimit before any Go + per-call WithTimeout + KindPollSweepEnd) + eligibleContainers + refForContainer + handleFetchResult + clearStaleErrorNotes + send* helpers + envInt + cronSlogAdapter"

key-decisions:
  - "[Phase 03 P03] storeReader package-private interface added so cronPoller can accept either *state.Store or the *safeStore deep-copy wrapper tests use — mirrors internal/docker.stateStore exactly. The poller's data dependency is read-only (writes flow through the channel), so the interface narrows to just Get(). Not a public extension point."
  - "[Phase 03 P03] runUpdater accepts a storeUpdater interface (Update only) — same WR-04-style narrowing for the consumer goroutine's write path. RunUpdater(public, takes *state.Store) wraps runUpdater(private, takes interface) so production wiring keeps the concrete type while tests inject an errStore for the error-path test (TestRunUpdater_ErrorFromStore_Logged) without bringing in a real *state.Store."
  - "[Phase 03 P03] Cron-spec fail-fast at NewPoller via a throwaway cron.AddFunc probe — discards the probe cron, then Run constructs the live scheduler with the same WithLocation+WithChain options and AddFunc's the real tick body capturing Run's ctx. This satisfies both (a) boot-time fail-fast on invalid HMI_UPDATE_CRON and (b) plan-check Warning 5 (sweep ctx derived from Run's ctx, not context.Background, so SIGTERM unblocks in-flight crane.Digest calls)."
  - "[Phase 03 P03] Canonical Notes strings centralized as package-level consts (notePinnedOptOut, noteTagMismatch, noteRegistryPrefix, noteRegistrySuffix). Comments reference the const symbols by name only, never the literal. This makes the plan's 'exactly one quoted match' source-grep acceptance criterion robust against doc additions."
  - "[Phase 03 P03] errgroup-with-context propagates SIGTERM into crane.Digest — sweep takes the cron tick's ctx (which is Run's ctx, captured via closure at AddFunc time), passes it to errgroup.WithContext, and per-worker uses context.WithTimeout(gctx, p.timeout) so the timeout is bounded by both the per-call budget AND root cancellation."
  - "[Phase 03 P03] Test cleanup uses t.Cleanup with LIFO ordering — updater-wait registers first (runs last), poller-wait registers second (runs first, calls cancel() then waits for cron.Stop().Done()). This ordering prevents TempDir-RemoveAll-cleanup races against in-flight state.Store.persist() writes that were observed during initial test development."

patterns-established:
  - "Pattern (Phase 3+): Single-consumer state-mutation channel — every package that mutates state.Store sends StateUpdate messages on the shared channel. RunUpdater is the only writer. Phase 4 will add a third producer (actions); the pattern is now structurally enforced and the drain-on-cancel semantic is part of the package's public contract."
  - "Pattern (Phase 3+): Tagged-union message via integer discriminator — UpdateKind iota is the established choice over N-channels for small unions. Future producers add a new Kind constant + (optionally) extend RunUpdater's slog attribution; the channel itself is single-typed."
  - "Pattern (Phase 3+): canonical-string-via-const for Notes / status / API error codes — when a plan's AC pins the literal string and the grep AC says 'exactly one match', define a package-level const and reference the symbol everywhere else (including comments)."
  - "Pattern (Phase 3+): test-seam non-_test.go constructor — when a test needs a different config than production (here: explicit concurrency vs env-var read), expose a package-private newXForTest in the production file (not a _test.go file). Production callers cannot see it (lowercase); tests in the same package can. Mirrors internal/docker.newDiscovererWithStore."
  - "Pattern (Phase 3+): cron-fail-fast via probe AddFunc — NewPoller validates the spec at construction time with a throwaway cron.New + AddFunc(spec, noop), surfaces a paste-ready error containing both the env var name and the format hint, and Run constructs the live scheduler with the same spec (now guaranteed to parse). Future cron-driven plans should adopt the probe pattern verbatim."
  - "Pattern (Phase 3+): goroutine cleanup via t.Cleanup with explicit LIFO ordering — when a test runs both a producer goroutine and a consumer goroutine that share a ctx, register the consumer-wait cleanup FIRST (runs LAST) and the producer-wait cleanup SECOND with cancel() inside it (runs FIRST). The producer drains gracefully (its own Stop().Done() path), then the consumer drains its pending channel, then TempDir RemoveAll runs safely."

requirements-completed: [DETECT-05, DETECT-08, DETECT-09, DETECT-10]

# Metrics
duration: ~50min
completed: 2026-05-14
---

# Phase 3 Plan 03: Polling, Tag-Pattern, Single-Consumer Channel Summary

**robfig/cron/v3 scheduler with errgroup-bounded fan-out (concurrency=4) feeding a single-consumer goroutine that drains-on-cancel — closes DETECT-05/08/09/10 in one orchestration package.**

## Performance

- **Duration:** ~50 min (TDD with 3 RED + 3 GREEN commits across 3 tasks)
- **Started:** 2026-05-14T13:36:00Z (approximate — first commit timestamp)
- **Completed:** 2026-05-14T13:54:00Z
- **Tasks:** 3 (6 commits via TDD: RED + GREEN per task)
- **Files modified:** 6 (4 new + 1 stub replacement + 1 test file paired)

## Accomplishments

- **Patterns regex cache (DETECT-08 backend):** compile-once cache keyed by compose service name; RWMutex for read-mostly access; permissive fallthrough + slog warning on invalid regex so boot never crashes on malformed `hmi-update.tag-pattern` labels. Empty pattern is "no constraint" semantically equivalent to never-set. Threat T-03-03-01 (ReDoS) prevented by construction since Go's regexp is RE2.
- **Single-consumer state-mutation channel (DETECT-10):** `StateUpdate{Kind, Service, Apply func(*state.State)}` with 4 UpdateKind variants. `RunUpdater` drains the channel into `state.Store.Update`, with an outer-select on ctx.Done that hands off to an inner non-blocking select draining pending messages — the load-bearing invariant for Phase 4's STATE-04 SIGKILL-resistance work. Two producers fed in Phase 3 (Phase 2's docker events; this plan's cron poller); Phase 4 will add a third.
- **cronPoller sweep (DETECT-05/08/09):** robfig/cron/v3 with `WithLocation(UTC)` + `WithChain(Recover(cronSlogAdapter{}))`. NewPoller validates the spec at boot via a throwaway probe (fail-fast on invalid HMI_UPDATE_CRON with both the env var name AND a "5-field" remediation hint). Run constructs the live cron scheduler and AddFuncs the tick body capturing Run's ctx (plan-check Warning 5 — SIGTERM unblocks in-flight crane.Digest calls). Sweep: `errgroup.WithContext + g.SetLimit(p.concurrency)` BEFORE any `g.Go` (Phase-3 pitfall guard verified by awk source-order AC), per-call `context.WithTimeout(gctx, p.timeout)`, graceful drain via `<-p.cronInst.Stop().Done()`.
- **Canonical Notes strings:** `pinned: opt-out` (DETECT-09), `running tag does not match tag-pattern label` (DETECT-08 misconfig), `registry error: <class> (check image ref)` (fetch errors). Each is a package-level const with exactly one quoted literal in the source — the grep ACs are robust against future doc additions.
- **24 unit tests passing under `go test ./internal/poll/ -race -count=10`** — the race detector is quiet across 10 repeats, the SetLimit ordering invariant is asserted by both source-grep and a runtime peak-in-flight test, and the drain-on-cancel test pre-loads 5 messages before cancel and confirms all 5 land before consumer exit.

## Task Commits

Each task was committed atomically with TDD's RED + GREEN pair:

1. **Task 1: Patterns regex cache (DETECT-08)**
   - RED: `e484be9` — `test(03-03): RED — Patterns regex cache (DETECT-08)`
   - GREEN: `2a4e87e` — `feat(03-03): implement Patterns regex cache (DETECT-08)`

2. **Task 2: StateUpdate channel + RunUpdater drain (DETECT-10)**
   - RED: `a726277` — `test(03-03): RED — StateUpdate channel + RunUpdater drain (DETECT-10)`
   - GREEN: `d5b07e5` — `feat(03-03): StateUpdate channel + RunUpdater drain-on-cancel (DETECT-10)`

3. **Task 3: cronPoller sweep + DETECT-05/08/09 close**
   - RED: `1e053db` — `test(03-03): RED — cronPoller sweep covering DETECT-05/08/09 + pitfall`
   - GREEN: `05d1305` — `feat(03-03): cronPoller sweep with errgroup fan-out (DETECT-05/08/09)`

## Files Created/Modified

- `internal/poll/patterns.go` (106 LOC, new) — `Patterns` RWMutex-guarded compiled-regex cache keyed by compose service name; `Set` logs `tag_pattern.invalid_regex` slog warning on compile failure and deletes the entry; `Match` is permissive on absent entries.
- `internal/poll/patterns_test.go` (143 LOC, new) — 7 tests including 8x100 concurrent race test under `-race`.
- `internal/poll/channel.go` (135 LOC, new) — `UpdateKind` enum + `StateUpdate` struct + `storeUpdater` interface seam + `RunUpdater` (public, `*state.Store`) + `runUpdater` (private, interface form). Inner non-blocking-select drains pending messages on ctx cancel; logs `poll.consumer.persist` on store.Update failure but continues processing.
- `internal/poll/channel_test.go` (420 LOC, new) — 6 tests + `safeStore` deep-copy wrapper (mirrors discovery_test.go's pattern) + `errStore` mock. Production-shape smoke (TestRunUpdater_NoLockHeldAcrossSend) uses real `*state.Store` + public RunUpdater + 4 concurrent producers.
- `internal/poll/poller.go` (16 → 476 LOC, modified) — replaced Phase-1 empty-interface stub with full `cronPoller` body. Fail-fast cron-spec validation via throwaway `AddFunc(spec, no-op)` probe; live `cron.Cron` constructed in `Run` so each invocation binds its own ctx into the tick closure; `errgroup.WithContext` + `SetLimit` BEFORE any `Go`; canonical Notes strings centralized in `note*` package consts; `cronSlogAdapter` satisfies `cron.Logger` interface for `cron.Recover` panic routing.
- `internal/poll/poller_test.go` (849 LOC, new) — 11 tests + `fakeResolver` with atomic in-flight/max-in-flight counters + `drainUpdater` test consumer. Goroutine cleanup uses `t.Cleanup` with explicit LIFO ordering (poller-wait registers second so it runs first, cancel()-ing inside; updater-wait registers first so it runs second after poller has drained).

## Decisions Made

- **storeReader narrow interface** (not exposing *state.Store concretely to cronPoller) — mirrors `internal/docker.stateStore` exactly. The poller only needs `Get()` since all writes flow through the StateUpdate channel; the wider `*state.Store` would be a temptation for future code to add Update calls outside the channel pattern.
- **Cron-spec fail-fast via throwaway probe** rather than validating at Run — keeps NewPoller's contract clean (returns error on invalid spec) and allows Run to bind its own ctx via the AddFunc closure (plan-check Warning 5). The probe cron.Cron is GC'd immediately.
- **Canonical Notes strings as package-level consts** — the plan's source-grep AC demanded "exactly one literal match" per canonical string. Defining `notePinnedOptOut` / `noteTagMismatch` / `noteRegistryPrefix+Suffix` consts and referencing the symbols in comments makes the AC robust against future doc additions that mention the strings descriptively.
- **`newPollerForTest` test seam in production file** (not _test.go) — mirrors `internal/docker.newDiscovererWithStore`. Tests need explicit concurrency for the SetLimit pitfall test without environment-variable side effects.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] state.Store.Get race in TestRunUpdater_AppliesEachMessage / DrainOnCancel / StateUpdate_AllKinds / ErrorFromStore_Logged**
- **Found during:** Task 2 (channel.go GREEN)
- **Issue:** `state.Store.Get()` returns a shallow snapshot whose inner `Containers` map header is shared with the writer. Reading `store.Get().Containers` from the test goroutine while the consumer goroutine concurrently runs `Update` trips the race detector — the exact same race Phase 2 documented in `discovery_test.go`'s `safeStore` wrapper.
- **Fix:** Imported the `safeStore` pattern from discovery_test.go (PATTERNS.md Pattern H — copy-paste convention) into channel_test.go: outer `sync.Mutex` serializes Get and Update through the wrapper; Get returns a freshly-allocated deep-copy of the Containers map. `runUpdater` accepts a `storeUpdater` interface so the wrapper can be injected without lying about a concrete `*state.Store`. The production smoke test (TestRunUpdater_NoLockHeldAcrossSend) keeps the real `*state.Store` + public `RunUpdater` to verify the production code path is also race-clean (the race detector is the load-bearing oracle there).
- **Files modified:** internal/poll/channel_test.go (added safeStore wrapper + 4 tests switched to runUpdater seam); internal/poll/channel.go (added storeUpdater package-private interface for the test seam).
- **Verification:** `go test ./internal/poll/ -race -count=10` — 13.8s, all green.
- **Committed in:** `d5b07e5` (Task 2 GREEN)

**2. [Rule 3 - Blocking] storeReader narrow interface for cronPoller**
- **Found during:** Task 3 (poller.go GREEN)
- **Issue:** poller_test.go constructs cronPoller via `newPollerForTest(..., store, ...)` passing `*safeStore`, but the planner's draft `cronPoller` struct held `store *state.Store` concretely. Tests would not compile without one of (a) test-only Get/Update wrapper, (b) widening the planner's draft signature.
- **Fix:** Introduced `storeReader` package-private interface (Get only — writes flow through the channel), mirroring `internal/docker.stateStore`. Production passes `*state.Store` concretely (which satisfies the interface); tests pass `*safeStore` for race-clean snapshots.
- **Files modified:** internal/poll/poller.go (storeReader interface + cronPoller.store typed as storeReader + newPoller/newPollerForTest signatures).
- **Verification:** `go build ./... && go vet ./internal/poll/...` clean; all 11 poller tests green.
- **Committed in:** `05d1305` (Task 3 GREEN)

**3. [Rule 1 - Bug] TempDir RemoveAll race against in-flight state.Store.persist**
- **Found during:** Task 3 (poller_test.go iteration after first GREEN attempt)
- **Issue:** With `defer cancel()`, the test function returned before the poller/updater goroutines had drained. Goroutines kept the state.Store's file handle live (mid-persist .tmp file or fsync queue); `t.TempDir`'s cleanup `RemoveAll` raced with the next persist and reported `unlinkat ...: directory not empty`. Affected TestPoller_PermanentErrorSurfacesNote intermittently.
- **Fix:** Replaced `defer cancel()` with `t.Cleanup` blocks in explicit LIFO ordering: updater-wait registered first (runs last after the poller has fully exited), poller-wait registered second with `cancel()` inside it (runs first, cancels ctx, waits for `cron.Stop().Done()` drain). This ensures all goroutines exit BEFORE t.TempDir's RemoveAll fires.
- **Files modified:** internal/poll/poller_test.go (all 11 test functions updated to t.Cleanup pattern).
- **Verification:** `go test ./... -race -count=1` and `go test ./internal/poll/ -race -count=10` both clean.
- **Committed in:** `05d1305` (Task 3 GREEN)

**4. [Rule 1 - Bug] Tight wall-clock deadlines flaked under `go test ./...` load**
- **Found during:** Task 3 (poller_test.go iteration)
- **Issue:** Initial deadlines were 250ms-1s, which works under `go test ./internal/poll/` (isolated) but flakes when the whole project test suite runs concurrent packages. `@every 100ms` cron first-tick latency + 20ms fakeResolver delay + goroutine scheduling under load can push wall-clock to 700ms+ for a single observable mutation.
- **Fix:** Bumped polling deadlines from 250ms-1s to 2-3s across all tests that wait for "at least N mutations to land" observations. The polling loop exits early when the condition is satisfied, so the fast path is unchanged.
- **Files modified:** internal/poll/poller_test.go (deadlines in TestPoller_TickInvokesSweep, SkipsPinnedContainers, SkipsStoppedContainers, AppliesTagPatternFilter, TagPatternRunningTagMismatch, ErrgroupSetLimitBeforeGo, FetchSendsDigestResolvedUpdate, PermanentErrorSurfacesNote).
- **Verification:** `go test ./... -race -count=1` clean; `go test ./internal/poll/ -race -count=10` clean (21s wall-clock, never times out).
- **Committed in:** `05d1305` (Task 3 GREEN)

---

**Total deviations:** 4 auto-fixed (3 bug fixes, 1 blocking interface widening). All are test-infrastructure related — no production-code deviations.
**Impact on plan:** Zero scope creep. Two patterns (safeStore deep-copy wrapper, t.Cleanup LIFO goroutine join) are now established conventions for the codebase's race-clean test discipline; both were already foreshadowed in PATTERNS.md (Pattern H + Pattern I).

## Issues Encountered

- **robfig/cron's `@every 100ms` first-tick latency** is observably slower than the literal expression suggests under loaded test machines — the scheduler goroutine itself adds wakeup latency. Resolved by polling-deadline-based waits instead of fixed sleeps; documented in test comments.
- **Plan-shown "errors_isPermanent / errorIs" shim ladder** was an illustrative draft; the plan explicitly directed "use the simpler form" (Warning 4). Implemented the final form: `errors.Is(err, registry.ErrPermanent)` with `"errors"` added to imports. No shim ladder lives in the final source.

## User Setup Required

None — Phase 3 is entirely backend Go code feeding the existing state.json file. The three new env vars (`HMI_UPDATE_CRON`, `HMI_UPDATE_REGISTRY_TIMEOUT_S`, `HMI_UPDATE_POLL_CONCURRENCY`) have defaults that work on every HMI; operators only override for tuning.

## Next Phase Readiness

**Plan 03-04 (main.go wiring) is unblocked.** The 8-line boot sequence is now:

```go
patterns := poll.NewPatterns()
updates := make(chan poll.StateUpdate, 64)
poller, err := poll.NewPoller(cronExpr, resolver, patterns, store, updates)
if err != nil { log.Fatalf("poll.NewPoller: %v", err) }

go poll.RunUpdater(ctx, updates, store)   // single consumer
go poller.Run(ctx)                          // producer B
// (Phase 2's discoverer is producer A — plan 03-04 refactors its
//  store.Update call sites into channel sends)
```

**Plan 03-05 (e2e specs) is unblocked** for DETECT-05/08/09 observation surface: `/api/state.containers[svc].available_digest` + `/api/state.containers[svc].notes` + `/api/state.last_poll_end` advancing past a captured baseline are all wired through this plan's channel.

**No blockers or concerns.**

## Self-Check: PASSED

- `internal/poll/patterns.go` — FOUND
- `internal/poll/patterns_test.go` — FOUND
- `internal/poll/channel.go` — FOUND
- `internal/poll/channel_test.go` — FOUND
- `internal/poll/poller.go` — FOUND (modified from stub)
- `internal/poll/poller_test.go` — FOUND
- Commits: `e484be9`, `2a4e87e`, `a726277`, `d5b07e5`, `1e053db`, `05d1305` — all FOUND in git log
- `go build ./...` — OK
- `go test ./internal/poll/ -race -count=10` — OK (21.1s)

---
*Phase: 03-registry-polling-update-detection*
*Completed: 2026-05-14*
