# Phase 2: Docker Client & Compose-File Reader - Pattern Map

**Mapped:** 2026-05-13
**Files analyzed:** 14 (11 Go + 3 e2e)
**Analogs found:** 14 / 14 (all files have Phase 1 analogs in-repo)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `internal/docker/client.go` (modify — expand interface) | port (interface) | abstraction | `internal/state/schema.go` (Phase 1 contract file) + existing stub | exact (modify in place) |
| `internal/docker/moby.go` (new) | adapter (concrete impl) | request-response | `internal/state/store.go` (constructor + facade methods) | role-match (constructor + facade pattern) |
| `internal/docker/discovery.go` (new) | service goroutine | event-driven streaming | `internal/state/persist.go` (single-consumer-of-state writer; Pattern 3 from ARCHITECTURE.md) | partial — first true event consumer in the codebase |
| `internal/docker/moby_test.go` (new) | test | table-driven unit | `internal/state/persist_test.go` (in-process concurrent test) + `internal/api/server_test.go` (httptest pattern) | role-match |
| `internal/docker/discovery_test.go` (new) | test | event-stream unit | `internal/state/persist_test.go` (concurrent producer/consumer pattern) | partial |
| `internal/compose/reader.go` (new) | service (stat-based snapshot) | file-I/O snapshot | `internal/state/store.go` (boot-read + in-mem snapshot via mutex) | role-match |
| `internal/compose/errors.go` (new) | sentinel errors | n/a | (no in-repo analog — first sentinel-error file; closest is the error-mention contract in `internal/state/store.go` lines 67-71) | new pattern; follows STACK convention |
| `internal/compose/reader_test.go` (new) | test | file-I/O unit | `internal/state/store_test.go` (TempDir + path-based round-trip) | exact |
| `internal/api/handlers.go` (modify — upgrade `healthz`) | controller | request-response | existing `healthz` body in same file (lines 22-39) | exact (in-place modify) |
| `internal/api/server.go` (modify — extend `NewServer` signature) | wiring | constructor injection | existing `NewServer` (lines 22-27) + `Server` struct (lines 13-18) | exact (in-place modify) |
| `internal/api/handlers_healthz_test.go` (new) | test | table-driven HTTP unit | `internal/api/server_test.go` (httptest.NewRequest + ServeHTTP, see `TestHealthz` lines 51-78 and `TestHealthzNilStore` lines 80-95) | exact |
| `cmd/hmi-update/main.go` (modify — thread new deps) | wire-up | sequential boot | existing `main()` (lines 24-55) | exact (in-place modify) |
| `e2e/compose.test.override.eacces.yml` (new) | test infra override | compose override | `e2e/compose.test.yml` (base — esp. `hmi-update:` service block, lines 42-71) | role-match |
| `e2e/compose.test.override.no-socket.yml` (new) | test infra override | compose override | `e2e/compose.test.yml` (base — same service block, omitting `/var/run/docker.sock:` line 49) | role-match |
| `e2e/tests/discovery.spec.ts` (new) | e2e test | poll-with-deadline | `e2e/tests/smoke.spec.ts` (api request + state-shape assertions, lines 41-86) | exact |
| `e2e/tests/healthz-negative.spec.ts` (new) | e2e test | request-response (503 path) | `e2e/tests/smoke.spec.ts` (`/healthz` assertions, lines 41-42) | exact |
| `e2e/tests/compose-drift.spec.ts` (new) | e2e test | file-mutation + request-response | `e2e/tests/smoke.spec.ts` (request + body assertions, generally) | role-match |

---

## Pattern Assignments

### `internal/docker/client.go` (port interface — expand existing stub)

**Analog:** `internal/state/schema.go` (Phase 1 contract file with doc-heavy comments anchoring the on-disk and wire shape).

**File-level doc-comment pattern** (`internal/docker/client.go` already established this; lines 1-12 of the existing stub):
```go
// Package docker wraps the moby/moby Docker daemon client used to enumerate
// watched containers and pull fresh images.
//
// Phase 1 ships the interface only; the body lands in phase 2 (DOCK-01..04).
package docker

// Client is the abstraction over moby/moby that plan-04's internal/api
// server depends on. Phase 2 implements it against
// `github.com/moby/moby/client`.
//
// TODO(phase-2): implement — see .planning/phases/02-*/*.md (DOCK-01..04).
type Client interface{}
```

**Expanding the interface** — when Phase 2 fills in methods, follow the pattern from `internal/state/schema.go` lines 14-30: each field/method gets a doc comment that explains the contract, references the brief / pitfalls / phase, and stays narrow (consumer-defined per ARCHITECTURE.md Pattern 5).

