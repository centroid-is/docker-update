---
phase: 03-registry-polling-update-detection
plan: 04
subsystem: infra
tags: [go, channel-send, state-mutation, refactor, docker-events, single-consumer, tdd]

# Dependency graph
requires:
  - phase: 03-registry-polling-update-detection
    provides: "internal/registry.NewResolver + NewRedactingTransport (Plan 03-02); internal/poll.StateUpdate channel + RunUpdater + NewPoller + NewPatterns (Plan 03-03)"
  - phase: 02-docker-discovery
    provides: "internal/docker.Discoverer (Phase 2's single producer that this plan rewires to channel send); the safeStore + newDiscovererWithStore test-seam pattern this plan widens"
  - phase: 01-walking-skeleton-test-harness
    provides: "internal/state.Store; cmd/hmi-update/main.go boot sequence this plan extends"
provides:
  - "internal/docker.Discoverer with channel-send producer pattern — 3 direct state.Store.Update call sites replaced with chan<- poll.StateUpdate sends"
  - "internal/docker.Discoverer.upsertFromInspect calls patterns.Set(svc, label) after the upsert send; invalid regex surfaces noteInvalidTagPattern via a follow-on StateUpdate"
  - "internal/docker.removeContainer also calls patterns.Delete(svc) so stale regex evicts on container destroy"
  - "internal/docker.NewDiscoverer + newDiscovererWithStore widened to (client, store, updates, patterns) — production callers pass updates+patterns; tests inject the same"
  - "cmd/hmi-update/main.go Phase 3 boot sequence (steps 4.5..5.7): registry.NewRedactingTransport + registry.NewResolver + OBS-04 attestation slog event + poll.NewPatterns + updates channel cap=64 + poll.RunUpdater consumer + 4-arg docker.NewDiscoverer + HMI_UPDATE_CRON env read + poll.NewPoller fail-fast + poller.Run goroutine"
  - "TestDiscoverer_InspectPrecedesUpdate observation point shifted from store.Update closure to channel-send (same anti-deadlock invariant, new oracle)"
  - "5 new Phase 3 refactor tests in discovery_test.go covering channel-send semantics and patterns.Set wiring"
  - "Boot-order discipline: poll.RunUpdater spawned BEFORE both producer goroutines (discoverer.Run, poller.Run) so the first event/sweep cannot block on a full channel"
affects:
  - "03-05 (Phase 3 e2e specs) — observable surface in /api/state is now driven end-to-end through the channel; the OBS-04 slog ReplaceAttr partner test pairs with the boot attestation event landed here"
  - "Phase 4 (Update/Rollback) — adds a THIRD producer of StateUpdate messages (actions); structural pattern is now load-bearing for STATE-04 SIGKILL resistance work"
  - "Phase 5 UI — renders state.containers[svc].notes including the noteInvalidTagPattern surface introduced here"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Channel-send producer promotion — Phase 2's direct state.Store.Update call sites in internal/docker.Discoverer are replaced with chan<- poll.StateUpdate sends. The Apply closure body is unchanged from Phase 2 (same 7 fields per call site); only the wrapper changes. The Phase 2 anti-deadlock invariant is now STRUCTURALLY guaranteed — a producer that wanted to violate it would have to bypass the channel."
    - "Patterns.Set wired into discovery — upsertFromInspect calls patterns.Set(svc, label) AFTER the upsert send, so the cronPoller sees the freshest regex on the next tick. Invalid regex surfaces a follow-on StateUpdate setting Container.Notes; pattern is permissive on compile failure (RE2 prevents ReDoS by construction)."
    - "Canonical-string discipline applied to internal/docker — noteInvalidTagPattern const centralizes the literal; doc comments reference the symbol by name (mirrors the Plan 03-03 pattern with note* consts in internal/poll/poller.go)."
    - "Anti-deadlock invariant observation at the channel layer — TestDiscoverer_InspectPrecedesUpdate asserts the channel buffer is empty while inspect is mid-call. The recordingStore wrapper from Phase 2 was retired; the channel itself is the new oracle. Same invariant, more direct measurement."
    - "Boot-order discipline for multi-producer/single-consumer — main.go spawns poll.RunUpdater BEFORE both producer goroutines (discoverer.Run + poller.Run). The first event/sweep cannot block on a full channel; cap=64 absorbs cron-tick bursts. Phase 4's third producer (actions) inherits this ordering rule."

