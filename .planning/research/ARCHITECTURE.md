# Architecture Research

**Domain:** Single-binary Go service with embedded SPA, Docker socket client, and OCI registry poller
**Researched:** 2026-05-13
**Confidence:** HIGH

## Standard Architecture

### System Overview

```
┌──────────────────────────────────────────────────────────────────────────┐
│                            hmi-update (single Go process)                │
├──────────────────────────────────────────────────────────────────────────┤
│                       HTTP layer (net/http + chi)                        │
│  ┌─────────────────┐  ┌─────────────────┐  ┌──────────────────────┐      │
│  │ Static handler  │  │  /api/* JSON    │  │  /healthz            │      │
│  │ (embed.FS, SPA) │  │   handlers      │  │  (state+sock probe)  │      │
│  └────────┬────────┘  └────────┬────────┘  └──────────┬───────────┘      │
│           │                    │                       │                 │
│           └────────────────────┼───────────────────────┘                 │
│                                │                                         │
├────────────────────────────────┼─────────────────────────────────────────┤
│                       Service layer (internal/)                          │
│                                │                                         │
│  ┌──────────────┐   ┌──────────▼───────────┐   ┌──────────────────┐      │
│  │   poll       │   │      state           │   │    actions       │      │
│  │  (cron +     │   │   (in-mem cache +    │   │ (update/rollback │      │
│  │  event sub)  ├──►│  RWMutex + atomic    │◄──┤  /force-pull     │      │
│  │              │   │   JSON persistence)  │   │  orchestrator)   │      │
│  └──┬────────┬──┘   └──────────┬───────────┘   └────┬─────────────┘      │
│     │        │                 │                    │                    │
├─────┼────────┼─────────────────┼────────────────────┼────────────────────┤
│     │        │            Adapters (internal/)      │                    │
│     ▼        ▼                 ▼                    ▼                    │
│  ┌──────┐ ┌──────┐  ┌────────────────────┐  ┌─────────────────┐          │
│  │ reg  │ │docker│  │  state/store.go    │  │  compose        │          │
│  │ HTTP │ │client│  │  (os.WriteFile +   │  │  (exec.Command  │          │
│  │ HEAD │ │Events│  │   os.Rename +      │  │   wrapper)      │          │
│  │ +tok │ │/pull │  │   fsync(dir))      │  │                 │          │
│  └──┬───┘ └──┬───┘  └──────────┬─────────┘  └────────┬────────┘          │
└─────┼────────┼─────────────────┼─────────────────────┼───────────────────┘
      │        │                 │                     │
      ▼        ▼                 ▼                     ▼
  GHCR/Hub  /var/run/   /state/hmi_update_state.json   docker compose
  registry  docker.sock                                (subprocess)
```

### Component Responsibilities

| Component | Responsibility | Typical Implementation |
|-----------|----------------|------------------------|
| `cmd/hmi-update` | Wire dependencies, parse env, start HTTP + poller, signal handling | `main.go`: build a `*Server`, `*Poller`, `*Store` and pass into each other via constructors |
| `internal/api` | HTTP routing, JSON marshalling, mapping service errors → HTTP codes | `chi` mux or stdlib `http.ServeMux` (Go 1.22+ supports method-prefixed routes) |
| `internal/state` | In-memory cache of state, RWMutex, atomic JSON persist, schema migration | `Store` struct with `map[string]Container`, `sync.RWMutex`, `Save()` writes tmp+rename+fsync |
| `internal/poll` | Cron tick, Docker event subscription, fan-out to registry checks | `robfig/cron/v3` scheduler + goroutine consuming `client.Events()` channel |
| `internal/registry` | OCI manifest digest fetching: token flow, HEAD, multi-arch resolution | `net/http` client + `crane.Digest()` fallback option |
| `internal/docker` | Wrapper over `docker/docker/client`: list containers, pull, tag, events | Thin facade exposing the small subset of `client.Client` methods we use |
| `internal/compose` | Subprocess invocation for `docker compose up -d --force-recreate <svc>` | `exec.CommandContext` with separate `Stdout`/`Stderr` buffers, captured into slog |
| `internal/actions` | Orchestration: update/rollback/force-pull workflows that span domains | Pure functions: `Update(ctx, name) error` that calls registry → docker → state → compose |
| `ui/` (embedded) | Svelte 5 SPA shipped as a `dist/` tree mounted into the binary via `embed.FS` | Vite build output, hashed asset filenames, served by `http.FileServerFS` |

### Architectural Style

This is a **modular monolith** with **hexagonal-flavored** internal boundaries: ports (interfaces in service packages) and adapters (`registry`, `docker`, `compose`, `state` filesystem). Sized correctly for a <30 MB binary with ~5 watched containers.

## Recommended Project Structure