**Method set to add** (per CONTEXT.md `### Claude's Discretion`):
- `Ping(ctx) error` — for healthz; required this phase
- `ContainerList(ctx, opts) (..., error)` — boot discovery
- `ContainerInspect(ctx, id) (..., error)` — refresh on event
- `Events(ctx, opts) (<-chan events.Message, <-chan error)` — subscribe
- `ImagePull(ctx, ref, opts) (io.ReadCloser, error)` — stub for Phase 4
- `ImageTag(ctx, src, dst) error` — stub for Phase 4

Stubs for Phase 4 methods land here too to avoid Phase 4 interface churn (per CONTEXT.md).

---

### `internal/docker/moby.go` (concrete `mobyClient` — new file)

**Analog:** `internal/state/store.go` — constructor pattern + facade-over-stdlib pattern.

**Constructor pattern** (`internal/state/store.go` lines 29-93 — note the early-return + os-error-classification style):
```go
func NewStore(path string) (*Store, error) {
	s := &Store{
		path: path,
		state: State{
			Version:    SchemaVersion,
			Containers: map[string]Container{},
		},
	}

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		// ... happy path
	case errors.Is(err, fs.ErrNotExist):
		// ... cold-boot path
	default:
		return nil, fmt.Errorf("read state at %s: %w", path, err)
	}
}
```

**Apply to `docker.NewClient(ctx)`:** Use the same shape — return `(*mobyClient, error)`, fail-fast wrapped errors (`fmt.Errorf("docker.NewClient: %w", err)`), classify failures (negotiation fail vs socket missing vs EACCES) so callers can branch.

**Imports pattern** — keep imports tight and grouped per Go convention (see `internal/state/store.go` lines 3-10 — stdlib first, blank line, then third-party). Expected imports for `moby.go`:
```go
import (
	"context"
	"io"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/filters"
	"github.com/moby/moby/client"
)
```

**Construction with API negotiation** (per CONTEXT.md `### Lifecycle & Wiring`):
```go
func NewClient(ctx context.Context) (*mobyClient, error) {
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker.NewClient: %w", err)
	}
	return &mobyClient{c: c}, nil
}
```

**Concurrency note** — `internal/state/store.go` lines 21-27 documents that the type is safe for concurrent use; do the same on `mobyClient` referencing the moby SDK contract (per CONTEXT.md `### Concurrency Invariants`).

---

### `internal/docker/discovery.go` (event loop goroutine — new file)

**Analog:** `internal/state/persist.go` (single-writer convention) + ARCHITECTURE.md Pattern 3 (single-consumer channel for state mutations).

**Comment pattern — load-bearing rationale inline** (from `internal/state/persist.go` lines 24-35, the "do not simplify this" callout):
```go
// This wrapper is research correction A5 (Option 2 in
// .planning/phases/01-walking-skeleton-test-harness/01-RESEARCH.md, lines
// 478-513) — see also research/PITFALLS.md Pitfall 7 and renameio
// issue #11. The rationale is documented inline so a future reviewer
// does not "simplify" by stripping the dir-fsync as redundant — it is
// load-bearing for HMI durability across operator-triggered power
// cycles, and Phase 4's SIGKILL fault-injection test depends on it.
```

**Apply to discovery.go** — equivalent callouts for: (a) why `ContainerInspect` is called per event payload rather than trusted (CONTEXT.md `### Claude's Discretion`); (b) why `client.Events` reconnect uses exponential backoff (CONTEXT.md `<specifics>`); (c) why `state.Store.Update` is the single mutation point (CONTEXT.md `### Concurrency Invariants`).

**Event-to-state mapping** (from CONTEXT.md `### Docker Client — Discovery & Events`):
```go
switch ev.Action {
case "start":
	// inspect → upsert
case "die":
	// keep row, set Stopped=true
case "destroy":
	// delete row
}
```

Each mutation goes through `store.Update(func(*state.State) { ... })` — see the seed pattern at `internal/api/server_test.go` lines 38-45 for the in-test variant; production callers follow the same surface.

**Reconnect backoff** (per CONTEXT.md `<specifics>` — 1s, 2s, 4s … up to 30s, log every attempt). No in-repo analog; this is a new pattern. Use `time.NewTimer` with a `min(backoff*2, 30*time.Second)` ramp and `slog.Warn("docker.events.reconnect", "attempt", n, "backoff_ms", ...)`.

**Slog event naming** (CONTEXT.md `### Claude's Discretion` allows freedom; follow the existing project convention from `internal/state/store.go` doc comments referencing `discovery.boot.start`, `discovery.event.received` etc.). Apply structured logging per ARCHITECTURE.md §"Background Flow: Docker event".

---

