---
phase: 06-display-blackout-ux-checkpoint
plan: 01
subsystem: docs
tags: [ux, decision-record, readme, operator-facing, playwright, e2e, contract-test, weston, flutter, pitfall-5, ui-08]

# Dependency graph
requires:
  - phase: 05-web-ui-completeness
    plan: 03
    provides: "WarningModal.svelte + display-warning.ts (requiresWarning predicate against ['flutter','weston'] case-insensitive substring) — the Phase 5 deliverable that Phase 6's spec verifies"
  - phase: 05-web-ui-completeness
    plan: 04
    provides: "App.svelte handleAction → requiresWarning gate → pendingAction → WarningModal flow — the wired UI path that the spec exercises end-to-end"

provides:
  - "PROJECT.md Key Decisions row recording UX-01 → option (a) (README warning + Phase-5 pre-action toast). Enumerates the rejected options (b) two-step prepare/switch + (c) per-service danger-flag label, naming the surface-area cost of each. Locks the choice as 'no Phase 6 code changes'."
  - "README.md (NEW at repo root) — operator-facing introduction. Load-bearing 'Before you click Update on flutter or weston' section names the 5-30s blackout window, the weston-cascade-onto-all-Wayland-clients gotcha, the Phase-5 confirmation toast, the Rollback safety net (<15s, local cache, no network), and Force Pull as the docker-image-prune recovery path. Pointers to PROJECT.md / ROADMAP.md / API.md / .planning/research/."
  - "e2e/tests/weston-warning.spec.ts — Playwright contract test asserting that clicking Update on weston-stub surfaces role=dialog with 'display' AND 'flicker' keywords BEFORE any /api/containers/*/update POST. Cancel path proves no POST leaks. Pins UI-08's substring-detection contract in CI."
  - "e2e/compose.test.yml weston-stub service block (watched, pull_policy: never against the pre-seeded zot:5000/centroid-is/stub:latest image). Added to hmi-update's depends_on so the Discoverer enumerates it at boot."

affects:
  - "Phase 7 (deployment / install runbook): README.md is the file Phase 7 extends with the full install runbook — id -g docker step (DEPLOY-08 / Pitfall 9), the compose deployment block (DEPLOY-04), the self-upgrade procedure. Phase 6 creates the file; Phase 7 grows it."
  - "Future Phase 5 refactors of display-warning.ts / WarningModal.svelte: weston-warning.spec.ts is the CI gate that catches a substring-detection regression (e.g., someone narrowing the predicate to exact match, or removing 'flicker' from the modal body). If UX-02 turns red, the contributor must update both Phase 5 and this spec deliberately."
  - "v2 option (b) two-step prepare/switch — explicitly NOT shipped here, but flagged in CONTEXT.md `<deferred>` as a candidate if operator feedback indicates the pre-action toast is insufficient."

# Tech tracking
tech-stack:
  added: []  # No new code dependencies — documentation-only plan under UX-01 option (a)
  patterns:
    - "Contract-test-against-shipped-implementation — the spec is RED-first against future regressions, but GREEN at execution time because Phase 5 already shipped UI-08. The commit type is `test(...)` (the file is purely test code); no GREEN follow-on `feat(...)` commit because there is no production code to write in Phase 6. Documents the difference between classical TDD (red→green→refactor on the same plan) and contract-test TDD (red-first against future drift; green-from-day-zero against today)."
    - "Flexible selector + keyword-only text assertion — the spec uses page.getByRole('dialog') + toContainText(/display/i, /flicker/i) instead of byte-matching the modal's exact copy. A Phase 5 copy refinement on the body string will not break this phase's UX-02 verification — only a regression in the substring-detection predicate itself does."
    - "Append-only Key Decisions table — Phase 6 grows PROJECT.md's table by exactly one row; existing rows preserved verbatim. Matches the established Phase 4 / Phase 5 pattern."
    - "Three-way audit trail for product decisions — CONTEXT.md `<decisions>` section + PROJECT.md Key Decisions row + README operator-callout. Future reviewer asking 'why didn't we ship option (b)?' finds the rejected-options' surface-area enumeration in all three documents, deep-linked."

