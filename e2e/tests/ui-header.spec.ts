// Phase 5 plan 05-05 — UI-04 + UI-06 (Header + 5s poll cadence).
//
// Surface under test:
//   - UI-04: Header shows "Refresh", "Watch now", and a last-poll
//     relative timestamp.
//   - UI-06: App polls /api/state every 5 seconds while idle. At
//     least 2 GETs MUST fire within a 6s observation window after
//     mount (covers the initial poll + at least one interval tick).
//   - Refresh button fires an immediate /api/state GET.
//
// RED-first per CLAUDE.md C4: authored ahead of Plan 05-02 (Header)
// and Plan 05-04 (poll loop). Now green against the wired Header +
// App.svelte's $effect setInterval poll.

import { expect, test } from '@playwright/test';

test.describe('ui-header — UI-04/06 surface', () => {
  test('UI-04 — header shows Refresh, Watch now, and a last-poll timestamp', async ({
    page,
  }) => {
    await page.goto('/');

    // Refresh + Watch now are aria-labelled buttons in Header.svelte.
    // The visible text is "Refresh" / "Watch now"; the aria-label is
    // verbose ("Refresh state from server" / "Trigger a poll right
    // now") per UI-SPEC.md §11. Both forms work as accessible-name
    // matches via Playwright's getByRole — we use the visible-text
    // form here for legibility.
    await expect(
      page.getByRole('button', { name: /^Refresh$/i }),
    ).toBeVisible();
    await expect(
      page.getByRole('button', { name: /^Watch now$/i }),
    ).toBeVisible();

    // Last-poll timestamp: Header.svelte renders a <span> with
    // aria-label="Last poll relative time". Its text is one of
    // "never", "Xs ago", "Xm Ys ago", or "Xh Ym ago" per
    // ui/src/lib/relative-time.ts. The "never" form is what shows
    // on the very first paint before the first poll completes.
    const ago = page.getByLabel('Last poll relative time');
    await expect(ago).toBeVisible();
    await expect(ago).toHaveText(/^(never|\d+s ago|\d+m \d+s ago|\d+h \d+m ago)$/);
  });

  test('UI-06 — 5s polling fires at least 2 GETs to /api/state in a 6s window', async ({
    page,
  }) => {
    // Intercept /api/state and count fetches. The handler runs on the
    // page-route boundary, so it captures ALL fetches from the page
    // (initial poll + setInterval ticks + any Refresh-driven poll).
    //
    // We use a fetchCount variable scoped to the test; route handlers
    // are functions, not closures over `let` declarations, so the
    // mutation is observed by the test body below.
    let fetchCount = 0;
    await page.route('**/api/state', async (route) => {
      fetchCount += 1;
      await route.continue();
    });

    await page.goto('/');

    // Wait 6 s — at 5s cadence we MUST see the initial poll (~t=0
    // after mount) AND at least one interval tick (~t=5s). The
    // assertion is "at least 2" rather than "exactly N" to absorb
    // jitter on slow CI runners.
    await page.waitForTimeout(6_000);
    expect(
      fetchCount,
      `expected ≥2 /api/state GETs within 6s window (5s cadence + initial poll); saw ${fetchCount}`,
    ).toBeGreaterThanOrEqual(2);
  });

  test('Refresh button fires an immediate /api/state GET', async ({ page }) => {
    let fetchCount = 0;
    await page.route('**/api/state', async (route) => {
      fetchCount += 1;
      await route.continue();
    });

    await page.goto('/');
    // Wait for the initial poll to settle so the click-driven fetch
    // is unambiguously the next one.
    await expect
      .poll(() => fetchCount, { timeout: 10_000 })
      .toBeGreaterThanOrEqual(1);
    const baseline = fetchCount;

    await page.getByRole('button', { name: /^Refresh$/i }).click();

    // Within 500ms a new /api/state GET MUST have fired. Playwright
    // expect.poll re-checks every 100ms by default; the 1000ms
    // timeout gives us 10 polls of headroom for slow runners.
    await expect
      .poll(() => fetchCount, {
        timeout: 1_000,
        message: 'Refresh click must trigger an immediate /api/state GET',
      })
      .toBeGreaterThan(baseline);
  });
});
