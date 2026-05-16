// SC-6 (iii) RED-first regression guard for the relative-path bind-mount bug.
// Will pass after Plan 09-03 lands the inspect-then-recreate primitive.
//
// Phase 9 plan 09-02 e2e regression guard — SC-2 (b) + SC-6 (iii).
//
// Reproduces the 2026-05-15/16 flutter incident (HANDOFF.md, BUG-7 chain):
// a compose service with a `./relative-path:/container-target` volume.
// Pre-Phase-9, docker-update's compose.Runner shells out to
// `docker compose -f /host/docker-compose.yml up -d --force-recreate <svc>`.
// Inside docker-update's container the `./relative-path` resolves against
// the CONTAINER's CWD (typically /), not the operator's host CWD. The
// recreated container ends up with a bind source like `/relative-path`
// or fails outright depending on how compose chooses to interpret the
// path. Either way the flutter wayland-socket bind broke, the display
// blacked out, and the field engineer could not recover via the UI.
//
// Post-Phase-9 the orchestrator calls ContainerInspect to read the OLD
// container's HostConfig.Binds (which carry the DAEMON-RESOLVED absolute
// host paths from compose-up time) and passes those verbatim to
// ContainerCreate. The recreated container ends up with the SAME absolute
// host bind path as the original — no CWD substitution.
//
// Test contract:
//   1. /api/state shows both relative-bind-A and relative-bind-B.
//   2. Inspect each container via `docker inspect` on the host daemon
//      (same daemon docker-update is bound to via the socket). Capture
//      HostConfig.Binds[0] — must be an ABSOLUTE host path matching the
//      operator's CWD + e2e/test-relative-mount/<NAME>.
//   3. POST /api/containers/relative-bind-<NAME>/update — exercises the
//      recreate path (a no-op pull against the local zot:5000/centroid-is/
//      stub:latest tag, but the recreate step is the surface under test).
//   4. Poll /api/state until action_in_flight clears (or 30s timeout).
//   5. Re-inspect the (newly-recreated) container; HostConfig.Binds[0]
//      MUST STILL match the original absolute host path. A pre-Phase-9
//      binary will fail this assertion: the recreate would have happened
//      via `docker compose ... up -d --force-recreate` inside docker-
//      update's container, mis-resolving `./test-relative-mount/A` against
//      the wrong CWD, and the new container's bind would NOT match the
//      original host path.
//
// SLA: 30s total (no-op pull + recreate is fast; the verify window is
// the long pole).
//
// This spec is RED on the pre-Phase-9 codebase (compose-runner mis-resolves
// the path) and goes GREEN once Plan 09-03 ships the inspect-then-recreate
// primitive that bypasses the compose CLI entirely.

import { execSync } from 'node:child_process';
import { setTimeout as sleep } from 'node:timers/promises';
import { expect, test } from '@playwright/test';

type Container = {
  service?: string;
  action_in_flight?: string;
  action_error?: string;
};

type StateBody = {
  containers: Record<string, Container>;
};

const SERVICES = ['relative-bind-A', 'relative-bind-B'] as const;

