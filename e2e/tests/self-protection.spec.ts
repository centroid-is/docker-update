// Phase 4 plan 04-06 — ACT-09 (self-protection: hmi-update refuses to act on itself)
//
// ACT-09: POST /api/containers/hmi-update/{update,rollback,force-pull} returns
//         409 self_protection with detail pointing the operator at
//         PROJECT.md's Manual self-upgrade procedure. The middleware
//         CheckSelfProtection runs BEFORE LookupContainer specifically so
//         hmi-update is rejected even though it is NOT in the watched-
//         containers state cache (hmi-update.watch label defaults to false
//         on the self container).
//
// Wire contract: middleware emits ActionBodySelfProtection verbatim:
//   `{"error":"self_protection","detail":"see PROJECT.md 'Manual self-upgrade procedure'"}`
// internal/actions/middleware.go exports the constant; internal/api/handlers_actions.go
// imports + reuses it (single source of truth).
//
// CRITICAL: this spec is the wire-side proof of the B1 invariant from
// Plan 04-03 review — without CheckSelfProtection-before-LookupContainer,
// POST /api/containers/hmi-update/update would 404 (misleading) instead
// of 409 (operator-actionable). The 04-04 unit test
// TestHandleActions_SelfProtection_BeforeLookup pins the source order;
// this spec pins the wire result.
//
// Three tests in this file: update / rollback / force-pull?recreate=true.
// (Plain force-pull without recreate is ALSO 409 — middleware
// CheckSelfProtection runs unconditionally regardless of the recreate
// query — but the recreate=true case is the operationally more dangerous
// path so we pin it.)

import { expect, test } from '@playwright/test';

test('self-protection: ACT-09 POST /api/containers/hmi-update/update returns 409 self_protection', async ({
  request,
}) => {
  const resp = await request.post('/api/containers/hmi-update/update');
  expect(resp.status(), 'ACT-09: POST /update on self must be 409').toBe(409);
  const body = (await resp.json()) as { error?: string; detail?: string };
  expect(body.error).toBe('self_protection');
  expect(
    body.detail,
    'detail must point operator at PROJECT.md self-upgrade procedure',
  ).toContain('PROJECT.md');
});

test('self-protection: ACT-09 POST /api/containers/hmi-update/rollback returns 409 self_protection', async ({
  request,
}) => {
  const resp = await request.post('/api/containers/hmi-update/rollback');
  expect(resp.status(), 'ACT-09: POST /rollback on self must be 409').toBe(409);
  const body = (await resp.json()) as { error?: string; detail?: string };
  expect(body.error).toBe('self_protection');
  expect(body.detail).toContain('PROJECT.md');
});

test('self-protection: ACT-09 POST /api/containers/hmi-update/force-pull (no recreate) returns 409 self_protection', async ({
  request,
}) => {
  // Force-pull-no-recreate is read-only with respect to the running
  // container (SAFE-03 carve-out for the safety-label middleware), but
  // self-protection runs UNCONDITIONALLY — the operator must not be able
  // to recreate hmi-update via the self-served API regardless of recreate
  // query value.
  const resp = await request.post('/api/containers/hmi-update/force-pull');
  expect(resp.status(), 'force-pull on self must be 409 even without recreate').toBe(409);
  const body = (await resp.json()) as { error?: string };
  expect(body.error).toBe('self_protection');
});

test('self-protection: ACT-09 POST /api/containers/hmi-update/force-pull?recreate=true returns 409 self_protection', async ({
  request,
}) => {
  // recreate=true is the operationally dangerous path — it would invoke
  // the full Update flow, which on the self container would terminate the
  // server mid-request. The middleware MUST reject this 409 before any
  // pull/recreate work begins.
  const resp = await request.post('/api/containers/hmi-update/force-pull?recreate=true');
  expect(resp.status(), 'force-pull?recreate=true on self must be 409').toBe(409);
  const body = (await resp.json()) as { error?: string };
  expect(body.error).toBe('self_protection');
});
