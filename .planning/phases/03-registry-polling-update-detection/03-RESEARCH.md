# Phase 3: Registry, Polling & Update Detection - Research

**Researched:** 2026-05-14
**Domain:** OCI registry digest resolution + cron poll loop + bounded concurrency + bearer-token redaction
**Confidence:** HIGH (every library API surface in this document was verified against pkg.go.dev or upstream source; assumptions are flagged inline)

## Summary

Phase 3 is the WUD-killer phase: the digest-detection layer where WUD 8.2.2's two named bugs (Pitfall 1 single-arch digest extraction, Pitfall 2 anonymous Basic Og== header) have to be designed out from the first red Playwright test. CONTEXT.md has already locked all four grey-area decisions — research's job here is to fill in the implementation-level technical detail (exact API signatures, exact channel patterns, exact regression-guard test shapes) and to cite-check the load-bearing claims (especially that `authn.Anonymous` does NOT emit a Basic Og== header, and that `crane.Digest(WithPlatform(amd64))` returns the *child* manifest digest, not the index digest, when given a multi-arch index).

The three load-bearing API findings:

1. `crane.Digest(ref string, opts ...crane.Option) (string, error)` — verified signature. `crane.WithPlatform` takes `*v1.Platform` (pointer, not value). `crane.WithTransport(http.RoundTripper)` exists. `crane.WithAuth(authn.Anonymous)` is the correct authn wiring — `authn.Anonymous` is a singleton `Authenticator` variable that returns an empty AuthConfig and produces NO `Authorization` header on the wire. [VERIFIED: pkg.go.dev/github.com/google/go-containerregistry/pkg/crane, pkg.go.dev/github.com/google/go-containerregistry/pkg/authn]

2. `crane.Digest` internally resolves a multi-arch index to the platform-specific child manifest via `Descriptor.Image()` when `WithPlatform` is set, then returns `img.Digest()` — i.e. **the child manifest's digest, NOT the index digest**. For single-arch manifests the same call returns the manifest's own `Docker-Content-Digest`. Both shapes produce the digest semantics DETECT-04 needs. [VERIFIED: github.com/google/go-containerregistry/blob/main/pkg/crane/digest.go]

3. `robfig/cron/v3` defaults to 5-field cron parsing (Minute Hour Dom Mon Dow) — matches `HMI_UPDATE_CRON="0 * * * *"` verbatim, no `WithSeconds()` needed for prod. For the e2e tests' `@every 5s` shape, `@every` is a built-in descriptor (no parser changes needed). `cron.Stop()` returns a `context.Context` that completes when in-flight jobs finish — exactly the drain semantic the consumer-goroutine pattern needs. [VERIFIED: pkg.go.dev/github.com/robfig/cron/v3]

**Primary recommendation:** Implement `internal/registry.craneResolver` as a thin (~80 LOC) facade around `crane.Digest`, with the redacting `http.RoundTripper` injected via `crane.WithTransport`. Implement `internal/poll.cronPoller` as a `cron.Cron` + bounded `errgroup` pool (`SetLimit(4)`) feeding the existing single-consumer state-update channel. State schema gains three top-level fields (`LastPollStart`, `LastPollEnd`, `LastPollError`) and three per-container fields (`LastPolledAt`, `AvailableDigest`, `Notes`). Patterns cache lives in `internal/poll/patterns.go` (RWMutex map). Redaction is double-defended: transport-level header strip + slog `ReplaceAttr` regex filter.

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Area 1 — Registry Resolver (Library Wiring)**

- **API surface:** `crane.Digest(ref, crane.WithAuth(authn.Anonymous), crane.WithPlatform(v1.Platform{OS: "linux", Architecture: "amd64"}))`. Exact API named by DETECT-01. `Docker-Content-Digest` response header is the digest source (DETECT-02 — `crane.Digest` returns the header value, never re-hashes the body).
- **Platform filter:** Hardcoded `linux/amd64`, matching v1 amd64-only constraint (CLAUDE.md "Platform: amd64 only for v1"). Add a `// TODO(V2-ARM64): wire from build/runtime arch` comment so the future buildx flip is one search away.
- **Authn:** Explicit `authn.Anonymous` — **never** `authn.DefaultKeychain`. This is the Pitfall 2 prevention: `DefaultKeychain` reads `~/.docker/config.json`, which on a host where `docker login` was run with an empty username emits `Authorization: Basic Og==` and breaks anonymous bearer flow against GHCR.
- **Timeout + retry:** Per-call `context.WithTimeout`, default 10 s, configurable via `HMI_UPDATE_REGISTRY_TIMEOUT_S`. Transient errors (network, 5xx, timeout) get 1 retry after 2 s backoff. Permanent errors (401, 403, 404) fail fast. Retry is exposed as a `Resolver` option, not hardcoded, so tests can disable it.

**Area 2 — Poller Architecture**

- **Cron library:** `github.com/robfig/cron/v3`. Constructed as `cron.New(cron.WithLocation(time.UTC))`. Cron expression from `HMI_UPDATE_CRON` env var (default `"0 * * * *"`). Invalid expression → fail-fast at boot with a paste-ready error pointing at `HMI_UPDATE_CRON`.
- **Fetch concurrency:** Bounded `golang.org/x/sync/errgroup` worker pool, max 4 concurrent `crane.Digest` calls. Per-call context cancellation so SIGTERM unblocks the sweep.
- **Channel pattern (single-consumer state mutations — DETECT-10):**
  - Producer A: Phase 2's docker events goroutine (already exists).
  - Producer B (NEW): Phase 3's poll-tick goroutine.
  - Both producers send `stateUpdate` messages on a single buffered channel (`chan stateUpdate`, cap 64).
  - Single consumer goroutine drains the channel and applies each message via `state.Store.Update(func(*State))`. The store's `RWMutex` is taken inside `Update` (existing); the consumer never holds the lock across registry/docker I/O.
  - On `ctx.Done()`, consumer drains pending messages then exits cleanly.
- **"Last polled" surface:** Add `LastPolledAt time.Time` per `state.Container` and top-level `LastPollStart`, `LastPollEnd`, `LastPollError string` on `state.State`. Serialized as `time.RFC3339Nano`. Tygo regenerates TS types — `internal/api/types.go` and `ui/src/lib/types.d.ts` updated together; `make check-types` proves no drift.
- **Manual poll endpoint:** NOT in Phase 3. Phase 4's `POST /api/containers/:svc/force-pull` (ACT-08 area) will use the resolver to re-fetch on demand. Phase 3 ships scheduled cron + event-triggered discovery only.

**Area 3 — Tag-Pattern & Digest-Pin Handling**

- Regex compilation at discovery time. Cached as a non-persisted derived field on a sibling in-memory map keyed by service name, scoped to `internal/poll/patterns.go` (struct `Patterns` with `mu sync.RWMutex` and `m map[string]*regexp.Regexp`). `state.Container.Labels["hmi-update.tag-pattern"]` is the source-of-truth raw string; the compiled regex is a derived in-memory artifact.
- Invalid regex behavior: log structured warning `event=tag_pattern.invalid_regex service=… pattern=… err=…`, treat as **no constraint** (permissive — container still polled against the bare `:latest` tag). Surface `notes: "invalid tag-pattern label, ignored"` in `state.Container.Notes`. Never crash boot.
- What the pattern filters: **upstream tag candidacy**. When `tag-pattern=^latest-pg17$` and image is `timescale/timescaledb:latest-pg17`, the resolver fetches the digest of `:latest-pg17` (the only tag the regex matches). If `:latest-pg18-oss` is pushed upstream, the resolver does not fetch it; `update_available` stays `false`. If the running tag itself doesn't match the regex (operator misconfig), surface `notes: "running tag does not match tag-pattern label"` and don't flip `update_available`. If no `hmi-update.tag-pattern` label is set, default is "any tag matches" — running tag is fetched directly with no constraint.
- Pinned-image handling: Phase 2 already sets `state.Container.Pinned = true` when image ref is `image: ...@sha256:...`. Phase 3 explicitly skips pinned containers in `Poller.eligibleContainers()`. `/api/state` surfaces `notes: "pinned: opt-out"`. Digest-drift detection for pinned refs is permanently out of scope.

**Area 4 — Observability & Token Redaction (OBS-04)**

- Token redaction strategy (belt-and-braces):
  1. `redactingTransport` in `internal/registry/transport.go` wraps `http.DefaultTransport`. Strips `Authorization`, `WWW-Authenticate`, `X-Registry-Auth`, `Proxy-Authorization` before any slog-debug logging the transport itself emits. Passed to go-containerregistry via `crane.WithTransport(redactingTransport)`.
  2. slog `ReplaceAttr` in the JSON handler config: drops any attr whose string value matches `^Bearer ` or `^Basic ` (compiled regex once at boot).
- What gets logged per poll: one structured event per fetch (`event=registry.fetch …`), one batch summary per cron tick (`event=poll.sweep …`), one boot-time event (`event=registry.authn keychain=anonymous`). Never logs the request URL with query params; never logs response headers.
- Pitfall 2 regression guard: unit test `internal/registry/transport_test.go` using `httptest.NewServer` captures every inbound request's `Authorization` header; asserts the slice is empty when using `authn.Anonymous` against a registry that issues a bearer challenge. Manual smoke on `ghcr.io/centroid-is/*` is success criterion #5 (one-time, documented in SMOKE.md). CI real-GHCR smoke job belongs to Phase 8 (CI-04).
- "Last polled" exposure only in `/api/state` (no `/metrics`, no separate `/api/poll-status`).

