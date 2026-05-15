---
phase: 06-display-blackout-ux-checkpoint
reviewed: 2026-05-15T12:16:29Z
depth: standard
files_reviewed: 4
files_reviewed_list:
  - README.md
  - e2e/tests/weston-warning.spec.ts
  - e2e/compose.test.yml
  - .planning/PROJECT.md
findings:
  critical: 0
  warning: 2
  info: 3
  total: 5
status: issues_found
fixed:
  at: 2026-05-15T12:20:00Z
  scope: critical_warning
  warnings_fixed: 2
  info_deferred: 3
  fixes:
    - id: WR-01
      status: fixed
      commit: f1c0d85
      files: [README.md]
    - id: WR-02
      status: fixed_requires_human_verification
      commit: 0879c6e
      files: [e2e/tests/weston-warning.spec.ts]
      note: networkidle wait strategy needs verification via make e2e-cron-fast (next CI run with weston-stub stack)
---

# Phase 6: Code Review Report

**Reviewed:** 2026-05-15T12:16:29Z
**Depth:** standard
**Files Reviewed:** 4
**Status:** issues_found

## Summary

Phase 6 is genuinely documentation-only as advertised. The UX-01 decision is recorded in `.planning/PROJECT.md` Key Decisions with option (a) rationale and rejected-option surface-area cost. The README "Before you click Update on flutter or weston" callout names the 5-30s blackout window, the weston cascade, the Phase-5 pre-action toast, the Rollback safety net, and Force Pull recovery — operator-readable. The Playwright spec correctly uses substring assertions on `/display/i` and `/flicker/i` rather than byte-matching Phase 5's copy. The `weston-stub` fixture reuses the offline-resilient `zot:5000/centroid-is/stub:latest` pattern.

No critical defects. Two warnings (one minor wording inconsistency with downstream UX-02 impact, one cancel-path race-window assumption) and three info-level items. No production-code regressions observed in this phase's scope.

## Warnings

### WR-01: README "5-30 seconds" uses ASCII hyphen; WarningModal uses en-dash "5–30"

**File:** `README.md:95`
**Issue:** The README callout names the blackout window as `5-30 seconds` (ASCII hyphen, U+002D). The Phase 5 WarningModal copy (`ui/src/lib/WarningModal.svelte:131`) uses `5–30 seconds` (en-dash, U+2013), which `ui-flutter-warning.spec.ts` asserts as load-bearing. Operators reading the README and then comparing the modal copy will see textually different ranges, and any future spec authored against the README's wording (e.g., a Phase 5 refactor that reads README as the canonical "blackout window" string) will silently miss the modal copy. The Phase 6 plan explicitly claimed the README would "name the 5–30s blackout window" — the unicode mismatch breaks that contract.
**Fix:** Normalize the README to use the en-dash to match the modal:
```diff
-Recreating either of them blanks the screen for 5-30 seconds while the new
+Recreating either of them blanks the screen for 5–30 seconds while the new
```
**Severity rationale:** Warning rather than info because the en-dash is the load-bearing string asserted by `ui-flutter-warning.spec.ts`; a Phase 5 copy refinement that adopts the README ASCII form would silently break the UI spec. The mismatch is also user-visible to anyone copy-pasting the range into a release note.

### WR-02: `weston-warning.spec.ts` cancel-path uses a fixed 500 ms grace window without proof the route handler has run

