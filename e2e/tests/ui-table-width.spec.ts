// ui-table-width.spec.ts — layout invariants for the 8-column table.
//
// User-reported bug 2026-05-17 was page-level horizontal scroll:
// shrink the viewport and the WHOLE document slides sideways instead
// of just the table wrapper scrolling internally. The contract is:
//
//   - body.scrollWidth ≤ viewport at every viewport width — page
//     NEVER horizontally scrolls (wrapper internal scroll is fine).
//   - On a wide desktop (1920 px), every column is visible without
//     internal scroll either — table fits comfortably.
//
// Internal wrapper scroll on medium viewports (1440 px and similar)
// is expected when content width exceeds wrapper width; the badge
// label widths are data-dependent ("error" is narrower than "update
// available") so the table's total width varies with state.

import { expect, test } from '@playwright/test';

async function measure(page: import('@playwright/test').Page) {
  return page.evaluate(() => {
    const wrapper = document.querySelector('div.overflow-x-auto') as HTMLElement | null;
    return {
      viewport: window.innerWidth,
      bodyScroll: document.body.scrollWidth,
      docElScroll: document.documentElement.scrollWidth,
      wrapperClient: wrapper?.clientWidth ?? -1,
      wrapperScroll: wrapper?.scrollWidth ?? -1,
    };
  });
}

async function measureAtViewport(
  page: import('@playwright/test').Page,
  width: number,
) {
  await page.setViewportSize({ width, height: 900 });
  await page.goto('/');
  await expect(page.locator('table tbody tr').first()).toBeVisible({
    timeout: 15_000,
  });
  await page.waitForTimeout(150); // let layout settle
  return measure(page);
}

test.describe('ui-table — width / scroll layout', () => {
  test('UI-W1 — at 1920 px viewport the wrapper does NOT scroll (every column fits)', async ({
    page,
  }) => {
    const m = await measureAtViewport(page, 1920);
    expect(
      m.wrapperScroll,
      `wrapper overflowed at 1920 px viewport: scrollWidth=${m.wrapperScroll} clientWidth=${m.wrapperClient}`,
    ).toBeLessThanOrEqual(m.wrapperClient + 1);
  });

  test('UI-W2 — at 1920 px viewport every column header is inside the wrapper bounding box', async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1920, height: 900 });
    await page.goto('/');
    await expect(page.locator('table tbody tr').first()).toBeVisible({
      timeout: 15_000,
    });
    await page.waitForTimeout(150);

    const wrapperBox = await page
      .locator('div.overflow-x-auto', { has: page.locator('table') })
      .boundingBox();
    expect(wrapperBox).not.toBeNull();

    const headers = page.locator('table thead th');
    const count = await headers.count();
    expect(count, 'expected 8 columns').toBe(8);

    for (let i = 0; i < count; i++) {
      const th = headers.nth(i);
      const text = (await th.textContent())?.trim() ?? `column ${i}`;
      const box = await th.boundingBox();
      expect(box, `header "${text}" must have a bounding box`).not.toBeNull();
      const headerRight = box!.x + box!.width;
      const wrapperRight = wrapperBox!.x + wrapperBox!.width;
      expect(
        headerRight,
        `header "${text}" right edge (${headerRight}) must fit inside wrapper right edge (${wrapperRight})`,
      ).toBeLessThanOrEqual(wrapperRight + 1);
    }
  });

  test('UI-W3 — page does NOT horizontally scroll at any viewport (600 / 1000 / 1440 / 1920)', async ({
    page,
  }) => {
    // The load-bearing invariant from the bug report: regardless of how
    // narrow the viewport gets, the document body must never exceed
    // viewport width. Internal wrapper scroll handles the table being
    // wider than the visible area at narrow viewports.
    for (const w of [600, 1000, 1440, 1920]) {
      const m = await measureAtViewport(page, w);
      expect(
        m.bodyScroll,
        `body.scrollWidth > viewport at width=${w}: bodyScroll=${m.bodyScroll} viewport=${m.viewport} (wrapper client=${m.wrapperClient} scroll=${m.wrapperScroll})`,
      ).toBeLessThanOrEqual(m.viewport + 1);
    }
  });
});
