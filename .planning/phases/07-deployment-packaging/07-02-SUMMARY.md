---
phase: 07-deployment-packaging
plan: 02
subsystem: infra
tags: [docker-compose, deployment, distroless, bind-mount, hmi]

# Dependency graph
requires:
  - phase: 07-deployment-packaging
    provides: "Phase 7 CONTEXT.md §2.3 host-docker-bind decision + §2.4 locked compose-example shape"
  - phase: 04-actions
    provides: "compose.Runner exec.CommandContext(`docker compose ...`) — establishes why the host docker CLI must reach the container"
  - phase: 01-foundation
    provides: "FOUND-04 multi-stage Dockerfile + `gcr.io/distroless/static-debian12:nonroot` final stage (UID 65532)"
provides:
  - "docker-compose.example.yml at repo root — operators copy + edit + `docker compose up -d`"
  - "Brief §F7 reference deployment block (image, port, three core bind-mounts, env, label) preserved verbatim"
  - "Phase 7 additions: container_name, restart: unless-stopped, user with <docker-gid> placeholder, two CLI-delivery bind-mounts, HMI_UPDATE_STATE_PATH env, inline operator-facing comments"
  - "Syntactic validity gate — `docker compose -f docker-compose.example.yml config` exits 0"
affects:
  - "07-03 (README install runbook references the <docker-gid> placeholder substitution; portability e2e spec consumes this file)"
  - "phase-08 (CI publish flow — `image:` line is the canonical Phase 8 publish target `ghcr.io/centroid-is/hmi-update:latest`)"
  - "operators on HMI hosts (copy-paste artifact; every line is documented)"

# Tech tracking
tech-stack:
  added: []  # no new dependencies — this is a pure configuration artifact
  patterns:
    - "Literal `<docker-gid>` placeholder (NOT `${DOCKER_GID}` env-substitution) — forces a visible operator edit and surfaces a clear failure when forgotten"
    - "Three core bind-mounts (socket / compose / state) preserved from brief §F7 verbatim; two CLI-delivery bind-mounts (host docker + cli-plugins) added under their own comment block so the §F7 invariant remains visible"
    - "user: '65532:<docker-gid>' (DEPLOY-08) — distroless nonroot UID + host docker GID; matches the EACCES Pitfall 9 remediation hint"
    - "No healthcheck on hmi-update (distroless lacks wget/curl/shell — documented in-file with pointer to deferred item D-07-04)"
    - "No depends_on — example shows ONLY the hmi-update service block, intended for merge into the operator's existing compose stack"

key-files:
  created:
    - "/Users/jonb/Projects/tmp/docker-compose.example.yml"
  modified: []

key-decisions:
  - "File location at REPO ROOT (not `.planning/`, not `e2e/`) — operators clone the repo and grep for `docker-compose` to find it; locating it under `.planning/` would hide it behind a phase artifact"
  - "Default bind-mount sources use `/opt/centroid/*` as illustrative paths; comment block tells operators to substitute when local layout differs"
  - "Two CLI-delivery bind-mounts (`/usr/bin/docker:ro` + `/usr/libexec/docker/cli-plugins:ro`) are present from day one rather than being a documented post-install step — the failure mode if absent (`exec: \"docker\": executable file not found`) is bad operator UX; including them up-front is zero cost on disk"

patterns-established:
  - "Comment-as-documentation density: every operator-editable value carries an inline `# why / how to edit` comment. The file IS the runbook for that line."
  - "Cross-reference pointers: file header names README §Installation on an HMI + PROJECT.md §Manual self-upgrade procedure + PROJECT.md Pitfall 9 so a confused operator has three concrete jump points."

requirements-completed:
  - DEPLOY-04
  - DEPLOY-07
  - DEPLOY-08

# Metrics
duration: ~1m
completed: 2026-05-15
---

# Phase 07 Plan 02: docker-compose.example.yml Summary

**Production compose deployment block landed at repo root — brief §F7 verbatim with Phase 7 additions (container_name, restart, distroless-nonroot user with literal `<docker-gid>` placeholder, two CLI-delivery bind-mounts) and an inline operator runbook in comments.**

## Performance

