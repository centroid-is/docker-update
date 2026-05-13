// RED-FIRST per C4. This test is authored before any HTTP server, Vite
// build, or compose stack exists. Plan 04 (Wave 3) drives it green by
// shipping internal/api/server.go, internal/api/static.go, the test
// compose stack, and the Playwright globalSetup that brings them up.
//
// What this test guards (Phase 1 acceptance gate):
//   - FOUND-03: GET /healthz returns 200
//   - FOUND-04: GET / serves the embedded Svelte shell with Cache-Control: no-cache
//   - FOUND-04: <table> has exactly seven <th> cells with the column slugs from UI-SPEC
//   - FOUND-04: empty-state row uses colspan="7" and contains the literal heading
//   - FOUND-05: GET /api/state returns JSON shape {"version": 1, "containers": <object>}
//   - Pitfall 8: /assets/<missing>.js returns 404 (NEVER index.html — strict no-fallback)
//   - Pitfall 8: existing /assets/*.js carries Content-Type application/javascript; charset=utf-8
//   - Pitfall 8: existing /assets/*.js carries Cache-Control: public, max-age=31536000, immutable
//
// Plan 04 brings up the test stack and drives this test green. Until then,
// `playwright test` exits with a network failure (no server on :8080) or a
// config error (no playwright.config.ts yet). Either is a valid RED signal.

import { expect, test } from '@playwright/test';

// The seven column slugs from .planning/phases/01-walking-skeleton-test-harness/01-UI-SPEC.md
// "Copywriting Contract". Order is load-bearing — the table must render them
// in this exact left-to-right sequence at all viewport widths.
const COLUMN_SLUGS = [
  'container',
  'image:tag',
  'current digest',
  'available digest',
  'previous digest',
  'status',
  'actions',
] as const;

const EMPTY_STATE_HEADING = 'No watched containers yet';

test('smoke: healthz, table shell, /api/state, and asset MIME/cache contract', async ({ page, request }) => {
  // ─────────────────────────────────────────────────────────────────────
  // FOUND-03 — /healthz returns 200
  // ─────────────────────────────────────────────────────────────────────
  const health = await request.get('/healthz');
  expect(health.status(), '/healthz should return 200 OK').toBe(200);

  // ─────────────────────────────────────────────────────────────────────
  // FOUND-04 — GET / serves the embedded Svelte shell with no-cache
  // ─────────────────────────────────────────────────────────────────────
  const indexResp = await request.get('/');
  expect(indexResp.status(), 'GET / should return 200 OK').toBe(200);
  expect(
    (indexResp.headers()['cache-control'] ?? '').toLowerCase(),
    'GET / must serve Cache-Control: no-cache (per UI-SPEC asset/cache contract)',
  ).toContain('no-cache');

  await page.goto('/');

  // No console errors on initial load (UI-SPEC acceptance check).
  const consoleErrors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push(msg.text());
  });

  // Exactly seven <th> cells with the slugs in the documented order.
  const headerTexts = await page.locator('table thead th').allTextContents();
  const normalized = headerTexts.map((t) => t.trim().toLowerCase());
  expect(normalized, 'Table must have exactly the seven UI-SPEC column slugs, in order').toEqual([
    ...COLUMN_SLUGS,
  ]);

  // Empty-state row: colspan="7", heading copy verbatim.
  const emptyCell = page.locator('table tbody td[colspan="7"]');
  await expect(emptyCell, 'Empty state row should use colspan="7" for the seven columns').toHaveCount(1);
  await expect(emptyCell, 'Empty state must contain the verbatim heading copy').toContainText(EMPTY_STATE_HEADING);

  // ─────────────────────────────────────────────────────────────────────
  // FOUND-05 — /api/state shape
  // ─────────────────────────────────────────────────────────────────────
  const stateResp = await request.get('/api/state');
  expect(stateResp.status(), '/api/state should return 200 OK').toBe(200);
  const stateCT = (stateResp.headers()['content-type'] ?? '').toLowerCase();
  expect(stateCT, '/api/state must be served as JSON').toContain('application/json');

  const stateBody = await stateResp.json();
  expect(stateBody, '/api/state body must have shape {version: 1, containers: object}').toMatchObject({
    version: 1,
    containers: expect.any(Object),
  });

  // ─────────────────────────────────────────────────────────────────────
  // Pitfall 8 — strict /assets/* no-fallback (404 on miss, NEVER index.html)
  // ─────────────────────────────────────────────────────────────────────
  const missing = await request.get('/assets/this-does-not-exist.js');
  expect(
    missing.status(),
    '/assets/<missing>.js must return 404 (Pitfall 8: no SPA fallback for asset paths)',
  ).toBe(404);
  // Belt-and-braces: even on 404, the response body must not be index.html.
  const missingBody = await missing.text();
  expect(missingBody, '/assets/<missing>.js 404 body must not echo index.html').not.toContain('<!doctype html');

  // ─────────────────────────────────────────────────────────────────────
  // Pitfall 8 — existing /assets/*.js must carry the explicit JS MIME type
  // and the immutable cache header. Vite emits hashed filenames so we
  // discover one by reading the index.html.
  // ─────────────────────────────────────────────────────────────────────
  const indexHtml = await indexResp.text();
  const jsAssetMatch = indexHtml.match(/\/assets\/[A-Za-z0-9._-]+\.js/);
  expect(
    jsAssetMatch,
    'index.html must reference at least one /assets/*.js (the Vite-emitted module bundle)',
  ).not.toBeNull();
  const jsAssetPath = jsAssetMatch![0];

  const jsResp = await request.get(jsAssetPath);
  expect(jsResp.status(), `${jsAssetPath} should be served`).toBe(200);
  const jsCT = (jsResp.headers()['content-type'] ?? '').toLowerCase();
  expect(
    jsCT,
    `Pitfall 8: ${jsAssetPath} must carry Content-Type application/javascript; charset=utf-8 (default Go mime table can guess wrong on distroless)`,
  ).toBe('application/javascript; charset=utf-8');
  const jsCache = (jsResp.headers()['cache-control'] ?? '').toLowerCase();
  expect(
    jsCache,
    `Hashed asset ${jsAssetPath} must carry Cache-Control: public, max-age=31536000, immutable`,
  ).toBe('public, max-age=31536000, immutable');

  // Final guard: no console errors during the page load.
  expect(consoleErrors, 'No console errors on initial /').toEqual([]);
});
