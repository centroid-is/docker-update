# Phase 3: Registry, Polling & Update Detection - Context

**Gathered:** 2026-05-14
**Status:** Ready for planning
**Mode:** Smart discuss (autonomous) — 4 grey areas, all accepted as recommended

<domain>
## Phase Boundary

Implement the digest-detection + poll loop that turns Phase 2's "watched container" enumeration into a working update-available signal:

1. **`internal/registry.Resolver` body** — wraps `github.com/google/go-containerregistry/pkg/crane` for correct multi-arch index handling (linux/amd64 platform filter) and anonymous bearer-token flow against GHCR / Docker Hub (DETECT-01..04).
2. **`internal/poll.Poller` body** — `robfig/cron/v3` scheduler driven by `HMI_UPDATE_CRON` env var (default `0 * * * *`), bounded worker-pool fetcher feeding a single-consumer channel that mutates `state.Store` (DETECT-05, DETECT-10).
3. **Tag-pattern filter** — `hmi-update.tag-pattern=<regex>` constrains which upstream tag's digest is fetched; compiled at discovery time (Phase 2 goroutine), permissive on invalid regex (DETECT-08).
4. **Pinned-image opt-out** — containers with `image: ...@sha256:...` are excluded from the fetch list with a `notes` field surfaced in `/api/state` (DETECT-09). Phase 2 already sets `Pinned: true`; Phase 3 honors it.
5. **Update-available flip** — on digest mismatch, `state.Container.UpdateAvailable=true` + `AvailableDigest=sha256:...` flips through `state.Store.Update(...)`; Phase 5 consumes via `/api/state` (DETECT-07).
6. **Bearer-token redaction (OBS-04)** — `redactingTransport` HTTP wrapper + slog `ReplaceAttr` belt-and-braces. Zero `Bearer` / `Authorization` matches in captured slog output across a full test run.
7. **Cron + event producer wiring** — Phase 2's discovery channel and Phase 3's poller produce into the same single-consumer goroutine; lock never held across registry I/O.

Out of scope for this phase: Update/Rollback/Force-pull endpoints (Phase 4), Compose runner mutex & verify-after-recreate (Phase 4), real UI rendering of `update_available` badge (Phase 5), display-blackout UX (Phase 6), production Dockerfile and image-size verification (Phase 7), full GitHub Actions pipeline and real-GHCR smoke job (Phase 8).

</domain>

<decisions>
## Implementation Decisions

### Area 1 — Registry Resolver (Library Wiring)

- **API surface:** `crane.Digest(ref, crane.WithAuth(authn.Anonymous), crane.WithPlatform(v1.Platform{OS: "linux", Architecture: "amd64"}))`. Exact API named by DETECT-01. `Docker-Content-Digest` response header is the digest source (DETECT-02 — `crane.Digest` returns the header value, never re-hashes the body).
- **Platform filter:** Hardcoded `linux/amd64`, matching v1 amd64-only constraint (CLAUDE.md "Platform: amd64 only for v1"). Add a `// TODO(V2-ARM64): wire from build/runtime arch` comment so the future buildx flip is one search away.
- **Authn:** Explicit `authn.Anonymous` — **never** `authn.DefaultKeychain`. This is the Pitfall 2 prevention: `DefaultKeychain` reads `~/.docker/config.json`, which on a host where `docker login` was run with an empty username emits `Authorization: Basic Og==` and breaks anonymous bearer flow against GHCR.
- **Timeout + retry:** Per-call `context.WithTimeout`, default 10 s, configurable via `HMI_UPDATE_REGISTRY_TIMEOUT_S`. Transient errors (network, 5xx, timeout) get 1 retry after 2 s backoff. Permanent errors (401, 403, 404) fail fast. Retry is exposed as a `Resolver` option, not hardcoded, so tests can disable it.

### Area 2 — Poller Architecture

