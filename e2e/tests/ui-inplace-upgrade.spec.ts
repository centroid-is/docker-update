// Phase 5 plan 05-05 — UI-10 (in-place upgrade) + Pitfall 8 byte-level
// proof.
//
// Surface under test:
//   - After rebuilding the binary mid-test, the running stack serves a
//     NEW /assets/<hash>.js (different content hash). The new asset:
//       * returns 200
//       * Cache-Control contains `immutable`
//       * Content-Type is `application/javascript; charset=utf-8`
//   - The OLD /assets/<oldhash>.js URL returns 404 — NOT a fallback to
//     index.html. The 404 body MUST NOT contain `<html`.
//
// This is the load-bearing Pitfall 8 spec. The Go unit tests in
// internal/api/handlers_assets_test.go pin the same invariants at the
// handler layer; this spec proves them end-to-end through a real
// browser request after a real binary swap.
//
// PERFORMANCE: this spec rebuilds the Svelte bundle, rebuilds the Go
// binary inside a multi-stage Dockerfile, and recreates the
// hmi-update compose service. Total wall-clock is ~35–80s per run.
// Tagged @inplace-upgrade so CI can elect to run it under a separate
// `npx playwright test --grep @inplace-upgrade` invocation if the
// shared test stack's wall-clock budget is tight.
//
// Test mode: `test.describe.configure({ mode: 'serial' })` — the
// rebuild touches shared compose state, so concurrent tests in this
// describe would corrupt each other. Playwright config workers=1
// already serialises across files, but the .serial guarantee inside
// this file is defensive.
//
// RED-first per CLAUDE.md C4: the rebuildAndRestart fixture is new
// in Plan 05-05 Task 2; this spec is the first consumer.