- **Duration:** ~1m (81s wall-clock)
- **Started:** 2026-05-15T11:28:13Z
- **Completed:** 2026-05-15T11:29:34Z
- **Tasks:** 1 / 1
- **Files created:** 1
- **Files modified:** 0

## Accomplishments

- `docker-compose.example.yml` exists at repo root (90 lines, ~3 KB on disk)
- Brief §F7 invariants preserved verbatim: `ghcr.io/centroid-is/hmi-update:latest`, port `8080:8080`, the three core bind-mounts (`docker.sock`, `/host/docker-compose.yml:ro`, `/state/hmi_update_state.json`), env vars (`HMI_UPDATE_CRON`, `HMI_UPDATE_COMPOSE_PATH`), label `hmi-update.watch: "false"`
- Phase 7 additions applied per CONTEXT.md §2.4: `container_name: hmi-update`, `restart: unless-stopped`, `user: "65532:<docker-gid>"` (literal placeholder, NOT `${VAR}` substitution — DEPLOY-08), `HMI_UPDATE_STATE_PATH` env for operator clarity, and two CLI-delivery bind-mounts (`/usr/bin/docker:ro` + `/usr/libexec/docker/cli-plugins:ro`) for Phase 4's `compose.Runner` exec path
- File header comment block points operators to README §Installation on an HMI (the runbook), PROJECT.md §Manual self-upgrade procedure (the upgrade dance), and PROJECT.md Pitfall 9 (the EACCES remediation hint)
- `docker compose -f docker-compose.example.yml config` exits 0 — confirms valid YAML, all five bind-mounts resolve, and the literal `<docker-gid>` placeholder is treated as a string (compose does NOT attempt `${}` expansion). Resolved output preserves all keys verbatim.

## Bind-mount inventory (5 total)

### Three core bind-mounts (brief §F7)

| Source (host) | Target (container) | Mode | Why |
|---------------|--------------------|------|-----|
| `/var/run/docker.sock` | `/var/run/docker.sock` | rw | Daemon-side facade for `docker.Client.{ContainerList, ImagePull, Events}` |
| `/opt/centroid/docker-compose.yml` | `/host/docker-compose.yml` | ro | `compose.Reader` source-of-truth; `:ro` enforces "never mutated" invariant from PROJECT.md |
| `/opt/centroid/hmi_update_state.json` | `/state/hmi_update_state.json` | rw | Atomic-write state durability across `docker compose up -d --force-recreate`; MUST be `chown 65532:65532` pre-start (Pitfall 9 remediation) |

### Two CLI-delivery bind-mounts (CONTEXT.md §2.3)

| Source (host) | Target (container) | Mode | Why |
|---------------|--------------------|------|-----|
| `/usr/bin/docker` | `/usr/bin/docker` | ro | Phase 4 `compose.Runner` does `exec.CommandContext(ctx, "docker", "compose", ...)`; distroless image lacks the binary, so we bind the host's. Debian 12's docker CLI is statically linked (RESEARCH §3) so it runs cleanly under distroless-static. |
| `/usr/libexec/docker/cli-plugins` | `/usr/libexec/docker/cli-plugins` | ro | Compose v2 is a CLI plugin, NOT a sub-command of `docker` — the plugin binary lives at `/usr/libexec/docker/cli-plugins/docker-compose` and is statically linked alongside the main CLI. |

## Task Commits

1. **Task 1: Create docker-compose.example.yml at repo root** — `df50458` (feat)

**Plan metadata:** _(this SUMMARY commit, made after self-check)_

## Files Created/Modified

- `docker-compose.example.yml` (NEW, at repo root) — production compose deployment block per brief §F7 + Phase 7 hardening additions

## Decisions Made

- **File location at repo root** (not `.planning/`, not `e2e/`) — operators are the audience; repo root is where `git clone && grep -r docker-compose` finds it.
- **Literal `<docker-gid>` placeholder** rather than `${DOCKER_GID}` env-substitution — `docker compose config` treats the angle-bracket string as a literal (no substitution attempted), and operators get an explicit edit step rather than a silent fall-through to a missing-env-var default. README install runbook (Plan 07-03) will document the `id -g docker` → substitute flow.
- **Default bind-mount sources use `/opt/centroid/*`** — illustrative; comment block tells operators to substitute for their local layout. Conventional FHS paths, no customer-specific information leaked to public repo (T-07-02-05 disposition `accept`).
- **No `healthcheck:` key** — distroless image lacks wget/curl/shell; in-file comment explicitly justifies and points to deferred item D-07-04 (`hmi-update --healthcheck` flag).
- **No `depends_on:` key** — example is the hmi-update service block ONLY, intended for merge into the operator's existing compose stack which has its own service dependencies.

