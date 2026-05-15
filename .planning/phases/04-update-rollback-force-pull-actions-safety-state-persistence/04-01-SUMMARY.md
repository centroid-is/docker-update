---
phase: 04-update-rollback-force-pull-actions-safety-state-persistence
plan: 01
subsystem: state-and-wire-schema
tags: [schema, tygo, poll, channel, forward-compat]
requires:
  - phase: 03
    plan: 01
    why: extends state.Container Phase 3 shape (AvailableDigest/LastPolledAt/Notes); reuses Phase 3 omitempty/omitzero pattern
provides:
  fields:
    - state.Container.ActionInFlight (string, omitempty)
    - state.Container.ActionError (string, omitempty)
    - api.Container.ActionInFlight (string, omitempty)
    - api.Container.ActionError (string, omitempty)
  enums:
    - poll.KindActionStart (iota=4)
    - poll.KindActionProgress (iota=5)
    - poll.KindActionResult (iota=6)
  typescript:
    - Container.action_in_flight (optional string)
    - Container.action_error (optional string)
affects:
  - plan: 04-03
    why: actions orchestrator (next wave consumer) produces KindActionStart/Progress/Result on the existing channel
  - plan: 04-05
    why: e2e specs may assert action_in_flight transitions on /api/state
tech-stack:
  patterns:
    - "Phase 3 forward-compat pattern: omitempty on strings, NewStore round-trip proof"
    - "Tygo source-of-truth: state.Container ↔ api.Container byte-identical tags"
    - "Append-only iota: T-04-01-03 invariant (pre-existing Kind integers stay stable)"
key-files:
  modified:
    - internal/state/schema.go
    - internal/api/types.go
    - internal/poll/channel.go
    - ui/src/lib/types.d.ts
  created:
    - internal/state/schema_phase4_test.go
    - internal/api/types_phase4_test.go
decisions:
  - "ActionInFlight is a plain string (not typed enum) — Notes precedent (CONTEXT.md Area 1 'Claude's Discretion')"
  - "ActionError is a single short string with 'phase_failed: reason' format — Notes precedent, avoids tygo struct complexity"
  - "Both new fields use omitempty (strings); no time.Time fields added in this plan so no omitzero usage"
  - "iota constants appended AFTER KindPollSweepEnd — never inserted; pre-existing Kind integers (0..3) stay stable"
metrics:
  duration_min: 6
  completed: 2026-05-15
  tasks_completed: 2
  files_modified: 4
  files_created: 2
  tests_added: 4
requirements:
  - ACT-11
---

# Phase 4 Plan 01: Schema Extensions for Action Lifecycle Summary

One-liner: Additive Phase 4 schema landing — `state.Container` + `api.Container` gain `ActionInFlight` and `ActionError` (omitempty strings); `poll.UpdateKind` appends three constants (`KindActionStart/Progress/Result`); tygo regenerates `ui/src/lib/types.d.ts`; forward-compat with Phase 3 on-disk state proven via `NewStore` round-trip.

## What landed

Two atomic commits, four files modified, two test files created. No consumer code wired — that is Plan 04-03's job. The two-field + three-iota surface is what every other Phase 4 plan compiles against (per the wave-1 unblock requirement in 04-01-PLAN.md `<objective>`).

## Files

### Modified

- `internal/state/schema.go` — `Container` struct gains `ActionInFlight string` and `ActionError string` (both `omitempty`) appended after `Notes`. Godoc cites Phase 4 plan 04-01 and CONTEXT.md Area 1. File-level godoc updated to enumerate Phase 4 additions alongside Phase 2/3.
- `internal/api/types.go` — wire-type mirror with byte-identical struct tags. Godoc cross-references `internal/state.Container` for full semantics (per Pattern E "tygo source-of-truth").
- `internal/poll/channel.go` — three new iota constants (`KindActionStart`, `KindActionProgress`, `KindActionResult`) appended after `KindPollSweepEnd`. Package-level doc-comment updated from "Phase 4 will add a third producer" → "Phase 4 plan 04-03 adds the third: actions.actionOrchestrator producing KindActionStart / KindActionProgress / KindActionResult."
- `ui/src/lib/types.d.ts` — tygo regenerated; `Container` interface gains optional `action_in_flight?: string` and `action_error?: string` keys.

### Created

- `internal/state/schema_phase4_test.go` — RED-FIRST forward-compat guard.
  - `TestPhase4SchemaFields_RoundTrip_Container` — round-trip + omitempty invariant.
  - `TestPhase4SchemaFields_ForwardCompat_Phase3OnDisk` — seeds a literal Phase-3-shape JSON file on disk, loads via `state.NewStore`, asserts `ActionInFlight == ""` and `ActionError == ""`, runs a no-op `Update`, and verifies the rewritten on-disk payload omits both keys entirely (omitempty proof through the `renameio` write path).
- `internal/api/types_phase4_test.go` — RED-FIRST tygo SoT guard.
  - `TestPhase4Types_StateApiTagParity` — reflection-based, asserts `state.Container.ActionInFlight/ActionError` struct tags are byte-identical to `api.Container`'s. Uses `t.Errorf` not `t.Fatal` so both drifts surface at once.
  - `TestPhase4APITypes_Parity_Container` — belt-and-braces JSON marshal parity check with the new fields populated.

## Commits

- `8c08d31` — `feat(04-01): add ActionInFlight + ActionError to state and api Container types` (Task 1)
- `e4cdbe8` — `feat(04-01): append KindActionStart/Progress/Result to poll.UpdateKind` (Task 2)

