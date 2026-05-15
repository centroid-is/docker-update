/**
 * rebuild-binary.ts — Plan 05-05 Task 2 fixture helper for
 * ui-inplace-upgrade.spec.ts (UI-10 / Pitfall 8 byte-level proof).
 *
 * Rebuilds the Svelte bundle + the Go binary embedding it, then
 * recreates the `hmi-update` compose service so the running stack
 * serves the new asset hashes. The spec uses this in a per-test
 * setup to assert:
 *   1. /assets/<new-hash>.js carries Cache-Control: immutable +
 *      Content-Type application/javascript
 *   2. /assets/<old-hash>.js returns 404 (NOT index.html fallback —
 *      this is the Pitfall 8 byte-level guard)
 *
 * Execution discipline (05-RESEARCH.md §K.2):
 *   - execFile (not exec) — argv passed as an array; no shell, no
 *     interpolation of attacker-controlled strings. This file's argv
 *     is hardcoded so the discipline is forward-protection, not
 *     active mitigation; T-05-05-04 mitigation either way.
 *   - cwd = the repository root (the directory containing the
 *     Makefile and e2e/). Both `make` targets and `docker compose -f
 *     e2e/compose.test.yml ...` resolve relative to the root.
 *   - Each subprocess stage runs sequentially; failure stops the
 *     chain and bubbles up via the awaited promise rejection.
 *   - Post-restart: poll http://localhost:8080/healthz until 200,
 *     30s deadline (1s interval). The new binary may take a moment
 *     to bind :8080; throw a clear message on timeout.
 *
 * Why subprocess (not docker SDK or compose-go library):
 *   - The host's docker compose CLI is the authoritative compose
 *     runtime for this project (CLAUDE.md C3 single-service-block
 *     constraint; STACK.md §3 subprocess decision). The fixture must
 *     mirror what an operator would run by hand.
 *   - `make ui` and `make build` already encapsulate the multi-step
 *     dance (npm ci → vite build → go build) so the fixture stays
 *     thin.
 *
 * Timing budget (rough, on a dev laptop):
 *   - make ui:       ~10–25s  (npm ci hits cache after first run)
 *   - make build:     ~3–8s   (pure-Go static binary)
 *   - docker compose up -d --build --force-recreate: ~20–40s
 *     (Dockerfile multi-stage rebuilds the runtime image; --build
 *      because the bundle changed)
 *   - /healthz poll:  ~2–10s  (depends on Discoverer first-tick)
 *   Total: ~35–80s per call. The spec runs this ONCE per test, in
 *   test.beforeAll, so the cost is amortized.
 *
 * Threat-register reference: T-05-05-04 (Tampering — rebuild-binary
 * helper subprocess). Argv discipline + hardcoded inputs close the
 * surface; nothing user-controlled flows through.
 */

import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { setTimeout as sleep } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const execFileAsync = promisify(execFile);

/**
 * Repository root resolved at module-load via import.meta.url. The
 * e2e/ package.json declares `"type": "module"`, so __dirname is
 * unavailable and the ESM equivalent is the fileURLToPath() of
 * import.meta.url. Path is e2e/fixtures/rebuild-binary.ts → repo
 * root is two levels up.
 */
const REPO_ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..');

/** Health endpoint to confirm the new binary is serving. */
const HEALTHZ_URL = 'http://localhost:8080/healthz';

/**
 * Maximum wall-clock time to wait for /healthz to return 200 after
 * docker compose finishes the recreate. 30s covers the worst-case
 * cold-start under macOS Docker Desktop; healthy boots converge in
 * 2–5s.
 */
const HEALTHZ_TIMEOUT_MS = 30_000;
const HEALTHZ_POLL_INTERVAL_MS = 1_000;

/**
 * Rebuild + restart the `hmi-update` compose service in-place.
 *
 * Sequence (matches CLAUDE.md TDD-first verify→implement loop's
 * post-implement smoke step):
 *   1. `make ui`     — rebuild Svelte bundle → internal/api/dist/
 *      (new content hashes on changed sources)
 *   2. `make build`  — rebuild Go binary → bin/hmi-update with the
 *      new bundle embedded via //go:embed all:dist
 *   3. `docker compose -f e2e/compose.test.yml up -d --build
 *      --force-recreate hmi-update`
 *      — rebuild the runtime image (new binary inside) and recreate
 *      the container. Other services stay up; the dependency graph
 *      keeps zot + stubs in place.
 *   4. Poll /healthz until 200 or timeout — the new binary is ready
 *      to serve.
 *
 * Throws on any subprocess failure (stderr captured in the promise
 * rejection.message). Throws with a deadline-exceeded message if
 * /healthz does not respond 200 within HEALTHZ_TIMEOUT_MS.
 *
 * Idempotent in the sense that calling twice in a row produces the
 * same final state (same bundle content → same hashes → same docker
 * image layer cache); only the recreate step has observable side
 * effects (container restart).
 */
export async function rebuildAndRestart(): Promise<void> {
  // Step 1: Svelte bundle. `make ui` runs `npm --prefix ui ci &&
  // npm --prefix ui run build` — npm ci hits the lockfile cache so
  // subsequent calls are fast (Vite is the dominant cost).
  await execFileAsync('make', ['ui'], { cwd: REPO_ROOT });

  // Step 2: Go binary. `make build` runs `go build -o bin/hmi-update
  // ./cmd/hmi-update`. CGO_ENABLED=0 is implied by the Dockerfile's
  // env (the host build here may produce a CGO binary, but that
  // never reaches the container — the next step rebuilds via the
  // Dockerfile which forces CGO_ENABLED=0).
  await execFileAsync('make', ['build'], { cwd: REPO_ROOT });

  // Step 3: rebuild + recreate the runtime image. --build forces the
  // multi-stage Dockerfile to re-run the COPY of bin/hmi-update (the
  // bundle is embedded inside that binary, so the runtime image's
  // hash changes too). --force-recreate guarantees the running
  // container is replaced even if compose thinks "nothing changed"
  // (the image SHA differs; --force-recreate covers any edge case
  // where compose memoizes service identity).
  await execFileAsync(
    'docker',
    [
      'compose',
      '-f',
      'e2e/compose.test.yml',
      'up',
      '-d',
      '--build',
      '--force-recreate',
      'hmi-update',
    ],
    { cwd: REPO_ROOT },
  );

  // Step 4: poll /healthz. The new container binds :8080 as soon as
  // main() reaches srv.ListenAndServe — typically <1s after the
  // recreate completes. The 30s ceiling protects against macOS
  // Docker Desktop's occasional networking-stack churn during a
  // force-recreate.
  const deadline = Date.now() + HEALTHZ_TIMEOUT_MS;
  let lastError: unknown;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(HEALTHZ_URL);
      if (res.ok) return;
      lastError = new Error(`status ${res.status}`);
    } catch (err) {
      lastError = err;
    }
    await sleep(HEALTHZ_POLL_INTERVAL_MS);
  }
  throw new Error(
    `rebuildAndRestart: ${HEALTHZ_URL} never returned 200 within ${HEALTHZ_TIMEOUT_MS}ms ` +
      `(last error: ${String(lastError)})`,
  );
}
