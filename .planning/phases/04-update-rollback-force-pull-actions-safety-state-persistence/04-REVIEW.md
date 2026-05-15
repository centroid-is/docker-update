---
phase: 04-update-rollback-force-pull-actions-safety-state-persistence
reviewed: 2026-05-15T00:00:00Z
depth: standard
files_reviewed: 18
files_reviewed_list:
  - internal/state/schema.go
  - internal/api/types.go
  - internal/poll/channel.go
  - internal/compose/runner.go
  - internal/compose/errors.go
  - internal/actions/orchestrator.go
  - internal/actions/mutex.go
  - internal/actions/middleware.go
  - internal/actions/verify.go
  - internal/actions/errors.go
  - internal/api/handlers_actions.go
  - internal/api/server.go
  - cmd/hmi-update/main.go
  - cmd/sigkillhelper/main.go
  - internal/actions/orchestrator_test.go
  - internal/actions/mutex_test.go
  - internal/actions/middleware_test.go
  - internal/actions/verify_test.go
  - internal/actions/handlers_actions_test.go
  - internal/api/getstate_noio_test.go
  - internal/state/store_sigkill_test.go
  - internal/compose/runner_test.go
  - e2e/fixtures/disconnect-network.ts
  - Dockerfile
findings:
  critical: 4
  warning: 6
  info: 4
  total: 14
status: issues_found
fixed:
  - BLOCKER-01
  - BLOCKER-02
  - BLOCKER-03
  - BLOCKER-04
  - WARNING-01
  - WARNING-02
  - WARNING-03
  - WARNING-04
  - WARNING-05
  - WARNING-06
fixed_at: 2026-05-15
fixed_by: gsd-code-fixer (auto mode, Critical + Warning scope)
deferred:
  - INFO-01
  - INFO-02
  - INFO-03
  - INFO-04
---

# Phase 4: Code Review Report

**Reviewed:** 2026-05-15
**Depth:** standard
**Files Reviewed:** 24 (production + test + e2e + Dockerfile)
**Status:** issues_found

## Summary

Phase 4 lands the headline differentiator — operator-driven Update / Rollback /
Force-pull actions with per-service mutex, safety labels, self-protection,
verify-after-recreate, and SIGKILL-resistant state persistence. The architectural
scaffolding (channel-driven state mutations, sentinel-error dispatch,
typed-inner-error contract, Pattern K verbatim bodies) is sound. Argv discipline
in `compose.execRunner` is solid; SIGKILL fault injection demonstrates the
renameio invariant holds; cross-package wire-contract constants prevent drift.

However, the review surfaces **four production-blocker bugs**:

1. **`inspectAndVerify` queries the OLD container ID after `docker compose up -d
   --force-recreate`.** The OLD container is destroyed by the recreate; the
   daemon returns 404 on `ContainerInspect(oldID)`, and the verify loop
   fails-fast with `ErrVerifyFailed`. Every successful recreate appears as a
   failed action to the operator. The unit tests do not catch this because the
   fakeDockerClient ignores the `id` argument. The Phase 4 Plan 04-03 SUMMARY
   acknowledges the gap but minimises it as "less diagnostic than it could be"
   — in fact it means the entire Update / Rollback / ForcePull-with-recreate
   happy path returns 500 verify_failed in production. The e2e specs that would
   have caught this are all deferred via `test.skip` to Plan 04-07 (Option D).

2. **HTTP `WriteTimeout=10s` < verify window 15s (default) / 60s
   (healthcheck opt-in).** The action handler cannot complete a normal Update
   within the server's write timeout. Clients see a connection failure after
   ~10 s; the orchestrator's ctx is cancelled mid-verify and converges to
   `ErrVerifyCanceled`. Combined with finding 1, no Update / Rollback flow
   reaches the operator's browser successfully.

3. **`compose.execRunner.UpdateService` drops the underlying `exec.Run()`
   error from the wrap chain.** The doc comment on `compose.ErrComposeFailed`
   explicitly claims the wrap chain preserves `*exec.ExitError` for `errors.As`
   extraction; the format string only wraps `ErrComposeFailed` and discards the
   original `err`. `errors.As(err, &exitErr)` returns false; `errors.Is(err,
   context.DeadlineExceeded)` returns false; `errors.Is(err,
   context.Canceled)` returns false. Contract violation between code and docs.

