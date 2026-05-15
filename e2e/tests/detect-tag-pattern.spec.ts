// DETECT-08 (Acceptance criterion 8) — A container labeled
// `hmi-update.tag-pattern=^latest-pg17$` is only considered "updated"
// when a fresh push lands on a tag MATCHING the pattern. A push to a
// non-matching tag (e.g. `:latest-pg18-oss`) must NOT flip
// `update_available`. A push to the matching tag (`:latest-pg17`) MUST
// flip it. An INVALID regex label surfaces a Note ("invalid tag-pattern
// label, ignored") and falls back to the bare :latest comparison.
//
// Tolerances (assumes `make e2e-cron-fast` provides DOCKER_UPDATE_CRON=@every 5s):
//   - negative-flip test: 12s wall-clock (≥2 cron ticks) of NO change.
//   - positive-flip test: 10s wall-clock for the flip to land.
//   - invalid-regex test: 30s wall-clock for the Note to populate via
//     the Discoverer's upsertFromInspect path.
//
// RED-FIRST (Plan 03-05 Task 0): this spec lands BEFORE the
// timescaledb-stub + invalid-pattern-stub services exist in
// compose.test.yml. All three tests fail with assertion-level errors
// because `state.containers['timescaledb-stub']` is undefined.

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
  labels?: Record<string, string>;
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

test('detect-tag-pattern: timescaledb-stub does NOT flip when :latest-pg18-oss pushed', async ({
  request,
}) => {
  // Wait for the container to first appear so we have a definite baseline.
  await waitForCondition<Container>(
    request,
    (state) => state.containers?.['timescaledb-stub'],
    30_000,
    'timescaledb-stub appears in /api/state',
  );

  // Push to a tag that does NOT match ^latest-pg17$.
  const ignoredDigest = pushFreshManifest('timescale/timescaledb', { tag: 'latest-pg18-oss' });
  expect(ignoredDigest).toMatch(/^sha256:[0-9a-f]+$/);

  // Wait ≥2 cron ticks (12s at @every 5s) and assert update_available
  // STAYS false. We poll continuously; the moment it flips true is a
  // bug.
  const deadline = Date.now() + 12_000;
  while (Date.now() < deadline) {
    const resp = await request.get('/api/state');
    const state = (await resp.json()) as StateBody;
    const c = state.containers?.['timescaledb-stub'];
    expect(c?.update_available, 'timescaledb-stub should NOT flip on non-matching tag').not.toBe(
      true,
    );
    // The container.notes should NOT mention an invalid pattern — a
    // valid pattern with no match means "ignore", not "broken".
    if (c?.notes) {
      expect(c.notes).not.toMatch(/invalid tag-pattern/);
    }
    await sleep(1000);
  }
});

test('detect-tag-pattern: timescaledb-stub DOES flip when :latest-pg17 (matching pattern) pushed', async ({
  request,
}) => {
  await waitForCondition<Container>(
    request,
    (state) => state.containers?.['timescaledb-stub'],
    30_000,
    'timescaledb-stub appears in /api/state',
  );

  const matchingDigest = pushFreshManifest('timescale/timescaledb', { tag: 'latest-pg17' });
  expect(matchingDigest).toMatch(/^sha256:[0-9a-f]+$/);

  const flipped = await waitForCondition<Container>(
    request,
    (state) => {
      const c = state.containers?.['timescaledb-stub'];
      if (c && c.update_available === true && c.available_digest === matchingDigest) return c;
      return undefined;
    },
    10_000,
    'timescaledb-stub.update_available flips on :latest-pg17 push',
  );

  expect(flipped.update_available).toBe(true);
  expect(flipped.available_digest).toBe(matchingDigest);
});

test('detect-tag-pattern: invalid regex label surfaces notes="invalid tag-pattern label, ignored"', async ({
  request,
}) => {
  // The invalid-pattern-stub service in compose.test.yml carries
  // hmi-update.tag-pattern="[unclosed(" which fails regexp.Compile.
  // The Discoverer's upsertFromInspect surfaces this as a Note.
  const c = await waitForCondition<Container>(
    request,
    (state) => {
      const got = state.containers?.['invalid-pattern-stub'];
      if (got && got.notes === 'invalid tag-pattern label, ignored') return got;
      return undefined;
    },
    30_000,
    'invalid-pattern-stub.notes === "invalid tag-pattern label, ignored"',
  );

  expect(c.notes).toBe('invalid tag-pattern label, ignored');
});