- **Cron library:** `github.com/robfig/cron/v3`. Constructed as `cron.New(cron.WithLocation(time.UTC))`. Cron expression from `HMI_UPDATE_CRON` env var (default `"0 * * * *"`). Invalid expression → fail-fast at boot with a paste-ready error pointing at `HMI_UPDATE_CRON`.
- **Fetch concurrency:** Bounded `golang.org/x/sync/errgroup` worker pool, max 4 concurrent `crane.Digest` calls. Per-call context cancellation so SIGTERM unblocks the sweep.
- **Channel pattern (single-consumer state mutations — DETECT-10):**
  - Producer A: Phase 2's docker events goroutine (already exists).
  - Producer B (NEW): Phase 3's poll-tick goroutine.
  - Both producers send `stateUpdate` messages on a single buffered channel (`chan stateUpdate`, cap 64).
  - **Single consumer goroutine** drains the channel and applies each message via `state.Store.Update(func(*State))`. The store's `RWMutex` is taken inside `Update` (existing); the consumer never holds the lock across registry/docker I/O.
  - On `ctx.Done()`, consumer drains pending messages then exits cleanly.
- **"Last polled" surface:** Add `LastPolledAt time.Time` per `state.Container` and top-level `LastPollStart`, `LastPollEnd`, `LastPollError string` on `state.State`. Serialized as `time.RFC3339Nano`. Tygo regenerates TS types — `internal/api/types.go` and `ui/src/lib/types.d.ts` updated together; `make check-types` proves no drift.
- **Manual poll endpoint:** NOT in Phase 3. Phase 4's `POST /api/containers/:svc/force-pull` (ACT-08 area) will use the resolver to re-fetch on demand. Phase 3 ships scheduled cron + event-triggered discovery only.

### Area 3 — Tag-Pattern & Digest-Pin Handling

- **Regex compilation:** At discovery time in the Phase 2 goroutine. Cached as a non-persisted derived field on a sibling in-memory map keyed by service name, scoped to `internal/poll` (or `internal/registry`):

  ```go
  // internal/poll/patterns.go
  type Patterns struct {
      mu sync.RWMutex
      m  map[string]*regexp.Regexp // service -> compiled regex
  }
  func (p *Patterns) Set(service, pattern string) error { ... }
  func (p *Patterns) Match(service, tag string) bool   { ... }
  ```

  `state.Container.Labels["hmi-update.tag-pattern"]` is the source-of-truth raw string; the compiled regex is a derived in-memory artifact (regexp.Regexp is not JSON-serializable).

- **Invalid regex behavior:** Log structured warning `event=tag_pattern.invalid_regex service=… pattern=… err=…`, treat as **no constraint** (permissive — container still polled against the bare `:latest` tag). Surface `notes: "invalid tag-pattern label, ignored"` in `state.Container.Notes` (NEW field, `string`, omitempty). Never crash boot.

- **What the pattern filters:** **Upstream tag candidacy.** When a container has `tag-pattern=^latest-pg17$` and is running image `timescale/timescaledb:latest-pg17`:
  - The resolver fetches the digest of `:latest-pg17` (the only tag the regex matches).
  - If `:latest-pg18-oss` is pushed upstream, the resolver does not fetch it; `update_available` stays `false`. Satisfies Acceptance criterion 8 directly.
  - If the running tag itself doesn't match the regex (operator misconfiguration: pattern `^latest-pg17$` + running `:latest`), surface `notes: "running tag does not match tag-pattern label"` and don't flip `update_available`.
  - If no `hmi-update.tag-pattern` label is set, default is "any tag matches" — running tag is fetched directly with no constraint.

- **Pinned-image handling:** Phase 2 already sets `state.Container.Pinned = true` when the image reference is `image: ...@sha256:...`. Phase 3 explicitly skips pinned containers in `Poller.eligibleContainers()`. `/api/state` surfaces `notes: "pinned: opt-out"` so the UI Phase 5 can render the appropriate badge. Digest-drift detection for pinned refs is documented in `<deferred>` as "intentionally not supported — digest pin is the source of truth."

### Area 4 — Observability & Token Redaction (OBS-04)

- **Token redaction strategy (belt-and-braces):**
  1. **`redactingTransport`** in `internal/registry/transport.go` wraps `http.DefaultTransport`. On every request it makes a shallow copy of `req.Header`, strips `Authorization`, `WWW-Authenticate`, `X-Registry-Auth`, `Proxy-Authorization` before any slog-debug logging the transport itself emits. Passed to go-containerregistry via `remote.WithTransport(redactingTransport)` / `crane.WithTransport(...)`.
  2. **slog `ReplaceAttr`** in the JSON handler config: drops any attr whose string value matches `^Bearer ` or `^Basic ` (compiled regex once at boot). Catches accidental future code that logs headers without going through the transport.