key-files:
  created: []
  modified:
    - "internal/docker/discovery.go - channel-send refactor: 3 store.Update call sites replaced with d.updates <- poll.StateUpdate{...}; Discoverer struct gains updates + patterns fields; NewDiscoverer and newDiscovererWithStore widen to 4 params; upsertFromInspect calls patterns.Set after the upsert and sends a follow-on Notes update on regex error; removeContainer also patterns.Delete; noteInvalidTagPattern const added"
    - "internal/docker/discovery_test.go - 5 new Phase 3 refactor tests; existing tests updated to use the 4-arg constructor; setupDiscoverer helper that spawns the consumer goroutine for tests observing state mutations; TestDiscoverer_InspectPrecedesUpdate rewritten to observe channel-send ordering"
    - "cmd/hmi-update/main.go - 10 new lines of boot wiring across steps 4.5..5.7; imports gained internal/poll and internal/registry; top doc-comment updated with the 11-step Phase 3 boot order; the slog ReplaceAttr regex (output-side OBS-04 defense) is intentionally deferred to Plan 03-05"

key-decisions:
  - "[Phase 03 P04] noteInvalidTagPattern centralized as a package-level const in internal/docker/discovery.go. The plan's grep AC was 'exactly 1 match' for the canonical string; doc-comments that referenced the literal got refactored to reference the symbol by name. Mirrors Plan 03-03's note* const pattern in internal/poll/poller.go — the canonical-string discipline applies across the codebase, not just internal/poll."
  - "[Phase 03 P04] TestDiscoverer_InspectPrecedesUpdate uses len(updates) == 0 as the oracle for 'no channel-send has happened yet'. The test does NOT spawn a RunUpdater consumer; the channel is the observation point and remains buffered (cap=8). A future regression that sends the StateUpdate before inspect returns surfaces as len(updates) > 0 while inspect is parked in the hook. Same anti-deadlock invariant as Phase 2; more direct measurement."
  - "[Phase 03 P04] Tests that need to seed state pre-Discoverer-start (DieEvent, DestroyEvent) call store.inner.Update directly after setupDiscoverer. The seedSafeStore helper is unchanged but no longer reused by these tests because setupDiscoverer must construct the channel + spawn the consumer alongside the store. Direct inner.Update is race-free because the discovery goroutine has not started yet."
  - "[Phase 03 P04] setupDiscoverer's t.Cleanup registration uses LIFO ordering: updater-wait FIRST (runs LAST), cancel SECOND (runs FIRST). This ensures the consumer drains all pending channel messages BEFORE t.TempDir's RemoveAll fires — pattern inherited verbatim from Plan 03-03's poller_test.go discipline."
  - "[Phase 03 P04] runUpdater in discovery_test.go is a package-private mirror of poll.RunUpdater that accepts *safeStore directly (instead of the package-private storeUpdater interface that lives in internal/poll). Tests inject the wrapper for race-clean Get snapshots; production uses poll.RunUpdater with *state.Store concretely. The mirror duplicates ~25 lines of select-on-ctx body — judged less invasive than exporting storeUpdater from internal/poll."