**File:** `e2e/tests/weston-warning.spec.ts:92-96`
**Issue:** The cancel-path test asserts `updatePostFired === false` after `page.waitForTimeout(500)`. The 500 ms is unjustified — there is no causal link to a known maximum delay between Cancel-click and a hypothetical leaked POST. If the App.svelte handler dispatches an async POST that takes >500 ms to begin (e.g., behind a `requestIdleCallback`, a `setTimeout(...,1000)`, or an unrelated framework microtask backlog), the test passes spuriously. The mitigation is to assert against the route counter only after a definite signal — e.g., wait for the dialog to be hidden, then wait for a confirmed network-idle, or assert after a longer budget tied to a real timeout.
**Fix:**
```ts
await warning.getByRole('button', { name: /^cancel$/i }).click();
await expect(warning).toBeHidden({ timeout: 2_000 });
// Wait for network idle as a stricter proof no POST was dispatched.
await page.waitForLoadState('networkidle', { timeout: 2_000 });
expect(
  updatePostFired,
  'Cancel from WarningModal must NOT trigger /api/containers/weston-stub/update',
).toBe(false);
```
**Severity rationale:** Warning rather than info because this is the load-bearing cancel-path assertion for UX-02; a false-pass here defeats the contract test's purpose. The current 500 ms window is below the threshold at which a delayed-dispatch regression would surface.

## Info

### IN-01: README references `PITFALLS.md` without path qualifier in one location

**File:** `README.md:77`
**Issue:** Line 77 says `see PITFALLS.md Pitfall 6 and ACT-09`, but line 105 in the same file uses the fully qualified `.planning/research/PITFALLS.md`. A file named `PITFALLS.md` does not exist at the repo root — only at `.planning/research/PITFALLS.md`. Operators clicking or grepping for `PITFALLS.md` from the repo root will not find it.
**Fix:**
```diff
-recreated — it would commit suicide mid-recreate, see PITFALLS.md Pitfall 6
+recreated — it would commit suicide mid-recreate, see
+`.planning/research/PITFALLS.md` Pitfall 6
```
**Severity rationale:** Info because the impact is operator-confusion, not a behavioral defect. The line is already inside a parenthetical aside.

### IN-02: Stale `LICENSE` reference in README

**File:** `README.md:138-141`
**Issue:** Section `## License` says `MIT — see LICENSE`. A LICENSE file does exist at the repo root, but its content was not verified to be MIT, and the SUMMARY note "(Phase 8 publish flow lands the file alongside the GHCR release)" contradicts the fact that LICENSE already exists. If the existing LICENSE is not MIT, the README is incorrect; if it is, the SUMMARY note is misleading.
**Fix:** Either remove the parenthetical aside if LICENSE is already in place, or verify and reconcile. Recommended:
```diff
-MIT — see `LICENSE` (Phase 8 publish flow lands the file alongside the GHCR
-release).
+MIT — see [`LICENSE`](./LICENSE).
```
**Severity rationale:** Info because legal/operational correctness of the license file is out of this phase's scope; the README text is the only artifact under review.

### IN-03: `weston-warning.spec.ts` happy-path test does not assert the post-Continue path

**File:** `e2e/tests/weston-warning.spec.ts:41-63`
**Issue:** The happy-path test asserts the modal appears with the correct keywords and a Cancel button. It does not assert that clicking a Continue/Confirm button actually dispatches the `POST /api/containers/weston-stub/update`. This is a gap — the contract is "modal-first, then POST"; the spec verifies "modal-first" but not "then POST." The Phase 5 `ui-flutter-warning.spec.ts` (per the 06-01-SUMMARY commentary) covers the Continue path, but Phase 6 leaves it unasserted. If a Phase 5 refactor inverts the predicate (e.g., shows the modal but never fires the POST on Continue), Phase 6's spec stays green.
**Fix:** Either add a third test ("Continue from warning toast triggers recreate on weston-stub") with the same `page.route` interception confirming `updatePostFired === true`, or document explicitly in the spec header that the Continue path is owned by Phase 5's `ui-flutter-warning.spec.ts`.
**Severity rationale:** Info because the Phase 5 sibling spec already covers the path, and the Phase 6 contract focus was the "modal must appear" + "Cancel must not leak" pair. Recording the gap so a future maintainer doesn't accidentally rely on Phase 6 covering both directions.

---

_Reviewed: 2026-05-15T12:16:29Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_

## REVIEW COMPLETE
