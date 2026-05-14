---
phase: 03-registry-polling-update-detection
plan: 01
subsystem: api
tags: [go, schema, tygo, typescript, time, json, forward-compat]

# Dependency graph
requires:
  - phase: 02-docker-discovery
    provides: "internal/state.Container with Phase 2 fields (ContainerID, Labels, Pinned, Stopped); the additive omitempty/omitzero pattern this plan extends"
  - phase: 01-walking-skeleton-test-harness
    provides: "tygo source-of-truth contract; internal/api/types.go ↔ state/schema.go json-tag parity invariant; make check-types CI gate"
provides:
  - "internal/state.Container.AvailableDigest (DETECT-05/07) — upstream sha256 from last poll"
  - "internal/state.Container.LastPolledAt (DETECT-05) — wall-clock time of last successful resolver.Digest call"
  - "internal/state.Container.Notes (DETECT-08/09) — single ops-readable sentence for pinned / invalid-pattern / running-tag-mismatch"
  - "internal/state.State.LastPollStart / LastPollEnd / LastPollError (OBS-04) — top-level sweep observability"
  - "internal/api.Container + internal/api.State mirror the six new fields verbatim (tygo source-of-truth invariant preserved)"
  - "ui/src/lib/types.d.ts regenerated; make check-types is green"
  - "Forward-compat invariant verified: pre-Phase-3 Phase 2 state files deserialize cleanly with zero-valued new fields"
affects:
  - "03-02 (registry resolver) — uses Container.AvailableDigest as the comparison target"
  - "03-03 (poller) — populates LastPollStart/End/Error and Container.LastPolledAt/Notes via the single-consumer channel"
  - "03-04 (discovery channel refactor) — store.Update closures will set the new fields"
  - "03-05 (Phase 3 e2e specs) — assert on /api/state shape now containing the new keys"
  - "Phase 5 UI — renders update_available + last_polled_at + notes per row"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Go `omitzero` tag (Go 1.24+) for time.Time fields whose zero value must be omitted from JSON — `omitempty` does NOT recognize struct zero-value semantics. Established as the project convention for any future time.Time on a wire/disk schema."
    - "Phase 3 schema additions follow Phase 2's append-only field convention: new omitempty/omitzero fields are inserted after the prior tail, plan-attribution comment block introduces them, individual field godocs cite the CONTEXT.md decision or requirement IDs."

key-files:
  created:
    - "internal/state/schema_phase3_test.go - round-trip + omitempty/omitzero + forward-compat invariant tests"
    - "internal/api/types_phase3_test.go - tygo source-of-truth parity tests between state.Container/api.Container and state.State/api.State"
  modified:
    - "internal/state/schema.go - added Container.AvailableDigest, Container.LastPolledAt, Container.Notes, State.LastPollStart, State.LastPollEnd, State.LastPollError + import time"
    - "internal/api/types.go - mirrored the six new fields verbatim + import time"
    - "ui/src/lib/types.d.ts - regenerated via tygo; new TS fields available_digest, last_polled_at, notes, last_poll_start, last_poll_end, last_poll_error"

key-decisions:
  - "[Phase 03 P01] Use `omitzero` (Go 1.24+) on the three time.Time fields, not `omitempty`. encoding/json's omitempty does not recognize struct zero-values, which would have leaked '0001-01-01T00:00:00Z' into wire payloads for un-polled containers — breaking the forward-compat invariant the plan explicitly demands. String fields keep `omitempty` since the v1 encoder recognizes \"\" as the zero value."
  - "[Phase 03 P01] Notes is a single string (not []string). At most one note applies; if two would, join with '; '. (CONTEXT.md Area 3 'Claude's Discretion' carried through.)"
  - "[Phase 03 P01] LastPollError is a plain string (not a structured object). v1 leaves it empty — errgroup workers swallow per-container errors. Reserved for future sweep-level failure modes. (CONTEXT.md Area 4 'Claude's Discretion' carried through.)"

patterns-established:
  - "Pattern: Wire/disk time.Time fields use `omitzero` tag — applied across schema.go and api/types.go in lockstep. Any future Phase that adds a time.Time field to either struct MUST use omitzero; check-types parity catches drift."
  - "Pattern: Schema addition triple-edit (schema.go → api/types.go → make types) lands in a single TDD pair of commits per file (RED test + GREEN implementation), with make check-types as the final gate."

requirements-completed: [DETECT-05, DETECT-07, DETECT-09, OBS-04]

# Metrics
duration: 6min
completed: 2026-05-14
---

# Phase 3 Plan 01: Schema Additions Summary

