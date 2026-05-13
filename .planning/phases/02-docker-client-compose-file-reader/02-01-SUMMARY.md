---
phase: 02-docker-client-compose-file-reader
plan: 01
subsystem: infra
tags: [go, moby, docker-client, tygo, state-schema, sdk-facade]

# Dependency graph
requires:
  - phase: 01-walking-skeleton-test-harness
    provides: internal/state.Store, internal/api.Container wire type, tygo source-of-truth pipeline, docker-stub Client interface

provides:
  - internal/docker.Client interface (6 methods — Ping, ContainerList, ContainerInspect, Events, ImagePull, ImageTag)
  - internal/docker.NewClient constructor over moby/moby/client v0.4.1
  - mobyClient concrete adapter — sole importer of github.com/moby/moby/client across the codebase
  - internal/docker/_sdk_shape.txt — durable SDK-shape capture (19 'go doc' sections + identifier index) backing the comm-23 drift gate
  - state.Container + api.Container schema bump (ContainerID, Labels, Pinned, Stopped) with regenerated ui/src/lib/types.d.ts
  - reflect-based method-count guard preventing silent Client interface growth (T-02-01-04)

affects: [02-02-compose-reader, 02-03-discovery-goroutine, 02-04-healthz-upgrade, 02-05-e2e-watched-container-spec, 04-actions]

# Tech tracking
tech-stack:
  added:
    - github.com/moby/moby/client v0.4.1 (pinned exactly — sub-1.0 semver per STACK.md)
    - github.com/moby/moby/api v1.54.2 (transitive — needed for api/types/{events,container})
    - 18 other transitive deps (containerd/errdefs, OTel, distribution/reference, etc.)
  patterns:
    - "Type-alias re-export: re-export SDK option/result types from internal/docker so callers never import github.com/moby/moby/* directly (boundary enforced by CI grep)."
    - "SDK shape capture artifact (B3 fix): every adapter file shipped with a sibling _sdk_shape.txt holding verbatim `go doc` output + an identifier index. CI's `comm -23` drift gate proves every client.Xxx referenced in source has a backing entry."
    - "Reflect-based interface-size guard: TestClient_InterfaceMethodCount fails if anyone adds a method without coordinated update — turns silent drift into a hard test failure."

key-files:
  created:
    - internal/docker/moby.go
    - internal/docker/moby_test.go
    - internal/docker/_sdk_shape.txt
  modified:
    - internal/docker/client.go (stub → 6-method interface + 9 type aliases)
    - internal/state/schema.go (+4 fields)
    - internal/api/types.go (+4 fields, byte-identical tags)
    - ui/src/lib/types.d.ts (regenerated via tygo)
    - go.mod / go.sum

key-decisions:
  - "Aliased container.Summary (not client.ContainerSummary) — the SDK reorganised result types into api/types subpackages around the docker/docker → moby/moby rename; the plan's skeleton referenced the legacy shape."
  - "Aliased client.ContainerInspectResult as ContainerInspect (wrapper struct {Container, Raw}) — preserves access to both typed fields and raw JSON for forward-compat without leaking the SDK type."
  - "Events facade unpacks EventsResult{Messages, Err} directly — no iterator-adapter goroutine needed (SDK is already channel-shaped); the plan's skeleton assumed an iterator translation."
  - "ImagePull returns ImagePullResponse as io.ReadCloser — ImagePullResponse embeds ReadCloser so the cast is free; callers retain access to JSONMessages/Wait via type assertion if needed."
  - "ImageTag facade flattens (ctx, ImageTagOptions{Source, Target}) → (ctx, src, dst) — two positional args are clearer than constructing a struct for a two-argument operation."
  - "Added identifier index suffix to _sdk_shape.txt — bare `go doc` output uses unqualified type names (e.g. 'ContainerListOptions') while source uses 'client.ContainerListOptions'; the index closes the form-mismatch gap for the comm-23 drift gate."
  - "All four new state.Container fields are omitempty (including booleans Pinned/Stopped) — keeps the wire payload clean for the 95% case of running non-pinned containers; the UI tests for presence, not for `=== false`."

patterns-established:
  - "Pattern: SDK boundary aliases — every external SDK gets a re-export wrapper package (here: internal/docker) so callers depend only on the wrapper. CI grep enforces."
  - "Pattern: durable shape capture — long-lived SDK adapters ship with a _sdk_shape.txt sibling and an in-source mirror block. Updates are atomic (regen + adapter edit in the same commit)."
  - "Pattern: reflect-based size guard for narrow interfaces — interfaces with deliberate method-count limits get a NumMethod() == N test. Drift becomes a test failure, not a code-review escape."

