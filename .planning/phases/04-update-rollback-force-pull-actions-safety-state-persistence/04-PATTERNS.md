# Phase 4: Update / Rollback / Force-pull Actions, Safety & State Persistence - Pattern Map

**Mapped:** 2026-05-15
**Files analyzed:** 23 new + 5 modified + 1 new helper binary + 9 new e2e specs/fixtures = 38 files
**Analogs found:** 37 / 38 (one fixture — `e2e/fixtures/disconnect-network.ts` — has no in-repo `docker network` analog; pattern comes verbatim from RESEARCH.md §"Disconnect-Network Fixture for Offline Rollback (ACT-04)")

## File Classification

### Production Go (new)

| New File | Role | Data Flow | Closest Analog | Match Quality |
|----------|------|-----------|----------------|---------------|
| `internal/compose/runner.go` | service (facade-over-CLI) | request-response | `internal/docker/moby.go` (facade-over-SDK) + sibling `internal/compose/reader.go` (struct shape, doc style) | exact (constructor-returns-interface, `package.Method`-prefixed error wraps, struct-level boot snapshot) |
| `internal/actions/orchestrator.go` | service (long-lived multi-step coordinator) | event-driven (HTTP request → channel send) | `internal/poll/poller.go` (orchestrator over multiple deps, ctx-aware lifecycle, channel send to single consumer) | exact (same anti-deadlock invariant, same `send(ctx, msg)` ctx-aware wrapper, same producer→channel→consumer triangle) |
| `internal/actions/mutex.go` | utility (per-key mutex map) | n/a | `internal/poll/patterns.go` (RWMutex-around-map pattern) + `internal/state/store.go` (RWMutex-around-value pattern) | exact (RWMutex protects map; lazy entry creation under write lock; read-mostly access path) |
| `internal/actions/middleware.go` | middleware (HTTP request validation) | request-response | `internal/api/handlers.go` (verbatim-constant response body pattern; defensive nil-guards; structured error JSON) | exact (same verbatim-string discipline as `healthzBody*` constants; same structured error shape) |
| `internal/actions/verify.go` | service (ticker-driven poll loop with ctx-aware cancel) | event-driven | `internal/poll/poller.go::Run` + `internal/docker/discovery.go::ctxAwareSleep` (lines 125–137) | exact (same `time.NewTicker(N) + defer Stop()` + `select { ctx.Done(); ticker.C }` shape) |
| `internal/actions/errors.go` | utility (sentinel errors) | n/a | `internal/compose/errors.go` (single sentinel + rich godoc) + `internal/registry/errors.go` (two sentinels + classify) | exact (same `errors.Is`-friendly package var pattern; same long-godoc-with-Phase-4-HTTP-mapping documentation style) |
| `internal/api/handlers_actions.go` | controller (HTTP handlers) | request-response | `internal/api/handlers.go::getState` + `::healthz` (verbatim-constant body strings; structured nil-defenses) | exact (same `w.Header().Set + WriteHeader + json.Encode` shape; same defensive guards) |
| `cmd/sigkillhelper/main.go` | helper binary (subprocess for fault injection) | n/a | **No in-repo analog** — first `cmd/` binary other than `hmi-update` | reference RESEARCH.md §"Pattern 6" lines 885–937 (full helper-binary template) |

### Production Go (modified)

| Modified File | Role | Data Flow | Closest Analog | Match Quality |
|---------------|------|-----------|----------------|---------------|
| `internal/state/schema.go` | model (extends `Container` with 2 fields) | n/a | itself (Phase 3 added `AvailableDigest`, `LastPolledAt`, `Notes` per identical pattern) | exact (append-only with `omitempty`, long godoc per field) |
| `internal/api/types.go` | model (tygo source-of-truth mirror) | n/a | itself (mirrors `state.Container` verbatim; Phase 3 precedent) | exact |
| `internal/poll/channel.go` | service (extends `UpdateKind` enum with 3 new variants) | pub-sub | itself (the enum lives there; Phase 4 appends to the iota block) | exact |
| `internal/api/server.go` | config (HTTP route wiring + constructor signature) | request-response | itself (Phase 2 extended `NewServer(store)` → `NewServer(store, dockerClient, composeReader)`; Phase 4 adds 4th arg + 3 routes) | exact |
| `cmd/hmi-update/main.go` | config (boot wiring) | n/a | itself (Phase 3 added 6 steps between 4 and 5; Phase 4 adds 2–3 more) | exact |

### Test Go (new)

| New Test File | Role | Data Flow | Closest Analog | Match Quality |
|---------------|------|-----------|----------------|---------------|
| `internal/compose/runner_test.go` | test (exec.Cmd seam injection) | request-response | `internal/compose/reader_test.go` (sibling — table-driven, `t.TempDir`, header doc style) + `internal/docker/moby_test.go` (compile-time interface-satisfaction guard) | exact (sibling-package style for test header; compile-time guard for interface; table-driven body) |
| `internal/actions/orchestrator_test.go` | test (multi-fake injection: docker.Client + compose.Runner + state.Store + resolver) | event-driven | `internal/poll/poller_test.go` (scripted fake pattern: `fakeResolver` with mutex-guarded scripts + call recording + concurrency instrumentation) | exact (transferable fake-with-mu pattern; same `digestScript map[string]string` shape for `pullScript` / `tagScript` / `composeScript`) |
| `internal/actions/mutex_test.go` | test (concurrent TryLock contention) | n/a | `internal/compose/reader_test.go::TestCheckUnchanged_Concurrent` (8 goroutines × N iterations under `-race`) + `internal/poll/patterns_test.go::TestPatterns_Concurrent_RaceClean` | exact (same `sync.WaitGroup` + `atomic.Int32` accumulator + `t.Errorf` discipline; RESEARCH.md lines 605–636 has the canonical body) |
| `internal/actions/middleware_test.go` | test (HTTP middleware table-driven rejection classes) | request-response | `internal/api/handlers_healthz_test.go::TestHealthzScenarios` (table-driven HTTP test with `httptest.NewRecorder`, structured-body assertions, verbatim-constant comparison) | exact (same table shape: `name, request setup, want status, want body`; same `Content-Type contains application/json` assertion) |
| `internal/actions/verify_test.go` | test (scripted `docker.Client.ContainerInspect` responses) | event-driven | `internal/poll/poller_test.go::fakeResolver` (scripted `Digest` responses with hooks) + `internal/docker/discovery_test.go::fakeClient` (lines 93–193 — scripted `Inspect` responses) | exact (replace `digestScript` with `inspectScript map[int]docker.ContainerInspect` for time-indexed tick-by-tick scripting) |
| `internal/api/handlers_actions_test.go` | test (httptest against orchestrator interface) | request-response | `internal/api/server_test.go` (`newTestServer` helper at lines 24–32) + `internal/api/handlers_healthz_test.go::fakeClient` (lines 31–73 — full `docker.Client` interface stub) | exact (transfer `newTestServer` pattern to take a `fakeOrchestrator`) |
| `internal/state/store_sigkill_test.go` | test (fault injection via subprocess SIGKILL) | n/a | `internal/state/persist_test.go::TestPersistAtomicity` (closest cousin — verifies single-process atomicity invariant; Phase 4 extends to cross-process SIGKILL) | role-match (build tag `//go:build sigkill_test` is new; RESEARCH.md lines 802–881 has the canonical parent-test body) |

### E2E (new)

| New e2e spec | Role | Data Flow | Closest Analog | Match Quality |
|--------------|------|-----------|----------------|---------------|
| `e2e/tests/update-flow.spec.ts` | test (HTTP action + state poll) | request-response | `e2e/tests/detect-multiarch.spec.ts` (push manifest → wait for predicate on `/api/state` → assert) | exact (same `waitForCondition` polling helper; add `POST /api/containers/.../update` step between push and assert) |
| `e2e/tests/rollback-flow.spec.ts` | test (HTTP action + offline) | request-response | `e2e/tests/detect-multiarch.spec.ts` + new `e2e/fixtures/disconnect-network.ts` | exact for online path; new fixture for offline path |
| `e2e/tests/idempotency.spec.ts` | test (HTTP action assertion on response body) | request-response | `e2e/tests/detect-multiarch.spec.ts` (response-body assertion shape) | exact (new assertion: `expect(body.no_op).toBe(true)`) |
| `e2e/tests/concurrent-actions.spec.ts` | test (parallel HTTP requests, mixed status assertion) | request-response | `e2e/tests/detect-multiarch.spec.ts` (request shape) | role-match (NEW pattern: `Promise.all([fetch, fetch])` then assert one 200 + one 409 in the result tuple) |
| `e2e/tests/self-protection.spec.ts` | test (negative-path 409 assertion) | request-response | `e2e/tests/healthz-negative.spec.ts` (negative-path status + body assertion pattern) | exact (transfer the `waitForHealth(url, 503, ...)` pattern → assert 409 on `POST /api/containers/hmi-update/update`) |
| `e2e/tests/safety-labels.spec.ts` | test (label-driven behavior + parallel poll assertion) | request-response | `e2e/tests/detect-tag-pattern.spec.ts` (label-driven assertion: container with specific label → expected /api/state behavior) | exact (same shape: stub container has `hmi-update.allow-update=false`; assert 409 on update; assert still polled — `last_polled_at` advances) |
| `e2e/tests/restart-persistence.spec.ts` | test (compose-restart + state survival) | request-response | `e2e/tests/compose-drift.spec.ts::afterAll` (`execSync` + `docker compose restart hmi-update` + `waitForHealth` poll loop, lines 43–53) | exact (RESEARCH.md lines 1401–1447 has the canonical body re-using exactly this pattern) |
| `e2e/tests/verify-failed.spec.ts` | test (action-then-assert; requires new crash-loop stub) | request-response | `e2e/tests/detect-multiarch.spec.ts` (action-then-assert shape) + RESEARCH.md Open Question #2 recommendation (new `crash-loop-stub` compose service) | role-match (action shape transfers; the new compose-service addition is documented in RESEARCH.md §"Open Questions" #2) |
| `e2e/fixtures/disconnect-network.ts` | fixture (subprocess `docker network` commands) | n/a | `e2e/fixtures/push-image.ts` (closest — `execSync`-against-CLI pattern; no in-repo `docker network` analog) | role-match (reference RESEARCH.md lines 1319–1372 for the canonical body) |