## Forward-compat invariant

The headline guarantee: an HMI upgrading from a Phase 3 build to a Phase 4 build does NOT need its `hmi_update_state.json` wiped. The test `TestPhase4SchemaFields_ForwardCompat_Phase3OnDisk` proves:

1. A literal Phase-3-shape JSON document (with `version`, `containers`, `last_poll_start`, `last_poll_end`, and a container with the original 13 fields — but NO `action_in_flight` or `action_error` keys) loads cleanly through `state.NewStore`.
2. Zero values land where expected: `Container.ActionInFlight == ""` and `Container.ActionError == ""`.
3. A subsequent no-op `state.Store.Update` rewrites the file via the renameio path; the rewritten payload still omits both keys (omitempty proof).

This mitigates T-04-01-01 (Tampering disposition: mitigate) in the plan's threat register.

## Append-only iota confirmation

`internal/poll/channel.go` integer values BEFORE this plan:

| Constant | Value |
|---|---|
| KindDigestResolved | 0 |
| KindContainerEvent | 1 |
| KindPollSweepStart | 2 |
| KindPollSweepEnd | 3 |

AFTER this plan — the four pre-existing constants retain their integer values; three new constants are appended:

| Constant | Value |
|---|---|
| KindDigestResolved | 0 |
| KindContainerEvent | 1 |
| KindPollSweepStart | 2 |
| KindPollSweepEnd | 3 |
| KindActionStart | 4 |
| KindActionProgress | 5 |
| KindActionResult | 6 |

This satisfies T-04-01-03: the wire-format `Kind` integer is stable across the Phase 3 → Phase 4 boundary.

## Verification gate

| Check | Result |
|---|---|
| `go build ./...` | exit 0 |
| `go test ./internal/state/... ./internal/api/... ./internal/poll/... -race -count=1` | exit 0 |
| `make check-types` (with tygo on PATH) | exit 0 (post-commit) |
| Acceptance criteria greps (Task 1: 8 / Task 2: 9) | all green |
| No regression in pre-existing Phase 1–3 unit tests | confirmed (full state/api/poll suites pass) |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking issue] `tygo` binary not on `make`'s PATH**
- **Found during:** Task 2 (`make types` invocation)
- **Issue:** `make types` failed with `tygo: No such file or directory` because the binary lives at `$HOME/go/bin/tygo` but `make` inherits a sanitized PATH that does not include `$(go env GOPATH)/bin`.
- **Fix:** Ran the target with `PATH="$HOME/go/bin:$PATH" make types` and `PATH="$HOME/go/bin:$PATH" make check-types`. The Makefile itself was not modified; this is an environment shim. If this recurs in future plans, candidate Rule 2 fix would be either (a) document the PATH requirement in PROJECT.md, or (b) update the `types` target to use `go run github.com/gzuidhof/tygo` so the toolchain is hermetic. Deferred for now — out of scope for 04-01.
- **Commit:** Effect captured in `e4cdbe8` (the regenerated `ui/src/lib/types.d.ts`).
- **Out-of-scope discovery filed for future:** Makefile PATH hardening; not adding to `deferred-items.md` because it is a developer-environment ergonomics issue rather than a code defect.

## Open notes for downstream plans

- **Plan 04-03 (orchestrator)** is the sole consumer of the three new `UpdateKind` constants. The `Apply` closure semantics are documented inline:
  - `KindActionStart` → set `ActionInFlight = "updating" | "rolling_back" | "force_pulling"`.
  - `KindActionProgress` → no-op on state currently (reserved for Phase 5 UI breadcrumbs).
  - `KindActionResult` → clear `ActionInFlight`; on success set `CurrentDigest`/`PreviousDigest`; on failure set `ActionError = "<phase>_failed: <reason>"`.
- **Plan 04-04 (HTTP handlers)** serializes containers through `internal/api.Container`; the new fields surface on `/api/state` automatically. T-04-04-* (path-leak guard on response body) is still owed by Plan 04-04 — this plan's `ActionError` string is the data shape that test will guard.
- **Plan 04-05 (Phase 5 UI)** reads `action_in_flight` for per-row spinner state and `action_error` for the toast surface. The TypeScript shape lands in this commit so Plan 04-05 can typecheck against it from day one.
- **Canonical literal site (Pattern J)** for the three `ActionInFlight` values is NOT promoted in this plan. CONTEXT.md leans on plain strings (Notes precedent); if Plan 04-03 needs the literals at more than one site, the WR-10 carry-forward says promote to `internal/state/notes.go` (or sibling `internal/state/actions.go`).

## Threat surface scan

No new threat surface introduced by this plan beyond what the plan's `<threat_model>` already enumerates (T-04-01-01/02/03 all dispositioned). The two new struct fields are not user-input sinks (orchestrator-set only); the three new enum constants are internal to the state-mutation channel and never serialized to the wire.

## Self-Check: PASSED

- Files created exist:
  - `internal/state/schema_phase4_test.go` — FOUND
  - `internal/api/types_phase4_test.go` — FOUND
- Files modified exist:
  - `internal/state/schema.go` — FOUND
  - `internal/api/types.go` — FOUND
  - `internal/poll/channel.go` — FOUND
  - `ui/src/lib/types.d.ts` — FOUND
- Commits exist:
  - `8c08d31` — FOUND (Task 1)
  - `e4cdbe8` — FOUND (Task 2)