requirements-completed: [DOCK-01]

# Metrics
duration: ~5min
completed: 2026-05-13
---

# Phase 02 Plan 01: Docker Client Facade + State Schema Expansion Summary

**Six-method `internal/docker.Client` interface over moby/moby/client v0.4.1, sole-importer boundary enforced by CI grep, plus state.Container expansion (ContainerID, Labels, Pinned, Stopped) regenerated cleanly through tygo.**

## Performance

- **Duration:** ~5 min (commit timestamps: 9bb6ff8 → e411bb9)
- **Started:** 2026-05-13T20:44:00Z (approx — RED commit)
- **Completed:** 2026-05-13T20:48:00Z (approx — GREEN commit)
- **Tasks:** 1 (TDD task split into RED commit + GREEN commit)
- **Files modified:** 9 (3 created + 6 modified, see Files section)

## Accomplishments

- **Interface contract sealed:** `internal/docker.Client` lists exactly 6 methods (Ping, ContainerList, ContainerInspect, Events, ImagePull, ImageTag). A reflect-guard test fails the build if anyone silently adds a 7th. Phase 2/3/4 can now wire against this interface without coordinating on the surface.
- **moby/moby/client v0.4.1 adapter shipped:** `mobyClient` wraps `*client.Client` and is the only package in the codebase that imports `github.com/moby/moby/client`. Boundary enforced by a CI grep in the plan's verify command.
- **Durable SDK shape artifact (B3 fix):** `internal/docker/_sdk_shape.txt` captures 19 verbatim `go doc` sections + an identifier index that the `comm -23` drift gate consumes. Future SDK bumps regenerate the file in the same commit as the adapter edit; lockstep enforced.
- **state/api schema expansion landed atomically:** ContainerID, Labels, Pinned, Stopped fields added to both `state.Container` and `api.Container` with byte-identical json tags. `make check-types` regenerates `ui/src/lib/types.d.ts` cleanly — the wire/disk/TS contract stays consistent.
- **Threat T-02-01-03 (module-path spoofing) provably clean:** zero `github.com/docker/docker` entries in go.sum; only `github.com/moby/moby/{api,client}` appear. The deprecated module did not sneak in via any transitive dep.

## Task Commits

This was a TDD task; the plan executed as RED → GREEN per C4:

1. **Task 1 RED: failing tests + SDK shape capture** — `9bb6ff8` (test) — moby_test.go + _sdk_shape.txt + go.mod/go.sum landing the dep pin; tests fail to compile because mobyClient/NewClient are undefined.
2. **Task 1 GREEN: interface + adapter + schema expansion** — `e411bb9` (feat) — client.go (interface + aliases) + moby.go (adapter) + state/schema.go (+4 fields) + api/types.go (+4 fields) + ui/src/lib/types.d.ts (regen).

No REFACTOR commit — the GREEN code is already at the documented quality bar (doc comments, alias rationale, threat-model cross-refs).

## Files Created/Modified

**Created:**
- `internal/docker/moby.go` — mobyClient adapter (one function per Client method) + NewClient constructor wrapping client.NewClientWithOpts(FromEnv, WithAPIVersionNegotiation). Top of file embeds verbatim SDK shape mirror.
- `internal/docker/moby_test.go` — TDD tests: var _ Client = (*mobyClient)(nil) compile-time guard, error-wrap prefix tests, reflect-based method-count guard.
- `internal/docker/_sdk_shape.txt` — 786-line durable capture of moby/moby/client v0.4.1 SDK surface; consumed by the comm -23 drift gate.

**Modified:**
- `internal/docker/client.go` — empty stub interface replaced by Client + 9 type aliases (ContainerListOptions, ContainerSummary, ContainerInspect, ContainerInspectOptions, EventsListOptions, EventMessage, ImagePullOptions, PingOptions, Filters).
- `internal/state/schema.go` — Container struct gains ContainerID, Labels, Pinned, Stopped fields with documented set-sites and consumer mapping.
- `internal/api/types.go` — mirrors state.Container expansion byte-identically (verified via tygo regen + check-types).
- `ui/src/lib/types.d.ts` — regenerated; four new optional fields appear on Container TS interface.
- `go.mod` / `go.sum` — pins github.com/moby/moby/client v0.4.1 + transitive deps.

## Decisions Made

The plan's skeleton was written against the legacy `docker/docker/client` SDK shape (CONTEXT.md flagged this risk explicitly in `<interfaces>`). The live `moby/moby/client v0.4.1` surface differs in several ways; key adaptations:

