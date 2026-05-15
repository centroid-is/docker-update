# docker-update

## What This Is

A single Go container that detects when `:latest` Docker images have been re-pushed for the containers running on Centroid's elevator HMI boxes, and gives Centroid field engineers per-container **Update** and **Rollback** buttons via a small Svelte web UI on the HMI LAN. Replaces a fragile patched WUD 8.2.2 setup and a heavier Komodo-based alternative with a tool that has rollback built in, ships as one image, and persists everything in a single JSON file alongside the compose stack.

## Core Value

A Centroid field engineer can confidently pull a fresh image to an HMI **and** roll it back to the previous digest, from one button per container in a browser, with no external services or extra state stores in the loop.

## Requirements

### Validated

<!-- Shipped and confirmed valuable. -->

(None yet — ship to validate)

### Active

<!-- Current scope. Building toward these. Each item is a hypothesis until shipped. -->

- [ ] **F1** Detect when `:latest` has been re-pushed for each labeled container (cron poll + Docker event subscription)
- [ ] **F2** Per-container `Update` action: pull new image, record previous digest, `compose up -d --force-recreate`
- [ ] **F3** Per-container `Rollback` action: local re-tag to previous digest, recreate; single-slot toggle
- [ ] **F4** State persistence to `./docker_update_state.json` with atomic writes (temp + rename)
- [ ] **F5** `hmi-update.tag-pattern=<regex>` label to constrain which upstream tags are comparable
- [ ] **F6** Svelte 5 single-page UI embedded in the binary, served at `/` — table view, per-row actions, toasts, 5s background refresh
- [ ] **F7** Compose deployment as a single service block — image from `ghcr.io/centroid-is/docker-update`
- [ ] **F8** Force-pull endpoint that re-pulls `:latest` even when digests match (recovers from accidentally-removed local images)
- [ ] **N1** Portable: `docker compose up -d` on any HMI works with no extra setup
- [ ] **N2** Stateless self-restart: service resumes from JSON on boot
- [ ] **N3** Idempotent update/rollback (both are 200 no-op when already at the target digest)
- [ ] **N4** `allow-update=false` / `allow-rollback=false` enforced server-side (UI hides button **and** API returns 409)
- [ ] **N5** LAN-only, unauthenticated (matches current WUD model)
- [ ] **N6** Small footprint: <30 MB image, <30 MB RAM at idle
- [ ] **N7** Structured `slog` JSON logging for every poll/update/rollback (container, before/after digests, exit code, duration)
- [ ] **N8** Observable: `GET /healthz` and `GET /api/state` endpoints
- [ ] **CI** GitHub Actions pipeline: build → unit tests → Playwright e2e → publish to GHCR with `:latest`, `:vX.Y.Z`, `:sha-<short>` tags

### Out of Scope

<!-- Explicit boundaries with reasoning to prevent re-adding. -->

- **Multi-host fleet management** — single HMI scope; orchestration is intentionally compose-only
- **Authentication / RBAC** — LAN-only deployment matches the existing WUD security posture; future phase if requirements change
- **Auto-update on detection** — operator must press the button; explicit by design
- **Container creation/deletion** — tool only manipulates compose-defined services that already exist
- **Logs viewer / exec / stats** — use `docker logs` and `docker exec`; not duplicating Portainer
- **Notifications (Slack/email/MQTT)** — future phase; UI is enough for the field-engineer flow
- **Private registry credentials** — all current images are public; deferred
- **arm64 image builds** — current elevator-hmi hardware is amd64; easy to add later via buildx
- **N-deep rollback history** — single-slot is sufficient for the toggle-recover workflow; bigger state and UI surface not justified
- **Drift detection on digest-pinned services** — `image: …@sha256:…` is treated as intentional opt-out, no detection
- **Tailwind UI kit (skeleton.dev etc.)** — Tailwind-only matches the project's no-extra-deps ethos
- **Komodo / WUD as the base** — WUD lacks rollback and needs `sed` patches; Komodo's 3-container MongoDB stack exceeds the deployment budget. See §1 of the brief for the full rationale

## Context

### Why now

The current solution is **WUD 8.2.2** with two upstream bugs patched at runtime via `sed` in a compose entrypoint override (wrong single-arch manifest-digest extraction; broken anonymous-credentials placeholder for layer pulls). The patches are pinned to specific line numbers and break across version bumps. WUD also has no rollback. **Komodo 2.x** has first-class update + rollback via Stack resources but is a three-container deployment with MongoDB state and per-HMI manual setup. Both are more expensive in per-HMI fiddling than a focused tool — and neither matches the "no second container, no database" deployment goal.

### Environment

- Debian HMI boxes running a Docker Compose stack: `flutter`, `centroidx-backend`, `weston`, `seatd`, `timescaledb`, plus cert/init containers.
- Non-DB containers pull `:latest` from GHCR (`ghcr.io/centroid-is/*`, public).
- Database uses `timescale/timescaledb:latest-pg17` from Docker Hub.
- Today's fleet is the `elevator-hmi` box plus more HMIs landing soon.