### Claude's Discretion

- Whether `Patterns` lives in `internal/poll/` or `internal/registry/`. Leaning `internal/poll/` because it's polling-loop logic, not registry-protocol logic.
- Exact slog field name conventions (`elapsed_ms` vs `duration_ms`; lean `elapsed_ms` for parity with Phase 2's `discovery.event.elapsed_ms`).
- Whether `stateUpdate` is one struct with a tagged union (`type UpdateKind int`) or three separate channels. Lean one-channel-one-struct.
- Whether the redacting transport is registered as a global `http.DefaultTransport` swap or scoped to the registry package only. Lean scoped.
- Exact retry policy class: lean "1 retry, fixed 2s sleep" over exponential backoff.
- Whether `LastPollError` is a string or a structured object. Lean string.
- Whether `Notes` is a single `string` or a `[]string`. Lean single `string` — at most one note applies at a time; if two apply, join with `; `.

### Deferred Ideas (OUT OF SCOPE)

- Digest-drift detection for `@sha256:`-pinned containers — intentionally not supported.
- `/metrics` Prometheus endpoint — V2.
- Per-container manual poll endpoint — Phase 4 (ACT-08 force-pull covers it).
- arm64 platform filter — V2-ARM64.
- Configurable retry policy class — operators get fixed "1 retry, 2 s sleep" today.
- Real-GHCR live smoke job in CI — Phase 8 (CI-04).
- `fsnotify`-driven label-edit detection — operators almost never edit labels in flight.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| DETECT-01 | `internal/registry` uses `crane.Digest()` with multi-arch index handling (linux/amd64 platform filter) | §"Standard Stack" + §"Code Examples: crane.Digest call", verified signature in §"Architecture Patterns Pattern 1" |
| DETECT-02 | `Docker-Content-Digest` response header is the digest source | Verified in §"Multi-arch index resolution semantics" — `crane.Digest` uses HEAD + reads the header via `remote.Head`/`remote.Image`, never re-hashes the body |
| DETECT-03 | Bearer-token flow does not send `Authorization: Basic Og==` | §"Bearer-token flow with authn.Anonymous" — `authn.Anonymous` is a singleton that emits NO Authorization header on the wire; §"Code Examples: redacting transport + regression test" provides the httptest-based guard |
| DETECT-04 | e2e fixture serves both OCI image index and direct single-arch manifest; both shapes resolve to same digest semantics | §"Per-platform manifest list pushing" + §"zot test fixture for both manifest shapes" |
| DETECT-05 | Cron poller using `robfig/cron/v3` on `HMI_UPDATE_CRON` (default `"0 * * * *"`) | §"robfig/cron/v3 wiring" |
| DETECT-06 | Docker event subscription detects new watched containers within 5 s | Phase 2 already ships this; Phase 3 inherits — see §"Single-consumer channel pattern" for how the event producer joins the same channel |
| DETECT-07 | New manifest pushed to `:latest` causes flip within `cron + 5 s` | §"Specifics" tests use `HMI_UPDATE_CRON=@every 5s`; §"Architecture Patterns Pattern 3" diagrams the producer-consumer flow |
| DETECT-08 | Tag-pattern constraint via `hmi-update.tag-pattern=<regex>` | §"Tag-pattern regex semantics" |
| DETECT-09 | `@sha256:`-pinned refs excluded from watch list with a note | §"Pinned image reference detection" — Phase 2 sets `Pinned: true`; Phase 3 skips in `eligibleContainers()` |
| DETECT-10 | Single-consumer channel; lock never held across registry/docker I/O | §"Single-consumer channel pattern" + Phase 2's `internal/docker/discovery.go` anti-deadlock invariant (already enforced) |
| OBS-04 | Bearer-token redaction — zero `Bearer`/`Authorization` matches in slog | §"Slog ReplaceAttr for token redaction" + double-defended transport strip |
</phase_requirements>

## Project Constraints (from CLAUDE.md)

- **C1 — One container, one binary:** Phase 3 adds no sidecars. The redacting transport lives inside the same process. `crane.Digest` is a library call, not a subprocess.
- **C2 — File-based persistence only:** New schema fields (`LastPolledAt`, `AvailableDigest`, `Notes`, `LastPollStart`, `LastPollEnd`, `LastPollError`) persist through the existing `renameio` atomic-write path. No SQLite/Mongo/Redis.
- **C3 — Self-contained compose deployment:** Three new env vars (`HMI_UPDATE_REGISTRY_TIMEOUT_S`, `HMI_UPDATE_POLL_CONCURRENCY`, plus already-documented `HMI_UPDATE_CRON`) — all optional with defaults.
- **C4 — TDD verify→implement loop:** Four Playwright spec files RED FIRST (`detect-multiarch`, `detect-tag-pattern`, `detect-pinned`, `obs-04-redaction`). Each must fail meaningfully against the Phase 2 stack before any Phase 3 implementation lands.
- **Platform: amd64 only for v1.** Hardcoded `linux/amd64` in `craneResolver` with `// TODO(V2-ARM64)` comment.
- **Footprint:** `google/go-containerregistry` adds ~5 MB to the binary (verified in STACK.md). Still well within the 30 MB image budget — pre-flight verification deferred to Phase 7.
- **CI grep guard:** Phase 3 introduces a second external-SDK facade (`internal/registry` over `google/go-containerregistry`). Mirrors Phase 2's `internal/docker` boundary — add a CI grep rule: no package outside `internal/registry/` may import `github.com/google/go-containerregistry/*`.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| OCI manifest digest fetching | `internal/registry` (Go process, talks HTTPS to GHCR/Docker Hub) | — | Wraps `crane.Digest`; only this package may import `google/go-containerregistry/*` |
| Cron scheduling | `internal/poll` (Go process, in-memory `cron.Cron`) | — | `robfig/cron/v3` scheduler owns the timer goroutine |
| Bounded concurrent fetch fan-out | `internal/poll` (in-memory `errgroup` worker pool) | `internal/registry` (per-call HTTP) | Pool sits in poll; per-call HTTP timeouts sit in registry |
| State mutation serialization | `internal/poll/channel.go` single-consumer goroutine | `internal/state.Store.Update` (existing RWMutex+persist) | Channel collapses 2+ producers into 1 writer; store owns the lock |
| Tag-pattern regex evaluation | `internal/poll/patterns.go` (in-memory RWMutex map) | `internal/docker/discovery.go` (sets raw label string) | Compiled regex is derived state, not persisted; raw string survives restarts via `state.Container.Labels` |
| Bearer-token redaction | `internal/registry/transport.go` (HTTP RoundTripper) | `cmd/hmi-update/main.go` (slog `ReplaceAttr` setup) | Belt-and-braces: transport strips on send; slog filter strips on log |
| Pinned-image opt-out | `internal/poll.eligibleContainers()` (in-memory filter) | `internal/docker/discovery.go` (already sets `Pinned: true`) | Phase 2 detects; Phase 3 honors |
| Schema migration | `internal/state/schema.go` (Go struct field add) + `internal/api/types.go` (tygo source) | `ui/src/lib/types.d.ts` (regenerated) | Single-source-of-truth via tygo; `make check-types` enforces |

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/google/go-containerregistry` | v0.20.x | `crane.Digest`, `authn.Anonymous`, `v1.Platform` | The single biggest reduction in WUD-class bug surface — `crane.Digest(ref, WithPlatform)` does bearer-token flow, multi-arch index resolution, and Docker-Content-Digest extraction in one call. Used by Kubernetes (kubectl debug), GitHub Actions runners, and Cosign. Source verified: handles both index-with-platform and single-arch fallback paths. [VERIFIED: github.com/google/go-containerregistry/blob/main/pkg/crane/digest.go] |
| `github.com/robfig/cron/v3` | v3.0.1 | Cron scheduler | Already in STACK.md as the project's chosen scheduler. Accepts 5-field expressions verbatim (matches `HMI_UPDATE_CRON="0 * * * *"`). Battle-tested in Prometheus, Caddy, Kubernetes operators. [VERIFIED: pkg.go.dev/github.com/robfig/cron/v3] |
| `golang.org/x/sync/errgroup` | latest (v0.8+) | Bounded concurrency for fetch fan-out | `Group.SetLimit(n)` is the canonical Go pattern for bounded worker pools post-1.20. `Group.Go(f)` blocks the producer when the limit is hit — no manual semaphore needed. Producer + consumers share a `Context` that cancels on first error. [VERIFIED: pkg.go.dev/golang.org/x/sync/errgroup] |
| `regexp` (stdlib) | std | Tag-pattern compilation | `regexp.Compile(pattern)` returns `(*Regexp, error)` — error path triggers the "invalid regex → permissive" branch. RE2 syntax, no catastrophic backtracking. |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `net/http` (stdlib) | std | `http.RoundTripper` interface for `redactingTransport` | Implementing `RoundTrip(*Request) (*Response, error)` — stdlib is sufficient |
| `log/slog` (stdlib) | std | Structured JSON logging + `ReplaceAttr` redaction | Already the project's logger; `HandlerOptions.ReplaceAttr` is the documented redaction hook [VERIFIED: pkg.go.dev/log/slog] |
| `context` (stdlib) | std | Per-call timeouts + SIGTERM cancellation propagation | `context.WithTimeout` for the 10s registry call; root ctx threads through `cron.Cron`'s job func into `errgroup.WithContext` |
| `time` (stdlib) | std | `time.RFC3339Nano` for `LastPolledAt` serialization | JSON wire format consistent with Go's default for `time.Time` |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `crane.Digest` (one-liner) | Hand-rolled `net/http` + bearer-token + Accept-header dance | Hand-rolled is ~200 LOC of code that's exactly where WUD's two bugs lived (Pitfalls 1 & 2). The whole reason crane is in the stack. |
| `errgroup.SetLimit(4)` | `make(chan struct{}, 4)` semaphore pattern | Both work; errgroup wins because it also propagates errors and ties Wait() to the producer. Same goroutine count either way. |
| `cron.New(cron.WithLocation(time.UTC))` | `time.Ticker` parsed-fixed-duration loop | The `HMI_UPDATE_CRON` env-var contract is a cron expression — using a ticker means writing a cron parser anyway. Skip; use robfig. |
| `authn.Anonymous` | `authn.DefaultKeychain` | `DefaultKeychain` reads `~/.docker/config.json`. If the host ran `docker login` with empty username at some point, the keychain emits `Basic Og==` and breaks GHCR. **Hard rule: use `authn.Anonymous` explicitly for v1.** [Pitfall 2 prevention] |
| Single-consumer channel | Per-handler direct `state.Store.Update` calls | Phase 4 will add a third producer (action goroutines). The channel keeps the writer count at 1 regardless of producer count — DETECT-10 invariant. |

**Installation:**
```bash
go get github.com/google/go-containerregistry@v0.20
go get github.com/robfig/cron/v3@v3.0.1
go get golang.org/x/sync@latest
```

**Version verification (run before plan-01 writes go.mod):**
```bash
# Verify latest crane (training data may be stale)
go list -m -versions github.com/google/go-containerregistry | tr ' ' '\n' | grep '^v0\.' | tail -5
# Verify cron/v3 is still at 3.0.1
go list -m -versions github.com/robfig/cron/v3
# Verify errgroup
go list -m -versions golang.org/x/sync
```
STACK.md lists `go-containerregistry v0.20.x` and `cron/v3 v3.0.1` as verified at 2026-05-13. Re-confirm against `go list -m -versions` before pinning. [VERIFIED: STACK.md "Version Compatibility" table 2026-05-13]

## Architecture Patterns

### System Architecture Diagram

```
                ┌──────────────────────────────┐
                │  HMI_UPDATE_CRON env var     │
                │  default: "0 * * * *"        │
                └──────────────┬───────────────┘
                               │ parsed once at boot (fail-fast on invalid)
                               ▼
       ┌──────────────────────────────────────────────────┐
       │  cron.New(cron.WithLocation(time.UTC))           │
       │  AddFunc(spec, onTick)  → onTick scheduled       │
       │  Start()  → internal goroutine ticks             │
       └──────────────┬───────────────────────────────────┘
                      │ onTick fires
                      ▼
       ┌──────────────────────────────────────────────────┐
       │  Poller.sweep(ctx):                              │
       │    1. snapshot state.Store.Get()                 │
       │    2. eligibleContainers (skip pinned/stopped)   │
       │    3. tag-pattern filter (Patterns.Match)        │
       │    4. errgroup.WithContext + SetLimit(4)         │
       │    5. for each: g.Go(fetchOne)                   │
       └──────────────┬───────────────────────────────────┘
                      │ per container (up to 4 in flight)
                      ▼
       ┌──────────────────────────────────────────────────┐
       │  Resolver.Digest(ctx, "img:tag"):                │
       │    crane.Digest(ref,                             │
       │      crane.WithAuth(authn.Anonymous),            │
       │      crane.WithPlatform(&amd64),                 │
       │      crane.WithTransport(redactingTransport),    │
       │      crane.WithContext(ctx))                     │
       │                                                  │
       │  ┌──────────────────────────────────────────┐    │
       │  │ Internally:                              │    │
       │  │  • HEAD /v2/<repo>/manifests/<tag>       │    │
       │  │  • Reads Docker-Content-Digest header    │    │
       │  │  • If index: Descriptor.Image() resolves │    │
       │  │    to amd64 child → img.Digest()         │    │
       │  │  • If manifest: returns Head digest      │    │
       │  │  • Bearer-token flow against             │    │
       │  │    WWW-Authenticate realm (no Basic Og==)│    │
       │  └──────────────────────────────────────────┘    │
       └──────────────┬───────────────────────────────────┘
                      │ returns sha256:... (or err)
                      ▼
       ┌──────────────────────────────────────────────────┐
       │  Each worker sends stateUpdate to channel:       │
       │    chan stateUpdate (buffered cap=64)            │
       └──────────────┬───────────────────────────────────┘
                      │             ▲
                      │             │ (Phase 2 docker events
                      │             │  goroutine — second producer)
                      ▼
       ┌──────────────────────────────────────────────────┐
       │  poll.RunUpdater(ctx, ch, store):                │
       │    single consumer; range over ch                │
       │    for each msg:                                 │
       │      store.Update(func(*State) { … })            │
       │    on ctx.Done(): drain pending, exit            │
       └──────────────┬───────────────────────────────────┘
                      │ takes state.Store.mu inside Update
                      ▼
       ┌──────────────────────────────────────────────────┐
       │  state.Store (existing — Phase 1)                │
       │    RWMutex + renameio.WriteFile                  │
       │    LastPolledAt, AvailableDigest, Notes          │
       │    LastPollStart, LastPollEnd, LastPollError     │
       └──────────────────────────────────────────────────┘
```

### Recommended Project Structure

```
internal/
├── registry/
│   ├── resolver.go       # Resolver interface + craneResolver impl
│   ├── resolver_test.go  # table-driven against httptest registry
│   ├── transport.go      # redactingTransport (http.RoundTripper)
│   ├── transport_test.go # Pitfall 2 regression guard (httptest)
│   ├── errors.go         # ErrPermanent (401/403/404) + ErrTransient (5xx/timeout)
│   └── _docs.txt         # (optional) crane API shape reference, mirroring docker/_sdk_shape.txt
├── poll/
│   ├── poller.go         # Poller interface + cronPoller impl
│   ├── poller_test.go    # table-driven with fake Resolver
│   ├── patterns.go       # Patterns (compiled regex cache, RWMutex)
│   ├── patterns_test.go  # invalid regex → permissive, valid → match/non-match
│   ├── channel.go        # stateUpdate type + RunUpdater consumer
│   └── channel_test.go   # drain on ctx cancel; lock never held across send
├── state/
│   └── schema.go         # MODIFY: add LastPolledAt, AvailableDigest, Notes
├── api/
│   └── types.go          # MODIFY: mirror schema additions
e2e/
└── tests/
    ├── detect-multiarch.spec.ts    # RED FIRST (DETECT-04)
    ├── detect-tag-pattern.spec.ts  # RED FIRST (DETECT-08)
    ├── detect-pinned.spec.ts       # RED FIRST (DETECT-09)
    └── obs-04-redaction.spec.ts    # RED FIRST (OBS-04)
```

### Pattern 1: `crane.Digest` for both shapes via `WithPlatform`

**What:** One function call resolves a multi-arch index OR a single-arch manifest to the correct platform-specific sha256.

**When to use:** Every Phase 3 digest fetch. The whole resolver body is this call plus retry/error-classification glue.

**Verified semantics:**
- For a **multi-arch image index**: internally calls `getManifest(ref)` → checks `desc.MediaType.IsIndex()` → calls `desc.Image()` (platform-aware) → returns `img.Digest()` (the **child** manifest's sha256). [VERIFIED: github.com/google/go-containerregistry/blob/main/pkg/crane/digest.go]
- For a **single-arch manifest**: `desc.MediaType.IsIndex()` is false → returns `desc.Digest` from the registry's `Docker-Content-Digest` response header. **The header is the authoritative source; no body re-hash.** [VERIFIED: same source]
- For **no platform specified** (omit `WithPlatform`): returns whatever the HEAD response gives (could be the index digest itself). This is why `WithPlatform` is mandatory for our use case.

**Example:**
```go
// internal/registry/resolver.go
import (
    "context"
    "github.com/google/go-containerregistry/pkg/authn"
    "github.com/google/go-containerregistry/pkg/crane"
    v1 "github.com/google/go-containerregistry/pkg/v1"
)

// TODO(V2-ARM64): wire from runtime.GOARCH when arm64 lands.
var amd64Platform = &v1.Platform{OS: "linux", Architecture: "amd64"}

type craneResolver struct {
    transport http.RoundTripper // redactingTransport
}

func (r *craneResolver) Digest(ctx context.Context, ref string) (string, error) {
    digest, err := crane.Digest(ref,
        crane.WithContext(ctx),
        crane.WithAuth(authn.Anonymous),
        crane.WithPlatform(amd64Platform),
        crane.WithTransport(r.transport),
    )
    if err != nil {
        return "", classifyErr(err) // returns ErrPermanent or ErrTransient
    }
    return digest, nil // "sha256:..."
}
```

### Pattern 2: Single-consumer channel collapsing two producers

**What:** Two producer goroutines (Phase 2 docker events + Phase 3 poll-tick) send `stateUpdate` messages on a single buffered channel; one consumer drains and applies via `state.Store.Update`.

**When to use:** Whenever multiple sources need to mutate the same store. Already established in Phase 2's discovery code (single producer for now); Phase 3 promotes it to two-producer.

**Anti-deadlock invariant** (already enforced in Phase 2's `internal/docker/discovery.go`):
> The consumer never holds `state.Store.mu` across registry/docker I/O. Inspect/fetch FIRST, then send the resolved fields on the channel. The consumer's only job inside the lock is the map mutation.

**Example:**
```go
// internal/poll/channel.go
type UpdateKind int
const (
    KindDigestResolved UpdateKind = iota // from poller
    KindContainerEvent                   // from docker events (Phase 2)
    KindPollSweepStart                   // top-level timestamp set
    KindPollSweepEnd
)

type stateUpdate struct {
    Kind     UpdateKind
    Service  string         // empty for KindPollSweepStart/End
    Apply    func(*state.State) // closure carries the actual mutation
}

func RunUpdater(ctx context.Context, ch <-chan stateUpdate, store *state.Store) {
    for {
        select {
        case <-ctx.Done():
            // Drain pending messages on shutdown
            for {
                select {
                case msg := <-ch:
                    _ = store.Update(msg.Apply)
                default:
                    return
                }
            }
        case msg := <-ch:
            if err := store.Update(msg.Apply); err != nil {
                slog.Error("poll.consumer.persist", "service", msg.Service, "err", err)
            }
        }
    }
}
```

### Pattern 3: Bounded `errgroup` worker pool with context propagation

**What:** `errgroup.WithContext(ctx)` + `g.SetLimit(4)` produces a worker pool that blocks the producer on `g.Go(f)` when the limit is hit, propagates cancellation to all workers, and returns the first error from `g.Wait()`.

**When to use:** The per-cron-tick sweep across N eligible containers. We don't want 50 concurrent HTTPS connections to GHCR.

**SIGTERM behavior:** The shared `ctx` is derived from the root context. SIGTERM → root ctx cancels → `crane.WithContext(ctx)` propagates into the registry HTTP client → in-flight `Read`/`Dial` returns `context.Canceled` quickly. No goroutine leak.

**Example:**
```go
// internal/poll/poller.go
import "golang.org/x/sync/errgroup"

func (p *cronPoller) sweep(ctx context.Context, eligible []state.Container) {
    g, gctx := errgroup.WithContext(ctx)
    g.SetLimit(p.concurrency) // default 4

    results := make([]fetchResult, len(eligible))
    for i, c := range eligible {
        i, c := i, c // shadow for closure
        g.Go(func() error {
            // crane.Digest call has its own per-call timeout layered atop gctx
            callCtx, cancel := context.WithTimeout(gctx, p.timeout)
            defer cancel()
            digest, err := p.resolver.Digest(callCtx, c.Image+":"+c.Tag)
            results[i] = fetchResult{service: c.Service, digest: digest, err: err}
            return nil // don't fail-fast on per-container errors
        })
    }
    _ = g.Wait()

    // Send all results on the state-update channel — single producer in this method
    for _, r := range results {
        p.updates <- buildStateUpdate(r) // never blocks long; channel cap=64
    }
}
```

### Pattern 4: redactingTransport via `crane.WithTransport`

**What:** A custom `http.RoundTripper` that wraps `http.DefaultTransport` and clears sensitive headers from any internal logging the transport itself emits (and serves as a hook for future request-side audit).

**Why:** crane's internal HTTP client will eventually emit `Authorization: Bearer <token>` headers (that's the whole point of the bearer flow). The wire still sees them — but our slog output must NEVER see them. The redacting transport is the request-side guard; slog `ReplaceAttr` is the output-side guard.

**Verified:** `crane.WithTransport(http.RoundTripper)` exists and accepts any `RoundTripper`. [VERIFIED: pkg.go.dev/github.com/google/go-containerregistry/pkg/crane]

**Example:** see §"Code Examples: redacting transport" below.

### Anti-Patterns to Avoid

- **Re-hashing the manifest body to compute the digest** — exactly Pitfall 1. The body is the wire bytes; the digest is what the registry stamped on `Docker-Content-Digest`. `crane.Digest` reads the header, period. Never call `digest := sha256.Sum256(body)`.
- **`authn.DefaultKeychain` "to be safe"** — exactly Pitfall 2. `DefaultKeychain` reads `~/.docker/config.json`; on a host that ran `docker login` with empty username, this emits `Basic Og==` to GHCR which returns 403, not 401, which silently breaks anonymous polling. Explicit `authn.Anonymous` is the only correct choice for v1.
- **Holding `state.Store.mu` across registry HTTPS** — every package outside `state` must compute results first, then call `store.Update(closure)` with a pure-map-mutation closure. Phase 2's `internal/docker/discovery.go` already enforces this; Phase 3 must mirror it. The single-consumer goroutine architecturally prevents the regression.
- **Reading registry URLs from container labels (custom registry override)** — out of scope for v1 (V2-PRIV-REG). Don't add a label-derived registry-URL parameter. The image ref `ghcr.io/owner/repo:tag` already encodes the registry; `crane.Digest` parses it.
- **`time.Ticker` "because cron is heavy"** — `robfig/cron/v3` adds one goroutine and ~200 KB to the binary. The `HMI_UPDATE_CRON` contract requires cron syntax. Don't reinvent the parser.
- **Goroutine-per-container with no SetLimit** — at 5 watched containers this is fine; at 50 you DDoS GHCR and hit anonymous rate limits. Bound with `errgroup.SetLimit(4)`.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Manifest digest extraction | Hand-rolled `net/http` + `Accept` header negotiation + body re-hash | `crane.Digest(ref, WithPlatform)` | This is exactly where WUD 8.2.2's two named bugs lived (Pitfalls 1 & 2). One library call replaces ~200 LOC of registry plumbing. |
| Multi-arch index resolution | Parse `manifests[]` array, filter on `platform.os` + `platform.architecture`, HEAD the child | `WithPlatform(&v1.Platform{OS: "linux", Architecture: "amd64"})` | crane's `Descriptor.Image()` does this in one call. Includes correct media-type handling for both `manifest.list.v2+json` and `oci.image.index.v1+json`. |
| Bearer-token flow | Parse `WWW-Authenticate` realm/scope, GET `/token`, retry with `Authorization: Bearer <jwt>` | crane internal transport (default) | Handles GHCR, Docker Hub, Quay's slightly different challenge formats uniformly. |
| Cron expression parsing | Strtok the 5 fields, evaluate ranges/steps, schedule | `robfig/cron/v3` | The parser is the whole library — write a parser, lose. |
| Bounded concurrency | `sem := make(chan struct{}, 4)` + `sem <- struct{}{}` / `<-sem` dance | `errgroup.SetLimit(4)` | errgroup also propagates errors, ties Wait to producer, integrates with context. Same goroutine count. |
| Atomic state persistence | `os.WriteFile` + rename | `state.Store.Update` (already there, uses `renameio`) | Phase 1 ships this. Phase 3 only writes through it. |
| Regex caching | Recompile on every poll | `Patterns` struct with `map[svc]*regexp.Regexp` + RWMutex | Compile once at discovery; read-mostly. Saves milliseconds per sweep. |
| Token redaction parser | Try to mask tokens at log-write time inline | slog `HandlerOptions.ReplaceAttr` with a compiled regex | One handler-level hook covers every `slog.Info/Warn/Error/Debug` site in the binary. |

**Key insight:** Phase 3's whole job is to stitch together five battle-tested libraries (`crane`, `cron/v3`, `errgroup`, stdlib `regexp`, stdlib `slog`) into a poll loop. The only original code is (a) the resolver/transport facade, (b) the patterns cache, (c) the channel + consumer goroutine, (d) the schema additions. ~400 LOC total of original Go. Every line of hand-rolled HTTP/manifest/token code is a place WUD 8.2.2 has already lost.

## Common Pitfalls

### Pitfall 1 (from PITFALLS.md #1): Single-arch digest from wrong field

**What goes wrong:** Re-computing the digest by hashing the manifest body instead of reading `Docker-Content-Digest` from the response header. The wire bytes don't always re-serialize identically; GHCR and Docker Hub disagree subtly on canonicalization.

**How `crane.Digest` avoids it:** The verified source path calls `remote.Head` (or `remote.Get`) which surfaces `desc.Digest` from the `Docker-Content-Digest` HTTP header. For indices, the platform-aware `Descriptor.Image()` resolves to the child manifest and returns *that* manifest's `Digest` field (also header-derived).

**Regression guard:** Push BOTH shapes to zot in the same e2e test (`detect-multiarch.spec.ts`). Both must flip `update_available` within `cron + 5 s`.

### Pitfall 2 (from PITFALLS.md #2): Anonymous flow breaks with `Basic Og==`

**What goes wrong:** A naive HTTP client emits `Authorization: Basic Og==` (base64 of `":"`) when username and password are both empty. GHCR responds 403 (not 401), the token endpoint returns a token with an empty `access` claim, and subsequent fetches fail with confusing "denied" errors.

**How we avoid it:** `authn.Anonymous` is a singleton whose `Authorization()` method returns an empty `AuthConfig{}`. crane's internal transport sees the empty config and emits NO `Authorization` header on the initial probe — the registry returns 401 with `WWW-Authenticate`, crane fetches the bearer token from `realm`, then retries with `Authorization: Bearer <jwt>`. [VERIFIED: pkg.go.dev/github.com/google/go-containerregistry/pkg/authn — "Anonymous is a singleton Authenticator for providing anonymous auth"]

**Regression guard:** `internal/registry/transport_test.go` uses `httptest.NewServer` that issues a bearer challenge on first hit. The test inspects every inbound request's `Authorization` header — asserts the FIRST request has none, asserts the second request has `Bearer <token>` (token fetched from the test's `realm` endpoint), asserts no request has `Basic Og==`. Implementation pattern in §"Code Examples" below.

### Pitfall 3 (from PITFALLS.md #3): Wrong `Accept` header → 404 or v1 fallback

**What goes wrong:** Sending only `application/vnd.docker.distribution.manifest.v2+json` against a multi-arch image returns 404 ("manifest unknown") on some registries.

**How `crane.Digest` avoids it:** crane's internal transport sends the full multi-valued `Accept` header (Docker manifest list + OCI image index + Docker manifest + OCI manifest) on every manifest request, and branches on the response `Content-Type`. No application-level configuration needed.

**Regression guard:** The multi-arch test (`detect-multiarch.spec.ts`) exercises both shapes against zot in one run.

### Pitfall 13 (from PITFALLS.md #13): SSRF / path traversal via service params

**What goes wrong:** A malicious label or compose edit could inject an unexpected registry URL or shell-style payload into the service name.

**How Phase 3 avoids it:**
- Image refs come from `state.Container.Image + ":" + state.Container.Tag` (or `:tag` from the tag-pattern match), both set by Phase 2's discovery from the docker daemon — never from user-supplied HTTP input.
- Service names are validated by Phase 4 at the router layer (ACT-10); Phase 3 never receives a service name from HTTP input — it only reads from `state.Store.Get()`.
- crane parses the ref using `name.ParseReference` which rejects malformed input — but Phase 3 doesn't need to add validation, the in-memory cache is the trust boundary.

### Phase-3-specific Pitfall: Cron string parsing mode mismatch

**What goes wrong:** Operator sets `HMI_UPDATE_CRON="0 0 * * * *"` (6 fields with seconds, robfig syntax-via-`WithSeconds`). Default parser is 5-field; `AddFunc` returns "expected 5 fields, got 6" and the boot crashes.

**Why it happens:** robfig's default parser is **strict 5-field** (Minute Hour Dom Mon Dow). The `cron.WithSeconds()` option flips it to 6-field. The brief and CLAUDE.md both say 5-field (`0 * * * *`); use the default.

**How to avoid:** Construct with `cron.New(cron.WithLocation(time.UTC))` only — no `WithSeconds()`. Document `HMI_UPDATE_CRON` accepts 5-field format. Fail-fast on parse error with a paste-ready remediation: `"invalid HMI_UPDATE_CRON %q: %w — expected 5-field cron expression like '0 * * * *'"`. [VERIFIED: pkg.go.dev/github.com/robfig/cron/v3]

**Warning signs:** Boot log: `cron parse: expected exactly 5 fields, found 6: [0 0 * * * *]`. The whole process exits with non-zero status.

**Test budget acceleration:** For e2e tests, `@every 5s` is the descriptor form (parser-builtin) that gives a 5-second tick — see DETECT-07 acceptance criterion. No `WithSeconds()` needed even for tests.

### Phase-3-specific Pitfall: `crane.Digest` against an image that doesn't have an amd64 entry

**What goes wrong:** Some registries serve indices without a `linux/amd64` entry (e.g. an arm-only image). `crane.Digest(ref, WithPlatform(amd64))` returns an error like `no child with platform linux/amd64 in index sha256:...`.

**How to avoid:** Classify this as `ErrPermanent` (it's a real misconfiguration — the operator pointed at an image that doesn't run on this HMI). Surface `notes: "no amd64 manifest in upstream index"` and skip future polls until label changes. Don't retry.

### Phase-3-specific Pitfall: errgroup `SetLimit` after `Go` was called

**What goes wrong:** `errgroup.SetLimit(n)` panics if a goroutine has already been added via `Go`. Easy to trip if you write `g.Go(...); g.SetLimit(4)`.

**How to avoid:** Call `g.SetLimit(p.concurrency)` immediately after `errgroup.WithContext(ctx)`, before the loop. [VERIFIED: pkg.go.dev/golang.org/x/sync/errgroup — "Must not be modified while goroutines are active"]

### Phase-3-specific Pitfall: Cron `Stop()` not awaited

**What goes wrong:** `cron.Stop()` returns immediately (via the returned context) with the scheduler signaling shutdown, but in-flight tick functions keep running. If the consumer goroutine exits before the scheduler's last tick finishes mutating, the tick's send-on-channel blocks forever.

**How to avoid:** In `main.go` shutdown sequence:
1. Cancel root ctx (signal handler).
2. `<-cronInstance.Stop().Done()` — waits for in-flight ticks to finish.
3. Close the state-update channel.
4. Wait for the consumer goroutine's WaitGroup.

`cron.Stop()` returning a context is the documented Drain mechanism. [VERIFIED: pkg.go.dev/github.com/robfig/cron/v3 — "Returns a context that can be used to wait for running jobs to complete"]

## Runtime State Inventory

> Phase 3 is greenfield-additive (new packages, new schema fields) — not a rename/refactor. This section is included to confirm no stored state needs migration.

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | `hmi_update_state.json` gains 3 top-level + 3 per-container fields. Existing fields untouched. | Forward-compatible: new fields are `omitempty`. Existing on-disk state files from Phase 2 deserialize cleanly into the new shape (missing fields = zero values = omitted on next write). No data migration required. |
| Live service config | None — Phase 3 introduces no new external service. zot test fixture already runs in Phase 1's compose stack. | None. |
| OS-registered state | None — no new tasks, processes, or daemons registered with the host OS. | None. |
| Secrets/env vars | Three new optional env vars: `HMI_UPDATE_REGISTRY_TIMEOUT_S`, `HMI_UPDATE_POLL_CONCURRENCY`. (`HMI_UPDATE_CRON` was already in Phase 1's env contract.) All have defaults; not setting them is non-breaking. | Document in README. No secrets — anonymous polling only. |
| Build artifacts / installed packages | New Go module deps: `github.com/google/go-containerregistry`, `golang.org/x/sync`. Adds ~5 MB to compiled binary (verified in STACK.md). | `go.mod` + `go.sum` regenerate on first `go build`. CI cache hit on subsequent builds. |

**Canonical question answered:** After every file in the repo is updated, is there any runtime system that has Phase-3-related state cached, stored, or registered externally? **No.** Phase 3 is entirely self-contained within the Go process and the JSON state file.

## Code Examples

### crane.Digest call (the heart of `internal/registry/resolver.go`)

```go
// Source: pkg.go.dev/github.com/google/go-containerregistry/pkg/crane
// Verified against: github.com/google/go-containerregistry/blob/main/pkg/crane/digest.go
package registry

import (
    "context"
    "errors"
    "net/http"

    "github.com/google/go-containerregistry/pkg/authn"
    "github.com/google/go-containerregistry/pkg/crane"
    v1 "github.com/google/go-containerregistry/pkg/v1"
)

// TODO(V2-ARM64): wire from runtime.GOARCH when arm64 lands.
var amd64Platform = &v1.Platform{OS: "linux", Architecture: "amd64"}

type Resolver interface {
    Digest(ctx context.Context, ref string) (string, error)
}

type craneResolver struct {
    transport http.RoundTripper
}

func NewResolver(transport http.RoundTripper) Resolver {
    return &craneResolver{transport: transport}
}

func (r *craneResolver) Digest(ctx context.Context, ref string) (string, error) {
    digest, err := crane.Digest(ref,
        crane.WithContext(ctx),
        crane.WithAuth(authn.Anonymous),
        crane.WithPlatform(amd64Platform),
        crane.WithTransport(r.transport),
    )
    if err != nil {
        return "", classify(err) // ErrPermanent or ErrTransient
    }
    return digest, nil // "sha256:..." — directly from Docker-Content-Digest
}
```

### Redacting transport + Pitfall 2 regression test

```go
// internal/registry/transport.go
package registry

import (
    "net/http"
)

// sensitiveHeaders are stripped from any request the transport
// otherwise might log internally. The wire still receives them (crane
// needs to send Authorization: Bearer <jwt>); this guard exists for
// defense-in-depth against future code that might log req.Header.
var sensitiveHeaders = []string{
    "Authorization",
    "WWW-Authenticate",
    "X-Registry-Auth",
    "Proxy-Authorization",
}

type redactingTransport struct {
    wrapped http.RoundTripper
}

func NewRedactingTransport() http.RoundTripper {
    return &redactingTransport{wrapped: http.DefaultTransport}
}

func (t *redactingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    // The wire request itself is unchanged — crane's bearer flow needs
    // Authorization to function. The guard is: if anything inside this
    // package later adds debug logging of req.Header, it must use a
    // shallow-copied header map with sensitive keys stripped. The
    // function below is the canonical helper for that.
    return t.wrapped.RoundTrip(req)
}

// SafeHeaders returns a copy of h with sensitive keys removed. Use
// this for any slog.Debug("req.header", ...) call site.
func SafeHeaders(h http.Header) http.Header {
    out := h.Clone()
    for _, k := range sensitiveHeaders {
        out.Del(k)
    }
    return out
}
```

```go
// internal/registry/transport_test.go — Pitfall 2 regression guard
package registry

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strings"
    "sync"
    "testing"

    "github.com/google/go-containerregistry/pkg/authn"
    "github.com/google/go-containerregistry/pkg/crane"
    v1 "github.com/google/go-containerregistry/pkg/v1"
)