1. **`client.ContainerListResult` wraps results in `{Items []container.Summary}`** — the facade unwraps to `[]ContainerSummary` so callers see a flat slice. Plan skeleton's `return res /* or res.Containers */, nil` placeholder resolved to `return res.Items, nil`.
2. **`container.Summary` lives in `github.com/moby/moby/api/types/container`** — not under the top-level `client` package. Aliased through `internal/docker.ContainerSummary` so callers never import the subpackage directly.
3. **`events.Message` lives in `github.com/moby/moby/api/types/events`** — same treatment; aliased as `internal/docker.EventMessage`.
4. **`client.Events` returns `EventsResult{Messages, Err}`** — already channel-shaped. No iterator-adapter goroutine needed. The plan skeleton's "iterator drain into channels" branch was unnecessary.
5. **`client.Ping(ctx, PingOptions)` returns `(PingResult, error)`** — facade discards PingResult; reachability is all `/healthz` cares about.
6. **`client.ImageTag(ctx, ImageTagOptions{Source, Target})` returns `(ImageTagResult, error)`** with empty result — facade flattens to (ctx, src, dst).
7. **No `EventMessage` symbol in `client` package** — confirmed via `go doc github.com/moby/moby/client EventMessage` returning "no symbol". The plan skeleton's `client.EventMessage` alias would not have compiled; resolved to `events.Message` from the subpackage.
8. **Identifier-index suffix appended to `_sdk_shape.txt`** — needed because `go doc` output uses bare type names (`ContainerListOptions`) while source uses qualified form (`client.ContainerListOptions`). The drift gate's `comm -23` operates on the qualified form, so the index closes the form gap.

## Deviations from Plan

None — the plan's `<action>` step 1-12 anticipated SDK signature deviations (explicit notes throughout: "ADAPT the method body to match what _sdk_shape.txt records") and the executions above are the documented adaptation path, not deviations from the plan's intent. The mechanical drift gate (`comm -23`) passes empty.

## Issues Encountered

