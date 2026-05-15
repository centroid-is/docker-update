// Phase 4 plan 04-06 — ACT-12 (state persists across docker compose restart)
//
// ACT-12: After `docker compose restart hmi-update`, the persisted state
//         file (./hmi_update_state.json in a tmpfs volume in e2e — see
//         e2e/compose.test.yml hmi-update.tmpfs) MUST replay cleanly. The
//         in-memory snapshot rebuilt by state.NewStore at boot carries
//         the same CurrentDigest and PreviousDigest as before the restart.
//
// This is the graceful-restart invariant. The SIGKILL-mid-write invariant
// (STATE-04) is empirically proven by Plan 04-05's
// internal/state/store_sigkill_test.go (100 fork-and-kill iterations). The
// two cover different fault models — graceful restart preserves the most
// recent successful write; SIGKILL preserves the LAST successful write
// (the renameio invariant).
//
// Wire contract:
//   1. POST /update → ActionResult with current_digest + previous_digest both sha256:...
//   2. execSync 'docker compose -f compose.test.yml restart hmi-update'
//      — re-execs the binary; main.go re-runs state.NewStore at boot
//   3. Poll /healthz until 200 (deadline 30s; mirrors compose-drift.spec.ts::afterAll)
//   4. GET /api/state — current_digest and previous_digest must match step 1
//
// SECURITY: execSync with hardcoded shell strings (no operator interpolation).
// Per the WR-08 review lesson, this is the safe pattern (compose path + service
// name are literals). If those values ever come from operator input, pivot to
// execFileSync with argv split.
//
// Tolerances:
//   - test.setTimeout(90_000): pre-restart Update ≈30s + restart ≈10s +
//     healthz-up poll ≈30s + state assertion = ~70s wall-clock.

import { execSync } from 'node:child_process';
import { setTimeout as sleep } from 'node:timers/promises';
import { expect, test } from '@playwright/test';

import { pushFreshManifest } from '../fixtures/push-image';

type Container = {
  service?: string;
  current_digest?: string;
  previous_digest?: string;
  available_digest?: string;
  update_available?: boolean;
};

type StateBody = { version: number; containers: Record<string, Container> };

async function waitForCondition<T>(
  request: import('@playwright/test').APIRequestContext,
  predicate: (state: StateBody) => T | undefined,
  timeoutMs: number,
  label: string,
): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  let lastBody: StateBody | null = null;
  while (Date.now() < deadline) {
    const resp = await request.get('/api/state');
    if (resp.ok()) {
      lastBody = (await resp.json()) as StateBody;
      const result = predicate(lastBody);
      if (result !== undefined) return result;
    }
    await sleep(500);
  }
  throw new Error(
    `${label} did not satisfy predicate within ${timeoutMs}ms. last body: ${JSON.stringify(lastBody)}`,
  );
}

async function waitForHealth(timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const r = await fetch('http://localhost:8080/healthz');
      if (r.ok) return;
    } catch {
      // Server momentarily unavailable during restart — retry.
    }
    await sleep(500);
  }
  throw new Error(`/healthz never returned 200 within ${timeoutMs}ms`);
}

test('restart-persistence: ACT-12 digests + previous_digest survive docker compose restart hmi-update', async ({
  request,
}) => {
  test.setTimeout(120_000);

  // Setup: push fresh manifest, wait for flip, run Update so both
  // current_digest AND previous_digest are populated.
  const pushed = pushFreshManifest('centroid-is/stub');
  await waitForCondition<Container>(
    request,
    (state) => {
      const c = state.containers?.['stub-watched-container'];
      if (c && c.update_available === true && c.available_digest === pushed) return c;
      return undefined;
    },
    10_000,
    'update_available === true before Update prelude',
  );
  const updResp = await request.post('/api/containers/stub-watched-container/update');
  expect(updResp.status(), 'pre-restart Update must succeed').toBe(200);
  const updBody = (await updResp.json()) as {
    current_digest: string;
    previous_digest: string;
  };
  expect(updBody.current_digest).toMatch(/^sha256:/);
  expect(updBody.previous_digest).toMatch(/^sha256:/);
  const currentBeforeRestart = updBody.current_digest;
  const previousBeforeRestart = updBody.previous_digest;

  // Restart hmi-update. compose.test.yml is in the e2e/ dir (we are
  // run from there via the Makefile target). The hardcoded string is
  // safe — no operator interpolation.
  //
  // Compose v2 `restart` keeps the container in place; the binary re-execs
  // and re-runs main.go's state.NewStore against the same tmpfs mount.
  execSync('docker compose -f compose.test.yml restart hmi-update', { stdio: 'inherit' });

  // Poll /healthz until 200 — mirrors compose-drift.spec.ts::afterAll +
  // healthz-negative.spec.ts::waitForHealth.
  await waitForHealth(30_000);

  // ACT-12: state must round-trip across the restart.
  const stateResp = await request.get('/api/state');
  expect(stateResp.ok()).toBe(true);
  const state = (await stateResp.json()) as StateBody;
  const c = state.containers?.['stub-watched-container'];
  expect(c, 'stub-watched-container must reappear after restart').toBeDefined();
  expect(
    c?.current_digest,
    'ACT-12: current_digest must survive docker compose restart',
  ).toBe(currentBeforeRestart);
  expect(
    c?.previous_digest,
    'ACT-12: previous_digest must survive docker compose restart',
  ).toBe(previousBeforeRestart);
});