### `internal/docker/moby_test.go` and `internal/docker/discovery_test.go` (new)

**Analog:** `internal/state/persist_test.go` (concurrent producer/consumer with `t.Errorf` not `t.Fatal` from goroutines).

**Concurrent test pattern** (lines 31-99 — `TestPersistAtomicity`):
```go
var wg sync.WaitGroup
stop := make(chan struct{})
wg.Add(2)

go func() {
	defer wg.Done()
	defer close(stop)
	for i := 0; i < 1000; i++ {
		if err := s.Update(...); err != nil {
			t.Errorf("Update iter %d: %v", i, err)
			return
		}
	}
}()

go func() {
	defer wg.Done()
	for {
		select {
		case <-stop:
			return
		default:
			// consume / verify
		}
	}
}()
wg.Wait()
```

**Goroutine assertion contract** (from `internal/state/persist_test.go` lines 29-31):
> The reader goroutine uses t.Errorf (not t.Fatal). t.Fatal inside a goroutine does not propagate to the test runner — it only halts the goroutine that calls it, leaving the test to pass falsely.

Apply to `discovery_test.go` event-stream tests — use `t.Errorf` for any assertion fired off-goroutine.

**httptest-based docker socket mock** — no exact analog (Phase 1 had no daemon dependency). Use `httptest.NewServer(http.HandlerFunc(...))` and pass `DOCKER_HOST=tcp://...` into `client.FromEnv` in tests. The closest pattern is the `httptest.NewRequest`/`httptest.NewRecorder` pair in `internal/api/server_test.go` lines 53-78.

**Table-driven shape** — follow `internal/state/store_test.go`:
```go
// Each test function exercises one scenario; if scenarios share scaffolding,
// promote a helper (e.g. newTestServer at server_test.go:19-27). No table-of-cases
// macro in Phase 1, but the comment-anchored per-test docstring is the convention.
```

---

### `internal/compose/reader.go` (new — `Reader` struct + `CheckUnchanged()`)

**Analog:** `internal/state/store.go` (boot-snapshot + concurrency invariant + stat-based contract).

**Boot snapshot captured in constructor** (modelled on `internal/state/store.go` lines 45-93):
```go
type Reader struct {
	path   string
	mu     sync.RWMutex // safe for concurrent CheckUnchanged from any goroutine
	bootInode   uint64
	bootModTime time.Time
	bootSize    int64
}

func NewReader(path string) (*Reader, error) {
	if path == "" {
		return nil, fmt.Errorf("compose.NewReader: empty HMI_UPDATE_COMPOSE_PATH")
	}
	r := &Reader{path: path}
	if err := r.captureBootSnapshot(); err != nil {
		return nil, fmt.Errorf("compose.NewReader: %w", err)
	}
	return r, nil
}
```

**Doc-comment style for the contract** (mirrors `internal/state/store.go` lines 12-27):
> Reader is safe for concurrent use; CheckUnchanged is read-only per CONTEXT.md `### Concurrency Invariants` and may be called from any goroutine.

**Inode-vs-(mtime,size) fallback** (CONTEXT.md `### Compose-File Reader` + `<specifics>`):
- Linux: `stat.Sys().(*syscall.Stat_t).Ino` gives the inode.
- Fallback on filesystems without stable inodes: compare `(ModTime(), Size())`.
- Log fallback decision in slog event payload (per `<specifics>`).

No exact analog for the stat-comparison pattern; this is a new family of code. Use the documented Pitfall 10 prescription from `PITFALLS.md` lines 269-273.

---

### `internal/compose/errors.go` (new — `ErrComposeFileMoved` sentinel)

**Analog:** None in-repo (Phase 1 used `fmt.Errorf` wrapping but no sentinel-error file). Establish a new convention.

**Pattern** (per CONTEXT.md `### Established Patterns` "Errors are sentinel values, not strings"):
```go
package compose

import "errors"

// ErrComposeFileMoved is returned from Reader.CheckUnchanged when the
// compose file's inode (or, on filesystems without stable inodes, its
// mtime+size pair) has drifted from the boot snapshot. Phase 4 maps this
// sentinel to HTTP 412 with body {"error":"compose_file_moved", ...}.
// See research/PITFALLS.md Pitfall 10.
var ErrComposeFileMoved = errors.New("compose: file moved or replaced since boot")
```

**Error-wrapping contract** (already exercised in `internal/state/store.go` lines 67-71):
> Test contract: the error message MUST contain "decode" or "parse" so operators see a clear signal in the boot log.

Equivalent for compose: the sentinel `ErrComposeFileMoved` is the test contract — callers use `errors.Is(err, compose.ErrComposeFileMoved)`. Wrap with `fmt.Errorf("compose: %w", ErrComposeFileMoved)` if extra context is helpful, but the sentinel identity must survive the wrap.

