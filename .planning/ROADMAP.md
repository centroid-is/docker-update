# Roadmap: hmi-update

## Overview

`hmi-update` is built TDD-first against a real `docker compose` test stack with a fake OCI registry (`project-zot/zot`) whose `:latest` digest can be flipped during a test. Because Playwright e2e tests must red-then-green for every functional requirement, the roadmap is goal-backward from "Centroid field engineer presses Update or Rollback in a browser and trusts the result." It begins with a walking-skeleton phase (FOUND) that exists solely so the *very first* feature test can fail meaningfully — without that harness, F1's red test is fake. From there the phases climb the stack: daemon-side correctness (DOCK), registry/detection (DETECT — the WUD 8.2.2 bug surface), mutating actions + safety + state persistence under fault (ACT/SAFE/STATE), the real Svelte UI (UI), an explicit product-decision checkpoint for the `flutter`/`weston` display-blackout UX (UX), deployment packaging on `distroless/static-debian12:nonroot` with the host-docker-GID install dance (DEPLOY), and finally CI/CD hardening including a real-GHCR anonymous-flow smoke job (CI). Each functional phase declares its Playwright e2e test green in CI and a manual smoke on an HMI-like stack as baseline success criteria, per the brief's C4 constraint.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [ ] **Phase 1: Walking Skeleton & Test Harness** - Stand up repo, atomic state store, empty Svelte shell, zot fake registry, Playwright globalSetup, tygo — first Playwright smoke test green before any feature test
- [ ] **Phase 2: Docker Client & Compose-File Reader** - `moby/moby/client` facade, stat-before-act compose reader, GID-aware healthz; watched containers visible within 60 s of `compose up -d`
- [ ] **Phase 3: Registry, Polling & Update Detection** - `crane.Digest()` over `Docker-Content-Digest` with full Accept matrix, multi-arch and single-arch fixtures, cron + Docker-events single-consumer poller, tag-pattern regex, digest-pin opt-out, bearer-token redaction
- [ ] **Phase 4: Update / Rollback / Force-pull Actions, Safety & State Persistence** - Verify-after-recreate poll, per-service mutex, self-protection 409, allow-update/allow-rollback server-enforced, SIGKILL-mid-write fault test, structured slog for every action
- [ ] **Phase 5: Web UI Completeness** - Svelte 5 table with status badges, per-row Update/Rollback/Force-pull/Copy, toasts, 5 s polling, in-place upgrade soft-refresh test, "display may flicker" pre-action warning
- [ ] **Phase 6: Display-Blackout UX Checkpoint (flutter/weston)** - Explicit product decision between (a) toast-only, (b) two-step prepare/switch UX, (c) double-confirm danger flag; README callout reflects choice; if (b), `prepared_digest` field and third button ship
- [ ] **Phase 7: Deployment & Packaging** - Multi-stage Dockerfile on `distroless/static-debian12:nonroot`, <30 MB image / <30 MB RAM verification, compose deployment block, host-docker-GID install runbook, documented self-upgrade procedure
- [ ] **Phase 8: CI/CD & Release Hardening** - GitHub Actions lint → unit → tygo diff → frontend → image build → Playwright e2e → publish; semver + sha + latest tags; real-GHCR anonymous-flow smoke job; manual-smoke gate on releases
- [ ] **Phase 9: Architectural Hardening (post-v0.1 bug-cluster)** - Four items grouped because they all stem from the compose-CLI shell-out surfaced during the 2026-05-15/16 production bring-up session:
  - **(a) Socket-only recreate** — replace `exec docker compose ... up -d --force-recreate` with in-process `ContainerInspect` → `ContainerRemove` → `ContainerCreate` → `ContainerStart` via the existing moby/moby/client. Drops the `/usr/bin/docker` + cli-plugins bind-mounts; lets the base image revert `distroless/base-debian12:nonroot` → `distroless/static-debian12:nonroot` (~20 MB image shrink); closes BUG-7b's compose-recreate race semantically rather than working around it; eliminates `compose_file_moved` 412 guard and `COMPOSE_PROJECT_NAME` collision class. Operator-edit-then-`docker compose up -d` flow is unchanged (confirmed acceptable in 2026-05-16 conversation). ~150–250 LOC plus a small bag of `ContainerInspect` → `ContainerCreate` field-translation tests.
  - **(b) Compose-path bug fix** — eliminate the `./relative/path` resolution split between docker-update's in-container compose path and the operator's host path. Subsumed by (a) since socket-only doesn't invoke compose. If (a) is deferred, the interim fix is to read `com.docker.compose.project.working_dir` off a watched container's `ContainerInspect.Config.Labels` and pass `--project-directory <value>` to the compose invocation. ~5 LOC interim, 0 LOC under (a).
  - **(c) CI 2-job split** — `ci.yml` runs 18 steps serially on one runner (~7–8 min). Split into `tests` (go vet + tygo + go test -race, ~3 min) and `image+downstream` (ui build → docker build → e2e → idle-RAM → portability, ~5–6 min); the two jobs run concurrently; total wall time ~5–6 min. Test job needs `mkdir -p internal/api/dist` stub so `//go:embed all:dist` parses without the UI artifact. ~30 lines of YAML. Independent of (a)/(b)/(d) — can land first.
  - **(d) Self-update via sidecar helper** — replace `409 self_protection` with a Watchtower-style sidecar: orchestrator spawns a one-shot helper container that waits, then drives the daemon API to recreate docker-update itself; helper exits; new docker-update boots and self-verifies via restart-count + healthz polling. Naturally pairs with (a) since the helper would use the same daemon API path. ~200–300 LOC plus a minimal helper image (alpine + the moby client, or a tiny Go binary statically linked).

  **Locked dependencies:** (a) implies (b); (d) builds on (a)'s ContainerCreate infrastructure. (c) is independent and can ship first. Estimated total: 1 milestone cycle.

