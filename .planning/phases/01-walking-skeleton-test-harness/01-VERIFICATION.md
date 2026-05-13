---
phase: 01
slug: walking-skeleton-test-harness
status: human_needed
created: 2026-05-13
verifier: orchestrator (inline)
---

# Phase 01 — Verification: Walking Skeleton & Test Harness

## Status: `human_needed`

All **automated** acceptance criteria pass. One **manual-smoke gate** remains per the brief's C4 constraint ("done requires real-stack observation, not just CI green"). This is Plan 01-04 Task 4, intentionally not executed by the executor.

## Goal-Backward Verification

**Phase goal:** Produce the minimum end-to-end test harness that lets a Playwright test drive a real binary inside a real docker compose stack and assert on `/api/state` — so every later phase's red test is meaningful.

**Did we achieve it? YES — pending manual smoke.** The Playwright smoke test went from RED (intentionally failing in Wave 1) → GREEN (asserting the seven-column table, `/healthz`, `/api/state` after Wave 3). Plans 02 (state) and 03 (UI/tygo) drove their intermediate gates green. Plan 04 wired the HTTP server with embedded UI, the test compose stack with `project-zot/zot` + stub watched container, Playwright `globalSetup` driving `docker compose up -d --wait`, the manifest-push fixture, the multi-stage distroless Dockerfile, and the CI pipeline.

## Must-Haves

| # | Must-Have | Status | Evidence |
|---|-----------|--------|----------|
| 1 | Playwright smoke green in CI (red-first written before implementation) | ✅ green | `cd e2e && npx playwright test --grep smoke` → 1 passed in 7.9s (commit `d58a21b`); RED state earlier confirmed in `628224b` |
| 2 | `make e2e` brings up the test stack via `--wait` | ✅ green | `docker compose -f e2e/compose.test.yml config` validates; `up -d --wait` reaches healthy in globalSetup |
| 3 | `make check-types` (tygo) fails CI on diff | ✅ green | `make check-types` exit 0 (commit `4558e25`); drift detection live |
| 4 | SIGKILL-mid-write leaves state file parseable | ✅ green (proxy) | `TestPersistAtomicity` + `TestCorruptedFile` exit 0 under `-race -count=10` (commit `e08714d`); SIGKILL semantics are exercised by the atomic temp+rename+dirsync wrapper |
| 5 | renameio dir-fsync correction (RESEARCH.md A5) implemented | ✅ green | `grep "filepath.Dir\|\.Sync()" internal/state/persist.go` matches; rationale comment cites issue #11 and Pitfall 7 |
| 6 | Manual smoke on HMI-like stack — `/healthz` 200, empty 7-col table at `/` | ⏳ pending operator | See "Operator checklist" below |

## Automated Verification Commands (all green)

| Command | Result |
|---------|--------|
| `go vet ./...` | exit 0 |
| `go test ./... -race` | exit 0 (state + api unit suites) |
| `go test ./internal/state/... -run TestPersist -count=10` | exit 0 (atomic write under contention) |
| `make check-types` | exit 0 (tygo drift detection live) |
| `docker compose -f e2e/compose.test.yml config` | exit 0 |
| `docker build .` | exit 0 (distroless image; runs as UID 65532) |
| `cd e2e && npx playwright test --grep smoke` | 1 passed (7.9s) |
| `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))"` | exit 0 |

## Requirements Coverage

All 11 Phase 1 requirements complete via automated tests:

| Req | Plan | Status |
|-----|------|--------|
| FOUND-01 (repo scaffolding) | 01-01 | ✅ |
| FOUND-02 (atomic state) | 01-02 | ✅ |
| FOUND-03 (HTTP server `/healthz` + `/api/state`) | 01-04 | ✅ |
| FOUND-04 (Svelte shell embedded + strict /assets/* + MIME) | 01-03, 01-04 | ✅ |
| FOUND-05 (compose.test.yml + zot + stub via `--wait`) | 01-04 | ✅ |
| FOUND-06 (Playwright globalSetup + smoke) | 01-04 | ✅ |
| FOUND-07 (manifest-push fixture via oras) | 01-04 | ✅ |
| FOUND-08 (tygo Go→TS + check-types) | 01-03 | ✅ |
| STATE-01 (single JSON file, no DB) | 01-02 | ✅ |
| STATE-02 (atomic writes via renameio/v2) | 01-02 | ✅ |
| STATE-03 (`version: 1` schema; resumes from JSON) | 01-02 | ✅ |

## Operator Checklist — Manual Smoke (Task 4 of Plan 01-04)

Per C4. Run on a clean environment that mirrors the HMI:

1. **Bring stack up:** From `/Users/jonb/Projects/tmp`, run `make clean && make e2e`. Expect "1 passed" within 60–120 s.
2. **Browser smoke** at `http://localhost:8080/` after `docker compose -f e2e/compose.test.yml up -d --wait`:
   - Header reads `hmi-update` (24 px, weight 600)
   - Background is light grey (zinc-50)
   - Table has exactly seven column headers in order: `container | image:tag | current digest | available digest | previous digest | status | actions`
   - Empty-state row heading: `No watched containers yet`; body matches UI-SPEC verbatim
   - At 1024 px viewport: no horizontal scrollbar
   - DevTools console: no errors
3. **API sanity:**
   - `curl http://localhost:8080/healthz` → `200` + `{"status":"ok"}`
   - `curl http://localhost:8080/api/state` → `200` + `{"version":1,"containers":{...}}`
4. **Cache header sanity (DevTools Network):**
   - `GET /` → `Cache-Control: no-cache`
   - `GET /assets/*.js` → `Cache-Control: public, max-age=31536000, immutable`
   - `GET /assets/*.js` → `Content-Type: application/javascript; charset=utf-8`
5. **Tear down:** `docker compose -f e2e/compose.test.yml down -v --remove-orphans`
6. **State persistence cold-boot:** bring stack back up; `docker compose exec hmi-update cat /state/hmi_update_state.json` → `{"version":1,"containers":{}}`

## Awaiting Operator Approval

Mark this VERIFICATION as `passed` once steps 1–6 are confirmed green. Specifically flag if:
- 7-column table fails to render OR empty-state copy is paraphrased (UI-SPEC violation)
- `/healthz` returns anything other than 200 + JSON
- `/api/state` is malformed
- `/assets/*.js` serves with wrong Content-Type (Pitfall 8 regression)
- `/assets/<nonexistent>` returns `index.html` instead of 404 (SPA-fallback regression)