---

### `internal/compose/reader_test.go` (new)

**Analog:** `internal/state/store_test.go` (TempDir + path-based round-trip with all-three boot paths: existing-valid, missing, corrupted).

**TempDir + atomic-edit pattern** (`internal/state/store_test.go` lines 27-57):
```go
func TestLoadAndPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// ... create, mutate, re-open, assert
}
```

**Apply to compose Reader:**
```go
func TestCheckUnchangedDetectsRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	// Write a valid (or any) file, NewReader captures snapshot
	// os.Rename atop a fresh tmp file (atomic-save pattern)
	// CheckUnchanged() must return ErrComposeFileMoved (errors.Is)
}
```

**Error-message-contract test pattern** (`internal/state/store_test.go` lines 83-99 — `TestCorruptedFile`):
```go
_, err := NewStore(path)
if err == nil { t.Fatalf("...") }
msg := strings.ToLower(err.Error())
if !strings.Contains(msg, "parse") && !strings.Contains(msg, "decode") {
	t.Errorf("...")
}
```

For compose, replace the string-check with `errors.Is(err, compose.ErrComposeFileMoved)` since we have a sentinel.

---

### `internal/api/handlers.go` (modify — upgrade `healthz` per DOCK-03)

**Analog:** existing `healthz` body in the same file (lines 22-39).

**Existing healthz handler** (`internal/api/handlers.go` lines 23-39):
```go
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	if s.store == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unhealthy","reason":"state store unavailable; check HMI_UPDATE_STATE_PATH and restart"}`))
		return
	}

	_ = s.store.Get()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
```

**Apply the existing literal-JSON pattern** to the four new branches from CONTEXT.md `### Healthz Remediation Hints (DOCK-03)`. Each branch is a `w.WriteHeader(503) + w.Write([]byte("..."))` pair. **Bodies are verbatim from CONTEXT.md** — do not paraphrase:
- EACCES on socket stat or Ping: `{"status":"unhealthy","reason":"docker socket permission denied — set compose user: '65532:$(id -g docker)' (Pitfall 9)"}`
- Socket missing: `{"status":"unhealthy","reason":"docker socket missing — add bind-mount '/var/run/docker.sock:/var/run/docker.sock'"}`
- Daemon unreachable (other): `{"status":"unhealthy","reason":"docker daemon unreachable"}`
- State store unavailable (unchanged): `{"status":"unhealthy","reason":"state store unavailable; check HMI_UPDATE_STATE_PATH and restart"}`

**Detection flow** — also per CONTEXT.md (`### Healthz Remediation Hints` step 1-3): stat socket → branch on `errors.Is(err, fs.ErrNotExist)` vs `errors.Is(err, fs.ErrPermission)`; then `Ping(ctx500ms)` → branch on `errors.Is(err, syscall.EACCES)`. The 500ms timeout uses `context.WithTimeout(r.Context(), 500*time.Millisecond)`.

**Security invariant preserved** — `TestHealthz` (`internal/api/server_test.go` lines 73-77) already asserts no absolute paths leak. New branches must satisfy the same guard: the EACCES hint is parameter-free (no `/var/run/...` in the body would be a regression, but the bind-mount hint *does* include the canonical socket path — this is documented user advice, not an internal path leak).

---

### `internal/api/server.go` (modify — extend `NewServer` signature)

**Analog:** existing `NewServer` (`internal/api/server.go` lines 22-27) and `Server` struct (lines 13-18).

**Existing constructor and struct:**
```go
type Server struct {
	store *state.Store
	mux   *http.ServeMux
}

func NewServer(store *state.Store) *Server {
	s := &Server{store: store, mux: http.NewServeMux()}
	s.routes()
	return s
}
```

**Extension pattern** (per CONTEXT.md `### Integration Points`): add `dockerClient docker.Client` and `composeReader *compose.Reader` to the struct, append to constructor params. Keep the field order: state first, then docker, then compose, then the mux.

```go
type Server struct {
	store         *state.Store
	dockerClient  docker.Client
	composeReader *compose.Reader
	mux           *http.ServeMux
}

func NewServer(store *state.Store, dockerClient docker.Client, composeReader *compose.Reader) *Server {
	s := &Server{
		store:         store,
		dockerClient:  dockerClient,
		composeReader: composeReader,
		mux:           http.NewServeMux(),
	}
	s.routes()
	return s
}
```

**ListenAndServe and Handler unchanged** (lines 41-55) — read/write timeouts stay at 10s.

---

### `internal/api/handlers_healthz_test.go` (new — split from `server_test.go`)