**Three per-Container fields (AvailableDigest, LastPolledAt, Notes) and three top-level State fields (LastPollStart, LastPollEnd, LastPollError) land additively in state.go and api/types.go with byte-identical JSON tags; tygo regenerates types.d.ts cleanly; forward-compat with Phase 2 on-disk state files is verified by literal-JSON unmarshal test.**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-05-14T13:05:47Z
- **Completed:** 2026-05-14T13:11:44Z
- **Tasks:** 2 (each TDD: RED + GREEN, so 4 commits)
- **Files modified:** 3 (schema.go, api/types.go, types.d.ts) + 2 new test files

## Accomplishments

- Six new fields land on the state + api types with byte-identical JSON tags (tygo source-of-truth invariant preserved across the addition).
- Forward-compat invariant load-tested: a literal Phase 2 JSON document (only the 10 original Container fields + top-level version + containers) unmarshals into the Phase 3 State with all six new fields at zero values.
- `omitzero` tag (Go 1.24+) adopted as the project-wide convention for time.Time fields on wire/disk schemas — closes a real gap the plan's text would have left open if executed verbatim.
- tygo regenerated ui/src/lib/types.d.ts; `make check-types` is green; no schema-vs-typescript drift.
- Downstream Phase 3 plans (03-02 through 03-05) can now reference Container.AvailableDigest, Container.LastPolledAt, Container.Notes, State.LastPollStart/End/Error in their store.Update closures.

## Task Commits

Each task was committed atomically as a TDD pair:

1. **Task 1 RED: failing schema tests** — `9174bb4` (test)
2. **Task 1 GREEN: add Phase 3 fields to internal/state** — `0e4ca56` (feat)
3. **Task 2 RED: failing api parity tests** — `fe109d9` (test)
4. **Task 2 GREEN: mirror fields into api/types.go + regen types.d.ts** — `99fb3bd` (feat)

_Note: No REFACTOR commit was needed — the GREEN implementations are already the canonical shape (no duplication, no dead code, no extracted helpers to consolidate)._

## Files Created/Modified

- `internal/state/schema.go` — modified. Added `import "time"`, three new Container fields, three new State fields, plan-attribution comment blocks, long godocs citing DETECT-05/07/09 and OBS-04, omitzero/omitempty rationale documented inline.
- `internal/state/schema_phase3_test.go` — new. RED-first tests for round-trip, omitempty/omitzero invariant, and the load-bearing forward-compat invariant (literal Phase 2 JSON unmarshal). 210 lines.
- `internal/api/types.go` — modified. Mirrors the six new fields verbatim from state schema; short godocs that cross-reference state.Container/state.State for full semantics; same `import "time"` addition; same omitzero on time.Time.
- `internal/api/types_phase3_test.go` — new. RED-first tests asserting byte-identical JSON between state.Container/api.Container and state.State/api.State (the tygo source-of-truth invariant pushed up to a unit test). 141 lines.
- `ui/src/lib/types.d.ts` — regenerated by tygo. New keys: `available_digest`, `last_polled_at` (typed as `string /* RFC3339 */` per tygo.yaml type_mappings), `notes`, `last_poll_start`, `last_poll_end`, `last_poll_error`.

## Decisions Made

- **`omitzero` on time.Time fields** — see Deviations below. Caught by the RED test before the GREEN code shipped; ensured the forward-compat invariant the plan demands actually holds, not just appears to hold.
- **Field order: append after existing tail** — followed Phase 2's append-only convention. Inserting after `Stopped` (Container) and `Containers` (State) keeps the diff minimal and makes the Phase 3 additions trivially identifiable in git blame.
- **Plan-attribution comment block before each field group** — Phase 2 established this house style; carried through verbatim for Phase 3.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Use `omitzero` (not `omitempty`) on time.Time fields**