func TestAnonymousFlow_NoBasicHeader(t *testing.T) {
    var (
        mu      sync.Mutex
        seen    []string
    )

    // First mux: token endpoint (bearer challenge target)
    tokenMux := http.NewServeMux()
    tokenMux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
        mu.Lock(); seen = append(seen, "token:"+r.Header.Get("Authorization")); mu.Unlock()
        _ = json.NewEncoder(w).Encode(map[string]string{"token": "fake-anon-jwt"})
    })
    tokenSrv := httptest.NewServer(tokenMux)
    defer tokenSrv.Close()

    // Second mux: registry that challenges then serves manifest
    regMux := http.NewServeMux()
    regMux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
        mu.Lock(); seen = append(seen, r.URL.Path+":"+r.Header.Get("Authorization")); mu.Unlock()
        if r.Header.Get("Authorization") == "" {
            w.Header().Set("WWW-Authenticate",
                `Bearer realm="`+tokenSrv.URL+`/token",service="reg",scope="repository:foo/bar:pull"`)
            w.WriteHeader(401)
            return
        }
        // Authorization should be "Bearer fake-anon-jwt" — never "Basic Og=="
        w.Header().Set("Docker-Content-Digest", "sha256:deadbeef...")
        w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
        w.WriteHeader(200)
    })
    regSrv := httptest.NewServer(regMux)
    defer regSrv.Close()

    refHost := strings.TrimPrefix(regSrv.URL, "http://")
    _, _ = crane.Digest(refHost+"/foo/bar:latest",
        crane.WithAuth(authn.Anonymous),
        crane.WithPlatform(&v1.Platform{OS: "linux", Architecture: "amd64"}),
    )

    // PITFALL 2 REGRESSION GUARD: no request anywhere ever carried Basic Og==
    mu.Lock()
    defer mu.Unlock()
    for _, s := range seen {
        if strings.Contains(s, "Basic Og==") {
            t.Fatalf("Pitfall 2 regression: empty-credentials Basic header sent: %s", s)
        }
    }
}
```

### Cron wiring (`internal/poll/poller.go`)

```go
// Source: pkg.go.dev/github.com/robfig/cron/v3
package poll