key-files:
  created:
    - "README.md (new at repo root, 39 lines) — operator-facing introduction; load-bearing 'Before you click Update on flutter or weston' section"
    - "e2e/tests/weston-warning.spec.ts (101 lines) — 2 Playwright test cases: happy path (modal visible with keywords) + cancel path (no POST leaks)"
    - ".planning/phases/06-display-blackout-ux-checkpoint/deferred-items.md — logs 4 pre-existing e2e failures unrelated to this plan"
  modified:
    - ".planning/PROJECT.md — Key Decisions table gains one UX-01 row (append-only)"
    - "e2e/compose.test.yml — adds weston-stub service block + depends_on entry on hmi-update"

key-decisions:
  - "UX-01 locked as option (a) — README warning + Phase-5 pre-action toast (UI-08). Options (b) and (c) rejected with documented cost: option (b) requires prepared_digest state field + third per-row button + new endpoint + Stage 1/2 spec (doubles per-row surface area, violates 'one button per container' Core Value); option (c) requires a hmi-update.danger=true label operators must remember to set (substring detection is more robust by default)."
  - "README.md image choice: reuse the pre-seeded zot:5000/centroid-is/stub:latest tag for weston-stub instead of the plan's suggested alpine:latest. Reason: every other stub in compose.test.yml uses the zot-prefixed tag + pull_policy: never to stay offline-resilient; introducing alpine:latest would force a network pull and break the no-network-required compose-up invariant. Documented Rule 3 deviation."
  - "Verify-gate fallback: plan calls `make e2e` but that target fails on main before any test runs (global-setup.ts requires HMI_UPDATE_CRON=@every 5s which only the cron-fast override sets). Fell back to `make e2e-cron-fast`. Pre-existing breakage; logged in deferred-items.md as a Phase 7 Makefile cleanup candidate. My two tests pass green; 4 unrelated specs fail with documented failure shape."
  - "Spec naming: weston-warning.spec.ts (the file in this plan) is distinct from ui-flutter-warning.spec.ts (the Phase 5 Plan 05-05 spec that a parallel agent landed during this plan's execution). The two specs verify the same UI-08 surface from different angles — Phase 5's spec text-matches the byte-exact 'Display may flicker.' + '5–30 seconds' modal copy; Phase 6's spec asserts only the load-bearing keywords so a Phase 5 copy refinement doesn't break the UX-02 contract."

patterns-established:
  - "Contract-test commit shape: when a spec verifies an already-shipped implementation from a prior plan, commit as `test(...)` only (no GREEN feat follow-on). The plan's tdd=true attribute marks the spec as RED-first against future regressions, not against the current code shape. Documents the lifecycle difference between within-plan TDD (RED→GREEN in the same plan) and cross-plan contract TDD (one plan ships GREEN; a later plan adds the RED-against-future gate)."
  - "Documentation-as-deliverable phase pattern: a phase whose entire output is `.md` files + a single CI verification spec. Phase 6 ships zero Go/TS source changes; the brief's 'C4. TDD: verify → implement → verify → implement' constraint is honored via the spec (the verify loop) without an implement step. Future product-decision-checkpoint phases (open candidate: post-v1 operator-feedback intake) can adopt this shape."

requirements-completed:
  - UX-01  # Decision record: option (a) chosen; PROJECT.md Key Decisions row + CONTEXT.md rationale
  - UX-02  # README.md operator callout + weston-warning.spec.ts CI verification of UI-08

# Metrics
duration: 10min
completed: 2026-05-15
---

# Phase 6 Plan 01: Display-Blackout UX Checkpoint Summary

**UX-01 locked as option (a) — Phase 5 already ships the pre-action toast, so Phase 6 ships zero code changes; the operator-facing README seed + a flexible-selector Playwright contract test pin UI-08's substring-detection behaviour in CI for future-Phase-5-refactor protection.**

