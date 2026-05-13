---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
status: executing
stopped_at: Completed 02-03 Task 1 (TDD RED+GREEN — docker.Discoverer); Phase 02 advanced to plan 04
last_updated: "2026-05-13T21:43:20.014Z"
last_activity: 2026-05-13
progress:
  total_phases: 8
  completed_phases: 1
  total_plans: 9
  completed_plans: 8
  percent: 89
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-05-13)

**Core value:** A Centroid field engineer can confidently pull a fresh image to an HMI and roll it back to the previous digest, from one button per container in a browser, with no external services or extra state stores in the loop.
**Current focus:** Phase 02 — Docker Client & Compose-File Reader

## Current Position

Phase: 02 (Docker Client & Compose-File Reader) — EXECUTING
Plan: 5 of 5
Status: Ready to execute
Last activity: 2026-05-13

Progress: [█████████░] 89%

## Performance Metrics

**Velocity:**

- Total plans completed: 1
- Average duration: ~7min
- Total execution time: ~7min

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 1. Walking Skeleton & Test Harness | 1/4 | ~7min | ~7min |

**Recent Trend:**

- Last 5 plans: 01-01 (7min, 3 tasks, 15 files)
- Trend: on-pace; first plan completed without checkpoint deviation

*Updated after each plan completion*