**Analog:** `internal/api/server_test.go` (specifically `TestHealthz` lines 51-78 and `TestHealthzNilStore` lines 80-95).

**Test helper pattern** (`internal/api/server_test.go` lines 19-27 — `newTestServer`):
```go
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := state.NewStore(filepath.Join(dir, "hmi_update_state.json"))
	if err != nil {
		t.Fatalf("state.NewStore: %v", err)
	}
	return NewServer(store)
}
```

**Apply to Phase 2** — this helper's signature changes to also take a `dockerClient` and `composeReader` (or accept defaults). Create a sibling helper for Phase 2 healthz that injects a fake `docker.Client`:
```go
func newTestServerWithDocker(t *testing.T, dc docker.Client) *Server {
	t.Helper()
	dir := t.TempDir()
	store, _ := state.NewStore(filepath.Join(dir, "hmi_update_state.json"))
	reader, _ := compose.NewReader(filepath.Join(dir, "docker-compose.yml")) // pre-create a dummy file
	return NewServer(store, dc, reader)
}
```

**httptest assertion pattern** (`internal/api/server_test.go` lines 53-78):
```go
req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
rec := httptest.NewRecorder()
srv.Handler().ServeHTTP(rec, req)

if got := rec.Code; got != http.StatusOK { ... }
if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") { ... }

body := rec.Body.String()
var parsed map[string]any
if err := json.Unmarshal([]byte(body), &parsed); err != nil { ... }
if parsed["status"] != "ok" { ... }
```

**Table-driven shape for new healthz scenarios** (CONTEXT.md `### Testing Strategy` "table-driven over (stat result, ping result) tuples"):
```go
cases := []struct{
	name       string
	statErr    error    // injected via test-fake docker.Client
	pingErr    error
	wantStatus int
	wantBodySubstr string
}{
	{"healthy", nil, nil, 200, `"status":"ok"`},
	{"socket-missing", fs.ErrNotExist, nil, 503, "docker socket missing"},
	{"socket-eacces", fs.ErrPermission, nil, 503, "docker socket permission denied"},
	{"ping-eacces", nil, syscall.EACCES, 503, "permission denied"},
	{"ping-other", nil, errors.New("connection refused"), 503, "docker daemon unreachable"},
}
for _, tc := range cases {
	t.Run(tc.name, func(t *testing.T) { ... })
}
```

**Path-leak guard** (`internal/api/server_test.go` lines 73-77) — preserve verbatim:
```go
if strings.Contains(body, "/private/") || strings.Contains(body, "/var/folders/") || strings.Contains(body, "/tmp/") {
	t.Errorf("/healthz body leaks an absolute path: %q", body)
}
```
Note: bodies in CONTEXT.md *do* reference `/var/run/docker.sock` and `$(id -g docker)` — those are remediation hints, not leaks of `t.TempDir()` paths. The guard above only flags test-machine paths.

---

### `cmd/hmi-update/main.go` (modify — thread docker client + compose reader)

**Analog:** existing `main()` (`cmd/hmi-update/main.go` lines 24-55).

**Existing boot order** (lines 38-54):
```go
slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

statePath := os.Getenv("HMI_UPDATE_STATE_PATH")
if statePath == "" { statePath = "./hmi_update_state.json" }

store, err := state.NewStore(statePath)
if err != nil {
	log.Fatalf("state.NewStore: %v", err)
}

srv := api.NewServer(store)
slog.Info("hmi-update starting", "addr", ":8080", "state_path", statePath)
if err := srv.ListenAndServe(":8080"); err != nil {
	log.Fatalf("ListenAndServe: %v", err)
}
```

**Apply Phase 2 boot order** (CONTEXT.md `### Lifecycle & Wiring` "Boot order in `cmd/hmi-update/main.go`"):
1. slog handler (existing — keep verbatim)
2. `state.NewStore` (existing — keep verbatim)
3. `docker.NewClient(ctx)` — NEW; `log.Fatalf` on error
4. `compose.NewReader(os.Getenv("HMI_UPDATE_COMPOSE_PATH"))` — NEW; `log.Fatalf` on missing/unstattable
5. `discovery.Run(ctx, dockerClient, store)` — NEW; goroutine, non-blocking
6. `api.NewServer(store, dockerClient, composeReader).ListenAndServe(":8080")` — MODIFIED signature

**Fail-fast pattern** (existing `cmd/hmi-update/main.go` lines 45-48 uses `log.Fatalf`):
```go
store, err := state.NewStore(statePath)
if err != nil {
	log.Fatalf("state.NewStore: %v", err)
}
```

Apply identically to the new constructors. **Each fail-fast message includes the function name** for slog-grep-friendliness.

