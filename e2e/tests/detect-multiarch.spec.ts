// DETECT-04 — Both manifest shapes resolve correctly and flip
// update_available within cron+5s:
//   1. single-arch manifest (existing pushFreshManifest)
//   2. OCI image index (new pushFreshIndex fixture; returns AMD64 child digest)
//
// DETECT-07 — A fresh push to :latest flips update_available for the
// affected container within cron + 5s. The compose override
// `compose.test.override.cron-fast.yml` sets HMI_UPDATE_CRON=@every 5s,
// so the assertion deadline is 10s wall-clock.
//
// DETECT-06 (secondary reinforcement) — the events-path is exercised
// indirectly because the stub-watched-container appears in /api/state
// via the Discoverer's start-event path; the flip then depends on the
// cron sweep producer.
//
// Tolerances (assumes `make e2e-cron-fast` provides HMI_UPDATE_CRON=@every 5s):
//   - flip SLA: 10s (5s cron + 5s slack).
//   - poll cadence: 500ms.
//
// RED-FIRST (Plan 03-05 Task 0): this spec MUST land BEFORE the
// pushFreshIndex fixture exists. The multi-arch test fails because the
// fixture file e2e/fixtures/push-index.ts is not yet on disk (or
// pushFreshIndex is not yet exported). The single-arch flip test may
// pass green-by-accident on a stack without the cron-fast override —
// that's accepted under the RED-FIRST contract; Task 4 wires the
// override into the test target.

import { setTimeout as sleep } from 'node:timers/promises';
import { expect, test } from '@playwright/test';

import { pushFreshManifest } from '../fixtures/push-image';
import { pushFreshIndex } from '../fixtures/push-index';

type Container = {
  service?: string;
  image?: string;
  update_available?: boolean;
  available_digest?: string;
  pinned?: boolean;
  notes?: string;
  tag?: string;
};

type StateBody = {
  version: number;
  containers: Record<string, Container>;
  last_poll_start?: string;
  last_poll_end?: string;
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

test('detect-multiarch: single-arch manifest push flips update_available within cron+5s', async ({
  request,
}) => {
  // Push a fresh single-arch manifest to centroid-is/stub:latest. The
  // global-setup pushed once at boot; this second push is what triggers
  // the cron sweep's "digest changed" comparison.
  const newDigest = pushFreshManifest('centroid-is/stub');
  expect(newDigest).toMatch(/^sha256:[0-9a-f]+$/);

  // Wait for the cron sweep to observe the new digest on
  // stub-watched-container. At @every 5s cron, a flip should land in
  // ≤10s wall-clock.
  const flipped = await waitForCondition<Container>(
    request,
    (state) => {
      const c = state.containers?.['stub-watched-container'];
      if (c && c.update_available === true) return c;
      return undefined;
    },
    10_000,
    'stub-watched-container.update_available === true after single-arch push',
  );

  expect(flipped.update_available).toBe(true);
  expect(flipped.available_digest).toBe(newDigest);
});

test('detect-multiarch: OCI image index push (multi-arch) flips update_available within cron+5s', async ({
  request,
}) => {
  // Push a multi-arch OCI image index to centroid-is/stub:latest with
  // amd64 + arm64 children. The fixture returns the AMD64 child digest —
  // the resolver's WithPlatform(amd64) MUST resolve the index to the
  // amd64 child, so /api/state.containers[svc].available_digest must
  // match the fixture's returned digest (NOT the index digest).
  const amd64ChildDigest = pushFreshIndex('centroid-is/stub');
  expect(amd64ChildDigest).toMatch(/^sha256:[0-9a-f]+$/);

  const flipped = await waitForCondition<Container>(
    request,
    (state) => {
      const c = state.containers?.['stub-watched-container'];
      if (c && c.update_available === true && c.available_digest === amd64ChildDigest) {
        return c;
      }
      return undefined;
    },
    10_000,
    'stub-watched-container.available_digest matches amd64 child digest after multi-arch index push',
  );

  expect(flipped.update_available).toBe(true);
  expect(flipped.available_digest).toBe(amd64ChildDigest);
});
