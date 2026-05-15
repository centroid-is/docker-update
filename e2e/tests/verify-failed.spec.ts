// Phase 4 plan 04-06 — Pitfalls 4 + 12 (verify-after-recreate failure path)
//
// Plan 04-03's verifyAfterRecreate observes Running + RestartCount on
// the freshly-recreated container. Pitfall 4: recreate succeeds but the
// container immediately crash-loops. The 15-second verify window catches
// the RestartCount > pre-action snapshot (=0 for a freshly-recreated
// container) signal and returns *VerifyDetail wrapped in ErrVerifyFailed.
//
// Plan 04-04's writeVerifyFailedBody (the SOLE Pattern K exception per
// T-04-04-03) extracts the *VerifyDetail via errors.As and emits the
// LOCKED structured JSON shape (CONTEXT.md Area 3 lines 102-112):
//
//   {
//     "error": "verify_failed",
//     "reason": "container restarted N times in 15s",
//     "exit_code": null,
//     "restart_count": N,
//     "running": false,
//     "container_id": "..."
//   }
//
// The reason string is constructed by the orchestrator via fmt.Sprintf
// over integers + duration (no operator paths in the trim domain).
//
// IMPORTANT (file header carries the warning for future readers): the
// compose stack INTENTIONALLY contains a crash-loop-stub service with
// command: sh -c 'exit 1' under restart: unless-stopped. It exists ONLY
// for this spec. Specs that iterate state.containers will see this entry
// with Stopped:true and a stream of die/start events. Do NOT assume only
// "happy" containers populate /api/state.
//
// Wire contract: POST /api/containers/crash-loop-stub/update → 500 with
// the structured body. Per CONTEXT.md A7, 60s test.setTimeout is the
// canonical extension to cover pull=1s + recreate=5s + 15s verify + 39s slack.
//
// Threat-model note (T-04-04-03 + T-01-04-03): the verify-failed body is
// the LOAD-BEARING path-leak guard at the e2e layer — handlers_actions_test.go's
// TestHandleActions_PathLeakGuard pins the unit-level assertion; this
// spec pins the wire-level assertion. Both must pass; both guard the
// same invariant.

import { setTimeout as sleep } from 'node:timers/promises';
import { expect, test } from '@playwright/test';

type Container = {
  service?: string;
  stopped?: boolean;
};

type StateBody = { version: number; containers: Record<string, Container> };

async function waitForContainer(
  request: import('@playwright/test').APIRequestContext,
  service: string,
  timeoutMs: number,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastBody: StateBody | null = null;
  while (Date.now() < deadline) {
    const resp = await request.get('/api/state');
    if (resp.ok()) {
      lastBody = (await resp.json()) as StateBody;
      if (lastBody.containers?.[service]) return;
    }
    await sleep(500);
  }
  throw new Error(
    `service ${service} never appeared in /api/state within ${timeoutMs}ms. last body: ${JSON.stringify(lastBody)}`,
  );
}

// DEFERRED to Plan 04-07 (D-04-06-01): the orchestrator's Update flow hits
// daemon-side ImagePull (cannot resolve `zot:5000`) before it ever reaches
// the verify-after-recreate loop, so the spec observes "pull_failed" instead
// of the locked "verify_failed" body. Body preserved verbatim for post-04-07
// activation. The verify-after-recreate code path is exhaustively pinned by
// Go unit tests (TestVerifyAfterRecreate_* in internal/actions/verify_test.go).
test.skip('verify-failed: crash-loop-stub update returns 500 with structured verify_failed body', async ({
  request,
}) => {
  // verifyAfterRecreate runs the full 15-second window before failing;
  // pull=1s + recreate=5s + 15s verify + margin → 60s test timeout per
  // CONTEXT.md A7 + Plan 04-06 acceptance criterion.
  test.setTimeout(60_000);

  // Sanity check: crash-loop-stub must be discoverable so the action
  // endpoint's LookupContainer middleware can resolve it. The container
  // is INTENTIONALLY crash-looping under restart: unless-stopped and
  // labeled hmi-update.watch=true (see e2e/compose.test.yml).
  await waitForContainer(request, 'crash-loop-stub', 60_000);

  const resp = await request.post('/api/containers/crash-loop-stub/update');
  expect(resp.status(), 'verify_failed must surface HTTP 500').toBe(500);

  const body = (await resp.json()) as {
    error?: string;
    reason?: string;
    exit_code?: number | null;
    restart_count?: number;
    running?: boolean;
    container_id?: string;
  };

  // Field-by-field shape match per CONTEXT.md Area 3 lines 102-112.
  expect(body.error, 'error field is the verify_failed sentinel').toBe('verify_failed');
  expect(body.reason, 'reason mentions restart or running counter').toMatch(/restart|running/i);
  expect(body.exit_code, 'exit_code is null per locked shape').toBeNull();
  expect(body, 'restart_count key must be present').toHaveProperty('restart_count');
  expect(body.restart_count, 'restart_count is an integer').toEqual(expect.any(Number));
  expect(body, 'running key must be present').toHaveProperty('running');
  expect(body.running, 'running is a bool').toEqual(expect.any(Boolean));
  expect(body, 'container_id key must be present').toHaveProperty('container_id');
  expect(body.container_id, 'container_id is a string').toEqual(expect.any(String));
});
