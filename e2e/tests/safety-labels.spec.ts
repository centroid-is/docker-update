// Phase 4 plan 04-06 — SAFE-01 + SAFE-02 + SAFE-03 (server-side safety labels)
//
// SAFE-01: hmi-update.allow-update=false on a watched container → POST /update
//          returns 409 action_disabled_by_label with detail mentioning
//          hmi-update.allow-update=false. The middleware writes the body
//          BEFORE the orchestrator runs — see internal/actions/middleware.go::
//          CheckSafetyLabel + actions.ActionBodyActionDisabledUpdate.
// SAFE-02: hmi-update.allow-rollback=false on a watched container → POST
//          /rollback returns 409 with detail mentioning
//          hmi-update.allow-rollback=false. Same middleware path.
// SAFE-03: The cron poller IGNORES the allow-update / allow-rollback labels.
//          A safety-locked container still gets last_polled_at advances on
//          each cron tick — the labels gate ACTIONS, not OBSERVATIONS.
//          Pinned by Go-level grep test in internal/actions/middleware_test.go::
//          TestSAFE03_PollIgnoresActionLabels; this is the wire-side proof.
//
// Test fixtures: timescaledb-stub (Plan 04-06 Task 1 added both labels to
// it — see e2e/compose.test.yml). Both SAFE-01 and SAFE-02 fire against
// the same container; SAFE-03 also uses timescaledb-stub.
//
// Wire contracts (consumed verbatim from internal/actions/middleware.go
// EXPORTED constants):
//   ActionBodyActionDisabledUpdate   = `{"error":"action_disabled_by_label","detail":"hmi-update.allow-update=false"}`
//   ActionBodyActionDisabledRollback = `{"error":"action_disabled_by_label","detail":"hmi-update.allow-rollback=false"}`
//
// Tolerances: SAFE-01 + SAFE-02 are synchronous — middleware short-circuits
// before any compose call (no waitForCondition needed). SAFE-03 requires
// waiting one cron tick to observe last_polled_at advancing; at
// HMI_UPDATE_CRON=@every 5s that's ≤7s wall-clock per advance.

import { setTimeout as sleep } from 'node:timers/promises';
import { expect, test } from '@playwright/test';

type Container = {
  service?: string;
  last_polled_at?: string;
};

type StateBody = { version: number; containers: Record<string, Container> };

async function getState(
  request: import('@playwright/test').APIRequestContext,
): Promise<StateBody> {
  const resp = await request.get('/api/state');
  expect(resp.ok()).toBe(true);
  return (await resp.json()) as StateBody;
}

test('safety-labels: SAFE-01 POST /update on timescaledb-stub returns 409 action_disabled_by_label', async ({
  request,
}) => {
  const resp = await request.post('/api/containers/timescaledb-stub/update');
  expect(resp.status(), 'SAFE-01 must return 409').toBe(409);
  const body = (await resp.json()) as { error?: string; detail?: string };
  expect(body.error).toBe('action_disabled_by_label');
  expect(
    body.detail,
    'detail must name the responsible label (hmi-update.allow-update=false)',
  ).toContain('hmi-update.allow-update=false');
});

test('safety-labels: SAFE-02 POST /rollback on timescaledb-stub returns 409 action_disabled_by_label', async ({
  request,
}) => {
  // SAFE-02 — middleware order is ValidateServiceName → CheckSelfProtection
  // → LookupContainer → CheckSafetyLabel. CheckSafetyLabel(ActionRollback)
  // fires BEFORE the orchestrator's no_previous_digest check, so we get
  // 409 even though timescaledb-stub has no previous_digest (it has never
  // been Updated). This is the load-bearing middleware-precedence
  // contract: safety labels gate at the perimeter, not inside the action body.
  const resp = await request.post('/api/containers/timescaledb-stub/rollback');
  expect(resp.status(), 'SAFE-02 must return 409').toBe(409);
  const body = (await resp.json()) as { error?: string; detail?: string };
  expect(body.error).toBe('action_disabled_by_label');
  expect(
    body.detail,
    'detail must name the responsible label (hmi-update.allow-rollback=false)',
  ).toContain('hmi-update.allow-rollback=false');
});

test('safety-labels: SAFE-03 timescaledb-stub.last_polled_at advances across cron ticks (poll ignores action labels)', async ({
  request,
}) => {
  // SAFE-03 — the poll loop ignores allow-update/allow-rollback. We
  // capture last_polled_at on timescaledb-stub, fire a 409 Update (proves
  // the SAFE-01 middleware path is engaged), sleep through at least one
  // cron tick, and assert last_polled_at advanced.
  //
  // Cron cadence: HMI_UPDATE_CRON=@every 5s under the cron-fast override;
  // 7s wall-clock is comfortable margin for one tick.
  test.setTimeout(20_000);

  const before = await getState(request);
  const beforePoll = before.containers?.['timescaledb-stub']?.last_polled_at;
  expect(beforePoll, 'timescaledb-stub must already have a last_polled_at baseline').toBeTruthy();

  // Fire a 409 Update — proves the safety-label middleware is engaged.
  // This must not affect last_polled_at (the poll loop is independent).
  const refused = await request.post('/api/containers/timescaledb-stub/update');
  expect(refused.status()).toBe(409);

  // Wait long enough for at least one cron sweep (@every 5s + slack).
  await sleep(7000);

  const after = await getState(request);
  const afterPoll = after.containers?.['timescaledb-stub']?.last_polled_at;
  expect(afterPoll, 'last_polled_at must still be populated after the cron tick').toBeTruthy();
  expect(afterPoll, 'SAFE-03: last_polled_at must advance for safety-locked containers').not.toBe(
    beforePoll,
  );
  // RFC3339Nano: ISO-8601 with monotone time; Date.parse is reliable.
  expect(
    new Date(afterPoll as string).getTime(),
    'last_polled_at must strictly increase (wall-clock monotone)',
  ).toBeGreaterThan(new Date(beforePoll as string).getTime());
});