## Phase Details

### Phase 1: Walking Skeleton & Test Harness
**Goal**: Produce the minimum end-to-end test harness that lets a Playwright test drive a real binary inside a real docker compose stack and assert on `/api/state` — so that every later phase's red test is meaningful
**Depends on**: Nothing (first phase)
**Requirements**: FOUND-01, FOUND-02, FOUND-03, FOUND-04, FOUND-05, FOUND-06, FOUND-07, FOUND-08, STATE-01, STATE-02, STATE-03
**Success Criteria** (what must be TRUE):
  1. Playwright e2e tests for the smoke scenarios (table renders, `/api/state` returns valid JSON) are written *before* implementation and pass green in CI against `docker compose -f e2e/compose.test.yml up -d --wait`
  2. A field engineer running `make e2e` on a developer machine sees the zot fake registry, the `hmi-update` binary, and a stub watched container come up healthy via `--wait`, and Playwright reports "smoke green"
  3. `make types` regenerates `ui/src/lib/types.d.ts` from `internal/api/types.go` and CI fails on diff — there is no path to hand-drift TS types
  4. A `kill -9` of the `hmi-update` process during a state-file write leaves `./hmi_update_state.json` either parseable-old or parseable-new, never truncated (Pitfall 7 unit test green)
  5. Manual smoke on an HMI-like stack confirms `docker compose up -d --wait` produces a working binary serving `/healthz` 200 and a valid empty table at `/`
**Plans**: 4 plans
- [x] 01-01-PLAN.md — Repo skeleton + RED Wave-0 tests (FOUND-01) [Wave 1]
- [x] 01-02-PLAN.md — Atomic JSON state store (renameio + dir-fsync wrapper) (FOUND-02, STATE-01, STATE-02, STATE-03) [Wave 2]
- [x] 01-03-PLAN.md — UI shell + tygo type pipeline (Svelte 5 + Vite 7 + Tailwind v4) (FOUND-04, FOUND-08) [Wave 2 - parallel with 01-02]
- [x] 01-04-PLAN.md — HTTP server + test stack + Dockerfile + CI; drives smoke test GREEN; manual smoke checkpoint (FOUND-03, FOUND-05, FOUND-06, FOUND-07) [Wave 3]
**UI hint**: yes