| Plan | Duration | Tasks | Files |
|------|----------|-------|-------|
| Phase 01 P01 | 7min | 3 | 15 |
| Phase 01 P03 | 5min | 2 tasks | 16 files |
| Phase 01 P02 | 20min | 2 tasks | 4 files |
| Phase 01 P04 | 25min | 3 tasks | 18 files |
| Phase 02 P01 | 5min | 1 tasks | 9 files |
| Phase 02 P02 | 13min | 1 tasks | 3 files |
| Phase 02 P03 | 25min | 1 tasks | 2 files |
| Phase 02 P04 | 10min | 2 tasks | 7 files |

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- Pre-Phase 1: TDD-first with Playwright e2e tests against a real `docker compose` test stack + zot fake registry — every functional requirement starts as a red test (C4)
- Pre-Phase 1: Walking-skeleton phase precedes F1 red test because the test harness must work before any feature test can fail meaningfully
- Pre-Phase 1: `github.com/moby/moby/client` (not deprecated `docker/docker/client`); Go 1.26; `distroless/static-debian12:nonroot` (pinned, not floating)
- Pre-Phase 1: `crane.Digest()` from `google/go-containerregistry` replaces hand-rolled Bearer-token + multi-arch index code (where WUD 8.2.2's two named bugs lived)
- Pre-Phase 1: `docker compose` via `os/exec` subprocess, not the Compose Go SDK (BuildKit/containerd transitive deps would blow the 30 MB image budget)
- [Phase 01 P01]: Go 1.26 over brief's 1.23 — 1.23 EOL 2026-02-11
- [Phase 01 P01]: renameio/v2 v2.0.2 added at scaffold time so plan 02 imports cleanly
- [Phase 01 P01]: .gitignore must use internal/api/dist/* not internal/api/dist/ — git cannot re-include files under excluded dirs; documented in-file
- [Phase 01 P01]: Plan 02 persist() must use the dir-fsync wrapper from RESEARCH.md Pitfall A — renameio.WriteFile does NOT fsync parent dir
- [Phase 01]: types.go mirrors state/schema.go json tags verbatim (omitempty on Image/Tag) — wire/disk schema parity is the load-bearing invariant — Plan 01-03 deviation Rule 1: plan's verbatim Go sample omitted omitempty but state.go is on-disk canonical
- [Phase 01]: tygo installed via go install (dev tool), not a go.mod dependency — Plan 01-03 — matches CI workflow install pattern; avoids polluting go.mod
- [Phase 01]: Vite emptyOutDir wipes internal/api/dist/.gitkeep; accepted per plan — make ui always reseeds dist/ before go build — Plan 01-03 — Vite v7 default behavior; CI workflow runs make ui before make build so this is safe
- [Phase 01]: Shipped renameio.WriteFile + explicit os.Open(filepath.Dir).Sync() wrapper (research correction A5, Option 2) to close the host-power-loss durability window that bare renameio leaves open
- [Phase 01]: Empty (0-byte) state file is treated identically to a missing file in NewStore — covers crash-mid-create recovery without operator intervention
- [Phase 01]: NewStore surfaces JSON decode failures with errors containing 'decode' rather than silently resetting the file; protects the previous-digest tail needed for rollback (threat T-01-02-05)
- [Phase 01]: Drop compose-side healthcheck on distroless services (zot + hmi-update); host-side poll in global-setup.ts instead — Both images are distroless with no wget/curl/sh; wget-based healthcheck fails with 'executable file not found' and the container stays Unhealthy forever, blocking docker compose up --wait. Host-side poll preserves the readiness gate.
- [Phase 01]: Map zot host port 15000 -> container 5000; overridable via ZOT_HOST_PORT env — Default port 5000 conflicts with macOS Control Center (AirPlay Receiver) on dev machines, producing a silent compose 'bind: address already in use' error.
- [Phase 01]: tmpfs /state for hmi-update in e2e stack (not a named volume) — Named volumes inherit root:root on first create; the distroless runtime image runs as UID 65532 (nonroot) and has no shell to chown. tmpfs supports uid/gid/mode mount options.
- [Phase 01]: Scope tygo generation to types.go via include_files — Default package-wide scan picked up server.go's exported Server struct and emitted an empty TS interface, causing make check-types to fail. include_files keeps the UI types contract scoped to the source-of-truth file.
- [Phase 02]: SDK alias container.Summary not client.ContainerSummary — moby/moby/client v0.4.1 reorganised result types into api/types subpackages; plan skeleton referenced legacy docker/docker shape
- [Phase 02]: client.Events returns EventsResult{Messages, Err} already channel-shaped — no iterator-adapter goroutine needed; facade unpacks directly
- [Phase 02]: Appended IDENTIFIER INDEX block to _sdk_shape.txt — bare go doc output uses unqualified type names while source uses client.X form; index closes the comm-23 drift gate's form-mismatch gap
- [Phase 02]: state.Container Pinned/Stopped use omitempty even for booleans — keeps wire payload clean for the 95% running-non-pinned case
- [Phase 02]: compose.Reader belt-and-braces (mtime,size) check runs unconditionally — not gated on !bootInodeStable; catches in-place os.WriteFile edits on stable-inode filesystems
- [Phase 02]: deleted compose file unified under ErrComposeFileMoved via dual %w wrap — both errors.Is(err, ErrComposeFileMoved) and errors.Is(err, fs.ErrNotExist) succeed on the same value; Phase 4 412 handler stays simple, Phase 5 UI can distinguish later
- [Phase 02]: internal/compose/errors.go establishes the codebase's first sentinel-error file convention — sibling errors.go with documented wrap semantics and HTTP-status mapping; future packages (registry, poll, actions) should follow
- [Phase 02]: Discoverer's stateStore interface is package-private (Get + Update only) — production NewDiscoverer takes *state.Store concretely; tests inject safeStore/recordingStore wrappers via newDiscovererWithStore. Not a Phase 3 extension point.
- [Phase 02]: Reconnect-backoff attempt counter persists across loop iterations on failure — the naive design (reset attempt on every Events() return) caps progression at 1s forever because the SDK returns the channel pair synchronously even on a failed subscription.
- [Phase 02]: parseImageRef defaults bare refs to tag="latest" (docker CLI implicit behaviour) — pinned refs (@sha256:) return tag="" so Container.Tag is empty for pinned; the Pinned bool carries that signal separately.
- [Phase 02]: safeStore test wrapper added — state.Store.Get returns a shallow snapshot whose inner map is shared with the writer; tests that poll state concurrently with a running Discoverer must wrap with a deep-copy layer or trip the race detector. The wrapper lives in _test.go space.
- [Phase 02]: Anti-deadlock invariant (ARCHITECTURE.md lines 419-420) verified by Test 9 via channel-instrumented call ordering — recordingStore.Update flips an atomic.Bool BEFORE delegating, so a regression that moves ContainerInspect into the Update closure trips t.Errorf at inspect-entry.
- [Phase ?]: [Phase 02]: //go:build !debug constraint required on debug_compose_noop.go — without it, 'go build -tags=debug ./...' fails with duplicate-method error. Plan 02-04 verify gate (grep -L for no build tag) was incorrect; the substantive gate (debug build succeeds) is the one that matters.
- [Phase ?]: [Phase 02]: /healthz response bodies are 5 named VERBATIM constants per CONTEXT.md — no interpolation, no fmt.Sprintf; any new branch requires threat-model review (T-02-04-01). EACCES hint references id -g docker (Pitfall 9) verbatim.
- [Phase ?]: [Phase 02]: HMI_UPDATE_DOCKER_HOST env-var test seam — handlers.go's dockerSocketPath() consults env first, falls back to /var/run/docker.sock. Mirrors Phase 1's HMI_UPDATE_STATE_PATH convention; t.Setenv-safe per Go test runner contract.
- [Phase ?]: [Phase 02]: Build-tag mutually-exclusive method pair pattern — debug-only handlers ship as two files (//go:build debug + //go:build !debug) declaring the same method with different bodies. Production binaries pass 'strings | grep route' with zero matches (T-02-04-02). Reusable pattern for future debug seams.

### Pending Todos

[From .planning/todos/pending/ — ideas captured during sessions]

None yet.

### Blockers/Concerns

[Issues that affect future work]

- Phase 6 (UX-01) is a *product* decision checkpoint, not a technical one — needs operator-experience input + the real UI from Phase 5 in hand to choose between options (a)/(b)/(c). If (b), Phase 6 adds non-trivial scope (`prepared_digest` field, third button, new endpoint).
- Phase 7 (DEPLOY-02): if `docker` + `compose` CLI plugins push the final image past 30 MB on `static-debian12:nonroot`, fall back to `cc-debian12:nonroot`. Measurement happens in Phase 7; budget verified there.

## Deferred Items

Items acknowledged and carried forward from previous milestone close:

| Category | Item | Status | Deferred At |
|----------|------|--------|-------------|
| *(none)* | | | |

## Session Continuity

Last session: 2026-05-13T21:43:09.054Z
Stopped at: Completed 02-03 Task 1 (TDD RED+GREEN — docker.Discoverer); Phase 02 advanced to plan 04
Resume file: None
