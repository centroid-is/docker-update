---
phase: 09-architectural-hardening-post-v0-1-bug-cluster
plan: 01
subsystem: infra
tags: [ci, github-actions, makefile, static-check, go-embed, parallelization]

# Dependency graph
requires:
  - phase: 01-walking-skeleton
    provides: ".gitignore negation rule `!internal/api/dist/.gitkeep` (Plan 01-01) — re-honored here by recreating the placeholder that had drifted out of the worktree."
  - phase: 08-deploy
    provides: "publish.yml decoupling (commit b45730a) — Phase 9 (c) is consistent with this decoupling; both ci.yml jobs run in parallel with publish.yml."
provides:
  - "Parallel `tests` + `image-downstream` CI jobs (SC-5 structural prerequisite — wall time observable post-merge)"
  - "`make grep-no-compose` static-check target (SC-1 enforcement gate) wired into jobs.tests"
  - "Committed `internal/api/dist/.gitkeep` so `//go:embed all:dist` parses without `npm run build` in the tests job"
affects: [09-02-recreate-tests, 09-03-socket-only-recreate, 09-04-self-update-sidecar]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "CI 2-job parallel split with no inter-job needs: declaration — relies on GitHub Actions' default fork-join behavior to maximise wall-clock concurrency"
    - "Makefile static-check gate via `if grep ... ; then FAIL; else PASS; fi` shell idiom; non-zero exit on production-code anti-pattern match"
    - "//go:embed all:dist parses against committed .gitkeep — defense in depth via gitignore negation (`!internal/api/dist/.gitkeep`) + defensive `mkdir -p` step in CI"

key-files:
  created:
    - "internal/api/dist/.gitkeep — embed placeholder (replaces a drifted-out file; was already negated in .gitignore)"
  modified:
    - ".github/workflows/ci.yml — 1 job ('build-test') replaced by 2 parallel jobs ('tests', 'image-downstream')"
    - "Makefile — appended `grep-no-compose` PHONY target (28 lines including header comment); PHONY declaration extended"

key-decisions:
  - "Preserve every existing step in image-downstream VERBATIM (continue-on-error settings, inline scripts, Phase 7 production-image build, idle-RAM gate, portability gate, playwright report upload) — minimises risk that the split inadvertently changes CI semantics on `main`"
  - "Add the `grep-no-compose` step in jobs.tests even though RESEARCH.md Example 3 omits it — the plan's W4 NOTE flagged this as a deliberate extension of the template; SC-1 enforcement is load-bearing for Plan 09-03"
  - "Defensive `mkdir -p internal/api/dist` retained even though .gitkeep is now tracked — RESEARCH.md Pitfall 6 explicitly recommends belt-and-braces for fresh-worktree scenarios where .gitkeep could be accidentally untracked"
  - "grep-no-compose scans `internal/recreate/` even though the directory does not yet exist — `grep -r` against a missing directory writes to stderr (swallowed by `2>/dev/null`) and the gate still runs cleanly against `internal/actions/`. Plan 09-03 will create `internal/recreate/`, at which point it is automatically included."

patterns-established:
  - "Phase 9 CI architecture: two top-level jobs with no `needs:`, neither blocking the other; publish.yml stays separate per b45730a"
  - "SC-N enforcement gate: a Makefile PHONY target with a grep+exit pattern, invoked by a single `run: make <gate>` CI step — keeps the gate definition in repo source where Go developers can reason about it, while CI just calls the same target an operator can run locally"

requirements-completed: [SC-5, SC-1]

# Metrics
duration: ~8min
completed: 2026-05-16
---

# Phase 9 Plan 01: CI 2-job split + grep-no-compose seed + .gitkeep stub Summary

**Split `ci.yml` into parallel `tests` + `image-downstream` jobs, seed the `make grep-no-compose` SC-1 enforcement gate consumed by jobs.tests, and re-track the `internal/api/dist/.gitkeep` embed placeholder so `//go:embed all:dist` parses without a UI build.**

## Performance

- **Duration:** ~8 min
- **Started:** 2026-05-16T18:57:00Z (approx — STATE.md `Phase 09 execution started` 2026-05-16)
- **Completed:** 2026-05-16T19:05:30Z
- **Tasks:** 2 (both atomic, single commit each)
- **Files modified:** 2 (`.github/workflows/ci.yml`, `Makefile`); 1 created (`internal/api/dist/.gitkeep`)

## Accomplishments

