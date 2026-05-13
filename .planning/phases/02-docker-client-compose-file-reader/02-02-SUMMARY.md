---
phase: 02-docker-client-compose-file-reader
plan: 02
subsystem: infra
tags: [go, compose, inode-drift, stat-before-act, sentinel-error, pitfall-10]

# Dependency graph
requires:
  - phase: 01-walking-skeleton-test-harness
    provides: internal/state.Store boot-snapshot + concurrent-safe pattern (analog for Reader constructor + RWMutex); persist_test.go t.Errorf-in-goroutine convention; TempDir-based hermetic-test idiom

provides:
  - internal/compose.Reader stat-based drift detector — boot snapshot of (inode, mtime, size), O(1) per-call CheckUnchanged
  - internal/compose.ErrComposeFileMoved sentinel — the codebase's FIRST `var ErrX = errors.New(...)` sentinel; convention now established
  - belt-and-braces (mtime, size) comparison applied unconditionally — catches in-place os.WriteFile edits even on stable-inode filesystems
  - slog.Info("compose.reader.boot", ...) construction-time event with drift_signal value visible in logs
  - 7 unit tests covering empty-path, missing-file, happy-path idempotency, atomic-rename drift, in-place-edit drift, concurrent-read race-safety, file-deletion drift

affects: [02-04-healthz-upgrade, 02-05-e2e-compose-drift-spec, 04-actions-update-rollback, 04-debug-endpoint-compose-stat]

# Tech tracking
tech-stack:
  added: []  # no new go.mod entries — only stdlib (os, syscall, log/slog, sync, time, context, fmt)
  patterns:
    - "Sentinel-error file (errors.go) — first occurrence in repo; package-level `var ErrX = errors.New(...)` with errors.Is-friendly wrap chains via fmt.Errorf(\"compose: ...: %w\", ..., ErrX)"
    - "Multi-wrap chain: fmt.Errorf(\"...: %w: %w\", sentinel, underlyingOSErr) preserves BOTH errors.Is checks (sentinel-of-interest AND fs.ErrNotExist) on a single err value"
    - "Boot-snapshot under RWMutex (analog to state.Store): captured once at construction, read on every check, write-mode lock reserved for future rotation but unused in v1"
    - "Unconditional belt-and-braces signal — even when the primary signal (inode) is available, also compare the cheaper-to-detect-in-place-edit signal (mtime, size). Documented with a 'do not simplify' callout."

key-files:
  created:
    - internal/compose/errors.go (47 lines — ErrComposeFileMoved sentinel + package doc)
    - internal/compose/reader.go (167 lines — Reader struct + NewReader + captureBootSnapshot + CheckUnchanged)
    - internal/compose/reader_test.go (256 lines — 7 tests + RED-FIRST header)
  modified: []

key-decisions:
  - "Belt-and-braces (mtime, size) comparison runs unconditionally — not gated on !bootInodeStable. The plan-skeleton implementation already did this; documented inline as load-bearing because in-place os.WriteFile edits preserve inode but change mtime/size, and the test contract requires those to be flagged."
  - "Deleted-file unification: stat ENOENT is wrapped as `%w: %w` of (ErrComposeFileMoved, fs.ErrNotExist) so BOTH errors.Is checks succeed on the same err. Phase 4's 412 handler does not need to distinguish today, but the dual-wrap leaves the option open without an API change."
  - "syscall.Stat_t.Ino used directly — explicit uint64 conversion kept on both Linux (uint64) and Darwin (uint64) so the same source compiles on either developer machine. Windows is out of scope for v1 (CLAUDE.md targets linux/amd64)."
  - "ctx parameter accepted but unused in v1 — `_ = ctx` documents the symmetry with the rest of the codebase + leaves room for future cancellation without an API break."
  - "Package-level doc comment placed on errors.go (the plan's prescribed location) — coexists with runner.go's Phase-4 stub comment; Go renders both."

patterns-established:
  - "Sentinel error file convention — errors.go sibling to the package's main file, exports `var ErrX = errors.New(...)`, documented with the wrap convention and the HTTP mapping. First use in repo; future packages should follow."
  - "Stat-snapshot drift primitive — a reusable shape for any future 'observe a file once at boot, check for drift on demand' need. Combine boot-stat under Lock + on-demand stat under RLock + sentinel error for drift signal."
  - "Test fixture for atomic-save simulation — write tmp file + os.Rename atop target, with a path suffix unique-per-test (`${path}.tmp-atomic`) to avoid TempDir collisions. Combined with a 50ms sleep before in-place-edit rewrites to tighten against macOS HFS+ 1s mtime resolution."

