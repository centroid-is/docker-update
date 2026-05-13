---
phase: 01
slug: walking-skeleton-test-harness
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-13
---

# Phase 1 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework (Go)** | `testing` stdlib + Go 1.26 |
| **Framework (E2E)** | `@playwright/test` v1.60+ |
| **Config file (Go)** | none — stdlib `go test ./...` |
| **Config file (E2E)** | `e2e/playwright.config.ts` (created in Wave 0) |
| **Quick run command** | `go test ./internal/state/... -count=1` |
| **Full suite command** | `make test && make e2e` |
| **Estimated runtime** | ~60–120 s end-to-end (Go ~2 s, e2e stack bring-up dominates) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/state/... -count=1` (fast, <2 s)
- **After every plan wave:** Run `make test && cd e2e && npx playwright test --grep smoke`
- **Before `/gsd-verify-work`:** Full suite (`make test && make e2e`) must be green
- **Max feedback latency:** 30 s (per-task) / 120 s (per-wave)

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 01-01-* | 01 | 1 | FOUND-01 | — | Repo skeleton present | structure | `test -d cmd/hmi-update && test -d internal/state && test -f go.mod` | ❌ W0 | ⬜ pending |
| 01-02-* | 01 | 1 | FOUND-02 / STATE-02 | T-01-02 ReadTimeout | Atomic write + parse-old-or-new under contention | unit | `go test ./internal/state/... -run TestPersist -count=10` | ❌ W0 (`internal/state/persist_test.go`) | ⬜ pending |
| 01-03-* | 01 | 1 | STATE-01 | T-01-04 no DB | Single JSON file, no DB | structural | `! grep -rn "sqlite\|mongo\|redis" --include='*.go' .` | ❌ W0 | ⬜ pending |
| 01-04-* | 01 | 1 | STATE-03 | — | `version: 1` schema; boot reads JSON | unit | `go test ./internal/state/... -run TestLoadAndPersist` | ❌ W0 | ⬜ pending |
| 01-05-* | 01 | 2 | FOUND-03 | T-01-01 / T-01-02 | `/healthz` 200, `/api/state` valid JSON, no path echoes in errors | unit + smoke | `go test ./internal/api/...` + `cd e2e && npx playwright test smoke.spec.ts` | ❌ W0 | ⬜ pending |
| 01-06-* | 01 | 2 | FOUND-04 | T-01-03 path traversal | Embedded shell + correct MIME, strict `/assets/*` | smoke | `cd e2e && npx playwright test smoke.spec.ts` (asserts content-type + 404 on traversal) | ❌ W0 | ⬜ pending |
| 01-07-* | 01 | 3 | FOUND-05 | — | Test stack reaches healthy via `--wait` | infra | `cd e2e && docker compose -f compose.test.yml up -d --wait` succeeds within 90 s | ❌ W0 (`compose.test.yml`, `zot-config.json`) | ⬜ pending |
| 01-08-* | 01 | 3 | FOUND-06 | — | globalSetup brings stack up and polls `/healthz`; first smoke green | smoke | `cd e2e && npx playwright test` | ❌ W0 | ⬜ pending |
| 01-09-* | 01 | 3 | FOUND-07 | — | `oras push` flips `:latest` mid-test fixture | manual + infra | `oras push --plain-http localhost:5000/centroid-is/stub:latest …` succeeds; helper `pushFreshManifest()` exercises it once in globalSetup | ❌ W0 (`e2e/fixtures/push-image.ts`) | ⬜ pending |
| 01-10-* | 01 | 4 | FOUND-08 | — | `tygo generate` matches checked-in types; CI fails on diff | unit | `make check-types` (≡ `make types && git diff --exit-code ui/src/lib/types.d.ts`) | ❌ W0 (`tygo.yaml`) | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/state/persist_test.go` — stubs for FOUND-02, STATE-02, STATE-03 (atomic write under contention; load+persist round trip; `version: 1` schema validation)
- [ ] `internal/api/server_test.go` — light unit coverage for `/healthz` and `/api/state` handlers (fast, no Playwright)
- [ ] `e2e/tests/smoke.spec.ts` — Phase 1 smoke (FOUND-03 / FOUND-04 / FOUND-05 / FOUND-06)
- [ ] `e2e/playwright.config.ts`, `e2e/global-setup.ts`, `e2e/global-teardown.ts` — Playwright infra (FOUND-06)
- [ ] `e2e/compose.test.yml`, `e2e/zot-config.json` — test stack (FOUND-05)
- [ ] `e2e/fixtures/push-image.ts` — oras helper (FOUND-07)
- [ ] `tygo.yaml`, `internal/api/types.go`, `Makefile` targets `types` and `check-types` — type contract (FOUND-08)
- [ ] `.github/workflows/ci.yml` — CI minimal pipeline (lint + unit + tygo diff + e2e)

*All of the above are greenfield — no existing infrastructure exists.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Manual smoke on HMI-like stack | C4 / Phase gate | Per-brief "done" requires real-stack observation, not just CI green | `docker compose -f e2e/compose.test.yml up -d --wait` on a clean Debian 12 VM (or local Docker) → open `http://localhost:8080/` in a browser, confirm empty 7-column table renders and `/healthz` returns 200 |
| `tygo` install verification | FOUND-08 | Tool install is operator-side, not testable in CI's bootstrap | `go install github.com/gzuidhof/tygo@latest` then `tygo generate` succeeds (CI workflow installs it; local dev install documented in README) |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies declared
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (every test/fixture/config file is a Wave 0 deliverable)
- [ ] No watch-mode flags (`--watch`) anywhere in test commands
- [ ] Feedback latency < 30 s per-task / 120 s per-wave
- [ ] `nyquist_compliant: true` set in frontmatter after planner completes Wave-0 task list

**Approval:** pending (planner finalizes Wave-0 task list)
