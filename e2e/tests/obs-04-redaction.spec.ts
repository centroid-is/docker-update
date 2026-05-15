// OBS-04 (output-side defense) — Across a full poll sweep, the
// docker-update stdout (captured via `docker compose logs --no-color`)
// must contain ZERO occurrences of:
//   - /Bearer /         (a Bearer-token header value)
//   - /Authorization:/i (a raw Authorization header line)
//   - /Basic Og==/      (the WUD 8.2.2 Pitfall 2 regression literal —
//                        the empty-creds placeholder `Basic Og==`
//                        that broke layer pulls)
//
// AFFIRMATIVE CHECK: the boot attestation event
// `slog.Info("registry.authn", "keychain", "anonymous")` MUST be
// present in the captured logs. Without this check the spec could
// false-green on an empty log stream (e.g. wrong container name) and
// silently mask a redaction regression.
//
// Tolerances (assumes `make e2e-cron-fast` provides DOCKER_UPDATE_CRON=@every 5s):
//   - wait for last_poll_end to advance past baseline: up to 8s.
//   - then capture logs and grep.
//
// RED-FIRST (Plan 03-05 Task 0): this spec lands BEFORE the slog
// ReplaceAttr regex is wired in cmd/docker-update/main.go. On a clean
// Phase 03-04 binary the spec MAY false-green if no Bearer string ever
// appears in logs (zot's anonymous-pull doesn't issue Bearer challenge
// in the happy path) — that's accepted under the RED-FIRST contract.
// The substantive proof comes from the affirmative `registry.authn`
// match (a positive assertion proving the log stream IS the one we
// captured) and the Task 1 unit tests in cmd/docker-update/main_test.go
// covering the regex + substring redactor.

import { execSync } from 'node:child_process';
import { setTimeout as sleep } from 'node:timers/promises';
import { expect, test } from '@playwright/test';

type StateBody = {
  version: number;
  containers: Record<string, unknown>;
  last_poll_start?: string;
  last_poll_end?: string;
};

const COMPOSE_BASE = 'docker compose -f compose.test.yml';

test('obs-04-redaction: docker-update stdout contains zero Bearer/Authorization matches across a full poll sweep', async ({
  request,
}) => {
  // (a) Read /api/state once and capture last_poll_end baseline. On a
  // cold boot it may be missing/empty; wait up to 8s for it to first
  // populate.
  const baselineDeadline = Date.now() + 8_000;
  let baseline = '';
  while (Date.now() < baselineDeadline) {
    const resp = await request.get('/api/state');
    const state = (await resp.json()) as StateBody;
    if (state.last_poll_end) {
      baseline = state.last_poll_end;
      break;
    }
    await sleep(500);
  }
  // (b) Wait until last_poll_end advances past baseline (≤8s at @every 5s).
  const advanceDeadline = Date.now() + 8_000;
  let advanced = false;
  while (Date.now() < advanceDeadline) {
    const resp = await request.get('/api/state');
    const state = (await resp.json()) as StateBody;
    if (state.last_poll_end && state.last_poll_end !== baseline) {
      advanced = true;
      break;
    }
    await sleep(500);
  }
  expect(
    advanced,
    `last_poll_end never advanced past baseline ${JSON.stringify(baseline)} within 8s — cron is not running fast enough OR the binary is not wired`,
  ).toBe(true);

  // (c) Capture docker-update's stdout+stderr across the full poll sweep.
  // Use `--no-color` to keep grep deterministic.
  const logs = execSync(`${COMPOSE_BASE} logs --no-color docker-update`, {
    encoding: 'utf8',
  });

  // (d) Regression guards: zero Bearer / Authorization / Basic Og== matches.
  expect(logs, 'docker-update logs must not contain a Bearer token literal').not.toMatch(/Bearer /);
  expect(logs, 'docker-update logs must not contain Authorization: header line').not.toMatch(
    /Authorization:/i,
  );
  expect(
    logs,
    'docker-update logs must not contain Pitfall 2 literal Basic Og== (empty-creds placeholder)',
  ).not.toMatch(/Basic Og==/);

  // (e) Affirmative: confirm the slog stream IS the one captured.
  // The boot attestation event `slog.Info("registry.authn", "keychain", "anonymous")`
  // is emitted at main.go step 4.7. If it's missing, the spec is grepping
  // the wrong stream and the redaction assertions are meaningless.
  expect(logs, 'registry.authn boot attestation event must appear in logs').toMatch(
    /registry\.authn.*anonymous/,
  );
});