- **What gets logged per poll:**
  - One structured event per fetch: `event=registry.fetch service=… image=… tag=… digest=sha256:… elapsed_ms=… status=ok|err err_class=…`. Never logs the request URL with query params (go-containerregistry may embed temporary credentials there); never logs response headers.
  - One batch summary per cron tick: `event=poll.sweep duration_ms=… polled=N changed=M skipped_pinned=K skipped_invalid_pattern=J`.
  - One boot-time event: `event=registry.authn keychain=anonymous` so operators can confirm authn choice from logs.

- **Pitfall 2 regression guard:**
  - Unit test `internal/registry/transport_test.go` using `httptest.NewServer` captures every inbound request's `Authorization` header; asserts the slice is empty when using `authn.Anonymous` against a registry that issues a bearer challenge.
  - Manual smoke on `ghcr.io/centroid-is/*` is success criterion #5 (one-time, documented in SMOKE.md).
  - The CI real-GHCR smoke job belongs to Phase 8 (CI-04).

- **"Last polled" exposure:** Only in `/api/state`. Top-level `last_poll_start`, `last_poll_end`, `last_poll_error` (string, omitempty) + per-container `last_polled_at`. No `/metrics` endpoint, no separate `/api/poll-status` endpoint. Keeps the binary lean and surface count minimal.

### File Layout

