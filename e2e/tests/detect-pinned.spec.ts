// DETECT-09 — A container whose image reference is digest-pinned
// (image: <repo>@sha256:<digest>) is treated as intentional opt-out:
//
//   1. /api/state shows the container with `pinned: true` and
//      `notes: "pinned: opt-out"`.
//   2. The cron sweep NEVER produces a flip of update_available for
//      this container — even when an upstream :latest push happens on
//      a related repo.
//
// Tolerances (assumes `make e2e-cron-fast` provides HMI_UPDATE_CRON=@every 5s):
//   - pinned-appears: 75s wall-clock (boot-list SLA matches DOCK-04).
//   - never-flips: 10s wall-clock (≥2 cron ticks of no flip).
//
// RED-FIRST (Plan 03-05 Task 0): this spec lands BEFORE the
// pinned-stub service exists in compose.test.yml. Both tests fail with
// assertion-level errors because `state.containers['pinned-stub']` is
// undefined.

import { setTimeout as sleep } from 'node:timers/promises';
import { expect, test } from '@playwright/test';

import { pushFreshManifest } from '../fixtures/push-image';

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

test('detect-pinned: container with image: busybox@sha256:... appears with pinned=true + notes="pinned: opt-out"', async ({
  request,
}) => {
  const c = await waitForCondition<Container>(
    request,
    (state) => {
      const got = state.containers?.['pinned-stub'];
      if (got && got.pinned === true) return got;
      return undefined;
    },
    75_000,
    'pinned-stub appears in /api/state with pinned=true',
  );

  expect(c.pinned).toBe(true);
  expect(c.notes).toBe('pinned: opt-out');
});

test('detect-pinned: pinned container never flips update_available even after upstream push', async ({
  request,
}) => {
  // Confirm pinned-stub is registered first.
  await waitForCondition<Container>(
    request,
    (state) => {
      const got = state.containers?.['pinned-stub'];
      if (got && got.pinned === true) return got;
      return undefined;
    },
    75_000,
    'pinned-stub registered before push',
  );

  // Push to centroid-is/stub — this would flip stub-watched-container,
  // but must NOT flip pinned-stub (different image; pinned container is
  // skipped by the poller regardless).
  pushFreshManifest('centroid-is/stub');

  // Poll for ≥2 cron ticks (10s at @every 5s) and assert the pinned
  // container's update_available stays false the entire time.
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    const resp = await request.get('/api/state');
    const state = (await resp.json()) as StateBody;
    const c = state.containers?.['pinned-stub'];
    expect(c?.update_available, 'pinned-stub.update_available must stay false').not.toBe(true);
    await sleep(1000);
  }
});