**Env var fallback pattern** (lines 40-43) — apply to `HMI_UPDATE_COMPOSE_PATH` (no default — CONTEXT.md says fail-fast if missing).

**Context creation** for the discovery goroutine — graceful shutdown is deferred to Phase 4 (CONTEXT.md `### Lifecycle & Wiring`), so in Phase 2 the context is `context.Background()` or a never-cancelled `context.WithCancel` — fine for now since `client.Events` returns when the ctx cancels.

---

### `e2e/compose.test.override.eacces.yml` (new — override forcing EACCES)

**Analog:** `e2e/compose.test.yml` — specifically the `hmi-update:` service block (lines 42-71).

**Existing `hmi-update` block** in `e2e/compose.test.yml`:
```yaml
hmi-update:
  build:
    context: ..
    dockerfile: Dockerfile
  ports:
    - "8080:8080"
  volumes:
    - /var/run/docker.sock:/var/run/docker.sock
    - ./compose.test.yml:/host/docker-compose.yml:ro
  tmpfs:
    - /state:uid=65532,gid=65532,mode=0755
  environment:
    - HMI_UPDATE_STATE_PATH=/state/hmi_update_state.json
    - HMI_UPDATE_LOG_LEVEL=info
  depends_on:
    zot:
      condition: service_started
    stub-watched-container:
      condition: service_started
```