### Phase 2: Docker Client & Compose-File Reader
**Goal**: Establish a hardened daemon-side adapter — `moby/moby/client` facade, compose-file reader with inode-drift detection, and a healthz that distinguishes EACCES from socket-missing — so every subsequent phase can assume "docker socket reachable, compose path stable, watched containers enumerated"
**Depends on**: Phase 1
**Requirements**: DOCK-01, DOCK-02, DOCK-03, DOCK-04, OBS-02
**Success Criteria** (what must be TRUE):
  1. Playwright e2e test (written first, then implementation) confirms a container labeled `hmi-update.watch=true` appears in `/api/state` within 60 s of `docker compose up -d` (Acceptance criterion 1)
  2. `GET /healthz` returns distinct remediation hints for socket-EACCES (wrong GID), socket-missing (no bind mount), and state-file-unreadable — verified by negative-path Playwright tests that override the compose stack's user/socket bind
  3. Compose file edited mid-run with an atomic-save editor (rename) is detected by `stat`-before-act; the next action emits a clear "compose file moved" error rather than silently acting on a stale inode
  4. Manual smoke on an HMI-like stack: bring up the test stack, label the stub container, observe it appear in the empty-shell UI within a minute
**Plans**: 5 plans
- [x] 02-01-PLAN.md — internal/docker facade (moby/moby/client v0.4.1 adapter) + state.Container field expansion (DOCK-01) [Wave 1]
- [x] 02-02-PLAN.md — internal/compose.Reader stat-based drift detector (DOCK-02) [Wave 1 - parallel with 02-01]
- [x] 02-03-PLAN.md — internal/docker.Discoverer boot list + events goroutine + reconnect backoff (DOCK-04) [Wave 2]
- [x] 02-04-PLAN.md — Healthz upgrade + Server signature + main.go wiring + build-tag-gated debug endpoint (DOCK-03, OBS-02) [Wave 3]
- [x] 02-05-PLAN.md — Compose overrides + Playwright e2e specs (discovery, healthz-negative, compose-drift) + Dockerfile/Makefile debug-image (DOCK-02, DOCK-03, DOCK-04, OBS-02 e2e proof) [Wave 4]

### Phase 3: Registry, Polling & Update Detection
**Goal**: Implement digest detection that is correct for both multi-arch indices and direct single-arch manifests, anonymous-token-flow safe against GHCR/Docker Hub, and serialized through a single-consumer poll channel — the WUD 8.2.2 bug class is designed out from the first red test
**Depends on**: Phase 2
**Requirements**: DETECT-01, DETECT-02, DETECT-03, DETECT-04, DETECT-05, DETECT-06, DETECT-07, DETECT-08, DETECT-09, DETECT-10, OBS-04
**Success Criteria** (what must be TRUE):
  1. Playwright e2e test (red-first) pushes a new manifest to zot in *both* OCI image index and direct single-arch manifest shapes within the same test run; the affected container flips to `update_available` within `cron + 5 s` (Acceptance criterion 2)
  2. `timescaledb` labeled `hmi-update.tag-pattern=^latest-pg17$` does NOT flip to `update_available` when a new `:latest-pg18-oss` is pushed (Acceptance criterion 8) — proven by Playwright
  3. A container with `image: ...@sha256:...` digest-pinned reference is excluded from the watched list with a "pinned: opt-out" note in `/api/state`
  4. `grep "Bearer "` and `grep "Authorization"` against captured `slog` output across a full test run return zero matches — bearer tokens, credentials, and Authorization headers are never logged
  5. Manual smoke on an HMI-like stack with a real `ghcr.io/centroid-is/*` image confirms the anonymous token flow does not send `Authorization: Basic Og==` (Pitfall 2 prevention; one local `crane.Digest()` call succeeds 200)
