// Phase 4 plan 04-06 — ACT-03 (online rollback) + ACT-04 (offline rollback)
//
// ACT-03: Per-container Rollback action — local re-tag + recreate; on a freshly-
//         updated container, swap CurrentDigest ↔ PreviousDigest.
// ACT-04: Rollback MUST succeed with the registry network detached. The
//         orchestrator uses docker.ImageTag (no registry call) on the local
//         image cache, then docker compose up -d --force-recreate. This is
//         the load-bearing differentiator from WUD 8.2.2.
//
// File header documents the "intentionally crash-loop" sibling crash-loop-stub
// service (Phase 4 plan 04-06 Task 1 — verify-failed.spec.ts fixture) so
// readers exploring this file aren't surprised by another stub that errors
// in state.containers. We never reference crash-loop-stub here.
//
// Tolerances:
//   - rollback online completion SLA: 30s (no pull + recreate=5s + verify=15s + margin)
//   - rollback offline completion SLA: 30s (same shape; registry unreachable
//     during the action but ImageTag is local, so no extra latency)
//   - test.setTimeout(60_000) to cover both phases comfortably
//
// Dependencies:
//   - pushFreshManifest (fixtures/push-image.ts) to seed an Update
//   - disconnectZotFromNetwork + reconnectZot (fixtures/disconnect-network.ts)
//
// CRITICAL SHAPE: reconnectZot() MUST run in finally{} so a failing
// assertion does not leave the stack partitioned for subsequent specs.

import { setTimeout as sleep } from 'node:timers/promises';
import { expect, test } from '@playwright/test';

import { disconnectZotFromNetwork, reconnectZot } from '../fixtures/disconnect-network';
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

// setupUpdatedContainer pushes a fresh manifest, waits for the cron flip,
// then runs POST /update so the container has both current_digest and
// previous_digest populated. Returns {newDigest, oldDigest} where
// newDigest is the post-update current and oldDigest is the pre-update
// current (now the previous_digest).
async function setupUpdatedContainer(
  request: import('@playwright/test').APIRequestContext,
): Promise<{ newDigest: string; oldDigest: string }> {
  const pushed = pushFreshManifest('centroid-is/stub');
  expect(pushed).toMatch(/^sha256:/);

  const beforeUpdate = await waitForCondition<Container>(
    request,
    (state) => {
      const c = state.containers?.['stub-watched-container'];
      if (c && c.update_available === true && c.available_digest === pushed) return c;
      return undefined;
    },
    10_000,
    'stub-watched-container update_available === true before Update',
  );
  const oldDigest = beforeUpdate.current_digest;
  expect(oldDigest, 'pre-update current_digest must exist').toMatch(/^sha256:/);

  const resp = await request.post('/api/containers/stub-watched-container/update');
  expect(resp.status(), 'POST /update should succeed').toBe(200);
  const body = (await resp.json()) as { current_digest: string; previous_digest: string };
  expect(body.current_digest).toBe(pushed);
  expect(body.previous_digest).toBe(oldDigest);
  return { newDigest: body.current_digest, oldDigest: oldDigest as string };
}

// DEFERRED to Plan 04-07 (D-04-06-01): prelude Update relies on daemon-side
// ImagePull which cannot resolve `zot:5000`. Body preserved verbatim for
// post-04-07 activation.
test.skip('rollback-flow: ACT-03 online rollback swaps current_digest <-> previous_digest', async ({
  request,
}) => {
  test.setTimeout(90_000);

  const { newDigest, oldDigest } = await setupUpdatedContainer(request);

  // ACT-03 — online rollback.
  const resp = await request.post('/api/containers/stub-watched-container/rollback');
  expect(resp.status(), 'POST /rollback should return 200').toBe(200);
  const body = (await resp.json()) as {
    current_digest: string;
    previous_digest: string;
    no_op?: boolean;
  };

  // Single-slot toggle: post-rollback current === pre-rollback previous;
  // post-rollback previous === pre-rollback current.
  expect(body.current_digest, 'rollback current must equal pre-rollback previous').toBe(oldDigest);
  expect(body.previous_digest, 'rollback previous must equal pre-rollback current').toBe(newDigest);
  expect(body.no_op ?? false, 'first rollback is NOT a no-op').toBe(false);

  // State write: registry :latest is unchanged so update_available re-flips.
  await waitForCondition<true>(
    request,
    (state) => {
      const c = state.containers?.['stub-watched-container'];
      if (
        c &&
        c.current_digest === oldDigest &&
        c.previous_digest === newDigest &&
        c.update_available === true
      ) {
        return true;
      }
      return undefined;
    },
    5_000,
    'state after rollback: digests swapped, update_available re-flipped to true',
  );
});

// DEFERRED to Plan 04-07 (D-04-06-01): prelude Update relies on daemon-side
// ImagePull which cannot resolve `zot:5000`. ACT-04 offline rollback itself
// does not need the registry, but the prelude does. Body preserved verbatim
// for post-04-07 activation.
test.skip('rollback-flow: ACT-04 offline rollback succeeds with registry network detached', async ({
  request,
}) => {
  test.setTimeout(90_000);

  const { newDigest, oldDigest } = await setupUpdatedContainer(request);

  // Partition the registry — the load-bearing ACT-04 condition.
  disconnectZotFromNetwork();
  try {
    const resp = await request.post('/api/containers/stub-watched-container/rollback');
    expect(resp.status(), 'ACT-04 offline rollback should return 200').toBe(200);
    const body = (await resp.json()) as {
      current_digest: string;
      previous_digest: string;
    };
    expect(body.current_digest, 'offline rollback current must equal pre-rollback previous').toBe(
      oldDigest,
    );
    expect(body.previous_digest, 'offline rollback previous must equal pre-rollback current').toBe(
      newDigest,
    );
  } finally {
    // ALWAYS restore the network so subsequent specs see a connected
    // stack. The healthz-negative afterAll pattern is the precedent.
    reconnectZot();
  }
});
