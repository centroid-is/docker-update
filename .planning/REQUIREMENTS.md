# Requirements: hmi-update

**Defined:** 2026-05-13
**Core Value:** A Centroid field engineer can confidently pull a fresh image to an HMI **and** roll it back to the previous digest, from one button per container in a browser, with no external services or extra state stores in the loop.

## v1 Requirements

Requirements for initial release. Each maps to a roadmap phase. The TDD constraint (C4) requires that every functional requirement starts as a failing Playwright e2e test and is not "done" until the test is green in CI **and** behavior has been observed manually on an HMI-like stack.

### Foundation — walking skeleton & test harness

The TDD constraint forces a phase where the harness can drive a binary and assert on output *before* the first feature test is written. These requirements deliver that harness.

- [ ] **FOUND-01**: Repository scaffolding exists with `cmd/hmi-update`, `internal/{api,state,docker,registry,poll,compose,actions}`, `ui/`, `e2e/`, `Dockerfile`, `Makefile`, `go.mod`, `.github/workflows/`
- [ ] **FOUND-02**: `internal/state` persists a versioned schema (`version: 1`, `containers: {...}`) to `./hmi_update_state.json` via `google/renameio/v2` (temp+rename+dirsync). Unit-tested across corrupted-file, missing-file, schema-bump scenarios.
- [ ] **FOUND-03**: HTTP server with `GET /healthz` and `GET /api/state` returning valid JSON, served from a single Go process on port 8080
- [ ] **FOUND-04**: Empty Svelte 5 + Vite + Tailwind v4 shell embedded via `//go:embed all:dist`, served at `/`, MIME-aware static handler with strict `/assets/*` no-fallback
- [ ] **FOUND-05**: `e2e/compose.test.yml` brings up `project-zot/zot` fake registry + `hmi-update` + one stub watched container; `docker compose up -d --wait` succeeds in CI
- [ ] **FOUND-06**: Playwright `globalSetup` drives `docker compose up -d --wait`; first smoke test asserts table renders and `/api/state` returns valid JSON
- [ ] **FOUND-07**: Manifest-push fixture (`oras push` or Go helper) flips `:latest` in zot mid-test
- [ ] **FOUND-08**: `tygo` generates `ui/src/lib/types.d.ts` from `internal/api/types.go`; `make types` is a CI fail-on-diff check

### Docker integration & daemon-side correctness

- [ ] **DOCK-01**: `internal/docker` facade over `github.com/moby/moby/client` (not deprecated `docker/docker/client`) — list-by-label, inspect, events subscribe, pull, tag
- [ ] **DOCK-02**: Compose-file reader at `HMI_UPDATE_COMPOSE_PATH` with `stat`-before-act and inode-drift detection (Pitfall 10 prevention)
- [ ] **DOCK-03**: `/healthz` distinguishes socket-EACCES (wrong GID) from socket-missing (no bind mount) with remediation hint (Pitfall 9 prevention)
- [ ] **DOCK-04**: Containers with `hmi-update.watch=true` enumerated and visible in `/api/state` within 60 s of `docker compose up -d` (Acceptance criterion 1)

### Update detection — registry, multi-arch, scheduling

- [ ] **DETECT-01**: `internal/registry` uses `github.com/google/go-containerregistry`'s `crane.Digest()` (not hand-rolled HTTP) to fetch the current `:latest` digest, including correct multi-arch index handling (linux/amd64 platform filter) — prevents WUD 8.2.2 bug class
- [ ] **DETECT-02**: `Docker-Content-Digest` response header is the digest source (never re-hash response body) — Pitfall 1 prevention
- [ ] **DETECT-03**: Bearer-token flow does not send placeholder `Authorization: Basic Og==` header (Pitfall 2 prevention); CI smoke test against real public GHCR confirms anonymous flow
- [ ] **DETECT-04**: e2e fixture serves both an OCI image index *and* a direct single-arch manifest in the same test run; both shapes resolve to the same digest
- [ ] **DETECT-05**: Cron poller using `robfig/cron/v3` ticks on `HMI_UPDATE_CRON` (default `0 * * * *`); poll surfaces `update_available: bool` plus the available digest in state and `/api/state`
- [ ] **DETECT-06**: Docker event subscription detects new containers with the watch label and adds them to state within 5 s (Acceptance criterion 1 secondary path)
- [ ] **DETECT-07**: New manifest pushed to `:latest` in the test registry causes the affected row to flip to `update_available` within `cron + 5 s` (Acceptance criterion 2)
- [ ] **DETECT-08**: Tag-pattern constraint via `hmi-update.tag-pattern=<regex>` label — only tags matching the regex are considered comparable. `^latest-pg17$` on `timescaledb` suppresses false-positive flips to `latest-pg18-oss` (Acceptance criterion 8)
- [ ] **DETECT-09**: Containers with `image: ...@sha256:...` digest-pinned references are excluded from watch list with a note (Q4 decision — opt-out, no drift detection)
- [ ] **DETECT-10**: Cron + event producers feed a single-consumer channel; state mutations serialized through `state.Store.mu`; no lock held across registry/docker I/O (concurrency invariant)

