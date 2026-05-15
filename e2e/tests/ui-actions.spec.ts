// Phase 5 plan 05-05 — UI-03 / UI-05 / UI-07 (per-row action wiring,
// safety labels, optimistic UI).
//
// Surface under test:
//   - UI-03: safety-label opt-out — rows whose container carries
//     hmi-update.allow-update=false hide the Update button and show
//     a lock icon. Same for allow-rollback. Force-pull is NEVER
//     label-gated.
//   - UI-05: success/error toasts surface after action POSTs. The
//     toast is the operator-visible signal of action completion.
//   - UI-07: optimistic UI — clicking Update on a non-flutter row
//     disables the button mid-flight (aria-busy=true / spinner) and
//     re-enables on response.
//
// IMPORTANT TEST-ENVIRONMENT CAVEAT (D-04-06-01):
//   The full Update / Rollback happy paths require ImagePull, which
//   fails in this test harness because the host docker daemon cannot
//   resolve `zot:5000` from its DNS context. Plan 04-07 is the
//   deferred fix. Until then, the action-completion sub-tests below
//   use `test.skip` with the same deferral message as
//   e2e/tests/update-flow.spec.ts, so they re-activate automatically
//   once 04-07 lands.
//
//   The label-gated (UI-03) and click-disables-button (UI-07 partial)
//   assertions are PURELY DOM-side — they exercise the UI's
//   optimistic disable path without depending on a successful
//   ImagePull. Those run unconditionally.
//
// RED-first per CLAUDE.md C4: authored ahead of Plan 05-02 (Row
// safety-label gating) + Plan 05-04 (action wiring). Now green
// against the wired Row.svelte allow-{update,rollback} branches and
// App.svelte's executeAction.

import { expect, test } from '@playwright/test';