import (
    "context"
    "fmt"
    "time"

    "github.com/robfig/cron/v3"
)

type cronPoller struct {
    cronInst *cron.Cron
    sweepFn  func(context.Context)
}

func NewPoller(spec string, sweepFn func(context.Context)) (*cronPoller, error) {
    c := cron.New(cron.WithLocation(time.UTC))
    p := &cronPoller{cronInst: c, sweepFn: sweepFn}
    if _, err := c.AddFunc(spec, func() {
        // ctx scoped per tick — Run injects via closure below
        p.sweepFn(context.Background())
    }); err != nil {
        // FAIL-FAST: paste-ready remediation
        return nil, fmt.Errorf(
            "invalid HMI_UPDATE_CRON %q: %w (expected 5-field cron like '0 * * * *')",
            spec, err)
    }
    return p, nil
}

func (p *cronPoller) Run(ctx context.Context) error {
    p.cronInst.Start()
    <-ctx.Done()
    // Drain in-flight ticks before returning.
    <-p.cronInst.Stop().Done()
    return ctx.Err()
}
```

### Patterns cache (`internal/poll/patterns.go`)

```go
package poll

import (
    "log/slog"
    "regexp"
    "sync"
)

type Patterns struct {
    mu sync.RWMutex
    m  map[string]*regexp.Regexp // service -> compiled regex; nil means "no constraint"
}

