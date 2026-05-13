---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
status: planning
stopped_at: "Completed plan 01-01 (Wave 1): repo skeleton + Wave-0 RED tests"
last_updated: "2026-05-13T12:59:38.651Z"
last_activity: 2026-05-13 — Roadmap drafted and approved; 73 v1 requirements mapped across 8 phases
progress:
  total_phases: 8
  completed_phases: 0
  total_plans: 4
  completed_plans: 1
  percent: 25
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-05-13)

**Core value:** A Centroid field engineer can confidently pull a fresh image to an HMI and roll it back to the previous digest, from one button per container in a browser, with no external services or extra state stores in the loop.
**Current focus:** Phase 1 — Walking Skeleton & Test Harness

## Current Position

Phase: 1 of 8 (Walking Skeleton & Test Harness)
Plan: 1 of 4 in current phase (01-01 complete; next: 01-02 state store)
Status: In Progress
Last activity: 2026-05-13 — Plan 01-01 complete: repo skeleton + Wave-0 RED tests

Progress: [███░░░░░░░] 25%

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

Last session: 2026-05-13T12:59:38.647Z
Stopped at: Completed plan 01-01 (Wave 1): repo skeleton + Wave-0 RED tests
Resume file: None — ready for plan 01-02 (state store implementation, drives persist_test + store_test green)