**Plans**: 5 plans
- [ ] 03-01-PLAN.md — Schema additions (Container.AvailableDigest/LastPolledAt/Notes + State.LastPollStart/End/Error) + tygo regen [Wave 1]
- [ ] 03-02-PLAN.md — internal/registry: craneResolver + redactingTransport + ErrPermanent/Transient (DETECT-01..03, OBS-04 request-side) [Wave 2 — parallel with 03-03]
- [ ] 03-03-PLAN.md — internal/poll: Patterns + StateUpdate channel/RunUpdater + cronPoller (DETECT-05, 08, 09, 10) [Wave 2 — parallel with 03-02]
- [ ] 03-04-PLAN.md — Wire registry+poll into discovery.go (channel-send producer) + cmd/hmi-update/main.go boot (DETECT-06, 10) [Wave 3]
- [ ] 03-05-PLAN.md — RED-first e2e specs + slog ReplaceAttr + drive green + manual smoke (DETECT-04, 06, 07, 08, 09, OBS-04 output-side) [Wave 4]

### Phase 4: Update / Rollback / Force-pull Actions, Safety & State Persistence
**Goal**: Deliver the headline differentiator — operator-driven per-container Update, Rollback, and Force-pull — with verify-after-recreate, per-service mutex, self-protection, server-enforced safety labels, and SIGKILL-resistant state — so a field engineer can trust the buttons
**Depends on**: Phase 3
**Requirements**: ACT-01, ACT-02, ACT-03, ACT-04, ACT-05, ACT-06, ACT-07, ACT-08, ACT-09, ACT-10, ACT-11, ACT-12, SAFE-01, SAFE-02, SAFE-03, STATE-04, STATE-05, OBS-01, OBS-03
**Success Criteria** (what must be TRUE):
  1. Playwright e2e test (red-first): clicking Update recreates the container on the new digest within 30 s; the running container's `RepoDigests[0]` matches the registry digest; `previous_digest` is recorded; `State.Running == true` with `RestartCount` unchanged for ≥15 s (Acceptance criterion 3 + Pitfalls 4 and 12)
  2. Playwright e2e test: clicking Rollback immediately after Update returns the container to the previous digest within 15 s with the network disconnected from the registry (rollback works entirely from local images); UI flips `update_available` back on (Acceptance criterion 4)
  3. `docker compose restart hmi-update` mid-flight: after restart the same containers, same digests, same rollback targets are present in `/api/state` (Acceptance criterion 5); SIGKILL during state write leaves a parseable file
  4. Direct `curl` to `POST /api/containers/timescaledb/update` returns 409 even when the UI button is hidden by `hmi-update.allow-update=false` (Acceptance criterion 7); direct hit on `POST /api/containers/hmi-update/update` returns 409 self-protection (Pitfall 6); concurrent double-click on the same service returns 200 + 409, not interleaved state
  5. Every poll/update/rollback/force-pull emits a structured `slog` JSON line with `container`, before/after digests, exit code, duration; `GET /api/state` (no I/O) returns the full state for the 5 s UI poll
  6. Manual smoke on an HMI-like stack confirms Update → Rollback → Update toggles between two digests, persists across `docker compose restart hmi-update`, and refuses to update `timescaledb`
**Plans**: 6 executed + 1 registered-deferred (7 total)
- [x] 04-01-PLAN.md — Schema additions: state.Container ActionInFlight/ActionError + poll.UpdateKind extensions + tygo regen (ACT-11) [Wave 1]
- [x] 04-02-PLAN.md — internal/compose.Runner body: exec.CommandContext + argv discipline + stderr capture + ctx-aware SIGTERM (ACT-01, ACT-03, ACT-05, ACT-10, OBS-01) [Wave 2 — parallel with 04-05]
- [x] 04-03-PLAN.md — internal/actions package: orchestrator + mutex + middleware + verify + errors + A1 probe (ACT-01..11, SAFE-01..03, OBS-01) [Wave 3]
- [x] 04-04-PLAN.md — HTTP handlers + Server signature + main.go boot + OBS-03 guard + API.md (ACT-01..05, ACT-09, ACT-11, SAFE-01..02, OBS-01, OBS-03) [Wave 4]
- [x] 04-05-PLAN.md — STATE-04 SIGKILL fault-injection harness + cmd/sigkillhelper + PROJECT.md self-upgrade & install docs (STATE-04, STATE-05) [Wave 2 — parallel with 04-02]
- [x] 04-06-PLAN.md — 8 RED-first Playwright specs + disconnect-network.ts + crash-loop-stub + manual smoke (ACT-01..12, SAFE-01..03, STATE-04, OBS-01) [Wave 5] — closed via Option D; 5 of 8 specs GREEN via harness, 8 test bodies deferred to 04-07
- [ ] 04-07-PLAN.md — (deferred) e2e pull-path resolution via crane.Pull → docker.ImageLoad refactor (Option B); unblocks 8 deferred test bodies; REGISTERED, NOT scheduled — promotion gated on Phase 5/7 readiness or explicit user request