- **Found during:** Task 1 GREEN (running TestPhase3SchemaFields_RoundTrip_Container after applying the plan's literal `omitempty` tags)
- **Issue:** The plan specifies `omitempty` on all six new fields including LastPolledAt, LastPollStart, LastPollEnd. `encoding/json`'s `omitempty` predicate does not recognize the `time.Time` struct zero value — only `nil`, `""`, `0`, `false`, empty slice/map/chan are "empty" for v1 omitempty. A zero-valued time.Time serializes to `"0001-01-01T00:00:00Z"`, leaking into the wire payload for any un-polled container. This would break the **forward-compat invariant** the plan explicitly demands (and tests) — a Phase 2 state file re-saved by a Phase 3 binary would gain six new keys it didn't have before.
- **Fix:** Switched the three `time.Time` fields (Container.LastPolledAt, State.LastPollStart, State.LastPollEnd) to Go 1.24+'s `omitzero` tag. `omitzero` calls `IsZero()` on the value (which `time.Time` implements correctly) and omits the key cleanly. The string fields (AvailableDigest, Notes, LastPollError) keep `omitempty` since `""` is recognized by the v1 encoder. Documented in each field's godoc and the file-level Container doc comment.
- **Files modified:** `internal/state/schema.go`, `internal/api/types.go`
- **Verification:** TestPhase3SchemaFields_RoundTrip_Container, TestPhase3SchemaFields_RoundTrip_State, TestPhase3SchemaFields_ForwardCompat_Phase2OnDisk, and TestPhase3APITypes_OmitZero_TimeFields all pass. The empty-Container marshal output is `{"service":"foo","update_available":false}` — no Phase 3 keys leak.
- **Committed in:** `0e4ca56` (Task 1 GREEN) and `99fb3bd` (Task 2 GREEN)

---

**Total deviations:** 1 auto-fixed (1 bug — encoding/json semantics)
**Impact on plan:** Correctness-essential. Without this fix, the plan's TestPhase3SchemaFields_ForwardCompat_Phase2OnDisk would still pass (it only asserts zero values on unmarshal, not omitted keys on re-marshal), but a real-world Phase 2 → Phase 3 upgrade would have surfaced corrupted-looking timestamps in every container row of the operator's state file on first write. The deviation is documented in the field godocs and the SUMMARY's `patterns-established` so future Phases adopt the convention.

## Issues Encountered

- **tygo CLI not on PATH for non-interactive shells.** `which tygo` returned not-found; the binary is at `$HOME/go/bin/tygo` (from a prior `go install`). Resolved by running `make types` and `make check-types` with `PATH="$HOME/go/bin:$PATH"` prefix. This is an operator-environment issue, not a project fix — `cmd/hmi-update/Makefile` already assumes `tygo` is on PATH, which is correct for CI (`actions/setup-go` adds GOBIN to PATH). No project change needed; flag for the operator to add `$HOME/go/bin` to their shell init.

## User Setup Required

None — no external service configuration required for this plan. All changes are local Go source + tygo-regenerated TypeScript types.

## Next Phase Readiness

- All six new schema fields are wire-and-disk available for plans 03-02 (registry resolver) and 03-03 (poller) to populate via the single-consumer channel pattern Phase 2 established.
- Phase 5 UI can begin reading `available_digest`, `last_polled_at`, `notes`, `last_poll_start`, `last_poll_end` from `/api/state` as soon as 03-03 ships — the contract is fixed and proven byte-stable via the parity tests.
- No blockers; no concerns. The forward-compat invariant is unit-tested, so plans that touch state.Store can land without worrying about pre-Phase-3 on-disk state files.

## Self-Check: PASSED

Verifying claims:

- File `internal/state/schema.go` exists: FOUND
- File `internal/state/schema_phase3_test.go` exists: FOUND
- File `internal/api/types.go` exists: FOUND
- File `internal/api/types_phase3_test.go` exists: FOUND
- File `ui/src/lib/types.d.ts` exists: FOUND
- Commit `9174bb4` (Task 1 RED) in git log: FOUND
- Commit `0e4ca56` (Task 1 GREEN) in git log: FOUND
- Commit `fe109d9` (Task 2 RED) in git log: FOUND
- Commit `99fb3bd` (Task 2 GREEN) in git log: FOUND
- `go build ./...` exits 0: PASS
- `go vet ./...` exits 0: PASS
- `go test ./internal/state/... -race -count=1` exits 0 (full suite, not just Phase 3 tests): PASS
- `go test ./internal/api/... -race -count=1 -run TestPhase3APITypes` exits 0: PASS
- `make check-types` exits 0: PASS
- JSON tags byte-identical between schema.go and api/types.go: PASS (TAGS_IDENTICAL via diff)

## TDD Gate Compliance

Plan is not a `type: tdd` plan (frontmatter says `type: execute`), but per-task TDD (`tdd="true"`) was used for both tasks. Gate sequence in git log:

- Task 1: `test(03-01)` (9174bb4) → `feat(03-01)` (0e4ca56) — RED then GREEN ✓
- Task 2: `test(03-01)` (fe109d9) → `feat(03-01)` (99fb3bd) — RED then GREEN ✓

Both tasks verified RED failed (compile errors on missing fields) before the GREEN commit. No spurious-pass risk.

---
*Phase: 03-registry-polling-update-detection*
*Completed: 2026-05-14*
