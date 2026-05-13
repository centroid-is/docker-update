# Project Research Summary

**Project:** `hmi-update` — single-binary Go container update manager with embedded Svelte UI for Centroid elevator HMI boxes
**Domain:** Container lifecycle tool / on-prem ops sidecar (OCI poller + Docker Compose actuator)
**Researched:** 2026-05-13
**Confidence:** HIGH

## Executive Summary

`hmi-update` occupies a genuinely unoccupied slot in the 2026 container-update ecosystem. After Watchtower was archived on 2025-12-17, the field collapsed into three lanes — notifier-only (Diun), auto-updater (WUD, Dockhand, Tugtainer, Shepherd), and platform/dashboard (Portainer, Komodo). **None of them offer operator-driven, per-container, single-slot rollback to a previous digest as a primary affordance**: Watchtower disclaims it; WUD has none; Komodo only does stack-level rollback (per-service is open issue #1276); Dockhand/Tugtainer/Shepherd only roll back automatically on a failed healthcheck. That gap — combined with the "one binary, one JSON file, one image" deployment posture that Komodo's three-container/MongoDB topology can't match — is the product's anchor. Frame `hmi-update` as the *only* operator-driven per-container rollback tool in the 2026 field, sized for a single HMI on a LAN.

The brief's technology choices hold up, but **three corrections are mandatory**: replace the deprecated `github.com/docker/docker/client` with `github.com/moby/moby/client@v0.4.1` (Docker Engine v29 renamed the module and CVEs are only patched on the new path); bump Go from 1.23 to 1.26 (1.23 went EOL 2026-02-11); and pin the runtime base to `gcr.io/distroless/static-debian12:nonroot` (the unversioned `static:nonroot` tag silently floats between Debian majors, exactly the wrong behaviour for an unattended HMI). Two strong additions: use `google/go-containerregistry` (`crane.Digest`) rather than hand-rolling the Bearer-token + multi-arch index dance — this is *the* code area where WUD 8.2.2 shipped two named bugs; and drive `docker compose` via `os/exec` subprocess instead of the Compose Go SDK (the SDK drags BuildKit/containerd transitive deps that would blow the 30 MB image budget).

The dominant risks are operational, not technological. The two WUD-class registry bugs (single-arch digest extraction from the wrong field; empty `Authorization` placeholder header that GHCR rejects with 403) must be designed out from the first registry test with both manifest shapes in the fixture set. Recreating `flutter` or `weston` on the running HMI causes an operator-visible 5–30 s display blackout — this likely forces a two-step Update UX (pre-pull and prepare, then explicit "switch now" with a "display will flicker" toast). The distroless `nonroot` user (UID 65532) cannot reach `/var/run/docker.sock` without the host's docker GID being passed in via compose `user:`. The `go:embed` + Vite cache strategy must strictly cache `/assets/*` as immutable while keeping `index.html` no-cache, or every binary upgrade silently breaks operator browsers. And **self-update of `hmi-update` from inside its own container is structurally impossible** — must be server-side refused and documented as a host-shell step.

## Key Findings

### Recommended Stack

Stack is conservative, dependency-minimal, and stays on currently-supported majors. The brief is largely correct; the corrections below are mandatory rather than optional. Full detail in [STACK.md](./STACK.md).

**Core technologies (corrections to the brief in bold):**

- **Go 1.26** (not 1.23) — Go 1.23 hit EOL 2026-02-11
- **`github.com/moby/moby/client@v0.4.1`** (not `github.com/docker/docker/client`) — old module deprecated as of Docker Engine v29; CVEs only patched on the new path
- **`github.com/google/go-containerregistry`** (`crane.Digest` + `pkg/v1/remote`) — replaces brief's hand-rolled HTTP/Bearer/multi-arch code; the single biggest reduction in WUD-class bug surface
- **`docker compose` via `os/exec` subprocess** (not the Compose Go SDK) — SDK pulls BuildKit/containerd transitively, blows the 30 MB image budget
- **stdlib `net/http`** — Go 1.22+ `ServeMux` covers the ~8 routes; no `chi`
- **`robfig/cron/v3` v3.0.1** — matches `HMI_UPDATE_CRON` contract verbatim
- **`log/slog`** stdlib — structured JSON
- **Svelte 5.55 (runes) + Vite 7 + Tailwind v4.3 + `@tailwindcss/vite`** — pin `vite-plugin-svelte@6` until Vite 8 soaks
- **`tygo`** — Go → TypeScript type generation from `internal/api/types.go`
- **`//go:embed all:dist`** — embed Vite output into the binary
- **`gcr.io/distroless/static-debian12:nonroot`** (pin the Debian suffix)
- **`@playwright/test@1.60`** + `globalSetup` driving `docker compose -f compose.test.yml up -d --wait`
- **`project-zot/zot` as fake registry** in e2e — pure-Go, OCI-compliant, mutable tags
- **`google/renameio/v2`** — handles temp+rename+dirsync correctly; do not roll our own

Expected final image: ~10–14 MB (well under 30 MB cap), with headroom for shipping `docker` + `compose` CLI plugins into the runtime image (Phase A must verify whether `static-debian12` is too minimal — `cc-debian12` is the fallback).

### Expected Features

Full detail in [FEATURES.md](./FEATURES.md): 14 table-stakes, 8 differentiators, 14 anti-features mapped across WUD/Komodo/Watchtower/Diun/Tugtainer/Dockhand/Portainer/Shepherd.

**Must have (table stakes):**

- Per-container digest-based detection against `:latest` with correct multi-arch index handling (F1; the historical WUD 8.2.2 bug area)
- Manual per-row Update: pull → record previous → `compose up -d --force-recreate <svc>` (F2)
- Display current + available digest with copy-to-clipboard (F6)
- Status badges (`up-to-date` / `update-available` / `rollback-available` / `disabled`)
- `GET /healthz` + `GET /api/state` (N8)
- Atomic single-file JSON state, no DB (F4, N2)
- Idempotent update/rollback (200 + no-op when at target) (N3)
- Label-based opt-in `hmi-update.watch=true`
- Manual "force a poll now" button
- Docker socket via bind mount
- Structured `slog` for every action (N7)
- Disabled-state buttons when no rollback target exists

**Should have (differentiators — each is a direct response to a documented gap in the competitor pack):**

- **Operator-driven per-container rollback (F3)** — *the* headline differentiator; no existing tool offers this exact thing
- **Single-slot toggle semantic** — Update and Rollback flip the same two digests back and forth
- **Single binary, single JSON file, single OCI image** — Komodo's biggest cost; we keep capability without the cost
- **Compose-native, never mutates the compose file** — avoids WUD's #546 regression
- **Force-pull endpoint (F8)** — no competitor has this exact thing
- **Server-enforced safety labels `allow-update=false` / `allow-rollback=false`** — critical for timescaledb
- **Tag-pattern constraint `hmi-update.tag-pattern=<regex>` (F5)** — required so `latest-pg17` doesn't get mistaken for `latest-pg18`
- **Compose service name as stable API identifier** — kills "container disappeared from UI" bug class

**Defer (v2+):**

- Auto-update on detection, multi-host fleet management, auth/RBAC, logs viewer / shell exec / stats, notifications, private registry credentials, N-deep rollback history, arm64 builds, drift detection on digest-pinned services, compose-file rewriting, auto-pruning of old images, server-side WebSockets/SSE, UI kit.

### Architecture Approach

Modular monolith with hexagonal-flavoured boundaries: HTTP → service (`poll`, `state`, `actions`) → adapters (`registry`, `docker`, `compose`, filesystem). State lives in-memory under `sync.RWMutex` and persists via atomic temp+rename+dirsync to a single JSON file. **Concurrency model: single-consumer channel for poll work** with cron + Docker events as producers, plus a per-service `map[string]*sync.Mutex` to serialise concurrent updates to the same service. Brief's package list is validated; ARCHITECTURE.md adds `internal/compose/` and `internal/actions/` plus the embed-target convention. Full detail in [ARCHITECTURE.md](./ARCHITECTURE.md).

**Major components:**

1. `cmd/hmi-update` — thin wire-up
2. `internal/api` — HTTP routing, JSON marshalling, MIME-aware static handler with strict `/assets/*` no-fallback; owns `types.go` that `tygo` consumes
3. `internal/state` — in-memory cache + `sync.RWMutex` + `renameio.WriteFile`; versioned schema
4. `internal/poll` — cron + Docker events with debounce → single-consumer channel
5. `internal/registry` — thin wrapper over `crane.Digest`
6. `internal/docker` — facade over `moby/moby/client`
7. `internal/compose` — `exec.CommandContext` wrapper with stdout/stderr → slog
8. `internal/actions` — `Update`, `Rollback`, `ForcePull` orchestrators
9. `ui/` — Svelte 5 SPA, Vite output to `internal/api/dist/`
10. `e2e/` — Playwright + `compose.test.yml` (zot + hmi-update + 2 stub watched containers)

**Frontend ↔ Backend contract:** `tygo` generates `ui/src/lib/types.d.ts` from `internal/api/types.go`; `make types` is CI fail-on-diff. Refresh transport: plain 5-second `fetch` polling against `GET /api/state` (memory-only, no I/O) — no SSE, no WebSocket.

### Critical Pitfalls

13 pitfalls catalogued in [PITFALLS.md](./PITFALLS.md) with phase mappings. Top five for roadmap visibility:

1. **WUD-class registry bugs (Pitfalls 1, 2, 3)** — single-arch digest must come from `Docker-Content-Digest` *response header* not body re-hash; never send `Authorization: Basic Og==` placeholder on anonymous token flow (GHCR returns 403 not 401); always send full Accept-header matrix and branch on response `Content-Type`. **Prevention:** use `crane.Digest()` (handles all three correctly), and ship the e2e fixture serving **both** an OCI image index *and* a direct single-arch manifest in the same test run.
2. **`compose up --force-recreate` silently uses stale local image (Pitfall 4)** — `compose up` does not pull. **Prevention:** after `docker pull`, verify local image's `RepoDigests[0]` matches registry's digest *before* recreate; record `current_digest` only after verifying the recreated container's image SHA matches the target (assert in e2e, not just compose exit code).
3. **Display blackout on `flutter`/`weston` recreate (Pitfall 5)** — recreating these blanks the elevator screen for 5–30 s with operator standing next to it. **This requires a design checkpoint in the roadmap**: probably a two-step Update UX (Stage 1: pre-pull + extract; Stage 2: explicit "Switch now" with "display will flicker" toast). README must call `weston` a "double-confirm danger" service.
4. **Distroless `nonroot` cannot reach `docker.sock` (Pitfall 9)** — host `docker` GID varies (998/999/100); container has only `nonroot:x:65532:`. **Prevention:** compose `user: "65532:<host-docker-gid>"`, document `id -g docker` install step, `/healthz` distinguishes socket-EACCES from socket-missing with remediation hint.
5. **`go:embed` + Vite cache strategy (Pitfall 8)** — strict `/assets/*` immutable + `index.html` no-cache, with `/assets/*` returning 404 (never SPA fallback) on miss. Plus explicit `mime.TypeByExtension` registration so distroless minimal env serves `.js` as `application/javascript`, not `text/html`. Otherwise binary upgrades silently break operator browsers.

Also non-negotiable: **self-update of `hmi-update` is structurally impossible** from inside its own container (Pitfall 6). With `hmi-update.watch=false` the row is hidden, but server must *also* refuse `POST /api/containers/<own-service>/update` with 409 — defense in depth. Documented manual upgrade: `docker compose pull hmi-update && docker compose up -d hmi-update` from a host shell.

Other notable: state file corruption from non-atomic writes (Pitfall 7 — `renameio` + schema-version); compose file path drift on atomic-save editors (Pitfall 10 — `stat` before each action); concurrent updates from double-click or cron-vs-manual race (Pitfall 11 — per-service mutex + 409 on collision + UI button-disable); restart-policy hides crashing recreate (Pitfall 12 — poll `docker inspect` for `State.Running == true` and `RestartCount` unchanged for ≤15s); SSRF / path traversal (Pitfall 13 — strict service-name regex at router, in-memory map lookup only).

## Implications for Roadmap

The TDD constraint forces a specific ordering: **the test harness itself must work before any feature test can be written**. This is the single most important architectural insight — it implies a dedicated walking-skeleton phase *before* the first feature phase.

### Phase A: Walking skeleton (test harness + scaffolding)

**Rationale:** TDD with Playwright requires the harness can drive the binary and assert on output *before* the first F-requirement test can fail meaningfully. Without this phase, F1's red test is fake.
**Delivers:** Repo + Dockerfile + Makefile + CI; `internal/state` with atomic persist (unit-tested); minimum HTTP server with `GET /healthz` and `GET /api/state`; empty Svelte+Vite+Tailwind shell rendering a stub table; `e2e/compose.test.yml` with zot fake registry + hmi-update + 1 stub watched container; manifest-push fixture (`oras push` from Playwright); `tygo` wired into Makefile as CI fail-on-diff check; first Playwright smoke test asserting table renders and `/api/state` returns valid JSON.
**Uses:** All Phase A stack elements (Go scaffolding, Vite, Tailwind, Playwright, zot, tygo, renameio).
**Addresses:** F4 (state schema + atomic writes), parts of N1/N2/N8.
**Avoids:** Pitfall 7 (atomic writes proven up-front), Pitfall 9 (boot-time `/healthz` distinguishes EACCES early).

### Phase 1: Docker client + compose-file reader + scaffolding hardening

**Rationale:** F1 needs to enumerate watched containers and read the compose file path; both are dependencies for every subsequent phase. Done before registry work because it surfaces the GID/permission and bind-mount issues that block later phases.
**Delivers:** `internal/docker` facade (list-by-label, inspect, events, pull, tag); compose-file reader with stat-before-act and inode-drift detection; `/healthz` enhancement distinguishing socket EACCES from missing; first-time-install smoke test on clean Debian 12 host.
**Implements:** Architecture component 6.
**Avoids:** Pitfall 9 (distroless nonroot vs docker.sock GID), Pitfall 10 (compose file path drift).

### Phase 2: Registry / digest detection (F1, F5)

**Rationale:** Riskiest phase — owns the WUD-class bug area and the multi-arch manifest matrix. Doing it second means docker + registry layers are both proven before the update/rollback orchestrator is written.
**Delivers:** `internal/registry` thin wrapper over `crane.Digest`; `internal/poll` with cron + debounced docker events feeding single-consumer channel; tag-pattern regex compiled at startup with fail-loud errors; e2e fixtures push **both** an OCI image index and a direct single-arch manifest.
**Implements:** Architecture components 4 and 5.
**Avoids:** Pitfalls 1, 2, 3 (use `crane.Digest`; both manifest shapes in fixtures; real-GHCR smoke test in CI).
**Addresses:** F1, F5; exercises N7 logging structure end-to-end.

### Phase 3: Update + Rollback + Force-pull execution (F2, F3, F8, N3, N4)

**Rationale:** All three actions share the same orchestrator skeleton. F2 must precede F3 because rollback consumes `previous_digest` from F2; F8 is trivial after F2. Concurrency hardening lives here because this is the first phase with mutating actions.
**Delivers:** `internal/compose` exec wrapper; `internal/actions` orchestrators; per-service `map[string]*sync.Mutex`; verify-after-recreate poll (15 s deadline on `State.Running` + `RestartCount`); server-side self-protection (refuse `update`/`rollback` targeting `hmi-update` itself); server-enforced `allow-update`/`allow-rollback` returning 409; idempotent no-op; strict service-name validation at router; F4's restart-mid-write fault-injection test.
**Implements:** Architecture components 7 and 8.
**Avoids:** Pitfalls 4 (verify image SHA), 6 (self-update server refusal), 11 (per-service mutex), 12 (verify-after-recreate poll), 13 (router validation).
**Addresses:** F2, F3, F4 (now end-to-end testable), F8, N3, N4.

### Phase 4: UI completeness + embedding strategy (F6)

**Rationale:** Phase A shipped an empty Svelte shell sufficient for preceding F-tests to render against. Phase 4 builds the real UI. The cache-strategy gymnastics live here because this is the first phase shipping a non-trivial bundle that must survive binary upgrade.
**Delivers:** Real Svelte UI matching F6 acceptance; in-place upgrade Playwright test (upgrade hmi-update image with tab open, soft-refresh, page works); button-disable-on-click + toast-on-completion (Pitfall 11); pre-action "display may flicker" toast (Pitfall 5).
**Avoids:** Pitfall 8 (cache + MIME), parts of Pitfall 5 (UX warning), parts of Pitfall 11 (UI side).
**Addresses:** F6 in full, UX surface of N4.

### Phase 5: Design checkpoint — display-blackout UX for `flutter`/`weston`

**Rationale:** Pitfall 5 is operator-visible and likely demands a *product* decision, not a *technical* one. Roadmap should not assume basic F2 "click → recreate" is acceptable for display-drawing services. Discrete checkpoint deciding between (a) leave F2 as-is + README warning, (b) two-step Update UX (pre-pull + "Switch now"), or (c) per-service "danger flag" requiring double-confirm. Placed after Phase 4 so the team has the real UI in front of them when deciding.
**Delivers:** Documented decision; if (b) or (c), corresponding scope addition; README "before you click Update on flutter/weston" callout.
**Addresses:** Pitfall 5 with deliberate product framing.

### Phase 6: Deployment, observability, security hardening (F7, N1, N5, N6, N7, N8, CI)

**Rationale:** Deployment-time concerns best resolved as focused phase rather than scattered. Includes GHCR publishing with semver tags, image-size verification (<30 MB), idle-RAM verification (<30 MB), documented manual self-upgrade, bearer-token redaction audit.
**Delivers:** Full `Dockerfile` multi-stage pinned to Go 1.26 + Node 22 + distroless `static-debian12:nonroot` (or `cc-debian12` if Phase A measurements demand it); `docker` + `compose` CLI in runtime image; GitHub Actions CI with semver tagging; README install runbook with `id -g docker` step; documented hmi-update manual self-upgrade path; clean Debian 12 smoke-test; security-review checklist from Pitfall 13.
**Avoids:** Remainder of Pitfall 9 (runbook), Pitfall 6 (self-upgrade docs), parts of Pitfall 13 (log redaction audit).
**Addresses:** F7, N1, N5, N6, N7, N8, CI.

### Phase Ordering Rationale

- **Phase A before F1's red test:** TDD-against-real-stack requires harness exists first.
- **Phase 1 (Docker client) before Phase 2 (registry):** the daemon side most likely to surface GID/permission issues that would otherwise block every later phase at deploy time.
- **Phase 2 (registry) before Phase 3 (actions):** F1 produces state that F2 mutates and F3 reverts.
- **F2 before F3 within Phase 3:** rollback consumes `previous_digest` from F2.
- **F8 inside Phase 3 after F2:** trivial reuse of pull + verify path.
- **F4 acceptance test inside Phase 3, not Phase A:** primitives ship in A, but end-to-end "compose restart mid-update preserves rollback target" needs F2 producing real state.
- **Phase 4 (UI) before Phase 5 (design checkpoint):** team needs real UI in front of them for the two-step-vs-toast decision.
- **Phase 5 before Phase 6:** deployment runbook depends on whether Phase 5 adds operator-facing affordances.
- **Phase 6 last:** image-size and idle-RAM verification have to wait until all code is in.

### Research Flags

**Needs `/gsd-research-phase`:**

- **Phase 2 (registry):** primary risk concentration. Verify latest `crane.Digest` API shape, anonymous-token semantics across GHCR vs Docker Hub vs Quay, `Retry-After` handling for Docker Hub's 100/6h limit. Re-confirm zot 2026 supports OCI image index push via `oras`.
- **Phase 3 (actions / compose subprocess):** verify minimum `docker compose` version pinning, `--force-recreate` interactions with `pull_policy` across Compose v2.20–v2.30, `cmd.WaitDelay` on Go 1.26, and `docker inspect` JSON shape for `State.Health.Status` / `State.RestartCount` stability.
- **Phase 5 (design checkpoint):** *not technical research* — needs operator-experience input + competitive scan of how Komodo/Portainer surface "this will interrupt service" warnings.

**Standard patterns (skip research-phase):**

- **Phase A (walking skeleton):** `//go:embed`, `tygo`, Vite, Playwright `globalSetup`, `renameio` are well-trodden; STACK.md has all the versions and recipes.
- **Phase 1 (Docker client + compose-file reader):** standard `moby/moby/client` usage; GID issue documented in PITFALLS.md with fix.
- **Phase 4 (UI + embedding):** Svelte 5 + Vite + Tailwind v4 are well-documented happy paths; cache-strategy documented in ARCHITECTURE.md.
- **Phase 6 (deployment):** GitHub Actions + GHCR + distroless is standard; only open question (distroless base) settled by Phase A's image-size measurement.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | All versions verified against `pkg.go.dev`, npm registry, official release pages within the week. Three corrections documented with primary sources. |
| Features | HIGH | Competitor matrix built from 12+ primary GitHub repos. "Operator-driven per-container rollback gap" confirmed by Watchtower #2099, Komodo #1276, direct inspection of Dockhand/Tugtainer/Shepherd rollback semantics. |
| Architecture | HIGH | Single-binary Go + embedded SPA + Docker socket + OCI poll is heavily-trodden. Concurrency model (RWMutex + single-consumer channel + per-service mutex) is canonical. Atomic write via `renameio` is the standard recommendation. |
| Pitfalls | HIGH on registry/compose/embed/distroless mechanics (corroborated by Docker, distroless, Vite, registry-spec issue trackers); MEDIUM on display-blackout specifics (no direct community precedent — derived from Wayland/Weston lifecycle and field-engineer UX reasoning). |

**Overall confidence:** HIGH

### Gaps to Address

- **Display-blackout UX design (Pitfall 5)** — explicit Phase 5 checkpoint. Needs operator input + product decision; not resolvable from research alone.
- **Distroless runtime base for `docker` CLI** — Phase A must measure whether `static-debian12:nonroot` suffices or whether `cc-debian12:nonroot` (with libc) is required. Start with `static-debian12`, fall back if needed. 30 MB budget holds either way.
- **Real-GHCR smoke test cadence** — CI smoke against a real public GHCR repo catches anonymous-token-flow regressions but introduces external dependency. Phase 2 decides whether to gate CI on it or schedule it.
- **`oras` vs raw-HTTP for manifest-push fixtures** — Phase A picks `oras push` via `child_process`; fall back to a 30-LOC Go helper in the test stack if `oras` is flaky in CI.
- **CI rate-limit on Docker Hub** — mitigate by using zot for everything except one read-only Docker Hub smoke check.
- **Two-step Update UX scope (if Phase 5 picks option b)** — adds a "pre-pulled" digest field in state, a third per-row button, and additional toast states. Roughly +1 small phase or expanded F2 scope.

## Sources

### Primary (HIGH confidence)
- [STACK.md](./STACK.md), [FEATURES.md](./FEATURES.md), [ARCHITECTURE.md](./ARCHITECTURE.md), [PITFALLS.md](./PITFALLS.md) — all verified 2026-05-07 to 2026-05-13

Key external corroboration:
- Go release page (1.26.3 current, 1.23 EOL 2026-02-11)
- Docker Engine v29 release notes (module rename `docker/docker` → `moby/moby`)
- distroless README (unversioned `static:nonroot` as moving floor)
- Watchtower archive (2025-12-17) + discussion #2099 ("no rollback")
- Komodo issue #1276 (per-service rollback open feature request)
- WUD issues #391, #819 (the two reference bugs)
- distribution/distribution #2395 (`Docker-Content-Digest` semantics)
- compose-spec compose-go (CLI-via-subprocess trade-off)

### Secondary (MEDIUM confidence)
- Field-engineer UX reasoning for Pitfall 5 — derived from Wayland/Weston lifecycle; no direct community precedent
- Image-size budget for `docker compose` CLI inside distroless — needs Phase A measurement
- 2026 minor releases of WUD and Komodo — primary uncertainty area called out by FEATURES.md

### Tertiary (LOW confidence)
- None — research did not rely on single-source or speculative material for any roadmap-affecting decision.

---
*Research completed: 2026-05-13*
*Ready for roadmap: yes*
