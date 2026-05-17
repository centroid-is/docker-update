// Standalone Playwright config for running specific tests against
// the live HMI at 10.50.10.175 (no docker-compose fixture, no
// global setup). Used ad-hoc to TDD UI bugs against production.
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  workers: 1,
  fullyParallel: false,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: 'http://10.50.10.175',
    trace: 'off',
  },
});