### Phase 5: Web UI Completeness
**Goal**: Ship the real Svelte 5 single-page UI — table, status badges, per-row Update / Rollback / Force-pull / Copy, toasts, 5 s polling, in-place-upgrade-safe asset caching, and the pre-action "display may flicker" warning for `flutter`/`weston`
**Depends on**: Phase 4
**Requirements**: UI-01, UI-02, UI-03, UI-04, UI-05, UI-06, UI-07, UI-08, UI-09, UI-10
**Success Criteria** (what must be TRUE):
  1. Playwright e2e test (red-first) covering the F6 acceptance surface: table renders columns (container / image:tag / current digest / available digest / previous digest / status badge / actions), buttons disable on click and re-enable on response, toast fires on success/failure, copy-icon copies the full digest
  2. In-place upgrade Playwright test: with the page open, rebuild the `hmi-update` image with a new bundle hash, `docker compose up -d hmi-update`, soft-refresh — page works without hard-refresh; `/assets/*` returns immutable Cache-Control and never falls back to `index.html`; every `.js` asset serves `Content-Type: application/javascript` (Pitfall 8)
  3. Targeting `flutter` or `weston` produces a pre-action "display may flicker" warning toast *before* recreate is triggered (Pitfall 5 UX surface)
  4. Header shows `Refresh`, `Watch now`, and a visible last-poll timestamp; rows where `allow-update=false` show no Update button and a small lock icon
  5. Manual smoke on an HMI-like stack with a 1024 px browser confirms all three per-row actions render cleanly and the toast UX is operator-readable
**Plans**: 5 plans
- [ ] 05-01-PLAN.md — Tailwind v4 @theme Solaris tokens + reduced-motion baseline (UI-01) [Wave 1]
- [ ] 05-02-PLAN.md — Header + Table + Row + StatusBadge + ActionButton + CopyButton + relative-time (UI-01..04, UI-07, UI-09) [Wave 2 — parallel with 05-03]
- [ ] 05-03-PLAN.md — Toast + ToastContainer + WarningModal + display-warning + focus-trap (UI-05, UI-08) [Wave 2 — parallel with 05-02]
- [ ] 05-04-PLAN.md — App.svelte rewrite: 5s poll + action wiring + toast/modal hosting + actions.ts (UI-04..08) [Wave 3]
- [ ] 05-05-PLAN.md — handlers.go Cache-Control + MIME hardening + 5 RED-first Playwright specs + manual smoke checkpoint (UI-01..10, Pitfall 8) [Wave 4]
**UI hint**: yes

### Phase 6: Display-Blackout UX Checkpoint (flutter/weston)
**Goal**: Make an explicit product decision — with the real UI from Phase 5 in front of the team — about how to surface the 5–30 s display blackout when recreating display-drawing services; ship documentation (and optional two-step UX) to match
**Depends on**: Phase 5
**Requirements**: UX-01, UX-02, UX-03
**Success Criteria** (what must be TRUE):
  1. UX-01 decision is recorded in PROJECT.md Key Decisions: (a) leave Update as-is + README warning, (b) two-step prepare/switch UX, or (c) per-service danger flag with double-confirm
  2. README contains a "before you click Update on flutter/weston" callout that reflects the chosen option, present on `git diff main` for the phase commit
  3. If option (b) was chosen: state schema gains `prepared_digest`, UI gains a third per-row button + corresponding action endpoint, and a Playwright e2e test (red-first) covers Stage 1 (prepare) → Stage 2 (switch) with the "Switch now" affordance and "display will flicker" confirmation
  4. Manual smoke on an HMI-like stack: an operator clicking Update on a `weston`-like service experiences the chosen UX without surprise
