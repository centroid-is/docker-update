---
phase: 9
slug: architectural-hardening-post-v0-1-bug-cluster
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-16
---

# Phase 9 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Derived from `09-RESEARCH.md` § Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework (unit / table)** | Go `testing` (stdlib) — existing helpers in `internal/actions/`, `internal/docker/`, `internal/compose/` |
| **Framework (e2e)** | Playwright `@playwright/test` 1.60 with `docker compose up -d --wait` globalSetup |
| **Config file** | `go.mod` (root); `e2e/playwright.config.ts` |
| **Quick run command** | `go test ./... -race -run <TestName>` (single-test, sub-second on a watched subset) |
| **Full suite command** | `go test ./... -race` + `make e2e-cron-fast` |
| **Estimated runtime** | ~45–90 s unit (with `-race`); ~6 min e2e suite |

---

## Sampling Rate

- **After every task commit:** `go test ./internal/{recreate,docker,actions,api,selfupdate}/ -race -run <affected>` (sub-second to ~10 s)
- **After every plan wave:** Full `go test ./... -race` + the wave's e2e specs (`make e2e-cron-fast` filtered)
- **Before `/gsd-verify-work`:** Full suite green + manual smoke checkpoint on elevator-hmi 10.50.10.175
- **Max feedback latency:** 10 s for per-task quick runs; 6 min for per-wave full

---

## Per-Task Verification Map

Phase 9 has **no formal REQ-IDs** (architectural hardening, incident-driven). The 7 Success Criteria from ROADMAP.md Phase 9 are the goal-backward anchors. Task IDs (`9-WW-TT`) will be filled by the planner; this table reserves the test slots.

