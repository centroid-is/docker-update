# Deferred items — Phase 05 web-ui-completeness

These items were discovered during execution of Plan 05-05 but are out of scope
for this plan. They are logged here per the GSD executor scope-boundary rule
(only auto-fix issues directly caused by the current task's changes).

## Pre-existing untracked file: `e2e/tests/weston-warning.spec.ts`

- **Discovered:** during Plan 05-05 execution (2026-05-15).
- **Origin:** untracked spec file in working tree at plan start; commit
  history shows it was created by an earlier session/draft and not committed.
- **Scope:** belongs to Phase 6 (UX-02 → UI-08 contract verification per its
  own header comment); covers the same surface as Plan 05-05 Task 3's
  `e2e/tests/ui-flutter-warning.spec.ts` but with Phase 6 framing.
- **Action:** left in place; Plan 05-05 ships the new `ui-flutter-warning.spec.ts`
  per the plan acceptance criteria. The leftover spec file may either be
  deleted in Phase 6 or refactored to delegate to the canonical Plan 05-05
  spec — defer the decision to Phase 6.
- **Risk:** none for Plan 05-05 (the two specs use different test names so
  they coexist; both will run under `make e2e` and assert the same modal).

## Pre-staged `.github/workflows/ci.yml` modifications

- **Discovered:** at Plan 05-05 start; unstaged diff adding `permissions:`,
  `concurrency:`, renaming job `go` → `build-test`, and adding Node.js
  setup steps.
- **Scope:** CI hardening (security + cache improvements). Not part of
  Plan 05-05's surface — Plan 05-05 covers handlers + Playwright specs,
  not CI workflow. Belongs to Phase 8 (CI/CD).
- **Action:** left untouched; Plan 05-05's `git commit` calls stage
  specific files only (no `git add .` or `git add -A`).

## Pre-staged compose diff for `weston-stub`

- **Discovered:** at Plan 05-05 start; `e2e/compose.test.yml` carried an
  unstaged diff adding a `weston-stub` service block.
- **Status:** the diff matches Plan 05-05 Task 2 acceptance criteria
  (weston-stub service block + `hmi-update.watch=true` label) verbatim
  except for the comment framing (mentions "Phase 6 plan 06-01"). Adopted
  as Plan 05-05 Task 2's contribution; the comment header rewritten to
  reference Plan 05-05.