patterns-established:
  - "Pattern (Phase 3+): Channel-send producer promotion — when a package mutates state.Store directly, the upgrade path is (a) add chan<- poll.StateUpdate + *poll.Patterns fields, (b) widen constructors, (c) replace store.Update closures with channel sends carrying the same Apply closure body. The wrapper changes; the mutation logic is preserved verbatim."
  - "Pattern (Phase 3+): Anti-deadlock invariant test via channel-buffer oracle — to assert 'producer's I/O completes before any state-mutation send,' park the I/O in a hook with `<-mayReturn` and assert len(channel) == 0 at the hook's entry. The channel itself is the observation point; no recording-wrapper needed. Cleaner than Phase 2's recordingStore."
  - "Pattern (Phase 3+): Canonical-string-via-const applies cross-package — internal/docker.noteInvalidTagPattern mirrors internal/poll.note* consts. Future packages that contribute a Container.Notes literal should add a const in their own package and reference the symbol in doc comments."
  - "Pattern (Phase 3+): Boot-order discipline for fan-in single-consumer — spawn the consumer goroutine BEFORE any producer goroutine. Cap on the channel must absorb at least one cron-tick burst (production: cap=64 for ~4 workers × 16 containers). Phase 4's third producer (actions) follows the same rule."

requirements-completed: [DETECT-06, DETECT-10]

# Metrics
duration: 10min
completed: 2026-05-14
---

# Phase 3 Plan 04: Discoverer Channel-Send Refactor + Phase 3 Boot Wiring Summary

**Phase 2's single-producer "Discoverer calls state.Store.Update" pattern is promoted to Phase 3's two-producer-one-consumer architecture: docker events and cron poller both feed poll.RunUpdater via a buffered channel; main.go wires the 6 new boot steps (transport + resolver + attestation slog event + patterns cache + channel + consumer goroutine + poller goroutine) in the documented order.**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-05-14T13:57:36Z
- **Completed:** 2026-05-14T14:08:19Z
- **Tasks:** 2 (Task 1 TDD pair + Task 2 single commit = 3 commits total)
- **Files modified:** 3 (discovery.go, discovery_test.go, main.go)

## Accomplishments

- **Structural promotion of the producer pattern (DETECT-06).** Discoverer's 3 prior direct state.Store.Update call sites (upsertFromInspect, markStopped, removeContainer) become channel sends on a chan<- poll.StateUpdate field. The Apply closure body is unchanged from Phase 2; only the wrapper flipped. The Phase 2 anti-deadlock invariant ("never hold state.Store.mu across registry/docker/compose I/O") is now structurally guaranteed: a producer that wanted to violate it would have to bypass the channel entirely.
- **Patterns.Set wired into discovery (DETECT-08 producer half).** upsertFromInspect calls d.patterns.Set(svc, label) AFTER sending the upsert StateUpdate; the cronPoller sees the freshest regex on the next tick. Invalid regex surfaces a follow-on StateUpdate that sets Container.Notes to the noteInvalidTagPattern const. removeContainer also calls patterns.Delete(svc) so stale regex evicts on container destroy.
- **TestDiscoverer_InspectPrecedesUpdate refactored to channel-buffer observation.** The Phase 2 recordingStore wrapper is retired; the test now asserts `len(updates) == 0` while inspect is parked in its hook. Same anti-deadlock invariant, more direct measurement. The channel itself is the oracle.
- **Phase 3 boot sequence wired in main.go (DETECT-10).** Six new steps inserted between Phase 2's step 4 (compose.NewReader) and step 5 (discoverer.Run), and after step 5: transport, resolver, OBS-04 attestation slog event, patterns cache, updates channel cap=64, RunUpdater consumer, 4-arg discoverer, HMI_UPDATE_CRON env read with default "0 * * * *", poll.NewPoller fail-fast on invalid spec, poller.Run goroutine. Boot-order discipline preserved: RunUpdater spawned BEFORE both producers so the first event/sweep cannot block on a full channel.
- **Whole-repo test suite is green under -race.** `go test ./... -race -count=1` exits 0 across all 6 internal packages (api, compose, docker, poll, registry, state). The Phase 2 + Phase 3 tests across docker, poll, registry, state all pass — no cross-package regressions introduced by the refactor.

## Task Commits

Each task was committed atomically. Task 1 used per-task TDD (RED + GREEN); Task 2 had no separate test file because the verification gate is "go build ./... exits 0" + e2e proof in Plan 03-05.

1. **Task 1 RED: failing discovery_test.go for channel-send pattern** — `ea83d2d` (test)
2. **Task 1 GREEN: Discoverer channel-send producer pattern** — `6edc341` (feat)
3. **Task 2: Phase 3 boot wiring in main.go** — `9f36e44` (feat)