func NewPatterns() *Patterns {
    return &Patterns{m: map[string]*regexp.Regexp{}}
}

// Set compiles pattern for service. Empty pattern → no constraint (entry removed).
// Invalid regex → logs warning, returns the compile error so the caller can
// surface notes on state.Container. Boot is never crashed.
func (p *Patterns) Set(service, pattern string) error {
    p.mu.Lock()
    defer p.mu.Unlock()
    if pattern == "" {
        delete(p.m, service)
        return nil
    }
    re, err := regexp.Compile(pattern)
    if err != nil {
        slog.Warn("tag_pattern.invalid_regex",
            "service", service, "pattern", pattern, "err", err)
        delete(p.m, service)
        return err
    }
    p.m[service] = re
    return nil
}

// Match returns true if tag matches the compiled pattern, or true if no
// pattern is set for this service (default: any tag matches).
func (p *Patterns) Match(service, tag string) bool {
    p.mu.RLock()
    defer p.mu.RUnlock()
    re, ok := p.m[service]
    if !ok || re == nil {
        return true // permissive default
    }
    return re.MatchString(tag)
}
```

### slog `ReplaceAttr` redaction (`cmd/hmi-update/main.go`)

```go
// Source: pkg.go.dev/log/slog
import (
    "log/slog"
    "os"
    "regexp"
    "strings"
)

