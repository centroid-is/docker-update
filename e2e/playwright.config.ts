import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  globalSetup: './global-setup.ts',
  globalTeardown: './global-teardown.ts',
  workers: 1, // serialise — we share one docker stack across tests
  fullyParallel: false,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:8080',
    trace: 'on-first-retry',
    // Plan 05-05 Task 2 — grant clipboard read+write to every test
    // context so ui-table.spec.ts can call
    // `page.evaluate(() => navigator.clipboard.readText())` and
    // verify CopyButton wrote the full digest. Chromium on
    // http://localhost grants writeText without explicit permission,
    // but readText REQUIRES the permission grant in Playwright
    // contexts (the in-test page is not a user-gesture-trusted
    // surface by default). Granting both is the minimal-surprise
    // shape; no spec needs more.
    //
    // Source: 05-RESEARCH.md §K.1 + Playwright API
    // BrowserContextOptions.permissions.
    permissions: ['clipboard-read', 'clipboard-write'],
  },
});
