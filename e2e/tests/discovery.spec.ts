// DOCK-04 — Containers labeled hmi-update.watch=true are visible in
// /api/state within 60s (boot path) and within 5s when started mid-test
// (events path — DETECT-06 secondary surface preview).
//
// OBS-02 (happy-path) — GET /healthz returns 200 against the base stack
// with the real docker socket binding. Negative branches live in
// healthz-negative.spec.ts.
//
// Phase 1's smoke test only proved the /api/state JSON shape exists;
// this test proves the discovery goroutine from plan 02-03 actually
// populates it.
//
// Tolerances:
//   - boot SLA: 60s per DOCK-04 acceptance; the test polls /api/state every
//     1s with a 75s wall-clock deadline (15s slack for image build / boot).
//   - events path: 5s per CONTEXT.md; the test polls every 500ms with a
//     10s wall-clock deadline.

import { execSync } from 'node:child_process';
import { setTimeout as sleep } from 'node:timers/promises';
import { expect, test } from '@playwright/test';

type StateBody = {
  version: number;
  containers: Record<string, { service?: string; image?: string }>;
};

async function waitForContainer(
  request: import('@playwright/test').APIRequestContext,
  service: string,
  timeoutMs: number,
): Promise<StateBody['containers'][string]> {
  const deadline = Date.now() + timeoutMs;
  let lastBody: StateBody | null = null;
  while (Date.now() < deadline) {
    const resp = await request.get('/api/state');
    if (resp.ok()) {
      lastBody = (await resp.json()) as StateBody;
      const c = lastBody?.containers?.[service];
      if (c) return c;
    }
    await sleep(1000);
  }
  throw new Error(
    `service ${service} never appeared in /api/state within ${timeoutMs}ms. last body: ${JSON.stringify(lastBody)}`,
  );
}

test('discovery: stub-watched-container visible in /api/state within 60s', async ({ request }) => {
  // DOCK-04 boot path: a container labeled hmi-update.watch=true that
  // exists at boot-time should be enumerated by the Discoverer's
  // ContainerList call and appear in state within 60s. The Phase 1
  // compose.test.yml labels stub-watched-container with hmi-update.watch=true.
  const c = await waitForContainer(request, 'stub-watched-container', 75_000);
  expect(c).toMatchObject({ service: 'stub-watched-container' });
  // image should be the busybox-derived value from the base compose.test.yml
  expect(c.image).toBeTruthy();
});

test('healthz happy-path: GET /healthz returns 200 against base stack', async ({ request }) => {
  // OBS-02 — REQUIREMENTS.md says "200 if state file readable + docker
  // socket reachable." The base compose.test.yml binds
  // /var/run/docker.sock:/var/run/docker.sock (line 49) and gives the
  // docker-update container access to the real daemon, so the full chain
  // (state.Get + os.Stat + Ping) runs without mocks.
  //
  // This complements handlers_healthz_test.go's unit-level coverage:
  // the unit tests mock Ping; only this e2e proves the full daemon
  // round-trip.
  const r = await request.get('/healthz');
  expect(r.status()).toBe(200);
  const body = await r.json();
  expect(body.status).toBe('ok');
});

test('events path: docker-spawned labeled container visible within 5s', async ({ request }) => {
  // DOCK-04 events path: a labeled container started AFTER the discoverer
  // has subscribed to the docker events stream should appear in /api/state
  // within 5s. We launch a side-container on the compose network with
  // the hmi-update.watch=true label, then poll for /api/state's container
  // count to grow.
  //
  // We use the compose project's pre-existing network so the container is
  // reachable by the daemon the docker-update container is talking to (same
  // daemon, via the bind-mounted socket). The container is anonymous —
  // compose run does NOT necessarily inject com.docker.compose.service,
  // so we cannot rely on a specific service key appearing. Instead, we
  // assert the count of containers in /api/state grew — that signal is
  // sufficient to prove the events-driven path fires.

  const name = `events-test-${Date.now()}`;
  // Read baseline FIRST so the count comparison is taken right before the
  // event fires.
  const before = (await request.get('/api/state').then((r) => r.json())) as StateBody;
  const baseline = Object.keys(before.containers ?? {}).length;

  // `docker run -d --label hmi-update.watch=true busybox sleep 30` on the
  // host docker daemon — the same daemon docker-update is subscribed to via
  // the bind-mounted socket.
  execSync(
    `docker run -d --rm --name ${name} --label hmi-update.watch=true busybox:latest sleep 30`,
    { stdio: 'pipe' },
  );
  try {
    const deadline = Date.now() + 10_000; // 5s SLA + 5s slack
    let grew = false;
    while (Date.now() < deadline) {
      const now = (await request.get('/api/state').then((r) => r.json())) as StateBody;
      if (Object.keys(now.containers ?? {}).length > baseline) {
        grew = true;
        break;
      }
      await sleep(500);
    }
    expect(grew, 'a new watched container should appear within 5s of docker run').toBe(true);
  } finally {
    // Cleanup: kill the ad-hoc container regardless of test outcome. The
    // discoverer will see a destroy event and remove the row, restoring
    // state to baseline for follow-on specs.
    try {
      execSync(`docker rm -f ${name}`, { stdio: 'pipe' });
    } catch {
      /* ignore — container may have exited via --rm */
    }
  }
});