### Update & rollback actions

- [ ] **ACT-01**: `POST /api/containers/:service/update` performs: `docker pull <image>:<tag>` → verify pulled `RepoDigests[0]` matches registry digest → record previous `RepoDigests[0]` as `previous_digest` in state → `docker compose -f $HMI_UPDATE_COMPOSE_PATH up -d --force-recreate <service>` → verify recreated container `State.Running == true` and `RestartCount` unchanged for ≤15 s (Pitfalls 4 and 12 prevention)
- [ ] **ACT-02**: Clicking Update recreates the container on the new digest within 30 s; UI shows new current digest and prior digest as previous; state file matches (Acceptance criterion 3)
- [ ] **ACT-03**: `POST /api/containers/:service/rollback` performs: `docker tag <image>@<previous_digest> <image>:<tag>` (local re-tag) → `docker compose up -d --force-recreate <service>` → verify-after-recreate (same as ACT-01). Single-slot toggle semantic: after rollback, the previously-current digest becomes the new `previous_digest`, so a second Rollback flips back.
- [ ] **ACT-04**: Clicking Rollback immediately after Update returns the container to the previous digest within 15 s; UI flips `update_available` back on because registry `:latest` is unchanged (Acceptance criterion 4)
- [ ] **ACT-05**: `POST /api/containers/:service/force-pull` re-pulls `:latest` even when digests match (F8 — recovers from accidentally-removed local image)
- [ ] **ACT-06**: Update on a container already at `:latest` returns 200 with `no-op: true` in the response body (N3 idempotency)
- [ ] **ACT-07**: Rollback to current digest returns 200 with `no-op: true` (N3 idempotency)
- [ ] **ACT-08**: Per-service `map[string]*sync.Mutex` serializes concurrent updates targeting the same service; double-click or cron-vs-manual race returns 409 on collision (Pitfall 11 prevention)
- [ ] **ACT-09**: Server refuses `POST /api/containers/<own-service>/update` and `POST /api/containers/<own-service>/rollback` with 409 self-protection error — `hmi-update` cannot recreate itself from inside its own container (Pitfall 6 prevention)
- [ ] **ACT-10**: Strict service-name validation at router (allowlist regex `^[a-zA-Z0-9._-]+$`); in-memory map lookup only — no string-interpolated subprocess args (Pitfall 13 prevention)
- [ ] **ACT-11**: Action responses include `current_digest` and `previous_digest` in the body (F2/F3 API contract)
- [ ] **ACT-12**: After `docker compose restart hmi-update`, the same containers, same digests, same rollback targets persist (Acceptance criterion 5)

### Safety — server-enforced opt-outs

- [ ] **SAFE-01**: Containers labeled `hmi-update.allow-update=false` have the Update button hidden in the UI **and** any `POST /api/containers/<svc>/update` returns 409 (Acceptance criterion 7, applied to `timescaledb`)
- [ ] **SAFE-02**: Containers labeled `hmi-update.allow-rollback=false` have the Rollback button hidden **and** server returns 409 on direct API hit
- [ ] **SAFE-03**: Containers labeled `hmi-update.allow-update=false` and labeled `hmi-update.watch=true` are still polled for detection (read-only); only the action surface is disabled

### Web UI