## Performance

- **Duration:** 10 min
- **Started:** 2026-05-15T11:29:08Z
- **Completed:** 2026-05-15T11:39:14Z
- **Tasks:** 2 (PROJECT.md row + README.md / weston-warning.spec.ts + weston-stub fixture)
- **Files created:** 3 (README.md, e2e/tests/weston-warning.spec.ts, deferred-items.md)
- **Files modified:** 2 (.planning/PROJECT.md, e2e/compose.test.yml)
- **Tests added:** 2 (both pass on first attempt against the real e2e stack)

## Accomplishments

### Task 1 — UX-01 decision record + README.md (commit `6f643f6`)

- **`.planning/PROJECT.md` Key Decisions table** gains one row for UX-01. The row names option (a) explicitly, enumerates the surface-area cost of rejected options (b) (prepared_digest schema field + third per-row button + new endpoint + Stage 1/2 spec) and (c) (per-service hmi-update.danger=true label discipline), and deep-links to `.planning/phases/06-.../06-CONTEXT.md` for the full rationale. Append-only: every prior row preserved verbatim.

- **`README.md` (NEW at repo root)** — 39 lines, operator-facing tone. Sections:
  - Title + one-paragraph intro (paraphrase of PROJECT.md "What This Is").
  - `## Quick start` — `docker compose up -d hmi-update` + forward reference to Phase 7's full install runbook (id -g docker step).
  - **`## Before you click Update on flutter or weston`** — the load-bearing UX-02 section. Names the 5-30s blackout window, the weston-cascade-onto-all-Wayland-clients gotcha, the Phase-5 confirmation toast, the Rollback safety net (<15s, local cache, no network), Force Pull as the recovery path. One-line pointer to `.planning/research/PITFALLS.md` Pitfall 5 for the full failure-mode analysis.
  - `## Container labels` — mirrors PROJECT.md's table; the five labels operators need to know.
  - `## Project pointers` — links to PROJECT.md, ROADMAP.md, API.md, .planning/research/.

  README.md is a **seed** — Phase 7 extends it with the full install runbook (DEPLOY-08 / Pitfall 9: `id -g docker`, compose deployment block, self-upgrade procedure pointer). This phase creates it; Phase 7 grows it.

### Task 2 — Playwright contract spec + weston-stub fixture (commit `bd32592`)

- **`e2e/tests/weston-warning.spec.ts`** — Playwright test.describe with 2 cases:
  - **Happy path:** click `Update weston-stub` → `page.getByRole('dialog')` becomes visible within 5s → contains `/display/i` AND `/flicker/i` → has a Cancel button. Asserts UI-08 fires for the weston substring case-insensitively.
  - **Cancel path:** intercepts `**/api/containers/*/update` with `page.route(...)`; clicks Update → modal visible → clicks Cancel → modal hidden → after a 500 ms grace window, `updatePostFired === false`. Proves the modal is the only gate between click and recreate and that Cancel does not leak through to the server.

  Selectors stay deliberately flexible — `page.getByRole('dialog')` matches WarningModal's `role="dialog"` wrapper; the text assertion is `toContainText(/display/i)` + `toContainText(/flicker/i)` rather than byte-matching the exact "Display may flicker." copy. This means a Phase 5 wording refinement won't break Phase 6's UX-02 verification — only a regression in the substring-detection predicate (or the modal-shows-at-all behaviour) does.

  Test names embed the trace: "shows display flicker warning before Update on weston-stub" + "cancel from warning toast does not trigger recreate on weston-stub". Describe block carries the load-bearing trace `(UX-02 → UI-08 contract)`.