### Users

Centroid **field engineers** click the buttons in production — internal team deploying/maintaining HMIs at customer sites. The UI is optimized for technical operators, not end-customers. No hand-holding wrappers; clear status badges and disabled states are enough.

### Development culture

- **TDD with Playwright e2e tests first** — every functional requirement starts as a failing Playwright test against a real `docker compose` test stack that includes a fake OCI registry whose `:latest` digest can be flipped during the test.
- **"Work first try, but quickly"** — the user wants implementation to land green on the first attempt against the failing e2e tests, then move to the next requirement fast.
- Manual smoke on an HMI-like stack is part of "done" for each requirement — CI green alone is not sufficient (see brief §7).

## Constraints

- **Tech stack — Backend**: Go 1.23+, `net/http` (stdlib) or `chi` router, `docker/docker/client`, `log/slog`, `robfig/cron/v3` — single binary
- **Tech stack — Frontend**: Svelte 5 + Vite + TypeScript + Tailwind, embedded into the Go binary via `//go:embed`, single page, no SPA router
- **Tech stack — Image**: Multi-stage Dockerfile, final stage `gcr.io/distroless/static:nonroot`, target <30 MB
- **Tech stack — Testing**: Playwright (`@playwright/test`) e2e + Go `testing` table-driven unit tests
- **Tech stack — CI/CD**: GitHub Actions → build → unit → e2e → publish to `ghcr.io/centroid-is/docker-update`
- **Architecture — C1. One container, one binary**: whole tool is a single OCI image with one process. No sidecars/init/helpers. Frontend bundle embedded.
- **Architecture — C2. File-based persistence only**: all state in `./docker_update_state.json` (bind-mounted). Atomic writes. No SQLite/Mongo/Redis.
- **Architecture — C3. Self-contained compose deployment**: a single service block in the existing `docker-compose.yml` is all the on-HMI configuration required.
- **Process — C4. TDD: verify → implement → verify → implement**: every F-requirement starts as a failing Playwright test; implementation drives it green; manual smoke on HMI-like stack is required before "done."
- **Platform**: amd64 only for v1 (matches current HMI hardware). arm64 is a CI buildx flip later.
- **Security**: LAN-only, unauthenticated, matches WUD posture. Database (timescaledb) is `allow-update=false` / `allow-rollback=false` server-enforced.
- **Footprint**: <30 MB image, <30 MB RAM idle.
- **Repo**: Git repo `centroid-is/docker-update`. Image published to `ghcr.io/centroid-is/docker-update` with `:latest` tracking main, `:vX.Y.Z` per release, `:sha-<short>` per commit. The binary name, compose service name, healthz banner, log subject, and env-var prefix are all `docker-update` / `DOCKER_UPDATE_*`. The watched-container label namespace stays on `hmi-update.*` for backwards compatibility — see Container labels reference below and CLAUDE.md "Backwards-compatible label namespace".

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Build a focused tool instead of patching WUD or adopting Komodo | WUD has no rollback and needs fragile `sed` patches; Komodo's 3-container Mongo deployment exceeds the "single container, no DB" budget. The build also delivers rollback that WUD will never have. | — Pending |
| One Go binary with embedded Svelte UI (`//go:embed`) | Matches the single-container constraint; no sidecar, no static-file server, smaller deployment surface. | — Pending |
| Single JSON file for all state (`./docker_update_state.json`) | Eliminates the database. Travels with the compose file. Atomic temp+rename keeps writes safe. | — Pending |
| Single-slot rollback (one previous digest per container) | Sufficient for toggle-recover workflow; smaller state, simpler UI, fewer tests. N-deep can be added later if needed. | — Pending |
| Include force-pull endpoint in v1 | Recovers from accidentally-removed local images. Small surface — one endpoint + button. | — Pending |
| Compose **service name** as the API identifier | Stable across `docker compose up --force-recreate`; container names change. | — Pending |
| `image: …@sha256:…` pins are opt-outs, no drift detection | Pinned digests are intentionally frozen; reporting drift would create ambiguous semantics. | — Pending |
| amd64 only for v1 | Matches current elevator-hmi hardware; arm64 is a buildx flip when an ARM HMI lands. | — Pending |
| Tailwind-only, no UI kit | Matches the no-extra-deps ethos; toasts/disabled states are small hand-rolled components. | — Pending |
| TDD: Playwright e2e tests written **before** implementation, per F-requirement | The user wants behaviour proven against the real docker stack before any production code lands. Manual smoke is part of "done." | — Pending |
| **UX-01 — display-blackout UX for flutter/weston: chose option (a) (README warning + Phase-5 pre-action toast)** | Phase 5 already ships a pre-action "display may flicker" confirmation toast (UI-08) for service names matching `flutter` / `weston`; Rollback is the safety net. Options (b) two-step prepare/switch and (c) per-service danger flag both double the surface area (new schema field + endpoint + third button; or per-service label discipline). Option (a) preserves the brief's "one button per container" Core Value with zero Phase-6 code changes. Full rationale: `.planning/phases/06-display-blackout-ux-checkpoint/06-CONTEXT.md` and README.md "Before you click Update on flutter or weston". | Locked — Phase 6 ships documentation only; UX-03 (option (b) deliverables) explicitly not shipped |
| **Image path is `ghcr.io/centroid-is/docker-update` (GitHub repo URL); binary/service name remains `hmi-update` (operator-facing branding)** | The GitHub repository was named `centroid-is/docker-update` (the project's "what it is" framing — a Docker image updater for HMI compose stacks). To avoid an ambiguous published image identity, the GHCR image path follows the repo URL slug. The Go module path, compose service name, binary name, log subject, and healthz banner all retain `hmi-update` — that name is the operator-facing identity on the HMI and is unaffected. Historical phase docs (PLAN/RESEARCH/SUMMARY for phases 01–07) retain references to the original `ghcr.io/centroid-is/hmi-update` path as a historical record; the operative artifacts (Dockerfile, docker-compose.example.yml, ci.yml, publish.yml, README, CLAUDE.md, PROJECT.md, REQUIREMENTS.md, API.md) all use the new `docker-update` path. | Superseded by quick-260515-n1v — see row below |
| **Unified name rename (hmi-update → docker-update) with label-namespace preservation** | The previous split (image=docker-update, binary/service=hmi-update) produced operator confusion (see HANDOFF.md prompt history). Unifying on docker-update removes the dual-name footgun. Watched-container labels stay `hmi-update.*` because operators have them on dozens of HMI compose blocks; renaming them would require a coordinated fleet-wide compose edit. | Locked — quick-260515-n1v ships the rename; labels stay |

## Installation prerequisites

After `docker compose up -d`, the state file may need a one-time chown:

    chown 65532:65532 /opt/centroid/docker_update_state.json

This grants the distroless `nonroot` UID inside the container write access.
(See Pitfall 9 — same UID/GID pattern as the docker.sock GID interpolation.)

## Manual self-upgrade procedure

`docker-update` refuses to recreate itself via the API (ACT-09). To upgrade:

1. On the HMI host: `docker pull ghcr.io/centroid-is/docker-update:vX.Y.Z`
2. `docker compose -f /opt/centroid/docker-compose.yml up -d --force-recreate docker-update`
3. Wait ~10s; verify `curl http://localhost:8080/healthz` returns 200.

The HMI's web UI will be unreachable for ~5–15 s during step 2.
The state file (`docker_update_state.json`) persists across the recreate.

## Container labels reference

> **Backwards-compat note:** the label keys below intentionally keep the
> `hmi-update.*` namespace for backwards compatibility across the HMI fleet —
> see CLAUDE.md "Backwards-compatible label namespace" for the rationale.

| Label | Purpose | Default behavior if absent |
|-------|---------|----------------------------|
| `hmi-update.watch=true` | Mark a container as watched | Not watched |
| `hmi-update.tag-pattern=<regex>` | Constrain upstream tag candidacy | Any tag matches (`.*`) |
| `hmi-update.allow-update=false` | Server refuses Update for this container (SAFE-01) | Update allowed |
| `hmi-update.allow-rollback=false` | Server refuses Rollback for this container (SAFE-02) | Rollback allowed |
| `hmi-update.wait-for-healthy=true` | Extend verify-after-recreate to wait for `State.Health.Status == "healthy"` (60s window) | 15s consecutive-Running window |

## Configuration knobs (env vars)

| Variable | Default | Purpose |
|----------|---------|---------|
| `DOCKER_UPDATE_STATE_PATH` | `./docker_update_state.json` | State file path |
| `DOCKER_UPDATE_COMPOSE_PATH` | (required) | Path to bind-mounted docker-compose.yml |
| `DOCKER_UPDATE_CRON` | `0 * * * *` | Cron schedule for digest polling |
| `DOCKER_UPDATE_LOG_LEVEL` | `info` | slog level |
| `DOCKER_UPDATE_REGISTRY_TIMEOUT_S` | `10` | Per-registry-call timeout |
| `DOCKER_UPDATE_POLL_CONCURRENCY` | `4` | Max concurrent crane.Digest calls per tick |
| `DOCKER_UPDATE_REGISTRY_INSECURE` | (unset) | E2E-only: enable plain HTTP for registry |
| `DOCKER_UPDATE_DOCKER_HOST` | `/var/run/docker.sock` | Docker socket path |
| `DOCKER_UPDATE_SELF_SERVICE` | `docker-update` | (Phase 4) Compose service name this process is running as; refuses self-update |
| `DOCKER_UPDATE_VERIFY_WINDOW_S` | `15` | (Phase 4) Verify-after-recreate poll duration |
| `DOCKER_UPDATE_HEALTHCHECK_WINDOW_S` | `60` | (Phase 4) Extended window when `hmi-update.wait-for-healthy=true` |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd-transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd-complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-05-15 by quick-260515-n1v rename*