4. **`disconnectZotFromNetwork` reintroduces the WR-08 shell-interpolation
   anti-pattern that Phase 3 review fixed.** The `net` variable is derived
   from `docker network ls` and matched against a regex, then concatenated
   into an `execSync` template string. The fixture's own comment
   acknowledges this risk and proposes `execFileSync` as the safer path —
   adopt it.

The walking-skeleton wiring (state schema, channel kinds, mutex primitive,
middleware order, slog event schema, OBS-03 panicking-client guard, STATE-04
SIGKILL harness) all hold under review. Findings below are ranked by severity.

## Blocker Issues

### BLOCKER-01: `inspectAndVerify` queries pre-recreate container ID

**File:** `internal/actions/orchestrator.go:711-740`
**Issue:**

```go
func (o *actionOrchestrator) inspectAndVerify(ctx context.Context, service string, snapshot state.Container) error {
    // ... long acknowledgement comment ...
    snap := verifySnapshot{
        ContainerID:  snapshot.ContainerID,  // <- OLD container ID (pre-recreate)
        RestartCount: 0,
        ...
    }
    return o.verifyAfterRecreate(ctx, snap)
}
```

`docker compose up -d --force-recreate <svc>` destroys the OLD container and
creates a NEW one with a new ID. `verifyAfterRecreate` calls
`o.dockerInspector.ContainerInspect(ctx, snap.ContainerID)` (verify.go:228)
with the OLD ID; the daemon returns 404 ("No such container"), which trips
the fail-fast branch at verify.go:235–240 with `ErrVerifyFailed`. Every
successful recreate ships as a 500 to the operator.

The unit tests (`orchestrator_test.go::TestUpdate_HappyPath` and friends) all
pass because `fakeDockerClient.ContainerInspect` ignores the `id` argument
entirely (orchestrator_test.go:102-119) and returns scripted responses
indexed by call number. The 8 deferred Playwright specs (Plan 04-07) would
have caught this against a real docker daemon. The Plan 04-03 SUMMARY
classifies this as "NOT a regression — the failure mode is correctly
reported via ErrVerifyFailed" — review disagrees: a contract that surfaces
every successful recreate as a 500 verify_failed is broken.

**Fix:**

Extend the docker facade with a `ContainerByService(ctx, svc) (string, error)`
helper (or add the same to the compose Reader if compose project naming is
deterministic) and call it AFTER `runner.UpdateService` returns, BEFORE
`verifyAfterRecreate`:

```go
// internal/docker/client.go
type Client interface {
    // ... existing methods ...
    // ContainerByService returns the container ID currently associated with
    // the compose service label "com.docker.compose.service=<svc>".
    ContainerByService(ctx context.Context, svc string) (string, error)
}

// internal/actions/orchestrator.go::inspectAndVerify
newID, err := o.dockerClient.ContainerByService(ctx, service)
if err != nil {
    return fmt.Errorf("%w: %w", ErrVerifyFailed, &VerifyDetail{
        Reason: fmt.Sprintf("post-recreate ContainerByService(%s) failed: %v", service, err),
    })
}
snap := verifySnapshot{
    ContainerID: newID,
    // ... rest of the snapshot
}
```

Update the fakeDockerClient in orchestrator_test.go to enforce the new-ID
contract (refuse inspects on the OLD ID; return ID on the NEW one) so a
future regression of the same shape is caught.

**Severity rationale:** Headline differentiator does not function in
production. Every operator-initiated Update / Rollback / ForcePull-with-recreate
returns 500 verify_failed despite the recreate succeeding. Unit tests
falsely report green because the fake ignores the bug-relevant input. The
e2e specs that would have caught it are all `test.skip`'d.

---

### BLOCKER-02: HTTP `WriteTimeout=10s` shorter than verify window

**File:** `internal/api/server.go:127-135`
**Issue:**

```go
func (s *Server) ListenAndServe(addr string) error {
    srv := &http.Server{
        Addr:         addr,
        Handler:      s.mux,
        ReadTimeout:  10 * time.Second,
        WriteTimeout: 10 * time.Second,  // <- too short for actions
    }
    return srv.ListenAndServe()
}
```

