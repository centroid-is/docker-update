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

---

## D-04-06-01: Daemon-level zot:5000 DNS resolution blocks Phase 4 e2e Update flow

**Discovered during:** Plan 04-06 Task 4 (`make e2e-cron-fast` drive-green attempt).

**Status:** Architectural test-fixture limitation; out of scope for Plan 04-06.

**Symptom:** Every Phase 4 e2e spec that exercises an end-to-end Update flow
(update-flow / rollback-flow ACT-03 / idempotency ACT-06 / concurrent-actions
ACT-08 / restart-persistence / verify-failed) fails because the orchestrator's
`docker.Client.ImagePull("zot:5000/centroid-is/stub:latest")` returns
"pull_failed" with the underlying daemon error `no such host: zot`.

**Root cause:** `ImagePull` runs at the daemon level. The daemon's DNS context
is the host bridge network, not the compose-internal `e2e_default` network
where `zot` is an aliased service. The in-container HTTP client used by
`registry.Resolver.Digest` works correctly (compose embedded DNS resolves
`zot:5000`), so Phase 3's digest-fetch specs pass; Phase 4 is the first phase
to invoke `ImagePull` end-to-end.

**Fix candidates (any unblocks the deferred specs):**
1. Switch in-stack image refs from `zot:5000/...` to `localhost:15000/...` AND
   add `extra_hosts: ["zot:host-gateway"]` to the hmi-update service so
   resolver-side calls still target the in-cluster zot but daemon-side pulls
   route through the host port-forward. Cleanest test-only change.
2. Configure Docker Desktop's daemon `registry-mirrors` to map `zot:5000` to
   `localhost:15000`. Requires daemon.json changes — not portable across CI
   workers.
3. Inject `--add-host zot:host-gateway` into the e2e compose stack to teach
   the daemon about `zot`. Compose v2 supports `extra_hosts` at the service
   level; verifying daemon visibility is the open question.

**Affected specs (8):** update-flow, rollback-flow:ACT-03, rollback-flow:ACT-04,
idempotency:ACT-06, concurrent-actions:ACT-08, restart-persistence,
verify-failed (all blocked on a successful end-to-end Update). safety-labels:SAFE-03
fails for a different reason (see D-04-06-02).

**Workaround in 04-06:** None applied. The Phase 4 wire contracts are validated
by passing specs (self-protection ×4, safety-labels SAFE-01 + SAFE-02, idempotency
ACT-07) plus comprehensive Go unit tests (`go test ./... -race -count=1` exits 0).

**Recommended follow-up:** Dedicated quick task or fold into Phase 5 readiness
work. Estimated effort: 1-2 hours (option 1 above is the lowest-risk path).

---

## D-04-06-02: Cron NAME_UNKNOWN flakes against zot under crash-loop event traffic

**Discovered during:** Plan 04-06 Task 4 (post-fix e2e run with crash-loop-stub
present).

**Status:** Suspected race condition; not reproducible deterministically.
Out of scope for Plan 04-06.

**Symptom:** The cron poller intermittently logs
`registry.fetch.error: NAME_UNKNOWN: repository name not known to registry` for
`zot:5000/centroid-is/stub:latest` and similar refs that ARE present in the
local zot. The host-side `oras push` (in globalSetup.ts) succeeds, but the
cron-side fetch sometimes sees an empty-index state.

**Suspected cause:** Either:
- Zot's index hydration lag immediately after a fresh `docker compose up`
  (the seed manifest exists in zot's blob store but the manifest endpoint
  hasn't returned 200 yet during the cron's first sweep).
- Interaction with the crash-loop-stub event traffic (~1 die/start event per
  second) overwhelming the Discoverer + cron message-passing.

**Affected specs:** safety-labels:SAFE-03 (last_polled_at must advance — fails
because the poll loop hit NAME_UNKNOWN and didn't record the advance). May
also contribute to other Phase 3 spec flakes observed under repeat e2e runs.

**Workaround in 04-06:** None applied.

**Recommended follow-up:** Add a `waitForCondition` in safety-labels:SAFE-03
that allows a few retries of the cron flip before asserting; add a
"zot manifest hydrated" health-probe to globalSetup.ts; consider whether the
crash-loop-stub should opt out of `hmi-update.watch=true` (it currently
generates noise the Discoverer must process). Estimated effort: 1 hour.