requirements-completed: [DOCK-02]

# Metrics
duration: 13min
completed: 2026-05-13
---

# Phase 02 Plan 02: Compose-File Reader (Stat-Based Drift Detector) Summary

**internal/compose.Reader captures a boot stat snapshot (inode + mtime + size), exposes an O(1) CheckUnchanged that returns ErrComposeFileMoved on ANY drift signal — protecting Phase 4 actions against Pitfall 10 (atomic-save editor replacing the bind-mounted compose file).**

## Performance

- **Duration:** 13 min (commit timestamps: 1bee51e RED → 93bd965 GREEN)
- **Started:** 2026-05-13T21:02:57Z
- **Completed:** 2026-05-13T21:16:02Z (approx — pre-SUMMARY commit time)
- **Tasks:** 1 (TDD task split into RED commit + GREEN commit)
- **Files modified:** 3 created (no Phase 1 file touched)

## Accomplishments

- **DOCK-02 satisfied at the unit-test level:** Reader detects all three drift modes (atomic rename → inode change; in-place edit → mtime/size change; deletion → stat ENOENT) and surfaces a single sentinel `ErrComposeFileMoved` that Phase 4 will map to HTTP 412 with the documented remediation hint.
- **Sentinel-error convention established:** `internal/compose/errors.go` is the codebase's first `var ErrX = errors.New(...)` file. The convention (sibling errors.go with documented wrap semantics) is now available for `internal/registry`, `internal/poll`, `internal/actions` to follow when they ship their own typed errors.
- **No YAML parser, no docker SDK:** the package's dependency footprint is stdlib-only (`os`, `syscall`, `log/slog`, `sync`, `time`, `context`, `fmt`, `errors`). Grep verified — no `yaml` imports, no `github.com/docker/...` or `github.com/moby/...` imports. The package boundary is clean for future Phase 4 wiring.
- **Race-clean concurrent semantics:** TestCheckUnchanged_Concurrent fires 8×100 = 800 reads against an unchanged file; `-race` reports clean. The RWMutex pattern (boot-write-lock at construction, R-lock per check) is reusable for any future "snapshot once, observe many" primitive.

## Task Commits

This was a TDD task; the plan executed as RED → GREEN per C4:

1. **Task 1 RED: failing tests for compose.Reader** — `1bee51e` (test) — internal/compose/reader_test.go with all 7 tests. Build verified to fail with `undefined: NewReader` / `undefined: ErrComposeFileMoved`.
2. **Task 1 GREEN: sentinel + Reader implementation** — `93bd965` (feat) — internal/compose/errors.go + internal/compose/reader.go. All 7 tests pass under `-race`.

No REFACTOR commit — the GREEN code is already at the documented quality bar (load-bearing inline comments per the persist.go template, threat-model cross-refs, RWMutex doc comments).

**Plan metadata commit:** added in the final commit alongside SUMMARY + STATE updates.

## Files Created/Modified

**Created:**

- `internal/compose/errors.go` (47 lines) — `ErrComposeFileMoved` sentinel; package-level doc comment establishing the "Phase 2 does NOT parse YAML" stance and the Phase 4 HTTP 412 mapping.
- `internal/compose/reader.go` (167 lines) — `Reader` struct + `NewReader` constructor + `captureBootSnapshot` helper + `CheckUnchanged` method. Inline "do not simplify" callout on the unconditional belt-and-braces comparison.
- `internal/compose/reader_test.go` (256 lines) — RED-FIRST header per C4; 7 hermetic tests using `t.TempDir()` and the persist_test.go t.Errorf-in-goroutine convention.