The action endpoints (`POST /api/containers/{svc}/update|rollback|force-pull`)
block synchronously for the full pipeline duration:
- `ImagePull` (variable, often several seconds on a real registry)
- `docker compose up -d --force-recreate <svc>` (5–15 s on a real HMI)
- `verifyAfterRecreate` (15 s default, 60 s healthcheck opt-in)

CONTEXT.md Area 3 line 89 pins `verifyDuration = 15s`; with healthcheck
opt-in the window extends to 60 s (`HMI_UPDATE_HEALTHCHECK_WINDOW_S`).
`WriteTimeout=10s` means even the fastest possible happy path is cut off
~5 s before verify completes. The client sees a connection error; the
server cancels `r.Context()` which propagates to the orchestrator's verify
loop and converges to `ErrVerifyCanceled`. The user has no way to learn
whether the action succeeded — the response was never delivered.

This compounds with BLOCKER-01: even if the container ID lookup were
fixed, the operator would still see a 10 s timeout instead of a 200
response.

**Fix:**

Either (a) extend the timeouts to comfortably exceed the maximum
`HMI_UPDATE_HEALTHCHECK_WINDOW_S` value plus pull + recreate budget
(60 s + 30 s margin = 90 s minimum), and explicitly carve action endpoints
out of the slow-loris mitigation by using a per-route timeout middleware;
or (b) make the action endpoints asynchronous — return 202 Accepted with
an action ID immediately, let the orchestrator finish in the background,
and have the UI poll `/api/state.containers[svc].action_in_flight` for
completion.

Option (a), minimum change:

```go
const (
    actionTimeoutMax = 90 * time.Second  // healthcheckWindow (60s) + pull/recreate budget (30s)
)

func (s *Server) ListenAndServe(addr string) error {
    srv := &http.Server{
        Addr:         addr,
        Handler:      http.TimeoutHandler(s.mux, actionTimeoutMax, ""),  // or per-route
        ReadTimeout:  10 * time.Second,
        WriteTimeout: actionTimeoutMax + 5 * time.Second,
    }
    return srv.ListenAndServe()
}
```

If using `http.TimeoutHandler` the slow-loris invariant remains because
`ReadTimeout` caps request-body read; `WriteTimeout` only matters for
response body writes which the action handler emits in a single flush.

Add a regression test:

```go
// internal/api/server_test.go
func TestActionEndpoint_WriteTimeout_GreaterThanVerifyWindow(t *testing.T) {
    // assert constant WriteTimeout >= HMI_UPDATE_HEALTHCHECK_WINDOW_S default (60s)
}
```

**Severity rationale:** Every action that does not short-circuit on
idempotency, self-protection, safety-label, or 412 compose-drift will
exceed the WriteTimeout. The HTTP transport is the only operator-visible
interface; if no response reaches the browser, the UI cannot render
success or failure.

---

### BLOCKER-03: `compose.execRunner.UpdateService` drops underlying `cmd.Run` error

**File:** `internal/compose/runner.go:211-221`
**Issue:**

```go
err := cmd.Run()
exitCode := 0
if cmd.ProcessState != nil {
    exitCode = cmd.ProcessState.ExitCode()
}
elapsed := time.Since(start)
// ...
if err != nil {
    slog.Error("compose.run", ..., "err", err, ...)
    return fmt.Errorf("compose.UpdateService %s: exit %d: %w: %s",
        service, exitCode, ErrComposeFailed, stderrSnippet)  // <- err is dropped
}
```

The original `err` from `cmd.Run()` is logged via slog but **not** wrapped
into the returned error. The format string contains exactly one `%w`
(bound to `ErrComposeFailed`), so the wrap chain is
`fmt.wrapError → ErrComposeFailed`. The actual `*exec.ExitError`,
`context.DeadlineExceeded`, `context.Canceled`, or any other error class
from `cmd.Run` is invisible to callers.

This directly contradicts the documented contract in
`internal/compose/errors.go:48-50`:

> The wrap chain preserves the underlying `*exec.ExitError` so callers can
> read `cmd.ProcessState.ExitCode()` via `errors.As` if they need the
> exact code

`errors.As(err, &exitErr)` returns false on the returned error.
`errors.Is(err, context.Canceled)` (relevant for the `cmd.Cancel`/SIGTERM
path) returns false. The orchestrator cannot distinguish "compose exit
non-zero due to a real failure" from "compose interrupted by ctx cancel
(SIGTERM)" — both surface as `ErrComposeFailed`.