**Plans**: 1 plan
- [x] 06-01-PLAN.md — UX-01 decision record (option (a)) + README.md operator-facing callout + weston-warning e2e spec (UX-01, UX-02) [Wave 1]
**UI hint**: yes

### Phase 7: Deployment & Packaging
**Goal**: Produce the production-grade single OCI image and the compose deployment block that drops onto a clean Debian HMI with one documented install step (`id -g docker`); verify the <30 MB image and <30 MB RAM budgets; document the manual self-upgrade procedure
**Depends on**: Phase 6
**Requirements**: DEPLOY-01, DEPLOY-02, DEPLOY-03, DEPLOY-04, DEPLOY-05, DEPLOY-06, DEPLOY-07, DEPLOY-08, DEPLOY-09
**Success Criteria** (what must be TRUE):
  1. Playwright e2e test (red-first) for portability: copying `docker-compose.yml` to a second clean Debian 12 host and running `docker compose up -d` (with the documented `user: "65532:<docker-gid>"` step) produces a working install with the table loading at `:8080` and no manual UI steps (Acceptance criterion 6)
  2. Multi-stage Dockerfile builds on `node:22-alpine` → `golang:1.26-alpine` → `gcr.io/distroless/static-debian12:nonroot` (with `cc-debian12` fallback noted if the docker + compose CLI plugins push past 30 MB); final image size and idle RAM both measured <30 MB in CI
  3. Compose deployment block matches brief §F7 exactly: `image: ghcr.io/centroid-is/hmi-update:latest`, `ports: 8080:8080`, three bind-mounts (`docker.sock`, `docker-compose.yml:ro`, `hmi_update_state.json`), env (`HMI_UPDATE_CRON`, `HMI_UPDATE_COMPOSE_PATH`), labels including `hmi-update.watch=false`
  4. README install runbook documents the `id -g docker` step and the manual self-upgrade procedure (`docker compose pull hmi-update && docker compose up -d hmi-update` from a host shell, per Pitfall 6)
  5. Manual smoke on an HMI-like stack: clean install on a Debian 12 box that has not previously seen `hmi-update`; one operator runs the runbook end-to-end and reaches a working UI
**Plans**: 3 plans
- [ ] 07-01-PLAN.md — Production Dockerfile hardening + .dockerignore + image-prod Make target + version-injection ldflags (DEPLOY-01, DEPLOY-02, DEPLOY-03) [Wave 1]
- [ ] 07-02-PLAN.md — docker-compose.example.yml at repo root matching brief §F7 + §2.3 CLI-delivery bind-mounts (DEPLOY-04, DEPLOY-07, DEPLOY-08) [Wave 2 — parallel with 07-03]
- [ ] 07-03-PLAN.md — README install runbook + RED-first portability e2e + CI image-size/idle-RAM/portability gates (DEPLOY-02, DEPLOY-03, DEPLOY-05, DEPLOY-06, DEPLOY-08, DEPLOY-09) [Wave 2 — parallel with 07-02]