test.describe('ui-actions — UI-03/05/07 surface', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    // Wait for both relevant rows to appear: stub-watched-container
    // (no labels — full action set) and timescaledb-stub (carries
    // allow-update=false + allow-rollback=false safety labels).
    await expect(
      page.locator('table tbody tr', { hasText: 'stub-watched-container' }),
    ).toBeVisible({ timeout: 15_000 });
    await expect(
      page.locator('table tbody tr', { hasText: 'timescaledb-stub' }),
    ).toBeVisible({ timeout: 15_000 });
  });

  test('UI-03 — timescaledb-stub hides Update + Rollback buttons (safety label opt-out) and shows lock icons', async ({
    page,
  }) => {
    const row = page.locator('table tbody tr', { hasText: 'timescaledb-stub' });

    // No Update button — UI-SPEC.md §4.4: when allow-update=false,
    // Row.svelte renders a lock icon span instead of the ActionButton.
    await expect(
      row.getByRole('button', { name: /^Update timescaledb-stub$/i }),
    ).toHaveCount(0);
    // No Rollback button — same logic for allow-rollback=false.
    await expect(
      row.getByRole('button', { name: /^Rollback timescaledb-stub$/i }),
    ).toHaveCount(0);

    // Force-pull is NEVER label-gated (it's read-only with respect to
    // the running container — no recreate). The button MUST be
    // present.
    await expect(
      row.getByRole('button', { name: /^force-pull timescaledb-stub$/i }),
    ).toBeVisible();

    // Lock icon affordance: Row.svelte renders a span with
    // aria-label="Update timescaledb-stub disabled by ..." and a
    // matching one for Rollback. The aria-label CONTAINS the
    // safety-label name so an SR user hears the reason.
    await expect(
      row.getByLabel(/Update timescaledb-stub disabled by hmi-update\.allow-update=false/i),
    ).toBeVisible();
    await expect(
      row.getByLabel(/Rollback timescaledb-stub disabled by hmi-update\.allow-rollback=false/i),
    ).toBeVisible();
  });

  test('UI-03 — stub-watched-container shows the full three-action set (no safety labels)', async ({
    page,
  }) => {
    const row = page.locator('table tbody tr', { hasText: 'stub-watched-container' });
    // All three action buttons visible. The aria-label is composed by
    // ActionButton.svelte as `${kind} ${service}`.
    await expect(
      row.getByRole('button', { name: /^update stub-watched-container$/i }),
    ).toBeVisible();
    await expect(
      row.getByRole('button', { name: /^rollback stub-watched-container$/i }),
    ).toBeVisible();
    await expect(
      row.getByRole('button', { name: /^force-pull stub-watched-container$/i }),
    ).toBeVisible();
  });

  test('UI-07 — Update click immediately disables the button (optimistic aria-busy)', async ({
    page,
  }) => {
    // This is the optimistic-UI prefix of UI-07: click → aria-busy.
    // The completion half (toast on success) requires a working
    // ImagePull which is deferred to Plan 04-07; see test.skip below.
    //
    // We DO want the POST to actually fire (so the optimistic disable
    // is real, not a fake). But we don't want the test to wait for
    // the (failing) ImagePull to time out the verify window — so we
    // fulfil the POST with a synthetic 500 response to short-circuit
    // the server-side work. The optimistic disable lives entirely in
    // App.svelte's executeAction try-block; it sets aria-busy BEFORE
    // awaiting the fetch.
    //
    // Use page.route's fulfil shape with a structured action-error
    // body so App.svelte's catch arm has something coherent to log
    // (it doesn't matter what the error looks like — the assertion is
    // about the click → aria-busy transition).
    await page.route('**/api/containers/stub-watched-container/update', async (route) => {
      if (route.request().method() !== 'POST') {
        await route.continue();
        return;
      }
      // Slight delay so the test has a window to observe aria-busy.
      await new Promise((r) => setTimeout(r, 300));
      await route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'synthetic_test_failure', reason: 'test' }),
      });
    });

    const row = page.locator('table tbody tr', { hasText: 'stub-watched-container' });
    const updateBtn = row.getByRole('button', {
      name: /^update stub-watched-container$/i,
    });
    await expect(updateBtn).toBeVisible();

    // Pre-click: button is idle.
    await expect(updateBtn).not.toHaveAttribute('aria-busy', 'true');

    await updateBtn.click();

    // aria-busy=true should appear during the in-flight window
    // (App.svelte adds the service to busyServices BEFORE awaiting
    // the postAction fetch — Row passes isBusy through to
    // ActionButton's aria-busy attribute). expect.poll re-checks
    // every ~100ms; the 1s timeout absorbs runner jitter.
    await expect
      .poll(
        async () => await updateBtn.getAttribute('aria-busy'),
        { timeout: 1_000 },
      )
      .toBe('true');

    // After the fulfilled error response, aria-busy clears in the
    // finally block. The error toast surfaces; we don't assert on
    // the toast content here (UI-05 below handles that under the
    // real-action skip).
    await expect
      .poll(
        async () => await updateBtn.getAttribute('aria-busy'),
        { timeout: 5_000 },
      )
      .not.toBe('true');
  });

  test('UI-05 — error toast surfaces with the verbatim server reason on action failure', async ({
    page,
  }) => {
    // Same synthetic-error stratagem: fulfil the POST with a
    // structured 4xx that App.svelte's catch arm translates to a
    // toast with `body: reason`. The toast text contract is "Update
    // failed" (or kind-keyed verb) + the verbatim reason.
    await page.route('**/api/containers/stub-watched-container/update', async (route) => {
      if (route.request().method() !== 'POST') {
        await route.continue();
        return;
      }
      await route.fulfill({
        status: 409,
        contentType: 'application/json',
        body: JSON.stringify({
          error: 'service_busy',
          reason: 'another action is in flight for stub-watched-container',
        }),
      });
    });

    const row = page.locator('table tbody tr', { hasText: 'stub-watched-container' });
    await row
      .getByRole('button', { name: /^update stub-watched-container$/i })
      .click();

    // The Toast lives inside a ToastContainer with role="status".
    // App.svelte's 409 branch fires a warning-kind toast for
    // service_busy; reason is "another action is in flight for ...".
    // We assert on the reason substring — it's the load-bearing
    // information the operator needs to act on.
    const toastRegion = page.locator('[role="status"]');
    await expect(toastRegion).toContainText(/another action is in flight/i, {
      timeout: 5_000,
    });
  });

  // DEFERRED to Plan 04-07 (D-04-06-01): daemon-level ImagePull
  // cannot resolve `zot:5000` from the host docker daemon's DNS
  // context. Activate this assertion once 04-07 lands the e2e
  // pull-path fix; until then the synthetic-error tests above cover
  // the UI surface.
  test.skip('UI-05 — success toast surfaces on Update happy path (deferred to Plan 04-07)', async ({
    page,
  }) => {
    const row = page.locator('table tbody tr', { hasText: 'stub-watched-container' });
    await row
      .getByRole('button', { name: /^update stub-watched-container$/i })
      .click();
    const toastRegion = page.locator('[role="status"]');
    // Success toast title is "Updated stub-watched-container"; body
    // includes a digest prefix.
    await expect(toastRegion).toContainText(/updated/i, { timeout: 60_000 });
  });

  test.skip('UI-05 — info toast on force-pull happy path (deferred to Plan 04-07)', async ({
    page,
  }) => {
    const row = page.locator('table tbody tr', { hasText: 'stub-watched-container' });
    await row
      .getByRole('button', { name: /^force-pull stub-watched-container$/i })
      .click();
    const toastRegion = page.locator('[role="status"]');
    await expect(toastRegion).toContainText(/re-pulled/i, { timeout: 60_000 });
  });
});