**Fix:**

Use the Go 1.20+ multi-wrap form to preserve both sentinels:

```go
if err != nil {
    slog.Error("compose.run", ..., "err", err, ...)
    // Wrap both ErrComposeFailed AND the underlying err so errors.Is/As
    // can branch on either.
    return fmt.Errorf("compose.UpdateService %s: exit %d: %w: %w: %s",
        service, exitCode, ErrComposeFailed, err, stderrSnippet)
}
```

Add a test asserting both `errors.Is(err, ErrComposeFailed)` and
`errors.As(err, &exitErr)` succeed; assert
`errors.Is(err, context.Canceled)` on the ctx-cancel path.

**Severity rationale:** Contract violation between the documented behaviour
and the implementation. Future code that branches on
`errors.Is(err, context.Canceled)` to distinguish operator-initiated
cancellation from a real failure will silently misclassify. The wrap-chain
contract is also surfaced at the API layer (writeActionError dispatches
on the wrap chain).

---

### BLOCKER-04: `disconnectZotFromNetwork` reintroduces WR-08 shell-interpolation pattern

**File:** `e2e/fixtures/disconnect-network.ts:27,47,57`
**Issue:**

```ts
const networks = execSync(`docker network ls --format '{{.Name}}'`, {
  encoding: 'utf8',
});
// ...
execSync(`docker network disconnect ${net} zot`, { stdio: 'inherit' });
// ...
execSync(`docker network connect ${net} zot`, { stdio: 'inherit' });
```

Phase 3 review (WR-08) replaced `execSync` string interpolation with
`execFileSync(argv-array)` in `push-image.ts` precisely to prevent command
injection. The Phase 4 fixture reintroduces the anti-pattern — the `net`
variable is interpolated into the shell command via template literal.

While the file's own SECURITY NOTE acknowledges this and asserts the
input source (`docker network ls` filtered by a regex) is bounded, that
defence is fragile:

- `COMPOSE_PROJECT_NAME` can be set arbitrarily in CI / dev environments;
  the resulting network name passes the `/e2e.*_default$/` regex with
  benign content like `e2e-mybranch_default` but the regex anchors only
  pre/post-fix shape.
- The first match wins; a dev environment with two networks matching the
  pattern picks one non-deterministically.
- Reintroducing `execSync` here creates a precedent that the WR-08 fix
  was negotiable — future fixtures will copy this pattern.

**Fix:**

Pivot to `execFileSync` with an argv array, identical to push-image.ts
post-WR-08:

```ts
import { execFileSync } from 'node:child_process';

function getComposeNetwork(): string {
  const out = execFileSync('docker', ['network', 'ls', '--format', '{{.Name}}'], {
    encoding: 'utf8',
  });
  const match = out.split('\n').find((n) => /^e2e.*_default$/.test(n));
  if (!match) {
    throw new Error(
      `Could not find e2e compose network in:\n${out}\nIs the stack up?`,
    );
  }
  return match;
}

export function disconnectZotFromNetwork(): void {
  const net = getComposeNetwork();
  execFileSync('docker', ['network', 'disconnect', net, 'zot'], { stdio: 'inherit' });
}

export function reconnectZot(): void {
  const net = getComposeNetwork();
  execFileSync('docker', ['network', 'connect', net, 'zot'], { stdio: 'inherit' });
}
```

The argv-split form removes the shell entirely; even an arbitrarily named
network becomes a single argv element, not parsed as shell tokens.

**Severity rationale:** This is the exact same defect Phase 3 explicitly
fixed (WR-08, commit 6b840fc). Reintroducing it sets a precedent that the
fix is optional. The fix is one-line-per-call-site and matches an
already-established pattern in the same repo.

## Warnings

### WARNING-01: `handlers_actions.go::isNoPreviousDigest` hand-rolls `strings.Contains`

**File:** `internal/api/handlers_actions.go:267-282`
**Issue:**

```go
func isNoPreviousDigest(err error) bool {
    if err == nil {
        return false
    }
    const token = "no_previous_digest"
    s := err.Error()
    for i := 0; i+len(token) <= len(s); i++ {
        if s[i:i+len(token)] == token {
            return true
        }
    }
    return false
}
```

