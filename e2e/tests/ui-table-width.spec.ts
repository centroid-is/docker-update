// ui-table-width.spec.ts — TDD-first regression test for the
// table-width layout bug surfaced 2026-05-17:
//
// User reported: at a typical desktop viewport the table renders
// with empty space to the right of "last change" AND a horizontal
// scrollbar at the bottom of the wrapper. Status + actions
// columns are scrolled off-screen.
//
// Layout invariant under test:
//
//   - Wrapper width ≥ table content width  → NO horizontal scroll
//     bar; every column visible without scrolling.
//   - Wrapper width < table content width  → horizontal scroll bar
//     visible (the wrapper's overflow-x-auto kicks in); table is
//     content-width, NO phantom empty space inside the wrapper.
//
// Failure mode the bug reproduces:
//
//   - scrollWidth > clientWidth (scrollbar appears)
//   - AND last column (`actions`) bounding-box right edge falls
//     outside the wrapper's clientWidth (last column off-screen)
//   - AND viewport is wide enough that ALL columns should fit
//     (i.e. content fits in a normal desktop window — overflow
//     would be a CSS layout bug, not "your monitor is too small").

import { expect, test } from '@playwright/test';

const DESKTOP_VIEWPORT = { width: 1440, height: 900 };

test.describe('ui-table — width / scroll layout', () => {
  test.beforeEach(async ({ page }) => {
    await page.setViewportSize(DESKTOP_VIEWPORT);
    await page.goto('/');
    // Wait for at least one row to render so the table has its
    // measured layout.
    await expect(page.locator('table tbody tr').first()).toBeVisible({
      timeout: 15_000,
    });
  });

  test('UI-W1 — at 1440px viewport the wrapper does not horizontally scroll', async ({
    page,
  }) => {
    const measurements = await page
      .locator('div.overflow-x-auto', { has: page.locator('table') })
      .evaluate((wrapper) => {
        const table = wrapper.querySelector('table')!;
        return {
          wrapperClient: wrapper.clientWidth,
          wrapperScroll: wrapper.scrollWidth,
          tableOffset: (table as HTMLElement).offsetWidth,
        };
      });
    expect(
      measurements.wrapperScroll,
      `wrapper has horizontal overflow: scrollWidth=${measurements.wrapperScroll} clientWidth=${measurements.wrapperClient} tableOffset=${measurements.tableOffset}`,
    ).toBeLessThanOrEqual(measurements.wrapperClient);
  });

  test('UI-W2 — every column header is in the visible viewport at 1440px (no off-screen status/actions)', async ({
    page,
  }) => {
    const wrapperBox = await page
      .locator('div.overflow-x-auto', { has: page.locator('table') })
      .boundingBox();
    expect(wrapperBox, 'wrapper bounding-box must exist').not.toBeNull();

    const headers = page.locator('table thead th');
    const count = await headers.count();
    expect(count, 'expected 8 columns').toBe(8);

    for (let i = 0; i < count; i++) {
      const th = headers.nth(i);
      const text = (await th.textContent())?.trim() ?? `column ${i}`;
      const box = await th.boundingBox();
      expect(box, `header "${text}" must have a bounding box`).not.toBeNull();
      // Header's right edge must NOT fall outside the wrapper's
      // visible region (wrapper.x + wrapper.width). If it does, the
      // wrapper has scrolled the column off-screen — the exact bug
      // the user reported.
      const headerRight = box!.x + box!.width;
      const wrapperRight = wrapperBox!.x + wrapperBox!.width;
      expect(
        headerRight,
        `header "${text}" right edge (${headerRight}) must fit inside wrapper right edge (${wrapperRight})`,
      ).toBeLessThanOrEqual(wrapperRight + 1); // +1 for sub-pixel rounding
    }
  });
});