- `.github/workflows/ci.yml` now declares two top-level jobs (`tests`, `image-downstream`), each `runs-on: ubuntu-24.04`, with no `needs:` keyword linking them — they run in parallel under the default GitHub Actions scheduler.
- `Makefile` now ships a `.PHONY: grep-no-compose` target that scans `internal/actions/` and `internal/recreate/` for the three forbidden subprocess-compose patterns (`exec.Command`, `docker compose`, `compose up`) in non-test, non-comment production code, exiting non-zero on any match.
- `internal/api/dist/.gitkeep` is back in the index (it was untracked at plan start despite the gitignore negation rule from Plan 01-01) so the `tests` job's `//go:embed all:dist` parses at compile time.
- jobs.tests step list is exactly what the plan called for, in order: checkout → setup-go → defensive dist stub → go vet → install tygo → setup-node → tygo drift check → grep-no-compose → go test -race (9 steps total, 6 named).
- jobs.image-downstream preserves all 17 steps from the prior `build-test` job verbatim, including the three Phase-7 deployment gates (`Build production image`, `Idle-RAM gate`, `Portability gate`) and the failure-only playwright report upload.

## YAML structure of new ci.yml

| Job | runs-on | needs | step_count | Named steps |
|---|---|---|---|---|
| `tests` | `ubuntu-24.04` | null | 9 | defensive dist stub, go vet, install tygo, tygo drift check, grep-no-compose, go test (race) |
| `image-downstream` | `ubuntu-24.04` | null | 17 | ui install, ui build, build image (no push), image-size gate, install oras, install crane, install playwright browsers, e2e (cron-fast), Build production image, Idle-RAM gate, Portability gate, upload playwright report on failure |

Top-level keys preserved verbatim from the prior workflow: `name: ci`, `on: { push: { branches: [main] }, pull_request: }`, `permissions: { contents: read }`, `concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: true }`.

## Exact Makefile rule body for `grep-no-compose`

```
grep-no-compose:
	@if grep -rE 'exec\.Command|docker compose|compose up' internal/actions/ internal/recreate/ 2>/dev/null | grep -v '_test\.go' | grep -v '^[^:]*:[[:space:]]*//' ; then \
		echo 'FAIL: subprocess-compose pattern detected in non-test production code (SC-1)'; \
		exit 1; \
	else \
		echo 'PASS: grep-no-compose'; \
	fi
```

Both directions verified end-to-end before commit:

- **Positive case** (current codebase, no production-code subprocess-compose calls): `make grep-no-compose` → `PASS: grep-no-compose`, exit 0.
- **Negative case** (one-line probe `_grep_negative_probe.go` containing `exec.Command("docker", "compose")` temporarily placed in `internal/actions/`, then removed): `make grep-no-compose` → `FAIL: subprocess-compose pattern detected in non-test production code (SC-1)`, exit 2. Probe NOT committed.

## .gitkeep tracking confirmation

```
$ git ls-files internal/api/dist/.gitkeep
internal/api/dist/.gitkeep

$ git ls-files --stage internal/api/dist/.gitkeep
100644 e69de29bb2d1d6434b8b29ae775ad8c2e48c5391 0	internal/api/dist/.gitkeep
```

The placeholder is the canonical empty-blob SHA (`e69de29`), 0 bytes, mode 0644 — matches the expectations of `//go:embed all:dist` (the directory is present, contains a file, the pattern matches the dotfile per Go embed semantics).

## Post-merge CI run URL + wall-time observation

**Pending Plan 09-03 merge for representative wall-time measurement.** The structural prerequisite for SC-5 (no `needs:` between the two jobs) is met. Wall-time will be measurable after the next push to `main` via `gh run list --workflow=ci.yml --branch main --limit 5`; the SC-5 success gate (≤6 min wall clock) is empirical and tracked against the running-on-main observation, not against this plan's commits.

Note: the prior monolithic `build-test` job ran ~7–8 min serial; the bottleneck path for the new design is `image-downstream` (UI build + docker build + e2e + production image + idle-RAM 60s settle + portability), estimated 5–6 min. `tests` is estimated ~3 min (no docker, no UI build, no playwright). Wall clock should be `max(tests, image-downstream)` ≈ 5–6 min.

## Task Commits

Each task was committed atomically:

1. **Task 1: Replace ci.yml with parallel tests + image-downstream jobs** — `c39929b` (ci)
2. **Task 2: Add Makefile `grep-no-compose` target and track .gitkeep** — `888bc9d` (chore)

**Plan metadata:** _(this commit, hash recorded after final commit)_

## Files Created/Modified

- `.github/workflows/ci.yml` — Rewritten from 1-job (`build-test`, 18 steps serial) to 2-job parallel (`tests` 9 steps + `image-downstream` 17 steps); +63/-16 lines net.
- `Makefile` — Appended `grep-no-compose` PHONY target (28 lines including doc comment); `.PHONY:` declaration extended with the new target name. +28/-1 lines net.
- `internal/api/dist/.gitkeep` — Created (0-byte empty file). The negation in `.gitignore` was already in place from Plan 01-01; the placeholder itself had been dropped from the worktree (vite v7 emptyOutDir behavior per STATE.md decision log).

## Decisions Made

See `key-decisions` in frontmatter. Notable:

