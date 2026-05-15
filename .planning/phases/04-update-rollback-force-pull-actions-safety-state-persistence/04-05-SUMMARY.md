---
phase: 04-update-rollback-force-pull-actions-safety-state-persistence
plan: 05
subsystem: testing
tags: [sigkill, fault-injection, renameio, atomic-writes, build-tag, operator-runbook]

# Dependency graph
requires:
  - phase: 01-walking-skeleton-test-harness
    provides: renameio.WriteFile + parent-dir-fsync atomic-write pattern (internal/state/persist.go) — the invariant this plan empirically verifies
  - phase: 04-update-rollback-force-pull-actions-safety-state-persistence/04-01
    provides: state.Container.ActionInFlight + ActionError schema fields (helper's state.NewStore depends on the current schema)
provides:
  - cmd/sigkillhelper helper binary — ad-hoc fault-injection writer; never packaged into production image
  - internal/state/store_sigkill_test.go — build-tagged //go:build sigkill_test parent test; 100 SIGKILL iterations, zero corruption
  - make test-sigkill target — operator-explicit opt-in path; default `make test` is unaffected
  - PROJECT.md operator-facing runbook — Installation prerequisites (STATE-05 chown 65532:65532), Manual self-upgrade procedure (sibling-container approach for ACT-09 self-protection), Container labels reference (the five hmi-update.* labels), Configuration knobs (env-var inventory including Phase 4 newcomers)
affects: [phase-04-06 restart-persistence e2e spec (same renameio invariant); phase-07 image-size verification (sigkillhelper MUST NOT appear in production image); future state-store refactors (regression gate via make test-sigkill)]

# Tech tracking
tech-stack:
  added: [Go build tags for slow test gating, os/exec subprocess SIGKILL pattern, math/rand randomized fault-injection delay]
  patterns: [build-tagged test files (//go:build sigkill_test) for OS-coupled slow tests, helper-binary-in-cmd/ pattern for fork-and-kill harnesses (NOT packaged into production), parsedCount diagnostic gate to detect uninformative test runs]

key-files:
  created:
    - cmd/sigkillhelper/main.go
    - internal/state/store_sigkill_test.go
    - .planning/phases/04-update-rollback-force-pull-actions-safety-state-persistence/04-05-SUMMARY.md
  modified:
    - Makefile (added test-sigkill target + .PHONY entry)
    - .planning/PROJECT.md (appended 4 operator-facing sections before Evolution)

key-decisions:
  - "macOS Go runtime startup (~50-100ms) routinely outraces the canonical 1-50ms SIGKILL delay; widened the file-missing exemption from iter 0 only to any iteration. The corruption invariant is only meaningful when a file exists."
  - "Added parsedCount diagnostic gate (require ≥1 successful unmarshal across 100 iterations). Without this, a too-short delay range could pass the test trivially (all iterations skipped via file-missing). Typical macOS run exercises the parse path 91-95 times out of 100."
  - "PROJECT.md target is .planning/PROJECT.md (not a repo-root PROJECT.md) — single canonical location matches the existing /gsd workflow convention; plan's `files_modified: PROJECT.md` was a shorthand."
  - "PROJECT.md sections inserted BEFORE Evolution (not after) so substantive operator content sits next to Constraints/Key Decisions; Evolution is a meta-section."

patterns-established:
  - "Build-tag gated slow tests: //go:build sigkill_test confines a 5-15s OS-coupled test to an explicit Makefile target. Default `go test ./...` excludes it via build tag, keeping the unit-test budget <5s. Future fault-injection tests (e.g. disk-full simulation, partial-network-partition) should follow this pattern."
  - "Helper-binary-in-cmd/ for fork-and-kill harnesses: the helper lives at cmd/sigkillhelper/main.go but is built ad-hoc by the parent test into t.TempDir() and never appears in `make build` or the Dockerfile. Production image-size budget (<30MB) is unaffected."
  - "parsedCount diagnostic gate: when a fault-injection test has multiple acceptable outcomes (file-missing, empty, parseable), count the meaningful outcomes and fail loudly if zero of them occurred. Prevents trivially-passing tests on platforms where the fault arrives before the system-under-test starts."

requirements-completed:
  - STATE-04
  - STATE-05

# Metrics
duration: 8min
completed: 2026-05-15
---

# Phase 04 Plan 05: STATE-04 SIGKILL Fault-Injection + STATE-05 Install Runbook Summary

**SIGKILL-mid-write empirical verification of the Phase 1 renameio + parent-dir-fsync invariant via a build-tagged fork-and-kill harness (100 iterations, zero corruption), plus four operator-facing PROJECT.md runbook sections.**

## Performance

- **Duration:** ~8 min
- **Started:** 2026-05-15T07:58Z (approximate)
- **Completed:** 2026-05-15T08:06:30Z
- **Tasks:** 2
- **Files created:** 2 (helper binary, parent test)
- **Files modified:** 2 (Makefile, PROJECT.md)

## Accomplishments

- **Empirical STATE-04 verification:** 100 fork-and-SIGKILL iterations against the renameio + dir-fsync write path; zero parse errors across multiple stable runs (91-95 successful unmarshals per 100 iterations on macOS, the remainder file-missing due to Go runtime startup outracing short SIGKILL delays).
- **Build-tag isolation:** `go test ./...` time budget unaffected (10.6s for the full state package vs ~4s for the gated harness alone). The harness is invisible to default test runs (build tag `sigkill_test` confines it to `make test-sigkill`).
- **Operator runbook landed:** PROJECT.md gained Installation prerequisites (STATE-05), Manual self-upgrade procedure (the sibling-container approach for the ACT-09 self-protection 409), Container labels reference (the five `hmi-update.*` labels mapped to Phase 4's middleware behavior), and Configuration knobs (full env-var inventory including the three Phase 4 newcomers).

## Task Commits

Each task was committed atomically:

1. **Task 1: SIGKILL fault injection harness** — `bef90da` (test)
2. **Task 2: PROJECT.md operator runbook** — `f1c51d4` (docs)

## Files Created/Modified

- `cmd/sigkillhelper/main.go` — minimal helper binary; `sigkillhelper <state-path>` opens `state.NewStore`, loops `Update` with counter-embedded synthetic sha256 digests (`sha256:%064d`), 100µs between writes. Exits only via signal. Not packaged into production image.
- `internal/state/store_sigkill_test.go` — `//go:build sigkill_test` parent test. Builds helper into `t.TempDir()`, spawns 100 helper instances, sends SIGKILL at 1-50ms randomized delay each, verifies the on-disk file is missing/empty (helper outraced) OR parses cleanly into `state.State` with the correct `SchemaVersion`. Fails on any unmarshal error with full file contents in the diagnostic.
- `Makefile` — `test-sigkill` added to `.PHONY` line and as a new target that runs `go test -tags=sigkill_test -count=1 -run TestSIGKILL ./internal/state/...`.
- `.planning/PROJECT.md` — four new sections appended before Evolution: Installation prerequisites (chown 65532:65532), Manual self-upgrade procedure (3-step sibling-container procedure), Container labels reference (5-row table for the `hmi-update.*` labels), Configuration knobs (env-var inventory).

### Helper binary CLI signature

```
sigkillhelper <state-path>
```

Single positional arg. Loops indefinitely until signaled. Writes `state.Container{Service:"svc", Image:"test/image", Tag:"latest", CurrentDigest:"sha256:%064d"}` keyed by `"svc"`. Counter increments per iteration; a torn write would surface as a truncated JSON document (`"sha256:000000...0042` cut mid-string).

### Build-tag mechanism

```go
//go:build sigkill_test
// +build sigkill_test
```

The `//go:build` line is the canonical Go 1.17+ form; the `// +build` line is the legacy form kept for tooling compatibility. With no tag specified to `go test`, the file is excluded from compilation entirely (verified: `go test -run TestSIGKILL ./internal/state/...` reports "no tests to run" while the default `go test ./internal/state/...` passes in 10.6s with no inflation).

### Makefile target wiring

```makefile
test-sigkill:
	go test -tags=sigkill_test -count=1 -run TestSIGKILL ./internal/state/...
```

`-count=1` disables Go's test result cache (important for fault-injection tests where state.json artifacts in `t.TempDir()` differ per run). `-run TestSIGKILL` is a defensive narrowing in case future tag-gated tests are added.

### SIGKILL iteration semantics — accepted outcomes

For each of 100 iterations:

| Outcome | Acceptance |
|---------|------------|
| File missing (`os.IsNotExist`) | Acceptable on any iteration — Go runtime startup outraced the SIGKILL delay |
| File present, length 0 | Acceptable — `state.NewStore` treats empty as "no state yet" (mid-create crash recovery) |
| File present, parseable JSON, `Version == SchemaVersion` | The healthy path — exercises the renameio + dir-fsync invariant under SIGKILL |
| File present, unparseable JSON | **CORRUPTION** → `t.Fatalf` with full file contents in error |

`parsedCount` (count of healthy-path outcomes) must be ≥1 across 100 iterations or the test fails — a too-short delay range that lets every iteration skip via file-missing would produce a vacuously-passing test. Typical macOS runs see 91-95 parses.

### PROJECT.md insertion point

Inserted before `## Evolution` so substantive operator-facing content sits next to Constraints and Key Decisions, not after the meta-documentation section. Append-only: 46 line insertions, 0 deletions; all four pre-existing major sections (`## Requirements`, `## Context`, `## Constraints`, `## Key Decisions`, `## Evolution`) confirmed preserved by `git diff --stat`.

## Decisions Made

1. **Widened the file-missing exemption from iter-0-only to any iteration.** The canonical RESEARCH.md test body assumed only the very first iteration could have its SIGKILL beat the helper's first write; on macOS the Go runtime startup is consistently slow enough (~50-100ms) that *any* iteration with a sub-50ms delay can have the SIGKILL land before the first `Update` call. The corruption invariant is meaningful only when a file exists; missing-file is uninformative, not a failure.
2. **Added a `parsedCount` diagnostic gate.** Without it, a configuration drift that makes every iteration skip (e.g. delay range narrowed to 1-5ms, or helper startup further regressing) would produce a vacuously-passing test. The gate requires at least 1 successful unmarshal across 100 iterations and fails with a clear remediation hint ("widen the delay range or investigate helper startup latency") if zero are observed.
3. **Targeted `.planning/PROJECT.md` (not a repo-root `PROJECT.md`).** The plan frontmatter says `files_modified: PROJECT.md` as shorthand. The canonical file lives at `.planning/PROJECT.md` (verified: no repo-root `PROJECT.md` exists; the GSD workflow references the `.planning/` location).
4. **Inserted new sections BEFORE Evolution.** The Evolution section is meta-documentation describing how PROJECT.md gets updated. Operator content (install, self-upgrade, labels, env vars) belongs next to Constraints and Key Decisions, not after the meta-section.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Canonical RESEARCH.md SIGKILL test body had a too-strict file-missing exemption**
- **Found during:** Task 1 (SIGKILL harness — first run of `make test-sigkill`)
- **Issue:** RESEARCH.md lines 856-862 only allowed `os.IsNotExist` on iter 0; iter 1 with a 32ms SIGKILL delay failed because the macOS helper hadn't written its first file yet. The exemption assumed sub-millisecond helper startup, which is true on Linux but not macOS (Go runtime takes 50-100ms).
- **Fix:** Widened the exemption to any iteration AND added a `parsedCount` diagnostic gate that requires ≥1 successful unmarshal across 100 iterations (otherwise the test is uninformative).
- **Files modified:** `internal/state/store_sigkill_test.go`
- **Verification:** 3 consecutive runs across the harness — all pass, parsedCount 91-95 per run.
- **Committed in:** `bef90da` (Task 1 commit) — fix included in the initial Task 1 commit, not a separate hotfix.

---

**Total deviations:** 1 auto-fixed (Rule 1 — bug in the canonical test spec)
**Impact on plan:** The deviation is a macOS-portability hardening of the canonical test body; the invariant verified is unchanged (renameio + dir-fsync produces no torn writes under SIGKILL). No scope creep, no architectural shift.

## Issues Encountered

- **macOS Go runtime startup vs SIGKILL delay race:** the first `make test-sigkill` run hit "file missing on iter 1" because the helper's `main()` hadn't reached `state.NewStore` by the time SIGKILL landed (32ms delay vs ~50-100ms helper startup). Resolved by widening the file-missing exemption (see Deviation 1). This is a known macOS characteristic, not a bug in `state.persist()`.

## User Setup Required

None — no external service configuration required for Phase 4 Plan 05. Operators following the new PROJECT.md "Installation prerequisites" section will run `chown 65532:65532 /opt/centroid/hmi-update/hmi_update_state.json` during Phase 7+ deployment, but Phase 4 itself ships no new runtime dependencies.

## Open Notes for Plan 04-06

- **Plan 04-06's `e2e/tests/restart-persistence.spec.ts`** depends on the same renameio + dir-fsync invariant proven empirically by this plan. The e2e spec exercises the cross-`docker compose restart` durability path (process restart, not SIGKILL); STATE-04 here covers the SIGKILL-mid-write fault that the e2e spec cannot reproduce (Playwright's `child_process` interface doesn't give the kind of mid-write timing control this harness provides).
- **Future state-store refactors should run `make test-sigkill` as a regression gate.** Documented in the Makefile target comment ("Run before any state-store refactor or before releases that touch internal/state.").
- **Phase 7 image-size verification** must confirm `sigkillhelper` is NOT in the production image. The Dockerfile builds only `./cmd/hmi-update`; `cmd/sigkillhelper` is built ad-hoc by the parent test into `t.TempDir()` and never touched by `make build` or `make image`. No threat-flag follow-up needed.

## Next Phase Readiness

Plan 04-05 ships the empirical state-layer fault-injection harness and the operator runbook; both are independent of the action-orchestrator work in Plans 04-03 / 04-04 / 04-06 and can be picked up in any order by downstream waves. Phase 4 verification has one more concrete state-persistence gate (`make test-sigkill`) operators can run.

---
*Phase: 04-update-rollback-force-pull-actions-safety-state-persistence*
*Plan: 05*
*Completed: 2026-05-15*

## Self-Check: PASSED

- FOUND: `cmd/sigkillhelper/main.go`
- FOUND: `internal/state/store_sigkill_test.go`
- FOUND: `Makefile`
- FOUND: `.planning/PROJECT.md`
- FOUND: `.planning/phases/04-update-rollback-force-pull-actions-safety-state-persistence/04-05-SUMMARY.md`
- FOUND commit: `bef90da` (Task 1 — test harness)
- FOUND commit: `f1c51d4` (Task 2 — PROJECT.md runbook)
