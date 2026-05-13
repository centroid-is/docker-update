---
phase: 01-walking-skeleton-test-harness
plan: 02
subsystem: infra
tags: [go, state-store, renameio, atomic-write, dir-fsync, json, rwmutex, c2-file-only, red-to-green]

# Dependency graph
requires:
  - phase: 01-01
    provides: RED Wave-0 state-store tests (TestPersistAtomicity, TestLoadAndPersist, TestMissingFile, TestCorruptedFile) authored against an as-yet-unwritten internal/state package
provides:
  - internal/state/schema.go — Container + State types, SchemaVersion=1 constant
  - internal/state/store.go — Store{RWMutex} with NewStore (cold-boot / corrupted / valid / empty-file paths), Get (RLock snapshot), Update (write lock + persist)
  - internal/state/persist.go — renameio.WriteFile + explicit os.Open(filepath.Dir).Sync() wrapper closing the host-power-loss durability window (research correction A5 / Pitfall 7 / renameio issue #11)
  - go.mod: github.com/google/renameio/v2 promoted from indirect to direct
affects:
  - phase-01-03 tygo types (internal/state.Container mirrors internal/api.Container; plan 03 will reconcile field tags)
  - phase-01-04 HTTP server (imports state.Store directly; GET /api/state json.Marshals state.Get() output)
  - phase-02 docker-events consumer (mutates state via Update())
  - phase-02 cron poll (mutates state via Update())
  - phase-04 SIGKILL fault-injection (exercises this code unchanged; passes because of the parent-dir fsync)

# Tech tracking
tech-stack:
  added: []   # no new deps; renameio/v2 was already pinned in plan 01-01, this plan flips it from indirect → direct
  patterns:
    - "Atomic-write idiom: renameio.WriteFile + os.Open(filepath.Dir(path)).Sync() wrapper. Inline comment cites research correction A5, Pitfall 7, and renameio issue #11 so future cleanup PRs do not strip the wrapper."
    - "Locked-snapshot RWMutex pattern (ARCHITECTURE.md Pattern 1): mutations under Lock, snapshots under RLock; persist() called inside the write lock so on-disk content trails in-memory by at most one rename."
    - "NewStore boot-path triage: missing file → init+persist; empty file → init+persist (crash-mid-create recovery); corrupted JSON → error mentioning 'decode' (operator-visible, never silent reset); valid JSON → unmarshal verbatim."
    - "Defensive nil-map guard inside Update: if fn nils out Containers, repopulate before persist so the 'Containers is never nil' invariant survives caller mistakes."

key-files:
  created:
    - internal/state/schema.go
    - internal/state/store.go
    - internal/state/persist.go
  modified:
    - go.mod (renameio/v2 promoted from indirect to direct)

key-decisions:
  - "Shipped Option 2 of research correction A5 (the dir-fsync wrapper) rather than Option 1 (accept the renameio default). Rationale: ~50us per write is invisible at this scale, the brief implies durability across operator-action crashes on HMIs, and Phase 4's SIGKILL fault test would have flaked without it. Decision logged inline in persist.go."
  - "Dir-fsync is best-effort: if os.Open(filepath.Dir) fails, persist returns nil. The rename itself is still visible to subsequent opens via the kernel page cache; we only lose durability across an immediate power loss in that exact window. Documented inline + in threat register T-01-02-02 (disposition: mitigate)."
  - "Empty file (0 bytes) treated identically to a missing file. Covers crash-mid-create on a fresh HMI install — a previous process may have run os.Create and died before the first WriteFile. Without this branch, a 0-byte state file would surface as a decode error on next boot and require operator intervention for a self-recoverable condition."
  - "Container map header is shared in Get's shallow copy. Documented as 'callers MUST treat it as read-only'. The only v1 caller is internal/api which json.Marshals immediately, so the shared-map optimization is safe. If a future caller needs to mutate the returned state, copy the Containers map first — flagged in the godoc."
  - "renameio options NOT added: no IgnoreUmask(), no WithStaticPermissions(). Phase 1 accepts host-umask interaction; bind-mounted file is owned by UID 65532 (DEPLOY-08) and umask 022 yields 0644 — the desired mode."
  - "Task 1 ships a tiny `persist() { return nil }` stub in persist.go so `go build ./internal/state/...` passes Task 1's verify gate. Task 2's commit replaces the stub body with the real renameio wrapper. The plan's Task 1 done-line ('tests are still red because persist.go is not yet written') was contradicted by Task 1's own `go build` verify, so we satisfied both by shipping a stub method in a real persist.go that Task 2 overwrites. Two atomic commits, each buildable in isolation."

patterns-established:
  - "Per-task atomic commits, each leaving HEAD buildable: stub-then-replace is preferred over forward-references-that-do-not-compile when a plan has interdependent tasks."
  - "Inline rationale citing research-doc landmarks (A5 / Pitfall 7 / renameio issue #11). Future cleanup PRs that delete the wrapper have to argue against named, dated landmarks."

requirements-completed:
  - FOUND-02
  - STATE-01
  - STATE-02
  - STATE-03

# Metrics
duration: ~20min (most of it `-race -count=10` runtime: 98s per cycle)
completed: 2026-05-13
---

# Phase 01 Plan 02: Atomic JSON State Store Summary

**Three-file state package with renameio.WriteFile + explicit parent-directory fsync wrapper closing the host-power-loss durability window — drives plan 01-01's four RED Wave-0 tests to GREEN under `go test ./internal/state/... -race -count=10`.**

## Performance

- **Duration:** ~20 min wall-clock (test runtime alone: ~100s × 4 = ~6.7 min of compute)
- **Started:** 2026-05-13T12:58Z
- **Completed:** 2026-05-13T13:10Z
- **Tasks:** 2 / 2
- **Files created:** 3 (schema.go, store.go, persist.go)
- **Files modified:** 1 (go.mod — renameio/v2 indirect→direct)

## Accomplishments

- **Atomic write under contention proven:** TestPersistAtomicity (1000 concurrent writes + tight-loop read goroutine, `-race -count=10`) passes consistently. The reader never sees a torn JSON document.
- **Host-power-loss durability window closed:** Bare renameio.WriteFile leaves the rename durable across process crash but not host power loss. The `os.Open(filepath.Dir(s.path)).Sync()` wrapper closes it. Inline comment cites research correction A5, Pitfall 7, and renameio issue #11 so a future "simplification" PR has to argue against named landmarks.
- **Cold-boot path handles all four file states:** missing → create empty + persist; empty (0 bytes) → same; corrupted → error mentioning "decode" (operator-visible signal, never silent reset); valid → unmarshal verbatim.
- **Locked-snapshot RWMutex pattern locked in:** Update acquires the write lock, mutates via callback, calls persist while still locked. Get returns a shallow copy under RLock. Plan 04's HTTP handler can import state.Store directly with no extra synchronization.
- **Zero new dependencies:** renameio/v2 was already declared (as indirect) by plan 01-01; this plan promoted it to direct via `go mod tidy`.
- **STATE-01 invariant held:** `grep -rn 'sqlite\|mongo\|redis' --include='*.go' internal/state/` returns nothing.

## Task Commits

Each task was committed atomically via `gsd-sdk query commit`:

1. **Task 1: schema + Store skeleton (boot path, in-memory, forward-ref persist)** — `b6555ce` (feat)
   - internal/state/schema.go, internal/state/store.go, internal/state/persist.go (stub)
2. **Task 2: persist.go — renameio + explicit parent-dir fsync wrapper** — `e08714d` (feat)
   - internal/state/persist.go (real implementation), go.mod (renameio indirect→direct)

_Note: Plan 01-02 is TDD-as-a-whole (tasks tdd="true") rather than per-task RED/GREEN. The RED tests were authored upstream in plan 01-01 (commit `5a94d6f`); these two commits collectively form the GREEN gate. No REFACTOR commit was needed — the renameio wrapper landed in its final form on first write._

**Plan metadata:** _will land in the final docs commit produced by the executor after STATE/ROADMAP/REQUIREMENTS updates._

## Files Created/Modified

- `internal/state/schema.go` — `const SchemaVersion = 1`, `Container{Service, Image, Tag, CurrentDigest, PreviousDigest, UpdateAvailable}`, `State{Version, Containers map[string]Container}`. JSON tags match RESEARCH.md §"tygo configuration" lines 420-456 so plan 01-03's tygo run produces consistent TS types.
- `internal/state/store.go` — `Store{path, mu sync.RWMutex, state State}`. `NewStore(path)` handles four boot paths (missing / empty / corrupted / valid). `Get()` returns a shallow snapshot under RLock. `Update(fn func(*State)) error` acquires the write lock, invokes fn, repopulates Containers if nil, calls persist.
- `internal/state/persist.go` — `(s *Store) persist()` marshals s.state to indented JSON, calls `renameio.WriteFile(s.path, data, 0o644)`, then opens the parent directory and calls `dir.Sync()` (best-effort). Doc comment cites research correction A5, Pitfall 7, renameio issue #11, and the caller-must-hold-write-lock invariant.
- `go.mod` — `github.com/google/renameio/v2 v2.0.2` promoted from `// indirect` to a direct dependency.

## Decisions Made

See `key-decisions` in the frontmatter for the full list. Highlights:

1. **Shipped the dir-fsync wrapper (Option 2)**, not bare renameio. The ~50µs cost is invisible at v1's write rate (handful per minute); the durability win matters because HMIs power-cycle without ceremony and Phase 4's SIGKILL fault test would have flaked otherwise.
2. **Dir-fsync is best-effort.** If `os.Open(filepath.Dir)` fails, persist still returns nil — the rename is visible to subsequent opens via the kernel page cache, only host-power-loss-in-that-exact-window is at risk. Documented inline so reviewers do not "fix" it into a hard error.
3. **Empty-file boot path treated as missing.** A previous process might crash between `os.Create` and the first `WriteFile`. Without this branch, the next boot would decode 0 bytes as a corrupted file and demand operator intervention for a self-recoverable condition.
4. **Task 1 ships a stub `persist() { return nil }`** in a real `persist.go` so `go build` passes its verify gate; Task 2 overwrites it with the renameio wrapper. The plan's Task 1 done-line was internally inconsistent (verify required build, done-line said persist.go didn't exist yet) — stub-then-replace honors both constraints with two atomic, individually-buildable commits.