- **`e2e/compose.test.yml`** — adds the `weston-stub` service block + a `depends_on` entry on `hmi-update`. The service uses the pre-seeded `zot:5000/centroid-is/stub:latest` image with `pull_policy: never` (same offline-resilient pattern as every other stub in the file; **deviation from plan**: the plan suggested `image: alpine:latest`, which would force a network pull and break the no-network-required invariant the other stubs enforce; reusing the zot tag matches the Makefile pre-seed contract). `hmi-update.watch: "true"` label so the Discoverer enumerates it at boot.

## Confirmation: Both Playwright Tests Green Against the Real e2e Stack

`make e2e-cron-fast` (the working e2e target; see deferred-items.md for why plain `make e2e` is broken on main) shows both new tests passing on first attempt:

```
✓  35 tests/weston-warning.spec.ts:41:3 › weston pre-action warning toast (UX-02 → UI-08 contract) › shows display flicker warning before Update on weston-stub (128ms)
✓  36 tests/weston-warning.spec.ts:65:3 › weston pre-action warning toast (UX-02 → UI-08 contract) › cancel from warning toast does not trigger recreate on weston-stub (820ms)
```

The 128ms / 820ms timings reflect the modal-render latency vs. the cancel-grace-window respectively. Both well under the 5s / 2s timeout budgets the spec sets.

## Task Commits

1. **Task 1: PROJECT.md Key Decisions row + README.md** — `6f643f6` (docs)
2. **Task 2: weston-warning Playwright spec + weston-stub compose fixture** — `bd32592` (test)
3. **Phase 6 deferred items list** — `91eb836` (docs)

## Files Created/Modified

- `.planning/PROJECT.md` — modified (Key Decisions table extended by one UX-01 row; existing rows preserved verbatim).
- `README.md` — new at repo root, 39 lines.
- `e2e/compose.test.yml` — modified (weston-stub service block added + depends_on entry).
- `e2e/tests/weston-warning.spec.ts` — new, 101 lines.
- `.planning/phases/06-display-blackout-ux-checkpoint/deferred-items.md` — new (logs 4 pre-existing e2e failures).

## Decisions Made

See `key-decisions` in the frontmatter for the full list. Headline calls:

