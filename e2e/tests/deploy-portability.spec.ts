/**
 * deploy-portability.spec.ts — DEPLOY-05 / Phase 7 Success Criterion #1
 *
 * RED-first per C4: this spec runs against docker-compose.example.yml
 * (the brief §F7 reference deployment). Acceptance is:
 *   "copying docker-compose.yml to a second clean Debian 12 host and
 *    running `docker compose up -d` (with the documented
 *    user: '65532:<docker-gid>' edit) produces a working install with
 *    the table loading at :8080 and no manual UI steps."
 *
 * Strategy (Phase 7 RESEARCH §7): run on the ubuntu-24.04 CI runner as a
 * surrogate Debian 12 environment. Real-Debian-12 verification is the
 * manual smoke step (Phase 7 success criterion #5).
 *
 * Gating: this spec runs only when DEPLOY_PORTABILITY=1 is in the env.
 * The default `make e2e` invocation does NOT set it, so the existing
 * Phase 1+2+3+4+5+6 suites are untouched. Phase 7's CI workflow sets the
 * env var for the dedicated portability gate.
 *
 * Wall-clock budget: ~30s (image is built fresh once per CI run; the
 * docker layer cache absorbs subsequent invocations; compose up --wait
 * is the dominant cost).
 *
 * Pitfall 9 prevention: the in-alpine-stat detection of the docker
 * socket GID is the SAME technique the Makefile e2e target uses. A
 * host-side `id -g docker` returns the wrong number on macOS Docker
 * Desktop (the in-LinuxKit-VM socket is owned by GID 0, not the host
 * docker group); the in-alpine-container `stat` gives the GID the
 * container will actually see.
 */
import { test, expect } from '@playwright/test';
import { execSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as os from 'node:os';

const shouldRun = process.env.DEPLOY_PORTABILITY === '1';

test.describe('DEPLOY-05 portability (deploy-portability)', () => {
  test.skip(!shouldRun, 'set DEPLOY_PORTABILITY=1 to run');

  test('clean-dir compose-up from docker-compose.example.yml: healthz=200 + UI shell renders', async ({
    request,
  }) => {
    const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'hmi-portability-'));
    const composeOut = path.join(tmp, 'docker-compose.yml');
    const stateOut = path.join(tmp, 'hmi_update_state.json');
    const repoRoot = path.join(__dirname, '..', '..');

    try {
      // 1. Build a local "portability" tag from the production Dockerfile.
      //    Plan 07-01's image is the substrate; we tag it locally so the
      //    portability spec is self-contained (does NOT pull from GHCR).
      execSync('docker build -t hmi-update:portability .', {
        cwd: repoRoot,
        stdio: 'inherit',
      });

      // 2. Detect the in-container docker GID via the same alpine-stat
      //    technique the Makefile uses (Pitfall 9 — macOS host GID is
      //    wrong; alpine-stat reads the in-VM socket GID which is what
      //    the container will actually see on /var/run/docker.sock).
      const dockerGid = execSync(
        "docker run --rm -v /var/run/docker.sock:/var/run/docker.sock --entrypoint stat alpine -c '%g' /var/run/docker.sock",
        { encoding: 'utf-8' },
      ).trim();
      expect(dockerGid, 'docker GID must be a non-negative integer').toMatch(/^\d+$/);

      // 3. Read docker-compose.example.yml and substitute:
      //    - the published image ref (ghcr.io/centroid-is/docker-update:latest)
      //      → the locally-built hmi-update:portability tag
      //    - <docker-gid> placeholder → resolved dockerGid
      //    - "8080:8080" port mapping → "8081:8080" so we don't collide
      //      with the main e2e suite's hmi-update on host 8080
      //    - /opt/centroid/docker-compose.yml bind-source → tempdir
      //      (compose file self-references itself as the
      //      read-only compose.Reader source — fine for a portability
      //      smoke, since the spec only asserts the server boots and
      //      serves the UI; it does not exercise any compose-mutation
      //      action that would require a multi-service compose file)
      //    - /opt/centroid/hmi_update_state.json bind-source → tempdir
      let compose = fs.readFileSync(
        path.join(repoRoot, 'docker-compose.example.yml'),
        'utf-8',
      );
      compose = compose
        .replace(
          'ghcr.io/centroid-is/docker-update:latest',
          'hmi-update:portability',
        )
        .replace('<docker-gid>', dockerGid)
        .replace('"8080:8080"', '"8081:8080"')
        .replace(
          '/opt/centroid/docker-compose.yml:/host/docker-compose.yml:ro',
          `${composeOut}:/host/docker-compose.yml:ro`,
        )
        .replace(
          '/opt/centroid/hmi_update_state.json:/state/hmi_update_state.json',
          `${stateOut}:/state/hmi_update_state.json`,
        );
      fs.writeFileSync(composeOut, compose);

      // 4. Create the state file with the right ownership (mock the
      //    operator's chown step; in CI we run as a user that has
      //    docker group access). On a dev box without CAP_CHOWN the
      //    chown is a best-effort no-op — the state file just ends up
      //    with the wrong UID, which on macOS Docker Desktop is fine
      //    because the VM remaps anyway.
      fs.writeFileSync(stateOut, '');
      try {
        fs.chownSync(stateOut, 65532, 65532);
      } catch {
        /* dev box without CAP_CHOWN — best effort */
      }

      // 5. Bring up the stack. --wait blocks until the service is
      //    healthy or 60s elapse; the distroless hmi-update has no
      //    compose-side healthcheck so --wait falls back to the
      //    "started" gate (the container being up is the readiness
      //    signal here; /healthz is polled below for the real ready
      //    check).
      execSync(
        `docker compose -f ${composeOut} up -d --wait --timeout 60`,
        { stdio: 'inherit' },
      );

      // 6. Poll /healthz on the shifted port. 60s budget (30 × 2s).
      let healthOK = false;
      for (let i = 0; i < 30; i++) {
        try {
          const resp = await request.get('http://localhost:8081/healthz');
          if (resp.status() === 200) {
            healthOK = true;
            break;
          }
        } catch {
          /* server not ready yet — keep polling */
        }
        await new Promise((r) => setTimeout(r, 2000));
      }
      expect(healthOK, '/healthz must reach 200 within 60s').toBe(true);

      // 7. Assert the UI shell loads (200 + the loose substring
      //    "hmi-update" appears somewhere in the served HTML). This is
      //    the loosest assertion that the embedded //go:embed dist is
      //    being served by the binary, not an index-fallback for
      //    /api/* or a bare 404. The exact selector / title may change
      //    as Phase 5 UI evolves; the keyword is stable.
      const page = await request.get('http://localhost:8081/');
      expect(page.status(), 'GET / must return 200').toBe(200);
      const html = await page.text();
      expect(
        html.toLowerCase(),
        'served HTML must mention hmi-update (UI shell rendered)',
      ).toContain('hmi-update');
    } finally {
      // Teardown — best-effort, always-run. -v removes anonymous
      // volumes; safe because the state file is bind-mounted from
      // the tempdir, NOT a named volume.
      try {
        execSync(
          `docker compose -f ${composeOut} down -v --timeout 30`,
          { stdio: 'inherit' },
        );
      } catch {
        /* teardown best-effort */
      }
      try {
        fs.rmSync(tmp, { recursive: true, force: true });
      } catch {
        /* teardown best-effort */
      }
    }
  });
});
