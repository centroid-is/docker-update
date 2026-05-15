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
  // Plan 03-05 Task 4 — PRE-SEED docker images BEFORE `docker compose
  // up -d --wait`. The compose stack uses `pull_policy: never` for
  // stub-watched-container + timescaledb-stub with `zot:5000/...`
  // prefixed image refs. The docker daemon cannot resolve `zot:5000`
  // by hostname (that's a compose-network alias), so we re-tag the
  // already-cached busybox image under the expected tags before
  // `compose up`. Without this, compose would fail with "No such image".
  //
  // pinned-stub uses busybox@<digest> — a separate cache key from
  // busybox:latest, hence the explicit second pull.
  //
  // The Makefile e2e* recipes do the same pre-seed before invoking
  // Playwright; this code is a backup for `npx playwright test`
  // invocations that bypass the Makefile (e.g. local debugging).
  execSync(`docker pull busybox:latest`, { stdio: 'pipe' });
  execSync(`docker tag busybox:latest zot:5000/centroid-is/stub:latest`, {
    stdio: 'pipe',
  });
  execSync(`docker tag busybox:latest zot:5000/timescale/timescaledb:latest-pg17`, {
    stdio: 'pipe',
  });
  execSync(
    `docker pull busybox@sha256:1487d0af5f52b4ba31c7e465126ee2123fe3f2305d638e7827681e7cf6c83d5e`,
    { stdio: 'pipe' },
  );

  // Bring the stack up — but use --no-recreate so we don't clobber an
  // already-running stack that the Makefile target may have started
  // with a layered override (e.g. compose.test.override.cron-fast.yml).
  //
  // Recreating without the override is hazardous on macOS / Docker
  // Desktop because the HMI_DOCKER_GID env-var interpolation in the
  // base compose.test.yml is consumed by `compose up` against the
  // shell that invokes it. The Makefile recipe exports HMI_DOCKER_GID
  // explicitly; this globalSetup runs inside Node's child_process
  // which does NOT inherit shell-local env unless explicitly forwarded.
  // --no-recreate guarantees that if the Makefile already brought up
  // the stack with the correct GID + override, this idempotent `up`
  // touches nothing.
  //
  // Standalone invocations (`npx playwright test` without `make
  // e2e-cron-fast`): the stack isn't up yet, so `up --no-recreate`
  // behaves like a normal `up` and creates fresh containers. The
  // override is then NOT applied — production-default cron tick of
  // 60min applies. Phase 3's flip specs require the override to land
  // in <10s; running them via `make e2e-cron-fast` is the canonical
  // invocation.
  execSync([...COMPOSE, 'up', '-d', '--wait', '--no-recreate'].join(' '), { stdio: 'inherit' });

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

  // Seed zot with a :latest-pg17 manifest so the cronPoller has a
  // digest to fetch when the Discoverer registers timescaledb-stub.
  // Uses the Plan 03-05 Task 2 two-arg pushFreshManifest signature.
  pushFreshManifest('timescale/timescaledb', { tag: 'latest-pg17' });

  // Finally, wait for docker-update's /healthz to return 200. The binary
  // starts as soon as the container starts, but the http listener may
  // not be bound yet when `up -d --wait` returns.
  await waitForHealth('http://localhost:8080/healthz', 30_000);

  // Plan 03-05 timing invariant — wait for the cronPoller to observe
  // the seed pushes BEFORE Playwright starts the first test.
  //
  // Why this matters: DETECT-07 (single-arch flip) asserts
  // `update_available === true` AFTER a mid-test push. The flip rule
  // in internal/poll.handleFetchResult requires a PRIOR
  // AvailableDigest different from the resolved one. If a test
  // pushes before the cronPoller has observed the seed digest, the
  // first cron tick sees ONLY the test's fresh push:
  //   priorAvailable=""    → no flip (we have nothing to compare).
  //   priorAvailable=fresh → fresh != fresh → no flip on next tick.
  // The result: update_available never goes true and DETECT-07 fails
  // intermittently depending on test-vs-cron timing.
  //
  // Solution: synchronously block here until last_poll_end advances,
  // proving the seed digests have been recorded as
  // AvailableDigest. Subsequent test pushes will then see a
  // different prior digest and the flip rule kicks in.
  //
  // 15s deadline: at DOCKER_UPDATE_CRON=@every 5s, two cron ticks fit
  // comfortably. At the production default 0 * * * * (hourly), the
  // wait WILL exceed 15s and globalSetup throws — that's correct
  // behavior because the Phase 3 e2e flip tests REQUIRE the
  // cron-fast override, and a globalSetup running against
  // production cron should fail loudly.
  await waitForPollAdvance(15_000);
}

// waitForPollAdvance ensures that at least one cron sweep has
// completed AFTER the function is invoked. Captures last_poll_end
// at entry; polls /api/state until last_poll_end advances past that
// baseline. Used in globalSetup to synchronise with the cronPoller
// AFTER pushing the seed manifests, guaranteeing the seed digests
// have been recorded as AvailableDigest before any test runs.
//
// Throws if no advance is observed within the deadline.
async function waitForPollAdvance(timeoutMs: number): Promise<void> {
  let baseline: string | undefined;
  try {
    const res = await fetch('http://localhost:8080/api/state');
    if (res.ok) {
      const body = (await res.json()) as { last_poll_end?: string };
      baseline = body.last_poll_end;
    }
  } catch {
    // ignore; baseline stays undefined and any non-empty
    // last_poll_end will satisfy the predicate below.
  }

  const deadline = Date.now() + timeoutMs;
  let lastEnd: string | undefined;
  while (Date.now() < deadline) {
    try {
      const res = await fetch('http://localhost:8080/api/state');
      if (res.ok) {
        const body = (await res.json()) as { last_poll_end?: string };
        if (body.last_poll_end && body.last_poll_end !== baseline) {
          lastEnd = body.last_poll_end;
          return;
        }
      }
    } catch {
      // ignore transient connection errors — server may be momentarily
      // unavailable mid-restart.
    }
    await sleep(500);
  }
  throw new Error(
    `cronPoller never advanced past baseline ${JSON.stringify(baseline)} within ${timeoutMs}ms ` +
      `(last seen: ${String(lastEnd)}). Phase 3 e2e flip specs require ` +
      `DOCKER_UPDATE_CRON=@every 5s; did you run via 'make e2e-cron-fast'?`,
  );
}
