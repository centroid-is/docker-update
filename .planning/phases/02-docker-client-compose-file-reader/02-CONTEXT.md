# Phase 2: Docker Client & Compose-File Reader - Context

**Gathered:** 2026-05-13
**Status:** Ready for planning
**Mode:** Smart discuss (autonomous) — 3 grey areas, all accepted as recommended

<domain>
## Phase Boundary

Establish the hardened daemon-side adapter that every subsequent phase depends on:

1. **`internal/docker` facade** over `github.com/moby/moby/client` (DOCK-01) — list-by-label, inspect, events subscribe, pull, tag. Phase 2 implements the body of the `Client` interface that Phase 1 declared.
2. **Compose-file reader** at `HMI_UPDATE_COMPOSE_PATH` with `stat`-before-act + inode-drift detection (DOCK-02, Pitfall 10).
3. **`/healthz` upgrade** that distinguishes socket-EACCES (wrong GID) from socket-missing (no bind mount) from state-file-unreadable, with paste-ready remediation hints (DOCK-03, OBS-02).
4. **Watched-container enumeration** — containers labeled `hmi-update.watch=true` are enumerated at boot and via Docker events; visible in `/api/state` within 60 s of `docker compose up -d` (DOCK-04, Acceptance criterion 1).

Out of scope for this phase: registry digest detection and polling (Phase 3), Update/Rollback/Force-pull actions and their per-service mutex (Phase 4), real UI rendering (Phase 5), display-blackout UX (Phase 6), production Dockerfile and image-size verification (Phase 7), full GitHub Actions pipeline (Phase 8).

</domain>

<decisions>
## Implementation Decisions

