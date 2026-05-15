// DOCK-02 — Compose file replaced via atomic rename triggers
// GET /debug/compose-stat returning 412 + the verbatim drift response.
//
// The /debug/compose-stat route is registered only when the binary is
// built with -tags=debug (per plan 02-04's debug_compose.go). This spec
// auto-skips when the route is not present (production build) — that
// lets it co-exist with the regular `make e2e` run without breaking the
// production-image path. To run this spec affirmatively, use
// `make e2e-debug` which brings up the stack with
// compose.test.override.debug.yml flipping GO_TAGS=debug at build time.
//
// IDEMPOTENCY (see plan 02-05 truths):
//   The drift trigger flips the compose file's inode from the
//   container's POV. The docker-update process compares stat'd
//   {inode, mtime, size} against the boot-time snapshot, so once flipped
//   the reader returns ErrComposeFileMoved on every subsequent call.
//   afterAll restarts the docker-update service so its in-memory snapshot
//   is re-seeded from the (now-renamed) file's current state, and
//   subsequent specs see a clean /healthz + /debug/compose-stat=200.

import { execSync } from 'node:child_process';
import { readFileSync, renameSync, writeFileSync } from 'node:fs';
import { setTimeout as sleep } from 'node:timers/promises';
import { expect, test } from '@playwright/test';

const COMPOSE_BASE = 'docker compose -f compose.test.yml';

async function waitForHealth(url: string, status: number, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const r = await fetch(url);
      if (r.status === status) return;
    } catch {
      /* network not up yet */
    }
    await sleep(500);
  }
  throw new Error(`never reached ${url} with status ${status}`);
}

test.describe.serial('compose drift detection (DOCK-02)', () => {
  test.afterAll(async () => {
    // Re-seed the docker-update container's in-memory compose snapshot.
    // Without this, /debug/compose-stat returns 412 forever — every
    // follow-on spec inherits the drifted state. `docker compose
    // restart` re-execs the process; main.go runs compose.NewReader
    // again at boot and reads the now-current inode+mtime.
    execSync(`${COMPOSE_BASE} restart docker-update`, { stdio: 'pipe' });
    // Poll /healthz until 200 (proves the rebooted process is past
    // its boot list and ready to serve).
    await waitForHealth('http://localhost:8080/healthz', 200, 30_000);
  });

  test('compose drift: atomic rename of compose file detected by /debug/compose-stat', async ({
    request,
  }) => {
    // Precondition: skip when the debug route is not registered. This
    // distinguishes a production image (404) from a debug image
    // (200 ok / 412 moved). The skip message documents how to run
    // affirmatively.
    const probe = await request.get('/debug/compose-stat');
    if (probe.status() === 404) {
      test.skip(
        true,
        '/debug/compose-stat not registered — production build (run via `make e2e-debug` for this spec)',
      );
      return;
    }

    // The docker-update container bind-mounts e2e/compose.test.yml to
    // /host/docker-compose.yml. The reader inside the container stat'd
    // the IN-CONTAINER path at boot. Modifying the HOST file via
    // atomic rename changes the bind-mounted file's inode from the
    // container's perspective too.
    //
    // Playwright cwd is e2e/ (per the COMPOSE_BASE relative path
    // above). The compose file is at ./compose.test.yml relative to
    // that cwd.
    const composePath = './compose.test.yml';
    const original = readFileSync(composePath);
    const tmp = `${composePath}.drift-${Date.now()}`;
    writeFileSync(tmp, original); // identical content; only the inode changes
    try {
      renameSync(tmp, composePath); // atomic; inode flips
      // Give the container a beat to see the new inode (no async path
      // needed — stat is synchronous from the container's POV, but
      // give the kernel a brief window after the rename).
      await sleep(200);

      const drift = await request.get('/debug/compose-stat');
      expect(drift.status()).toBe(412);
      const body = await drift.json();
      // VERBATIM drift response from CONTEXT.md "Healthz Remediation
      // Hints" / plan 02-04 debugComposeStat handler:
      //   {"error":"compose_file_moved","hint":"restart docker-update to
      //    pick up the new docker-compose.yml"}
      expect(body.error).toBe('compose_file_moved');
      expect(body.hint).toBe('restart docker-update to pick up the new docker-compose.yml');
    } finally {
      // Best-effort restore: write the original content back so other
      // tests that re-read the compose file see consistent contents.
      // (Inode drift remains for THIS process — the afterAll restart
      // is what actually re-seeds the in-memory snapshot.)
      try {
        writeFileSync(composePath, original);
      } catch {
        /* ignore — afterAll restart re-seeds regardless */
      }
    }
  });
});