- [ ] **UI-01**: Svelte 5 single page served at `/` with a table: container | image:tag | current digest (short) | available digest (short) | previous digest (short) | status badge | actions (Update, Rollback, Force-pull, Copy)
- [ ] **UI-02**: Status badge per row: `up-to-date` / `update-available` / `rollback-available` / `disabled`
- [ ] **UI-03**: Per-row buttons disabled when no available update / no rollback target / safety label opt-out
- [ ] **UI-04**: Header has `Refresh`, `Watch now` (force immediate poll), and last-poll timestamp
- [ ] **UI-05**: Toast notifications for action success/failure
- [ ] **UI-06**: 5 s background refresh via `fetch` polling against `GET /api/state` while the page is open (no SSE/WebSocket)
- [ ] **UI-07**: Buttons disable on click and re-enable on response (Pitfall 11 UX side)
- [ ] **UI-08**: Pre-action "display may flicker" warning toast when targeting `flutter` or `weston` (Pitfall 5 UX warning)
- [ ] **UI-09**: Copy-digest icon per digest cell
- [ ] **UI-10**: In-place upgrade test: upgrade `hmi-update` image with the tab open; soft-refresh; page still works (`/assets/*` immutable, `index.html` no-cache cache strategy) — Pitfall 8 prevention

### State persistence

- [ ] **STATE-01**: All state lives in `./hmi_update_state.json` (bind-mounted into the container). No SQLite, no Mongo, no Redis (C2 hard constraint)
- [ ] **STATE-02**: Atomic writes via `google/renameio/v2` (temp file in same directory + rename + directory fsync) — Pitfall 7 prevention
- [ ] **STATE-03**: Schema field `version: 1` present; service reads state from JSON on boot and resumes (N2 stateless self-restart)
- [ ] **STATE-04**: Restart-mid-write fault-injection test (SIGKILL during write) leaves the file in a parseable state — either prior or new content, never truncated
- [ ] **STATE-05**: State file UID/GID matches container `nonroot` (UID 65532); install runbook documents `chown 65532:65532` (Pitfall 9 install step)

### UX design checkpoint — display-blackout

- [ ] **UX-01**: Explicit design decision for `flutter`/`weston` recreate display blackout: (a) leave Update as-is + README warning, (b) two-step Update UX with pre-pull then explicit "Switch now", or (c) per-service "danger flag" requiring double-confirm (Pitfall 5 product framing)
- [ ] **UX-02**: README "before you click Update on flutter/weston" callout reflecting the UX-01 decision
- [ ] **UX-03**: If UX-01 picks (b): state schema gains a `prepared_digest` field; UI gains a third per-row button + corresponding action endpoint

### Deployment, packaging, portability

- [ ] **DEPLOY-01**: Multi-stage Dockerfile: Stage 1 `node:22-alpine` builds Svelte bundle; Stage 2 `golang:1.26-alpine` builds Go binary with frontend embedded via `//go:embed`; final stage `gcr.io/distroless/static-debian12:nonroot` (not unversioned `static:nonroot`)
- [ ] **DEPLOY-02**: Final image target <30 MB (N6). If the `docker` + `compose` CLI plugins push past the cap with `static-debian12`, fall back to `cc-debian12:nonroot` (Phase A measurement decides)
- [ ] **DEPLOY-03**: Idle RAM <30 MB (N6)
- [ ] **DEPLOY-04**: Compose deployment block matches the brief §F7 shape: `image: ghcr.io/centroid-is/hmi-update:latest`, `ports: 8080:8080`, three bind-mounts (`/var/run/docker.sock`, `docker-compose.yml:ro`, `hmi_update_state.json`), env (`HMI_UPDATE_CRON`, `HMI_UPDATE_COMPOSE_PATH`), labels (`hmi-update.watch=false`)
- [ ] **DEPLOY-05**: Copying `docker-compose.yml` to a second host and running `docker compose up -d` produces a working install with no manual UI steps (N1 portability — Acceptance criterion 6)
- [ ] **DEPLOY-06**: amd64 image published; arm64 deliberately deferred via CI buildx switch (Q1 decision)
- [ ] **DEPLOY-07**: LAN-only, unauthenticated (N5) — no auth middleware in v1
- [ ] **DEPLOY-08**: Compose `user: "65532:<host-docker-gid>"` pattern documented in install runbook with `id -g docker` instruction (Pitfall 9 fix)
- [ ] **DEPLOY-09**: Manual self-upgrade procedure documented: `docker compose pull hmi-update && docker compose up -d hmi-update` from a host shell (Pitfall 6 — self-update is impossible)