- `internal/registry/resolver.go` — `Resolver` interface body + `craneResolver` concrete impl wrapping `crane.Digest`.
- `internal/registry/resolver_test.go` — table-driven against `httptest` registry; covers multi-arch index, single-arch manifest, 401 retry, timeout, invalid ref.
- `internal/registry/transport.go` — `redactingTransport` + `RoundTrip` impl.
- `internal/registry/transport_test.go` — captures inbound `Authorization` headers; asserts empty under `authn.Anonymous`.
- `internal/registry/errors.go` — sentinel errors: `ErrPermanent` (401/403/404 — don't retry), `ErrTransient` (5xx/timeout — retry once).
- `internal/poll/poller.go` — `Poller` interface body + `cronPoller` concrete impl.
- `internal/poll/poller_test.go` — table-driven with a fake `Resolver`.
- `internal/poll/patterns.go` — `Patterns` (compiled regex cache, mutex-protected).
- `internal/poll/patterns_test.go` — invalid regex → permissive + warning; valid regex → match/non-match.
- `internal/poll/channel.go` — `stateUpdate` message type + single-consumer goroutine `RunUpdater(ctx, ch, store)`.
- `internal/poll/channel_test.go` — drain semantics on ctx cancel; lock never held across producer send.
- `internal/state/schema.go` — extend `Container` with `LastPolledAt`, `AvailableDigest`, `Notes`; extend `State` with `LastPollStart`, `LastPollEnd`, `LastPollError`. Tygo regenerates.
- `internal/api/types.go` — mirror `Container`, `State` additions.
- `cmd/hmi-update/main.go` — wire `registry.NewResolver(...)`, `poll.NewPoller(cron, store, resolver, patterns)`, `poll.RunUpdater(ctx, ch, store)` goroutine.
- `e2e/tests/detect-multiarch.spec.ts` — RED FIRST. Pushes both OCI index and direct single-arch manifest; both flip `update_available` within `cron + 5 s`.
- `e2e/tests/detect-tag-pattern.spec.ts` — RED FIRST. `timescaledb` w/ `^latest-pg17$` label: pushing `:latest-pg18-oss` does NOT flip; pushing new `:latest-pg17` digest DOES.
- `e2e/tests/detect-pinned.spec.ts` — RED FIRST. Container with `image: ...@sha256:...` appears in `/api/state` with `notes: "pinned: opt-out"`; never gets `update_available=true` no matter what is pushed.
- `e2e/tests/obs-04-redaction.spec.ts` — RED FIRST. Captures hmi-update container's stdout during a full poll sweep with auth-enabled zot; `grep -E '(Bearer |Authorization:)'` returns zero matches.

### Concurrency Invariants (extended from Phase 2)

- The state-update channel (`chan stateUpdate`) is the only path that mutates `state.Store` from Phase 2's discovery goroutine, Phase 3's poller goroutine, and (in Phase 4) the actions goroutines.
- The single consumer goroutine takes `state.Store.mu` only inside `state.Store.Update(...)` — never holds it across registry/docker I/O.
- Registry fetches happen in a bounded worker pool (max 4); results are collected before any state mutation runs.
- `Patterns.Match` uses an RWMutex (read-mostly: compiled at discovery, read by poller).

### Configuration Knobs (env vars introduced this phase)

- `HMI_UPDATE_CRON` — cron expression, default `"0 * * * *"`. Already in CLAUDE.md / brief.
- `HMI_UPDATE_REGISTRY_TIMEOUT_S` — per-call timeout in seconds, default `10`. New.
- `HMI_UPDATE_POLL_CONCURRENCY` — max concurrent fetches, default `4`. New (documented but unlikely to need tuning).

### Claude's Discretion

- Whether `Patterns` lives in `internal/poll/` or `internal/registry/`. Leaning `internal/poll/` because it's polling-loop logic, not registry-protocol logic.
- Exact slog field name conventions (`elapsed_ms` vs `duration_ms`; lean `elapsed_ms` for parity with Phase 2's `discovery.event.elapsed_ms`).
- Whether `stateUpdate` is one struct with a tagged union (`type UpdateKind int`) or three separate channels. Lean one-channel-one-struct — simpler to reason about and the kind discriminator is fine for a 3-variant union.
- Whether the redacting transport is registered as a global `http.DefaultTransport` swap or scoped to the registry package only. Lean scoped — package-local injection prevents accidental coverage gaps in unrelated HTTP traffic (none currently exists, but future-proofs the redaction).
- Exact retry policy class: lean "1 retry, fixed 2s sleep" over exponential backoff. Cron will catch the next tick anyway, so over-retry is wasted.
- Whether `LastPollError` is a string or a structured object. Lean string — operators read it from `/api/state`, a structured object adds tygo complexity for no UI win.
- Whether `Notes` is a single `string` or a `[]string`. Lean single `string` — at most one note applies at a time (pinned XOR invalid-pattern XOR running-tag-mismatch). If two apply, join with `; `.

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets

- `internal/state.Store.Update(func(*State))` (Phase 1) — single mutation point already used by Phase 2; Phase 3's consumer goroutine uses the same entry.
- `internal/state.Container` (Phase 1 + 2) — currently has Service, Image, Tag, CurrentDigest, PreviousDigest, UpdateAvailable, ContainerID, Labels, Pinned, Stopped. Phase 3 extends with `AvailableDigest`, `LastPolledAt`, `Notes` (all omitempty).
- `internal/state.State` (Phase 1 + 2) — `{version, containers}`. Phase 3 adds `LastPollStart`, `LastPollEnd`, `LastPollError`.
- `internal/docker.Client` interface (Phase 2) — already exposes `ContainerList`, `ContainerInspect`, `Events`. Phase 3 does NOT extend the docker client; registry is its own package.
- `internal/registry.Resolver` interface (Phase 1 stub) — body lands this phase.
- `internal/poll.Poller` interface (Phase 1 stub) — body lands this phase.
- `internal/api/types.go` (Phase 1 + 2) — tygo source-of-truth for TS types; Phase 3 mirrors the state schema additions.
- `e2e/compose.test.yml` (Phase 1) — already includes zot + stub watched container. Phase 3 reuses; may add a second labeled container (`timescaledb-stub`) for DETECT-08.

### Established Patterns

- **Atomic writes via `renameio`** (Phase 1) — every state mutation flows through `state.Store.persist()`.
- **Single-consumer channel for state mutations** (Phase 1, partially wired in Phase 2 for events) — Phase 3 adds the second producer (poller).
- **Sentinel errors** (Phase 1, Phase 2's `compose.ErrComposeFileMoved`) — Phase 3 follows: `registry.ErrPermanent`, `registry.ErrTransient`.
- **Tygo source-of-truth** — `internal/api/types.go` ↔ `ui/src/lib/types.d.ts`; CI's `make check-types` enforces.
- **Table-driven tests** — `internal/state/store_test.go`, `internal/docker/moby_test.go` set the model.
- **Per-package facade over external SDK** — `internal/docker` over `moby/moby/client`; `internal/registry` over `google/go-containerregistry`. CI grep guards prevent direct external imports outside the facade package.
- **Red-first Playwright e2e** (Phase 1, Phase 2) — every requirement starts as a failing spec; implementation drives it green.

### Integration Points

- `cmd/hmi-update/main.go` — Phase 3 adds:
  - `resolver := registry.NewResolver(...)`
  - `patterns := poll.NewPatterns()` (populated by Phase 2's discovery goroutine via a callback now wired in)
  - `poller := poll.NewPoller(cronExpr, resolver, patterns, store)`
  - `updates := make(chan poll.StateUpdate, 64)`
  - Phase 2's discovery goroutine now sends `StateUpdate` on `updates` instead of calling `state.Store.Update` directly (refactor of one site).
  - `poller.Run(ctx, updates)` (poll producer)
  - `poll.RunUpdater(ctx, updates, store)` (single consumer)
- `internal/state/schema.go` → `internal/api/types.go` → `ui/src/lib/types.d.ts` triple-edit per schema addition.

</code_context>

<specifics>
## Specific Ideas

- **`cron + 5 s` budget for the e2e flip test** — Acceptance criterion 2. Tests set `HMI_UPDATE_CRON="@every 5s"` so the worst-case flip window is ~10 s, keeping CI fast. Production default stays `0 * * * *`.
- **Both manifest shapes in the same e2e test** — push a single-arch manifest first, assert flip, push an OCI image index (with linux/amd64 + linux/arm64 entries), assert flip again. Both shapes must resolve to the same digest semantics for `crane.Digest(..., WithPlatform(amd64))`.
- **Single-consumer goroutine drain on ctx cancel** — important for Phase 4's STATE-04 SIGKILL-resistance work. Phase 3 ships the drain semantics; Phase 4 ships the test under SIGKILL.
- **Tag-pattern is server-resolved, never UI-rendered** — the regex is operator infrastructure (label on the compose service). Phase 5's UI shows `notes` strings, not the raw pattern. Keeps the UI calm.
- **`notes` field is for ops-readable state** — pinned, invalid pattern, running-tag mismatch. Capped at one short sentence per container. Phase 5 reads it; Phase 3 writes it.
- **OBS-04 redaction is double-defended** — transport-level (request side) + slog `ReplaceAttr` (output side). Either alone is enough; both together survive a future careless logger call.

</specifics>

<deferred>
## Deferred Ideas

- **Digest-drift detection for `@sha256:`-pinned containers** — intentionally not supported. The digest pin is the source of truth; detecting drift is by-design impossible (the operator pinned it to opt out of automatic updates). Phase 5 may add a "pinned: opt-out" badge tooltip; the detection itself is permanently out of scope.
- **`/metrics` Prometheus endpoint** — V2. Operators read state through `/api/state` for v1; adding a Prometheus client library would push the binary past the <30 MB budget.
- **Per-container manual poll endpoint** — Phase 4 (force-pull naturally covers it via ACT-08).
- **arm64 platform filter** — V2-ARM64 (CLAUDE.md). Code carries a `// TODO(V2-ARM64)` marker at the platform-filter site.
- **Configurable retry policy class** — operators get fixed "1 retry, 2 s sleep" today. If a sluggish registry becomes a real pain, expose `HMI_UPDATE_REGISTRY_MAX_RETRIES` in V2.
- **Real-GHCR live smoke job in CI** — Phase 8 (CI-04). Phase 3 ships the unit-test regression guard; the live-network test is a CI concern.
- **`fsnotify`-driven label-edit detection** — Phase 2 stat'd compose, but tag-pattern label edits on a running container are detected only on the next docker event (restart, recreate) or the next compose recreate. Operators almost never edit labels in flight; if a real workflow appears, V2.

</deferred>
</content>