```
hmi-update/
├── cmd/
│   └── hmi-update/
│       └── main.go              # wire-up + signal handling only
├── internal/
│   ├── api/
│   │   ├── server.go            # http.Server construction, middleware
│   │   ├── routes.go            # mux registration
│   │   ├── handlers_state.go    # GET /api/state, /healthz
│   │   ├── handlers_actions.go  # POST /api/containers/:name/{update,rollback,force-pull}
│   │   └── types.go             # request/response DTOs (source of TS types)
│   ├── state/
│   │   ├── schema.go            # State, Container struct definitions (versioned)
│   │   ├── store.go             # in-mem cache + RWMutex, Get/Set/Snapshot
│   │   └── persist.go           # Load(path), Save(path) with atomic temp+rename+fsync
│   ├── registry/
│   │   ├── client.go            # HTTPClient, Bearer-token flow, HEAD
│   │   ├── manifest.go          # multi-arch index resolution (amd64/linux)
│   │   └── types.go             # OCI media types, index/manifest structs
│   ├── docker/
│   │   ├── client.go            # NewClient(socketPath), facade methods
│   │   ├── events.go            # Subscribe() returns <-chan ContainerEvent
│   │   └── containers.go        # List(label), Pull(ref), Tag(src,dst)
│   ├── compose/
│   │   └── runner.go            # Up(ctx, service) error; captures stdout/stderr
│   ├── poll/
│   │   ├── poller.go            # cron loop + Watch() pump from docker.Events
│   │   └── debounce.go          # collapses event bursts (e.g. compose recreate)
│   └── actions/
│       ├── update.go            # pull → record previous_digest → compose up
│       ├── rollback.go          # docker tag → compose up
│       └── force_pull.go        # docker pull regardless of digest
├── ui/                          # Svelte 5 + Vite + Tailwind
│   ├── src/
│   │   ├── App.svelte
│   │   ├── lib/api.ts           # fetch client; types from generated types.d.ts
│   │   └── lib/types.d.ts       # generated by tygo from internal/api/types.go
│   ├── package.json
│   ├── tailwind.config.js
│   └── vite.config.ts           # outDir: ../internal/api/dist
├── internal/api/dist/           # Vite build output, embedded via //go:embed
├── e2e/
│   ├── playwright.config.ts
│   ├── compose.test.yml         # zot fake registry + hmi-update + 2 watched test containers
│   ├── fixtures/
│   │   └── push-image.ts        # helper: push a new manifest digest to fake registry
│   └── tests/
│       ├── f1-discovery.spec.ts
│       ├── f2-update.spec.ts
│       └── ...
├── tygo.yaml                    # generates ui/src/lib/types.d.ts from internal/api/types.go
├── Dockerfile
├── Makefile
├── .github/workflows/ci.yml
├── go.mod
└── go.sum
```

### Structure Rationale