### Docker Client — Discovery & Events
- **Initial discovery:** Boot-time `client.ContainerList(ctx, container.ListOptions{Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: "hmi-update.watch=true"})})` populates state once at startup.
- **Continuous discovery:** `client.Events(ctx, events.ListOptions{Filters: ...})` subscribes to `type=container` + `event=start,die,destroy`. Each event triggers a `ContainerInspect` to refresh fields, then mutates state through `state.Store.Update(...)` (single-consumer channel pattern from ARCHITECTURE.md — the channel is set up here, the cron producer joins in Phase 3).
- **Event mapping:**
  - `start` → add or refresh row (Inspect → upsert into `state.Containers`)
  - `die` → mark stopped (keep row, set a "stopped" status hint that Phase 5's status badge consumes)
  - `destroy` → remove row from state
- **Per-container enumeration fields (added to `internal/state.Container`):**
  - `Service` (from `com.docker.compose.service` label — already present)
  - `Image` (image name without tag)
  - `Tag` (parsed from image reference)
  - `ContainerID` (short, 12 chars)
  - `Labels` (filtered map: `hmi-update.watch`, `hmi-update.tag-pattern`, `hmi-update.allow-update`, `hmi-update.allow-rollback`)
  - `Pinned` (bool — `image: …@sha256:…` references)
  - `Stopped` (bool — set on `die`)
- **Digest-pinned image handling (DETECT-09 forecast):** Enumerate with `Pinned: true`; row appears in `/api/state` so the UI can show "pinned: opt-out" in Phase 5. Phase 3's poll loop filters pinned containers from the digest-fetch list.

### Compose-File Reader
- **Trigger:** `stat`-per-call. `compose.Reader.CheckUnchanged(ctx) error` is invoked by Phase 4 actions immediately before `compose up -d --force-recreate <svc>`. Deterministic, no goroutine, no fsnotify dependency.
- **Boot snapshot:** Captured at startup via `os.Stat(HMI_UPDATE_COMPOSE_PATH)`. Stored in-memory on the `Reader` struct as `{Inode uint64, ModTime time.Time, Size int64}`. Survives process lifetime; restart picks up the new file as the new baseline.
- **Drift error:** Defined sentinel `compose.ErrComposeFileMoved` in `internal/compose/errors.go`. Returned from `CheckUnchanged` whenever inode (preferred) or `(mtime, size)` (fallback on filesystems without stable inodes) differs from the boot snapshot. Phase 4's action handlers map this sentinel to HTTP 412 with body `{"error":"compose_file_moved","hint":"restart hmi-update to pick up the new docker-compose.yml"}`.
- **YAML parsing:** Phase 2 does NOT parse the compose file. Service identity comes from the `com.docker.compose.service` container label. YAML parsing is deferred until a phase actually needs the structured content (most likely never — the docker daemon is the source of truth for what compose is running).

### Healthz Remediation Hints (DOCK-03)
- **Detection flow** inside `/healthz`:
  1. `os.Stat("/var/run/docker.sock")` (path overridable via `HMI_UPDATE_DOCKER_HOST` for tests).
     - `errors.Is(err, fs.ErrNotExist)` → socket-missing.
     - `errors.Is(err, fs.ErrPermission)` → EACCES on the socket itself.
  2. If stat succeeded: `dockerClient.Ping(ctxWith500msTimeout)`.
     - On `errors.Is(err, syscall.EACCES)` (or string-match `"permission denied"` as belt-and-braces — docker SDK error shapes are not always typed) → EACCES.
     - Other errors → generic "docker daemon unreachable".
  3. If both pass, also run `state.Store.Get()` (no-op, but proves the store is still wired) → final 200.
- **Response bodies (verbatim):**
  - EACCES on socket stat or Ping: `{"status":"unhealthy","reason":"docker socket permission denied — set compose user: '65532:$(id -g docker)' (Pitfall 9)"}` — 503.
  - Socket missing: `{"status":"unhealthy","reason":"docker socket missing — add bind-mount '/var/run/docker.sock:/var/run/docker.sock'"}` — 503.
  - Docker daemon unreachable (other): `{"status":"unhealthy","reason":"docker daemon unreachable"}` — 503.
  - State store unavailable (unchanged from Phase 1): `{"status":"unhealthy","reason":"state store unavailable; check HMI_UPDATE_STATE_PATH and restart"}` — 503.
  - Healthy: `{"status":"ok"}` — 200.
- **No caching.** Fresh `Ping(ctxWith500ms)` per request. UI polls `/healthz` at most every 5 s; cost is negligible and a stale cache would mask a wedged daemon.

### Lifecycle & Wiring
- **Docker client construction:** `docker.NewClient(ctx)` at boot, default `client.WithAPIVersionNegotiation()` + `client.FromEnv` (so `DOCKER_HOST` env var works for tests). Single shared instance passed to:
  - `api.Server` (for healthz reachability check)
  - `compose.Reader` is independent of docker client
  - Phase 3's poller (constructed later) consumes the same client through dependency injection.
- **Boot order in `cmd/hmi-update/main.go`:**
  1. slog handler (existing)
  2. `state.NewStore` (existing)
  3. `docker.NewClient(ctx)` — fail-fast with clear error if construction fails (e.g., bad `DOCKER_HOST`)
  4. `compose.NewReader(os.Getenv("HMI_UPDATE_COMPOSE_PATH"))` — fail-fast if path missing/unstattable
  5. `discovery.Run(ctx, dockerClient, store)` — boot list + event subscription goroutine
  6. `api.NewServer(store, dockerClient, composeReader).ListenAndServe(":8080")`
- **Graceful shutdown:** Deferred to Phase 4 alongside STATE-04. Phase 2's discovery goroutine receives a context that cancels on SIGTERM; `client.Events` returns and the goroutine exits.

### Concurrency Invariants (foundation for Phase 3 & 4)
- All state mutation goes through `state.Store.Update(func(*State))` — RWMutex protects the in-memory snapshot + writes through `renameio` to disk.
- The discovery goroutine is the single consumer of Docker events. No other goroutine writes to `state` from Phase 2.
- `dockerClient` is concurrency-safe per moby SDK contract; no extra locking needed.
- `compose.Reader.CheckUnchanged` is read-only; safe to call from any goroutine.

### Testing Strategy
- **Unit tests (Go `testing`):**
  - `internal/docker/client_test.go` — table-driven against an in-process Docker socket mock or `httptest` server that speaks the Engine API for the subset we use (events, list, inspect). Cover: boot list with label filter, event stream parsing, error propagation.
  - `internal/compose/reader_test.go` — t.TempDir() with a real file, mutate via `os.Rename`-on-tmp pattern (atomic), assert sentinel error.
  - `internal/api/handlers_healthz_test.go` — table-driven over (stat result, ping result) tuples; mock `docker.Client.Ping` via interface.
- **E2E tests (Playwright):**
  - `e2e/tests/discovery.spec.ts` — bring up stack, wait for `/api/state` to contain `stub-watched-container` within 60 s (DOCK-04). Start a second labeled container via `docker exec` mid-test, expect it visible within 5 s (DETECT-06 secondary path — early proof).
  - `e2e/tests/healthz-negative.spec.ts` — uses `e2e/compose.test.override.eacces.yml` (compose override that runs hmi-update under a UID without docker GID); expects 503 + the EACCES hint string. Mirror override for socket-missing (omit bind-mount); expects the socket-missing hint string.
  - `e2e/tests/compose-drift.spec.ts` — `os.rename` the compose file mid-test (atomic-save pattern), expect Phase 2's stat to flag drift. Because Phase 2 has no action endpoint yet, the spec calls a temporary debug endpoint `GET /debug/compose-stat` that runs `CheckUnchanged()` and returns 200 ok / 412 moved. The debug endpoint is gated by `HMI_UPDATE_DEBUG=1` and stays out of production builds via build tag `//go:build debug`. Phase 4 removes the debug endpoint once `POST /api/containers/:svc/update` exercises the reader naturally.

### File Layout
- `internal/docker/client.go` — `Client` interface (existing, expand with methods)
- `internal/docker/moby.go` — `mobyClient` concrete impl wrapping `*client.Client`
- `internal/docker/discovery.go` — boot list + event loop goroutine
- `internal/docker/discovery_test.go`, `internal/docker/moby_test.go`
- `internal/compose/reader.go` — `Reader` struct + `CheckUnchanged()` method
- `internal/compose/errors.go` — `ErrComposeFileMoved` sentinel
- `internal/compose/reader_test.go`
- `internal/api/handlers.go` — `healthz` upgraded to take `dockerClient docker.Client`
- `internal/api/server.go` — `NewServer` signature extended to `(store, dockerClient, composeReader)`
- `internal/api/handlers_healthz_test.go`
- `cmd/hmi-update/main.go` — boot order wiring (above)
- `e2e/compose.test.override.eacces.yml` — compose override for negative healthz test
- `e2e/compose.test.override.no-socket.yml` — compose override for socket-missing test
- `e2e/tests/discovery.spec.ts`, `e2e/tests/healthz-negative.spec.ts`, `e2e/tests/compose-drift.spec.ts`

### Claude's Discretion
- Exact method set on `docker.Client` interface (the four operations needed in Phase 2 are List, Inspect, Events; Pull/Tag are needed by Phase 4 but can land as interface stubs now to avoid Phase 4 interface churn).
- Short vs full container ID storage (currently leaning short, 12 chars, matches `docker ps` output).
- Whether `discovery.Run` returns a `*Discoverer` handle (for graceful shutdown in Phase 4) or just runs as a fire-and-forget goroutine (lean toward returning a handle even in Phase 2 — Phase 4 needs the seam).
- 500ms vs 1s timeout on the healthz Ping (500ms preferred — fails fast under wedge).
- Exact wording of slog event names (`discovery.boot.start`, `discovery.event.received`, etc.).
- Whether to use `filters.NewArgs(filters.Arg("label", "hmi-update.watch=true"))` or pass label-filter map directly (newer moby SDK accepts both).
- Whether to inspect on `start` events or trust the event payload (lean toward inspect — payload missing some fields we need like labels).
- How aggressively to retry `client.Events` reconnect on disconnect (exponential backoff up to 30s).

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `internal/state.Store.Update(func(*State))` (Phase 1) — single mutation point, already concurrency-safe via RWMutex.
- `internal/state/Container` struct (Phase 1) — currently has Service, Image, Tag, CurrentDigest, PreviousDigest, UpdateAvailable. Phase 2 extends with ContainerID, Labels, Pinned, Stopped. Tygo regenerates `ui/src/lib/types.d.ts` from `internal/api/types.go` — both files must be edited together to keep `make check-types` green.
- `internal/api.Server` (Phase 1) — already exposes `healthz` and `getState`; Phase 2 modifies the constructor signature and the `healthz` body.
- `internal/docker.Client` interface (Phase 1 stub) — empty, ready to be defined with the real method set.
- `internal/compose.Runner` interface (Phase 1 stub) — Phase 4's concern, NOT touched in Phase 2.
- `e2e/global-setup.ts` + `compose.test.yml` (Phase 1) — already brings up zot + stub container + hmi-update. Phase 2 adds new spec files plus two compose overrides.

### Established Patterns
- **Atomic writes via `renameio`** — every state mutation flows through `state.Store.persist()` (which calls `renameio.WriteFile` + explicit dir-fsync). Phase 2 inherits this.
- **Single-consumer channel for state mutations** — laid out in ARCHITECTURE.md and the Phase 1 CONTEXT.md. Phase 2 implements the *first* producer (Docker events); Phase 3 adds the second (cron poller).
- **Errors are sentinel values, not strings** — Phase 1 uses error wrapping (`fmt.Errorf("%w", ...)`) plus typed errors. Phase 2 introduces `compose.ErrComposeFileMoved` following the same pattern.
- **Tests are table-driven where possible** — `internal/state/store_test.go` is the model.
- **Tygo source-of-truth contract** — `internal/api/types.go` field tags mirror `internal/state.Container` verbatim. Extending `state.Container` requires the matching edit in `api.Container`. CI's `make check-types` step catches drift.
- **Distroless runtime + tmpfs in e2e** — Phase 1 set up `/state:uid=65532,gid=65532` tmpfs; Phase 2's healthz negative tests will need similar care for the docker socket mount.

### Integration Points
- `cmd/hmi-update/main.go` is the wiring point. Phase 2 adds 3 lines (docker client, compose reader, discovery goroutine launch) and threads them through `api.NewServer`.
- `internal/api.Server` constructor signature changes from `NewServer(store)` to `NewServer(store, dockerClient, composeReader)`. `server_test.go` updates accordingly.
- `internal/state.Container` gains fields. `internal/api/types.go` mirrors. `make types` regenerates TS types. CI's `make check-types` proves no drift.
- `e2e/compose.test.yml` already has `hmi-update` binding `/var/run/docker.sock` to the container — Phase 2's discovery code uses it. The two new compose overrides target this same service block.

</code_context>

<specifics>
## Specific Ideas

- The 60s SLA in DOCK-04 / Acceptance criterion 1 is generous on purpose — it accommodates slow boot on cold HMIs. The actual target on a warm machine is <2s.
- The `discovery.Run` goroutine must NOT block boot — it kicks off async and the HTTP server comes up immediately. The first poll of `/api/state` may return an empty `containers` map; the Playwright test polls up to 60s.
- Inode equality is the preferred drift signal; on filesystems where inodes are not stable (e.g., some FUSE mounts), fall back to `(mtime, size)`. Document the fallback in the slog event payload.
- The EACCES hint references `id -g docker` — this is the documented Pitfall 9 fix from STACK.md. Operators copy-paste it.
- Resilience: if `client.Events` disconnects (daemon restarted, transient network glitch), the discovery goroutine reconnects with exponential backoff (1s, 2s, 4s, up to 30s) — log every reconnect attempt. After a successful reconnect, re-run the boot `ContainerList` to catch any state changes that occurred while disconnected.

</specifics>

<deferred>
## Deferred Ideas

- **YAML parsing of the compose file** — Phase 2 doesn't need it. May resurface if a future phase wants to render service dependency graphs.
- **fsnotify-based drift detection** — `stat`-per-call is simpler and deterministic; fsnotify can land in V2 if HMIs prove to do hot compose-file edits.
- **Caching `Ping` results** — V2 if `/healthz` traffic ever becomes a load concern. Today it's a 5s UI poll.
- **Graceful shutdown of discovery goroutine on SIGTERM** — Phase 4 (STATE-04 owns the broader fault-injection story).
- **Bearer-token redaction audit** — Phase 3 (OBS-04). Phase 2 doesn't speak to any registry yet.
- **Per-service mutex for actions** — Phase 4 (ACT-08); the docker client is concurrency-safe by itself.
- **Container `restart` event handling** — the brief doesn't require it; treat `restart` as a no-op for state. Reconsider in V2 if operators want a restart-count badge.

</deferred>
</content>
</invoke>