**Override shape** for EACCES (set `user:` to UID 65532 but a GID that does NOT match the host's docker group — e.g. `65532:65532` — so the socket bind-mount is unreadable):
```yaml
services:
  hmi-update:
    user: "65532:65532"   # forces docker socket EACCES on Linux CI
```

**Override shape** for socket-missing (`e2e/compose.test.override.no-socket.yml`) — omit/replace the docker socket mount. Compose merges overrides by replacing the entire `volumes:` list for the service (a known compose gotcha), so the override must include `tmpfs:` + the compose path mount but omit the socket line.

**Compose merge semantics warning** — `e2e/compose.test.yml` lines 51-59 already includes a long-form comment about why `tmpfs:` is used (the UID/GID-65532 ownership trap). Apply the same comment-heavy convention to the override files: each override must explain *why* it's there and what test scenario it serves.

---

### `e2e/tests/discovery.spec.ts` (new — watched-container enumeration)

**Analog:** `e2e/tests/smoke.spec.ts` (specifically the `/api/state` shape assertions on lines 76-86).

**Existing /api/state shape check** (`e2e/tests/smoke.spec.ts` lines 76-86):
```typescript
const stateResp = await request.get('/api/state');
expect(stateResp.status(), '/api/state should return 200 OK').toBe(200);
const stateCT = (stateResp.headers()['content-type'] ?? '').toLowerCase();
expect(stateCT, '/api/state must be served as JSON').toContain('application/json');

const stateBody = await stateResp.json();
expect(stateBody, '/api/state body must have shape {version: 1, containers: object}').toMatchObject({
  version: 1,
  containers: expect.any(Object),
});
```

**Apply Phase 2 polling pattern** (CONTEXT.md `### Testing Strategy` "wait for `/api/state` to contain `stub-watched-container` within 60 s"):
```typescript
test('discovery: stub-watched-container visible in /api/state within 60s', async ({ request }) => {
	const deadline = Date.now() + 60_000;
	while (Date.now() < deadline) {
		const resp = await request.get('/api/state');
		if (resp.ok()) {
			const body = await resp.json();
			if (body.containers && body.containers['stub-watched-container']) {
				expect(body.containers['stub-watched-container']).toMatchObject({
					service: 'stub-watched-container',
					// labels include hmi-update.watch=true
				});
				return;
			}
		}
		await new Promise((r) => setTimeout(r, 1000));
	}
	throw new Error('stub-watched-container never appeared in /api/state within 60s');
});
```

**Polling-with-deadline pattern** is also exercised in `e2e/global-setup.ts` lines 8-22 (`waitForHealth`) — copy that exact loop structure for fast/slow consistency.

**Mid-test docker-exec second container** (CONTEXT.md `### Testing Strategy` mentions "Start a second labeled container via `docker exec` mid-test, expect it visible within 5 s"):
```typescript
import { execSync } from 'node:child_process';

execSync('docker compose -f compose.test.yml run -d --rm --label hmi-update.watch=true busybox sleep 60');
// then poll /api/state for the new service name with a 5s deadline
```

---

### `e2e/tests/healthz-negative.spec.ts` (new)

**Analog:** `e2e/tests/smoke.spec.ts` (specifically the healthz check on lines 41-42).

**Existing healthz assertion** (lines 41-42):
```typescript
const health = await request.get('/healthz');
expect(health.status(), '/healthz should return 200 OK').toBe(200);
```

**Negative-path shape** for Phase 2 — bring the stack up with the override compose file, then assert 503 + verbatim hint string. The orchestration of which compose file to use is per-test, not in `playwright.config.ts` (which is shared). Use a helper or `test.beforeAll` that brings up `docker compose -f e2e/compose.test.yml -f e2e/compose.test.override.eacces.yml up -d`. Then:
```typescript
const health = await request.get('/healthz');
expect(health.status()).toBe(503);
const body = await health.json();
expect(body.reason).toContain('docker socket permission denied');
expect(body.reason).toContain("65532:$(id -g docker)"); // verbatim Pitfall 9 hint
```

**Body string is verbatim from CONTEXT.md** — do not paraphrase. CONTEXT.md provides the full expected strings under `### Healthz Remediation Hints (DOCK-03)` "Response bodies (verbatim)".

---

### `e2e/tests/compose-drift.spec.ts` (new)

**Analog:** `e2e/tests/smoke.spec.ts` (request shape and status assertions, plus `e2e/fixtures/push-image.ts` for the atomic-write trick on lines 24-26).

**Atomic-rename pattern** (`e2e/fixtures/push-image.ts` lines 24-26 shows `writeFileSync` to /tmp then a separate operation):
```typescript
const file = `/tmp/payload-${Date.now()}-${Math.random().toString(36).slice(2)}.txt`;
writeFileSync(file, `payload-${Date.now()}`);
```

**Apply for compose-drift**:
```typescript
import { renameSync, writeFileSync } from 'node:fs';

// Atomic save pattern (vim/VSCode style): write tmp + rename atop the target.
const target = './compose.test.yml';
const tmp = `${target}.tmp-${Date.now()}`;
writeFileSync(tmp, /* new contents */);
renameSync(tmp, target); // this changes the inode → drift detected
```

**Debug endpoint pattern** (CONTEXT.md `### Testing Strategy` "calls a temporary debug endpoint `GET /debug/compose-stat`"):
The debug endpoint is registered behind `//go:build debug` (CONTEXT.md). The Playwright test:
```typescript
const drift = await request.get('/debug/compose-stat');
expect(drift.status()).toBe(412);
const body = await drift.json();
expect(body.error).toBe('compose_file_moved');
expect(body.hint).toBe('restart hmi-update to pick up the new docker-compose.yml');
```

The debug endpoint is removed in Phase 4 (per CONTEXT.md `### Testing Strategy` "Phase 4 removes the debug endpoint once `POST /api/containers/:svc/update` exercises the reader naturally").

---

## Shared Patterns

### Authentication / Authorization
**Not applicable.** Per CONTEXT.md (and the project brief), hmi-update is LAN-only / unauthenticated in v1. No auth middleware in Phase 2.

### Error Handling
**Source:** `internal/state/store.go` lines 67-71 (wrapped errors with operator-visible substrings) + `internal/state/persist.go` lines 42-44 (the renameio error path).
**Apply to:** All new Go files in `internal/docker/` and `internal/compose/`.

```go
// Wrap with fmt.Errorf and %w so errors.Is/As work upstream.
if err := someOp(); err != nil {
	return fmt.Errorf("docker.discovery.boot: %w", err)
}

// Sentinel errors for control-flow branches (compose drift, etc.)
var ErrComposeFileMoved = errors.New("compose: file moved or replaced since boot")
```

### Logging
**Source:** `cmd/hmi-update/main.go` lines 26-38 (slog handler config) + slog event-name doc comments referenced in `internal/state/store.go`.
**Apply to:** Every new long-lived goroutine and every action in `internal/docker/discovery.go`.

```go
slog.Info("discovery.boot.start", "label_filter", "hmi-update.watch=true")
slog.Info("discovery.boot.list", "count", len(containers))
slog.Info("discovery.event.received", "action", ev.Action, "service", svc, "container_id", short(id))
slog.Warn("discovery.events.reconnect", "attempt", n, "backoff_ms", backoffMs)
slog.Error("discovery.inspect.fail", "container_id", id, "err", err)
```

Use lowercase dotted event names per the existing project convention (the slog event names listed in CONTEXT.md `### Claude's Discretion`).

### State Mutation
**Source:** `internal/state/store.go` lines 125-133 (`Update(func(*State))`) — the single mutation point.
**Apply to:** `internal/docker/discovery.go` — every event handler calls `store.Update(func(*state.State) { ... })`.

```go
// On `start` event:
if err := store.Update(func(st *state.State) {
	c := st.Containers[svc]
	c.Service = svc
	c.Image = inspect.Config.Image // parse image name
	c.Tag = parseTag(inspect.Config.Image)
	// ... other fields per CONTEXT.md
	st.Containers[svc] = c
}); err != nil {
	slog.Error("discovery.event.start.persist", "service", svc, "err", err)
}
```

**Critical concurrency invariant** (`internal/state/store.go` doc comments lines 12-22 + ARCHITECTURE.md "Anti-deadlock rule"):
> Never hold `state.Store.mu` while calling registry/docker/compose. Take Lock → mutate map → release Lock → persist.

In Phase 2 this means: do **not** call `dockerClient.ContainerInspect` from inside `store.Update`'s function closure. Inspect first, then call `store.Update` with the resolved fields.

### Validation
**Not applicable in Phase 2.** No new user input surfaces (Phase 2 only adds healthz branches and read-only debug endpoints; no params accepted from URL).

### Tygo Source-of-Truth
**Source:** `internal/api/types.go` (entire file — 17 lines) + `internal/state/schema.go` lines 14-30.
**Apply to:** When CONTEXT.md adds fields to `internal/state.Container` (ContainerID, Labels, Pinned, Stopped), the corresponding `api.Container` in `internal/api/types.go` MUST be updated in the same commit.

The "tygo source-of-truth" convention is documented at the top of `internal/api/types.go` lines 1-15:
> The field tags here must mirror internal/state/Container so that what the store persists to ./hmi_update_state.json deserializes cleanly into the same wire shape served by GET /api/state.

**CI guard:** `make check-types` (Makefile lines 25-27) runs `tygo generate` and fails the build if `ui/src/lib/types.d.ts` drifts. The CI pipeline already wires this; Phase 2's struct changes will trigger regeneration.

### Test File Naming and Structure
**Source:** `internal/state/store_test.go` and `internal/state/persist_test.go` (paired tests for paired source files).
**Apply to:** Phase 2 — each new Go source file gets a paired `_test.go` next to it. Comments at the top of every test file (`internal/state/store_test.go` lines 1-16) document RED-FIRST authorship per C4.

---

## No Analog Found

| File | Role | Data Flow | Reason | Fallback |
|------|------|-----------|--------|----------|
| `internal/docker/discovery.go` event reconnect loop | resilience | event-stream with backoff | Phase 1 had no long-running goroutines or external streams | Use stdlib `time.NewTimer` + capped exponential per CONTEXT.md `<specifics>`; no library |
| `internal/compose/errors.go` sentinel | error type | n/a | Phase 1 used `fmt.Errorf` exclusively, no `var ErrX = errors.New(...)` exists yet | Establish the pattern fresh; follows CONTEXT.md `### Established Patterns` "Errors are sentinel values, not strings" |
| Inode comparison via `syscall.Stat_t` | OS-specific stat | file-I/O snapshot | No in-repo file uses `syscall.Stat_t` yet | Direct stdlib usage per PITFALLS.md Pitfall 10; document fallback to (mtime, size) in slog payload per CONTEXT.md `<specifics>` |
| Compose override files | test infra | yaml override | Phase 1 ships only a single base `compose.test.yml`; no override layering | Document compose-merge semantics inline; reference [Docker compose multi-file](https://docs.docker.com/compose/multiple-compose-files/merge/) in comments |

---

## Metadata

**Analog search scope:**
- `/Users/jonb/Projects/tmp/internal/` (all 7 packages; 5 stubs and 2 implementations)
- `/Users/jonb/Projects/tmp/cmd/hmi-update/main.go`
- `/Users/jonb/Projects/tmp/e2e/` (compose.test.yml, playwright.config.ts, global-setup.ts, global-teardown.ts, fixtures/push-image.ts, tests/smoke.spec.ts)
- `/Users/jonb/Projects/tmp/Makefile`, `tygo.yaml`, `go.mod`

**Files scanned:** 22 source files + 4 planning files (CONTEXT.md, STACK.md, ARCHITECTURE.md, PITFALLS.md)

**Pattern extraction date:** 2026-05-13

**Phase 1 packages with usable concrete code:**
- `internal/state/` (store.go, persist.go, schema.go) — fully implemented, the richest analog pool
- `internal/api/` (server.go, handlers.go, static.go, types.go) — fully implemented HTTP layer
- `cmd/hmi-update/main.go` — boot wiring example

**Phase 1 packages with stub-only interfaces (treat as contracts, not analogs):**
- `internal/docker/client.go` (12 lines)
- `internal/compose/runner.go` (13 lines)
- `internal/poll/poller.go` (15 lines)
- `internal/registry/resolver.go` (13 lines)
- `internal/actions/orchestrator.go` (17 lines)