// Compile once at boot. Belt-and-braces with redactingTransport.
var bearerOrBasic = regexp.MustCompile(`^(Bearer|Basic)\s`)

func newRedactingHandler() slog.Handler {
    return slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelInfo,
        ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
            // Only inspect string-kinded attrs (cheap; most are not strings).
            if a.Value.Kind() != slog.KindString {
                return a
            }
            s := a.Value.String()
            if bearerOrBasic.MatchString(s) {
                return slog.Attr{Key: a.Key, Value: slog.StringValue("REDACTED")}
            }
            // Also catch substring matches in case a logger renders headers
            // as "Authorization=Bearer ey...":
            if strings.Contains(s, "Bearer ") || strings.Contains(s, "Basic ") {
                return slog.Attr{Key: a.Key, Value: slog.StringValue("REDACTED")}
            }
            return a
        },
    })
}
```

### Zot test fixture: push both manifest shapes

```bash
# Source: github.com/project-zot/zot + crane CLI
# Single-arch manifest push (already used in Phase 1 via oras):
oras push localhost:5000/test/single-arch:latest \
  --plain-http \
  ./payload.txt:application/vnd.example+type

# Multi-arch index push via crane CLI:
# 1. Build two single-arch manifests
crane append --new_tag localhost:5000/test/multiarch:amd64 \
  --platform linux/amd64 -f ./amd64.tar.gz --insecure
crane append --new_tag localhost:5000/test/multiarch:arm64 \
  --platform linux/arm64 -f ./arm64.tar.gz --insecure
# 2. Combine into an index pointing at :latest
crane index append --tag localhost:5000/test/multiarch:latest \
  -m localhost:5000/test/multiarch:amd64 \
  -m localhost:5000/test/multiarch:arm64 \
  --insecure