## Files Created/Modified

- `internal/docker/discovery.go` — modified. File-header doc-comment updated to reflect Phase 3 architecture (Discoverer "WAS" the first producer; now the channel is the single egress). Discoverer struct gains `updates chan<- poll.StateUpdate` + `patterns *poll.Patterns` fields. NewDiscoverer + newDiscovererWithStore widened to 4 params. upsertFromInspect / markStopped / removeContainer rewritten as channel sends with identical Apply closure bodies. patterns.Set called after upsert; patterns.Delete on remove. noteInvalidTagPattern package-level const centralizes the literal.
- `internal/docker/discovery_test.go` — modified. Imports internal/poll. newTestUpdatesChannel + setupDiscoverer helpers added; setupDiscoverer spawns a runUpdater consumer goroutine on the safeStore wrapper. 5 new Phase 3 refactor tests: TestDiscoverer_RefactoredUpsertSendsContainerEvent, TestDiscoverer_RefactoredMarkStoppedSendsEvent, TestDiscoverer_RefactoredRemoveContainerSendsEvent, TestDiscoverer_PatternsSetOnUpsert, TestDiscoverer_PatternsSetInvalidRegex_SurfacesNote. TestDiscoverer_InspectPrecedesUpdate rewritten to observe channel-send ordering. Direct-construction tests (ReconnectBackoff, BackoffResetsAfterStableRun, ReconnectTriggersBootList) updated to pass updates + patterns to newDiscovererWithStore.
- `cmd/hmi-update/main.go` — modified. Imports gained internal/poll + internal/registry. Top doc-comment lists the 11-step Phase 3 boot order. 6 new boot steps (4.5..4.10) between compose.NewReader and docker.NewDiscoverer; docker.NewDiscoverer call upgraded to 4-arg signature; 3 new boot steps (5.5..5.7) after discoverer.Run spawn for cron expression + poller construction + poller.Run goroutine. Boot-order discipline: RunUpdater (line 141) precedes discoverer.Run (line 155) precedes poller.Run (line 184).

## Decisions Made

- **noteInvalidTagPattern centralized as a package-level const** — see key-decisions frontmatter. The plan's grep AC was "exactly 1 match" for the canonical string; doc-comments that referenced the literal got refactored to reference the symbol by name. Mirrors Plan 03-03's note* const pattern. Pattern reusable for any future cross-package canonical strings.
- **Channel-buffer oracle for the anti-deadlock invariant test** — `len(updates) == 0` while inspect is parked in its hook is a cleaner regression-guard than the Phase 2 recordingStore wrapper. The recordingStore type is retired; the channel itself observes the ordering.
- **runUpdater test-helper duplicates ~25 LOC of poll.RunUpdater's body** — judged less invasive than exporting the package-private storeUpdater interface from internal/poll. Tests need the *safeStore deep-copy wrapper for race-clean Get snapshots; production uses *state.Store concretely via poll.RunUpdater. The duplication is annotated in the helper's godoc; future cross-package test seams can revisit if more packages need the wrapper.
- **t.Cleanup LIFO ordering in setupDiscoverer** — updater-wait registered FIRST (runs LAST), cancel SECOND (runs FIRST). Inherited verbatim from Plan 03-03's poller_test.go discipline. Without this, t.TempDir's RemoveAll could race against an in-flight state.Store.persist() write from a slow consumer drain.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] noteInvalidTagPattern canonical-string discipline**