Identical anti-pattern to Phase 3 WR-09 (`fix(03-review): WR-09 swap
hand-rolled contains() for strings.Contains`, commit c697286). The
comment claims "linear substring scan ... is cheaper than a regex and
there is no allocation overhead" — both points apply equally to
`strings.Contains` which is a single function call with an SIMD-optimised
implementation in the stdlib (`bytealg.IndexString`) that beats this loop.

**Fix:**

```go
import "strings"

func isNoPreviousDigest(err error) bool {
    return err != nil && strings.Contains(err.Error(), "no_previous_digest")
}
```

**Severity rationale:** Maintainability + drift. The repo's own Phase 3
review fixed this exact pattern. Reintroducing it in Phase 4 erodes the
consistency the fix was meant to establish.

---

### WARNING-02: `isNoPreviousDigest` ordering masks future sentinel additions

**File:** `internal/api/handlers_actions.go:215-255`
**Issue:**

`writeActionError` checks substring `no_previous_digest` **after** every
sentinel-based `errors.Is` branch. If a future revision promotes the
no-previous-digest condition to a proper sentinel (`ErrNoPreviousDigest`)
and the orchestrator wraps it with another sentinel like `ErrPullFailed`
in error chain construction, the substring check might still fire but
mapped to 400 instead of the new sentinel's intended status.

The comment at line 261-266 admits "A future revision may promote this to
a dedicated sentinel; for now the narrow substring match is the contract."
But the contract is not enforced — a future change to the orchestrator
that produces an error like `"actions.Update foo: no_previous_digest
encountered: %w", ErrPullFailed` would route to 400 instead of 500.

**Fix:**

Promote `no_previous_digest` to a proper sentinel now (one new
`var ErrNoPreviousDigest = errors.New("actions: rollback requires previous digest")`
in `internal/actions/errors.go`) and have the orchestrator wrap it:

```go
// orchestrator.go Rollback step 2
if snapshot.PreviousDigest == "" {
    return ActionResult{}, fmt.Errorf("actions.Rollback %s: %w", service, ErrNoPreviousDigest)
}
```

Then in writeActionError use `errors.Is(err, actions.ErrNoPreviousDigest)`
in the dispatch chain alongside the other sentinels. Remove
`isNoPreviousDigest`. The substring contract becomes unnecessary.

**Severity rationale:** Adds drift surface and contradicts the
sentinel-error pattern the rest of the file follows. Sentinel promotion
is a small, mechanical change with no behaviour delta.

---

### WARNING-03: `cmd.Cancel` does not signal compose's child process group

**File:** `internal/compose/runner.go:188`
**Issue:**

```go
cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
```

`docker compose` is a CLI plugin that spawns child processes (the compose
plugin itself plus per-service docker calls). `cmd.Process.Signal` sends
SIGTERM only to the direct child (`docker`), not to the process group.
Compose's child processes may continue running. This makes the
`cmd.WaitDelay = 10 * time.Second` grace ineffective — the SIGTERM lands
on docker but compose plugin children continue past the 10 s grace until
the runtime's eventual SIGKILL terminates the process group.

**Fix:**

Set `SysProcAttr.Setpgid` and signal the whole group on cancel:

```go
import "syscall"

cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
cmd.Cancel = func() error {
    if cmd.Process == nil {
        return nil
    }
    // Signal the entire process group; negate pid to target the group.
    return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}
```

Add a test that spawns a parent-and-child shell pipeline and asserts both
processes receive SIGTERM on ctx cancel.

**Severity rationale:** Graceful shutdown contract is incomplete. Under
SIGTERM-via-ctx-cancel, lingering child processes hold file descriptors
and may produce confusing operator-visible state until the OS reaps them
via SIGKILL.

---

### WARNING-04: `ImagePull` stream is not consumed under `ImagePull` errors

**File:** `internal/actions/orchestrator.go:683-686`
**Issue:**

```go
rc, err := o.dockerClient.ImagePull(ctx, ref, docker.ImagePullOptions{})
if err != nil {
    return "", fmt.Errorf("%w: ImagePull %s: %v", ErrPullFailed, ref, err)
}
pulledDigest, err := drainPullStream(rc)
```

