---
phase: 04-update-rollback-force-pull-actions-safety-state-persistence
plan: 02
subsystem: infra
tags: [compose, os-exec, docker-cli, slog, sigterm, argv-discipline, sentinel-errors]

# Dependency graph
requires:
  - phase: 01-walking-skeleton
    provides: Phase 1 Runner interface stub (type Runner interface{}) and compose package skeleton
  - phase: 02-docker-client-compose-file-reader
    provides: compose.Reader (boot-snapshot drift detection), compose.ErrComposeFileMoved sentinel pattern, internal/compose/errors.go file convention
  - phase: 04-update-rollback-force-pull-actions-safety-state-persistence/01
    provides: state.Container ActionInFlight/ActionError fields and poll.UpdateKind action variants (consumers of this runner's return values)
provides:
  - "compose.Runner interface (UpdateService + ComposePath; two methods, no more)"
  - "compose.NewRunner(composePath) constructor with exec.LookPath fail-fast (T-04-02-05)"
  - "compose.ErrComposeFailed sentinel for errors.Is branching by action handlers"
  - "execRunner with argv-discipline body (literal slice — Pitfall 13 prevention)"
  - "cmd.Cancel = SIGTERM + cmd.WaitDelay = 10s ctx-aware shutdown contract"
  - "stderr capture into wrap chain; 4096-byte truncated tail in slog for bounded log entries"
  - "compose.run slog event (dotted convention) — Info on success, Error on failure"
  - "commandRunner test seam (cmdFactory field) for fake-exec injection"
affects: [04-03 action-handlers-orchestrator, 04-04 http-action-handlers, 04-05 verify-after-recreate, plan-04-03 orchestrator construction]

# Tech tracking
tech-stack:
  added: [] # no new go.mod deps; uses stdlib os/exec, syscall, log/slog, bytes
  patterns:
    - "facade-over-CLI pattern (sibling to internal/docker's facade-over-SDK and internal/registry's facade-over-go-containerregistry)"
    - "test seam via package-private function-field (commandRunner type + cmdFactory field) — first os/exec facade in repo; sets the pattern for future Cmd-based facades"
    - "argv-discipline grep gate — the verbatim literal '\"compose\", \"-f\", r.composePath, \"up\", \"-d\", \"--force-recreate\", service' is pinned by both unit-test deep-equal AND project-level acceptance grep"
    - "cmd.Cancel + cmd.WaitDelay graceful-shutdown contract for any future os/exec wrappers"
    - "stderr truncation with '...[truncated]...' marker + last-N-bytes tail — bounded-log-line pattern reusable for future subprocess wrappers"

key-files:
  created:
    - internal/compose/runner_test.go
    - .planning/phases/04-update-rollback-force-pull-actions-safety-state-persistence/deferred-items.md
  modified:
    - internal/compose/runner.go
    - internal/compose/errors.go

key-decisions:
  - "Runner interface stays at exactly two methods (UpdateService, ComposePath); no force-pull-without-recreate method here (that goes through docker.Client.ImagePull directly per CONTEXT.md Area 1)"
  - "cmd.Cancel = SIGTERM (override default SIGKILL) + cmd.WaitDelay = 10s — matches docker compose's stop_grace_period default so interactive and API behavior align"
  - "Stderr truncated to last 4096 bytes for slog (bounded log entry); FULL stderr stays in error wrap chain for HTTP-layer surfacing"
  - "Slog event name 'compose.run' (dotted convention per Pattern G) — same name for success and failure; Info vs Error level + presence of stderr_snippet attr discriminate"
  - "Test portability: resolve true/false/sh/head/tr/sleep via exec.LookPath at package-test init time (before any t.Setenv strips PATH) with fallback to /usr/bin and /bin — macOS lacks /bin/true, /bin/false, /usr/bin/sleep"

patterns-established:
  - "Pattern: facade-over-CLI with exec.LookPath at constructor (fail-fast at boot) + commandRunner test seam (function-field) for fake injection"
  - "Pattern: argv-discipline grep gate as a project-level success criterion — pin the verbatim wire shape so future 'simplification' that joins args into a shell string is rejected at the gate"
  - "Pattern: dual-purpose stderr capture — full in error wrap chain, truncated tail in slog with '...[truncated]...' marker"

requirements-completed: [ACT-01, ACT-03, ACT-05, ACT-10, OBS-01]

# Metrics
duration: 17min
completed: 2026-05-15
---

# Phase 04 Plan 02: Compose Runner — exec.CommandContext body with argv discipline + SIGTERM grace Summary

**Production execRunner replaces the Phase 1 `type Runner interface{}` stub with a `docker compose -f <path> up -d --force-recreate <service>` invoker that pins the wire shape via an exec.CommandContext literal argv slice (Pitfall 13 defense in depth), grants 10s SIGTERM grace before SIGKILL on ctx cancel, captures stderr into both the wrap chain and a bounded slog snippet, and wraps a new ErrComposeFailed sentinel for errors.Is branching.**

## Performance

- **Duration:** 17 min
- **Started:** 2026-05-15T07:41:00Z
- **Completed:** 2026-05-15T07:58:06Z
- **Tasks:** 2 (TDD pair: RED tests then GREEN body)
- **Files modified:** 3 (runner.go, runner_test.go, errors.go)
- **Files created:** 1 (runner_test.go); deferred-items.md is plan-housekeeping not a code artifact

## Accomplishments

- Runner interface tightened from empty stub to `{UpdateService(ctx, service) error; ComposePath() string}` — exactly the two-method shape Plan 04-03's Orchestrator and Plan 04-04's HTTP handlers depend on.
- `compose.ErrComposeFailed` sentinel added in `internal/compose/errors.go` alongside `ErrComposeFileMoved` — establishes the codebase's second compose-package sentinel with the canonical wrap chain convention.
- `execRunner` body lands the canonical RESEARCH.md Pattern 1: `exec.LookPath("docker")` at construction (fail-fast at boot — T-04-02-05); literal argv slice (no shell interpolation possible — T-04-02-01); `cmd.Cancel = SIGTERM` override + `cmd.WaitDelay = 10*time.Second` (T-04-02-02 / 03); stderr captured into wrap chain with 4096-byte truncated tail for slog.
- 9 RED-first tests drove the implementation: compile-time Runner interface guard, NewRunner fail-fast on missing docker, NewRunner happy path, argv-discipline pin (literal-element-7 service name), exit-0 happy path, exit-nonzero ErrComposeFailed wrap, stderr-truncated to 4096 bytes with `...[truncated]...` marker, ctx-cancel-SIGTERM-within-11s deadline, composePath survives to argv[2]. All pass under `-race -count=5` on the cancel path and `-race -count=2` across the full compose package.
- commandRunner function-field test seam established as the project's first `os/exec` facade test pattern — sets the model for any future subprocess wrappers.

## Task Commits

Each task was committed atomically:

1. **Task 1: RED-first runner_test.go with full test contract + ErrComposeFailed sentinel** — `23c1243` (test)
2. **Task 2: execRunner body — argv discipline + stderr capture + cmd.Cancel/WaitDelay + slog event** — `89cd260` (feat)

**Plan metadata commit:** will follow (this SUMMARY.md)

## Files Created/Modified

- `internal/compose/runner.go` — Replaced `type Runner interface{}` stub (15 LOC) with two-method interface + commandRunner test seam type + execRunner struct + NewRunner constructor + UpdateService body + ComposePath getter (~190 LOC). Long-form package-preamble doc comment includes security note on Pitfall 13 (argv discipline) and cmd.Cancel rationale (SIGTERM vs SIGKILL default).
- `internal/compose/runner_test.go` — NEW. 9 tests covering compile-time interface pin, fail-fast constructor, argv discipline, happy path, non-zero exit, stderr truncation, ctx cancel within WaitDelay, composePath pass-through. Portable across Linux and macOS via mustResolveBinary helper.
- `internal/compose/errors.go` — Appended `ErrComposeFailed` sentinel with rich godoc documenting the HTTP 500 wrap shape Plan 04-04 will produce, the ProcessState.ExitCode() read path, and the stderr-tail wrap-chain contract.
- `.planning/phases/04-.../deferred-items.md` — NEW. Documents the pre-existing `internal/registry/...` test timeout (verified to exist on bare main via `git stash`), out of scope for this plan.

## Decisions Made

- **Runner stays at two methods.** Force-pull-without-recreate calls `docker.Client.ImagePull` directly (CONTEXT.md Area 1); not exposed on Runner.
- **cmd.Cancel = SIGTERM, cmd.WaitDelay = 10s.** Matches docker compose's `stop_grace_period` default so interactive and API behavior produce identical container-shutdown traces.
- **Stderr dual-purpose:** full content into error wrap chain (for HTTP handler `reason` field), truncated 4096-byte tail with `...[truncated]...` marker into slog (for bounded log lines).
- **Slog event name `compose.run` (dotted convention per Pattern G).** Same name on success and failure; level + presence of `err`/`stderr_snippet` attrs are the operator's discriminators.
- **Argv-discipline grep gate is load-bearing.** The literal `"compose", "-f", r.composePath, "up", "-d", "--force-recreate", service` AND a project-level grep on `"docker", "compose", "-f"` are pinned by both unit-test DeepEqual and acceptance grep — any future refactor that joins these into a shell-style string is rejected at the gate.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Test portability across macOS (binary paths)**

- **Found during:** Task 2 (first `go test ./internal/compose/...` run after GREEN body landed)
- **Issue:** RED tests used `/bin/true` and `/bin/false` directly; on macOS these paths do not exist (only `/usr/bin/{true,false}`). `sh -c "head -c 10000 ..."` also failed because `t.Setenv("PATH", stubDir)` stripped PATH down to a tempdir containing only the fake `docker` stub, so the spawned shell couldn't resolve `head`/`tr`/`sleep`. All five tests using `/bin/true`, `/bin/false`, or shell commands failed on first GREEN run.
- **Fix:** Added a `mustResolveBinary(name)` helper that tries `exec.LookPath` first (with the real PATH available at package-test init time), then falls back to `/usr/bin/<name>` and `/bin/<name>`. Package-level `var binTrue, binFalse, binSh, binHead, binTr, binSleep` capture absolute paths once — before any `t.Setenv` per-test override. Tests then reference these absolute paths so PATH-stripping doesn't break them.
- **Files modified:** internal/compose/runner_test.go (var block + mustResolveBinary + 5 test sites updated)
- **Verification:** `go test ./internal/compose/... -race -count=2` passes (full suite, 21.6s); cancel-path `-race -count=5` passes (51.7s); 9/9 tests green.
- **Committed in:** 89cd260 (Task 2 commit — folded in because the test fix is part of making the GREEN gate work locally)

---

**Total deviations:** 1 auto-fixed (1 bug — test environment portability)
**Impact on plan:** No scope creep. The fix is a portability shim in test code only; production code is unaffected. The argv-discipline contract, the cmd.Cancel/WaitDelay contract, and the slog schema all land as specified in the plan.

## Issues Encountered

- **Pre-existing registry test timeout:** `internal/registry/...` tests time out after 30s (panics with stack trace pointing at `os/exec.Cmd.Start` reading file). Verified to exist on bare main via `git stash` — completely independent of this plan's changes (this plan only touches `internal/compose/`). Logged in `deferred-items.md`.
- **Cancel-path test takes ~10.1s wall-clock per iteration:** The `TestUpdateService_CtxCancel_SendsSIGTERM_Within10s` test cancels ctx after 100ms with a subprocess running `trap 'exit 0' TERM; sleep 30`. Most shells do NOT process trapped signals while in a blocking `sleep` syscall — sh waits for sleep to return before firing the TERM trap, so SIGTERM goes to the shell, sh's sleep child gets nothing, sh waits ~10s for WaitDelay's SIGKILL. This is the *expected* observable behavior of the contract (WaitDelay caps total wait at 10s) and the test deadline is 11s — under deadline, so PASS. Documented in test comments. The integration test in Plan 04-05 (SIGKILL fault injection) will exercise the actual graceful-shutdown path with a real docker compose subprocess that respects SIGTERM.

## User Setup Required

None — no external service configuration required. The runner relies on the host's `docker` CLI being in PATH; on the production HMI this is guaranteed by the Debian Docker Engine installation. The Phase 4 deployment notes (in 04-CONTEXT.md "Configuration Knobs") document the env vars (`HMI_UPDATE_COMPOSE_PATH`, etc.) but no new env var is introduced by this plan.

## Next Phase Readiness

**Ready for Plan 04-03 (Wave 3 — actions/orchestrator body):**

- `compose.NewRunner(composePath string) (Runner, error)` is the call site for orchestrator construction. Wire in `cmd/hmi-update/main.go` between `compose.NewReader` and `registry.NewResolver` per CONTEXT.md "Integration Points".
- `compose.ErrComposeFailed` is the sentinel orchestrator branches on with `errors.Is(err, compose.ErrComposeFailed)`. The wrap chain preserves the underlying `*exec.ExitError` for ExitCode reads via `errors.As` if needed.
- `Runner.UpdateService(ctx, service)` is the action handler's recreate call — invoked after `composeReader.CheckUnchanged(ctx)` succeeds and after `docker.Client.ImagePull` for the Update flow; called directly for the Rollback flow (after `docker.Client.ImageTag` re-tags to the previous digest); called optionally for `force-pull?recreate=true`.
- Stderr surfaces through the wrap chain to the orchestrator → HTTP handler as the `reason` field in the 500 JSON body (Plan 04-04).

**No blockers for downstream waves.** This plan is independent of Plan 04-05 (different files) and runs parallel in Wave 2 per the plan's wave assignment.

## Self-Check: PASSED

- internal/compose/runner.go — FOUND (modified, 190 LOC)
- internal/compose/runner_test.go — FOUND (created, 380 LOC, 9 tests)
- internal/compose/errors.go — FOUND (modified, ErrComposeFailed appended)
- Commit 23c1243 (Task 1: RED tests + sentinel) — FOUND in git log
- Commit 89cd260 (Task 2: execRunner body + macOS test portability fix) — FOUND in git log
- All project-level success criteria green (build, race-tests, argv-grep gate, no shell interpolation grep, cmd.WaitDelay + cmd.Cancel grep)
- STATE.md untouched (per phase_context instructions); ROADMAP.md untouched

---
*Phase: 04-update-rollback-force-pull-actions-safety-state-persistence*
*Plan: 02*
*Completed: 2026-05-15*