### Phase 8: CI/CD & Release Hardening
**Goal**: Lock in the green-CI-and-manual-smoke release gate — full GitHub Actions pipeline, three-tag publishing convention, and a real-GHCR anonymous-token-flow smoke job that catches Pitfall 2 regressions before publish
**Depends on**: Phase 7
**Requirements**: CI-01, CI-02, CI-03, CI-04, CI-05
**Success Criteria** (what must be TRUE):
  1. Playwright e2e test (red-first) for the publish gate: a CI run on `main` produces an image published to `ghcr.io/centroid-is/hmi-update` with all three tags (`:latest`, `:vX.Y.Z` when a Git semver tag is present, `:sha-<short>`); a deliberately-broken e2e blocks publish
  2. Lint (`go vet` + `golangci-lint`) → unit (`go test`) → tygo diff check → frontend build → multi-stage docker build → Playwright e2e → publish runs in that order; any step's failure stops the pipeline
  3. Real-GHCR smoke job runs a single read-only `crane.Digest()` against a frozen public image (e.g. a stable `ghcr.io/centroid-is/*` reference) and asserts 200 — fails loudly if anonymous token flow regresses (Pitfall 2 belt-and-braces; note: this smoke targets a Phase 3 concern but lives in the CI surface, hence its placement here)
  4. Release process documents the manual-smoke gate: a release is only tagged after the green CI run *and* a recorded manual smoke note on the elevator-hmi (or an HMI-like stack) per C4
  5. Manual smoke on an HMI-like stack confirms that a new `:sha-<short>` image pulled from GHCR runs cleanly under the Phase 7 install runbook
**Plans**: 3 plans
- [ ] 08-01-PLAN.md — Main CI workflow (.github/workflows/ci.yml) + .golangci.yml + image-size-check Make target + RED-first deliberately-broken e2e verification (CI-01, CI-03, CI-04) [Wave 1]
- [ ] 08-02-PLAN.md — Publish workflow (.github/workflows/publish.yml) with metadata-action@v5 three-tag emission + post-publish anonymous-token-flow smoke (CI-02, CI-04) [Wave 2 — depends on 08-01]
- [ ] 08-03-PLAN.md — RELEASING.md + SMOKE.md + PROJECT.md cross-link + dry-run verification of the release flow (CI-05) [Wave 3 — depends on 08-01, 08-02]

### Phase 9: Architectural Hardening (post-v0.1 bug-cluster)
**Goal**: Eliminate the compose-CLI shell-out failure class surfaced during the 2026-05-15/16 production bring-up by replacing `exec docker compose up -d --force-recreate` with socket-only in-process container recreation, unblocking the HMI display permanently, restoring the static-debian12 base image (~20 MB image shrink), removing CheckSelfProtection's 409 by routing self-update through a sidecar helper, and cutting CI wall time roughly in half via a parallel 2-job split. Four items grouped because they share the same root cause (compose-CLI surface) and naturally pair on the moby/moby/client primitive.
**Depends on**: Phase 4 (Update / Rollback / Force-pull Actions) — Phase 9 directly modifies the action layer's recreate path
**Requirements**: None — architectural hardening driven by 2026-05-15/16 incident log, not formal acceptance criteria. Locked items captured below.
**Success Criteria** (what must be TRUE):
  1. `docker-update` no longer invokes `docker compose` (or any subprocess) to recreate watched containers. All update / rollback / force-pull paths use `ContainerInspect → ContainerRemove → ContainerCreate → ContainerStart` directly via `moby/moby/client`. `grep -r "docker compose\|compose up\|exec.Command" internal/actions/` returns no production code hits.
  2. The `flutter` and `weston` services on the elevator-hmi (10.50.10.175) recover to a healthy display after an update or rollback driven by docker-update, with **no manual `docker compose up -d` step** by the operator. Demonstrated by a Playwright e2e that uses two services with `./relative-path` bind-mounts (mirroring the wayland-socket layout) and asserts both end up with the bind-mount resolved against the operator's host path, not docker-update's container path.
  3. The runtime base image reverts from `gcr.io/distroless/base-debian12:nonroot` to `gcr.io/distroless/static-debian12:nonroot`. The `/usr/bin/docker` and `/usr/libexec/docker/cli-plugins/docker-compose` bind-mounts are removed from `docker-compose.example.yml`. Final image size measured <12 MB (target: ~20 MB shrink from the current ~26 MB).
  4. Self-update succeeds end-to-end without `409 self_protection`: a `POST /api/update/docker-update` (or equivalent) call spawns a one-shot helper container, the helper recreates docker-update via the daemon API, the helper exits, and the new docker-update boots and self-verifies via restart-count + `/healthz` polling. Demonstrated by Playwright e2e + manual smoke on the HMI.
  5. CI wall time on `main` drops from ~7–8 min serial to ≤6 min wall via a 2-job split — `tests` (`go vet` + tygo diff + `go test -race`) running in parallel with `image+downstream` (UI build → docker build → e2e → idle-RAM → portability). Both jobs gate publish.
  6. RED-first regression tests exist for the four classes of bugs that drove this phase: (i) `compose_file_moved` 412, (ii) `COMPOSE_PROJECT_NAME` collision, (iii) `./relative-path` bind-mount resolution split, (iv) `CheckSelfProtection` 409. Each test was failing on the pre-Phase-9 codebase and passes after Phase 9 lands. TDD-first per CLAUDE.md C4.
  7. Manual smoke on elevator-hmi (10.50.10.175): one operator drives a full Update → verify-green → Rollback → verify-green cycle against `flutter` from the docker-update UI, with no terminal interaction during or after, and reaches a working display both before and after.