When `ImagePull` returns a non-nil error AND a non-nil `rc` (the moby SDK
behaviour is undocumented at this boundary), the ReadCloser is leaked.
While the orchestrator's path here returns immediately, the moby SDK has
been observed to return both `rc` and `err` together on certain auth /
registry errors. A leaked `rc` holds an HTTP connection and a file
descriptor.

**Fix:**

Defensive close on error:

```go
rc, err := o.dockerClient.ImagePull(ctx, ref, docker.ImagePullOptions{})
if err != nil {
    if rc != nil {
        _ = rc.Close()
    }
    return "", fmt.Errorf("%w: ImagePull %s: %v", ErrPullFailed, ref, err)
}
```

**Severity rationale:** Resource leak under partial failure. Bounded
impact (one descriptor per failed pull), but compounds under retry loops
that future caller code may add.

---

### WARNING-05: `verifyAfterRecreate` deadline is captured before the first tick fires

**File:** `internal/actions/verify.go:172-184`
**Issue:**

```go
ticker := time.NewTicker(verifyTickInterval)
defer ticker.Stop()

deadline := time.Now().Add(verifyWindow + 2*verifyTickInterval)
```

`time.NewTicker(verifyTickInterval)` fires the first tick after
`verifyTickInterval` has elapsed, NOT immediately. With
`verifyTickInterval=1s` and `verifyWindow=15s`, the first inspect call
happens at t=1s; the 15th at t=15s. The deadline at t+15s+2s=17s gives
the loop 17 seconds to fit 15 ticks — fine in production. But in test
mode with `verifyTickInterval=1ms` and `verifyWindow=15ms`, the first
tick fires at t=1ms and the 15th at t=15ms, with deadline at t=17ms.

The Go runtime ticker can drift on a loaded CI machine by tens of
milliseconds. The deadline-check is fragile under CI load. The
auto-fix in Plan 04-03's SUMMARY adds a `2*verifyTickInterval` safety
factor; under CI scheduler jitter exceeding 2 ms this could still
flake.

**Fix:**

Use a budgeted Sleep rather than time.NewTicker:

```go
for consecutive < target {
    select {
    case <-ctx.Done():
        return ErrVerifyCanceled
    case <-time.After(verifyTickInterval):
    }
    // ... inspect + check
    if time.Now().After(deadline) {
        // deadline expired
    }
}
```

Or fire the first inspect immediately (before the first ticker.C) so the
loop semantics match "15 inspect calls separated by verifyTickInterval"
rather than "15 ticker waits each followed by inspect".

**Severity rationale:** Bounded test flake; production timing is comfortable.
Low priority but a one-line clean-up.

---

### WARNING-06: cron-vs-action race writes `AvailableDigest` after ForcePull success

**File:** `internal/actions/orchestrator.go:633-649`
**Issue:**

ForcePull's success-result closure writes `AvailableDigest = pulledDigest`
unconditionally. If a cron `KindDigestResolved` message is enqueued
behind the action's `KindActionResult`, the cron message will overwrite
the just-written `AvailableDigest` with whatever the resolver returned
(possibly the same digest, possibly a stale one if the cron sweep started
before the force-pull and queued behind).

The single-consumer channel + Apply-closure pattern means messages are
applied serially, but the orchestrator's `pullAndVerifyDigest` already
calls `resolver.Digest`, so the two should agree by Pitfall 1. Still, the
ordering invariant "cron sees the registry first, then action overwrites"
is not enforced — under network flap the cron message arriving after the
action could revert `UpdateAvailable=false` back to `true` for a brief
window.

**Fix:**

Compare-and-set within the Apply closure:

```go
Apply: func(s *state.State) {
    c, ok := s.Containers[service]
    if !ok {
        return
    }
    // Only update AvailableDigest if our pulled digest is newer than any
    // cron-supplied digest. Order is enforced by the timestamp ladder
    // (we just pulled this microsecond; cron's LastPolledAt is older).
    c.AvailableDigest = pulledDigest
    c.LastPolledAt = time.Now()  // pin our authority
    // ... rest of the closure
},
```

Or document that cron-vs-action races are accepted and the next cron
tick reconciles (Phase 5 UI should not assume monotonic
`UpdateAvailable`).

**Severity rationale:** Brief UI flutter, no data loss. State eventually
converges via the next cron sweep. Documenting the accepted race is the
cheapest fix.

## Info

### INFO-01: `cmd/sigkillhelper/main.go` race on the loop counter