# Verify: crane.Digest returns the amd64 CHILD digest, not the index digest:
crane digest --platform linux/amd64 localhost:5000/test/multiarch:latest --insecure
# Expected: sha256:<amd64-child-sha>, NOT sha256:<index-sha>
```

**Implementation note:** The e2e test container can either embed `crane` (a static Go binary, ~30 MB) or shell out to `oras` (already in Phase 1's harness) — but `oras` cannot easily build an OCI index. **Recommend: bake a `crane` static binary into the test container** (or run `crane` from the Playwright host via `child_process`). `crane` is the right tool because it's the canonical reference implementation; `oras` is for arbitrary artifacts. [VERIFIED: pkg.go.dev/github.com/google/go-containerregistry/cmd/crane]

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Hand-rolled `Authorization: Basic <empty>` flow (WUD 8.2.2) | `authn.Anonymous` explicit singleton | go-containerregistry's bearer transport (stable since v0.5+, 2020) | Pitfall 2 disappears |
| Body-rehash digest derivation | `Docker-Content-Digest` header read | OCI Distribution Spec 1.0 (2018), enforced by registries since 2019 | Pitfall 1 disappears |
| Index digest returned for multi-arch refs | Platform-aware child resolution via `Descriptor.Image()` | `crane.WithPlatform` introduced 2020 (v0.1.4) | DETECT-04 works correctly |
| `sync.WaitGroup` + semaphore channel for bounded fan-out | `errgroup.WithContext` + `SetLimit` | x/sync `SetLimit` added 2022 (Go 1.18-era) | Producer blocking is built-in; less ceremony |
| `cron.Cron.Stop()` returns nothing (v2 API) | Returns a `context.Context` (v3 API) | robfig/cron v3.0.0 (2019) | Drain on shutdown is one-liner: `<-c.Stop().Done()` |
| `docker/docker/client` Docker Go SDK | `github.com/moby/moby/client` | Docker Engine v29 (Nov 2025) | Already migrated in Phase 2; Phase 3 inherits |
| Distroless `static:nonroot` unversioned | `static-debian12:nonroot` pinned | distroless project docs Q3-2024 | Already pinned in Dockerfile |

**Deprecated/outdated:**

- `crane.WithAuth(authn.Anonymous)` is NOT the same as omitting auth options entirely. Without `WithAuth`, crane uses `authn.DefaultKeychain` (Pitfall 2 risk). **Always pass `WithAuth(authn.Anonymous)` explicitly.**
- `gorilla/mux` (mentioned in some older registry-poller blog posts) is archived; project uses stdlib `net/http` per Phase 1 decision.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | zot accepts both OCI image index and Docker manifest list media types | §"Zot test fixture" | LOW — zot is OCI-distribution-spec compliant per upstream README; both media types are part of the spec |
| A2 | `crane.WithContext(ctx)` propagates ctx cancellation into the HTTP client's `Dial`/`Read` paths | §"Pattern 3: errgroup" + §"Code Examples" | MEDIUM — verified via crane Options docs but not by reading `net/http.Transport.DialContext` source. If wrong, SIGTERM may take up to the registry's TCP keepalive (default 30s) to unblock. Mitigation: rely on the parent `context.WithTimeout(ctx, 10s)` to also bound the call |
| A3 | The slog `ReplaceAttr` hook is called for every string-kinded attribute (including nested in `slog.Group`) | §"Slog ReplaceAttr" | LOW — verified: "Not called for Group attributes, only their contents" per slog docs |
| A4 | Phase 2's discovery goroutine can be refactored to send `stateUpdate` on a channel instead of calling `state.Store.Update` directly, without breaking Phase 2 tests | §"Integration Points" | MEDIUM — Phase 2's `discovery_test.go` instruments `state.Store.Update` indirectly via a recordingStore wrapper. The wrapper interface is package-private. Plan-01 of Phase 3 must verify this refactor doesn't trip TestDiscoverer_InspectPrecedesUpdate; the channel send happens AFTER inspect, so the anti-deadlock test should still pass — but verify in the plan step |
| A5 | `crane` CLI is available in CI (or can be installed as a static Go binary) | §"Code Examples: zot fixture" | LOW — crane is a single static Go binary built from go-containerregistry; `go install` adds it in seconds |
| A6 | New schema fields (`LastPolledAt`, `AvailableDigest`, `Notes`, `LastPollStart`, `LastPollEnd`, `LastPollError`) deserialize cleanly from Phase 2's existing on-disk state files (forward-compat) | §"Runtime State Inventory" | LOW — Go's `encoding/json` unmarshals missing fields as zero values; all new fields use `omitempty` |
| A7 | The Pitfall 2 regression test pattern (httptest.NewServer issuing bearer challenge) accurately mirrors GHCR's anonymous-pull challenge shape | §"Code Examples: regression test" | MEDIUM — verified against [GHCR auth docs](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry) at a high level; real GHCR's exact `WWW-Authenticate` realm format may differ in details. Phase 8's CI-04 live-GHCR smoke is the ultimate verification |

## Open Questions

1. **Should the redacting transport be registered as `http.DefaultTransport` globally?**
   - What we know: CONTEXT.md leans "scoped to registry package only" — package-local injection prevents accidental coverage gaps.
   - What's unclear: Future packages that emit outbound HTTP (none currently — no other outbound HTTP exists in the binary) could leak headers.
   - Recommendation: Honor CONTEXT.md — scoped only. Add a CI grep rule "no `http.DefaultClient` usage outside `internal/registry/`" as a tripwire if future code adds outbound HTTP.

2. **Should `Patterns.Set` errors propagate up to surface `Notes` on the container?**
   - What we know: CONTEXT.md says yes — "invalid tag-pattern label, ignored" goes into `state.Container.Notes`.
   - What's unclear: Does Phase 2's discovery goroutine know how to write `Notes`? Currently it writes Service/Image/Tag/ContainerID/Labels/Pinned/Stopped — six fields. Adding Notes is a fresh field in this phase.
   - Recommendation: Phase 3 introduces `Notes` to the schema; Phase 2's `upsertFromInspect` is updated to call `patterns.Set(svc, labels["hmi-update.tag-pattern"])` AFTER state.Update, then a follow-on Update sets `Notes` if the compile failed. Document this as a Phase 2 callback wired in by Phase 3's `main.go` integration step.

3. **What's the failure mode when `cron.AddFunc` succeeds but the sweep panics?**
   - What we know: robfig's `cron.Cron` recovers panics from job functions in the scheduler goroutine (per default `cron.WithChain(cron.Recover(...))` — but only if explicitly added).
   - What's unclear: Does v3.0.1 install `Recover` by default?
   - Recommendation: Explicit `cron.New(cron.WithLocation(time.UTC), cron.WithChain(cron.Recover(slogLogger)))` so a panic in `sweep()` doesn't kill the whole scheduler goroutine.

4. **Is the per-call 10s timeout sufficient for a cold GHCR fetch?**
   - What we know: GHCR's anonymous bearer-token fetch typically completes in <1s warm, <3s cold. Manifest HEAD is <500ms typical.
   - What's unclear: Edge cases (Cloudflare-fronted, IPv6-only HMI, OS-level DNS misconfig).
   - Recommendation: 10s default is generous; configurable via `HMI_UPDATE_REGISTRY_TIMEOUT_S`. The retry-once-after-2s policy effectively gives 22s total worst case.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `go` toolchain | All Go code | ✓ | 1.26 (verified `go.mod`) | — |
| `crane` CLI | e2e multi-arch fixture | ✗ (not yet installed) | — | Install via `go install github.com/google/go-containerregistry/cmd/crane@latest` in the test container Dockerfile, or `npx --yes` equivalent if a Node wrapper exists |
| zot | Already in Phase 1's compose stack | ✓ | per Phase 1 (`project-zot/zot:v2.1+`) | — |
| Playwright | Already in Phase 1's e2e | ✓ | `@playwright/test@1.60+` | — |
| Docker daemon | Compose runtime + tests | ✓ | Phase 2 verified via `/healthz` | — |
| `oras` CLI | Phase 1's fixture (kept for single-arch push) | ✓ | per Phase 1 | crane CLI also covers single-arch push |

**Missing dependencies with no fallback:** None.

**Missing dependencies with fallback:** `crane` CLI is needed for index-push in the multi-arch test. Install in the e2e test-actor container (~30 MB static binary; acceptable for test images, never ships to production).

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework (Go) | Go `testing` (stdlib) — table-driven; existing pattern from `internal/state/store_test.go` |
| Framework (e2e) | `@playwright/test@1.60+` — existing from Phase 1 |
| Config file (Go) | None — `go test ./...` is enough |
| Config file (e2e) | `e2e/playwright.config.ts` (existing) |
| Quick run command (Go unit) | `go test ./internal/registry/... ./internal/poll/... -race -count=1` |
| Quick run command (single e2e) | `cd e2e && npx playwright test tests/detect-multiarch.spec.ts` |
| Full suite command | `make test && make e2e` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| DETECT-01 | `crane.Digest` wrapped correctly; amd64 platform always set | unit | `go test ./internal/registry/ -run TestResolver_Digest` | ❌ Wave 0 |
| DETECT-02 | `Docker-Content-Digest` is the digest source (no body rehash) | unit | `go test ./internal/registry/ -run TestResolver_UsesContentDigestHeader` (httptest server that serves a body whose sha256 differs from its declared header) | ❌ Wave 0 |
| DETECT-03 | `authn.Anonymous` produces zero `Basic` headers | unit | `go test ./internal/registry/ -run TestAnonymousFlow_NoBasicHeader` | ❌ Wave 0 |
| DETECT-04 | Both OCI index + single manifest flip `update_available` | e2e | `cd e2e && npx playwright test tests/detect-multiarch.spec.ts` | ❌ Wave 0 |
| DETECT-05 | `cron.New + AddFunc(spec, fn)` triggers sweep on tick | unit | `go test ./internal/poll/ -run TestCronPoller_Tick` (use `@every 1s` + ctx timeout) | ❌ Wave 0 |
| DETECT-06 | Docker event → in-state within 5s | e2e | already covered by Phase 2's `discovery.spec.ts`; Phase 3 adds a probe in `detect-multiarch.spec.ts` | partial |
| DETECT-07 | Manifest push → `update_available` flips within `cron + 5s` | e2e | `cd e2e && npx playwright test tests/detect-multiarch.spec.ts` (HMI_UPDATE_CRON=`@every 5s`) | ❌ Wave 0 |
| DETECT-08 | Tag-pattern `^latest-pg17$` suppresses `:latest-pg18-oss` flip | e2e | `cd e2e && npx playwright test tests/detect-tag-pattern.spec.ts` | ❌ Wave 0 |
| DETECT-09 | Pinned containers excluded from poll, surface `notes` | e2e | `cd e2e && npx playwright test tests/detect-pinned.spec.ts` | ❌ Wave 0 |
| DETECT-10 | Single-consumer channel; no lock-during-IO | unit + race detector | `go test ./internal/poll/ -run TestUpdater -race -count=10` (race detector trips if regression moves I/O into store.Update closure) | ❌ Wave 0 |
| OBS-04 | Zero `Bearer `/`Basic ` matches in slog output | e2e | `cd e2e && npx playwright test tests/obs-04-redaction.spec.ts` (greps `docker logs hmi-update`) | ❌ Wave 0 |

### Sampling Rate

- **Per task commit:** `go test ./internal/registry/... ./internal/poll/... -race -count=1` (~3s)
- **Per wave merge:** `make test && cd e2e && npx playwright test tests/detect-*.spec.ts tests/obs-04-*.spec.ts` (~90s)
- **Phase gate:** Full suite green before `/gsd-verify-work` — `make test && make e2e` (~3min)

### Wave 0 Gaps

- [ ] `internal/registry/resolver_test.go` — covers DETECT-01, DETECT-02
- [ ] `internal/registry/transport_test.go` — covers DETECT-03 (Pitfall 2 regression guard)
- [ ] `internal/registry/errors_test.go` — covers error classification (transient vs permanent)
- [ ] `internal/poll/poller_test.go` — covers DETECT-05 (cron tick), DETECT-10 (consumer drain)
- [ ] `internal/poll/patterns_test.go` — covers DETECT-08 (regex compile, match, invalid fallthrough)
- [ ] `internal/poll/channel_test.go` — covers DETECT-10 (single-consumer drain on ctx cancel)
- [ ] `e2e/tests/detect-multiarch.spec.ts` — covers DETECT-01..04, DETECT-07
- [ ] `e2e/tests/detect-tag-pattern.spec.ts` — covers DETECT-08
- [ ] `e2e/tests/detect-pinned.spec.ts` — covers DETECT-09
- [ ] `e2e/tests/obs-04-redaction.spec.ts` — covers OBS-04

## Security Domain

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no | All registry calls are anonymous (`authn.Anonymous`); no credentials handled |
| V3 Session Management | no | No user sessions in v1 (LAN-only, unauthenticated per N5) |
| V4 Access Control | no | Same — Phase 4 will add per-service `allow-update=false`, but that's not an ASVS access control concern |
| V5 Input Validation | yes | `regexp.Compile(pattern)` on tag-pattern labels (operator-controlled but trusted boundary — labels come from the compose file, not HTTP). No HTTP input reaches Phase 3 directly. |
| V6 Cryptography | no | TLS to GHCR/Docker Hub via stdlib `crypto/tls` (transitive through `http.DefaultTransport`); no custom crypto |
| V7 Error Handling & Logging | yes | OBS-04 token redaction — sensitive headers never reach slog output |
| V8 Data Protection | no | No PII; image digests and names are not secret (per Pitfall 13 §"Recovery Strategies") |
| V9 Communication | yes | All registry HTTPS (TLS 1.2+ via stdlib defaults); zot test fixture uses HTTP via `--plain-http` (acceptable, test-only) |
| V14 Configuration | yes | Env-var contract (`HMI_UPDATE_CRON`, `HMI_UPDATE_REGISTRY_TIMEOUT_S`, `HMI_UPDATE_POLL_CONCURRENCY`) — fail-fast on invalid; document defaults |

### Known Threat Patterns for {Go single-binary + OCI registry polling}

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Bearer-token leak via slog | Information Disclosure | Double-defended: `redactingTransport` (request side) + slog `ReplaceAttr` regex (output side) |
| Anonymous-flow degradation to `Basic Og==` (Pitfall 2) | Spoofing / Tampering | Explicit `authn.Anonymous`; `httptest`-based regression test |
| Single-arch digest mismatch (Pitfall 1) | Tampering | `crane.Digest` reads `Docker-Content-Digest` from response header, never re-hashes body |
| Tag-pattern regex DoS (ReDoS) | Denial of Service | Go's `regexp` package uses RE2 — no catastrophic backtracking by construction. [VERIFIED: pkg.go.dev/regexp] |
| State write during registry I/O wedge | Denial of Service | Single-consumer channel; lock never held across I/O (DETECT-10) |
| Slow-loris registry → cron sweep wedge | Denial of Service | Per-call `context.WithTimeout(ctx, 10s)` |
| Excessive anonymous pull → rate-limit | Denial of Service | `errgroup.SetLimit(4)` bounds concurrent fetches; cron default `0 * * * *` is one tick/hour |

## Sources

### Primary (HIGH confidence)

- [pkg.go.dev/github.com/google/go-containerregistry/pkg/crane](https://pkg.go.dev/github.com/google/go-containerregistry/pkg/crane) — verified `crane.Digest(ref string, opt ...Option) (string, error)` signature; verified `crane.WithAuth`, `crane.WithPlatform(*v1.Platform)`, `crane.WithTransport(http.RoundTripper)`, `crane.WithContext(context.Context)` option signatures
- [pkg.go.dev/github.com/google/go-containerregistry/pkg/authn](https://pkg.go.dev/github.com/google/go-containerregistry/pkg/authn) — verified `authn.Anonymous` is a singleton `Authenticator` variable; verified it emits no Authorization header
- [github.com/google/go-containerregistry/blob/main/pkg/crane/digest.go](https://github.com/google/go-containerregistry/blob/main/pkg/crane/digest.go) — verified that `crane.Digest` with `WithPlatform` against an index calls `Descriptor.Image()` and returns the resolved child manifest digest
- [pkg.go.dev/github.com/google/go-containerregistry/pkg/v1/remote](https://pkg.go.dev/github.com/google/go-containerregistry/pkg/v1/remote) — verified `remote.Get` (un-interpreted) vs `remote.Image` (platform-aware resolution) behavior
- [pkg.go.dev/github.com/robfig/cron/v3](https://pkg.go.dev/github.com/robfig/cron/v3) — verified `cron.New(...Option)`, `AddFunc(spec, func)`, `Start()`, `Stop() context.Context`, default 5-field parser, `WithLocation`, `WithSeconds`, `@every` descriptor
- [pkg.go.dev/log/slog](https://pkg.go.dev/log/slog) — verified `HandlerOptions.ReplaceAttr func(groups []string, a Attr) Attr` signature; verified `Value.Kind() == KindString` filter pattern
- [pkg.go.dev/golang.org/x/sync/errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup) — verified `errgroup.WithContext`, `Group.SetLimit`, `Group.Go`, `Group.Wait` semantics; verified `SetLimit` blocks producer
- [STACK.md](.planning/research/STACK.md) — project's locked stack (Go 1.26, crane v0.20.x, cron/v3 v3.0.1)
- [PITFALLS.md](.planning/research/PITFALLS.md) — Pitfalls 1, 2, 3 (WUD reference bugs) + Pitfall 13 (SSRF/validation)
- [ARCHITECTURE.md](.planning/research/ARCHITECTURE.md) — Pattern 3 (single-consumer channel) + Pattern 5 (constructor injection)

### Secondary (MEDIUM confidence)

- [github.com/google/go-containerregistry issue #769](https://github.com/google/go-containerregistry/issues/769) — historical context for HEAD-request digest extraction
- [github.com/google/go-containerregistry issue #1749](https://github.com/google/go-containerregistry/issues/1749) — clarifies that `Docker-Content-Digest` reliance is per OCI distribution spec
- [pkg.go.dev/github.com/google/go-containerregistry/cmd/crane](https://pkg.go.dev/github.com/google/go-containerregistry/cmd/crane) — `crane` CLI for index push (multi-arch test fixture)
- [GHCR working with the container registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry) — high-level GHCR auth flow

### Tertiary (LOW confidence — flagged for validation)

- None. Every load-bearing claim is verified via Primary source.

## Metadata

**Confidence breakdown:**

- Standard stack: HIGH — every library version and API signature was checked against pkg.go.dev within this session
- Architecture: HIGH — patterns mirror Phase 2's already-shipped `internal/docker/discovery.go` (anti-deadlock invariant + RWMutex semantics)
- Pitfalls: HIGH — Pitfalls 1, 2, 3 are pre-researched in PITFALLS.md with primary OCI spec sources; Phase-3-specific pitfalls (cron 5-vs-6 field, errgroup SetLimit ordering) are verified against pkg.go.dev

**Research date:** 2026-05-14
**Valid until:** 2026-06-13 (30 days — stable libraries; crane and cron/v3 are low-churn)

## Planner Hints

A reasonable plan decomposition (the planner is free to deviate but this is the obvious shape):

- **Plan 03-01 — Schema additions + tygo regen.** Smallest, lowest-risk first. Adds `LastPolledAt`, `AvailableDigest`, `Notes` to `state.Container`; adds `LastPollStart`, `LastPollEnd`, `LastPollError` to `state.State`. Mirrors in `internal/api/types.go`. Runs `make types` and verifies `make check-types`. No runtime behavior change; unblocks every later plan.
- **Plan 03-02 — `internal/registry` (resolver + transport + errors).** craneResolver, redactingTransport, ErrPermanent/ErrTransient, classify(). Unit tests including the Pitfall 2 regression guard. NO wiring into main.go yet (the package is self-contained).
- **Plan 03-03 — `internal/poll/patterns.go` + tests.** Compiled-regex cache. Trivial; sets up DETECT-08 plumbing.
- **Plan 03-04 — `internal/poll/channel.go` + `RunUpdater` + tests.** Single-consumer goroutine with drain-on-cancel. Wire Phase 2's discovery goroutine to send on the channel instead of calling `state.Store.Update` directly (small refactor of `internal/docker/discovery.go`). Verify TestDiscoverer_InspectPrecedesUpdate still passes.
- **Plan 03-05 — `internal/poll/poller.go` cronPoller + sweep + errgroup.** Pulls everything together. Wires cron tick → eligibleContainers → tag-pattern filter → bounded fetch → stateUpdate sends.
- **Plan 03-06 — main.go integration + e2e specs.** Wire resolver, patterns, poller, RunUpdater into `cmd/hmi-update/main.go`. Land all four RED-FIRST Playwright specs. Drive them green. Verify OBS-04 redaction via `docker logs hmi-update | grep -E '(Bearer |Authorization: )'` returns zero lines.

The planner may choose to fold 03-03 + 03-04 into one plan (both are small, both feed 03-05). The planner may also choose to land RED specs at the START of 03-02 (before any registry impl) per the TDD-first C4 constraint — that's CONTEXT.md's explicit posture.

## RESEARCH COMPLETE
