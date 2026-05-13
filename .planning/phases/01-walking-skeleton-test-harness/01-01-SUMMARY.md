---
phase: 01-walking-skeleton-test-harness
plan: 01
subsystem: infra
tags: [go, go-modules, embed, gitignore, playwright, renameio, scaffolding, red-first, c4-tdd]

# Dependency graph
requires:
  - phase: 00 (none — this is the first executor for the project)
    provides: empty repo with .planning/ tree only
provides:
  - Compilable Go module at github.com/centroid-is/hmi-update (go 1.26)
  - Five stub-interface packages (docker, registry, poll, compose, actions) so internal/api can compile in plan 04 before phases 2-4 land
  - api.State + api.Container stub types — feed for tygo in plan 03
  - internal/api/dist/.gitkeep — load-bearing placeholder so plan 04's //go:embed all:dist resolves at compile time
  - Four RED-first state-store unit tests (TestPersistAtomicity, TestLoadAndPersist, TestMissingFile, TestCorruptedFile) — drive plan 02 green
  - One RED-first Playwright smoke test (e2e/tests/smoke.spec.ts) — drives plan 04 green
  - .gitignore with the negation trick that actually works (internal/api/dist/* not internal/api/dist/)
affects: [phase-01-02 state store, phase-01-03 tygo types, phase-01-04 HTTP server + embed + Playwright wiring]

# Tech tracking
tech-stack:
  added:
    - github.com/google/renameio/v2 v2.0.2 (Go dep, runtime — used by plan 02)
    - "@playwright/test ^1.60.0 (e2e devDep, declared only — not yet installed)"
    - "@types/node ^22.0.0 (e2e devDep, declared only)"
  patterns:
    - "RED-FIRST per C4: tests authored before implementation; go test exits non-zero by design"
    - "stub interface packages: type X interface{} + TODO(phase-N) comment so downstream importers compile before bodies land"
    - "//go:embed all:dist placeholder strategy: ship a tracked .gitkeep so compile resolves before Vite ever runs"

key-files:
  created:
    - go.mod
    - go.sum
    - .gitignore
    - cmd/hmi-update/main.go
    - internal/api/types.go
    - internal/api/dist/.gitkeep
    - internal/docker/client.go
    - internal/registry/resolver.go
    - internal/poll/poller.go
    - internal/compose/runner.go
    - internal/actions/orchestrator.go
    - internal/state/persist_test.go
    - internal/state/store_test.go
    - e2e/package.json
    - e2e/tests/smoke.spec.ts
  modified: []

key-decisions:
  - "Go module declares go 1.26 (not the brief's 1.23 — 1.23 went EOL 2026-02-11 per STACK.md)"
  - "renameio/v2 pinned at v2.0.2 (current as of 2026-05-13)"
  - ".gitignore uses internal/api/dist/* (not internal/api/dist/) — git cannot re-include a file under an excluded directory; this is the only pattern that actually keeps .gitkeep tracked. The plan's literal verify grep for '^internal/api/dist/$' was contradicted by the plan's own intent; functional correctness takes precedence."
  - "TestPersistAtomicity reader goroutine uses t.Errorf (not t.Fatal) — t.Fatal in a goroutine doesn't propagate"
  - "Dir-fsync wrapper recommended for plan 02 (per RESEARCH.md Pitfall A): renameio.WriteFile does NOT fsync the parent directory; plan 02's persist() should follow the wrapper sample in RESEARCH.md §'renameio/v2 API and the directory-fsync correction'"

patterns-established:
  - "Conventional commits with (phase-NN) scope — chore/test/feat/fix"
  - "RED-first marker comment in every red test: 'RED-FIRST per C4. This test is authored before <symbol> exists. Plan NN drives it green.'"
  - "Stub interface package layout: each interface in its own package with TODO(phase-N) anchor for future maintainers"

requirements-completed:
  - FOUND-01  # Repo skeleton present (cmd/, internal/, go.mod) — verified by `test -d cmd/hmi-update && test -d internal/state && test -f go.mod`

# Metrics
duration: 7min
completed: 2026-05-13
---

# Phase 01 Plan 01: Walking Skeleton & Test Harness (Wave 1) Summary

**Greenfield repo scaffold: compileable Go module with five stub-interface packages, four RED-first state-store unit tests, and one RED-first Playwright smoke test — every contract surface plan 02/03/04 will drive green is now in place.**

## Performance

- **Duration:** ~7 min
- **Started:** 2026-05-13T12:50:24Z
- **Completed:** 2026-05-13T12:57:03Z
- **Tasks:** 3 / 3
- **Files created:** 15 (one of which — internal/api/dist/.gitkeep — is intentionally empty)
- **Files modified:** 0

## Accomplishments

- **Repo compiles end to end:** `go build ./...` exits 0 from a clean checkout. The five stub-interface packages (`internal/{docker,registry,poll,compose,actions}`) each declare their single named interface so `internal/api` in plan 04 can import them without scavenger-hunting.
- **`internal/api/dist/.gitkeep` shipped and *actually tracked*:** required deviation from the plan's literal gitignore syntax to make this work — see Deviations below. Without this file, plan 04's `//go:embed all:dist` fails at compile time.
- **Wave-0 state-store tests in RED:** four tests (TestPersistAtomicity / TestLoadAndPersist / TestMissingFile / TestCorruptedFile) authored before plan 02's Store / NewStore / Update / Get symbols exist. `go test ./internal/state/...` exits 1 with `undefined: NewStore` — the deliberate RED signal.
- **Wave-0 Playwright smoke spec in RED:** asserts every Phase-1 acceptance surface from UI-SPEC.md and PITFALLS.md Pitfall 8 — `/healthz` 200, seven UI-SPEC column slugs in order, `colspan="7"` empty-state row with verbatim heading, `/api/state` JSON shape, strict `/assets/*` no-fallback, explicit `application/javascript; charset=utf-8` MIME, and `Cache-Control: public, max-age=31536000, immutable`.
- **Dir-fsync correction logged for plan 02:** RESEARCH.md Pitfall A is a known-but-easily-missed semantic gap in `renameio.WriteFile`. Logged here as a key decision so the plan-02 executor doesn't ship persist() without it.

## Task Commits

Each task was committed atomically per the GSD SDK commit helper:

1. **Task 1: Repo skeleton + go.mod + .gitignore + stub packages** — `0a52b9e` (chore)
   - 11 files: `.gitignore`, `go.mod`, `go.sum`, `cmd/hmi-update/main.go`, `internal/api/types.go`, `internal/api/dist/.gitkeep`, plus five stub package files.
2. **Task 2: Failing Go unit tests for state store (RED — drives plan 02 green)** — `5a94d6f` (test)
   - 2 files: `internal/state/persist_test.go`, `internal/state/store_test.go`.
3. **Task 3: Failing Playwright smoke test (RED — drives plan 04 green) + e2e package scaffold** — `628224b` (test)
   - 2 files: `e2e/package.json`, `e2e/tests/smoke.spec.ts`.

**Plan metadata commit:** will be created after this SUMMARY is written (per `<final_commit>` in execute-plan workflow).

## Files Created/Modified

### Module + tooling
- `go.mod` — `module github.com/centroid-is/hmi-update`, `go 1.26`, `require github.com/google/renameio/v2 v2.0.2`.
- `go.sum` — checksum locks for renameio/v2 dep graph.
- `.gitignore` — bin/, ui/dist/, ui/node_modules/, e2e/{node_modules,playwright-report,test-results}/, .DS_Store, plus the `internal/api/dist/* + !internal/api/dist/.gitkeep` pattern explained in deviation #1.

### Go scaffolding
- `cmd/hmi-update/main.go` — minimum compilable `package main; func main() {}`. Plan 04 wires the HTTP server.
- `internal/api/types.go` — stub `Container struct{}` and `State struct { Version int; Containers map[string]Container }` with json tags. tygo feed.
- `internal/api/dist/.gitkeep` — empty file. Load-bearing: ensures `//go:embed all:dist` (added in plan 04) finds a non-empty parent directory at compile time before Vite ever runs.
- `internal/docker/client.go` — `type Client interface{}` + `TODO(phase-2): implement`.
- `internal/registry/resolver.go` — `type Resolver interface{}` + `TODO(phase-3): implement`.
- `internal/poll/poller.go` — `type Poller interface{}` + `TODO(phase-3): implement`.
- `internal/compose/runner.go` — `type Runner interface{}` + `TODO(phase-4): implement`.
- `internal/actions/orchestrator.go` — `type Orchestrator interface{}` + `TODO(phase-4): implement`.

### Wave-0 tests (RED by design — see Known Stubs below for the "this is intentional, not a bug" framing)
- `internal/state/persist_test.go` — `TestPersistAtomicity`. Writer goroutine calls `s.Update(...)` 1000 times mutating `Containers["svc1"].Tag`; reader goroutine `os.ReadFile + json.Unmarshal` in a tight loop. Both goroutines use `t.Errorf` (not `t.Fatal`).
- `internal/state/store_test.go` — `TestLoadAndPersist`, `TestMissingFile`, `TestCorruptedFile`. Round-trip, cold-boot, and operator-visible-error-on-corruption paths.
- `e2e/package.json` — declares `@playwright/test ^1.60.0` and `@types/node ^22.0.0`. ESM `"type": "module"`. **No `npm install` ran** — that's a plan-04 CI concern.
- `e2e/tests/smoke.spec.ts` — single `test('smoke: …')` block asserting all eight Phase-1 surface contracts (healthz, table shell with 7 column slugs in order, empty-state colspan="7", /api/state shape, strict /assets/* no-fallback, JS MIME, immutable cache, no console errors).

## Decisions Made

- **Go 1.26, not the brief's 1.23.** Pinned in `go.mod`. Source: STACK.md ("1.23 is EOL"), CONTEXT.md ("Go version: 1.26 (research correction over brief's 1.23)").
- **renameio/v2 added at scaffold time** (rather than in plan 02) so plan 02's executor can `import "github.com/google/renameio/v2"` without a separate `go get`. Keeps plan 02's diff cleaner.
- **TestPersistAtomicity follows RESEARCH.md verbatim.** Reader goroutine pattern uses `t.Errorf`, includes an empty-file check (a torn write would manifest as either invalid JSON or `len(data) == 0`), and a `select { case <-stop: return; default: ... }` loop with `close(stop)` from the writer for clean teardown.
- **TestCorruptedFile asserts on error message contents.** Per plan: "operator-visible signal — not a silent reset". The test uses `strings.ToLower(err.Error())` and checks for substring `parse` or `decode` so plan 02's executor has flexibility in error wording (e.g., `json: cannot decode ...` or `parse state.json: ...`).
- **smoke.spec.ts uses `expect.any(Object)` for the `containers` field** of `/api/state`. Phase 1's empty-table acceptance requires `containers` to be present and an object; it does NOT yet need to be an empty object (the plan-04 implementation may seed it with the test compose stack's stub container). The `toMatchObject` shape match preserves that flexibility.
- **smoke.spec.ts discovers the JS asset path by regex-matching index.html** rather than hardcoding a filename. Vite emits content-hashed names; hardcoding would fail on every Vite rebuild.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 — Bug] .gitignore literal syntax is non-functional; corrected to actually work**

- **Found during:** Task 1 (verify step — `gsd-sdk commit` rejected `internal/api/dist/.gitkeep` as ignored).
- **Issue:** The plan's literal acceptance criterion specifies the pattern pair `internal/api/dist/` + `!internal/api/dist/.gitkeep`. Per git's [documented gitignore semantics](https://git-scm.com/docs/gitignore) — "It is not possible to re-include a file if a parent directory of that file is excluded" — this pair does NOT keep `.gitkeep` tracked. Once `internal/api/dist/` is excluded as a *directory*, git never descends into it, so the negation rule below it has no file to apply to. I verified this with `git check-ignore -v internal/api/dist/.gitkeep` and `git status --ignored`: with the literal-grep pattern in place, `.gitkeep` is reported ignored.
- **Fix:** Changed the pattern to `internal/api/dist/*` (matches the *contents*, so git still descends into the directory) followed by `!internal/api/dist/.gitkeep`. Re-verified with `git check-ignore -v internal/api/dist/.gitkeep` → no longer ignored, `git add internal/api/dist/.gitkeep` succeeds. The plan's *intent* ("keeps the placeholder tracked even though the directory contents are ignored") is preserved; the literal grep is sacrificed.
- **Why this is correct:** The plan's stated intent and its literal verify command are contradictory in git's semantics. They cannot both be satisfied. I chose the horn that preserves the stated *functional* requirement — `.gitkeep` must be tracked so `//go:embed all:dist` resolves in plan 04. The alternative (satisfy the literal grep and ship a `.gitkeep` that is silently ignored) would have broken plan 04's compile step. The .gitignore file includes an in-file comment documenting the choice so future readers (and any future planner re-running this) immediately understand why the pattern looks the way it does.
- **Files modified:** `.gitignore`
- **Verification:** `git check-ignore -v internal/api/dist/.gitkeep` → matches the negation rule (`.gitignore:16:!internal/api/dist/.gitkeep`), exit 0. Commit `0a52b9e` includes `internal/api/dist/.gitkeep` as a tracked file. `git ls-files internal/api/dist/.gitkeep` returns the path.
- **Committed in:** `0a52b9e` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 — corrected non-functional .gitignore syntax)
**Impact on plan:** Required for plan 04 to compile. No scope creep. The plan's *intent* (keep `.gitkeep` tracked) is preserved; only its non-functional literal grep is sacrificed. This deviation surfaces a planner-side issue that should be fed back: future iterations of similar plans should specify `internal/api/dist/*` in the acceptance criterion.

## Known Stubs

The following symbols are intentionally empty/stubbed in this plan. They are NOT bugs — the entire plan's purpose is to ship contracts as stubs that subsequent plans drive green:

| Stub | Location | Reason | Resolved by |
| --- | --- | --- | --- |
| `Container struct{}` (empty body) | `internal/api/types.go:23` | Phase 1 wire-type placeholder; full json-tagged fields land in plan 03 via tygo | Plan 01-03 (Wave 3) |
| `type Client interface{}` | `internal/docker/client.go:10` | Stub for downstream packages to import | Phase 02 (DOCK-01..04) |
| `type Resolver interface{}` | `internal/registry/resolver.go:10` | Stub for downstream packages | Phase 03 (DETECT-01..08) |
| `type Poller interface{}` | `internal/poll/poller.go:10` | Stub for downstream packages | Phase 03 (DETECT-09..12) |
| `type Runner interface{}` | `internal/compose/runner.go:10` | Stub for downstream packages | Phase 04 (ACTION-01..05) |
| `type Orchestrator interface{}` | `internal/actions/orchestrator.go:14` | Stub for downstream packages | Phase 04 (ACTION-01..05) |
| `func main() {}` | `cmd/hmi-update/main.go:10` | Minimum compilable main; plan 04 wires state.Store + api.Server | Plan 01-04 |
| `internal/api/dist/.gitkeep` (empty file) | dist/ placeholder | //go:embed all:dist needs the dir non-empty at compile time | Plan 01-04 (Vite emits real bundle) |
| `internal/state/{persist,store}_test.go` symbols (NewStore, Store, Update, Get, etc.) | RED-first test files | Test harness exists before implementation per C4 | Plan 01-02 (Wave 2 drives green) |
| `e2e/tests/smoke.spec.ts` (the whole test is RED) | e2e | Test harness exists before HTTP server / Vite build / compose stack per C4 | Plan 01-04 (Wave 3 drives green) |

All stubs are documented in-file with `TODO(phase-N): implement` anchors or `RED-FIRST per C4` comments.

## TDD Gate Compliance

This plan's frontmatter is `type: execute` (not `type: tdd` at plan level). Two of three tasks have `tdd="true"`:
- **Task 2 (state-store unit tests)** is the RED half of a TDD cycle whose GREEN half lives in plan 01-02. The plan deliberately splits RED and GREEN across plans for the "all of Wave 0 in one bucket" sequencing. The commit `5a94d6f` is the `test(...)` RED gate; the corresponding `feat(...)` GREEN gate must appear in plan 01-02.
- **Task 3 (Playwright smoke spec)** is the RED half of a longer TDD cycle whose GREEN half lives in plan 01-04. The commit `628224b` is the `test(...)` RED gate; the corresponding `feat(...)` / `chore(...)` GREEN gates must appear in plan 01-04.

**This plan owns ONLY the RED gates.** Verification of the corresponding GREEN gates is the responsibility of plans 01-02 and 01-04.

## Issues Encountered

- **gsd-sdk commit initially failed** because `internal/api/dist/.gitkeep` matched the (then-broken) `internal/api/dist/` gitignore pattern. Diagnosed via `git check-ignore -v`, fixed by switching to the `internal/api/dist/*` pattern. See deviation #1 above.
- No other issues. Both RED-signal verifications (state tests fail with `undefined: NewStore`; smoke.spec.ts has no server to talk to) behaved as expected by the plan.

## User Setup Required

None — no external service configuration required for this plan.

## Self-Check: PASSED

Files claimed created exist:

- ✓ `go.mod`
- ✓ `go.sum`
- ✓ `.gitignore`
- ✓ `cmd/hmi-update/main.go`
- ✓ `internal/api/types.go`
- ✓ `internal/api/dist/.gitkeep` (tracked; verified via `git ls-files`)
- ✓ `internal/docker/client.go`
- ✓ `internal/registry/resolver.go`
- ✓ `internal/poll/poller.go`
- ✓ `internal/compose/runner.go`
- ✓ `internal/actions/orchestrator.go`
- ✓ `internal/state/persist_test.go`
- ✓ `internal/state/store_test.go`
- ✓ `e2e/package.json`
- ✓ `e2e/tests/smoke.spec.ts`

Commits claimed exist in git history:

- ✓ `0a52b9e` — chore(phase-01): scaffold Go module, stub packages, and gitignore
- ✓ `5a94d6f` — test(phase-01): RED-first state-store unit tests (drives plan 02 green)
- ✓ `628224b` — test(phase-01): RED-first Playwright smoke spec + e2e package scaffold (drives plan 04 green)

Plan-level verification commands:

- ✓ `go build ./...` exits 0
- ✓ `go test ./internal/state/...` exits non-zero with `undefined: NewStore` (RED signal as designed)
- ✓ `grep -q "RED-FIRST per C4" e2e/tests/smoke.spec.ts` exits 0
- ✓ `git status --short` shows no modifications to `.planning/` or `CLAUDE.md` (only `hmi-update-brief.md` which pre-existed)

## Next Phase Readiness

- **Plan 01-02 (Wave 2 — state store implementation)** can begin immediately. The test harness is fully wired; the executor's job is to implement `internal/state/{schema.go, store.go, persist.go}` per the signatures the RED tests already call. The dir-fsync wrapper from RESEARCH.md §"renameio/v2 API and the directory-fsync correction" is the recommended persist() shape — see Decisions section above.
- **Plan 01-03 (Wave 3 — tygo + Container fields)** has its source file (`internal/api/types.go`) and the tygo dep needs only `tygo.yaml` + the expanded Container struct.
- **Plan 01-04 (Wave 3 — HTTP server + Vite + Playwright wiring)** has every stub interface it needs to import, the `.gitkeep` so its `//go:embed all:dist` will compile, and the smoke.spec.ts contract it must drive green.
- **No blockers.** The deferred items in the plan (e.g., `go vet ./...` was not part of acceptance — only `go build ./...`) remain deferred to their phases.

---

*Phase: 01-walking-skeleton-test-harness*
*Plan: 01-01*
*Completed: 2026-05-13*
