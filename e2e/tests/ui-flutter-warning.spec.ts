// Phase 5 plan 05-05 — UI-08 (flutter/weston pre-action WarningModal).
//
// Surface under test:
//   - Clicking Update on a service whose name contains "weston" or
//     "flutter" (case-insensitive substring) opens a WarningModal
//     BEFORE any POST fires.
//   - Modal copy contains the verbatim "Display may flicker" sentence
//     (UI-SPEC.md §11; en-dash form "5–30 seconds" in the body).
//   - Cancel button dismisses the modal and emits NO POST.
//   - Continue button dismisses the modal and DOES emit exactly one
//     POST to /api/containers/weston-stub/update.
//   - Esc key dismisses the modal as Cancel (no POST).
//
// Load-bearing assertion: postCount. The cancel-no-POST invariant is
// the operator-protective contract — UI-08 exists because Update on
// flutter/weston blanks the HMI display for 5–30s, so an accidental
// click MUST NOT fire the recreate.
//
// RED-first per CLAUDE.md C4: authored ahead of Plans 05-03 + 05-04,
// now green against the wired WarningModal + handleAction gate.

import { expect, test } from '@playwright/test';

test.describe('ui-flutter-warning — UI-08 surface (weston-stub fixture)', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    // Wait for the weston-stub row to appear. Plan 05-05 Task 2 added
    // this fixture to e2e/compose.test.yml; the Discoverer enumerates
    // it via the docker events stream within seconds of boot.
    await expect(
      page.getByRole('button', { name: /^Update weston-stub$/i }),
    ).toBeVisible({ timeout: 15_000 });
  });

  test('Cancel in flicker modal does NOT fire POST /update', async ({ page }) => {
    // Intercept every POST to /api/containers/* and count how many
    // fire. The cancel path MUST keep postCount at 0 — that's the
    // operator-protective UI-08 contract.
    let postCount = 0;
    await page.route('**/api/containers/**', async (route) => {
      if (route.request().method() === 'POST') {
        postCount += 1;
      }
      await route.continue();
    });

    await page.getByRole('button', { name: /^Update weston-stub$/i }).click();

    // The WarningModal MUST surface BEFORE any POST. The dialog role
    // + aria-modal=true is set by WarningModal.svelte. Modal copy
    // assertions verify the operator-visible "Display may flicker"
    // headline AND the en-dash "5–30 seconds" body (UI-SPEC §11).
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible({ timeout: 5_000 });
    await expect(dialog).toContainText('Display may flicker');
    // Character-class tolerates hyphen / en-dash / em-dash; the
    // canonical UI-SPEC §11 form is the en-dash "5–30 seconds".
    // The prior unescaped `.` matched ANY character — "5X30 seconds"
    // and "500 seconds" would have passed (WR-05 in 05-REVIEW.md).
    await expect(dialog).toContainText(/5[-–—]30 seconds/);

    // Cancel and prove modal disappears.
    await dialog.getByRole('button', { name: /^Cancel$/i }).click();
    await expect(dialog).toBeHidden({ timeout: 2_000 });

    // Grace window for any racing POST — App.svelte's Cancel path
    // (handleCancel) clears pendingAction without invoking postAction,
    // so this must remain 0. The grace window is empirical: enough
    // time for a hypothetical racing fetch to complete its handshake;
    // if a POST were going to fire, it would have done so by now.
    await page.waitForTimeout(500);
    expect(
      postCount,
      'Cancel from WarningModal must NOT fire any POST to /api/containers/*',
    ).toBe(0);
  });

  test('Continue in flicker modal fires exactly one POST /update and closes the modal', async ({
    page,
  }) => {
    // Same intercept; we count POSTs and assert exactly one fires on
    // the Continue path. The server-side action body may not complete
    // successfully in this test environment (the daemon-DNS gap from
    // Plan 04-07 — daemon-level ImagePull cannot resolve `zot:5000`
    // from the host docker daemon's DNS context). That's fine for
    // UI-08: the spec verifies the UI gate's behaviour, not the
    // server's recreate outcome.
    let postCount = 0;
    let postUrl = '';
    await page.route('**/api/containers/**', async (route) => {
      if (route.request().method() === 'POST') {
        postCount += 1;
        postUrl = route.request().url();
      }
      await route.continue();
    });

    await page.getByRole('button', { name: /^Update weston-stub$/i }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible({ timeout: 5_000 });

    // Continue is the cyan primary button (data-primary in
    // WarningModal.svelte). Click it; the modal must close.
    await dialog.getByRole('button', { name: /^Continue$/i }).click();
    await expect(dialog).toBeHidden({ timeout: 5_000 });

    // Grace window — the POST is fired by handleConfirm immediately
    // after closing the modal; it's a non-blocking fire-and-await
    // path. The assertion below proves exactly one such POST landed
    // and that it targets the weston-stub update endpoint.
    await page.waitForTimeout(1_000);
    expect(postCount, 'Continue must fire exactly one POST').toBe(1);
    expect(
      postUrl,
      'Continue must POST to /api/containers/weston-stub/update',
    ).toMatch(/\/api\/containers\/weston-stub\/update$/);
  });

  test('Esc dismisses the flicker modal as Cancel (no POST)', async ({ page }) => {
    let postCount = 0;
    await page.route('**/api/containers/**', async (route) => {
      if (route.request().method() === 'POST') {
        postCount += 1;
      }
      await route.continue();
    });

    await page.getByRole('button', { name: /^Update weston-stub$/i }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible({ timeout: 5_000 });
    await expect(dialog).toContainText('Display may flicker');

    // Esc — focus-trap.ts dispatches a CustomEvent('cancel') which
    // WarningModal.svelte's `oncancel={onCancel}` consumes. The
    // operator-equivalence between Esc and Cancel is UI-SPEC §4.7
    // (modal accessibility contract).
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden({ timeout: 2_000 });

    await page.waitForTimeout(500);
    expect(
      postCount,
      'Esc from WarningModal must NOT fire any POST (Esc === Cancel)',
    ).toBe(0);
  });
});