| SC # | Behavior | Test Type | Automated Command | File (Wave 0 = to-create) | Status |
|------|----------|-----------|-------------------|---------------------------|--------|
| SC-1 | No `docker compose`/`exec.Command` in `internal/actions/` or `internal/recreate/` (production code) | static check | `make grep-no-compose` (zero matches in non-`_test.go` files) | Wave 0 — Makefile target + CI tests-job step | ⬜ pending |
| SC-2 (a) | Relative-path bind-mounts resolve to operator host paths after recreate | unit | `go test ./internal/recreate/ -run TestTranslate_HostConfig_Binds_AbsoluteAfterDaemonResolution -race -v` | Wave 0 — `internal/recreate/translate_test.go` | ⬜ pending |
| SC-2 (b) | Two services with `./relative-path` bind-mounts recover after a Phase-9 Update | e2e | `cd e2e && npx playwright test relative-bind-mount.spec.ts` | Wave 0 — `e2e/relative-bind-mount.spec.ts` + fixture in `e2e/compose.test.yml` | ⬜ pending |
| SC-3 (a) | Base image is `static-debian12:nonroot` | static check | `grep '^FROM gcr.io/distroless/static-debian12:nonroot' Dockerfile` | Wave 0 — CI tests-job inline grep | ⬜ pending |
| SC-3 (b) | Final image size <12 MB | size gate | tighten existing `image-size` gate threshold | Existing — re-tune in `.github/workflows/ci.yml` | ⬜ pending |
| SC-3 (c) | `docker-compose.example.yml` has no `/usr/bin/docker` or `/usr/libexec/docker/cli-plugins` bind-mounts | static check | `! grep -E '/usr/(bin/docker\|libexec/docker/cli-plugins)' docker-compose.example.yml` | Wave 0 — CI tests-job inline grep | ⬜ pending |
| SC-4 (a) | `POST /api/self-update` returns 202 + helper-spawned body | unit | `go test ./internal/api/ -run TestHandleSelfUpdate_202_HelperSpawned -race` | Wave 0 — `internal/api/handlers_self_test.go` | ⬜ pending |
| SC-4 (b) | Self-update succeeds end-to-end (parent exits, helper recreates, new parent boots, `/healthz=200`) | e2e | `cd e2e && npx playwright test self-update.spec.ts` | Wave 0 — `e2e/self-update.spec.ts` | ⬜ pending |
| SC-5 | CI wall time on `main` ≤6 min | measurement | observed on `gh run list --workflow=ci.yml --branch main --limit 5` post-merge | Wave 0 — informal measurement (no test file); CI status badge recommended | ⬜ pending |
| SC-6 (i) | `compose_file_moved` 412 regression (RED on pre-9, GREEN post-9) | unit | `go test ./internal/actions/ -run TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly -race` | Wave 0 — `internal/actions/orchestrator_test.go` add | ⬜ pending |
| SC-6 (ii) | `COMPOSE_PROJECT_NAME` collision regression (structurally impossible post-9) | unit | `go test ./internal/recreate/ -run TestRecreate_NoComposeProjectNameEnvDependency -race` | Wave 0 — `internal/recreate/translate_test.go` add | ⬜ pending |
| SC-6 (iii) | `./relative-path` regression (RED pre-9, GREEN post-9) | unit + e2e | as in SC-2 above | covered | ⬜ pending |
| SC-6 (iv) | `CheckSelfProtection` still 409 for per-service self; new `/api/self-update` bypasses it | unit | `go test ./internal/api/ -run "TestHandleUpdate_DockerUpdateSvc_StillReturns409\|TestHandleSelfUpdate_BypassesCheckSelfProtection" -race` | Wave 0 — `internal/api/handlers_self_test.go` | ⬜ pending |
| SC-7 | Manual smoke on elevator-hmi: full Update→verify→Rollback→verify via UI, no terminal interaction | manual | SMOKE.md entry; commit reference | Existing template | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/recreate/recreate_test.go` — atomic recreate-sequence cases (Stop fails, Remove fails, Create fails, NetworkConnect fails, Start fails)
- [ ] `internal/recreate/translate_test.go` — 13+ field-translation cases (one per row in RESEARCH.md Translation Table) including `COMPOSE_PROJECT_NAME` no-env assertion
- [ ] `internal/api/handlers_self_test.go` — 202 response, bypass CheckSelfProtection, 503 on unwired selfUpdater, 409 on actions-in-flight (per RESEARCH.md Open Question 5)
- [ ] `internal/selfupdate/spawn_test.go` — `Spawn` returns helperID; ContainerCreate args carry `--self-update-orchestrator` flag; AutoRemove=true; helper-label preset
- [ ] `internal/selfupdate/orchestrate_test.go` — orchestrator-mode main path verifies via `/healthz` polling; timeout exits non-zero
- [ ] `e2e/relative-bind-mount.spec.ts` — fixture: two services with `./test-relative-mount` volumes; Update via API; assert new container's `HostConfig.Binds[0]` is absolute and matches operator host path
- [ ] `e2e/self-update.spec.ts` — fixture: a `docker-update` container; `POST /api/self-update`; poll `/healthz` on new container; assert restart-count delta and image-tag change
- [ ] `e2e/compose.test.yml` — add a relative-bind-mount service fixture (mirroring flutter's wayland-socket pattern)
- [ ] `Makefile` — `grep-no-compose` static-check target + invocation in CI tests job
- [ ] `Makefile` — defensive `mkdir -p internal/api/dist` step (belt-and-braces for fresh worktrees with no UI build)
- [ ] Static-check scripts: image-size threshold tightening, `docker-compose.example.yml` no-CLI-mounts grep

**Framework install:** Already present (Go testing + Playwright). No new framework needed.

---

## Manual-Only Verifications

| Behavior | SC | Why Manual | Test Instructions |
|----------|----|------------|-------------------|
| Full Update→verify→Rollback→verify cycle on flutter from docker-update UI, with no terminal interaction, reaching a working display both before and after | SC-7 | Display health is a physical observation (working pixels on the HMI screen); can't automate without a camera | 1) SSH to `centroid@10.50.10.175`; 2) Open `http://10.50.10.175/` in a LAN browser; 3) Click **Update** on `flutter`; observe display blackout + recovery to working frame; 4) Click **Rollback** on `flutter`; observe blackout + recovery to prior frame; 5) Record SMOKE.md entry with timestamps + observer signature |
| Self-update end-to-end via UI (not just e2e) on the HMI | SC-4 (b) augment | The e2e covers logical correctness; HMI smoke covers actual host-daemon behavior under production constraints (cgroup limits, real systemd, real persistent state) | 1) Note current `docker-update` image digest via `/api/state`; 2) Push a `:sha-<new>` tag to GHCR; 3) Click **Update** on `docker-update` itself in the UI; 4) Wait ~30 s for the page to reconnect; 5) Verify `/api/state` shows the new image digest and `/healthz=ok` |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify command or Wave 0 dependency
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references in the per-task map
- [ ] No watch-mode flags (`-watch`, `--watchAll`)
- [ ] Feedback latency <10 s for quick runs
- [ ] `nyquist_compliant: true` set in frontmatter (set by planner after task IDs resolve to test commands)

**Approval:** pending