- **UX-01 → option (a)** is the load-bearing product decision the phase exists to lock. The rationale enumerates rejected options' surface-area cost so a six-months-later reviewer asking "why didn't we ship option (b)?" finds the answer in PROJECT.md directly + the full text in CONTEXT.md.
- **weston-stub image: `zot:5000/centroid-is/stub:latest`** (not the plan's suggested `alpine:latest`). Rule 3 deviation; matches the offline-resilient pattern used by every other stub in the file. Documented in the service block's comment header.
- **Spec selectors stay flexible** — keyword-only text matching (`/display/i` + `/flicker/i`) instead of byte-matching the modal's exact copy. Phase 5 owns the copy; Phase 6 owns the contract.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking] PROJECT.md path: plan named `PROJECT.md` at repo root; actual file is `.planning/PROJECT.md`**

- **Found during:** Task 1 (running the plan's verify-gate `grep -F 'UX-01' PROJECT.md` from the repo root failed with `No such file or directory`).
- **Issue:** The plan's `<automated>` verify clauses use `grep -F 'UX-01' PROJECT.md` against the repo root, but the project's PROJECT.md lives at `.planning/PROJECT.md` (and only there — no root-level copy exists).
- **Fix:** Edited the canonical file at `.planning/PROJECT.md`. Verify-gate equivalents ran against the real path; all grep gates pass (`UX-01`, `option (a)`, `one button per container` all present).
- **Files modified:** `.planning/PROJECT.md` (commit `6f643f6`)

**2. [Rule 3 — Blocking] weston-stub image: plan suggested `alpine:latest`; reused `zot:5000/centroid-is/stub:latest` instead**

- **Found during:** Task 2 (inspecting e2e/compose.test.yml for the pattern other stubs use)
- **Issue:** The plan's `<action>` block proposes `image: alpine:latest` for the weston-stub service. But every other watched stub in compose.test.yml uses `zot:5000/centroid-is/stub:latest` with `pull_policy: never`; the Makefile e2e targets pre-seed busybox under that tag specifically so compose-up never hits the network. Introducing alpine:latest would force a docker pull from docker.io and break the no-network-required compose-up invariant. The pre-seed pattern is the established contract.
- **Fix:** Used `image: zot:5000/centroid-is/stub:latest` + `pull_policy: never` (matches stub-watched-container / timescaledb-stub / invalid-pattern-stub). The weston-stub block carries a comment header explicitly naming the deviation and why.
- **Files modified:** `e2e/compose.test.yml` (commit `bd32592`)

**3. [Rule 3 — Blocking] Verify target: plan named `make e2e`; that target is broken on main; fell back to `make e2e-cron-fast`**

- **Found during:** Task 2 verify gate (running `make e2e` failed in `e2e/global-setup.ts::waitForPollAdvance` before any test ran)
- **Issue:** `make e2e` fails before any test runs because `global-setup.ts` requires `last_poll_end` to advance within 15s, which only happens with the cron-fast override (`HMI_UPDATE_CRON=@every 5s`). The Makefile `e2e` target doesn't apply the override; only `e2e-cron-fast` does. The error message itself says so verbatim. This is pre-existing breakage; no Phase 6 code touched the cron config or the global-setup.
- **Fix:** Ran `make e2e-cron-fast` instead. Both new specs pass green; 4 unrelated pre-existing specs fail (logged in deferred-items.md). Documented as a Phase 7 Makefile cleanup candidate.
- **Files modified:** none (verify-only deviation)

### Pre-existing failures NOT fixed (scope boundary)

The `make e2e-cron-fast` run shows 4 failing specs unrelated to Phase 6 Task 2's changes. Per the SCOPE BOUNDARY rule, these are not in this plan's scope. Logged to `.planning/phases/06-display-blackout-ux-checkpoint/deferred-items.md`:

- `tests/detect-multiarch.spec.ts:73` — flip detection timing
- `tests/discovery.spec.ts:76` — events-path 5s window
- `tests/healthz-negative.spec.ts:109` — Pitfall 9 hint copy regression ("docker daemon unreachable" vs expected "docker socket permission denied")
- `tests/smoke.spec.ts:37` — empty-state row colspan="7" (test was authored against a fixture that produced zero watched containers; current fixture has 8+ stubs so the empty-state row correctly does not render)

## TDD Gate Compliance

Plan 06-01 carries `tdd="true"` on Task 2. The standard RED→GREEN→REFACTOR cycle does not apply cleanly here because the implementation (Phase 5 Plan 05-03 + 05-04's UI-08 substring detection + WarningModal mount) was already shipped before Phase 6 began. The spec is **contract-RED against future regressions, GREEN against today** — a deliberate cross-plan TDD shape rather than within-plan.

The commit is `test(06-01): weston-warning Playwright spec + weston-stub compose fixture` (commit `bd32592`). No following GREEN `feat(...)` commit exists because there is no Phase 6 production code to write. This is intentional and reflects the plan's `<success_criteria>` item 1.4: "UX-03 explicitly NOT shipped — option (a) chosen, so the state schema, third per-row button, and Stage 1/Stage 2 e2e spec do not land."

## Issues Encountered

- **None blocking.** The 4 pre-existing test failures observed during the verify gate are documented as deferred items; both new weston-warning tests pass on first attempt.
- A linter/parallel agent edited `e2e/compose.test.yml` mid-execution (comments only; service block content preserved). Left unmodified per the system reminder.
- Other agents landed parallel work during this plan (Phase 5 Plan 05-05's `ui-flutter-warning.spec.ts`; Phase 7's compose deployment blocks). Confirmed via `git log --all -- e2e/tests/weston-warning.spec.ts` that my spec file is owned by commit `bd32592` only.

## Verification

### Task 1 acceptance criteria (all green)

- `grep -F 'UX-01' .planning/PROJECT.md` → 1 match (Key Decisions row, append-only)
- `grep -F 'option (a)' .planning/PROJECT.md` → 1 match
- `grep -F 'one button per container' .planning/PROJECT.md` → 2 matches (Core Value verbatim + rationale paragraph)
- `test -f README.md` → present
- `grep -F 'Before you click Update on flutter' README.md` → 1 match (section heading)
- `grep -F '5-30 seconds' README.md` → 1 match (blackout window named)
- `grep -c 'weston' README.md` → 4 matches (section heading + cascade note + substring detection reference + table mention)
- `grep -F 'display may flicker' README.md` → 1 match (toast wording reference)
- `grep -F 'Rollback' README.md` → 1 substantive match + 1 in the labels table (safety-net mention)
- `grep -F 'Force Pull' README.md` → 1 match (recovery path)
- `grep -F 'Pitfall 5' README.md` → 1 match (deep link)
- `grep -F 'docker compose up -d hmi-update' README.md` → 1 match (quick start)

### Task 2 acceptance criteria (all green)

- `test -f e2e/tests/weston-warning.spec.ts` → present
- `grep -F 'shows display flicker warning' e2e/tests/weston-warning.spec.ts` → 1 match
- `grep -F 'cancel from warning toast' e2e/tests/weston-warning.spec.ts` → 1 match
- `grep -F 'UX-02' e2e/tests/weston-warning.spec.ts` + `grep -F 'UI-08' e2e/tests/weston-warning.spec.ts` → 2 matches each (describe block + comment header)
- `grep -c 'display' e2e/tests/weston-warning.spec.ts` → 6 occurrences (selector + assertion + comments)
- `grep -c 'flicker' e2e/tests/weston-warning.spec.ts` → 5 occurrences
- `grep -F 'weston-stub' e2e/compose.test.yml` → present (service block + depends_on entry + several comment references)
- `grep -F 'hmi-update.watch:' e2e/compose.test.yml` (under weston-stub) → present (label confirmed via `awk '/^  weston-stub:/,/healthcheck:/'`)
- `make e2e-cron-fast` (Rule 3 fallback from `make e2e`) → both new specs pass, 128 ms + 820 ms
- The spec runs in CI via the default Playwright config (`playwright.config.ts` testDir = `tests/` picks it up — no config change required)

### Plan-level success criteria

- [x] All 2 tasks executed
- [x] Each task committed individually (Task 1 = `6f643f6`, Task 2 = `bd32592`, deferred items = `91eb836`)
- [x] SUMMARY.md created (this file)
- [x] PROJECT.md gains a UX-01 Key Decisions row
- [x] README.md exists at repo root with the flicker callout section
- [x] e2e/tests/weston-warning.spec.ts exists and both tests pass green
- [x] e2e/compose.test.yml has a `weston-stub` service block with `hmi-update.watch: "true"`
- [x] No modifications to STATE.md or ROADMAP.md (confirmed via `git diff` against the per-task commits)

## Threat Model Compliance

- **T-06-01-01 (Info Disclosure — mismatched docs):** MITIGATED. Task 2's weston-warning.spec.ts is the empirical contract test; if Phase 5's UI-08 regresses or never shipped, the spec fails. Confirmed green via `make e2e-cron-fast`.
- **T-06-01-02 (Tampering — decision drift):** MITIGATED. The PROJECT.md Key Decisions row names UX-01 explicitly and deep-links to CONTEXT.md. A future contributor changing the decision requires a new phase (via `/gsd-phase insert`) — not a silent commit.
- **T-06-01-03 (Repudiation — no audit trail):** MITIGATED. Three-way trail: CONTEXT.md `<decisions>` (rejected-options cost enumeration) + PROJECT.md Key Decisions row + README operator callout. All three deep-link to each other.
- **T-06-01-04 (DoS — spec false positive):** ACCEPT (disposition unchanged). The spec uses `page.getByRole('dialog')` (tighter than the plan's flexible `[role=alertdialog], [role=dialog], .toast`) + the keyword filter `/display/i` + `/flicker/i`. False positive requires a non-WarningModal dialog to also contain both keywords, which is operationally implausible — there is only one role=dialog on the page at any time per the App.svelte shape.
- **T-06-01-05 (Elevation — surprise recreate):** MITIGATED. The cancel-path test asserts `updatePostFired === false` after a 500 ms grace window. Confirms no POST leaks through when the operator cancels.
- **Pitfall 5 (display blackout operator-surprise):** MITIGATED via README "Before you click Update on flutter or weston" + Phase 5 toast + Rollback safety net (all named in the README).

No new threat surface introduced. No threat_flags to surface.

## Open Notes for Phase 7

- README.md is the **seed** Phase 7 extends. Phase 7's docs work should append (NOT rewrite) sections for:
  - Full install runbook (Pitfall 9: `id -g docker` step, compose deployment block from DEPLOY-04).
  - Self-upgrade procedure (PROJECT.md already has the canonical 3-step recipe; mirror it).
  - Force-pull semantics for offline HMIs (DEPLOY-09 candidate).
- The `make e2e` target is broken on main (always fails in global-setup). Phase 7's Makefile cleanup pass should either delete the target or wire it to inherit the cron-fast override. Tracked in `.planning/phases/06-display-blackout-ux-checkpoint/deferred-items.md`.

## Open Notes for v2

- **Option (b) two-step prepare/switch** is the deferred candidate if operator feedback after v1 release indicates the pre-action toast is insufficient (e.g., "I want to pre-pull on Tuesday and switch on Friday during a maintenance window"). The schema field (`prepared_digest`) is a small surface; the UI button + endpoint are the bulk of the work. Tracked in CONTEXT.md `<deferred>` already.
- **Extending the substring-trigger list** beyond `flutter` / `weston` to include `kiosk` / `signage` etc. — flagged in CONTEXT.md `<deferred>`. Two paths: extend the array in `ui/src/lib/display-warning.ts`, or introduce a `hmi-update.display-drawing=true` label that the UI checks alongside the substring match. Defer until a real HMI ships with such a service.

## User Setup Required

None — documentation-only deliverables (PROJECT.md row + README); spec runs in CI against the existing compose stack.

## Manual Smoke

Skipped at execution time — Plan 06-01 is explicitly documentation-only under option (a), and the empirical UI behaviour is pinned by the Playwright spec which executed end-to-end against the real e2e stack (`docker compose -f e2e/compose.test.yml up -d` with the cron-fast override; UI served from the embedded Svelte bundle; both happy-path and cancel-path tests passed at 128 ms and 820 ms respectively). The CI contract test is a stricter version of the manual smoke listed in the plan's `<success_criteria>` item 5 — it asserts the same operator flow (click Update on weston-stub → see toast → click Cancel → no recreate) without requiring a human in the loop.

A real-browser manual smoke against an HMI-like stack remains a Phase 7 / pre-release activity per CLAUDE.md C4.

## Self-Check: PASSED

- File `.planning/PROJECT.md` UX-01 row present: FOUND
- File `README.md` at repo root: FOUND
- File `README.md` "Before you click Update on flutter or weston" section: FOUND
- File `e2e/tests/weston-warning.spec.ts` exists: FOUND
- File `e2e/compose.test.yml` weston-stub service block: FOUND
- File `.planning/phases/06-display-blackout-ux-checkpoint/deferred-items.md`: FOUND
- Commit `6f643f6` (Task 1) present in git log: FOUND
- Commit `bd32592` (Task 2) present in git log: FOUND
- Commit `91eb836` (deferred items) present in git log: FOUND
- `make e2e-cron-fast` shows both weston-warning tests as ✓ (passed): CONFIRMED
- No modifications to STATE.md: CONFIRMED (per phase-context constraint)
- No modifications to ROADMAP.md: CONFIRMED (per phase-context constraint)

---
*Phase: 06-display-blackout-ux-checkpoint*
*Plan: 01*
*Completed: 2026-05-15*