**Locked items** (all four ship in this phase unless explicitly dropped):
  - **(a) Socket-only recreate** — replace `exec docker compose up -d --force-recreate <svc>` with `ContainerInspect → ContainerRemove → ContainerCreate → ContainerStart` via `moby/moby/client`. Closes the compose-CLI failure class (path bug, `COMPOSE_PROJECT_NAME`, dynamic linker, `compose_file_moved` 412). Lets the base image revert to `static-debian12:nonroot`. ~150–250 LOC plus `ContainerInspect → ContainerCreate` field-translation tests.
  - **(b) Compose-path fix** — subsumed by (a). Interim fallback if (a) is split out: read `com.docker.compose.project.working_dir` from the watched container's labels and pass `--project-directory <host-path>` to the compose invocation. ~5 LOC interim; 0 LOC under (a).
  - **(c) CI 2-job split** — `tests` (`go vet` + tygo diff + `go test -race`, ~3 min) ‖ `image+downstream` (UI → docker build → e2e → idle-RAM → portability, ~5–6 min). Both gate publish. Test job needs `mkdir -p internal/api/dist` stub so `//go:embed all:dist` parses without the UI artifact. ~30 lines of YAML. Independent of (a)/(b)/(d) — can land first.
  - **(d) Self-update via sidecar helper** — replace `409 self_protection` with a Watchtower-style one-shot helper container that drives the daemon API to recreate docker-update itself; new docker-update self-verifies via restart-count + `/healthz` polling. Naturally builds on (a)'s `ContainerCreate` primitive. ~200–300 LOC plus a minimal helper image.

**Locked dependencies:** (a) implies (b); (d) builds on (a)'s `ContainerCreate` infrastructure. (c) is independent and can ship first. Estimated total: 1 milestone cycle.

**Plans**: TBD — to be produced by `/gsd-plan-phase 9`. Expected wave structure: Wave 1 = (c) CI split (independent); Wave 2 = (a) socket-only + (b) absorbed; Wave 3 = (d) self-update sidecar (depends on (a)).

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Walking Skeleton & Test Harness | 1/4 | In Progress|  |
| 2. Docker Client & Compose-File Reader | 0/5 | Not started | - |
| 3. Registry, Polling & Update Detection | 0/5 | Not started | - |
| 4. Update / Rollback / Force-pull Actions, Safety & State Persistence | 6/6 + 1 deferred | Closed (Option D); 04-07 registered, not scheduled | 2026-05-15 |
| 5. Web UI Completeness | 0/5 | Not started | - |
| 6. Display-Blackout UX Checkpoint | 0/1 | Not started | - |
| 7. Deployment & Packaging | 0/3 | Not started | - |
| 8. CI/CD & Release Hardening | 0/3 | Not started | - |
| 9. Architectural Hardening (post-v0.1 bug-cluster) | 0/0 | Not started | - |
