// Phase 6 plan 06-01 — UX-02 → UI-08 contract verification.
//
// This spec pins the Phase 5 pre-action WarningModal (UI-08, shipped in
// Plan 05-03 + wired in Plan 05-04 via ui/src/App.svelte::handleAction's
// requiresWarning gate) for service names containing the substring
// "weston" (case-insensitive). The locked Phase 6 UX-01 decision is
// option (a): leave Update as-is + README warning + Phase-5 pre-action
// toast — see .planning/phases/06-display-blackout-ux-checkpoint/06-CONTEXT.md.
// This spec is the CI guarantee that future Phase 5 refactors cannot
// silently regress the substring detection without UX-02 turning red.
//
// Selectors:
//   - role="dialog" + aria-modal="true" is the WarningModal wrapper
//     (ui/src/lib/WarningModal.svelte). The modal title "Display may
//     flicker." satisfies the case-insensitive "display.*flicker" regex
//     directly; the body adds "blank the HMI display for 5–30 seconds"
//     for further keyword reinforcement.
//   - Update button on each row carries aria-label="Update {service}"
//     (ui/src/lib/ActionButton.svelte) so we click via getByRole.
//   - Cancel button is a plain text "Cancel" inside the dialog.
//
// Fixture: weston-stub service in e2e/compose.test.yml, watched
// (hmi-update.watch=true), `zot:5000/centroid-is/stub:latest` image
// pre-seeded by the Makefile e2e targets — same shape as
// stub-watched-container so the discovery loop enumerates it.

import { expect, test } from '@playwright/test';

test.describe('weston pre-action warning toast (UX-02 → UI-08 contract)', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    // Wait for the container table to populate; the Discoverer's
    // initial enumeration lands within a few seconds of /api/state
    // being polled. The row whose aria-label-bearing Update button
    // names "weston-stub" is our load-bearing signal.
    await expect(
      page.getByRole('button', { name: /^Update weston-stub$/i }),
    ).toBeVisible({ timeout: 15_000 });
  });

  test('shows display flicker warning before Update on weston-stub', async ({
    page,
  }) => {
    // Click Update on the weston-stub row.
    await page
      .getByRole('button', { name: /^Update weston-stub$/i })
      .click();

    // The pre-action WarningModal (UI-08) MUST surface for the weston
    // substring match. Spec asserts only the load-bearing keywords —
    // exact copy ("Display may flicker." + body) is owned by Phase 5
    // (UI-SPEC.md §11) and a Phase 5 copy refinement should not break
    // this phase's UX-02 verification.
    const warning = page.getByRole('dialog');
    await expect(warning).toBeVisible({ timeout: 5_000 });
    await expect(warning).toContainText(/display/i);
    await expect(warning).toContainText(/flicker/i);

    // Operator must have a cancel affordance inside the dialog.
    await expect(
      warning.getByRole('button', { name: /^cancel$/i }),
    ).toBeVisible();
  });

  test('cancel from warning toast does not trigger recreate on weston-stub', async ({
    page,
  }) => {
    // Intercept all action POSTs so we can prove no update fired.
    let updatePostFired = false;
    await page.route('**/api/containers/*/update', async (route) => {
      updatePostFired = true;
      await route.continue();
    });

    await page
      .getByRole('button', { name: /^Update weston-stub$/i })
      .click();

    const warning = page.getByRole('dialog');
    await expect(warning).toBeVisible({ timeout: 5_000 });
    await expect(warning).toContainText(/display/i);
    await expect(warning).toContainText(/flicker/i);

    // Click Cancel — the modal must dismiss and no POST must fire.
    await warning.getByRole('button', { name: /^cancel$/i }).click();
    await expect(warning).toBeHidden({ timeout: 2_000 });

    // Small grace window in case a racing POST is dispatched after
    // dismissal (it must not be — App.svelte's confirmAction is the
    // only path that calls postAction, and Cancel sets pendingAction
    // back to null without invoking it).
    await page.waitForTimeout(500);
    expect(
      updatePostFired,
      'Cancel from WarningModal must NOT trigger /api/containers/weston-stub/update',
    ).toBe(false);
  });
});