## Deviations from Plan

**1. [Rule 3 - Blocking] Task 1 ships a placeholder `persist()` body to satisfy Task 1's `go build` verify gate**

- **Found during:** Task 1 (schema + Store skeleton)
- **Issue:** Plan 01-02's Task 1 `<verify>` block runs `go build ./internal/state/...`, but the `<done>` description says "tests are still red because persist.go is not yet written" — a contradiction since `store.Update` forward-references `s.persist()` and Go does not link a binary with undefined methods. Per executor Rule 3 (blocking issue, auto-fix), the cleanest resolution is to ship a real `persist.go` with a `return nil` stub at Task 1 and replace the body with the renameio wrapper at Task 2.
- **Fix:** `internal/state/persist.go` lands at Task 1 with a 7-line stub method and a comment explicitly marking it as a placeholder for Task 2. Task 2 overwrites the function body with the real implementation. Both commits build in isolation; only the second one makes the tests green.
- **Files modified:** internal/state/persist.go (Task 1 stub → Task 2 real body)
- **Verification:** `go build ./internal/state/...` passes after each commit; `go test ./internal/state/... -race -count=10` is green after `e08714d`.
- **Committed in:** `b6555ce` (Task 1 stub), `e08714d` (Task 2 real body)

**2. [Rule 3 - Blocking] `go.mod` promoted renameio/v2 from indirect to direct**

