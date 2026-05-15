// Phase 4 plan 04-06 — ACT-01 / ACT-02 / ACT-11 (Update happy path)
//
// ACT-01: Per-container Update action — pull new image + recreate + verify.
// ACT-02: Update completes within 30s wall-clock (pull + recreate + 15s verify).
// ACT-11: Update success response carries {current_digest, previous_digest}
//         (both sha256:... — see internal/api/handlers_actions.go::writeActionResult).
//
// Test sequence (canonical per 04-PATTERNS.md lines 962-985):
//   1. pushFreshManifest('centroid-is/stub')                  → flip cron
//   2. waitForCondition update_available===true (≤10s @every-5s) → upstream visible
//   3. capture beforeDigest = state.current_digest             → ACT-11 baseline
//   4. POST /api/containers/stub-watched-container/update
//   5. assert resp.status===200 + body.current_digest matches /^sha256:/
//      + body.previous_digest === beforeDigest                 → ACT-11
//   6. waitForCondition update_available===false && state.current_digest === body.current_digest
//      && state.previous_digest === beforeDigest               → state-write confirmed
//
// Tolerances (assumes `make e2e-cron-fast` provides HMI_UPDATE_CRON=@every 5s):
//   - cron flip SLA: 10s
//   - update completion SLA: 30s (default Playwright per-test timeout suffices:
//     pull=1s + recreate=5s + verify=15s + margin=9s = 30s; A7 in RESEARCH.md)
//   - state-write SLA after POST returns: 5s
//
// Wire contract: see API.md POST /api/containers/{service}/update.
// Dependencies: pushFreshManifest from fixtures/push-image.ts.

import { setTimeout as sleep } from 'node:timers/promises';
import { expect, test } from '@playwright/test';

import { pushFreshManifest } from '../fixtures/push-image';

type Container = {
  service?: string;
  image?: string;
  tag?: string;
  current_digest?: string;
  previous_digest?: string;
  available_digest?: string;
  update_available?: boolean;
  action_in_flight?: string;
  action_error?: string;
};

type StateBody = {
  version: number;
  containers: Record<string, Container>;
};

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

// DEFERRED to Plan 04-07 (D-04-06-01): daemon-level ImagePull cannot resolve
// `zot:5000` from the host docker daemon's DNS context. Test body preserved
// verbatim so it activates as soon as 04-07 lands the e2e pull-path fix.
// See .planning/phases/04-update-rollback-force-pull-actions-safety-state-persistence/04-07-PLAN.md.
test.skip('update-flow: ACT-01/02/11 POST /update flips digests and returns {current_digest,previous_digest}', async ({
  request,
}) => {
  // Bump timeout to accommodate full update budget: pull + recreate + 15s verify.
  test.setTimeout(60_000);

  // Step 1: push new manifest to flip update_available.
  const newDigest = pushFreshManifest('centroid-is/stub');
  expect(newDigest).toMatch(/^sha256:[0-9a-f]+$/);

  // Step 2: wait for cron sweep to surface upstream digest.
  const beforeFlip = await waitForCondition<Container>(
    request,
    (state) => {
      const c = state.containers?.['stub-watched-container'];
      if (c && c.update_available === true && c.available_digest === newDigest) return c;
      return undefined;
    },
    10_000,
    'stub-watched-container.update_available === true with available_digest === newly-pushed digest',
  );

  // Step 3: capture the CurrentDigest before the action so we can assert
  // body.previous_digest === beforeDigest (ACT-11).
  const beforeDigest = beforeFlip.current_digest;
  expect(beforeDigest, 'current_digest must be populated before Update').toMatch(/^sha256:/);

  // Step 4: POST /update.
  const resp = await request.post('/api/containers/stub-watched-container/update');
  expect(resp.status(), 'POST /update should return 200 on happy path').toBe(200);
  const body = (await resp.json()) as {
    current_digest: string;
    previous_digest?: string;
    no_op?: boolean;
  };

  // Step 5: ACT-11 — body shape assertions.
  expect(body.current_digest).toMatch(/^sha256:[0-9a-f]+$/);
  expect(body.previous_digest, 'ACT-11: previous_digest must be the pre-action current').toBe(
    beforeDigest,
  );
  expect(body.current_digest, 'current_digest must be the pulled digest').toBe(newDigest);
  expect(body.no_op ?? false, 'happy-path update is NOT no_op').toBe(false);

  // Step 6: confirm state write applied.
  await waitForCondition<true>(
    request,
    (state) => {
      const c = state.containers?.['stub-watched-container'];
      if (
        c &&
        c.update_available === false &&
        c.current_digest === body.current_digest &&
        c.previous_digest === beforeDigest
      ) {
        return true;
      }
      return undefined;
    },
    5_000,
    'state after Update: update_available=false, current_digest=new, previous_digest=before',
  );
});