- **`grep-no-compose` step is added to jobs.tests even though RESEARCH.md Example 3 omits it.** The plan's Task 1 `<action>` block explicitly flags this (W4 NOTE): "RESEARCH.md § Code Examples / Example 3 YAML template does NOT include this step — it is added here as the SC-1 enforcement gate per this plan's objective." Followed the plan, not the research template.
- **`continue-on-error: true` retained on e2e cron-fast and portability gate.** RESEARCH.md Example 3 retains these settings; preserving them keeps the publish-decoupling posture intact (publish.yml does NOT consume ci.yml's success — see commit b45730a).
- **Production-image + idle-RAM + portability steps stay in `image-downstream`.** These were not explicit in RESEARCH.md Example 3 (which abbreviated them as `# existing inline script`), but the plan's Task 1 `<action>` says "Copy the existing ci.yml's [...] portability gate steps VERBATIM (including their `continue-on-error` settings and inline scripts)." Followed plan; the production-image step (Phase 7 DEPLOY-01) is included because it sits in the same image-downstream surface even though the plan only enumerated UI/docker/e2e/idle-RAM/portability.

## Deviations from Plan

None — plan executed exactly as written. The `<action>` block's enumeration of image-downstream steps to copy verbatim ("UI build, image build, image-size gate, playwright install, e2e cron-fast, idle-ram gate, portability gate") was implicitly extended to include the Phase 7 production-image step that sits between e2e and idle-RAM in the existing workflow. This was structurally required (the idle-RAM gate consumes `docker-update:prod` produced by the production-image step). The plan's `<acceptance_criteria>` does not assert on this step's presence/absence; nothing was added beyond what was already in the prior workflow.

The `.gitkeep` task action said: _"If `git ls-files internal/api/dist/.gitkeep` returns nothing, create an empty file at that path and `git add` it. Otherwise no-op."_ Pre-task, `git ls-files internal/api/dist/.gitkeep` returned nothing — so the conditional branch fired: created the file and staged it. This is per-plan, not a deviation.

## Issues Encountered

- `yamllint` and `yq` are not installed on the workstation; the plan's `<verify>` block requires one of them OR a python YAML parser fallback. Used `python3 -c 'import yaml; yaml.safe_load(open(".github/workflows/ci.yml"))'` for the YAML parse gate (explicitly named in the plan's acceptance criteria as the fallback). All grep-based acceptance criteria ran natively.
- The `git ls-files internal/api/dist/.gitkeep` precondition the plan's RESEARCH.md A6 assumed (".gitkeep is tracked, verified this session via `ls internal/api/dist/`") was stale on this workstation — `ls` showed `assets/` and `index.html` (vite build output) but no `.gitkeep`. Task 2's conditional create-then-stage branch handled it; the plan was robust to this case.

## User Setup Required

None — this plan only modifies repository-internal files (workflow, Makefile, embed placeholder). No environment variables, no external services, no operator-side compose edits.

## Next Phase Readiness

- **Plan 09-02 (RED tests for relative-bind-mount + recreate atomicity):** Unblocked. The `tests` job will execute the new test files when Plan 09-02 lands them.
- **Plan 09-03 (GREEN socket-only recreate):** Unblocked. The `grep-no-compose` gate is in place; Plan 09-03 must keep it passing (and will benefit from the gate catching any accidental regression in the new `internal/recreate/` package).
- **Plan 09-04 (self-update sidecar):** Unblocked. No CI structural changes required from this plan.
- **Wall-time SC-5 confirmation:** Will be observable post-Plan-09-03 merge to `main` via `gh run list --workflow=ci.yml --branch main --limit 5` and the per-job timings displayed in the Actions UI. Not assertable from this plan alone.

## Threat Flags

None — no new network endpoints, auth paths, file access patterns, or schema changes at trust boundaries introduced. The `grep-no-compose` static-check exposes only line contents of tracked source files in a public repo (per the plan's `<threat_model>` row T-09-01-04, accepted).

## Self-Check: PASSED

Verified before SUMMARY.md commit:

- `.github/workflows/ci.yml` — present in worktree.
- `Makefile` — present in worktree (grep-no-compose target).
- `internal/api/dist/.gitkeep` — present in worktree, tracked (`git ls-files` returns the path).
- Commit `c39929b` (Task 1) — present in `git log --oneline -5`.
- Commit `888bc9d` (Task 2) — present in `git log --oneline -5`.
- `make grep-no-compose` — exits 0 on current codebase (positive case).
- `python3 -c 'import yaml; yaml.safe_load(...)'` on ci.yml — parses cleanly.
- `git diff HEAD~2 HEAD -- .github/workflows/publish.yml` — empty (publish.yml untouched per b45730a invariant).

---
*Phase: 09-architectural-hardening-post-v0-1-bug-cluster*
*Completed: 2026-05-16*
