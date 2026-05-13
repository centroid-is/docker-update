# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-05-13)

**Core value:** A Centroid field engineer can confidently pull a fresh image to an HMI and roll it back to the previous digest, from one button per container in a browser, with no external services or extra state stores in the loop.
**Current focus:** Phase 1 — Walking Skeleton & Test Harness

## Current Position

Phase: 1 of 8 (Walking Skeleton & Test Harness)
Plan: 0 of TBD in current phase
Status: Ready to plan
Last activity: 2026-05-13 — Roadmap drafted and approved; 73 v1 requirements mapped across 8 phases

Progress: [░░░░░░░░░░] 0%

## Performance Metrics

**Velocity:**
- Total plans completed: 0
- Average duration: —
- Total execution time: —

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| - | - | - | - |

**Recent Trend:**
- Last 5 plans: —
- Trend: —

*Updated after each plan completion*

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- Pre-Phase 1: TDD-first with Playwright e2e tests against a real `docker compose` test stack + zot fake registry — every functional requirement starts as a red test (C4)
- Pre-Phase 1: Walking-skeleton phase precedes F1 red test because the test harness must work before any feature test can fail meaningfully
- Pre-Phase 1: `github.com/moby/moby/client` (not deprecated `docker/docker/client`); Go 1.26; `distroless/static-debian12:nonroot` (pinned, not floating)
- Pre-Phase 1: `crane.Digest()` from `google/go-containerregistry` replaces hand-rolled Bearer-token + multi-arch index code (where WUD 8.2.2's two named bugs lived)
- Pre-Phase 1: `docker compose` via `os/exec` subprocess, not the Compose Go SDK (BuildKit/containerd transitive deps would blow the 30 MB image budget)

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

Last session: 2026-05-13
Stopped at: Roadmap and STATE created; ready to `/gsd-plan-phase 1`
Resume file: None
