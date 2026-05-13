# Phase 02 — Deferred Items

Issues discovered during plan execution that are out of scope for the
current plan and tracked here for future resolution.

## D-02-01: Base e2e stack hmi-update service hits docker-socket EACCES on macOS Docker Desktop (and likely Linux CI default)

**Discovered:** plan 02-05 execution, 2026-05-13.

**Symptom:** `cd e2e && docker compose -f compose.test.yml up -d --wait`
brings up the base stack; `curl http://localhost:8080/healthz` returns
**503** with body
`{"status":"unhealthy","reason":"docker socket permission denied — set
compose user: '65532:$(id -g docker)' (Pitfall 9)"}`.

The container logs show:
```
ERROR discovery.boot.fail err=docker.ContainerList: permission denied
  while trying to connect to the docker API at unix:///var/run/docker.sock
```

**Root cause:** The hmi-update container's Dockerfile sets `USER 65532:65532`
(nonroot in distroless). On Docker Desktop for macOS, the in-VM docker
socket at `/var/run/docker.sock` is owned by `root:root` (UID 0 / GID 0)
with mode 0660 — UID 65532 has no read/write access. The compose v2 healthz
upgrade in plan 02-04 now correctly surfaces this as a 503 with the
Pitfall 9 remediation hint; pre-upgrade healthz returned 200
unconditionally and masked the underlying socket-access issue.

**Impact on plan 02-05:**
- `e2e/tests/discovery.spec.ts` cannot pass on macOS Docker Desktop
  because the discoverer's `ContainerList` fails before stub-watched-
  container can appear in `/api/state`, and `/healthz` does not return
  200 against the base stack.
- `e2e/tests/smoke.spec.ts` (Phase 1 gate) is also regressed on the
  same platform — it asserts `/healthz` == 200.
- `e2e/tests/healthz-negative.spec.ts` eacces case: the override
  `user: "65532:65532"` produces the same EACCES as the base stack on
  Docker Desktop (effectively a no-op on this platform — both states
  are 503 EACCES). The override IS distinguishable on Linux CI where
  the host docker group GID is ~999/998 and the base stack would
  pass /healthz==200 if-and-only-if the base hmi-update service is
  configured with `user: "65532:$(id -g docker)"`.
- `e2e/tests/compose-drift.spec.ts` SKIPS on production builds via the
  /debug/compose-stat 404 probe — unaffected.

**Out of scope for plan 02-05:** fixing the base stack's user mapping
requires editing `e2e/compose.test.yml`'s hmi-update service block to
add `user: "65532:$(id -g docker)"` or the Docker-Desktop-aware variant
`user: "65532:0"`. That edit belongs in the Phase 1 (walking skeleton)
or Phase 7 (deploy hardening) plan family — plan 02-05's scope is the
e2e proof layer, not the underlying base-stack permissions.

**Resolution path:**
- **Short-term (Phase 8 CI setup):** GitHub Actions ubuntu-24.04 runners
  have docker.sock owned by root:docker with GID 998 or 999. Add a
  `user: "65532:${HMI_HOST_DOCKER_GID:-999}"` template to compose.test.yml
  and document setting `HMI_HOST_DOCKER_GID=$(stat -c %g /var/run/docker.sock)`
  as a CI pre-step. On Docker Desktop the operator sets
  `HMI_HOST_DOCKER_GID=0` to grant nonroot access to the root-owned VM
  socket.
- **Long-term (Phase 7 deploy):** the production deploy will document the
  Pitfall 9 mitigation as the canonical onboarding step. Real HMI boxes
  run Debian with a well-known docker group GID; the operator either
  sets `user: ${HMI_USER}` in their override or accepts EACCES and the
  /healthz remediation hint guides them to the fix.

**Verification (planned for Phase 1 or Phase 7):**
1. Add the user/GID env var indirection to `e2e/compose.test.yml`.
2. Re-run `cd e2e && npx playwright test --grep smoke` on macOS Docker
   Desktop with `HMI_HOST_DOCKER_GID=0` — should report PASS.
3. Re-run `cd e2e && npx playwright test --grep discovery` — should also
   PASS within 75 s.
4. Run on Linux (CI) with the default `HMI_HOST_DOCKER_GID` resolution
   (or auto-detection script) — both should PASS without manual env.

**Owner:** Phase 1 or Phase 7 — to be decided at next phase planning.

**Related:** research/PITFALLS.md Pitfall 9; CLAUDE.md "Security" section;
plan 02-04 SUMMARY "Healthz Remediation Hints" — the verbatim hint string
references this exact remediation.
