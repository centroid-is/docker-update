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

// Column slugs — renamed in the P9-N follow-up UI overhaul. The original
// UI-SPEC names ("container", "image:tag", "current digest", "available
// digest", "previous digest") were the noun-form of each digest field;
// the new slugs frame each column by operator intent (rollback IS the
// rollback target; "last change" is the wall-clock answer to "when did
// this last move"). Order is still load-bearing for muscle memory.
const COLUMN_SLUGS = [
  'service',
  'image',
  'current',
  'available',
  'rollback',
  'last change',
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

  test('UI-01 — table renders the 8 column slugs in order', async ({
    page,
  }) => {
    const headerTexts = await page.locator('table thead th').allTextContents();
    const normalized = headerTexts.map((t) => t.trim().toLowerCase());
    // expect.toEqual on a tuple — order-sensitive by design (operator
    // muscle memory depends on the left-to-right order being stable).
    expect(
      normalized,
      'Table must have exactly the eight column slugs, in order',
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
    // The empty-state row uses colspan=8 and contains the "No watched
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
    // during the very first poll window — the Discoverer enumerates
    // the container (Stopped/image/tag) within ~5s of boot, then
    // ContainerInspect populates current_digest from RepoDigests[0]
    // on the next /api/state read. Allow up to 20s for both legs to
    // land in the UI (cron-fast: @every 5s; production cron is
    // hourly so this test only makes sense under cron-fast).
    await expect(digestSpan).toBeVisible({ timeout: 20_000 });
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
    // We test against `available_digest` rather than `current_digest`
    // because the test environment uses `docker tag busybox:latest
    // zot:5000/centroid-is/stub:latest` (Makefile e2e pre-seed) — the
    // container's docker-inspected RepoDigests[0] still references
    // docker.io/library/busybox, not the local zot retag, so
    // current_digest is environment-empty until an Update fires.
    // available_digest, however, is populated by the cron poller's
    // crane.Digest call against the zot manifest pushed by
    // global-setup.ts (oras push to localhost:15000/centroid-is/stub:latest).
    // The UI-09 invariant ("CopyButton writes the FULL digest, not the
    // 19-char truncated display form") holds identically against
    // either field — both render through the same CopyButton +
    // shortDigest pipeline in Row.svelte.
    let fullDigest: string | undefined;
    const deadline = Date.now() + 20_000;
    while (Date.now() < deadline) {
      const stateResp = await page.request.get('/api/state');
      if (stateResp.ok()) {
        const state = (await stateResp.json()) as {
          containers: Record<
            string,
            { current_digest?: string; available_digest?: string }
          >;
        };
        const c = state.containers['stub-watched-container'];
        const d = c?.current_digest ?? c?.available_digest;
        if (d && /^sha256:[a-f0-9]{64}$/.test(d)) {
          fullDigest = d;
          break;
        }
      }
      await page.waitForTimeout(500);
    }
    expect(
      fullDigest,
      'stub-watched-container must have current_digest OR available_digest as sha256:<64 hex> within 20s (oras push + cron tick)',
    ).toBeDefined();

    // Locate ANY CopyButton on the stub-watched-container row; the
    // button's value prop is the FULL digest regardless of which cell
    // (current/available/previous). We pick the first visible one.
    const stubRow = page.locator('table tbody tr', {
      hasText: 'stub-watched-container',
    });
    const copyBtn = stubRow
      .getByRole('button', { name: /^copy (current|available|previous) digest$/i })
      .first();
    await expect(copyBtn).toBeVisible({ timeout: 10_000 });
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
