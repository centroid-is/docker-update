# Phase 3: Registry, Polling & Update Detection - Pattern Map

**Mapped:** 2026-05-14
**Files analyzed:** 15 new + 4 modified + 4 e2e specs = 23 files
**Analogs found:** 22 / 23 (one file — `transport.go` — has no in-repo `http.RoundTripper` analog; pattern comes from RESEARCH.md §"Code Examples: Redacting transport")

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `internal/registry/resolver.go` | service (facade over SDK) | request-response | `internal/docker/moby.go` | exact (both: thin facade wrapping an external SDK with `New*` constructor returning interface) |
| `internal/registry/resolver_test.go` | test | request-response | `internal/docker/moby_test.go` | exact (compile-time satisfies + table-driven against `httptest`) |
| `internal/registry/transport.go` | middleware (HTTP RoundTripper) | request-response | *(no in-repo RoundTripper analog)* | reference RESEARCH.md §"Code Examples: Redacting transport" |
| `internal/registry/transport_test.go` | test | request-response | `internal/docker/moby_test.go` (table-driven) + Phase 2's `discovery_test.go` (`fakeClient` pattern → here it's a `httptest.NewServer`) | role-match |
| `internal/registry/errors.go` | utility (sentinel errors) | n/a | `internal/compose/errors.go` | exact (single-sentinel `errors.New(...)` package var with rich godoc + `errors.Is` contract) |
| `internal/poll/poller.go` | service (long-lived goroutine + scheduler) | event-driven (cron tick) | `internal/docker/discovery.go` | exact (both: long-lived goroutine, ctx cancellation, channel-send to single-consumer, `Run(ctx) error` contract) |
| `internal/poll/poller_test.go` | test | event-driven | `internal/docker/discovery_test.go` (`fakeClient` script + `eventsHandled`/`callCounts` recording) | exact |
| `internal/poll/patterns.go` | utility (mutex-protected cache) | n/a | `internal/state/store.go` (`sync.RWMutex` around a map) | exact |
| `internal/poll/patterns_test.go` | test | n/a | `internal/state/store_test.go` (table-driven, t.TempDir scaffolding) + `internal/compose/reader_test.go` (concurrent `-race` pattern) | role-match |
| `internal/poll/channel.go` | service (single-consumer goroutine) | pub-sub (multi-producer → single-consumer) | `internal/docker/discovery.go` `drainEvents` loop | role-match (same select-on-ctx + drain semantics) |
| `internal/poll/channel_test.go` | test | pub-sub | `internal/docker/discovery_test.go` ctx-cancel paths | role-match |
| `internal/state/schema.go` (MODIFY) | model | n/a | itself (Phase 2 added `ContainerID`, `Labels`, `Pinned`, `Stopped` — same pattern) | exact |
| `internal/api/types.go` (MODIFY) | model (tygo source) | n/a | itself (mirrors `state.Container` verbatim) | exact |
| `internal/docker/discovery.go` (MODIFY) | service | event-driven | itself (refactor `store.Update` callsites → channel send) | exact |
| `cmd/hmi-update/main.go` (MODIFY) | config (boot wiring) | n/a | itself (Phase 2's boot order; Phase 3 adds steps 7–10) | exact |
| `e2e/tests/detect-multiarch.spec.ts` | test (e2e) | request-response | `e2e/tests/discovery.spec.ts` | exact (same compose stack, `request.get('/api/state')` polling, `waitForContainer` helper) |
| `e2e/tests/detect-tag-pattern.spec.ts` | test (e2e) | request-response | `e2e/tests/discovery.spec.ts` + `e2e/fixtures/push-image.ts` | exact |
| `e2e/tests/detect-pinned.spec.ts` | test (e2e) | request-response | `e2e/tests/discovery.spec.ts` | exact |
| `e2e/tests/obs-04-redaction.spec.ts` | test (e2e) | request-response | `e2e/tests/compose-drift.spec.ts` (`execSync` against `docker compose logs`) + `e2e/tests/discovery.spec.ts` | role-match |

## Pattern Assignments

### `internal/registry/resolver.go` (service-facade, request-response)

**Analog:** `/Users/jonb/Projects/tmp/internal/docker/moby.go` (lines 108–162)

Same shape: SDK-shape comment block at the top → `package` → unexported concrete struct wrapping the SDK client → `NewClient`/`NewResolver` constructor returning the interface (not the concrete pointer) → wrapped error messages prefixed with `"package.Function"`.

**SDK-shape comment block pattern** (`moby.go` lines 1–106):

```go
// SDK shape recorded on 2026-05-13 — see _sdk_shape.txt for the canonical record.
//
// The block below is the in-source mirror of internal/docker/_sdk_shape.txt
// (verbatim `go doc` output for moby/moby/client v0.4.1). Both files exist
// on purpose: a reviewer reading moby.go in isolation sees the contract the
// adapter was written against, and CI greps _sdk_shape.txt for mechanical
// drift detection ...
//
// $ go doc github.com/moby/moby/client ContainerListOptions
// ...
```

**Apply to `resolver.go`:** Recommend a `// $ go doc github.com/google/go-containerregistry/pkg/crane Digest`-style block at the top, mirroring an `internal/registry/_sdk_shape.txt` capture, so CI greps stay symmetric with `internal/docker`. CONTEXT.md "File Layout" already hints at `_docs.txt`.

**Constructor pattern** (`moby.go` lines 154–161):

```go
func NewClient(ctx context.Context) (Client, error) {
    _ = ctx // reserved for future cancellation-aware construction
    c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
    if err != nil {
        return nil, fmt.Errorf("docker.NewClient: %w", err)
    }
    return &mobyClient{c: c}, nil
}
```

**Apply to `resolver.go`:** `NewResolver(transport http.RoundTripper) (Resolver, error)` (or no error — construction can't fail since transport is injected). Return the interface, not `*craneResolver`. Match the `"registry.NewResolver"` error-wrap prefix.

**Method body pattern** (`moby.go` lines 175–181):

```go
func (m *mobyClient) ContainerList(ctx context.Context, opts ContainerListOptions) ([]ContainerSummary, error) {
    res, err := m.c.ContainerList(ctx, opts)
    if err != nil {
        return nil, fmt.Errorf("docker.ContainerList: %w", err)
    }
    return res.Items, nil
}
```

**Apply to `resolver.go` `Digest()`:** Same wrap pattern, but additionally classify the error before wrapping into `ErrPermanent` / `ErrTransient` (see `errors.go` analog below). The body itself is the verified `crane.Digest(ref, WithContext, WithAuth(Anonymous), WithPlatform(amd64), WithTransport(r.transport))` from RESEARCH.md §"Code Examples".

---

### `internal/registry/resolver_test.go` (test, request-response)

**Analog:** `/Users/jonb/Projects/tmp/internal/docker/moby_test.go`

**Compile-time interface-satisfaction guard** (`moby_test.go` lines 38–41):

```go
func TestMobyClient_SatisfiesClient(t *testing.T) {
    t.Parallel()
    var _ Client = (*mobyClient)(nil)
}
```

**Apply:** `var _ Resolver = (*craneResolver)(nil)` — load-bearing build-time assertion.

**For the `httptest.NewServer`-based registry mock pattern,** use RESEARCH.md §"Code Examples: Redacting transport + Pitfall 2 regression test" (lines 637–684 of `03-RESEARCH.md`) verbatim. Table-driven over `{name, manifestShape (index|single-arch), wantErr, wantDigest}`.

**Test header doc style** (`moby_test.go` lines 1–21): every test file begins with a "What these tests guard" bullet list naming each test by symbol. Mirror this in `resolver_test.go` for the four cases: multi-arch index, single-arch manifest, 401 retry, invalid ref.

---

### `internal/registry/transport.go` (middleware, http.RoundTripper)

**No in-repo analog.** This is the first `http.RoundTripper` implementation in the codebase. Reference RESEARCH.md §"Code Examples: Redacting transport" (lines 573–617 of `03-RESEARCH.md`) — verbatim 30-LOC implementation.

For doc-comment style, mirror `internal/compose/errors.go`'s pattern: a long godoc on the package var (`sensitiveHeaders`) explaining the "wire still sees them — slog must not" invariant, with an `errors.Is`-style usage example in prose.

---

### `internal/registry/transport_test.go` (test, request-response)

**Analog (regression-guard pattern):** RESEARCH.md §"Code Examples: Redacting transport + Pitfall 2 regression test" (lines 637–684 of `03-RESEARCH.md`).

**Mutex-protected slice-capture pattern is in-repo at** `/Users/jonb/Projects/tmp/internal/docker/discovery_test.go` lines 67–94 (`fakeClient` struct with `mu sync.Mutex` guarding `inspectCalls []string`):

```go
type fakeClient struct {
    mu sync.Mutex
    // ...
    inspectScript map[string]ContainerInspect
    inspectCalls  []string // captured IDs in order
    // ...
}
```

**Apply:** `transport_test.go` uses an analogous `seen []string` with `mu sync.Mutex` to capture every inbound `Authorization` header across the bearer-flow's 2 requests (token endpoint + registry endpoint).

**Threat-tagged assertion comment style** (`moby_test.go` line 45):

```go
// (threat T-02-01-02: error wrapping must not leak DOCKER_HOST values).
```

**Apply:** tag the Pitfall 2 regression assertion with `// PITFALL 2 REGRESSION GUARD: no request anywhere ever carried Basic Og==` (line already drafted in RESEARCH.md §"Code Examples").

---

### `internal/registry/errors.go` (utility, sentinel errors)

**Analog:** `/Users/jonb/Projects/tmp/internal/compose/errors.go` (entire file, 45 lines)

**Full excerpt** (`errors.go` lines 1–45):

```go
// Package compose provides a read-only adapter ...
package compose

import "errors"

// ErrComposeFileMoved is returned (wrapped) from Reader.CheckUnchanged
// when the compose file's inode (or, on filesystems without stable
// inodes, its (mtime, size) pair) has drifted from the boot snapshot
// captured by NewReader.
//
// Phase 4 maps this sentinel to HTTP 412 with body:
//
//	{"error":"compose_file_moved","hint":"restart hmi-update to pick up the new docker-compose.yml"}
//
// See .planning/research/PITFALLS.md Pitfall 10 for the canonical
// description of the failure mode this sentinel guards against. Callers
// test with errors.Is so the sentinel identity survives any number of
// fmt.Errorf("compose: %w", ...) wraps:
//
//	if err := reader.CheckUnchanged(ctx); err != nil {
//	    if errors.Is(err, compose.ErrComposeFileMoved) {
//	        // serve 412 with remediation hint
//	    }
//	    // other errors: surface as 500
//	}
var ErrComposeFileMoved = errors.New("compose: file moved or replaced since boot")
```

**Apply to `registry/errors.go`:** two sentinels (`ErrPermanent`, `ErrTransient`) each with the same shape — long godoc explaining (a) what HTTP status codes map to which, (b) how the poller branches on `errors.Is`, (c) the wrap pattern (`fmt.Errorf("registry: ...: %w", ErrPermanent)`). Match the inline `errors.Is(err, registry.ErrTransient)` example in prose.

```go
// (template — exact text TBD by planner)
var ErrPermanent = errors.New("registry: permanent error (401/403/404; do not retry)")
var ErrTransient = errors.New("registry: transient error (5xx/timeout; retry once)")
```

---

### `internal/poll/poller.go` (service, event-driven)

**Analog:** `/Users/jonb/Projects/tmp/internal/docker/discovery.go` (entire 539-line file, especially lines 119–283 — `NewDiscoverer`, `Run`, `eventsLoop`)

**Anti-deadlock invariant header comment** (`discovery.go` lines 1–27):

```go
// Architectural anchor (see .planning/research/ARCHITECTURE.md Pattern 3
// "single-consumer channel for state mutations" + lines 400-422):
//
//   Discoverer is the FIRST producer of container-related state mutations
//   in Phase 2. Phase 3's cron poller becomes the second producer (registry
//   digest checks); both producers feed state through state.Store.Update,
//   which is the single mutation surface that serializes writes under
//   state.Store.mu and writes through to disk via persist().
//
// Anti-deadlock invariant (ARCHITECTURE.md lines 419-420 — "Never hold
// state.Store.mu while calling registry/docker/compose"):
```

**Apply:** `poller.go` opens with the mirror-image comment: "cronPoller is the SECOND producer; the first lives in internal/docker/discovery.go; both feed the channel defined in internal/poll/channel.go." Same anti-deadlock language.

**Long-lived `Run(ctx) error` contract** (`discovery.go` lines 166–175):

```go
func (d *Discoverer) Run(ctx context.Context) error {
    slog.Info("discovery.boot.start", "label_filter", "hmi-update.watch=true")
    if err := d.bootList(ctx); err != nil {
        slog.Error("discovery.boot.fail", "err", err)
    }
    return d.eventsLoop(ctx)
}
```

**Apply:** `func (p *cronPoller) Run(ctx context.Context) error { p.cronInst.Start(); <-ctx.Done(); <-p.cronInst.Stop().Done(); return ctx.Err() }` (this draft is in RESEARCH.md lines 721–727; the in-repo style is the `slog.Info("poller.boot.start", "cron_expr", spec)` opener pattern at the top of `Run`).

**ctx-aware sleeper pattern** (`discovery.go` lines 95–108):

```go
func ctxAwareSleep(ctx context.Context, d time.Duration) {
    if d <= 0 { return }
    t := time.NewTimer(d)
    defer t.Stop()
    select {
    case <-ctx.Done():
    case <-t.C:
    }
}
```

**Apply (if retry pattern needs a sleep):** `craneResolver`'s 2s retry sleep uses the same select-on-ctx pattern. Same `SetSleeperForTest` swap hook (`discovery.go` lines 144–155) for the retry test.

**slog event naming convention** (`discovery.go` line 167): `"discovery.boot.start"`, `"discovery.event.received"`, `"discovery.events.reconnect"`, etc. **Apply:** `"poll.sweep.start"`, `"poll.sweep.end"`, `"registry.fetch"`, `"registry.authn"`. CONTEXT.md "Claude's Discretion" specifies `elapsed_ms` for parity.

---

### `internal/poll/poller_test.go` (test, event-driven)

**Analog:** `/Users/jonb/Projects/tmp/internal/docker/discovery_test.go` lines 54–175 (the `fakeClient` recording type)

**Scripted-fake pattern** (`discovery_test.go` lines 67–95):

```go
type fakeClient struct {
    mu sync.Mutex

    // scripted ContainerList responses, one per call.
    listScript [][]ContainerSummary
    listCalls  int

    // scripted ContainerInspect responses, keyed by container ID.
    inspectScript map[string]ContainerInspect
    inspectCalls  []string // captured IDs in order

    // Optional hook called at the entry of ContainerInspect — used by
    // TestDiscoverer_InspectPrecedesUpdate to instrument call ordering.
    inspectHook func(id string)
}
```

**Apply:** `poller_test.go` defines a `fakeResolver` with the same shape:

- `digestScript map[string]string` (image:tag → digest)
- `digestErrScript map[string]error` (image:tag → ErrPermanent / ErrTransient / nil)
- `digestCalls []string` (captured `ref` args, in order)
- `mu sync.Mutex` guarding all of the above
- `digestHook func(ref string)` for the in-flight ordering test

**Call-count helper** (`discovery_test.go` lines 169–175):

```go
func (f *fakeClient) callCounts() (listCalls, eventsCalls int, inspectIDs []string) {
    f.mu.Lock()
    defer f.mu.Unlock()
    ids := make([]string, len(f.inspectCalls))
    copy(ids, f.inspectCalls)
    return f.listCalls, f.eventsCalls, ids
}
```

**Apply:** `func (f *fakeResolver) callCounts() (digestCalls int, refs []string)` — same defensive copy under lock.

**Goroutine assertion convention** (`discovery_test.go` line 33):

```go
// Goroutine assertion contract (per persist_test.go lines 29-31): assertions
// fired off-goroutine use t.Errorf, NEVER t.Fatal — t.Fatal inside a goroutine
// only halts the goroutine that calls it and leaves the test to pass falsely.
```

**Apply:** load-bearing for `poller_test.go` because the sweep dispatches up to 4 worker goroutines under `errgroup`. Use `t.Errorf` inside `g.Go(func)` closures.

---

### `internal/poll/patterns.go` (utility, mutex-protected cache)

**Analog:** `/Users/jonb/Projects/tmp/internal/state/store.go` (lines 11–133, full file)

**RWMutex-around-a-value pattern** (`store.go` lines 22–27):

```go
type Store struct {
    path  string
    mu    sync.RWMutex
    state State
}
```

**Apply:** `Patterns` follows the identical shape:

```go
type Patterns struct {
    mu sync.RWMutex
    m  map[string]*regexp.Regexp // service -> compiled regex (nil = no constraint)
}
```

(this draft is in RESEARCH.md lines 741–745.)

**Concurrent-read contract** (`store.go` lines 95–106):

```go
// Get returns a snapshot of the current state under an RLock.
//
// The returned value is a shallow copy of Store.state. The Containers map
// header is shared; callers MUST treat it as read-only. ...
func (s *Store) Get() State {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.state
}
```

**Apply:** `Patterns.Match(service, tag string) bool` uses RLock (read-mostly path). `Patterns.Set(service, pattern string) error` uses write Lock and logs the invalid-regex warning before returning (`event=tag_pattern.invalid_regex service=…` per CONTEXT.md §"Tag-pattern regex semantics").

---

### `internal/poll/patterns_test.go` (test)

**Analog:** `/Users/jonb/Projects/tmp/internal/state/store_test.go` (lines 1–99) + `/Users/jonb/Projects/tmp/internal/compose/reader_test.go` (test header style + concurrent `-race` test pattern)

**Test header doc style** (`store_test.go` lines 1–16):

```go
// RED-FIRST per C4. These tests are authored before internal/state/store.go
// exists. Plan 02 (Wave 2) drives them green by implementing NewStore, Get,
// Update, and the version: 1 schema.
//
// What these tests guard:
//   - TestLoadAndPersist: STATE-03 round trip. ...
//   - TestMissingFile: STATE-03 boot-from-cold. ...
//   - TestCorruptedFile: operator-visible signal — a corrupted state file is
//     NOT silently overwritten ...
```

**Apply:** `patterns_test.go` header lists: `TestPatterns_ValidRegex_Match`, `TestPatterns_ValidRegex_NoMatch`, `TestPatterns_InvalidRegex_PermissiveWithWarning`, `TestPatterns_Concurrent_RaceClean`. Each backed by the failure mode it guards (invalid regex → permissive + warning is from CONTEXT.md §"Tag-Pattern & Digest-Pin Handling").

**Goroutine `-race` test pattern is in** `internal/compose/reader_test.go` `TestCheckUnchanged_Concurrent` (referenced in lines 30–34 of that file's header). **Apply:** 8 goroutines × 100 `Match` calls against a populated `Patterns`, all returning the expected bool, with `t.Errorf` (not `t.Fatal`) in off-goroutine assertions.

---

### `internal/poll/channel.go` (service, single-consumer goroutine)

**Analog:** `/Users/jonb/Projects/tmp/internal/docker/discovery.go` lines 314–335 (the `drainEvents` loop)

**Select-on-ctx + drain semantics** (`discovery.go` lines 314–335):

```go
func (d *Discoverer) drainEvents(ctx context.Context, eventCh <-chan EventMessage, errCh <-chan error) (eventsHandled int, reason string) {
    for {
        select {
        case <-ctx.Done():
            return eventsHandled, "ctx-cancelled"
        case err, ok := <-errCh:
            if !ok { return eventsHandled, "errch-closed" }
            if err != nil {
                slog.Warn("discovery.events.stream.err", "err", err)
                return eventsHandled, "stream-err"
            }
        case ev, ok := <-eventCh:
            if !ok { return eventsHandled, "eventch-closed" }
            d.handleEvent(ctx, ev)
            eventsHandled++
        }
    }
}
```

**Apply to `channel.go` `RunUpdater`:** the consumer goroutine has the same shape: `for { select { case <-ctx.Done(): drain-then-return; case msg := <-ch: store.Update(msg.Apply) } }`. RESEARCH.md lines 344–363 has the draft. The "drain pending on ctx.Done" inner `for` loop is the only addition (CONTEXT.md §"Channel pattern" — "On ctx.Done(), consumer drains pending messages then exits cleanly").

**slog logging on consumer error** (`discovery.go` lines 413–415):

```go
if err := d.store.Update(...); err != nil {
    slog.Error("discovery.event.start.persist", "service", svc, "err", err)
}
```

**Apply:** consumer uses `slog.Error("poll.consumer.persist", "service", msg.Service, "err", err)` — matches the dotted-event-name convention.

---

### `internal/poll/channel_test.go` (test)

**Analog:** `/Users/jonb/Projects/tmp/internal/docker/discovery_test.go` (the ctx-cancel + reconnect-backoff tests)

**Pattern:** `t.TempDir()` → `state.NewStore(tempPath)` → start `RunUpdater(ctx, ch, store)` in a goroutine → send 3 messages → call `cancel()` → wait for goroutine to exit (use a `done := make(chan struct{}); go { RunUpdater(...); close(done) }` pattern) → assert `store.Get()` reflects all 3 mutations (drain happened).

**Helper for store-from-tempdir** (`discovery_test.go` lines 181–189):

```go
func newTestStore(t *testing.T) *state.Store {
    t.Helper()
    dir := t.TempDir()
    store, err := state.NewStore(filepath.Join(dir, "state.json"))
    if err != nil {
        t.Fatalf("state.NewStore: %v", err)
    }
    return store
}
```

**Apply verbatim.** This helper should arguably move to a `state.NewTestStore(t)` testhelper if both `discovery_test.go` and `channel_test.go` need it — but copy-paste for now follows the existing convention.

---

### `internal/state/schema.go` (MODIFY: model, add 3 per-container + 3 top-level fields)

**Analog:** itself — Phase 2 added `ContainerID`, `Labels`, `Pinned`, `Stopped` in the exact pattern Phase 3 needs.

**Phase 2 field-addition pattern** (`schema.go` lines 39–67):

```go
// ContainerID is the short (12-char) docker container ID, matching the
// `docker ps` column. Set by the discovery goroutine on boot
// ContainerList + every `start` event Inspect. CONTEXT.md "Claude's
// Discretion" picks short over full ID for parity with operator-visible
// tooling.
ContainerID string `json:"container_id,omitempty"`

// Labels carries the subset of container labels relevant to hmi-update —
// `hmi-update.watch`, `hmi-update.tag-pattern`, `hmi-update.allow-update`,
// `hmi-update.allow-rollback`. ...
Labels map[string]string `json:"labels,omitempty"`

// Pinned is true when the container's image reference is digest-pinned
// (e.g. `image: ghcr.io/foo/bar@sha256:...`). ...
Pinned bool `json:"pinned,omitempty"`

// Stopped is true when the most recent docker event for this container
// was `die` (container exited). ...
Stopped bool `json:"stopped,omitempty"`
```

**Apply to Phase 3 additions:**

```go
// AvailableDigest is the upstream sha256 most recently fetched by the
// Phase 3 poll loop. Empty until the first successful resolver.Digest()
// call. Compared against CurrentDigest to compute UpdateAvailable.
AvailableDigest string `json:"available_digest,omitempty"`

// LastPolledAt is the wall-clock time of the most recent successful
// resolver.Digest() call for this container. Serialized as RFC3339Nano
// to match the project default. Zero-valued (omitted) until first poll.
LastPolledAt time.Time `json:"last_polled_at,omitempty"`

// Notes is a single short sentence ("pinned: opt-out" /
// "invalid tag-pattern label, ignored" / "running tag does not match
// tag-pattern label" / "no amd64 manifest in upstream index"). At most
// one note applies at a time; if two would apply, join with "; ".
Notes string `json:"notes,omitempty"`
```

And in `State` (lines 75–78):

```go
type State struct {
    Version    int                  `json:"version"`
    Containers map[string]Container `json:"containers"`

    // Phase 3: poll-loop observability.
    LastPollStart time.Time `json:"last_poll_start,omitempty"`
    LastPollEnd   time.Time `json:"last_poll_end,omitempty"`
    LastPollError string    `json:"last_poll_error,omitempty"`
}
```

Every field is `omitempty` — matches the Phase 2 precedent; pre-existing on-disk state from Phase 2 deserializes cleanly into the new shape (RESEARCH.md "Runtime State Inventory" confirms forward-compat).

**Tygo source-of-truth invariant** (`schema.go` lines 19–24):

```go
// Field tags intentionally match the shape documented in
// .planning/phases/01-walking-skeleton-test-harness/01-RESEARCH.md
// §"tygo configuration" ... internal/api/types.go mirrors this shape verbatim;
// tygo's source-of-truth contract (Makefile `check-types`) catches drift.
```

**Critical:** every field added to `schema.go` MUST also land in `api/types.go` in the same commit. `make check-types` enforces.

---

### `internal/api/types.go` (MODIFY: model, mirror schema additions)

**Analog:** itself — mirror of `state.Container` and `state.State`. Lines 28–63 show the existing field-by-field mirror.

**Apply:** verbatim copy of the three per-container fields (`AvailableDigest`, `LastPolledAt`, `Notes`) into `api.Container`, and the three top-level fields (`LastPollStart`, `LastPollEnd`, `LastPollError`) into `api.State`. Tags byte-identical to `schema.go`. The `time.Time` fields require an `import "time"` if not already present.

---

### `internal/docker/discovery.go` (MODIFY: refactor 3 `store.Update` sites → channel send)

**Analog:** itself — the three current call sites are at lines 403–415 (`upsertFromInspect`), 426–435 (`markStopped`), 446–450 (`removeContainer`).

**Current pattern** (lines 403–415):

```go
if err := d.store.Update(func(st *state.State) {
    c := st.Containers[svc]
    c.Service = svc
    // ... 7 fields set ...
    st.Containers[svc] = c
}); err != nil {
    slog.Error("discovery.event.start.persist", "service", svc, "err", err)
}
```

**Apply (refactored to channel send):**

```go
d.updates <- poll.StateUpdate{
    Kind:    poll.KindContainerEvent,
    Service: svc,
    Apply: func(st *state.State) {
        c := st.Containers[svc]
        c.Service = svc
        // ... same 7 fields ...
        st.Containers[svc] = c
    },
}
```

The closure body is unchanged; only the wrapper changes from `d.store.Update(...)` → `d.updates <- StateUpdate{...}`. The single-consumer goroutine in `poll/channel.go` invokes `store.Update(msg.Apply)`.

**Discoverer struct gains a field** (`discovery.go` lines 66–91):

```go
type Discoverer struct {
    client     Client
    store      stateStore        // KEEP for read paths (serviceForContainerID)
    updates    chan<- poll.StateUpdate // NEW: replaces 3 store.Update call sites
    patterns   *poll.Patterns    // NEW: Phase 2's compile-on-discover seam
    // ... rest unchanged
}
```

**Constructor signature change** (`discovery.go` lines 119–127):

`NewDiscoverer(client, store)` → `NewDiscoverer(client, store, updates, patterns)`. `cmd/hmi-update/main.go` is the only caller (Phase 2's `discovery := docker.NewDiscoverer(dockerClient, store)` at line 103 of main.go).

**Patterns compilation seam:** in `upsertFromInspect` after `filteredLabels := filterHmiLabels(cfg.Labels)` (line 401), call `d.patterns.Set(svc, filteredLabels["hmi-update.tag-pattern"])`. CONTEXT.md §"Tag-pattern regex semantics" says invalid regex is logged + permissive (no error returned to caller).

**Test impact:** `discovery_test.go` `newDiscovererWithStore` (line 134) gains the same two new params; the `inspectPrecedesUpdate` test instrumentation switches from inspecting `store.Update` call ordering to inspecting `updates <- ...` channel-send ordering. Same invariant, different observation point.

---

### `cmd/hmi-update/main.go` (MODIFY: wire registry + poll + consumer goroutine)

**Analog:** itself (Phase 2's boot order at lines 37–119)

**Phase 2 boot order** (main.go lines 37–119) — Phase 3 inserts new steps:

```go
// Phase 2 (existing):
// 1. slog handler
// 2. state.NewStore
// 3. docker.NewClient
// 4. compose.NewReader
// 5. docker.Discoverer goroutine
// 6. api.NewServer(...).ListenAndServe

// Phase 3 (NEW — insert between steps 4 and 5):
// 4.5. transport := registry.NewRedactingTransport()
// 4.6. resolver, err := registry.NewResolver(transport)
//      [log.Fatalf on err with "registry.NewResolver" prefix]
// 4.7. slog.Info("registry.authn", "keychain", "anonymous")  // OBS-04 boot event
// 4.8. patterns := poll.NewPatterns()
// 4.9. updates := make(chan poll.StateUpdate, 64)
// 4.10. consumerWg := &sync.WaitGroup{} ; consumerWg.Add(1)
//       go func() { defer consumerWg.Done(); poll.RunUpdater(ctx, updates, store) }()

// Phase 2 step 5, MODIFIED:
// 5. discoverer := docker.NewDiscoverer(dockerClient, store, updates, patterns)
//    [new 4-arg signature]
//    go discoverer.Run(ctx)

// Phase 3 (NEW — insert after step 5):
// 5.5. cronExpr := os.Getenv("HMI_UPDATE_CRON"); if cronExpr == "" { cronExpr = "0 * * * *" }
// 5.6. poller, err := poll.NewPoller(cronExpr, resolver, patterns, updates, store, timeout, concurrency)
//      [log.Fatalf on err — paste-ready remediation per RESEARCH.md cron-parsing pitfall]
// 5.7. go poller.Run(ctx)
```

**Env-var parsing pattern** (`main.go` lines 39–50, `HMI_UPDATE_LOG_LEVEL` example):

```go
level := slog.LevelInfo
if v := os.Getenv("HMI_UPDATE_LOG_LEVEL"); v != "" {
    switch v {
    case "debug": level = slog.LevelDebug
    case "warn":  level = slog.LevelWarn
    case "error": level = slog.LevelError
    }
}
```

**Apply to `HMI_UPDATE_REGISTRY_TIMEOUT_S` and `HMI_UPDATE_POLL_CONCURRENCY`:** same `if v := os.Getenv(...); v != "" { strconv.Atoi(v) ... }` pattern with sensible defaults (10s and 4 respectively, per CONTEXT.md §"Configuration Knobs").

**Fail-fast error wrapping pattern** (`main.go` line 60):

```go
if err != nil {
    log.Fatalf("state.NewStore: %v", err)
}
```

**Apply:** every new constructor (`registry.NewResolver`, `poll.NewPoller`) uses `log.Fatalf("<package>.<Constructor>: %v", err)`. Operator greps boot logs.

**slog `ReplaceAttr` setup for OBS-04** (NEW — no in-repo analog; reference RESEARCH.md §"Slog ReplaceAttr for token redaction"):

The current `slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})` call at line 51 needs to grow `ReplaceAttr: redactBearer` where `redactBearer` is the compiled-once regex defined in `internal/registry/` (or co-located in `cmd/hmi-update/main.go` if it's the only caller).

---

### `e2e/tests/detect-multiarch.spec.ts` (e2e test, request-response)

**Analog:** `/Users/jonb/Projects/tmp/e2e/tests/discovery.spec.ts` (entire 127-line file)

**`waitForContainer` polling helper** (`discovery.spec.ts` lines 28–47):

```typescript
async function waitForContainer(
  request: import('@playwright/test').APIRequestContext,
  service: string,
  timeoutMs: number,
): Promise<StateBody['containers'][string]> {
  const deadline = Date.now() + timeoutMs;
  let lastBody: StateBody | null = null;
  while (Date.now() < deadline) {
    const resp = await request.get('/api/state');
    if (resp.ok()) {
      lastBody = (await resp.json()) as StateBody;
      const c = lastBody?.containers?.[service];
      if (c) return c;
    }
    await sleep(1000);
  }
  throw new Error(
    `service ${service} never appeared in /api/state within ${timeoutMs}ms. last body: ${JSON.stringify(lastBody)}`,
  );
}
```

**Apply:** generalize to `waitForCondition(predicate: (state: StateBody) => boolean, timeoutMs: number)` — used to wait for `state.containers[svc].update_available === true` AND `state.containers[svc].available_digest === "sha256:..."`.

**Mid-test manifest push** — use `pushFreshManifest(repo)` from `/Users/jonb/Projects/tmp/e2e/fixtures/push-image.ts`:

```typescript
export function pushFreshManifest(repo: string): string {
  // ... writes payload, oras-pushes, returns sha256:... ...
}
```

**Apply to `detect-multiarch.spec.ts`:** push two shapes in one test:

1. Push single-arch manifest via `pushFreshManifest('stub')` → assert `update_available === true` AND `available_digest === <returned sha>` within `cron + 5s` (i.e. `HMI_UPDATE_CRON=@every 5s` set in compose override → 10s deadline).
2. Push a multi-arch image index (needs a new fixture helper — `pushFreshIndex(repo)` — that wraps `crane index append` or `oras manifest push` of an `application/vnd.oci.image.index.v1+json`). Same assertion.

**Test header doc style** (`discovery.spec.ts` lines 1–18):

```typescript
// DOCK-04 — Containers labeled hmi-update.watch=true are visible in
// /api/state within 60s (boot path) and within 5s when started mid-test
// (events path — DETECT-06 secondary surface preview).
//
// Tolerances:
//   - boot SLA: 60s per DOCK-04 acceptance; the test polls /api/state every
//     1s with a 75s wall-clock deadline (15s slack for image build / boot).
```

**Apply:** `detect-multiarch.spec.ts` header pins DETECT-04 + DETECT-07, tolerances (`cron + 5s` = 10s deadline at `@every 5s`), and the manifest-shape matrix.

---

### `e2e/tests/detect-tag-pattern.spec.ts` (e2e test, request-response)

**Analog:** `e2e/tests/discovery.spec.ts` + `e2e/fixtures/push-image.ts`

**Pattern:** same `waitForContainer` polling loop. Test sequence per CONTEXT.md §"Tag-Pattern & Digest-Pin Handling":

1. Start a `timescaledb-stub` service in `compose.test.yml` (or a per-test compose override) with `image: timescale/timescaledb:latest-pg17` and `label hmi-update.tag-pattern: "^latest-pg17$"`.
2. Assert `/api/state.containers["timescaledb-stub"]` has matching `labels["hmi-update.tag-pattern"]`.
3. `pushFreshManifest('timescale/timescaledb', { tag: 'latest-pg18-oss' })` → wait 15s → assert `update_available` STAYS `false` (negative assertion).
4. `pushFreshManifest('timescale/timescaledb', { tag: 'latest-pg17' })` → wait 15s → assert `update_available === true`.

**Note:** `pushFreshManifest` currently hardcodes `:latest` (line 31 of push-image.ts). The planner should extend it to accept an optional `tag` param. This is a fixture extension, not a new file.

---

### `e2e/tests/detect-pinned.spec.ts` (e2e test, request-response)

**Analog:** `e2e/tests/discovery.spec.ts`

**Pattern:** add a `pinned-stub` service to `compose.test.yml` (or per-test override) with `image: busybox@sha256:...` (any known busybox digest). Assert at boot:

```typescript
const c = await waitForContainer(request, 'pinned-stub', 75_000);
expect(c.pinned).toBe(true);
expect(c.notes).toBe('pinned: opt-out');
```

Then push to `:latest` for an unrelated tag and assert `c.update_available` stays `false` even after `2 × cron` (10s deadline at `@every 5s`).

---

### `e2e/tests/obs-04-redaction.spec.ts` (e2e test, log-capture)

**Analog (closest):** `/Users/jonb/Projects/tmp/e2e/tests/compose-drift.spec.ts` (uses `execSync` for compose ops) + `/Users/jonb/Projects/tmp/e2e/tests/discovery.spec.ts` (event-driven mid-test container spawn)

**`execSync` against `docker compose logs` pattern** — there is no direct in-repo precedent for log-capture, but `compose-drift.spec.ts` line 49 (`execSync(\`${COMPOSE_BASE} restart hmi-update\`, ...)`) shows the `execSync`-on-compose pattern. **Apply:**

```typescript
// Capture hmi-update stdout/stderr across a full poll sweep:
const logs = execSync('docker compose -f compose.test.yml logs --no-color hmi-update', {
  encoding: 'utf8',
});

// Pitfall 2 + OBS-04 regression guards:
expect(logs).not.toMatch(/Bearer /);
expect(logs).not.toMatch(/Authorization:/i);
expect(logs).not.toMatch(/Basic Og==/);
```

**Wait-for-poll-completion pattern:** poll `/api/state` for `last_poll_end` to advance, then capture logs:

```typescript
const before = await request.get('/api/state').then(r => r.json());
const beforeEnd = before.last_poll_end ?? '';

// Force at least one sweep to complete by waiting cron + slack
await sleep(8_000);

const after = await request.get('/api/state').then(r => r.json());
expect(after.last_poll_end).not.toBe(beforeEnd);
```

Then `execSync` the log capture and assert absence of Bearer/Basic/Authorization strings.

**Test must use the zot fixture with bearer-challenge enabled.** Phase 1's zot is configured anonymous-pull (`e2e/zot-config.json`); for OBS-04, the test either (a) needs a per-test compose override that enables auth on zot, or (b) lives with the limitation that anonymous pull doesn't exercise the bearer flow and instead relies on the unit-level `transport_test.go` regression guard. The planner picks; CONTEXT.md §"Pitfall 2 regression guard" suggests both (unit test + e2e log scan) are wanted.

---

## Shared Patterns

### Pattern A: Long-form file-header doc-comment

**Source:** `internal/docker/discovery.go` lines 1–27 (with anti-deadlock invariant), `internal/state/store.go` lines 1–10, `internal/compose/errors.go` lines 1–17

**Apply to:** every new `*.go` file in `internal/registry/` and `internal/poll/`. Conventions:

1. First line: `// Package <name> <one-sentence purpose>.`
2. Architectural anchor block referencing `.planning/research/ARCHITECTURE.md` or the relevant CONTEXT.md section.
3. Anti-deadlock / threat / invariant block (where applicable).
4. Phase-and-plan attribution (`Phase 3 plan 03-XX-PLAN.md` lands the body).

### Pattern B: `errors.Is`-friendly sentinel package vars

**Source:** `internal/compose/errors.go` line 44

```go
var ErrComposeFileMoved = errors.New("compose: file moved or replaced since boot")
```

**Apply to:** `registry.ErrPermanent`, `registry.ErrTransient`. Wrap with `fmt.Errorf("registry: ... %w", ErrPermanent)`. Callers branch with `errors.Is`.

### Pattern C: Constructor returns interface, not concrete pointer

**Source:** `internal/docker/moby.go` line 154

```go
func NewClient(ctx context.Context) (Client, error) {
    // ...
    return &mobyClient{c: c}, nil
}
```

The doc comment on `NewClient` calls this out as WR-04 — exposing a pointer to an unexported struct creates ill-formed import situations.

**Apply to:** `registry.NewResolver(...) Resolver`, `poll.NewPoller(...) Poller` (Phase 1 stubs already declare these as interfaces; Phase 3 returns them from `New*`).

### Pattern D: Single-consumer channel for state mutations

**Source:** `internal/docker/discovery.go` lines 1–27 (the architectural-anchor block) — Phase 2 ships the producer pattern; the channel materializes in Phase 3.

**Apply to:** every state-mutation site in Phase 3 (poller sweep results, retry classifications, sweep-start/sweep-end timestamps, error capture). Producers send `poll.StateUpdate{Kind, Service, Apply}` on the buffered channel; the single consumer in `poll.RunUpdater` calls `store.Update(msg.Apply)`. The anti-deadlock invariant — never hold `state.Store.mu` across registry/docker I/O — is now structurally enforced by the channel (the producer's I/O completes before the channel send).

### Pattern E: Tygo source-of-truth — `internal/api/types.go` mirrors `internal/state/schema.go` verbatim

**Source:** `internal/state/schema.go` lines 19–24 + `internal/api/types.go` lines 1–14

**Apply to:** every Phase 3 schema field. Add to `schema.go`, mirror into `types.go` in the same commit, run `make check-types` to confirm `ui/src/lib/types.d.ts` regenerates cleanly.

### Pattern F: RED-FIRST test header listing what each test guards

**Source:** every `*_test.go` and `*.spec.ts` in the repo. Examples: `internal/docker/moby_test.go` lines 1–21, `internal/state/store_test.go` lines 1–16, `e2e/tests/discovery.spec.ts` lines 1–18.

**Apply to:** every new Phase 3 test file (`resolver_test.go`, `transport_test.go`, `poller_test.go`, `patterns_test.go`, `channel_test.go`, plus the 4 e2e specs). Bullet list of `TestSymbol: what it guards` is load-bearing for reviewer onboarding.

### Pattern G: slog event names follow `package.event.sub-event` dotted notation

**Source:** `internal/docker/discovery.go` (`"discovery.boot.start"`, `"discovery.event.received"`, `"discovery.events.reconnect"`, `"discovery.inspect.fail"`), `internal/compose/reader.go` (`"compose.reader.boot"`)

**Apply to:** `"registry.fetch"`, `"registry.authn"`, `"poll.sweep.start"`, `"poll.sweep.end"`, `"poll.consumer.persist"`, `"tag_pattern.invalid_regex"`. CONTEXT.md §"Claude's Discretion" specifies `elapsed_ms` (not `duration_ms`) for parity with `discovery.event.elapsed_ms`.

### Pattern H: `t.TempDir()` + `state.NewStore(filepath.Join(dir, "state.json"))` test scaffolding

**Source:** `internal/state/store_test.go` lines 27–30, `internal/docker/discovery_test.go` lines 181–189

**Apply to:** `channel_test.go`, `poller_test.go` — wherever a real `state.Store` is needed in unit tests.

### Pattern I: Goroutine assertions use `t.Errorf`, never `t.Fatal`

**Source:** `internal/docker/discovery_test.go` line 33 ("Goroutine assertion contract (per persist_test.go lines 29-31)")

**Apply:** every test that fires off-goroutine assertions (any `errgroup` test in `poller_test.go`, the consumer goroutine in `channel_test.go`).

## No Analog Found

| File | Role | Data Flow | Reason | Reference |
|------|------|-----------|--------|-----------|
| `internal/registry/transport.go` | middleware (http.RoundTripper) | request-response | No existing `http.RoundTripper` implementation in the repo. | RESEARCH.md §"Code Examples: Redacting transport" (verbatim 30-LOC template, lines 573–617 of `03-RESEARCH.md`) |

(Every other Phase 3 file has a strong in-repo analog.)

## Metadata

**Analog search scope:**
- `/Users/jonb/Projects/tmp/internal/` (all 5 sub-packages: api, compose, docker, state, registry, poll, actions)
- `/Users/jonb/Projects/tmp/cmd/hmi-update/`
- `/Users/jonb/Projects/tmp/e2e/tests/`
- `/Users/jonb/Projects/tmp/e2e/fixtures/`

**Files scanned:** 24 Go files + 4 Playwright spec files + 1 fixture file + 2 compose files = 31 source files.

**Strongest cross-cutting analog:** `internal/docker/discovery.go` — its long-running-goroutine + ctx-aware-sleep + drainEvents + slog-event-naming pattern is the template for the entire Phase 3 `internal/poll/` package. `internal/docker/moby.go` is the template for the entire `internal/registry/` package (facade-over-SDK shape).

**Pattern extraction date:** 2026-05-14