**1. `go mod tidy` removed the moby/moby/client require after first `go get`.** Cause: nothing imported the module yet (we hadn't written moby.go), so `go mod tidy` correctly classified it as unused. Resolution: re-ran `go get github.com/moby/moby/client@v0.4.1` after writing the RED test (which imports nothing from moby) — the GREEN moby.go import keeps the dep pinned. Indirect-marker `// indirect` in go.mod is expected and correct until plan 02-02 adds the discovery package's direct import.

**2. `comm -23` drift gate initially flagged 7 false misses.** Cause: `go doc` output uses unqualified type names; source uses `client.X`. Resolution: appended an "IDENTIFIER INDEX" block to `_sdk_shape.txt` listing every reachable identifier in `client.X` form. The block is documented as the gate's machine-readable input.

## SDK Shape Capture (Reference)

The full `internal/docker/_sdk_shape.txt` is 786 lines — committed in full as the canonical record. Section headers (the comm -23 input — every section maps to a single SDK surface):

```
$ go doc github.com/moby/moby/client                               (top-level package)
$ go doc github.com/moby/moby/client ContainerListOptions
$ go doc github.com/moby/moby/client ContainerListResult
$ go doc github.com/moby/moby/client ContainerInspect
$ go doc github.com/moby/moby/client ContainerInspectOptions
$ go doc github.com/moby/moby/client ContainerInspectResult
$ go doc github.com/moby/moby/client EventsListOptions
$ go doc github.com/moby/moby/client Events
$ go doc github.com/moby/moby/client EventsResult
$ go doc github.com/moby/moby/client ImagePullOptions
$ go doc github.com/moby/moby/client ImagePull
$ go doc github.com/moby/moby/client ImagePullResponse
$ go doc github.com/moby/moby/client ImageTagOptions
$ go doc github.com/moby/moby/client ImageTag
$ go doc github.com/moby/moby/client ImageTagResult
$ go doc github.com/moby/moby/client Ping
$ go doc github.com/moby/moby/client PingOptions
$ go doc github.com/moby/moby/client PingResult
$ go doc github.com/moby/moby/client Filters
$ go doc github.com/moby/moby/api/types/events Message
$ go doc github.com/moby/moby/api/types/container Summary
$ go doc github.com/moby/moby/api/types/container InspectResponse
```

The most consequential signatures (in-source mirror — see top-of-file comment block in `internal/docker/moby.go` for the exact captures):

```
func (cli *Client) Ping(ctx, PingOptions) (PingResult, error)
func (cli *Client) ContainerList(ctx, ContainerListOptions) (ContainerListResult, error)
func (cli *Client) ContainerInspect(ctx, id, ContainerInspectOptions) (ContainerInspectResult, error)
func (cli *Client) Events(ctx, EventsListOptions) EventsResult           // returns {Messages, Err} channels directly
func (cli *Client) ImagePull(ctx, ref, ImagePullOptions) (ImagePullResponse, error)
func (cli *Client) ImageTag(ctx, ImageTagOptions) (ImageTagResult, error)
```

## go.sum Audit (T-02-01-03 mitigation)

Verified via grep:
- `github.com/docker/docker` entries in go.sum: **0** (zero — the deprecated module did NOT sneak in)
- `github.com/moby/moby/client` entries: 1 (the pinned v0.4.1)
- `github.com/moby/moby/api` entries: 1 (v1.54.2 — required for events/container subpackages)

No unexpected supply-chain entries. Other transitive deps (containerd/errdefs, OTel, distribution/reference, opencontainers/{go-digest,image-spec}, Microsoft/go-winio for Windows, docker/go-{connections,units}) are all legitimate moby/moby/api dependencies for the Engine API client.

## Threat Model Coverage

| Threat ID | Status | Evidence |
|-----------|--------|----------|
| T-02-01-01 (tampering go.mod) | mitigated | go.mod pins exact v0.4.1; transitive deps recorded in go.sum |
| T-02-01-02 (info disclosure via NewClient errors) | mitigated | `fmt.Errorf("docker.NewClient: %w", err)` wrap; TestNewClient_BadDockerHost pins the prefix |
| T-02-01-03 (spoofing — docker/docker) | mitigated | go.sum grep returns zero; CI grep enforces `internal/docker/*.go` is the only importer |
| T-02-01-04 (unaudited interface growth) | accepted-with-guard | TestClient_InterfaceMethodCount reflect-based; failure forces coordinated edit |
| T-02-01-05 (EOP via daemon-socket path leak) | mitigated (deferred to healthz) | NewClient surface is not yet HTTP-exposed; plan 02-04 owns the response-body scrubbing |
| T-02-01-06 (SDK shape drift) | mitigated | _sdk_shape.txt + comm-23 drift gate; failures block future SDK bumps |

## Next Phase Readiness

**Ready for plan 02-02 (compose reader):**
- `internal/docker.Client` is concrete — wave-2 plans can dependency-inject without coordinating on the interface.
- `state.Container` + `api.Container` field set is stable. Plan 02-03 (discovery goroutine) writes to ContainerID/Labels/Pinned/Stopped without further schema churn.
- TS types regenerated; UI work in Phase 5 inherits the four new fields.

**Blockers/concerns for downstream plans:**
- None for plans 02-02 through 02-05.
- Plan 02-03 will pull `github.com/moby/moby/client` indirectly via `internal/docker` only — the `// indirect` marker in go.mod will flip to direct once 02-03 lands (cosmetic; not a regression signal).

## Self-Check: PASSED

Verified files exist:
- `internal/docker/moby.go` — FOUND
- `internal/docker/moby_test.go` — FOUND
- `internal/docker/_sdk_shape.txt` — FOUND (786 lines)
- `internal/docker/client.go` — FOUND (modified)
- `internal/state/schema.go` — FOUND (modified)
- `internal/api/types.go` — FOUND (modified)
- `ui/src/lib/types.d.ts` — FOUND (regenerated)

Verified commits exist:
- `9bb6ff8` — FOUND (test commit)
- `e411bb9` — FOUND (feat commit)

Verified gates pass:
- `go build ./...` — exit 0
- `go vet ./...` — exit 0
- `go test ./... -race` — exit 0
- `make check-types` — exit 0
- `_sdk_shape.txt` exists, non-empty, 19 sections (>= 6 required)
- `comm -23` drift gate — empty output (PASS)
- moby SDK boundary grep — only `internal/docker/*.go` matches

## TDD Gate Compliance

- RED commit: `9bb6ff8` — test(02-01): adds failing tests; build verified to fail with `undefined: mobyClient` / `undefined: NewClient`.
- GREEN commit: `e411bb9` — feat(02-01): drives tests to pass.
- REFACTOR commit: not present — GREEN code is already at the quality bar (doc comments, threat-model cross-refs, alias rationale). REFACTOR is optional per execute-plan.md and was assessed unnecessary for this task.

---
*Phase: 02-docker-client-compose-file-reader*
*Completed: 2026-05-13*
