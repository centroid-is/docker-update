import { execSync } from 'node:child_process';
import { setTimeout as sleep } from 'node:timers/promises';

import { pushFreshManifest } from './fixtures/push-image';

const COMPOSE = ['docker', 'compose', '-f', 'compose.test.yml'];

async function waitForHealth(url: string, timeoutMs: number) {
  const deadline = Date.now() + timeoutMs;
  let lastErr: unknown;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url);
      if (res.ok) return;
      lastErr = new Error(`status ${res.status}`);
    } catch (err) {
      lastErr = err;
    }
    await sleep(500);
  }
  throw new Error(`Healthcheck never returned 200: ${url} (last error: ${String(lastErr)})`);
}

export default async function globalSetup() {
  // Bring the stack up. We use `up -d --wait` for parity with the
  // documented pattern, but neither zot nor hmi-update has a compose-side
  // healthcheck (their runtime images are distroless and lack wget /
  // curl / sh — see comments in compose.test.yml). The host-side polls
  // below are the actual readiness gates for this phase.
  execSync([...COMPOSE, 'up', '-d', '--wait'].join(' '), { stdio: 'inherit' });

  // Wait until zot's OCI Distribution endpoint answers GET /v2/ — this
  // is the standard registry liveness probe. The host port matches the
  // mapping in compose.test.yml (15000 -> 5000) so we avoid the macOS
  // Control Center port-5000 conflict.
  const zotPort = process.env.ZOT_HOST_PORT ?? '15000';
  await waitForHealth(`http://localhost:${zotPort}/v2/`, 30_000);

  // Push the initial :latest manifest into zot so any test that resolves
  // centroid-is/stub gets a real digest. The helper lives in
  // fixtures/push-image.ts so Phase 3 mid-test pushes share the code path.
  pushFreshManifest('centroid-is/stub');

  // Finally, wait for hmi-update's /healthz to return 200. The binary
  // starts as soon as the container starts, but the http listener may
  // not be bound yet when `up -d --wait` returns.
  await waitForHealth('http://localhost:8080/healthz', 30_000);
}