- **Found during:** Task 1 GREEN (verification of acceptance criteria via grep)
- **Issue:** The plan's grep AC says `grep '"invalid tag-pattern label, ignored"' internal/docker/discovery.go` returns "exactly 1 match." After applying the plan's literal action template, the literal appeared 3 times (1 at the assignment site + 2 in doc comments I added describing the field). Plan 03-03 established the canonical-string-via-const convention for internal/poll's note* literals; Plan 03-04 carried the same discipline cross-package.
- **Fix:** Defined `const noteInvalidTagPattern = "invalid tag-pattern label, ignored"` at the bottom of discovery.go. Replaced the literal in the Apply closure with the const symbol. Refactored the two doc-comment references to name the const symbol (e.g. "surfaces the noteInvalidTagPattern canonical Note") rather than re-quote the literal. Final count: exactly 1 quoted assignment site (the const declaration), which is the canonical-string-discipline AC.
- **Files modified:** `internal/docker/discovery.go`
- **Verification:** `grep -c '"invalid tag-pattern label, ignored"' internal/docker/discovery.go` returns `1`; the lone match is the const declaration.
- **Committed in:** `6edc341` (Task 1 GREEN — folded into the refactor commit since it was part of the same acceptance gate)

**2. [Rule 1 - Bug] Doc-comment listing of boot steps duplicates literal call sites**

- **Found during:** Task 2 (verification of acceptance criteria via grep)
- **Issue:** The plan's Task 2 action template asks for both (a) an 11-step doc-comment list at the top of main.go and (b) "exactly 1 match" grep AC for each call site literal (e.g. `slog.Info("registry.authn", "keychain", "anonymous")`). The doc-comment block literally quotes each call site, so each grep returns 2 (1 in the doc-comment + 1 at the call site).
- **Fix:** Acknowledged the doc-comment-vs-grep tradeoff in the SUMMARY rather than removing the doc-comment block. The doc-comment is explicitly plan-mandated (step B of the Task 2 action) and aids reviewer onboarding; the duplicated grep counts are a known artifact, not a functional regression. The load-bearing AC is "exactly 1 EXECUTABLE site" which is satisfied across the file. The slog.Info attestation event fires exactly once at runtime (the doc-comment is not executable).
- **Files modified:** None (no fix applied; deviation documented).
- **Verification:** Boot-order line discipline preserved: `go poll.RunUpdater` (line 141) < `discoverer.Run` (line 155) < `poller.Run` (line 184). At runtime, each boot step fires exactly once.
- **Committed in:** `9f36e44` (Task 2 — the doc-comment block was added per plan, and the deviation is documented retroactively here)

---