---

## Pattern Assignments

### `internal/compose/runner.go` (service, facade-over-CLI)

**Primary analog:** `/Users/jonb/Projects/tmp/internal/docker/moby.go` (lines 108–161 — package preamble + struct + constructor shape)
**Secondary analog:** `/Users/jonb/Projects/tmp/internal/compose/reader.go` (lines 25–98 — sibling package struct + NewReader pattern + `path` field idiom)

**Package preamble** (`moby.go` lines 108–116 + sibling `compose/reader.go` lines 1–14):

The new file replaces the current `runner.go` stub. Keep the existing `package compose` declaration. Replace the `type Runner interface{}` stub (line 13) with a method-bearing contract:

```go
type Runner interface {
    // UpdateService runs `docker compose -f <path> up -d --force-recreate <service>`.
    UpdateService(ctx context.Context, service string) error
    ComposePath() string
}
```

**Constructor pattern** (`moby.go` lines 154–161 — verbatim shape):

```go
func NewClient(ctx context.Context) (Client, error) {
    _ = ctx
    c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
    if err != nil {
        return nil, fmt.Errorf("docker.NewClient: %w", err)
    }
    return &mobyClient{c: c}, nil
}
```

**Apply to `runner.go`:** `NewRunner(composePath string) (Runner, error)` returns the interface (WR-04 pattern); fail-fast on `exec.LookPath("docker")` ENOENT; wrap with `"compose.NewRunner"` prefix. Concrete impl is unexported `execRunner` (struct mirrors `mobyClient`'s shape — three fields: `composePath`, `dockerBin`, and any future runner-options). RESEARCH.md lines 290–367 has the canonical body.

**Sentinel error wrap pattern** (`moby.go` line 178 — verbatim shape):

```go
return nil, fmt.Errorf("docker.ContainerList: %w", err)
```

**Apply to `runner.UpdateService`:** Same `package.Method` prefix; wrap `ErrComposeFailed` sentinel (defined in `internal/actions/errors.go`) so callers branch with `errors.Is`. The stderr snippet truncation (lines 344–347 of RESEARCH.md) is appended to the wrap chain.

**Doc-comment style** (`moby.go` lines 144–153 — note on threat T-02-01-02):

Long doc on the constructor explaining the failure modes + the security note (here: argv discipline / Pitfall 13 prevention). Match the `internal/compose/reader.go`'s "Production target: linux/amd64" portability note pattern (lines 11–13).

---

### `internal/compose/runner_test.go` (test, exec.Cmd seam)

**Primary analog:** `/Users/jonb/Projects/tmp/internal/compose/reader_test.go` (lines 1–37 — sibling test header doc style)
**Secondary analog:** `/Users/jonb/Projects/tmp/internal/docker/moby_test.go` (compile-time interface guard)

**Test header doc style** (`reader_test.go` lines 1–37):

```
// RED-FIRST per C4. These tests are authored before internal/compose/runner.go's
// execRunner body exists ...
//
// What these tests guard:
//   - TestNewRunner_DockerNotFound: ...
//   - TestUpdateService_HappyPath: ...
//   - TestUpdateService_NonZeroExit_ErrComposeFailed: ...
//   - TestUpdateService_StderrCaptured_Truncated: ...
//   - TestUpdateService_CtxCancel_SendsSIGTERM: ...
//   - TestUpdateService_ArgvDiscipline_NoShellInterpolation: ...
```

Mirror the verbatim shape.

**Compile-time interface guard** (`moby_test.go` lines ~38–41 — referenced in PATTERNS.md Phase 3):

```go
func TestExecRunner_SatisfiesRunner(t *testing.T) {
    t.Parallel()
    var _ Runner = (*execRunner)(nil)
}
```

**Apply:** load-bearing compile-time pin.

**exec.Cmd test seam:** No direct in-repo analog (this is the first `os/exec` facade test in the repo). Use a `commandRunner func(name string, args ...string) *exec.Cmd` field on the struct so tests inject a swapped factory pointing at `/bin/echo` (success) or `/bin/false` (non-zero exit). The pattern is documented in RESEARCH.md §"Pattern 1 Compose Runner" + `discovery.go::sleeper` field at lines 108–118 of `discovery.go` is the closest in-repo precedent (function-field test seam).

---

### `internal/actions/orchestrator.go` (service, multi-step coordinator)

**Primary analog:** `/Users/jonb/Projects/tmp/internal/poll/poller.go` (lines 1–536 — entire file; especially lines 1–67 for the doc-comment-with-anti-deadlock-anchor, lines 98–203 for the constructor/struct shape, lines 215–240 for the `Run(ctx)` lifecycle, lines 254–301 for the orchestrating method body)

**Anti-deadlock invariant header comment** (`poller.go` lines 1–43):

```go
// Package poll (continued). poller.go owns the cron scheduler that
// sweeps eligible containers per HMI_UPDATE_CRON tick. cronPoller is the
// SECOND producer of state mutations (Phase 2's docker events goroutine
// is the first); both feed the channel defined in channel.go.
//
// Architectural anchor (mirror of internal/docker/discovery.go's
// anti-deadlock invariant — see ARCHITECTURE.md lines 419-420):
//
//	cronPoller NEVER calls resolver.Digest from inside state.Store.Update's
//	closure. The sweep computes all digests in a bounded errgroup pool,
//	then sends one StateUpdate per container result on the channel. ...
```

**Apply to `orchestrator.go`:** Mirror-image — "Package actions (Phase 4 body). orchestrator.go is the THIRD producer of state mutations (Phase 2 docker events + Phase 3 cron poll were one and two); the action goroutines spawned per HTTP request feed the same channel defined in internal/poll/channel.go." Reuse the anti-deadlock language verbatim. Reference DETECT-10 invariant carrying forward.

**Constructor + interface pattern** (`poller.go` lines 73–203 — `Poller` interface, `cronPoller` struct, `NewPoller` constructor returning interface):

```go
type Poller interface {
    Run(ctx context.Context) error
}

type cronPoller struct {
    spec        string
    store       storeReader
    resolver    registry.Resolver
    patterns    *Patterns
    updates     chan<- StateUpdate
    timeout     time.Duration
    concurrency int
}

func NewPoller(...) (Poller, error) { ... }
```

**Apply to `orchestrator.go`:** Replace the empty stub at `internal/actions/orchestrator.go:17`:

```go
type Orchestrator interface {
    Update(ctx context.Context, service string) (ActionResult, error)
    Rollback(ctx context.Context, service string) (ActionResult, error)
    ForcePull(ctx context.Context, service string, recreate bool) (ActionResult, error)
    // Middleware-accessible helpers
    LookupContainer(svc string) (state.Container, bool)
    SelfService() string
}

type actionOrchestrator struct {
    mu          sync.RWMutex
    locks       map[string]*sync.Mutex   // see mutex.go
    docker      docker.Client
    runner      compose.Runner
    resolver    registry.Resolver
    composeRdr  *compose.Reader
    store       *state.Store
    updates     chan<- poll.StateUpdate
    selfService string
    verifyWindow      time.Duration
    healthcheckWindow time.Duration
}

func NewOrchestrator(...) (Orchestrator, error) { ... }
```

The constructor follows the WR-04 pattern (returns interface; concrete struct unexported). Open Question #4 in RESEARCH.md concurs.

**Channel send pattern** (`poller.go` lines 504–509 — ctx-aware `send` wrapper):

```go
func (p *cronPoller) send(ctx context.Context, u StateUpdate) {
    select {
    case p.updates <- u:
    case <-ctx.Done():
    }
}
```

**Apply verbatim** to `actionOrchestrator.send`. Three new `UpdateKind` constants (`KindActionStart`, `KindActionProgress`, `KindActionResult`) are produced through this same wrapper.

**Slog dotted-event-name convention** (`poller.go` lines 198–202 + `discovery.go` shape):

Event names: `"action.start"`, `"action.phase"`, `"action.complete"`, `"action.verify_failed"`, `"action.compose_failed"`, `"action.pull_failed"` (RESEARCH.md lines 1461–1468 lock the spelling). Match the dotted convention.

**Linear sequence flow** — RESEARCH.md lines 175–241 has the ASCII flow diagram; CONTEXT.md Area 1 lines 32–46 has the verbatim 11-step Update flow. The orchestrator body is mechanical translation.

---

### `internal/actions/orchestrator_test.go` (test, fake-injection)

**Primary analog:** `/Users/jonb/Projects/tmp/internal/poll/poller_test.go` (lines 1–150 — `fakeResolver` scripted-response pattern; the entire 1006-line file is the template for multi-fake table-driven tests)

**Scripted-fake-with-mutex pattern** (`poller_test.go` lines 55–116):

```go
type fakeResolver struct {
    mu              sync.Mutex
    digestScript    map[string]string
    digestErrScript map[string]error
    digestCalls     []string
    digestHook      func(ref string)
    inFlight        int32
    maxInFlight     int32
    delay           time.Duration
}
```

**Apply:** Need three new fakes living in this test file:

1. `fakeDockerClient` — extends `internal/api/handlers_healthz_test.go::fakeClient` (lines 31–73, full Client interface stubs). Add scripted responses for `ImagePull` (returns a `bytes.Buffer` containing JSON pull-progress messages with an `aux` digest), `ImageTag` (success/error script), `ContainerInspect` (time-indexed script for verify-loop tests).
2. `fakeRunner` — implements `compose.Runner` with `updateServiceScript map[string]error` and `updateCalls []string` recording (mirror `fakeResolver` shape).
3. `fakeResolver` — already exists in `internal/poll/poller_test.go`; the actions package may need its own copy (sibling-package copy-paste convention per channel_test.go's `newTestStore` helper).

**Call-count helper pattern** (`poller_test.go` lines 110–116):

```go
func (f *fakeResolver) callCounts() (n int, refs []string) {
    f.mu.Lock()
    defer f.mu.Unlock()
    out := make([]string, len(f.digestCalls))
    copy(out, f.digestCalls)
    return len(f.digestCalls), out
}
```

**Apply:** every fake exposes `callCounts()` for ordering assertions.

**Goroutine assertion contract** (`poller_test.go` lines 37–39 — load-bearing):

```
// Goroutine assertion contract (per discovery_test.go line 33): assertions
// fired off-goroutine use t.Errorf, NEVER t.Fatal — the sweep dispatches
// up to 4 worker goroutines under errgroup.
```

**Apply:** the action orchestrator dispatches goroutines for verify-after-recreate; off-goroutine assertions use `t.Errorf`.

**Test cases to land (RED-first contract — header doc):**
- `TestOrchestrator_SatisfiesOrchestrator` (compile-time)
- `TestUpdate_HappyPath` (ACT-01 + ACT-02 + ACT-11)
- `TestUpdate_Idempotent_NoOp` (ACT-06)
- `TestUpdate_PullFailed_State_ActionError_Set` (ACT-01 failure branch)
- `TestUpdate_DigestMismatch_AbortsBeforeCompose` (Pitfall 1)
- `TestUpdate_ComposeFailed_State_ActionError_Set` (Pitfall 4/12)
- `TestRollback_HappyPath` (ACT-03)
- `TestRollback_NoPreviousDigest_400` (Rollback flow step 2)
- `TestRollback_OfflineWorks` (ACT-04 unit-level: no resolver call)
- `TestForcePull_Default_NoRecreate` (ACT-05 default)
- `TestForcePull_WithRecreate_FullUpdateFlow` (ACT-05 `?recreate=true`)
- `TestOrchestrator_CheckUnchangedFirst_412OnDrift` (Pitfall G integration)
- `TestOrchestrator_SendsKindActionStart_Then_KindActionResult` (DETECT-10 carry-forward)

---

### `internal/actions/mutex.go` (utility, per-key mutex map)

**Primary analog:** `/Users/jonb/Projects/tmp/internal/poll/patterns.go` (lines 50–106 — entire struct + RWMutex-around-map pattern with `Set`/`Match`/`Delete` API)
**Secondary analog:** `/Users/jonb/Projects/tmp/internal/state/store.go` (lines 23–27 — RWMutex-around-value pattern)

**RWMutex-around-map pattern** (`patterns.go` lines 50–58):

```go
type Patterns struct {
    mu sync.RWMutex
    m  map[string]*regexp.Regexp
}

func NewPatterns() *Patterns {
    return &Patterns{m: map[string]*regexp.Regexp{}}
}
```

**Apply to `mutex.go`:** the `actionOrchestrator` struct already holds `mu sync.RWMutex` + `locks map[string]*sync.Mutex` (per CONTEXT.md Area 2). `mutex.go` houses the `lockService` method + `unlockService` closure. Double-checked locking on entry creation per RESEARCH.md lines 568–591:

```go
func (o *actionOrchestrator) lockService(svc string) (func(), error) {
    o.mu.RLock()
    m, ok := o.locks[svc]
    o.mu.RUnlock()
    if !ok {
        o.mu.Lock()
        m, ok = o.locks[svc]
        if !ok {
            m = &sync.Mutex{}
            o.locks[svc] = m
        }
        o.mu.Unlock()
    }
    if !m.TryLock() {
        return nil, ErrServiceBusy
    }
    return m.Unlock, nil
}
```

**Why this shape over `sync.Map`:** CONTEXT.md Area 2 explicitly leans `map+RWMutex` ("explicit, easier to reason about than `sync.Map`'s opaque sharding"). The `patterns.go` precedent is the direct in-repo template.

---

### `internal/actions/mutex_test.go` (test, concurrent contention)

**Primary analog:** `/Users/jonb/Projects/tmp/internal/poll/patterns_test.go::TestPatterns_Concurrent_RaceClean` (lines 99–125 — `sync.WaitGroup` + N goroutines × M iterations under `-race`)
**Secondary analog:** `/Users/jonb/Projects/tmp/internal/state/persist_test.go::TestPersistAtomicity` (lines 31–99 — goroutine-assertion-with-`t.Errorf` pattern)

**Concurrent test shape** (RESEARCH.md lines 605–636 has the canonical body):

```go
func TestLockService_Concurrent(t *testing.T) {
    o := &actionOrchestrator{locks: map[string]*sync.Mutex{}}
    var wg sync.WaitGroup
    acquired := atomic.Int32{}
    rejected := atomic.Int32{}
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            unlock, err := o.lockService("svc-a")
            if err != nil {
                rejected.Add(1)
                return
            }
            acquired.Add(1)
            time.Sleep(time.Microsecond)
            unlock()
        }()
    }
    wg.Wait()
    if acquired.Load() < 1 || rejected.Load() < 1 {
        t.Errorf("expected mix of acquired+rejected; got %d/%d", acquired.Load(), rejected.Load())
    }
}
```

**Goroutine assertion contract** (`patterns_test.go` lines 27–30): use `t.Errorf` inside the closure (the comment is verbatim load-bearing for the off-goroutine discipline).

**Test cases to land:**
- `TestLockService_FirstAcquireSucceeds`
- `TestLockService_SecondAcquireReturnsErrServiceBusy`
- `TestLockService_UnlockAllowsReacquire`
- `TestLockService_CrossServiceParallelism` (lock svc-a, lock svc-b → both succeed)
- `TestLockService_Concurrent` (100 goroutines, mix of acquired+rejected, race-clean)
- `TestLockService_DoubleCheckedLocking_NoDuplicateMutex` (Phase-4-specific pitfall — RESEARCH.md lines 533–540)

Run with `go test -race -count=50 ./internal/actions/...`.

---

### `internal/actions/middleware.go` (middleware, HTTP request validation)

**Primary analog:** `/Users/jonb/Projects/tmp/internal/api/handlers.go` (lines 31–58 — verbatim-constant response body pattern; lines 60–79 — `looksLikeSocketEACCES` narrow-substring backstop; lines 113–211 — defensive-guard layered detection ladder)

**Verbatim-constant response body pattern** (`handlers.go` lines 45–58):

```go
const (
    healthzBodyOK            = `{"status":"ok"}`
    healthzBodySocketEACCES  = `{"status":"unhealthy","reason":"docker socket permission denied — set compose user: '65532:$(id -g docker)' (Pitfall 9)"}`
    healthzBodySocketMissing = `{"status":"unhealthy","reason":"docker socket missing — add bind-mount '/var/run/docker.sock:/var/run/docker.sock'"}`
    // ...
)
```

**Apply to `middleware.go`:** Define a single block of action-error body constants:

```go
const (
    actionBodyInvalidServiceName  = `{"error":"invalid_service_name","detail":"service name must match ^[a-zA-Z0-9._-]+$"}`
    actionBodyContainerNotFound   = `{"error":"container_not_found"}`
    actionBodySelfProtection      = `{"error":"self_protection","detail":"see PROJECT.md 'Manual self-upgrade procedure'"}`
    actionBodyActionDisabledLabel = `{"error":"action_disabled_by_label","detail":"hmi-update.allow-update=false"}` // or allow-rollback variant
    actionBodyServiceBusy         = `{"error":"service_busy"}`
    actionBodyComposeFileMoved    = `{"error":"compose_file_moved","hint":"restart hmi-update to pick up the new docker-compose.yml"}`
)
```

Match the discipline that EVERY response body is a verbatim constant (matches `handlers.go` "do NOT interpolate variables into these constants" doc-comment at lines 35–42; carry that comment into `middleware.go`). The `compose_file_moved` body is already specified in `internal/compose/errors.go` lines 19–28.

**Layered detection ladder** (`handlers.go` lines 113–211 — 5-step ladder with structured early returns):

The middleware chain follows the identical pattern (RESEARCH.md lines 949–1057 has the canonical bodies):

1. Service-name regex → 400
2. State lookup → 404
3. Self-protection → 409
4. Safety-label → 409
5. (downstream to orchestrator: compose check → 412, mutex → 409, idempotency → 200)

Each step uses the helper `writeError(w, status, body)` (matches `handlers.go` `w.Header().Set(...) + w.WriteHeader(status) + w.Write([]byte(body))` shape at lines 113–120).

**Regex-compiled-once-at-boot pattern** (`internal/poll/patterns.go::Set` uses `regexp.Compile` at runtime, but RESEARCH.md lines 977 leans `regexp.MustCompile` at package init for the service-name allowlist):

```go
var serviceNameRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
```

This is appropriate since the regex is hard-coded (not operator-supplied). `MustCompile` panic at boot is acceptable (compile-time programmer error, never fires for a valid literal). Phase 3's `internal/registry/transport.go` similarly hard-codes `sensitiveHeaders` (lines 44–49) — same compile-time-constant discipline.

---

### `internal/actions/middleware_test.go` (test, table-driven HTTP)

**Primary analog:** `/Users/jonb/Projects/tmp/internal/api/handlers_healthz_test.go::TestHealthzScenarios` (lines 98–259 — table-driven HTTP test with full setup matrix)

**Table-driven HTTP pattern** (`handlers_healthz_test.go` lines 98–145):

```go
func TestHealthzScenarios(t *testing.T) {
    cases := []struct {
        name           string
        // ... setup fields ...
        wantStatus     int
        wantBodyExact  string  // verbatim match against the constant
        // ... assertion fields ...
    }{
        // 8 scenarios per plan 02-04 <behavior>
    }
    for _, tc := range cases {
        tc := tc
        t.Run(tc.name, func(t *testing.T) {
            // setup, httptest.NewRecorder, dispatch, assert
        })
    }
}
```

**Apply:** rows for each middleware rejection class:
- `invalid_service_name` (path traversal probe: `../../etc/passwd`)
- `container_not_found` (svc not in state)
- `self_protection` (svc matches `HMI_UPDATE_SELF_SERVICE`)
- `action_disabled_by_label_update` (`hmi-update.allow-update=false`)
- `action_disabled_by_label_rollback` (`hmi-update.allow-rollback=false`)
- `force_pull_not_governed_by_safety_label` (SAFE-03 carve-out: force-pull on `allow-update=false` container succeeds reach orchestrator)

**Verbatim-body comparison** (`handlers_healthz_test.go` lines ~150–180 — exact-match assertion against the constant): the test asserts the response body equals the constant by name, not by re-typing the JSON. Catches typos in either place.

---

### `internal/actions/verify.go` (service, ticker-driven poll loop)

**Primary analog:** `/Users/jonb/Projects/tmp/internal/poll/poller.go::Run` (lines 215–240 — cron Start/Stop + `<-ctx.Done()` + drain pattern)
**Secondary analog:** `/Users/jonb/Projects/tmp/internal/docker/discovery.go::ctxAwareSleep` (lines 125–137 — `select { ctx.Done(); t.C }` shape)

**`time.NewTicker` + ctx-aware select pattern** (no direct in-repo match for the *consecutive-success-counter* pattern; RESEARCH.md lines 651–771 has the canonical body):

```go
ticker := time.NewTicker(verifyTickInterval)
defer ticker.Stop()
for {
    select {
    case <-ctx.Done():
        return ErrVerifyCanceled
    case <-ticker.C:
        // tick body: ContainerInspect, classify, increment/reset counter
    }
}
```

The shape mirrors `discovery.go::ctxAwareSleep` (lines 125–137) and the `poll.RunUpdater` consumer (`channel.go` lines 110–135) — both use the `for { select { case <-ctx.Done(); case msg := <-ch } }` skeleton.

**Verify-failure error shape** — locked in CONTEXT.md Area 3 lines 102–112; landed via `fmt.Errorf("%w: ...", ErrVerifyFailed)` so callers branch with `errors.Is`. Pattern matches `internal/registry/errors.go::classify` (lines 75–101) verbatim.

**Healthcheck opt-in branch:** When `Labels["hmi-update.wait-for-healthy"] == "true"`, the deadline extends to `healthcheckWindow`. The label lookup uses the cached `state.Container.Labels` (no `ContainerInspect` for label retrieval — matches the SAFE-03 / OBS-03 no-I/O discipline used by middleware).

---

### `internal/actions/verify_test.go` (test, scripted inspect responses)

**Primary analog:** `/Users/jonb/Projects/tmp/internal/poll/poller_test.go::fakeResolver` (scripted responses with hooks) + `/Users/jonb/Projects/tmp/internal/docker/discovery_test.go::fakeClient` (lines 93–193 — scripted `ContainerInspect` responses)

**Scripted Inspect pattern** (`discovery_test.go` lines 102–157):

```go
type fakeClient struct {
    mu sync.Mutex
    inspectScript map[string]ContainerInspect
    inspectCalls  []string
    inspectHook   func(id string)
}

func (f *fakeClient) ContainerInspect(ctx context.Context, id string) (ContainerInspect, error) {
    f.mu.Lock()
    hook := f.inspectHook
    insp, ok := f.inspectScript[id]
    f.inspectCalls = append(f.inspectCalls, id)
    f.mu.Unlock()
    if hook != nil { hook(id) }
    if !ok {
        return ContainerInspect{}, errors.New("...")
    }
    return insp, nil
}
```

**Apply:** verify_test uses an `inspectScript` keyed by *tick index* (call number) — each tick gets the next scripted response in sequence. Implement as `[]docker.ContainerInspect` indexed by `len(inspectCalls)`. This lets a single test row script "tick 1: Running, tick 2: Running, tick 3: RestartCount++" and assert `ErrVerifyFailed` on tick 3.

**Test cases:**
- `TestVerifyAfterRecreate_15ConsecutiveSuccessfulTicks_Returns_Nil` (happy path, 15 inspect calls)
- `TestVerifyAfterRecreate_RestartCountIncremented_ReturnsErrVerifyFailed` (fail at tick 3)
- `TestVerifyAfterRecreate_NotRunning_ReturnsErrVerifyFailed` (fail immediately)
- `TestVerifyAfterRecreate_CtxCanceled_ReturnsErrVerifyCanceled` (distinct from `ErrVerifyFailed`)
- `TestVerifyAfterRecreate_HealthcheckOptIn_Healthy_ReturnsNil` (60s window; healthy short-circuit)
- `TestVerifyAfterRecreate_HealthcheckOptIn_Unhealthy_ReturnsErrVerifyFailed`
- `TestVerifyAfterRecreate_HealthcheckOptIn_NoStatusAfter60s_SoftSuccess` (RESEARCH.md lines 696–714)
- `TestVerifyAfterRecreate_TickBoundary_CancelDuringFinalTick_ReturnsErrVerifyCanceled` (RESEARCH.md lines 1490–1492 — Phase-4-specific pitfall)

---

### `internal/actions/errors.go` (utility, sentinel errors)

**Primary analog:** `/Users/jonb/Projects/tmp/internal/registry/errors.go` (lines 1–53 — two sentinels + long godoc + errors.Is-friendly wrapping)
**Secondary analog:** `/Users/jonb/Projects/tmp/internal/compose/errors.go` (entire 44-line file — single-sentinel form)

**Full canonical excerpt to mirror** (`registry/errors.go` lines 1–53):

```go
// Package registry (continued). errors.go defines the two sentinel
// errors the Phase 3 poller branches on. See compose/errors.go for the
// codebase's first instance of this pattern (Phase 2 plan 02-02); this
// file mirrors its shape with two sentinels instead of one ...
//
// Callers test with errors.Is so the sentinel identity survives any
// number of fmt.Errorf("registry: %w", ...) wraps:
//
//	if _, err := resolver.Digest(ctx, ref); err != nil {
//	    if errors.Is(err, registry.ErrPermanent) { ... }
//	    if errors.Is(err, registry.ErrTransient) { ... }
//	}
//
// Phase 4 will additionally map ErrPermanent to a 4xx response on
// POST /api/containers/:svc/update; that mapping is not in scope here.
package registry

var ErrPermanent = errors.New("registry: permanent error (401/403/404; do not retry)")
var ErrTransient = errors.New("registry: transient error (5xx/timeout; retry once)")
```

**Apply to `actions/errors.go`:** Mirror with 7 sentinels (per RESEARCH.md Component Responsibilities table):

```go
var ErrServiceBusy             = errors.New("actions: service busy (per-service mutex held)")
var ErrSelfProtection          = errors.New("actions: refusing self-action (see PROJECT.md self-upgrade procedure)")
var ErrActionDisabledByLabel   = errors.New("actions: action disabled by hmi-update.allow-* label")
var ErrVerifyFailed            = errors.New("actions: verify-after-recreate failed")
var ErrVerifyCanceled          = errors.New("actions: verify-after-recreate canceled by context")
var ErrComposeFailed           = errors.New("actions: compose runner returned non-zero exit")
var ErrPullFailed              = errors.New("actions: docker pull failed or digest mismatch")
```

Each gets a long godoc explaining: (a) HTTP status code the API handler maps to, (b) how middleware/orchestrator branches on `errors.Is`, (c) the wrap pattern (`fmt.Errorf("actions: ... %w", ErrFoo)`).

**Note:** The `internal/registry/errors.go` file additionally houses the `classify()` function (lines 69–101); the action layer does NOT need an analogous classifier because the error origin is unambiguous at each call site (we know we're calling docker.ImagePull → wrap with ErrPullFailed, etc.). Keep `errors.go` focused on the sentinels.

---

### `internal/api/handlers_actions.go` (controller, HTTP handlers)

**Primary analog:** `/Users/jonb/Projects/tmp/internal/api/handlers.go` (lines 112–228 — `healthz` + `getState` handlers — verbatim shape for `w.Header().Set + WriteHeader + json.Encode`)

**Handler skeleton** (`handlers.go` lines 219–228 — `getState`):

```go
func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
    st := s.store.Get()
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    w.Header().Set("Cache-Control", "no-store")
    if err := json.NewEncoder(w).Encode(st); err != nil {
        slog.Error("getState: encode failed", "err", err)
        return
    }
}
```

**Apply to `handleUpdate`** (RESEARCH.md lines 1094–1110 has the canonical body):

```go
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
    svc, ok := actions.ValidateServiceName(w, r)
    if !ok { return }
    c, ok := s.orchestrator.LookupContainer(svc)
    if !ok { actions.WriteContainerNotFound(w); return }
    if !s.orchestrator.CheckSelfProtection(w, svc) { return }
    if !actions.CheckSafetyLabel(w, c, actions.ActionUpdate) { return }
    result, err := s.orchestrator.Update(r.Context(), svc)
    if err != nil { s.orchestrator.WriteActionError(w, err); return }
    actions.WriteActionResult(w, result)
}
```

`handleRollback` and `handleForcePull` follow the identical shape; force-pull additionally reads `r.URL.Query().Get("recreate") == "true"` to switch to the full Update flow.

**Defensive nil-guard pattern** (`handlers.go` lines 117–141 — step 1 + step 2 nil-defense): every new handler does a defensive `if s.orchestrator == nil { return 503 }` style check (matches the `s.store == nil` and `s.dockerClient == nil` precedents). RESEARCH.md A6 confirms `state.Store.Get()` is RLock-safe under middleware concurrent reads.

---

### `internal/api/handlers_actions_test.go` (test, httptest)

**Primary analog:** `/Users/jonb/Projects/tmp/internal/api/server_test.go` (lines 18–71 — `newTestServer` + `newTestServerWithContainer` + `newTestReader` helpers)
**Secondary analog:** `/Users/jonb/Projects/tmp/internal/api/handlers_healthz_test.go::fakeClient` (lines 31–73 — full `docker.Client` interface stub)

**Test helper pattern** (`server_test.go` lines 24–71):

```go
func newTestServer(t *testing.T) *Server {
    t.Helper()
    dir := t.TempDir()
    store, err := state.NewStore(filepath.Join(dir, "hmi_update_state.json"))
    if err != nil { t.Fatalf("state.NewStore: %v", err) }
    return NewServer(store, fakeClient{}, newTestReader(t, dir))
}
```

**Apply:** extend `newTestServer` with a `fakeOrchestrator` injected as the new 4th argument (NewServer signature change). The `fakeOrchestrator` lives in `handlers_actions_test.go` as a struct implementing `actions.Orchestrator` with scripted method responses.

**Status assertion pattern** (`handlers_healthz_test.go` lines 145–225 — `wantStatus`, `wantBodyExact`, JSON-validity, content-type assertions):

Apply to each handler — every test case asserts (a) HTTP status, (b) response body verbatim (compare against the `actionBody*` constants), (c) `Content-Type: application/json`. The verbatim-constant comparison catches typos in either location.

---

### `internal/state/store_sigkill_test.go` (test, fault injection)

**Primary analog:** `/Users/jonb/Projects/tmp/internal/state/persist_test.go::TestPersistAtomicity` (the closest cousin — verifies the *single-process* atomicity invariant via goroutine writer + reader race; Phase 4 extends to *cross-process* SIGKILL atomicity)

**Header doc pattern** (`persist_test.go` lines 1–11):

```
// RED-FIRST per C4. This test is authored before ...
//
// What this test guards (FOUND-02 / STATE-02): under heavy write contention,
// every reader of the on-disk state file must see either the previous valid
// JSON snapshot or the next valid JSON snapshot — never a torn or truncated
// half-write. ...
```

**Apply:**

```
// RED-FIRST per C4. Build-tagged `//go:build sigkill_test` so default
// `go test ./...` stays fast (CONTEXT.md "STATE-04 fault injection runs
// ONLY in `make test-sigkill`").
//
// What this test guards (STATE-04): the renameio + parent-dir-fsync pattern
// (internal/state/persist.go) must leave hmi_update_state.json in a parseable
// state (either prior or new content) even when the writer process is
// SIGKILLed mid-write. Parent-test spawns cmd/sigkillhelper, sends SIGKILL
// at randomized 1-50ms intervals, verifies the on-disk file parses cleanly
// after every iteration. 100 iterations, zero corruption.
```

**Build tag** (RESEARCH.md lines 803–807):

```go
//go:build sigkill_test
// +build sigkill_test
```

**Test body** — RESEARCH.md lines 827–880 has the canonical 50-line body. Parent calls `go build -o tmpDir/sigkillhelper ./cmd/sigkillhelper`, then loops: spawn → sleep random delay → SIGKILL → wait → ReadFile → json.Unmarshal → assert no error.

**Makefile target** — RESEARCH.md lines 942–944:

```makefile
.PHONY: test-sigkill
test-sigkill:
	go test -tags=sigkill_test -count=1 -run TestSIGKILL ./internal/state/...
```

---

### `cmd/sigkillhelper/main.go` (helper binary)

**No in-repo analog.** This is the second `cmd/` binary (first is `hmi-update`). The helper is intentionally minimal — a state-write loop spawned by the parent test.

**Reference:** RESEARCH.md lines 884–937 has the full canonical body. Three load-bearing properties:

1. CLI signature: `sigkillhelper <state-path>` — single argv element.
2. Tight loop: `for { counter++; store.Update(...); time.Sleep(100*time.Microsecond) }`.
3. Each iteration writes a *distinct* payload (counter embedded in `CurrentDigest: fmt.Sprintf("sha256:%064d", counter)`) so a torn write would manifest as a truncated JSON document.

**Build:** the helper builds via `go build ./cmd/sigkillhelper` from the parent test's `t.TempDir()`; never packaged into the production image (it's an ephemeral test fixture). The build invocation is the first step of the parent test.

---

### `internal/state/schema.go` (MODIFY, model)

**Analog:** itself — Phase 2 added `ContainerID`, `Labels`, `Pinned`, `Stopped`; Phase 3 added `AvailableDigest`, `LastPolledAt`, `Notes` (lines 54–117). Same append-only-with-omitempty pattern.

**Field-addition pattern** (`schema.go` lines 86–117):

```go
// AvailableDigest is the upstream sha256 most recently fetched by the
// Phase 3 poll loop. Empty until the first successful resolver.Digest()
// call. Compared against CurrentDigest to compute UpdateAvailable.
// Set by the poll consumer goroutine (internal/poll/channel.go); never
// mutated outside state.Store.Update. omitempty so a not-yet-polled
// row does not clutter the wire payload with "" (DETECT-05/DETECT-07).
AvailableDigest string `json:"available_digest,omitempty"`
```

**Apply for Phase 4 additions** (per CONTEXT.md Area 1 "state.Container extensions"):

```go
// ActionInFlight is the current in-flight per-row action (Phase 4).
// Values: "" (idle), "updating", "rolling_back", "force_pulling".
// Set by orchestrator via KindActionStart; cleared via KindActionResult.
// UI Phase 5 reads this for per-row spinner state. omitempty so an idle
// container does not clutter the wire payload with "".
ActionInFlight string `json:"action_in_flight,omitempty"`

// ActionError is the last action's failure surface (Phase 4). Empty when
// the most recent action succeeded. Format: "<phase>_failed: <reason>"
// e.g. "verify_failed: container restarted 3 times in 15s". Cleared on
// the next successful action. Matches the Notes precedent (single short
// string, not a structured object). UI Phase 5 reads this for a toast.
ActionError string `json:"action_error,omitempty"`
```

**Forward-compat invariant** (CONTEXT.md note + Phase 3 verified pattern):

Pre-Phase-4 on-disk Phase-3 state files load cleanly with `ActionInFlight=""` and `ActionError=""` (both `omitempty`). The Phase 4 test should add `TestPhase4SchemaFields_ForwardCompat_Phase3OnDisk` mirroring `internal/state/schema_phase3_test.go` lines 1–100 verbatim.

---

### `internal/api/types.go` (MODIFY, model)

**Analog:** itself — Phase 3 mirror precedent (lines 64–77 of `types.go`).

**Verbatim mirror** of the two `state.Container` Phase 4 additions:

```go
// ActionInFlight is the current in-flight per-row action (Phase 4).
// See internal/state.Container.ActionInFlight for full semantics.
ActionInFlight string `json:"action_in_flight,omitempty"`

// ActionError is the last action's failure surface (Phase 4). See
// internal/state.Container.ActionError for the full format.
ActionError string `json:"action_error,omitempty"`
```

Tags byte-identical. `make check-types` enforces; Phase 4 should add a sibling `internal/api/types_phase4_test.go` mirroring `internal/api/types_phase3_test.go` lines 1–60 verbatim.

---

### `internal/poll/channel.go` (MODIFY, service)

**Analog:** itself — the `UpdateKind iota` block at lines 44–64.

**Existing iota block** (`channel.go` lines 44–64):

```go
type UpdateKind int

const (
    KindDigestResolved UpdateKind = iota
    KindContainerEvent
    KindPollSweepStart
    KindPollSweepEnd
)
```

**Apply (append-only):**

```go
const (
    KindDigestResolved UpdateKind = iota
    KindContainerEvent
    KindPollSweepStart
    KindPollSweepEnd

    // Phase 4 — action lifecycle (orchestrator producer).
    // See internal/actions/orchestrator.go.

    // KindActionStart marks the orchestrator entering an action body.
    // Apply closure sets state.Container.ActionInFlight to one of
    // "updating", "rolling_back", "force_pulling".
    KindActionStart

    // KindActionProgress carries an intermediate phase (pulled, recreated).
    // Apply closure is a no-op on state currently (reserved for Phase 5
    // UI breadcrumbs); included for observability symmetry.
    KindActionProgress

    // KindActionResult marks the orchestrator completing or aborting an action.
    // Apply closure clears ActionInFlight, sets CurrentDigest/PreviousDigest
    // on success, sets ActionError on failure.
    KindActionResult
)
```

**Doc-comment header update:** the package preamble at lines 1–31 already says "Phase 4 will add a third for actions: update, rollback, force-pull"; Phase 4 lands the materialization. Update the comment to reflect that the third producer is now wired in.

---

### `internal/api/server.go` (MODIFY, config)

**Analog:** itself — Phase 2 extended the constructor from `NewServer(store)` to `NewServer(store, dockerClient, composeReader)` (lines 56–71).

**Routes table** (`server.go` lines 81–87):

```go
func (s *Server) routes() {
    s.mux.HandleFunc("GET /healthz", s.healthz)
    s.mux.HandleFunc("GET /api/state", s.getState)
    s.mux.Handle("/", newStaticHandler())
}
```

**Apply (Phase 4 adds 3 routes; RESEARCH.md lines 1082–1090):**

```go
func (s *Server) routes() {
    s.mux.HandleFunc("GET /healthz", s.healthz)
    s.mux.HandleFunc("GET /api/state", s.getState)
    // Phase 4 action endpoints (ACT-01..05).
    s.mux.HandleFunc("POST /api/containers/{service}/update", s.handleUpdate)
    s.mux.HandleFunc("POST /api/containers/{service}/rollback", s.handleRollback)
    s.mux.HandleFunc("POST /api/containers/{service}/force-pull", s.handleForcePull)
    s.mux.Handle("/", newStaticHandler())
}
```

**Constructor signature change** (extends Phase 2 precedent):

`NewServer(store *state.Store, dockerClient docker.Client, composeReader *compose.Reader)` →
`NewServer(store *state.Store, dockerClient docker.Client, composeReader *compose.Reader, orchestrator actions.Orchestrator)`.

**Test impact:** `internal/api/server_test.go::newTestServer` (lines 24–32) gets a 4th argument. All existing call sites in `server_test.go` and `handlers_healthz_test.go` update. The defensive-nil-guard pattern (`handlers.go` line 137 `if s.dockerClient == nil`) extends to `if s.orchestrator == nil` — return 503 with a documented `orchestratorUnwired` body. Match the WR-03 fix style (`healthzBodyClientUnwired` at lines 51–57).

---

### `cmd/hmi-update/main.go` (MODIFY, config)

**Analog:** itself — Phase 2 (lines 144–179) and Phase 3 (lines 189–265) layered new boot steps with verbose comments. Phase 4 continues the pattern.

**Boot order pattern** (`main.go` lines 1–37 — the documented step list at the top):

```go
// Phase 3 boot order (CONTEXT.md "Lifecycle & Wiring" + 03-04-PLAN.md):
//  1. slog handler (level via HMI_UPDATE_LOG_LEVEL)
//  2. state.NewStore (path via HMI_UPDATE_STATE_PATH)
//  3. docker.NewClient(ctx)
//  4. compose.NewReader(env)
//  4.5. registry.NewRedactingTransport
//  4.6. registry.NewResolver(transport)
//  4.7. slog.Info("registry.authn", "keychain", "anonymous")
//  4.8. poll.NewPatterns
//  4.9. updates := make(chan poll.StateUpdate, 64)
//  4.10. go poll.RunUpdater(ctx, updates, store)
//  5. docker.NewDiscoverer(dockerClient, store, updates, patterns)
//  5.5. cronExpr from HMI_UPDATE_CRON
//  5.6. poll.NewPoller(cronExpr, resolver, patterns, store, updates)
//  5.7. go poller.Run(ctx)
//  6. api.NewServer(store, dockerClient, composeReader).ListenAndServe(":8080")
```

**Apply (Phase 4 — insert steps 4.11, 5.8, 5.9; update step 6 signature):**

```go
// Phase 4 boot order additions (CONTEXT.md "Integration Points"):
//  4.11. runner, err := compose.NewRunner(composePath)   [fail-fast on docker CLI missing]
//  5.8.  selfService := getenv("HMI_UPDATE_SELF_SERVICE", "hmi-update")
//        verifyWindow := getenv int seconds("HMI_UPDATE_VERIFY_WINDOW_S", 15)
//        healthcheckWindow := getenv int seconds("HMI_UPDATE_HEALTHCHECK_WINDOW_S", 60)
//  5.9.  orchestrator, err := actions.NewOrchestrator(
//          dockerClient, runner, resolver, composeReader, store, updates,
//          selfService, verifyWindow, healthcheckWindow)
//  6.    api.NewServer(store, dockerClient, composeReader, orchestrator)
```

**Env-var parsing pattern** (`main.go` lines 124–136 + `internal/poll/poller.go::envInt` lines 513–521):

```go
// poll/poller.go has the canonical envInt helper:
func envInt(name string, def int) int {
    if v := os.Getenv(name); v != "" {
        if n, err := strconv.Atoi(v); err == nil && n > 0 {
            return n
        }
    }
    return def
}
```

**Apply:** `verifyWindow := time.Duration(envInt("HMI_UPDATE_VERIFY_WINDOW_S", 15)) * time.Second` — reuse the helper (possibly promote to `main.go`-local or copy-paste; both directories already have local copies of similar helpers per `discovery_test.go::newTestStore` convention).

**Fail-fast wrapping** (`main.go` lines 149–151 — `log.Fatalf("state.NewStore: %v", err)`):

```go
log.Fatalf("compose.NewRunner: %v", err)
log.Fatalf("actions.NewOrchestrator: %v", err)
```

Match the `<package>.<Constructor>` prefix discipline.

---

### `e2e/tests/update-flow.spec.ts` (e2e test, action + state poll)

**Primary analog:** `/Users/jonb/Projects/tmp/e2e/tests/detect-multiarch.spec.ts` (entire 126-line file — `waitForCondition` helper + `pushFreshManifest` + state assertion)

**`waitForCondition` polling helper** (`detect-multiarch.spec.ts` lines 51–71):

```typescript
async function waitForCondition<T>(
  request: import('@playwright/test').APIRequestContext,
  predicate: (state: StateBody) => T | undefined,
  timeoutMs: number,
  label: string,
): Promise<T> { /* poll /api/state every 500ms */ }
```

**Apply verbatim:** reuse the helper (copy-paste or promote to `e2e/fixtures/state.ts` — the planner picks; copy-paste matches the in-repo convention).

**Action sequence:**

1. `pushFreshManifest('centroid-is/stub')` → triggers cron flip.
2. `waitForCondition(state => state.containers['stub-watched-container'].update_available === true, 10_000)` → confirms upstream digest visible.
3. `const resp = await request.post('/api/containers/stub-watched-container/update')` — the new step.
4. `expect(resp.ok()).toBe(true); const body = await resp.json();`
5. `expect(body.current_digest).toMatch(/^sha256:/); expect(body.previous_digest).toMatch(/^sha256:/);` (ACT-11).
6. `waitForCondition(state => state.containers['stub-watched-container'].update_available === false && state.containers['stub-watched-container'].current_digest === body.current_digest, 5_000)` — confirms state-write completed.

**Test header doc style** (`detect-multiarch.spec.ts` lines 1–26):

```typescript
// ACT-01 / ACT-02 / ACT-11 — Update endpoint happy path:
//   1. push fresh manifest, wait for flip (≤10s at @every 5s cron)
//   2. POST /api/containers/stub-watched-container/update
//   3. assert response.ok && response.current_digest && response.previous_digest
//   4. assert state.update_available flips back to false
//
// Tolerances (assumes `make e2e-cron-fast`):
//   - cron flip SLA: 10s
//   - update completion SLA: 30s (per ACT-02: pull + recreate + 15s verify)
//   - response shape: { current_digest: "sha256:...", previous_digest: "sha256:..." }
```

---

### `e2e/tests/rollback-flow.spec.ts` (e2e test, action + offline)

**Primary analog:** `e2e/tests/detect-multiarch.spec.ts` + new `e2e/fixtures/disconnect-network.ts`

**Two tests in one file:**

1. **Online rollback (ACT-03):** Update → assert state — Rollback → assert digests swap.
2. **Offline rollback (ACT-04):** Update → `disconnectZotFromNetwork()` (from new fixture) → Rollback → assert success (ImageTag is local) → `reconnectZot()` in `finally`.

**Try/finally for fixture cleanup** (RESEARCH.md lines 1378–1394):

```typescript
disconnectZotFromNetwork();
try {
  const resp = await request.post('/api/containers/stub-watched-container/rollback');
  // ... assertions ...
} finally {
  reconnectZot();
}
```

This pattern matches the `compose-drift.spec.ts::afterAll` discipline (lines 43–53) — always-restore-state.

---

### `e2e/tests/idempotency.spec.ts` (e2e test)

**Primary analog:** `e2e/tests/detect-multiarch.spec.ts`

**Two tests:**

1. **ACT-06:** Update when `CurrentDigest === AvailableDigest` → assert `body.no_op === true`.
2. **ACT-07:** Rollback when `CurrentDigest === PreviousDigest` (after a previous rollback) → assert `body.no_op === true`.

The new assertion shape — `expect(body.no_op).toBe(true)` — is novel for the codebase but follows the existing structured-JSON-response convention.

---

### `e2e/tests/concurrent-actions.spec.ts` (e2e test, parallel HTTP)

**Primary analog:** `e2e/tests/detect-multiarch.spec.ts` (request shape only — the parallel pattern is novel)

**New pattern: `Promise.all` + mixed-status assertion:**

```typescript
const [r1, r2] = await Promise.all([
  request.post('/api/containers/stub-watched-container/update'),
  request.post('/api/containers/stub-watched-container/update'),
]);
const statuses = [r1.status(), r2.status()].sort();
expect(statuses).toEqual([200, 409]);  // exactly one succeeds, one is service_busy
```

**Cross-service parallelism test:** Update svc-a + Update svc-b concurrently → both 200. The compose stack must define both `stub-watched-container` and (e.g.) `stub2-watched-container`; check if `compose.test.yml` already has two watched stubs (CONTEXT.md mentions `timescaledb-stub` exists for SAFE-01).

---

### `e2e/tests/self-protection.spec.ts` (e2e test, negative-path)

**Primary analog:** `/Users/jonb/Projects/tmp/e2e/tests/healthz-negative.spec.ts` (lines 1–80 — negative-path 503 assertion pattern transfers cleanly)

**`waitForHealth(url, expectStatus, timeoutMs)` pattern** (`healthz-negative.spec.ts` lines 43–76):

Adapt to action endpoints — the helper polls `request.post(...)` until either expected-status or deadline. For self-protection: assert immediate 409 on first call (no polling needed — middleware rejects synchronously).

```typescript
test('self-protection: ACT-09 POST /api/containers/hmi-update/update returns 409', async ({ request }) => {
  const resp = await request.post('/api/containers/hmi-update/update');
  expect(resp.status()).toBe(409);
  const body = await resp.json();
  expect(body.error).toBe('self_protection');
  expect(body.detail).toContain('PROJECT.md');  // verbatim hint string
});
```

---

### `e2e/tests/safety-labels.spec.ts` (e2e test, label-driven)

**Primary analog:** `/Users/jonb/Projects/tmp/e2e/tests/detect-tag-pattern.spec.ts` (label-driven assertion pattern — container with specific label → expected /api/state behavior)

**Test cases:**

1. **SAFE-01:** Update on `timescaledb-stub` (which has `hmi-update.allow-update=false`) → 409 `action_disabled_by_label`.
2. **SAFE-02:** Rollback on container with `hmi-update.allow-rollback=false` → 409.
3. **SAFE-03:** Same `timescaledb-stub` — assert `last_polled_at` continues to advance after multiple cron ticks (proves the poll loop still ticks for safety-locked containers).

**Compose-file requirement:** RESEARCH.md "Runtime State Inventory" lines 1147–1148 — verify `timescaledb-stub` has `hmi-update.allow-update: "false"` in `e2e/compose.test.yml`; add if absent.

**SAFE-03 cron-advance assertion shape** (re-use `last_poll_end` advancing pattern from `e2e/tests/obs-04-redaction.spec.ts` lines ~50–60 referenced in PATTERNS-Phase-3):

```typescript
const before = (await request.get('/api/state').then(r => r.json())).containers['timescaledb-stub'].last_polled_at;
// trigger 409 update attempt (proves middleware path)
await request.post('/api/containers/timescaledb-stub/update');
// wait for at least one cron sweep
await sleep(7000);
const after = (await request.get('/api/state').then(r => r.json())).containers['timescaledb-stub'].last_polled_at;
expect(after).not.toBe(before);  // SAFE-03: poll still ticks
```

---

### `e2e/tests/restart-persistence.spec.ts` (e2e test, compose-restart)

**Primary analog:** `/Users/jonb/Projects/tmp/e2e/tests/compose-drift.spec.ts::afterAll` (lines 43–53 — `execSync` + `docker compose restart hmi-update` + `waitForHealth` poll loop)

**Pattern transfer** (RESEARCH.md lines 1401–1447 has the canonical body — copy verbatim):

```typescript
execSync('docker compose -f compose.test.yml restart hmi-update', { stdio: 'inherit' });
// Poll /healthz until 200 (mirrors compose-drift.spec.ts:afterAll)
const deadline = Date.now() + 30_000;
while (Date.now() < deadline) {
  try { const h = await request.get('/healthz'); if (h.ok()) break; } catch {}
  await sleep(500);
}
// Verify state is preserved
const stateResp = await request.get('/api/state');
expect(state.containers['stub-watched-container'].current_digest).toBe(currentDigest);
expect(state.containers['stub-watched-container'].previous_digest).toBe(previousDigest);
```

---

### `e2e/tests/verify-failed.spec.ts` (e2e test, action-then-assert)

**Primary analog:** `e2e/tests/detect-multiarch.spec.ts` (action shape transfers)
**Compose requirement:** new `crash-loop-stub` service per RESEARCH.md Open Question #2 recommendation (lines 1582–1585):

```yaml
crash-loop-stub:
  image: busybox
  command: ["sh", "-c", "exit 1"]
  restart: unless-stopped
  labels:
    hmi-update.watch: "true"
```

**Assertion shape:**

```typescript
const resp = await request.post('/api/containers/crash-loop-stub/update');
expect(resp.status()).toBe(500);
const body = await resp.json();
expect(body.error).toBe('verify_failed');
expect(body.reason).toMatch(/restarted/);  // RESEARCH.md lines 776-784 — verify-failed response shape
expect(body.restart_count).toBeGreaterThan(0);
```

**Timeout extension:** `test.setTimeout(60_000)` because verify takes the full 15s before failing (RESEARCH.md A7 — explicit timeout bump for verify-failed spec).

---

### `e2e/fixtures/disconnect-network.ts` (fixture)

**No in-repo analog.** Closest: `/Users/jonb/Projects/tmp/e2e/fixtures/push-image.ts` (the `execSync`-against-CLI shape).

**Reference:** RESEARCH.md lines 1319–1372 has the canonical 50-line body. Three exports:
- `getComposeNetwork(): string` — discovers `e2e_default` via `docker network ls`.
- `disconnectZotFromNetwork(): void` — calls `docker network disconnect <net> zot`.
- `reconnectZot(): void` — calls `docker network connect <net> zot`.

**Apply the `execSync` discipline from `push-image.ts`** (lines 41–47 — `execSync(cmd, { encoding: 'utf8' })`):

```typescript
execSync(`docker network disconnect ${net} zot`, { stdio: 'inherit' });
```

---

## Shared Patterns

### Pattern A: Long-form file-header doc-comment with anti-deadlock anchor

**Source:** `internal/docker/discovery.go` lines 1–34 + `internal/poll/poller.go` lines 1–43 + `internal/poll/channel.go` lines 1–31

**Apply to:** every new `*.go` file in `internal/actions/` and `internal/compose/runner.go`. Required sections:

1. First line: `// Package <name> <one-sentence purpose>.` (or `// Package <name> (continued).` for additional files in the same package).
2. Architectural anchor block referencing `.planning/research/ARCHITECTURE.md` Pattern 3 ("single-consumer channel") OR the relevant CONTEXT.md section.
3. Anti-deadlock invariant block: "actions NEVER holds state.Store.mu while calling registry/docker/compose I/O."
4. Phase-and-plan attribution: "Phase 4 plan 04-XX-PLAN.md lands the body."

### Pattern B: `errors.Is`-friendly sentinel package vars

**Source:** `internal/compose/errors.go` (one sentinel), `internal/registry/errors.go` (two sentinels)

**Apply to:** `internal/actions/errors.go` (seven sentinels). Each gets a long godoc listing (a) HTTP status code the API layer maps to, (b) the wrap pattern (`fmt.Errorf("actions: ... %w", ErrFoo)`), (c) an `errors.Is`-branch example in prose.

### Pattern C: Constructor returns interface, not concrete pointer (WR-04)

**Source:** `internal/docker/moby.go::NewClient` (line 154 — returns `Client` interface), `internal/poll/poller.go::NewPoller` (line 132 — returns `Poller` interface), `internal/registry/transport.go::NewRedactingTransport` (line 71 — returns `http.RoundTripper`)

**Apply to:** `compose.NewRunner(...) (Runner, error)`, `actions.NewOrchestrator(...) (Orchestrator, error)`. Concrete structs (`execRunner`, `actionOrchestrator`) remain unexported.

### Pattern D: Single-consumer channel for state mutations (DETECT-10 carry-forward)

**Source:** `internal/poll/channel.go` (the canonical formal statement) + `internal/poll/poller.go::send` (the ctx-aware producer wrapper at lines 504–509)

**Apply to:** every state-mutation site in Phase 4. Action handlers send `poll.StateUpdate{Kind: poll.KindActionStart|KindActionProgress|KindActionResult, Service, Apply}` on the same channel; the existing `poll.RunUpdater` consumer applies. The orchestrator MUST NOT call `state.Store.Update` directly — that would break the DETECT-10 invariant.

### Pattern E: Tygo source-of-truth — `internal/api/types.go` mirrors `internal/state/schema.go` verbatim

**Source:** `internal/state/schema.go` lines 19–24 + `internal/api/types.go` lines 1–14

**Apply to:** every Phase 4 schema field. Add to `schema.go`, mirror into `types.go` in the same commit, run `make check-types`. Add `internal/state/schema_phase4_test.go` + `internal/api/types_phase4_test.go` mirroring the Phase 3 forward-compat / parity tests verbatim.

### Pattern F: RED-FIRST test header listing what each test guards

**Source:** every `*_test.go` and `*.spec.ts` in the repo (`internal/state/store_test.go` lines 1–16, `internal/poll/poller_test.go` lines 1–40, `e2e/tests/detect-multiarch.spec.ts` lines 1–26)

**Apply to:** every new Phase 4 test file. Bullet list of `TestSymbol: what it guards` is load-bearing for reviewer onboarding.

### Pattern G: slog event names follow `package.event.sub-event` dotted notation

**Source:** `internal/docker/discovery.go` (`"discovery.boot.start"`, `"discovery.event.received"`), `internal/poll/poller.go` (`"poll.boot.start"`, `"registry.fetch"`, `"poll.sweep.end"`), `internal/compose/reader.go` (`"compose.reader.boot"`)

**Apply to:** Phase 4 events locked in RESEARCH.md lines 1461–1468:
- `action.start` (orchestrator entry)
- `action.phase` (intermediate: pulled / recreated / verified)
- `action.complete` (success exit)
- `action.verify_failed` (verify-after-recreate failure)
- `action.compose_failed` (runner exit non-zero)
- `action.pull_failed` (pull error or digest mismatch)
- `compose.run` (the runner's per-invocation event — `internal/compose/runner.go`)

### Pattern H: `t.TempDir()` + `state.NewStore(filepath.Join(dir, "state.json"))` test scaffolding

**Source:** `internal/state/store_test.go` lines 28–30, `internal/docker/discovery_test.go::newTestStore`, `internal/poll/channel_test.go::newTestStore` (lines 53–64)

**Apply to:** every Phase 4 test that needs a real `state.Store` (`orchestrator_test.go`, `store_sigkill_test.go`'s parent test, `handlers_actions_test.go`). The convention is package-local copy-paste of `newTestStore`; if a 4th caller appears the helper can be promoted to `state.NewTestStore(t)`.

### Pattern I: Goroutine assertions use `t.Errorf`, never `t.Fatal`

**Source:** `internal/state/persist_test.go` lines 29–31 (origin of the convention), `internal/docker/discovery_test.go` line 33 (reference), `internal/poll/poller_test.go` lines 37–39 (cited), `internal/poll/patterns_test.go` lines 27–30

**Apply:** every Phase 4 test that spawns goroutines (`mutex_test.go::TestLockService_Concurrent`, `orchestrator_test.go` verify-loop tests, `store_sigkill_test.go::TestSIGKILLDuringWrite`). Document the convention at the top of each affected file (matching the verbatim shape in `patterns_test.go`).

### Pattern J: Canonical short string literals live at exactly ONE assignment site (WR-10 carry-forward)

**Source:** `internal/state/notes.go` (lines 44–50 — five canonical Note literals shared across `internal/poll` and `internal/docker`)

**Apply to:** if the planner needs new shared literals across `internal/actions` + `internal/poll` + `internal/api` (e.g., the three `ActionInFlight` values: `"updating"`, `"rolling_back"`, `"force_pulling"`), promote them to `internal/state/notes.go` (or a new `internal/state/actions.go` sibling) so the source-grep audit invariant holds. CONTEXT.md Area 1 "Claude's Discretion" leans plain `string` (matches Notes precedent); WR-10 says the literal must live at exactly one site.

### Pattern K: Verbatim-constant HTTP response bodies (T-01-04-03 path-leak guard)

**Source:** `internal/api/handlers.go` lines 31–58 (`healthzBody*` block + the doc-comment discipline at lines 35–42 — "Do NOT interpolate variables into these constants")

**Apply to:** `internal/actions/middleware.go` and `internal/api/handlers_actions.go`. Every HTTP response body is a verbatim const. The path-leak guard tests (`handlers_healthz_test.go` lines 145–155 — "Body does NOT contain test-host TempDir prefixes") transfer directly to the action-endpoint tests.

---

## No Analog Found

| File | Role | Data Flow | Reason | Reference |
|------|------|-----------|--------|-----------|
| `e2e/fixtures/disconnect-network.ts` | fixture (docker network commands) | n/a | First in-repo use of `docker network disconnect`. Closest is `e2e/fixtures/push-image.ts` (execSync-against-CLI shape transfers). | RESEARCH.md §"Disconnect-Network Fixture for Offline Rollback (ACT-04)" lines 1319–1372 (verbatim 50-LOC template) |
| `cmd/sigkillhelper/main.go` | helper binary (state-write loop until SIGKILLed) | n/a | First `cmd/` binary other than `hmi-update`. | RESEARCH.md §"Pattern 6 SIGKILL Fault-Injection Test Harness" lines 884–937 (verbatim 50-LOC template) |
| `internal/state/store_sigkill_test.go` | test (cross-process fault injection via subprocess) | n/a | First build-tagged test in the repo; first fork-exec test. Closest cousin is `persist_test.go::TestPersistAtomicity` (single-process atomicity). | RESEARCH.md §"Pattern 6 ... parent test" lines 802–881 (verbatim canonical body) + RESEARCH.md "Makefile target" lines 942–944 |

All three "no-analog" files have detailed canonical bodies in RESEARCH.md; the planner can paste-and-adjust without inventing structure.

---

## Metadata

**Analog search scope:**
- `/Users/jonb/Projects/tmp/internal/` (all 6 sub-packages: api, actions, compose, docker, poll, registry, state)
- `/Users/jonb/Projects/tmp/cmd/hmi-update/`
- `/Users/jonb/Projects/tmp/e2e/tests/`
- `/Users/jonb/Projects/tmp/e2e/fixtures/`

**Files scanned:** 34 Go files + 8 Playwright spec files + 2 fixture files + 2 compose files = 46 source files.

**Strongest cross-cutting analogs:**

1. **`internal/poll/poller.go`** (lines 1–536) — template for the `internal/actions/orchestrator.go` body: long-running coordinator, ctx-aware lifecycle, channel-send to single consumer, scripted-fake test pattern. The Phase 3 poll loop is the spitting image of what the Phase 4 action orchestrator needs to be, modulo the trigger (cron tick vs HTTP request).

2. **`internal/api/handlers.go`** (lines 31–228) — template for the entire HTTP surface of Phase 4: verbatim-constant response bodies + defensive-nil-guard layering + dotted slog events + path-leak T-01-04-03 discipline. The `healthz` handler's 5-step detection ladder maps 1-to-1 onto the action-endpoint middleware chain.

3. **`internal/poll/patterns.go`** (the full 106-line file) — template for `internal/actions/mutex.go`'s RWMutex-around-map structure. The "permissive default" semantics of `Patterns.Match` parallel the "lazy creation under double-check" semantics of `lockService`.

4. **`internal/compose/errors.go` + `internal/registry/errors.go`** — combined template for `internal/actions/errors.go`'s 7 sentinels with rich godocs.

**Pattern extraction date:** 2026-05-15

## PATTERN MAPPING COMPLETE