// inspectBinds returns the first Bind entry from `docker inspect <name>`.
// Format is "<host-path>:<container-path>[:<mode>]" per the docker
// inspect contract.
function inspectBinds(name: string): string[] {
  const raw = execSync(
    `docker inspect --format='{{json .HostConfig.Binds}}' ${name}`,
    { stdio: ['ignore', 'pipe', 'pipe'] },
  )
    .toString()
    .trim()
    // The --format wraps the JSON in single quotes on some shells; strip them.
    .replace(/^'/, '')
    .replace(/'$/, '');
  if (raw === 'null' || raw === '') return [];
  try {
    return JSON.parse(raw) as string[];
  } catch {
    return [];
  }
}

// waitForActionClear polls /api/state until the named container's
// action_in_flight is empty (action completed — success or failure both
// drain the field).
async function waitForActionClear(
  request: import('@playwright/test').APIRequestContext,
  service: string,
  timeoutMs: number,
): Promise<Container> {
  const deadline = Date.now() + timeoutMs;
  let last: Container | undefined;
  while (Date.now() < deadline) {
    const resp = await request.get('/api/state');
    if (resp.ok()) {
      const body = (await resp.json()) as StateBody;
      const c = body.containers?.[service];
      if (c) {
        last = c;
        if (!c.action_in_flight) {
          return c;
        }
      }
    }
    await sleep(500);
  }
  throw new Error(
    `${service}: action_in_flight never cleared within ${timeoutMs}ms (last: ${JSON.stringify(last)})`,
  );
}

test('relative bind-mount resolves to operator host path after update', async ({ request }) => {
  // 30s budget: no-op pull (<1s) + recreate (<5s) + verify window (15s) +
  // 9s margin per service × 2 services. Bump default Playwright per-test
  // timeout accordingly.
  test.setTimeout(90_000);

  // Step 1: both services visible in /api/state.
  const stateResp = await request.get('/api/state');
  expect(stateResp.ok(), 'GET /api/state').toBeTruthy();
  const state = (await stateResp.json()) as StateBody;
  for (const svc of SERVICES) {
    expect(
      state.containers?.[svc],
      `${svc} must be visible in /api/state (Discoverer + hmi-update.watch=true)`,
    ).toBeTruthy();
  }

  // Step 2: capture initial Binds[0] for each service. Must be ABSOLUTE
  // host path. Compose resolves the relative source at compose-up time;
  // the daemon stores the resolved absolute path.
  const initial: Record<string, string> = {};
  for (const svc of SERVICES) {
    const binds = inspectBinds(svc);
    expect(binds.length, `${svc} must have ≥1 Bind in HostConfig`).toBeGreaterThanOrEqual(1);
    const bind = binds[0];
    // Bind format: "<host-source>:<container-target>[:<mode>]".
    // Host source MUST be absolute (start with /). RESEARCH.md A2: the
    // daemon always carries absolute host paths in HostConfig.Binds.
    expect(
      bind.startsWith('/'),
      `${svc}: HostConfig.Binds[0] must be absolute host path, got ${bind}`,
    ).toBeTruthy();
    // The bind target side must match what compose.test.yml declared (/data).
    expect(
      bind.includes(':/data'),
      `${svc}: HostConfig.Binds[0] must target /data, got ${bind}`,
    ).toBeTruthy();
    initial[svc] = bind;
  }

  // Step 3: POST /update for each service in sequence — the orchestrator
  // serialises per-service via the action mutex, but cross-service runs
  // can interleave. Sequential keeps the test debuggable.
  for (const svc of SERVICES) {
    const resp = await request.post(`/api/containers/${svc}/update`);
    // Accept any 2xx outcome — happy path is 200, no-op (same digest) is
    // also 200. A 412/500 here means the orchestrator surfaced an
    // unexpected error and the test fails informatively.
    expect(
      resp.status(),
      `POST /api/containers/${svc}/update should succeed; got ${resp.status()} ${await resp.text()}`,
    ).toBeLessThan(300);

    // Step 4: wait until action_in_flight clears for this service.
    const post = await waitForActionClear(request, svc, 45_000);
    // action_error MAY be populated (e.g. verify_failed on a no-op pull
    // is possible if the daemon GC's the new container quickly). The
    // bind-mount assertion below is what we're guarding; an error here
    // means the action surfaced something we should know about, but it
    // does not invalidate the Binds[0] assertion.
    if (post.action_error) {
      // Log for visibility but don't fail the test — the load-bearing
      // assertion is on the recreated container's bind paths.
      console.warn(
        `${svc}: action completed with action_error=${post.action_error}; continuing to bind-path assertion`,
      );
    }
  }

  // Step 5: re-inspect each service. The recreated container's
  // HostConfig.Binds[0] MUST equal the pre-update absolute host path. If
  // it does not, the recreate path used the wrong CWD to resolve the
  // relative source — the exact regression Phase 9 (a) fixes.
  for (const svc of SERVICES) {
    const binds = inspectBinds(svc);
    expect(binds.length, `${svc} post-update must still have ≥1 Bind`).toBeGreaterThanOrEqual(1);
    expect(
      binds[0],
      `${svc} POST-UPDATE BIND REGRESSION (SC-6 iii): recreated container's HostConfig.Binds[0]=${binds[0]} does NOT match the pre-update operator host path ${initial[svc]}. This is the BUG-7 family — the recreate path mis-resolved the relative source against the wrong CWD.`,
    ).toBe(initial[svc]);
  }
});