### Observability — logging & endpoints

- [ ] **OBS-01**: Every poll/update/rollback/force-pull logs container, before/after digests, exit code, duration as structured `log/slog` JSON (N7)
- [ ] **OBS-02**: `GET /healthz` returns 200 if state file readable + docker socket reachable; 503 otherwise with remediation hint (N8)
- [ ] **OBS-03**: `GET /api/state` returns the full state JSON (memory-only, no I/O) for the 5 s UI poll (N8)
- [ ] **OBS-04**: Bearer-token redaction audit: no registry tokens, credentials, or `Authorization` headers appear in slog output (Pitfall 13 hardening)

### CI/CD

- [ ] **CI-01**: GitHub Actions pipeline: lint (`go vet`, `golangci-lint`) → unit tests (`go test`) → tygo diff check → frontend build → multi-stage docker build → Playwright e2e → publish image
- [ ] **CI-02**: Image published to `ghcr.io/centroid-is/hmi-update` with three tags: `:latest` tracking `main`, `:vX.Y.Z` per Git tag (semver), `:sha-<short>` per commit
- [ ] **CI-03**: e2e job runs `docker compose -f e2e/compose.test.yml up -d --wait` then `npx playwright test`; failure blocks publish
- [ ] **CI-04**: Real-GHCR smoke job runs a single read-only `crane.Digest()` against a frozen public image to catch anonymous-token-flow regressions (Pitfall 2 belt-and-braces)
- [ ] **CI-05**: All releases gated on green CI **and** a manual smoke note on the elevator-hmi or HMI-like stack (C4 — "done" requires manual smoke)

## v2 Requirements

Deferred to future release. Tracked but not in v1 roadmap.

### Future capabilities

- **V2-AUTH**: Authentication and/or RBAC if `hmi-update` ever leaves the LAN
- **V2-NOTIF**: Slack/email/MQTT notifications for update-available and post-update results
- **V2-PRIV-REG**: Private registry credentials support
- **V2-ARM64**: arm64 image builds when ARM HMI hardware lands
- **V2-N-DEEP**: N-deep rollback history with explicit choose-which-digest UI
- **V2-DRIFT-PINNED**: Optional drift detection on digest-pinned services
- **V2-COMPOSE-REWRITE**: Optional compose-file pinning rewrites on update
- **V2-AUTO-PRUNE**: Auto-prune stale unreferenced images
- **V2-WEBSOCKET**: Server-side push (SSE/WebSocket) replacing the 5 s poll
- **V2-MULTI-HOST**: Multi-host fleet management

## Out of Scope

Explicitly excluded for v1. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Multi-host fleet management | v1 is single-HMI scope; orchestration is intentionally compose-only |
| Authentication / RBAC | LAN-only deployment matches existing WUD posture; future phase if requirements change |
| Auto-update on detection | Operator must press the button; explicit by design |
| Container creation/deletion | Tool only manipulates compose-defined services that already exist |
| Logs viewer / shell exec / stats | Use `docker logs` / `docker exec`; not duplicating Portainer |
| Notifications (Slack/email/MQTT) | Future phase; UI is enough for the field-engineer flow |
| Private registry credentials | All current images are public; deferred |
| arm64 image builds for v1 | Current elevator-hmi hardware is amd64; easy buildx flip later |
| N-deep rollback history | Single-slot is sufficient for the toggle-recover workflow; bigger state and UI surface not justified |
| Drift detection on `@sha256:`-pinned services | Pinned digests are intentional opt-outs; reporting drift creates ambiguous semantics |
| Compose-file rewriting | Avoids WUD #546 regression class; the compose file is the source of truth, never mutated |
| Tailwind UI kit (skeleton.dev etc.) | Matches the no-extra-deps ethos; toasts/disabled states are small hand-rolled components |
| Compose Go SDK for `up -d` | Drags BuildKit/containerd transitive deps; blows 30 MB image budget — use `os/exec` subprocess |
| `docker/docker/client` Go module | Deprecated as of Docker Engine v29; use `github.com/moby/moby/client` |
| Hand-rolled registry HTTP + Bearer-token + multi-arch index code | Where WUD 8.2.2's two named bugs lived; use `google/go-containerregistry` `crane.Digest()` |
| Watchtower / Komodo / WUD as base | WUD needs `sed` patches and has no rollback; Komodo's 3-container MongoDB topology exceeds deployment budget |
| Self-update of `hmi-update` from inside its own container | Structurally impossible — process cannot kill itself mid-recreate; manual host-shell upgrade is the documented path |
| SSE / WebSocket for state push | 5 s `fetch` polling against in-memory `/api/state` is enough on a LAN |