- **Found during:** Task 2 (real persist.go)
- **Issue:** Plan 01-01 declared `github.com/google/renameio/v2 v2.0.2 // indirect` in go.mod. The moment `persist.go` imports the package directly, `go build` removes the `// indirect` marker. The plan does not call this out as an expected modification, but it's a mechanical consequence of the import.
- **Fix:** Ran `go mod tidy`; staged the resulting go.mod diff with the Task 2 commit.
- **Files modified:** go.mod
- **Verification:** `grep '// indirect' go.mod | grep renameio` returns empty; `go build` clean.
- **Committed in:** `e08714d` (Task 2 commit)

---

**Total deviations:** 2 auto-fixed (both Rule 3 — blocking-issue resolutions; neither involves new behavior, both unblock the literal verify gates the plan ships)
**Impact on plan:** No scope creep. Both deviations are mechanical: a stub-then-replace pattern to satisfy interdependent task verifies, and a go.mod indirect→direct promotion that is a side effect of the import the plan explicitly mandates.

## Issues Encountered

- **renameio is registered as indirect.** Mechanically resolved by `go mod tidy` (see Deviation #2). Took ~5 seconds to identify and fix.
- **`-race -count=10` test runtime is ~100s.** This is expected — the contention test does 1000 writes × 10 cycles × race instrumentation overhead. Not an issue, just a planning datapoint for CI budget. The grep-only static gates run in <1s, so they're suitable for fast pre-commit checks.

## Threat Flags

None. All surface area introduced by this plan was already enumerated in the plan's `<threat_model>` register (T-01-02-01 through T-01-02-05). The dir-fsync wrapper directly implements the mitigation for T-01-02-02; renameio.WriteFile mitigates T-01-02-01; the "decode" error message satisfies T-01-02-05. T-01-02-03 (ENOSPC) and T-01-02-04 (world-readable 0o644) remain accepted-risk per the register.

## Next Phase Readiness

- **Plan 01-03 (tygo + Container types):** unblocked. `internal/state.Container` has the canonical field set; plan 03's `internal/api.Container` will mirror it for the tygo TypeScript codegen.
- **Plan 01-04 (HTTP server + Playwright wiring):** unblocked. `state.NewStore` and `state.Store.Get` are ready to be called from `main.go`; the GET /api/state handler will be `json.NewEncoder(w).Encode(store.Get())`.
- **Phase 2 (docker-events + cron consumers):** the `state.Update(fn func(*State)) error` API is what those consumers will call. RWMutex contract is documented in the godoc.
- **Phase 4 (SIGKILL fault-injection):** the parent-dir fsync wrapper is in place. SIGKILL between renameio's rename and the next write should leave a parseable JSON file at s.path — exactly what the fault test asserts.

## Self-Check: PASSED

- `internal/state/schema.go`: FOUND
- `internal/state/store.go`: FOUND
- `internal/state/persist.go`: FOUND
- Commit `b6555ce` (Task 1): FOUND in `git log`
- Commit `e08714d` (Task 2): FOUND in `git log`
- `go test ./internal/state/... -race -count=10`: exit 0
- `grep -E "filepath.Dir|os\.OpenFile.*O_DIRECTORY|\.Sync\(\)" internal/state/persist.go`: matches found
- `grep "research correction A5\|Pitfall 7\|renameio.*issue.*11\|issue #11" internal/state/persist.go`: matches found
- `grep -rn "sqlite\|mongo\|redis" --include='*.go' internal/state/`: no matches (STATE-01 held)
- `grep "const SchemaVersion = 1" internal/state/schema.go`: match found
- `grep "renameio.WriteFile" internal/state/persist.go`: match found

---
*Phase: 01-walking-skeleton-test-harness*
*Completed: 2026-05-13*