## Deviations from Plan

None — plan executed exactly as written. The verbatim YAML body from `<task>.<action>` was preserved character-for-character; all 17 grep gates and the `docker compose config` syntactic-validation gate pass on first run.

## Issues Encountered

None.

**Minor note on orchestrator success-criteria string drift (not a deviation):** The parent orchestrator's success-criteria checklist phrased the watch-label assertion as `grep -F 'hmi-update.watch=false'` (key=value form). YAML serialises this label as `hmi-update.watch: "false"` (colon-space form), which is what the plan body's acceptance criteria and `must_haves.truths` explicitly mandate, and what `docker compose -f docker-compose.example.yml config` resolves the label to. The label IS present and correct; the orchestrator's grep string is a check-only artifact. The plan acceptance gate (`grep -F 'hmi-update.watch: "false"'`) passes with 1 match.

## User Setup Required

None — this file IS the operator-facing setup artifact. The README install runbook (Plan 07-03) will operationalise the edit-and-deploy procedure.

## Next Phase Readiness

- **Plan 07-03 (Wave 2 sibling)** can now reference `docker-compose.example.yml` from:
  - the README §Installation on an HMI step-by-step (`sudo cp docker-compose.example.yml /opt/centroid/docker-compose.yml`)
  - the portability e2e spec `e2e/tests/deploy-portability.spec.ts` (reads + substitutes the `<docker-gid>` placeholder + retargets the image to a locally-built `hmi-update:portability` tag)
- **Phase 8 publish flow** — the `image: ghcr.io/centroid-is/hmi-update:latest` line is the canonical publish target. `docker/metadata-action@v5` in Phase 8 CI will publish to this exact path, no rewrites needed.
- **Operators** can copy this file to an HMI host today, perform the two documented edits (substitute `<docker-gid>`, adjust bind-mount source paths), and `docker compose up -d hmi-update` against a Phase 7-built image.

## Threat Surface Scan

No new threat surface introduced beyond what the plan's `<threat_model>` already covers (T-07-02-01 through T-07-02-07 + DEPLOY-07). All seven Phase 7-02 STRIDE entries have `mitigate` or `accept` dispositions handled in-file:

- T-07-02-01 (image-ref mismatch) → mitigated: `ghcr.io/centroid-is/hmi-update:latest` is the verbatim string Phase 8 will publish to
- T-07-02-02 (operator forgets `<docker-gid>` substitution) → mitigated: literal placeholder, compose `up` will fail with a clear "invalid user" message; runbook step 2 documents the substitute
- T-07-02-03 (state-file chown missing) → mitigated: header comment step 4 explicit, cross-refs PROJECT.md and Pitfall 9
- T-07-02-04 (CLI bind-mounts stripped) → mitigated: present from start; comment block explains the dependency
- T-07-02-05 (path leak to public repo) → accept: `/opt/centroid/*` are conventional FHS defaults
- T-07-02-06 (self-watch label honoured) → mitigated: label present; ACT-09 server-side backstop noted in comment
- T-07-02-07 (restart: always swallowing operator stops) → mitigated: `restart: unless-stopped` chosen and applied

No new STRIDE flags emerged during execution.

## Self-Check: PASSED

- `test -f /Users/jonb/Projects/tmp/docker-compose.example.yml` — FOUND
- `docker compose -f docker-compose.example.yml config` — exit 0 (validated YAML, all bind-mounts resolve)
- Commit `df50458` — FOUND in `git log --oneline`
- All 17 grep acceptance gates from the plan — PASS (≥1 match each)
- Absence gates — `healthcheck:` only appears in a `# No healthcheck:` comment (not a real key); `depends_on:` absent entirely
- Concurrent-scope files untouched — Dockerfile, Makefile, main.go, STATE.md, ROADMAP.md, README.md all show no modifications in `git status` since Task 1 commit

---
*Phase: 07-deployment-packaging*
*Completed: 2026-05-15*