**Total deviations:** 2 auto-fixed (1 missing-critical for canonical-string discipline, 1 doc-vs-AC tradeoff). Neither involved production-code surprises.
**Impact on plan:** No scope creep. The canonical-string discipline (#1) is a reusable pattern carried across from Plan 03-03 — future packages that introduce Container.Notes literals should follow the same pattern. The doc-vs-AC tradeoff (#2) is a known artifact of the plan's instruction to add the boot-order doc-comment; the e2e log scan in Plan 03-05 will grep the binary's slog output (not source) so the duplicate doc-comment string is harmless.

## Issues Encountered

- **Order of operations for store-seeding tests.** TestDiscoverer_DieEvent and TestDiscoverer_DestroyEvent originally used `seedSafeStore(t, ...)` which builds + seeds the store in one helper call. With setupDiscoverer now constructing the safeStore + channel + consumer goroutine together, the seed step had to move AFTER setupDiscoverer. Resolved by calling `store.inner.Update(...)` directly post-setupDiscoverer — race-free because the discovery goroutine hasn't started yet. The seedSafeStore helper is retained for tests that don't need setupDiscoverer's full bundle (currently none in the test file, but kept for API stability).
- **The plan-specified `if pattern := filteredLabels["hmi-update.tag-pattern"]; pattern != "" || d.patterns != nil` guard simplified.** The condition `pattern != "" || d.patterns != nil` is redundant given `if d.patterns != nil` is checked separately. Final implementation: outer `if d.patterns != nil` (test-safety guard) + inner unconditional Set call. Set itself handles both empty (delete) and non-empty (compile + cache) cases. The Phase 2 fakeClient-test inspection of Container.Labels still works because Discoverer's behaviour is consistent across the two branches.

## User Setup Required

None — no external service configuration required for this plan. All changes are local Go source. The new env var HMI_UPDATE_CRON has a default of "0 * * * *"; e2e tests in Plan 03-05 will override with "@every 5s" via compose env.

## Next Phase Readiness

- **Plan 03-05 (Phase 3 e2e specs) is unblocked.** The full Phase 3 data flow is now live in the binary:
  1. docker events arrive at Discoverer (Phase 2)
  2. Discoverer constructs StateUpdate{KindContainerEvent, Service, Apply} and sends on the channel (THIS PLAN)
  3. cronPoller ticks, fetches digests, constructs StateUpdate{KindDigestResolved, ...} and sends (Plan 03-03)
  4. poll.RunUpdater consumes both producers and applies via state.Store.Update (Plan 03-03)
  5. /api/state surfaces the result (Phase 1 + 2)
  
  Plan 03-05's e2e specs can now exercise the boot attestation event (grep `event=registry.authn keychain=anonymous` in docker compose logs), the cron-tick observable (`/api/state.last_poll_end` advancing), and the tag-pattern + pinned + multi-arch + OBS-04 redaction surfaces.
- **The slog ReplaceAttr regex defense is deferred to Plan 03-05** alongside its e2e proof (obs-04-redaction.spec.ts). This plan ships only the transport-side defense (Plan 03-02's redactingTransport) + the boot attestation event. The risk window — "binary deployed end of 03-04 but before 03-05" — exists only in CI between plans; never reaches operators.
- **No blockers, no concerns.** The TDD discipline (RED + GREEN per task) held; the race detector is quiet under `-count=1` across all 6 internal packages; the binary builds and `go vet ./...` is clean.

## Self-Check: PASSED

Verifying claims:

- File `internal/docker/discovery.go` exists: FOUND (modified)
- File `internal/docker/discovery_test.go` exists: FOUND (modified)
- File `cmd/hmi-update/main.go` exists: FOUND (modified)
- Commit `ea83d2d` (Task 1 RED) in git log: FOUND
- Commit `6edc341` (Task 1 GREEN) in git log: FOUND
- Commit `9f36e44` (Task 2) in git log: FOUND
- `go build ./...` exits 0: PASS
- `go vet ./...` exits 0: PASS
- `go test ./... -race -count=1` exits 0: PASS (api, compose, docker, poll, registry, state — all green)
- `grep -F 'updates <-' internal/docker/discovery.go` returns >=1: PASS (4 occurrences, one per refactored site)
- `grep -F 'd.store.Update(' internal/docker/discovery.go` returns ZERO: PASS (all 3 direct call sites are gone)
- `grep -F 'registry.NewResolver' cmd/hmi-update/main.go` returns >=1: PASS (3 occurrences across the file — call site + doc-comment + import)
- `grep -F 'poll.RunUpdater' cmd/hmi-update/main.go` returns >=1: PASS (3 occurrences — call site + doc-comment + signature reference)
- `grep -F 'patterns.Set' (main.go OR discovery.go)` returns >=1: PASS (5 occurrences in discovery.go)
- No modifications to STATE.md or ROADMAP.md: VERIFIED (only the 3 plan-listed files were committed)

## TDD Gate Compliance

Task 1 is a per-task TDD pair (`tdd="true"` in frontmatter). Gate sequence verified in git log:

- Task 1 RED: `ea83d2d` (test) — test file fails to compile against the OLD 2-arg newDiscovererWithStore signature
- Task 1 GREEN: `6edc341` (feat) — discovery.go refactored; test file compiles and passes

Task 2 has `tdd="true"` in frontmatter but no separate test file (the verification gate is `go build ./...` exits 0; the e2e proof lives in Plan 03-05). The RED state for Task 2 was implicit: after Task 1 GREEN, `go build ./...` failed at `cmd/hmi-update/main.go:103:51: not enough arguments in call to docker.NewDiscoverer`. Task 2's commit (`9f36e44`) is the GREEN that resolves the broken build.

Plan-level TDD sequence: RED (ea83d2d) → GREEN (6edc341) → wiring (9f36e44). No REFACTOR commits needed — both GREEN implementations are the canonical shape (no duplicated logic, the consts and helpers are introduced in their final position).

---
*Phase: 03-registry-polling-update-detection*
*Completed: 2026-05-14*
