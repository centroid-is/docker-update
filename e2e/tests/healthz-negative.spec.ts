// OBS-02 / DOCK-03 — /healthz negative-path coverage. Each test
// tears down the base stack, brings up the override stack, runs its
// assertions, then tears down. The afterAll restores the base stack
// so subsequent specs (and the global teardown) see a consistent
// baseline.
//
// This is ~30s of overhead per test — acceptable for what it proves:
// the two 503 branches of /healthz with VERBATIM remediation hint
// strings per CONTEXT.md "Healthz Remediation Hints (DOCK-03)".
//
// Stack orchestration happens inside the spec's beforeEach/afterAll
// hooks rather than in playwright.config.ts, which is intentionally
// untouched (one shared baseline globalSetup for the suite). The trade-
// off is per-test stack swap cost; the win is each negative spec is
// self-contained and runnable in isolation.

import { execSync } from 'node:child_process';
import { setTimeout as sleep } from 'node:timers/promises';
import { expect, test } from '@playwright/test';

const COMPOSE_BASE = 'docker compose -f compose.test.yml';

type HealthzResponse = { status: number; body: { status: string; reason?: string } };

async function waitForHealth(
  url: string,
  expectStatus: number,
  timeoutMs: number,
): Promise<HealthzResponse> {
  const deadline = Date.now() + timeoutMs;
  let lastStatus = 0;
  let lastBody: unknown = null;
  while (Date.now() < deadline) {
    try {
      const r = await fetch(url);
      lastStatus = r.status;
      // Read body once, defensively try to parse JSON.
      const text = await r.text();
      try {
        lastBody = JSON.parse(text);
      } catch {
        lastBody = text;
      }
      if (r.status === expectStatus) {
        return { status: r.status, body: lastBody as HealthzResponse['body'] };
      }
    } catch {
      /* network not up yet — retry */
    }
    await sleep(500);
  }
  // Deadline expired — return the last seen response (test will fail on
  // the status assertion).
  return {
    status: lastStatus,
    body: (lastBody as HealthzResponse['body']) ?? { status: 'unknown' },
  };
}

function downStack() {
  execSync(`${COMPOSE_BASE} down -v --remove-orphans`, { stdio: 'pipe' });
}

function upStackWithOverride(override: string) {
  // --wait blocks until healthchecks pass; both zot and hmi-update have NO
  // compose-side healthchecks (distroless, no shell), so --wait completes
  // as soon as the containers START. We then poll /healthz from the host.
  execSync(`${COMPOSE_BASE} -f ${override} up -d --wait`, { stdio: 'inherit' });
}

function upBaseStack() {
  execSync(`${COMPOSE_BASE} up -d --wait`, { stdio: 'inherit' });
}

test.describe.serial('healthz negative-path coverage (DOCK-03 / OBS-02)', () => {
  test.afterAll(async () => {
    // Restore the base stack so the global-teardown's `down -v` sees a
    // consistent baseline, and any specs ordered after this one (whether
    // by file-name sort or future additions) see a fresh /healthz==200.
    downStack();
    upBaseStack();
    // Re-poll /healthz==200 so the next spec doesn't race the boot.
    await waitForHealth('http://localhost:8080/healthz', 200, 30_000);
  });

  test('eacces: /healthz returns 503 with the Pitfall 9 remediation hint', async () => {
    downStack();
    upStackWithOverride('compose.test.override.eacces.yml');
    const r = await waitForHealth('http://localhost:8080/healthz', 503, 30_000);
    expect(r.status).toBe(503);
    expect(r.body.status).toBe('unhealthy');
    // VERBATIM strings from CONTEXT.md "Healthz Remediation Hints":
    // {"status":"unhealthy","reason":"docker socket permission denied —
    //   set compose user: '65532:$(id -g docker)' (Pitfall 9)"}
    expect(r.body.reason).toContain('docker socket permission denied');
    expect(r.body.reason).toContain("65532:$(id -g docker)");
  });

  test('no-socket: /healthz returns 503 with the bind-mount remediation hint', async () => {
    downStack();
    upStackWithOverride('compose.test.override.no-socket.yml');
    const r = await waitForHealth('http://localhost:8080/healthz', 503, 30_000);
    expect(r.status).toBe(503);
    expect(r.body.status).toBe('unhealthy');
    // VERBATIM strings from CONTEXT.md "Healthz Remediation Hints":
    // {"status":"unhealthy","reason":"docker socket missing — add
    //   bind-mount '/var/run/docker.sock:/var/run/docker.sock'"}
    expect(r.body.reason).toContain('docker socket missing');
    expect(r.body.reason).toContain('/var/run/docker.sock:/var/run/docker.sock');
  });
});