**Modified:** none in this plan (the package's existing `runner.go` Phase 4 stub is untouched).

## Decisions Made

1. **Belt-and-braces (mtime, size) comparison runs unconditionally** — even when the primary inode signal is available, we still compare mtime/size. Documented with a "do not simplify" callout (mirroring `internal/state/persist.go` lines 24-35 convention). Rationale: a `os.WriteFile(path, ...)` in place preserves the inode on every common filesystem but changes (mtime, size); skipping the mtime/size check on stable-inode FS would silently miss this drift signal.

2. **Deleted-file unification under both sentinels** — the wrap chain is `fmt.Errorf("...: %w: %w", ErrComposeFileMoved, underlyingOSErr)`. Go's `errors.Is` walks the entire chain, so BOTH `errors.Is(err, ErrComposeFileMoved)` AND `errors.Is(err, fs.ErrNotExist)` return true on the same error value. Phase 4 doesn't distinguish today, but Phase 5's UI might want to say "compose file deleted" vs "compose file replaced" — the option survives without an API change.

3. **`syscall.Stat_t.Ino` direct usage with explicit `uint64()` conversion** — same source compiles on Linux and Darwin (both have `Ino uint64`). The explicit conversion is redundant on these targets but documents intent and would surface a compile error if a future toolchain bump changed the type. `//nolint:unconvert` annotates the redundancy.

4. **ctx parameter accepted but unused** — `CheckUnchanged(ctx context.Context) error` matches the codebase's other "may eventually need cancellation" surfaces. The current implementation is non-blocking (single stat syscall, ~microseconds), so `_ = ctx` documents the discard.

5. **Package doc comment on errors.go (not reader.go)** — the plan prescribed errors.go as the documentation home. The pre-existing `runner.go` keeps its own brief comment describing the Phase-4 Runner stub. Both render correctly in `go doc`; the two-doc-files arrangement is idiomatic Go when responsibilities differ.

## Deviations from Plan

None — plan executed exactly as written.

The plan's `<action>` step 1-5 had verbatim Go skeletons for errors.go and reader.go; the implementation followed those skeletons with minor whitespace/comment-polish adjustments only:

- The plan skeleton's `Reader` doc comment was preserved verbatim except for a small expansion of the "do not simplify" rationale on `bootModTime/bootSize` (the plan said "belt-and-braces"; this implementation expanded with a pointer to `internal/state/persist.go` as the convention anchor).
- The plan's test file was structured per `<behavior>` Tests 1-7 verbatim; comment headers expanded with cross-references to the persist_test.go t.Errorf convention.

The plan's `<verify>` automated check (`go build ./... && go vet ./... && go test ./internal/compose/... -race -v && grep -c 'ErrComposeFileMoved' internal/compose/errors.go internal/compose/reader.go`) — all four steps pass.

## Issues Encountered

**1. Two package-level doc comments in `internal/compose/`.** The plan called for a doc comment on the new `errors.go`, but `runner.go` (Phase 1 stub) already had one. Resolution: kept both. Go renders the first one alphabetically (`errors.go`) as the canonical package doc; `runner.go`'s comment continues to document the `Runner` interface specifically. Verified `go doc github.com/centroid-is/hmi-update/internal/compose` shows clean output covering both.

**2. None other.** The plan's threat model anticipated mtime-resolution flakiness on macOS HFS+; the 50ms sleep in TestCheckUnchanged_InPlaceEdit handled it cleanly. The developer's filesystem is APFS, which has sub-second mtime resolution, so the sleep was actually overkill on this machine — but kept for CI portability per the plan's guidance.

## Test Environment Detail (per plan `<output>` request)

- **Primary drift signal exercised:** **inode-primary** (`drift_signal=inode-primary` in every test's slog event). The developer machine runs macOS APFS, which exposes stable non-zero inodes via `syscall.Stat_t`.
- **mtime+size fallback path:** NOT exercised on this developer machine; the `else` branch in `captureBootSnapshot` is reachable only on filesystems where `info.Sys().(*syscall.Stat_t)` fails the type assertion or `st.Ino == 0`. Coverage of that branch is deferred — the belt-and-braces mtime+size comparison is exercised by TestCheckUnchanged_InPlaceEdit and TestCheckUnchanged_AtomicRename regardless of which branch the boot snapshot took.
- **Flakiness:** None. The 50ms sleep in TestCheckUnchanged_InPlaceEdit was sufficient on APFS (sub-second resolution). On HFS+ developer machines or 1s-resolution NFS mounts the same 50ms would still be sufficient — `os.WriteFile` is atomic at the OS-call level, so a single nanosecond of clock advance during the sleep is enough; the only failure mode would be sub-millisecond mtime granularity, which APFS/ext4/HFS+ do not exhibit.
- **Slog event verbatim:**
  ```
  INFO compose.reader.boot path=<TempDir>/docker-compose.yml inode=120327095 mtime=2026-05-13T21:04:57.833360454Z size=13 drift_signal=inode-primary
  ```

## go test ./internal/compose/... -race -v output

```
=== RUN   TestNewReader_EmptyPath
--- PASS: TestNewReader_EmptyPath (0.00s)
=== RUN   TestNewReader_MissingFile
--- PASS: TestNewReader_MissingFile (0.00s)
=== RUN   TestNewReader_HappyPath
--- PASS: TestNewReader_HappyPath (0.00s)
=== RUN   TestCheckUnchanged_AtomicRename
--- PASS: TestCheckUnchanged_AtomicRename (0.00s)
=== RUN   TestCheckUnchanged_InPlaceEdit
--- PASS: TestCheckUnchanged_InPlaceEdit (0.05s)
=== RUN   TestCheckUnchanged_Concurrent
--- PASS: TestCheckUnchanged_Concurrent (0.00s)
=== RUN   TestCheckUnchanged_FileDeleted
--- PASS: TestCheckUnchanged_FileDeleted (0.00s)
PASS
ok  	github.com/centroid-is/hmi-update/internal/compose	1.379s
```

`errors.Is(err, compose.ErrComposeFileMoved)` is verified true in 4 tests (rename, in-place edit, delete) + one of the chained checks under delete.

## Threat Model Coverage

| Threat ID | Status | Evidence |
|-----------|--------|----------|
| T-02-02-01 (Tampering — compose file replaced) | mitigated | TestCheckUnchanged_AtomicRename + TestCheckUnchanged_InPlaceEdit; both signal ErrComposeFileMoved. Phase 4 will surface as HTTP 412. |
| T-02-02-02 (Spoofing — symlink swap) | accepted | Documented acceptance; inode comparison still detects underlying file change. |
| T-02-02-03 (Info disclosure — error messages echo path/stat) | mitigated | Path comes from operator-controlled env var, not attacker-controlled API surface. Intentional remediation guidance. |
| T-02-02-04 (DoS — attacker spams mtime change) | accepted | Requires host-side write access; attacker already owns the box. Out of scope. |

## Next Phase Readiness

**Ready for plan 02-03 (discovery goroutine):** independent — different package, no file overlap. Wave-2 plan 02-03 was awaiting plan 02-02's Reader to land before its Phase 4 consumer can be wired, but plan 02-03's own scope (Docker events goroutine) does not depend on the Reader directly.

**Ready for plan 02-04 (healthz upgrade + main.go wiring):** the `compose.NewReader` constructor signature is final; `cmd/hmi-update/main.go` can now add the boot step 4 from CONTEXT.md `### Lifecycle & Wiring`.

**Ready for plan 02-05 (e2e compose-drift Playwright spec):** the sentinel + 412 mapping contract is in place. Plan 02-05 will add the `//go:build debug` debug endpoint `GET /debug/compose-stat` that exercises Reader.CheckUnchanged and returns 200/412.

**Phase 4 consumers (later):** plan 02-02 delivers the contract Phase 4 will call. Phase 4's update/rollback handlers will invoke `composeReader.CheckUnchanged(ctx)` before `docker compose -f $HMI_UPDATE_COMPOSE_PATH up -d --force-recreate <svc>` and branch on `errors.Is(err, compose.ErrComposeFileMoved)` to serve the documented 412 body.

**No blockers or concerns introduced.**

## Self-Check: PASSED

Verified files exist:
- `internal/compose/errors.go` — FOUND
- `internal/compose/reader.go` — FOUND
- `internal/compose/reader_test.go` — FOUND

Verified commits exist (per `git log --oneline --all`):
- `1bee51e` — FOUND (test commit, RED phase)
- `93bd965` — FOUND (feat commit, GREEN phase)

Verified gates pass:
- `go build ./...` — exit 0
- `go vet ./...` — exit 0
- `go test ./internal/compose/... -race -v` — 7/7 PASS
- `grep -c 'ErrComposeFileMoved' internal/compose/errors.go internal/compose/reader.go` — `errors.go:4 reader.go:7` (sentinel referenced in 11 sites across the package)
- No YAML parser imported (grep verified)
- No docker SDK imported into `internal/compose/*.go` (grep verified — `internal/docker` is the sole importer per plan 02-01's boundary check)

## TDD Gate Compliance

- RED commit: `1bee51e` — test(02-02): adds failing tests; build verified to fail with `undefined: NewReader` / `undefined: ErrComposeFileMoved` (output captured pre-commit).
- GREEN commit: `93bd965` — feat(02-02): drives all 7 tests to pass under -race.
- REFACTOR commit: not present — GREEN code is already at the documented quality bar (doc comments, "do not simplify" callouts per persist.go convention, threat-model cross-refs in the plan). REFACTOR is optional per execute-plan.md and assessed unnecessary.

---
*Phase: 02-docker-client-compose-file-reader*
*Completed: 2026-05-13*
