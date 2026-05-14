---
phase: 3
phase_name: Registry, Polling & Update Detection
reviewer: gsd-code-reviewer
depth: standard
reviewed: 2026-05-14
status: issues_found
critical_count: 3
warning_count: 11
info_count: 8
fixed: [CR-01, CR-02, CR-03, WR-01, WR-02, WR-03, WR-04, WR-05, WR-06, WR-07, WR-08, WR-09, WR-10]
fixed_at: 2026-05-14
fix_scope: critical_warning
skipped_in_scope: []
deferred_info: [IN-01, IN-02, IN-03, IN-04, IN-05, IN-06, IN-07, IN-08, WR-11]
---

# Phase 3: Code Review Report

## Summary

Phase 3 (registry resolver + cron poller + single-consumer channel + OBS-04 redaction) is in good shape on the load-bearing invariants:
- `authn.DefaultKeychain` is correctly absent from production registry code (Pitfall 2 dam holds).
- `crane.Digest(WithPlatform(linux/amd64))` is the only path the resolver takes (Pitfall 1 dam holds).
- `errgroup.SetLimit(p.concurrency)` is called before any `g.Go(...)` (Phase-3 ordering pitfall avoided).
- `omitzero` is consistently applied to `time.Time` JSON fields.
- `RunUpdater` is spawned before both producers in `main.go` boot order.
- Tests use `t.Errorf` (not `t.Fatal`) for off-goroutine assertions.

However, three blockers and 11 warnings remain.

## Critical Issues

### CR-01: `cronPoller.send` blocks indefinitely if `updates` channel is full

**File:** `internal/poll/poller.go:498-500`
**Severity:** BLOCKER

`cronPoller.send()` does an unconditional blocking send on `p.updates`:

```go
func (p *cronPoller) send(u StateUpdate) {
    p.updates <- u
}
```

Failure modes:
1. **SIGTERM hang:** Sweep mid-fan-out when ctx cancels. Worker calls `send(...)` which blocks because consumer has drained ctx.Done and exited.
2. **Slow consumer cascade:** Slow `state.Store.persist()` backs up the channel; 64-cap exceeded with 20+ containers + concurrency=4.

**Fix:** Make `send` ctx-aware:

```go
func (p *cronPoller) send(ctx context.Context, u StateUpdate) {
    select {
    case p.updates <- u:
    case <-ctx.Done():
    }
}
```

Thread `gctx` through all send-call paths (`sendPinnedNote`, `sendTagMismatch`, `sendFetchError`, `handleFetchResult`, sweep-level sends).

---

### CR-02: `cronPoller.Run` mutates `p.cronInst` without synchronization

**File:** `internal/poll/poller.go:211, 215, 224, 229`
**Severity:** BLOCKER

`Run` assigns to `p.cronInst` on entry and reads/calls `Start()`/`Stop()` on it. Two concurrent `Run` calls (or stop-then-start race) would race.

**Fix:** Make `cronInst` local to `Run`'s stack frame:

```go
func (p *cronPoller) Run(ctx context.Context) error {
    c := cron.New(cron.WithLocation(time.UTC), cron.WithChain(cron.Recover(cronSlogAdapter{})))
    if _, err := c.AddFunc(p.spec, func() { p.sweep(ctx) }); err != nil { ... }
    c.Start()
    <-ctx.Done()
    <-c.Stop().Done()
    return ctx.Err()
}
```

Removes shared mutable state.

---

### CR-03: `newRedactingHandler` regex doesn't cover bare-token forms

**File:** `cmd/hmi-update/main.go:79, 87-92`
**Severity:** BLOCKER

The regex `^(Bearer|Basic)\s` requires the prefix. Gaps:
1. **Token-only logging:** `slog.String("authn", "Og==")` (just the credentials, no `Basic ` prefix) is NOT redacted — substring fallback also misses.
2. The unit test docstring claims it catches "the exact Pitfall 2 regression literal" — only true for the prefixed form.

**Fix:** Add a base64-credentials probe to the ReplaceAttr:

