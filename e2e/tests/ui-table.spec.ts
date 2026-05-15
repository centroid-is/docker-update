// Phase 5 plan 05-05 — UI-01, UI-02, UI-09 contract verification.
//
// Surface under test:
//   - UI-01: 7-column table with the locked column slugs.
//   - UI-02: at least one row appears for the stub container; digests
//            render in a monospace font.
//   - UI-09: CopyButton writes the FULL digest (sha256:<64 hex>) to the
//            clipboard — not the 19-char truncated display form.
//
// RED-first per CLAUDE.md C4: this spec was authored ahead of Plans
// 05-01..04 and now runs green against the wired UI. The clipboard
// assertion depends on the playwright.config.ts permission grant
// (Plan 05-05 Task 2 — `permissions: ['clipboard-read',
// 'clipboard-write']`).
//
// Discovery wait pattern: smoke.spec.ts uses an explicit table+headers
// assertion; we follow the same shape so a first-paint table-empty
// state does not flake the row-count assertion. The `stub-watched-
// container` row is the load-bearing presence signal (it's pre-seeded
// by docker compose at e2e boot and visible to the Discoverer within
// ~5s — discovery.spec.ts proved this).

import { expect, test } from '@playwright/test';

// Column slugs verbatim from .planning/phases/01-walking-skeleton-test-harness/01-UI-SPEC.md.
// Order is load-bearing — operators read left-to-right by muscle memory
// once they've used the tool more than once.
const COLUMN_SLUGS = [
  'container',
  'image:tag',
  'current digest',
  'available digest',
  'previous digest',
  'status',
  'actions',
] as const;

test.describe('ui-table — UI-01/02/09 surface', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    // Wait for the Discoverer's first enumeration to surface in the
    // 5s poll. stub-watched-container is the canonical "always-present"
    // fixture from e2e/compose.test.yml.
    await expect(
      page.locator('table tbody tr', { hasText: 'stub-watched-container' }),
    ).toBeVisible({ timeout: 15_000 });
  });

  test('UI-01 — table renders the 7 locked column slugs in order', async ({
    page,
  }) => {
    const headerTexts = await page.locator('table thead th').allTextContents();
    const normalized = headerTexts.map((t) => t.trim().toLowerCase());
    // expect.toEqual on a tuple — order-sensitive by design (UI-SPEC
    // and operator-muscle-memory both depend on the left-to-right
    // order being stable).
    expect(
      normalized,
      'Table must have exactly the seven UI-SPEC column slugs, in order',
    ).toEqual([...COLUMN_SLUGS]);
  });

  test('UI-02 — at least one row appears for the stub container; digest cells use a monospace font', async ({
    page,
  }) => {
    // Row presence: stub-watched-container is the load-bearing fixture.
    // The discovery loop enumerates it via the docker events stream
    // within seconds of boot (discovery.spec.ts pins this).
    const rows = page.locator('table tbody tr');
    await expect(rows).not.toHaveCount(0);
    // The empty-state row uses colspan=7 and contains the "No watched
    // containers yet" sentinel. Asserting at-least-one regular row
    // requires the row count > 1 (the empty-state row is suppressed
    // when containers populate, but belt-and-braces against a
    // table-empty regression).
    const stubRow = page.locator('table tbody tr', {
      hasText: 'stub-watched-container',
    });
    await expect(stubRow).toHaveCount(1);

    // Digest cells render in a monospace font — UI-SPEC.md §4.3
    // requires ui-monospace / SFMono-Regular / Menlo / Consolas. The
    // Row.svelte markup uses `class="font-mono text-xs"` on the digest
    // span; Tailwind v4 expands font-mono into the standard mono stack.
    // We assert the computed font-family substring; "mono" appears in
    // every typical resolution (ui-monospace, SFMono-Regular, Menlo
    // — all contain it as either token or hyphenated form). Belt-and-
    // braces against a future class refactor.
    const digestSpan = stubRow.locator('span.font-mono').first();
    // The stub container's current_digest may legitimately be empty
    // during the very first poll window. Wait for it to populate
    // before asserting on the font-family.
    await expect(digestSpan).toBeVisible({ timeout: 10_000 });
    const fontFamily = await digestSpan.evaluate(
      (el) => getComputedStyle(el).fontFamily,
    );
    expect(
      fontFamily.toLowerCase(),
      `digest cell font-family must contain "mono" (got ${fontFamily})`,
    ).toMatch(/mono/);
  });

  test('UI-09 — CopyButton writes the FULL digest to the clipboard (not the truncated display form)', async ({
    page,
  }) => {
    // Load /api/state directly to know what the canonical full digest
    // is — the Row truncates to 19 chars (sha256: + 12 hex + ellipsis)
    // for display per Row.svelte::shortDigest, but the CopyButton
    // payload MUST be the full sha256:<64 hex>. This is the UI-09
    // assertion's load-bearing distinction.
    const stateResp = await page.request.get('/api/state');
    expect(stateResp.ok()).toBe(true);
    const state = (await stateResp.json()) as {
      containers: Record<string, { current_digest?: string }>;
    };
    const fullDigest = state.containers['stub-watched-container']?.current_digest;
    expect(
      fullDigest,
      'stub-watched-container.current_digest must be populated by the time the row is visible',
    ).toBeDefined();
    expect(fullDigest!, 'digest must be sha256-prefixed 64-hex form').toMatch(
      /^sha256:[a-f0-9]{64}$/,
    );

    // Find the CopyButton next to the current digest of the stub row
    // and click it. The button's aria-label is `Copy current digest`
    // (CopyButton.svelte::ariaLabel composes "Copy " + the `label`
    // prop, and Row.svelte passes `label="current digest"`).
    const stubRow = page.locator('table tbody tr', {
      hasText: 'stub-watched-container',
    });
    const copyBtn = stubRow.getByRole('button', {
      name: /copy current digest/i,
    });
    await expect(copyBtn).toBeVisible();
    await copyBtn.click();

    // Read back from the clipboard via Playwright's clipboard
    // permission (granted in playwright.config.ts). The page.evaluate
    // boundary is the only way to reach the page-side clipboard API.
    const copied = await page.evaluate(() => navigator.clipboard.readText());
    expect(
      copied,
      'CopyButton must write the FULL sha256:<64 hex> digest — not the truncated 19-char display form',
    ).toBe(fullDigest);

    // Negative: the copied payload must NOT be the truncated display
    // form. Belt-and-braces against a future regression that would
    // pass the displayed text through to navigator.clipboard.writeText.
    expect(copied).not.toMatch(/…$/);
    expect(copied.length).toBe('sha256:'.length + 64);
  });
});