import { rmSync, writeFileSync, existsSync, readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

import { expect, test } from '@playwright/test';

import { rebuildAndRestart } from '../fixtures/rebuild-binary';

// ESM-compatible repo-root resolution (e2e/ is "type": "module"; no
// __dirname global). This file lives at e2e/tests/ui-inplace-upgrade.spec.ts;
// repo root is two levels up.
const SPEC_DIR = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = join(SPEC_DIR, '..', '..');
// MARKER_FILE must match the import path injected into App.svelte
// (`./lib/build-marker`). Vite resolves the import relative to App.svelte
// at `ui/src/App.svelte`, so `./lib/build-marker` → `ui/src/lib/build-marker.ts`.
// Keep both ends of the import contract aligned here; CR-01 in
// 05-REVIEW.md called out the prior drift.
const MARKER_FILE = join(REPO_ROOT, 'ui', 'src', 'lib', 'build-marker.ts');

test.describe.configure({ mode: 'serial' });

test.describe('@inplace-upgrade ui-inplace-upgrade — UI-10 + Pitfall 8 byte-level proof', () => {
  // Generous timeout: rebuild + restart + /healthz poll can run to 80s
  // on a cold dev machine; Playwright's default 30s is too tight.
  test.setTimeout(150_000);

  // Marker payload captured at module-load so we can restore the
  // ui/src/build-marker.ts file on teardown even if the test body
  // fails partway through.
  let markerExistedBefore = false;
  let markerOriginalContent: string | undefined;

  test.beforeAll(() => {
    markerExistedBefore = existsSync(MARKER_FILE);
    if (markerExistedBefore) {
      // Read prior content so we can restore exactly on afterAll.
      markerOriginalContent = readFileSync(MARKER_FILE, 'utf8');
    }
  });

  test.afterAll(() => {
    // Restore the marker file to its pre-test state. If it didn't
    // exist before, remove the one we wrote.
    if (!markerExistedBefore && existsSync(MARKER_FILE)) {
      rmSync(MARKER_FILE);
    } else if (markerExistedBefore && markerOriginalContent !== undefined) {
      writeFileSync(MARKER_FILE, markerOriginalContent, 'utf8');
    }
  });

  test('in-place upgrade serves new bundle with immutable Cache-Control + JS MIME; old asset 404s with NO index.html fallback', async ({
    page,
    request,
  }) => {
    // ── Step 1: capture the OLD bundle URL ─────────────────────────
    await page.goto('/');
    const oldUrl = await page.evaluate(() => {
      const scripts = Array.from(document.scripts);
      const moduleScript = scripts.find((s) =>
        s.src.includes('/assets/') && s.src.endsWith('.js'),
      );
      return moduleScript?.src ?? '';
    });
    expect(oldUrl, 'index.html must reference a /assets/*.js module bundle').toMatch(
      /\/assets\/[^/]+\.js$/,
    );

    // Sanity-pre-check: the old asset MUST currently return 200 +
    // application/javascript + immutable. If this fails, Plan 05-05
    // Task 1 didn't ship correctly — the test wouldn't be able to
    // make any byte-level statement about the post-rebuild state.
    const preResp = await request.get(oldUrl);
    expect(preResp.status(), 'pre-rebuild: old URL must currently 200').toBe(200);
    const preCT = (preResp.headers()['content-type'] ?? '').toLowerCase();
    expect(preCT, 'pre-rebuild: Content-Type baseline').toBe(
      'application/javascript; charset=utf-8',
    );
    const preCC = (preResp.headers()['cache-control'] ?? '').toLowerCase();
    expect(preCC, 'pre-rebuild: Cache-Control baseline').toContain('immutable');

    // ── Step 2: write a marker file so the next Vite build emits a
    //    DIFFERENT bundle hash. Vite hashes content; identical source
    //    bytes produce identical hashes — without this, the rebuild
    //    would be a no-op and the spec's "new hash" assertion would
    //    fail.
    const marker = `// Plan 05-05 ui-inplace-upgrade.spec.ts marker. Auto-generated;
// removed in afterAll. The timestamp content here forces Vite to
// emit a new /assets/<hash>.js so the spec can verify Pitfall 8
// invariants against the new bundle URL.
export const BUILD_MARKER = ${Date.now()};
`;
    writeFileSync(MARKER_FILE, marker, 'utf8');

    // Import the marker from App.svelte so it actually appears in the
    // bundle (an unimported module is tree-shaken). We add the import
    // line conservatively — only if not already present.
    const appSvelte = join(REPO_ROOT, 'ui', 'src', 'App.svelte');
    const appOriginal: string = readFileSync(appSvelte, 'utf8');
    let appPatched = appOriginal;
    if (!appOriginal.includes('./lib/build-marker')) {
      // Inject a no-op import + reference. We only do this for the
      // duration of this test; afterAll restores the original. The
      // injection lives inside the <script lang="ts"> block — find
      // the first line after the opening <script> tag and append.
      appPatched = appOriginal.replace(
        /(<script lang="ts">\s*\n)/,
        `$1  // Plan 05-05 in-place-upgrade marker import (test-only; removed by spec afterAll).
  import { BUILD_MARKER } from './lib/build-marker';
  void BUILD_MARKER;
`,
      );
      writeFileSync(appSvelte, appPatched, 'utf8');
    }

    try {
      // ── Step 3: rebuild + restart. This is the load-bearing step:
      //    new bundle hash → new /assets/<hash>.js → server-side
      //    Cache-Control + MIME contract must hold against it.
      await rebuildAndRestart();

      // ── Step 4: reload the page and capture the NEW bundle URL.
      await page.reload({ waitUntil: 'networkidle' });
      const newUrl = await page.evaluate(() => {
        const scripts = Array.from(document.scripts);
        const moduleScript = scripts.find((s) =>
          s.src.includes('/assets/') && s.src.endsWith('.js'),
        );
        return moduleScript?.src ?? '';
      });
      expect(newUrl, 'post-rebuild index.html must reference a /assets/*.js').toMatch(
        /\/assets\/[^/]+\.js$/,
      );
      expect(
        newUrl,
        'post-rebuild bundle URL MUST differ from old URL (new content hash)',
      ).not.toBe(oldUrl);

      // ── Step 5: new asset must carry the Pitfall 8 invariants.
      const newResp = await request.get(newUrl);
      expect(newResp.status(), 'new asset must return 200').toBe(200);
      const newCC = (newResp.headers()['cache-control'] ?? '').toLowerCase();
      expect(
        newCC,
        'new asset Cache-Control MUST contain `immutable` (Pitfall 8)',
      ).toContain('immutable');
      expect(newCC).toBe('public, max-age=31536000, immutable');
      const newCT = (newResp.headers()['content-type'] ?? '').toLowerCase();
      expect(
        newCT,
        'new asset Content-Type MUST be application/javascript; charset=utf-8 (Pitfall 8 — distroless has no /etc/mime.types)',
      ).toBe('application/javascript; charset=utf-8');

      // ── Step 6: old asset must 404 — NOT fall back to index.html.
      //    This is THE Pitfall 8 byte-level guard. A stale tab
      //    requesting the old hashed asset MUST receive a clean 404,
      //    not the SPA shell under a .js URL (which would trigger
      //    Chromium's strict-MIME-for-modules hard error).
      const oldResp = await request.get(oldUrl);
      expect(
        oldResp.status(),
        'old asset URL MUST return 404 after rebuild (no SPA fallback)',
      ).toBe(404);
      const oldBody = (await oldResp.text()).toLowerCase();
      expect(
        oldBody,
        'old asset 404 body MUST NOT contain `<html` (no fallback to index.html)',
      ).not.toContain('<html');
      expect(
        oldBody,
        'old asset 404 body MUST NOT contain `<!doctype html`',
      ).not.toContain('<!doctype html');
    } finally {
      // Restore App.svelte to its pre-test content. The afterAll
      // hook restores build-marker.ts; this restores App.svelte.
      writeFileSync(appSvelte, appOriginal, 'utf8');
    }
  });
});