```go
if len(s) >= 4 && len(s) <= 200 && strings.HasSuffix(s, "=") {
    if decoded, err := base64.StdEncoding.DecodeString(s); err == nil &&
        bytes.Contains(decoded, []byte{':'}) {
        return slog.Attr{Key: a.Key, Value: slog.StringValue("REDACTED")}
    }
}
```

OR update the docstring to clarify scope (defensible — the production code never logs raw base64 tokens).

---

## Warnings

### WR-01: `defaultConcurrency` + `envInt`'s `n > 0` guard — `=0` silently falls back to default
**File:** `internal/poll/poller.go:64, 129-132, 505-512`
Dead clamp at lines 130-132 (`if concurrency < 1`) — unreachable because `envInt` already filters. Remove or change guard to `n >= 0`.

### WR-02: `TestPatterns_Concurrent_RaceClean` doesn't exercise the RWMutex
**File:** `internal/poll/patterns_test.go:99-126`
Pure parallel reads — no `Set`/`Delete` interleaving. Add a writer goroutine.

### WR-03: `clearStaleErrorNotes` uses ad-hoc string indexing
**File:** `internal/poll/poller.go:441`
Replace `len(n) >= len(prefix) && n[:len(prefix)] == prefix` with `strings.HasPrefix(n, prefix)`.

### WR-04: Stale `noteTagMismatch` window after operator fixes running tag
**File:** `internal/poll/poller.go:437-445`
Missing unit test asserting clear-on-success transition. Implementation correct; coverage gap.

### WR-05: `sendPinnedNote` fires every tick — redundant fsync wear
**File:** `internal/poll/poller.go:301-304, 450-460`
Pinned-note is stable; only send if `c.Notes != notePinnedOptOut`. Same logic for `sendTagMismatch`.

### WR-06: `upsertFromInspect` sends two StateUpdates per docker event
**File:** `internal/docker/discovery.go:457-492`
Consolidate upsert + invalid-pattern note into one Apply closure for atomicity. Removes FIFO-ordering reliance.

### WR-07: Missing test that successful fetch preserves `noteInvalidTagPatternMirror`
**File:** `internal/poll/poller.go:437-445`
Implementation correct; add unit test.

### WR-08: `pushFreshIndex` uses `execSync` string interpolation
**File:** `e2e/fixtures/push-index.ts:51-114`
Use `execFileSync` with argv array — no shell injection surface even on operator-controlled inputs.

### WR-09: `errors_test.go::contains` reinvents `strings.Contains`
**File:** `internal/registry/errors_test.go:172-184`
Replace with `strings.Contains` (package already imports strings).

### WR-10: `noteInvalidTagPattern` literal duplicated across `internal/docker` + `internal/poll`
**File:** `internal/poll/poller.go:403-423, internal/docker/discovery.go:503`
No compile-time agreement check. Promote to `internal/state.NoteInvalidTagPattern` (exported) and reference from both.

### WR-11: `TestSlogReplaceAttr_PreservesNonString` pins on stdlib's slog duration encoding
**File:** `cmd/hmi-update/main_test.go:77-84`
Test brittle to future stdlib changes. Acceptable; document.

---

## Info

- **IN-01:** Boot-order doc-comment duplicates call literals — accepted per Plan 03-04 deviation #2.
- **IN-02:** `Run`'s `AddFunc` error branch is dead code (already validated in NewPoller).
- **IN-03:** `classify` includes a defensive `" NNN "` substring match — failure-safe overmatch.
- **IN-04:** `cronSlogAdapter.Error`'s `append(...)...` is fine (inline-consumed).
- **IN-05:** `RunUpdater` doesn't handle channel close — not currently reachable but defensive `ok` check would harden.
- **IN-06:** `NewResolver` reads env at construction — restart required to change `HMI_UPDATE_REGISTRY_INSECURE`.
- **IN-07:** `handleFetchResult` over-captures `state.Container` but GC'd promptly.
- **IN-08:** `/tmp/` fixture files accumulate over CI sessions.

---

## REVIEW COMPLETE