**File:** `cmd/sigkillhelper/main.go:40-62`
**Issue:**

The helper writes incrementing counter values; the parent test SIGKILLs
at randomised intervals. The test doc says the counter is embedded in the
synthetic digest so a torn write would manifest as a truncated JSON
string. Fine. But the helper does NOT log the highest counter value before
exit — operators inspecting why the test passes can't see whether
iteration N hit counter 5 or counter 5000.

**Fix (low priority):**

Print final counter to stderr in a SIGTERM handler (won't fire under
SIGKILL but covers manual debugging):

```go
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, syscall.SIGTERM)
go func() {
    <-sigCh
    fmt.Fprintf(os.Stderr, "sigkillhelper exit: counter=%d\n", counter)
    os.Exit(0)
}()
```

---

### INFO-02: `pullJSONMessage.Error` short-circuits without consuming the rest of the stream

**File:** `internal/actions/orchestrator.go:812-814`
**Issue:**

```go
if msg.Error != "" {
    return "", fmt.Errorf("docker pull stream error: %s", msg.Error)
}
```

`drainPullStream` returns immediately on the first error message. The
`defer rc.Close()` does close the stream, but the moby daemon may have
queued additional progress events that never get drained. While Close()
should release the connection, on some moby versions the daemon side
times out waiting for the consumer to drain. Not a strict bug — the
deferred Close handles it — but consider whether you want to log the rest
of the stream for diagnostics.

**Fix (low priority):**

After capturing the error, drain remaining messages best-effort for slog
diagnostics:

```go
if msg.Error != "" {
    rest := drainAndLog(dec)  // best-effort, no error propagation
    return "", fmt.Errorf("docker pull stream error: %s (drained %d trailing msgs)", msg.Error, rest)
}
```

---

### INFO-03: `actions.Action` constants overlap with HTTP body fragments

**File:** `internal/actions/middleware.go:57-63`
**Issue:**

```go
type Action string

const (
    ActionUpdate    Action = "update"
    ActionRollback  Action = "rollback"
    ActionForcePull Action = "force-pull"
)
```

`ActionForcePull = "force-pull"` is used both as the URL path segment
(matched against `r.URL.Path`) and as the discriminator switch tag in
`CheckSafetyLabel`. If a future revision renames the URL path to
`force_pull` (snake-case) while leaving the Action constant unchanged,
the safety-label dispatch will silently fall through (no SAFE-03
carve-out fires). The coupling between the URL and the Action constant
should be made explicit via a Test that asserts both shapes agree.

**Fix (low priority):**

Add a guard test:

```go
func TestAction_URLPath_Parity(t *testing.T) {
    // The URL path segments (registered in server.routes) must match the
    // Action constant string values, otherwise CheckSafetyLabel
    // discriminates against a stale identifier.
    cases := []struct{ url, action string }{
        {"update", string(ActionUpdate)},
        {"rollback", string(ActionRollback)},
        {"force-pull", string(ActionForcePull)},
    }
    for _, tc := range cases {
        if tc.url != tc.action {
            t.Errorf("URL %q != Action constant %q", tc.url, tc.action)
        }
    }
}
```

---

### INFO-04: `ActionInFlight` field has no canonical exported constants

**File:** `internal/state/schema.go:139` / `internal/actions/orchestrator.go:341,485,616`
**Issue:**

The orchestrator writes the literal strings `"updating"`, `"rolling_back"`,
`"force_pulling"` into `state.Container.ActionInFlight`. The Plan 04-01
SUMMARY notes this could promote to canonical constants in
`internal/state/notes.go` (or a sibling) if used at more than one site —
which they are, in orchestrator.go alone. The UI Phase 5 will need to
discriminate on these values; without exported constants both sides will
hard-code the literal strings and a typo on either side surfaces as a
silent UI bug (spinner never appears).

**Fix (low priority):**

Promote to exported constants in `internal/state/`:

```go
// internal/state/actions.go
package state

const (
    ActionInFlightUpdating     = "updating"
    ActionInFlightRollingBack  = "rolling_back"
    ActionInFlightForcePulling = "force_pulling"
)
```

Then use these in orchestrator.go AND in tygo-generated TypeScript so the
UI's discriminator stays in lockstep.

---

_Reviewed: 2026-05-15_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_

## REVIEW COMPLETE
