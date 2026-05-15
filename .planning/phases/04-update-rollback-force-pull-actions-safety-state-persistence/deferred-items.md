# Deferred Items (Phase 04)

Out-of-scope discoveries during plan execution. Tracked here, not fixed in-plan
per the SCOPE BOUNDARY rule.

## Pre-existing registry test timeout

**Discovered during:** Plan 04-02 execution (regression check)
**Status:** Exists on bare `main` (verified via `git stash`).
**Symptom:**
```
panic: test timed out after 30s
FAIL    github.com/centroid-is/hmi-update/internal/registry
```
**Likely cause:** A network-dependent test in `internal/registry/` is hitting
an external registry that doesn't respond within the test timeout. The
stack trace shows `os/exec.Cmd.Start` → goroutine reading file → blocked
on Read. May be `crane.Digest` against a registry hostname that isn't
reachable from the dev sandbox.

**Not in scope for 04-02:** This plan only touches `internal/compose/runner.go`,
`internal/compose/runner_test.go`, and `internal/compose/errors.go`. Compose
tests pass under `-race -count=2`. The registry failure is independent.

**Recommended follow-up:** Triage `internal/registry/*_test.go` for the test
that hangs; either add a `-short` skip, mock the network call, or wrap in
`testing.Short()` gate. Phase 4 plan owner can pick up in a separate quick
task or fold into Plan 04-05 (parallel wave 2 with 04-02).