- **`cmd/hmi-update/` is thin.** Only does flag/env parsing, builds dependencies, starts the server. Easy to understand at a glance. No business logic.
- **`internal/` is invisible to importers** (Go's module system enforces this) — important even for a single-repo project because it prevents accidental coupling once we publish anything.
- **`actions/` lives separately from `api/`.** HTTP handlers are thin: parse → call action → marshal response. Actions can be unit-tested without HTTP. They are the orchestration layer that span registry+docker+state+compose.
- **`docker/`, `registry/`, `compose/` are adapters** that translate from external APIs into our domain types. The actions package depends on small interfaces it defines, not on the concrete clients. This makes `poll` and `actions` testable with fakes in unit tests, while keeping e2e tests as the source of truth.
- **`internal/api/dist/` is the embed target.** Vite's `outDir` writes here; the Go embed directive `//go:embed dist/*` lives in the same package as the static handler. Keeps the embed proximity close to consumer.
- **`internal/api/types.go` is the contract.** It's the **only** source for request/response shapes. `tygo` reads this file and emits `ui/src/lib/types.d.ts`. Single source of truth, no hand-drift.
- **`e2e/compose.test.yml` is checked in.** It composes a `zot` fake registry, `hmi-update`, and two stub containers labeled `hmi-update.watch=true`. Playwright fixtures push manifests into `zot` mid-test.

The brief's suggested layout is **validated** — only addition is `internal/compose/`, `internal/actions/`, and the embed-target convention.

## Architectural Patterns

### Pattern 1: In-memory cache with atomic JSON persist

**What:** Keep the canonical state in a `state.Store` struct guarded by `sync.RWMutex`. Every mutation calls `persist()` synchronously to write `state.json.tmp` then `os.Rename`. Reads never touch disk.

**When to use:** Small state (<100 KB), high read/write ratio, single process owns the file. Exactly our shape: ~10 containers × ~500 bytes each.

**Trade-offs:**
- Pro: Reads are lock-free under `RLock`, ~ns scale. No serialization overhead per HTTP request.
- Pro: Recovery is trivial — read JSON at boot, hold in memory.
- Con: A crash between `Set` and `persist` loses the last mutation. Acceptable here: every mutation is initiated by an explicit user click; user can retry.

**Why not `sync.Map`:** It's optimized for the wrong pattern (read-once, write-once per key). We have a small fixed key set that mutates atomically across multiple fields. RWMutex around a regular map is simpler, faster for our scale, and lets us snapshot the full struct under a single read lock for JSON marshalling.

**Example:**
```go
type Store struct {
    mu    sync.RWMutex
    path  string
    state State
}

func (s *Store) UpdateContainer(name string, fn func(*Container)) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    c := s.state.Containers[name]
    fn(&c)
    s.state.Containers[name] = c
    return s.persist() // tmp+rename+fsync, fail-loud
}

func (s *Store) Snapshot() State {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return deepCopy(s.state) // cheap; tens of containers
}
```

### Pattern 2: Atomic file write with directory fsync

**What:** Write to `state.json.tmp` in the same directory as the target, `fsync` the file, `os.Rename` to `state.json`, then `fsync` the **directory** descriptor.

**When to use:** Every state mutation. Mandatory for a bind-mounted state file that survives container restarts and host reboots.

**Trade-offs:**
- Pro: On Linux ext4/xfs with `data=ordered` (default), this is the canonical safe pattern.
- Pro: Survives Docker container kill mid-write.
- Con: Two fsyncs add ~1-5 ms latency per write. Imperceptible at our update rate.
- **Bind-mount gotcha:** the temp file MUST be on the same filesystem as the target. Since both live in `/state/` (the bind-mount), this is automatic. Never write to `/tmp` then rename — that crosses filesystems and `os.Rename` will fail with `EXDEV`.
- **fsync(dir) is required** for durability across host reboot. Without it, the rename can be lost if the host crashes seconds after the Go process believes the write completed.

**Recommendation:** Use `github.com/google/renameio` (or `renameio/v2`) — it handles tmp+rename+dirsync correctly and avoids the trap of forgetting the directory fsync. Lighter weight than rolling our own.

**Example:**
```go
import "github.com/google/renameio/v2"

func (s *Store) persist() error {
    data, err := json.MarshalIndent(s.state, "", "  ")
    if err != nil { return err }
    return renameio.WriteFile(s.path, data, 0o644) // handles tmp+rename+fsync
}
```

### Pattern 3: Single goroutine fans events into the poller, single writer to state

**What:** The cron tick AND the docker event subscriber both push work onto a single `chan pollRequest`. One consumer goroutine drains the channel and performs registry checks serially. State writes happen only in that consumer goroutine.

**When to use:** When you have multiple producers of "go check thing X" signals and want strict serialization without read-modify-write races.

**Trade-offs:**
- Pro: Eliminates the classic bug where the cron poll and an event-driven poll both read the same state, both modify, both write — last-writer wins.
- Pro: Trivial to reason about: state mutations form a total order.
- Pro: HTTP handlers for `/api/state` still read concurrently via RLock; only the writer is serialized.
- Con: A slow registry call could backlog the channel. Mitigate with `select { default: drop }` semantics for cron — the next tick will retry.

**Example:**
```go
type pollRequest struct {
    serviceName string
    reason      string // "cron", "event:create", "manual"
}

func (p *Poller) Run(ctx context.Context) {
    work := make(chan pollRequest, 32)

    // Producer 1: cron ticks
    p.cron.AddFunc(p.spec, func() {
        for _, name := range p.watchedServices() {
            select { case work <- pollRequest{name, "cron"}: default: }
        }
    })

    // Producer 2: docker events
    go p.pumpEvents(ctx, work)

    // Single consumer
    for req := range work {
        if ctx.Err() != nil { return }
        p.checkOne(ctx, req)
    }
}
```

### Pattern 4: Embedded SPA with hashed assets

**What:** Vite emits `dist/index.html` plus `dist/assets/index-<hash>.js` and `dist/assets/index-<hash>.css`. Use `//go:embed dist/*` and serve via `http.FileServerFS` rooted at a `fs.Sub` of the embed FS. Hashed filenames mean assets get long cache lifetimes, while `index.html` stays `no-cache`.

**When to use:** Every Go+SPA single-binary deployment.

**Trade-offs:**
- Pro: One binary, no separate web server.
- Pro: Hashed names mean we never serve a stale JS file after a binary upgrade.
- Con: SPA fallback (non-existent paths → `index.html`) requires a tiny custom handler; `http.FileServerFS` alone returns 404. For us this is moot — no client-side router — but worth knowing.

**Example:**
```go
//go:embed dist
var distFS embed.FS

func newStaticHandler() http.Handler {
    sub, _ := fs.Sub(distFS, "dist")
    fs := http.FileServerFS(sub)
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if strings.HasSuffix(r.URL.Path, ".html") || r.URL.Path == "/" {
            w.Header().Set("Cache-Control", "no-cache")
        } else {
            w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
        }
        fs.ServeHTTP(w, r)
    })
}
```

### Pattern 5: Constructor injection with narrow interfaces

**What:** Each service package defines the **interface it consumes** (not the interface its dependency exports). `actions.Updater` depends on a `RegistryClient` interface defined in `actions/`, with a 2-method surface. The concrete `registry.Client` satisfies it.

**When to use:** Whenever a package would otherwise import a heavy dependency just for tests. Especially valuable for `actions` and `poll`.

**Trade-offs:**
- Pro: Unit tests inject fakes without touching docker daemon or network.
- Pro: Makes the contract between layers explicit and minimal.
- Con: One extra interface per consumer. Trivial for a 6-package codebase.

```go
// in internal/actions/update.go
type registryClient interface {
    Digest(ctx context.Context, ref string) (string, error)
}

type Updater struct {
    reg     registryClient
    docker  dockerClient
    compose composeRunner
    store   *state.Store
}

func NewUpdater(reg registryClient, ...) *Updater { ... }
```

## Data Flow

### Request Flow: User clicks "Update"

```
Browser (Svelte)
    │ fetch POST /api/containers/flutter/update
    ▼
http.ServeMux → handlers_actions.go::Update
    │ pure transport: parse name, no logic
    ▼
actions.Updater.Update(ctx, "flutter")
    │
    ├─► state.Store.GetContainer("flutter")             [RLock, in-memory]
    ├─► docker.Client.Pull("ghcr.io/.../...:latest")    [streams via Docker API]
    ├─► docker.Client.InspectImage(...)                 [read new RepoDigests[0]]
    ├─► state.Store.UpdateContainer("flutter", fn)      [Lock, write+fsync state.json]
    ├─► compose.Runner.Up(ctx, "flutter")               [exec.Cmd subprocess]
    └─► state.Store.UpdateContainer("flutter", fn)      [record last_action, last_action_at]
        │
    ▼
return UpdateResponse{ current, previous, duration_ms }
    │
    ▼
slog.Info("update", container=..., before=..., after=..., exit_code=..., duration_ms=...)
    ▼
JSON response → Browser → toast
```

### Request Flow: 5-second background refresh

```
Browser setInterval(5s) → GET /api/state
    ▼
handlers_state.go::GetState
    ▼
state.Store.Snapshot()    [RLock only; no disk, no daemon, no network]
    ▼
json.NewEncoder(w).Encode(snapshot)
```

Cost is dominated by JSON marshal. Easily handles dozens of concurrent UI tabs.

### Background Flow: Cron tick

```
robfig/cron tick (every "0 * * * *" by default)
    ▼
Poller.onTick: for each watched service, send pollRequest{name, "cron"} to channel
    ▼
Single consumer goroutine:
    for each req:
        ▼
    registry.Client.HeadDigest(ref)         [GET /token, HEAD /v2/.../manifests/tag]
        │  if multi-arch index → GET index, find linux/amd64 entry, HEAD that
        ▼
    compare to state.Container.current_digest
        │  if different → state.Store.UpdateContainer (set available_digest)
        ▼
    slog.Info("poll", ...)
```

### Background Flow: Docker event

```
docker.Client.Events(ctx, filters{type=container, event=create|start|destroy})
    ▼
Poller.pumpEvents: for each event:
    │  if event.Actor.Attributes["label.hmi-update.watch"] == "true":
    ▼
    debouncer collapses bursts (compose recreate emits multiple events in <1s)
    ▼
    send pollRequest{serviceName, "event"} → same consumer channel
```

Debounce: keep a `map[serviceName]*time.Timer`; reset on each event; fire after 500 ms of quiet.

### State Management (frontend)

```
Svelte store
    │ on mount: fetch /api/state → write to store
    │ setInterval(5000): fetch /api/state → diff → write to store
    │ on button click: fetch /api/containers/:n/update → on response, fetch /api/state
    ▼
Components subscribe via $store
```

No client-side mutations; the server's state is canonical. Optimistic UI is unnecessary at this scale.

### Key Data Flows

1. **Discovery flow:** Docker `create` event → poller debouncer → registry digest fetch → state write → next UI poll picks it up. End-to-end <5 s.
2. **Update detection flow:** Cron tick → enumerate watched containers → registry HEAD per container → diff → state write. End-to-end ~seconds depending on registry RTT.
3. **Manual action flow:** UI → API handler → action orchestrator → (docker + registry + compose + state) → state write → UI re-polls within 5 s.
4. **Recovery flow:** Process restart → `state.Load(path)` → in-memory cache populated → handlers serve. No registry hits on boot; first cron tick within minutes refreshes data.

## Concurrency Model

**Goroutines in steady state:**
1. `http.Server` (one accept loop + N handler goroutines)
2. `cron.Cron` scheduler (one goroutine; spawns short-lived goroutines per tick — we override to push to channel instead)
3. **One** poller consumer goroutine draining `chan pollRequest`
4. Docker `Events()` pump goroutine
5. Signal handler / shutdown context

**Synchronization primitives:**

| Resource | Primitive | Acquired by |
|----------|-----------|-------------|
| `state.Store.state` (in-memory map) | `sync.RWMutex` | HTTP handlers (RLock for reads), poller consumer + action orchestrators (Lock for writes) |
| `state.json` on disk | implicit (single writer goroutine through Store.Lock) | only the Store; never written from two goroutines at once |
| `chan pollRequest` | channel semantics | cron callback + docker events producer (send); poller consumer (receive) |
| `context.Context` | cancellation tree | main → server, poller, action orchestrators; everything respects ctx.Done() |
| `compose` subprocess | none required | each invocation is `exec.CommandContext`-bounded; serialize per-service via per-service mutex map if needed |

**Compose serialization:** Two concurrent updates on the **same** service would race (both calling `docker compose up -d --force-recreate <svc>`). Solution: a `map[serviceName]*sync.Mutex` in `actions.Updater`, locked per-service. Different services can update in parallel.

**Anti-deadlock rule:** Never hold `state.Store.mu` while calling registry/docker/compose. Pattern is: take Lock → mutate map → release Lock → persist. The persist step takes the lock internally; arrange as one critical section.

**Graceful shutdown:** `main.go` listens for SIGINT/SIGTERM, cancels the root context, calls `srv.Shutdown(ctx)` with a 10 s deadline, then `poller.Stop()`. In-flight actions complete or are cancelled via context.

## Suggested Build Order (TDD-First)

The TDD constraint says each functional requirement starts as a failing Playwright e2e test against a real compose stack with a fake registry. This forces a specific ordering: **the test harness itself must work before any feature test can be written.** Build vertically through the smallest possible end-to-end skeleton first, then iterate per F-requirement.

### Phase A: Walking skeleton (before any F-requirement test can fail meaningfully)

**Goal:** Stand up the minimum that lets a Playwright test render a page and read state.

1. **Repo + Dockerfile + Makefile + CI scaffolding.** `make build` produces a binary; `make image` builds the OCI image; CI runs `go build`.
2. **`internal/state` (schema + Store + Load/Save) with unit tests.** Schema, atomic persist via `renameio`. Covered by Go unit tests, no e2e.
3. **`cmd/hmi-update` + `internal/api`: minimal HTTP server.** Routes for `GET /healthz` (returns 200) and `GET /api/state` (returns the loaded state). Embed an empty placeholder Svelte page.
4. **`ui/`: Svelte 5 + Vite + Tailwind scaffold.** A single `App.svelte` that fetches `/api/state` and renders an empty table. Vite outputs to `internal/api/dist/`. Go `//go:embed` consumes it.
5. **`e2e/compose.test.yml`: minimum test stack.** `zot` fake registry + `hmi-update` (built from this repo) + 1 stub container labeled `hmi-update.watch=true`. Playwright config points at `http://localhost:8080`.
6. **Fixture: push manifest helper.** A TS helper using `oras` CLI or raw HTTP to push a new manifest to `zot` and return the digest. Required for F1/F2/F3 tests.
7. **Tygo wired into Makefile.** `make types` regenerates `ui/src/lib/types.d.ts` from `internal/api/types.go`. Add to CI as a check (fail if out of date).
8. **First Playwright smoke test:** open `/`, assert table renders, assert `/api/state` returns valid JSON. **This must pass before any feature test is written.**

At the end of Phase A, the test harness can drive the binary and assert on its output. **Now, and only now, F1's failing test makes sense.**

### Phase B: Feature implementation, one F-requirement at a time

Order chosen to maximize what later tests can reuse:

1. **F1: Update detection (poll + events).** Build `internal/registry`, `internal/docker`, `internal/poll`. F1 test pushes a new manifest to `zot`, expects UI to show "update available" within poll interval. Highest test surface; once green, F2/F3 reuse the registry + docker scaffolding.
2. **F4: State persistence atomic writes** — actually built in Phase A but the F4 e2e test (`compose restart` → state preserved) is added here once F1 is producing real state to preserve.
3. **F5: Tag pattern label.** Small addition to `registry`/`poll`. Stack atop F1.
4. **F2: Manual update action.** Build `internal/compose` and `internal/actions/update.go`. Reuses F1's registry + docker + state. The compose subprocess is exercised against `e2e/compose.test.yml`.
5. **F3: Manual rollback.** Builds on F2 — uses `docker tag` instead of pull, same compose path. State already has `previous_digest` because F2 records it.
6. **F8: Force-pull.** Trivial after F2 (pull without digest comparison).
7. **F6: Complete UI.** Buttons, toasts, disabled states, copy-digest icons. Most of the UI work happens here, but the Svelte shell from Phase A means each F-test along the way already had a place to render.
8. **F7 / N1: Compose deployment portability.** Acceptance test 6: bring up on a second host. CI job.
9. **N7, N8: Structured logging + observability.** Slog wiring (parts of which existed from F1 onward) and `/healthz` enhancement (docker socket reachability).

### Why this order respects TDD

- **F1 must come first** because it produces the state that F2 mutates and F3 reverts. Trying to test F2 before F1 means hand-stuffing state via fixtures — fragile and decouples tests from the real system.
- **F4's persistence test can't exist** until something produces state to persist; F1 does that.
- **Compose subprocess (F2) is delayed** until after F1 because invoking `docker compose up -d --force-recreate` requires the test stack's compose file to be reachable from inside the `hmi-update` container — a bind-mount and a known path. Phase A establishes both.
- **UI completeness (F6) is last** because the smaller scaffolding UI from Phase A is enough for every other F-test to make assertions on.

## Test-Stack Architecture

### Components of `e2e/compose.test.yml`

```yaml
services:
  registry:
    image: ghcr.io/project-zot/zot-linux-amd64:latest
    ports: ["5000:5000"]
    volumes:
      - ./fixtures/zot-config.json:/etc/zot/config.json:ro
      - zot-data:/var/lib/registry
    # zot supports OCI distribution spec; tags are mutable; we push fresh
    # manifests mid-test to flip :latest

  watched-a:
    # A trivial test container whose image lives in the fake registry.
    # Labeled to be watched by hmi-update.
    image: registry:5000/test/watched-a:latest
    labels:
      - hmi-update.watch=true

  watched-b:
    image: registry:5000/test/watched-b:latest
    labels:
      - hmi-update.watch=true
      - hmi-update.allow-update=false  # for F7 safety test

  hmi-update:
    build:
      context: ..
      dockerfile: Dockerfile
    ports: ["8080:8080"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./compose.test.yml:/host/docker-compose.yml:ro
      - hmi-state:/state
    environment:
      - HMI_UPDATE_CRON=*/1 * * * * *   # 1-second poll for fast tests
      - HMI_UPDATE_COMPOSE_PATH=/host/docker-compose.yml
      - HMI_UPDATE_STATE_PATH=/state/hmi_update_state.json
    depends_on: [registry, watched-a, watched-b]

volumes:
  zot-data:
  hmi-state:
```

### Why `zot`

- **Pure-Go, single binary, OCI distribution spec compliant.** Lighter than CNCF distribution `registry:2` and runs as a true distroless container.
- **Anonymous pull works out of the box** — matches our prod GHCR public-image assumption.
- **Tag mutability is the default.** Pushing a new manifest at `:latest` simply moves the tag pointer; exactly the production behavior we need to simulate.
- **No external dependencies.** Composed in, torn down with `docker compose down -v`.

Alternative is CNCF `distribution/distribution:2`. It works too but is older and ships as a debian-based image. Zot wins for this use case.

### Playwright fixture: pushing a fresh manifest mid-test

The test needs to flip `registry:5000/test/watched-a:latest` to a new digest during the test. Approach:

1. **Use `oras push` (CLI) via `child_process` from Playwright.** Build a small "throwaway artifact" (different bytes each time — append `Date.now()` to a file) and push it as `:latest`. The new manifest has a new digest; tag mutates to point at it.

```typescript
// e2e/fixtures/push-image.ts
import { execSync } from 'child_process'
import { writeFileSync } from 'fs'

export function pushNewVersion(repo: string): string {
  const file = `/tmp/payload-${Date.now()}.txt`
  writeFileSync(file, `version-${Date.now()}`)
  const out = execSync(
    `oras push localhost:5000/${repo}:latest ${file} --plain-http`,
    { encoding: 'utf8' }
  )
  // parse digest from oras output, return it
  return parseDigest(out)
}
```

2. **Alternative: raw HTTP from a Go test helper binary.** If `oras` CLI introduces a dependency we don't want in the e2e environment, write a 30-line Go binary that pushes a manifest via HTTP and exposes it as an HTTP endpoint the Playwright tests POST to. Adds a `helper` service to `compose.test.yml`.

3. **For multi-arch testing:** push an OCI image index manifest with one `manifests[]` entry for linux/amd64. Validates our registry client's index-resolution path. Build a fixture that constructs the index JSON inline and PUTs it.

### Disposability across runs

```bash
# Makefile target
e2e:
	docker compose -f e2e/compose.test.yml down -v --remove-orphans
	docker compose -f e2e/compose.test.yml up -d --build --wait
	cd e2e && npx playwright test
	docker compose -f e2e/compose.test.yml down -v
```

- `down -v` between runs nukes the zot volume and the hmi-update state volume. Each test starts from clean.
- `--wait` blocks until healthchecks pass — hmi-update needs `/healthz` to return 200 before tests start.
- **Per-test isolation:** Playwright `test.beforeEach` calls `POST /api/admin/reset` (a test-only endpoint compiled with a build tag) to clear state.json and re-trigger discovery. Alternatively, `test.beforeAll` does a full compose down/up. Faster: the reset endpoint.

### CI integration

GitHub Actions: a single job runs `make e2e`. Docker layer caching keeps the build fast. Playwright's `@playwright/test` GitHub reporter posts results into the PR. Total target: e2e completes in <3 minutes.

## OCI Registry Interaction — Verified Recommendation

The brief proposes raw HTTP (`HEAD /v2/<repo>/manifests/<tag>` with manual Bearer token flow). After research:

| Approach | Verdict |
|---|---|
| Raw HTTP | Works. Means writing token endpoint discovery (`Www-Authenticate` parsing), Bearer flow, Accept header negotiation, manifest-list parsing — ~200 lines of testable code. |
| `google/go-containerregistry` (`crane.Digest`) | **Recommended.** One line: `crane.Digest(ref, crane.WithPlatform(&v1.Platform{OS: "linux", Architecture: "amd64"}))`. Handles tokens via `authn.DefaultKeychain`, handles multi-arch indices, follows the spec. ~5 MB added to the binary. |
| `oras-go` | Overkill. Designed for artifact upload/download, not registry polling. |

**Recommendation: use crane.** The brief's "raw HTTP" idea predates familiarity with what crane provides. `crane.Digest()` covers the entire F1 requirement in one call. We retain a thin wrapper interface in `internal/registry/` so tests can fake it without standing up zot for every test.

Confidence: HIGH (verified against [crane package docs](https://pkg.go.dev/github.com/google/go-containerregistry/pkg/crane)).

## Compose Subprocess — Verified Recommendation

The Docker `compose-go` and `github.com/docker/compose/v2` libraries exist but:
- Their public Go API surface is **not** considered stable for embedding. The maintainers' explicit guidance is "use the CLI."
- Pulling them in adds significant dependency surface (BuildKit, swarm modules, etc.) — at odds with the <30 MB binary target.
- Testcontainers-Go embeds compose-go for compose-file parsing only, then invokes `docker compose` CLI for orchestration. Same pattern fits our needs.

**Recommendation: subprocess via `exec.CommandContext`.** Capture stdout and stderr into separate buffers, log them via slog with the captured exit code. Use `cmd.WaitDelay` (Go 1.20+) so a hung compose process can be killed via context cancellation. Pin the `docker` CLI version inside the hmi-update container image so behavior is reproducible.

```go
func (r *Runner) Up(ctx context.Context, service string) error {
    cmd := exec.CommandContext(ctx, "docker", "compose",
        "-f", r.composePath, "up", "-d", "--force-recreate", service)
    var stdout, stderr bytes.Buffer
    cmd.Stdout, cmd.Stderr = &stdout, &stderr
    cmd.WaitDelay = 5 * time.Second
    err := cmd.Run()
    slog.Info("compose up",
        "service", service,
        "exit_code", cmd.ProcessState.ExitCode(),
        "duration_ms", time.Since(start).Milliseconds(),
        "stdout", stdout.String(),
        "stderr", stderr.String())
    return err
}
```

Note: ship the `docker` CLI **and** the `compose` plugin inside the distroless image. The distroless base doesn't include them by default; copy them in via a builder stage. This may push image size above 30 MB — measure during Phase A. Fallback: use `gcr.io/distroless/cc-debian12` and copy `docker` + `compose` binaries from an Alpine builder.

## Frontend ↔ Backend Contract

**Type generation: `tygo`.** Single source of truth: `internal/api/types.go`. Tygo writes TypeScript to `ui/src/lib/types.d.ts`. Wire `make types` into pre-commit (or CI fail-on-diff). Hand-rolled types drift; six F-requirements × Go-TS pairs is enough surface to want generation.

**Refresh transport: 5-second polling, not SSE or WebSocket.**

- 5-second polling against a fast `/api/state` endpoint (memory-only, no I/O) costs negligible CPU and keeps the architecture single-threaded-feeling.
- SSE would add a long-lived goroutine per browser tab and require manual reconnection handling on transient network blips that the elevator HMI LAN may have.
- The UI is a single tab open on an HMI box's local browser — concurrency concerns are zero.
- For F1's "Watch now" button: a `POST /api/poll` endpoint that returns once the cron channel has been kicked. UI calls it then re-fetches state. Don't bother with push notifications.

## Embedding the Bundle — Concrete Recipe

```go
// internal/api/static.go
package api

import (
    "embed"
    "io/fs"
    "net/http"
)

//go:embed all:dist
var distFS embed.FS

func staticHandler() http.Handler {
    sub, err := fs.Sub(distFS, "dist")
    if err != nil { panic(err) }
    return http.FileServerFS(sub)
}
```

**Gotchas:**
- Use `//go:embed all:dist` to include files starting with `_` and `.` (Vite emits `.vite/` metadata in some configs).
- `http.FileServerFS` (Go 1.22+) is preferred over the older `http.FileServer(http.FS(sub))`.
- Vite's `base` config option must match the URL path the server mounts assets under. For us, mount at root `/` → set `base: '/'` in `vite.config.ts`.
- Distroless `nonroot` user (UID 65532) needs read access to the binary; the embedded FS lives inside the binary so no filesystem permissions issue.

## Scaling Considerations

| Scale | Architecture Adjustments |
|-------|--------------------------|
| 1 HMI, ~5 watched containers | Current architecture. RWMutex, JSON file, polling UI. Memory <30 MB. |
| 10 HMIs (no shared backend) | Same architecture, deployed N times. Each HMI is independent. No changes needed. |
| 100+ HMIs (out of scope) | Different product — fleet management — would require a central control plane. Explicitly out of scope per PROJECT.md. |
| Many watched containers per HMI (>50) | RWMutex still fine. Registry polling becomes the bottleneck; add parallelism inside the consumer goroutine with a small worker pool. State file remains <10 KB. |

### Scaling Priorities

1. **First bottleneck:** Registry rate limiting. GHCR has anonymous-pull limits. Mitigate by spacing registry calls (don't burst-fan-out on cron tick — process serially or with a low worker count) and respecting `Retry-After` headers.
2. **Second bottleneck:** Compose subprocess latency. `compose up -d --force-recreate` can take 10-30 s. Don't block the HTTP request — accept the request, return 202 with a job ID, persist job status, let the UI poll `/api/jobs/:id`. For v1, blocking up to ~60 s is fine because there's a human waiting on the click.

## Anti-Patterns

### Anti-Pattern 1: Reading state from disk on every HTTP request

**What people do:** `GET /api/state` calls `os.ReadFile(path)` + `json.Unmarshal`.
**Why it's wrong:** Inefficient under the 5-second UI poll. Worse, it races with the writer: a partial write between the unlink and the rename could be observed (it can't with proper atomic rename, but the temptation to "just read the file" leads to dropping the atomicity discipline).
**Do this instead:** State is in memory. Disk is the durability backstop only. Load once on boot, write through on every mutation.

### Anti-Pattern 2: Holding the state mutex while calling Docker/registry

**What people do:** `s.mu.Lock(); doc.Pull(); s.persist(); s.mu.Unlock()`.
**Why it's wrong:** `Pull` can take 30+ seconds; every concurrent HTTP read blocks. Worse, if `Pull` hangs, the lock never releases.
**Do this instead:** Read state under lock, release, perform I/O, acquire lock to write result. Use a per-service mutex to prevent concurrent action conflicts on the same service.

### Anti-Pattern 3: Trusting tag mutability for "no update"

**What people do:** "If the tag is `:latest`, just call `docker pull` periodically — Docker tells us if there's a new image."
**Why it's wrong:** `docker pull` actually downloads layers, wasting bandwidth on every poll. `docker pull` with no change is not free — it still talks to the registry and can be rate-limited.
**Do this instead:** `HEAD /v2/.../manifests/tag` only — single header request, cheap. Pull only when the user clicks Update.

### Anti-Pattern 4: Generating TS types by hand "because the API is small"

**What people do:** Hand-write `interface ContainerState { ... }` in TypeScript matching the Go struct.
**Why it's wrong:** Inevitably drifts. Renaming a Go field silently breaks the UI. Adding an optional field on the Go side gets forgotten in TS.
**Do this instead:** `tygo`. The cost is one config file and one Makefile target. The benefit is compile-time mismatches surface at type-check time, not runtime.

### Anti-Pattern 5: Using the Docker SDK's "high-level helpers" for compose

**What people do:** Try to invoke `github.com/docker/compose/v2/pkg/api` from Go directly.
**Why it's wrong:** Compose's Go API is internal — the maintainers reserve the right to break it. You inherit BuildKit, swarm, secrets, and dozens of other dependencies you don't need.
**Do this instead:** Shell out to `docker compose` CLI via `exec.Command`. Mature, stable, ships in the same container.

### Anti-Pattern 6: Writing to `/tmp` then renaming to `/state/`

**What people do:** `os.WriteFile("/tmp/state.json", ...)` then `os.Rename("/tmp/state.json", "/state/state.json")`.
**Why it's wrong:** `os.Rename` across filesystems returns `EXDEV` ("invalid cross-device link"). Bind mounts are separate filesystems.
**Do this instead:** Temp file lives in the same directory as the final file. Use `renameio.WriteFile` and let it handle the placement.

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---------|---------------------|-------|
| GHCR / Docker Hub | HTTPS `HEAD /v2/.../manifests/<tag>` via crane | Anonymous for our public images; honor `Retry-After`; cap concurrency |
| Docker daemon | UNIX socket `/var/run/docker.sock` via `docker/docker/client` | Mount RO not possible — Docker needs RW. Run as nonroot in `docker` group, or accept root in v1 |
| Docker Compose CLI | `exec.CommandContext("docker", "compose", ...)` | Must be present in the runtime image; capture stdout/stderr/exit code |
| Filesystem (bind mount) | `renameio.WriteFile` to `/state/hmi_update_state.json` | Bind mount must be a directory, not a file (else atomic rename fails) — adjust compose example accordingly |

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|---------------|-------|
| `api` ↔ `actions` | Direct function call via `*actions.Updater` etc. | Returns rich errors; `api` maps to HTTP codes |
| `actions` ↔ `registry`/`docker`/`compose` | Small consumer-defined interfaces | Enables fakes for unit tests; e2e tests use real implementations |
| `actions`/`poll` ↔ `state` | Direct via `*state.Store` | Store owns its mutex; callers never see it |
| `poll` ↔ `actions` | `poll` writes via `state.Store`; doesn't trigger actions | Detection is passive; UI invokes actions explicitly |
| `cmd/hmi-update` ↔ all | Constructor injection in `main()` | One wire-up function, no globals |

## Sources

- [Atomically writing files in Go — Michael Stapelberg](https://michael.stapelberg.ch/posts/2017-01-28-golang_atomically_writing/)
- [renameio package — google/renameio](https://pkg.go.dev/github.com/google/renameio)
- [A way to do atomic writes — LWN](https://lwn.net/Articles/789600/)
- [crane package — google/go-containerregistry](https://pkg.go.dev/github.com/google/go-containerregistry/pkg/crane)
- [google/go-containerregistry — GitHub](https://github.com/google/go-containerregistry)
- [OCI Distribution Spec — content negotiation discussion](https://github.com/opencontainers/distribution-spec/issues/212)
- [Open Container Initiative Distribution Specification](https://oci-playground.github.io/specs-latest/specs/distribution/v1.1.0-rc2/oci-distribution-spec.html)
- [Container images, multi-architecture, manifests — Open Sourcerers](https://www.opensourcerers.org/2020/11/16/container-images-multi-architecture-manifests-ids-digests-whats-behind/)
- [Docker events Go client — pkg.go.dev](https://pkg.go.dev/github.com/docker/docker/api/types/events)
- [docker system events — Docker Docs](https://docs.docker.com/reference/cli/docker/system/events/)
- [EventListener reconnect issue — fsouza/go-dockerclient #163](https://github.com/fsouza/go-dockerclient/issues/163)
- [embed package — Go stdlib](https://pkg.go.dev/embed)
- [Embed Vite app in a Go Binary — Tushar Choudhari](https://www.tushar.ch/writing/embed-vite-app-in-go-binary)
- [tygo — Generate TypeScript types from Go](https://github.com/gzuidhof/tygo)
- [Testcontainers for Go — Docker Compose](https://golang.testcontainers.org/features/docker_compose/)
- [project-zot/zot — OCI registry](https://github.com/project-zot/zot)
- [docker compose v2 Go module](https://pkg.go.dev/github.com/docker/compose/v2)
- [Mutex vs RWMutex vs sync.Map benchmark](https://github.com/ntsd/go-mutex-comparison)
- [Polling vs Long Polling vs SSE vs WebSockets](https://blog.algomaster.io/p/polling-vs-long-polling-vs-sse-vs-websockets-webhooks)

---
*Architecture research for: hmi-update*
*Researched: 2026-05-13*
