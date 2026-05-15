// Phase 4 plan 04-06 — ACT-06 + ACT-07 (idempotent no-op responses)
//
// ACT-06: Update is idempotent — if CurrentDigest === AvailableDigest (i.e.
//         already at the upstream :latest), the orchestrator short-circuits
//         and returns 200 {"no_op":true, "current_digest":"sha256:..."}.
// ACT-07: Rollback is idempotent — if CurrentDigest === PreviousDigest, the
//         orchestrator short-circuits and returns 200 {"no_op":true}. At
//         e2e level this branch is unreachable through the public API
//         (the orchestrator's toggle always keeps current != previous).
//         The canonical e2e proof of "Rollback is idempotency-safe" is the
//         no_previous_digest 400 path, which fires when a container has
//         never been updated. The true CurrentDigest === PreviousDigest
//         200 no_op branch is pinned by the orchestrator unit test
//         TestRollback_Idempotent_NoOp (internal/actions/orchestrator_test.go).
//
// Wire contract: internal/api/handlers_actions.go::writeActionResult emits
// {"current_digest":"...","previous_digest":"...","no_op":true} when
// ActionResult.NoOp == true. previous_digest and no_op carry omitempty;
// the no_op:true key MUST be present on idempotent responses.
//
// Tolerances: each test runs at most one POST that engages the
// orchestrator end-to-end (the no-op branch returns immediately after
// the idempotency check, no compose recreate). 60s per-test timeout
// accommodates the prelude Update on ACT-06.

import { setTimeout as sleep } from 'node:timers/promises';
import { expect, test } from '@playwright/test';

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

test('idempotency: ACT-06 Update returns no_op when current_digest === available_digest', async ({
  request,
}) => {
  test.setTimeout(60_000);

  // Drive into the "already up to date" state: push manifest, wait for
  // flip, run Update (now CurrentDigest === AvailableDigest). A second
  // Update is the idempotency-trigger path.
  const pushed = pushFreshManifest('centroid-is/stub');
  await waitForCondition<Container>(
    request,
    (state) => {
      const c = state.containers?.['stub-watched-container'];
      if (c && c.update_available === true && c.available_digest === pushed) return c;
      return undefined;
    },
    10_000,
    'update_available === true before first Update',
  );
  const firstUpdate = await request.post('/api/containers/stub-watched-container/update');
  expect(firstUpdate.status(), 'first Update succeeds').toBe(200);
  const firstBody = (await firstUpdate.json()) as { current_digest: string };
  expect(firstBody.current_digest).toBe(pushed);

  // ACT-06: second Update — orchestrator short-circuits on idempotency.
  const noopResp = await request.post('/api/containers/stub-watched-container/update');
  expect(noopResp.status(), 'ACT-06 no-op Update should return 200').toBe(200);
  const noopBody = (await noopResp.json()) as {
    current_digest: string;
    previous_digest?: string;
    no_op?: boolean;
  };
  expect(noopBody.no_op, 'ACT-06: body.no_op must be true').toBe(true);
  expect(noopBody.current_digest).toMatch(/^sha256:/);
  expect(noopBody.current_digest, 'no_op response carries unchanged current_digest').toBe(pushed);
});

test('idempotency: ACT-07 Rollback without previous_digest returns 400 no_previous_digest', async ({
  request,
}) => {
  // ACT-07 — Rollback safety. The orchestrator returns 400
  // no_previous_digest when the container has never been updated.
  // This is the e2e-reachable branch of Rollback idempotency safety.
  // The true CurrentDigest === PreviousDigest 200 no_op branch requires
  // direct state-store surgery (unreachable via the public API); it is
  // covered by the orchestrator unit test TestRollback_Idempotent_NoOp.
  //
  // Target container: invalid-pattern-stub — has hmi-update.watch=true so
  // LookupContainer returns it from cached state, but it has no
  // previous_digest (it has never been Updated; its tag-pattern label is
  // broken so the poller flags it with notes="invalid tag-pattern label,
  // ignored" but does not set AvailableDigest, and no Update has occurred
  // against it).
  //
  // Wire contract per handlers_actions.go::isNoPreviousDigest + Plan 04-04
  // error-status mapping: orchestrator emits wrap chain
  //   "actions.Rollback invalid-pattern-stub: no_previous_digest"
  // handler dispatches via substring match to 400 actionBodyNoPreviousDigest:
  //   {"error":"no_previous_digest","detail":"rollback requires a recorded
  //    previous digest; perform an Update first"}
  const resp = await request.post('/api/containers/invalid-pattern-stub/rollback');
  expect(resp.status(), 'ACT-07: Rollback without previous_digest must be 400').toBe(400);
  const body = (await resp.json()) as { error?: string; detail?: string };
  expect(body.error).toBe('no_previous_digest');
  expect(body.detail).toContain('previous digest');
});