## Traceability

Empty initially; populated by roadmapper. Each requirement maps to exactly one phase.

| Requirement | Phase | Status |
|-------------|-------|--------|
| FOUND-01 | TBD | Pending |
| FOUND-02 | TBD | Pending |
| FOUND-03 | TBD | Pending |
| FOUND-04 | TBD | Pending |
| FOUND-05 | TBD | Pending |
| FOUND-06 | TBD | Pending |
| FOUND-07 | TBD | Pending |
| FOUND-08 | TBD | Pending |
| DOCK-01 | TBD | Pending |
| DOCK-02 | TBD | Pending |
| DOCK-03 | TBD | Pending |
| DOCK-04 | TBD | Pending |
| DETECT-01 | TBD | Pending |
| DETECT-02 | TBD | Pending |
| DETECT-03 | TBD | Pending |
| DETECT-04 | TBD | Pending |
| DETECT-05 | TBD | Pending |
| DETECT-06 | TBD | Pending |
| DETECT-07 | TBD | Pending |
| DETECT-08 | TBD | Pending |
| DETECT-09 | TBD | Pending |
| DETECT-10 | TBD | Pending |
| ACT-01 | TBD | Pending |
| ACT-02 | TBD | Pending |
| ACT-03 | TBD | Pending |
| ACT-04 | TBD | Pending |
| ACT-05 | TBD | Pending |
| ACT-06 | TBD | Pending |
| ACT-07 | TBD | Pending |
| ACT-08 | TBD | Pending |
| ACT-09 | TBD | Pending |
| ACT-10 | TBD | Pending |
| ACT-11 | TBD | Pending |
| ACT-12 | TBD | Pending |
| SAFE-01 | TBD | Pending |
| SAFE-02 | TBD | Pending |
| SAFE-03 | TBD | Pending |
| UI-01 | TBD | Pending |
| UI-02 | TBD | Pending |
| UI-03 | TBD | Pending |
| UI-04 | TBD | Pending |
| UI-05 | TBD | Pending |
| UI-06 | TBD | Pending |
| UI-07 | TBD | Pending |
| UI-08 | TBD | Pending |
| UI-09 | TBD | Pending |
| UI-10 | TBD | Pending |
| STATE-01 | TBD | Pending |
| STATE-02 | TBD | Pending |
| STATE-03 | TBD | Pending |
| STATE-04 | TBD | Pending |
| STATE-05 | TBD | Pending |
| UX-01 | TBD | Pending |
| UX-02 | TBD | Pending |
| UX-03 | TBD | Pending |
| DEPLOY-01 | TBD | Pending |
| DEPLOY-02 | TBD | Pending |
| DEPLOY-03 | TBD | Pending |
| DEPLOY-04 | TBD | Pending |
| DEPLOY-05 | TBD | Pending |
| DEPLOY-06 | TBD | Pending |
| DEPLOY-07 | TBD | Pending |
| DEPLOY-08 | TBD | Pending |
| DEPLOY-09 | TBD | Pending |
| OBS-01 | TBD | Pending |
| OBS-02 | TBD | Pending |
| OBS-03 | TBD | Pending |
| OBS-04 | TBD | Pending |
| CI-01 | TBD | Pending |
| CI-02 | TBD | Pending |
| CI-03 | TBD | Pending |
| CI-04 | TBD | Pending |
| CI-05 | TBD | Pending |

**Coverage:**
- v1 requirements: 67 total
- Mapped to phases: 0 (pending roadmap)
- Unmapped: 67 ⚠️ (expected — roadmapper populates this)

---
*Requirements defined: 2026-05-13*
*Last updated: 2026-05-13 after initial definition*
