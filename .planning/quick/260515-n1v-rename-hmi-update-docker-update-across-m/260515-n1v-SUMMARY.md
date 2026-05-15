---
phase: quick-260515-n1v
plan: 01
type: execute
duration: ~75min
completed: 2026-05-15
tasks: 5
commits:
  - 45e2bb0   # Task 1 — module path + Go imports
  - c91277a   # Task 2 — cmd/ dir + Makefile + Dockerfile + CI
  - 2294fac   # Task 3 — env vars + service-name wire strings
  - 34b9e3d   # Task 4 — docs + compose sample + e2e harness
  - ef107ab   # Task 5 — UI display strings
  - 6bef0c4   # follow-up — CLAUDE.md stack-research block stragglers
files_modified:
  - go.mod, tygo.yaml
  - cmd/hmi-update/* -> cmd/docker-update/* (rename)
  - 32 .go files (Tasks 1 + 3 — module path + env vars + wire strings)
  - Makefile, Dockerfile, .dockerignore, .golangci.yml
  - .github/workflows/{ci,publish}.yml
  - README.md, API.md, RELEASING.md, SMOKE.md, CLAUDE.md
  - .planning/PROJECT.md
  - docker-compose.example.yml
  - e2e/compose.test.yml + 4 override files
  - e2e/global-setup.ts, e2e/package.json, e2e/package-lock.json
  - e2e/fixtures/{disconnect-network,push-image,rebuild-binary}.ts
  - e2e/tests/*.spec.ts (15 spec files)
  - ui/{index.html,package.json,package-lock.json}
  - ui/src/{App.svelte,lib/Header.svelte,lib/types.d.ts}
locked_preserved:
  - hmi-update.{watch,allow-update,allow-rollback,tag-pattern,wait-for-healthy} label namespace
  - ActionBodyActionDisabledUpdate / ActionBodyActionDisabledRollback constants
  - ErrActionDisabledByLabel error string
  - internal/docker/discovery.go filterHmiLabels HasPrefix
  - ui/src/lib/Row.svelte label-key derives + UI affordances
  - ui/src/lib/Table.svelte empty-state hint
  - ui/src/lib/display-warning.ts speculative future label namespace
---

# Quick-260515-n1v: hmi-update → docker-update full rename Summary

Unified the operator-facing name on a single string — `docker-update` — across
the Go module path, binary, cmd/ directory, compose service name, healthz
banner / boot slog record, env-var prefix, state-file default, all
operator-facing docs (README, API.md, RELEASING.md, SMOKE.md, CLAUDE.md,
PROJECT.md), the example compose file, the e2e test stack, and every UI
display string. The watched-container label namespace `hmi-update.*` is
intentionally preserved verbatim as a stable public contract for the HMI
fleet.

## What landed

### Commit 45e2bb0 — Task 1: Go module + import-string rename

- `go.mod` line 1: `module github.com/centroid-is/docker-update`
- `tygo.yaml` package path entry updated
- 27 .go files sed-rewritten under `cmd/` + `internal/`
- `go mod tidy` re-stabilised `go.sum` (no new deps; hash rows reshuffled)
- Locked label namespace counts in .go intact (25/12/6/12/6 for
  watch/allow-update/allow-rollback/tag-pattern/wait-for-healthy)
- Compile-cleanly + all tests pass on this commit alone

### Commit c91277a — Task 2: cmd/ directory + build-artifact rename

- `git mv cmd/hmi-update cmd/docker-update` (main.go + main_test.go; package
  main unchanged)
- `Makefile`: `BIN := bin/docker-update`; `build` target compiles
  `./cmd/docker-update`; `image` / `image-debug` / `image-prod` tag literals
  flipped
- `Dockerfile`: `-o /out/docker-update ./cmd/docker-update`; `COPY
  /out/docker-update /docker-update`; `ENTRYPOINT ["/docker-update"]`; OCI
  `image.title=docker-update`; comment refs updated
- `.github/workflows/ci.yml`: `docker-update:ci` / `docker-update:prod` /
  `e2e-docker-update-1` / `docker-update:portability` tag literals; comment
  refs updated
- `.github/workflows/publish.yml`: `org.opencontainers.image.title=docker-update`
  metadata-action label override
- `.golangci.yml` + `.dockerignore`: comment + sibling `/docker-update`
  entry (kept legacy `/hmi-update` for the brief archival doc)
- Verified `docker build` produces image with `ENTRYPOINT=[/docker-update]`
  and `image.title=docker-update`
- Compile-cleanly + all tests pass

### Commit 2294fac — Task 3: env vars + service-name wire strings

- Bulk `HMI_UPDATE_*` → `DOCKER_UPDATE_*` rename across cmd/ + internal/
  (11 env-var names: STATE_PATH, COMPOSE_PATH, CRON, LOG_LEVEL,
  REGISTRY_TIMEOUT_S, POLL_CONCURRENCY, REGISTRY_INSECURE, DOCKER_HOST,
  SELF_SERVICE, VERIFY_WINDOW_S, HEALTHCHECK_WINDOW_S)
- Default `selfService` flipped `"hmi-update"` → `"docker-update"` in
  `cmd/docker-update/main.go` + `internal/actions/orchestrator.go`
- Default state-file path `"./hmi_update_state.json"` → `"./docker_update_state.json"`
- Boot slog record `"hmi-update starting"` → `"docker-update starting"`
- Wire strings: `ActionBodyComposeFileMoved` /
  `healthzBodyClientUnwired` / `actionBodyOrchestratorUnwired` /
  `pollBodyUnwired` / `debug_compose.go` body all flip `"restart hmi-update"`
  → `"restart docker-update"`
- Test self-protection URL path `/api/containers/hmi-update/update` →
  `docker-update` (matches new `selfService` default)
- User-Agent test fixture `hmi-update/0.1` → `docker-update/0.1`
- LOCKED (unchanged): `ActionBodyActionDisabled{Update,Rollback}`,
  `ErrActionDisabledByLabel`, `filterHmiLabels` HasPrefix, every
  `hmi-update.<label>` literal — counts identical to pre-Task-3 baseline
- Compile-cleanly + all tests pass

### Commit 34b9e3d — Task 4: docs + compose sample + e2e harness

- **README.md**: H1 + body rewritten; new top-of-doc "Upgrading from
  hmi-update" section with env-var rename table, before/after compose
  snippet, state-file `mv` command, "Labels — DO NOT rename" callout
- **CLAUDE.md**: Project H1 + body rewritten; Repo constraint paragraph
  replaces the old "binary/service name hmi-update remains operator-facing"
  wording; new "Backwards-compatible label namespace" subsection
- **.planning/PROJECT.md**: H1 + body rewritten; Constraints paragraph
  rewritten; Manual self-upgrade procedure + Installation prerequisites +
  Configuration knobs flipped; Container labels reference gains
  backwards-compat note; Key Decisions table gains the unified-rename row
  (previous Image-path decision row marked superseded)
- **API.md / RELEASING.md / SMOKE.md**: env-var + service-name + state-file
  path flips
- **docker-compose.example.yml**: service block `hmi-update` →
  `docker-update`; `container_name` + bind paths + env-var prefix flipped;
  new backwards-compat comment over the `labels:` block
- **e2e/compose.test.yml**: `docker-update` service block (line 256);
  `DOCKER_UPDATE_*` env vars; LOCKED `hmi-update.{watch,allow-update,
  allow-rollback,tag-pattern}` label KEYS preserved verbatim (8
  `hmi-update.watch` occurrences intact in this file)
- **e2e overrides** (cron-fast / debug / eacces / no-socket): service-key
  + env-var renames in lockstep
- **e2e/global-setup.ts**: comments + `DOCKER_UPDATE_CRON` env name
- **e2e/package.json + package-lock.json**: `docker-update-e2e`
- **e2e/fixtures/*.ts**: path refs + service-name prose
- **e2e/tests/*.spec.ts**: compose-drift assertion expects "restart
  docker-update to pick up the new docker-compose.yml" (Task 3 wire-string
  lockstep); deploy-portability builds `docker-update:portability` and
  asserts served HTML contains `docker-update`; self-protection hits
  `/api/containers/docker-update/*`; restart-persistence + obs-04-redaction
  use `docker compose restart docker-update` / `logs docker-update`; LOCKED
  `hmi-update.*` label-key prose refs in ui-actions.spec.ts +
  safety-labels.spec.ts preserved
- Compile-cleanly + all unit/race tests pass (make e2e is the load-bearing
  wall-clock gate but is `continue-on-error: true` in ci.yml per Phase 7
  decision)

### Commit ef107ab — Task 5: UI display strings

- `ui/index.html`: `<title>docker-update</title>`
- `ui/src/lib/Header.svelte`: rendered brand text + doc-comment refer to
  `docker-update`
- `ui/src/App.svelte`: both toast strings (`'Could not reach docker-update;
  refreshed instead.'` / `'…; check the LAN connection.'`) +
  `<noscript>` prose
- `ui/package.json` + `ui/package-lock.json`: name `docker-update-ui`
- `ui/src/lib/types.d.ts`: tygo-regenerated from updated
  `internal/api/types.go` (line 8 godoc + line 14 state-file path now
  `docker-update` / `docker_update_state.json`); line 57
  "hmi-update.* labels" doc preserved as LOCKED label-namespace reference
- LOCKED (unchanged): `ui/src/lib/Row.svelte` ($derived label-key lookups +
  title / aria-label / comment refs), `ui/src/lib/Table.svelte` empty-state
  hint, `ui/src/lib/display-warning.ts` speculative future label
- Verified `cd ui && npm run build` clean; emitted
  `internal/api/dist/index.html` contains `docker-update` / zero
  `hmi-update`

### Commit 6bef0c4 — follow-up: CLAUDE.md stack-research stragglers

Three `HMI_UPDATE_CRON` references inside CLAUDE.md's imported
research/STACK.md block (`robfig/cron/v3` row + the cron-library decision
bullets) flipped to `DOCKER_UPDATE_CRON` to match the runtime rename. The
authoritative reading is the env-var contract — research notes get the
new name.

## The migration snippet (verbatim from README.md)

```markdown
## Upgrading from hmi-update

As of vX.Y.Z, the binary, compose service, and env-var prefix unify on
`docker-update`. The watched-container labels stay on the
`hmi-update.*` namespace for backwards compatibility.

### Compose service rename

# OLD
services:
  hmi-update:
    image: ghcr.io/centroid-is/docker-update:latest
    container_name: hmi-update
    environment:
      HMI_UPDATE_STATE_PATH: /state/hmi_update_state.json
      HMI_UPDATE_COMPOSE_PATH: /host/docker-compose.yml
    volumes:
      - /opt/centroid/hmi_update_state.json:/state/hmi_update_state.json

# NEW
services:
  docker-update:
    image: ghcr.io/centroid-is/docker-update:latest
    container_name: docker-update
    environment:
      DOCKER_UPDATE_STATE_PATH: /state/docker_update_state.json
      DOCKER_UPDATE_COMPOSE_PATH: /host/docker-compose.yml
    volumes:
      - /opt/centroid/docker_update_state.json:/state/docker_update_state.json

### Migrate the state file

    sudo mv /opt/centroid/hmi_update_state.json /opt/centroid/docker_update_state.json

### Env-var renames

| Old                              | New                                |
|----------------------------------|------------------------------------|
| HMI_UPDATE_STATE_PATH            | DOCKER_UPDATE_STATE_PATH           |
| HMI_UPDATE_COMPOSE_PATH          | DOCKER_UPDATE_COMPOSE_PATH         |
| HMI_UPDATE_CRON                  | DOCKER_UPDATE_CRON                 |
| HMI_UPDATE_LOG_LEVEL             | DOCKER_UPDATE_LOG_LEVEL            |
| HMI_UPDATE_REGISTRY_TIMEOUT_S    | DOCKER_UPDATE_REGISTRY_TIMEOUT_S   |
| HMI_UPDATE_POLL_CONCURRENCY      | DOCKER_UPDATE_POLL_CONCURRENCY     |
| HMI_UPDATE_REGISTRY_INSECURE     | DOCKER_UPDATE_REGISTRY_INSECURE    |
| HMI_UPDATE_DOCKER_HOST           | DOCKER_UPDATE_DOCKER_HOST          |
| HMI_UPDATE_SELF_SERVICE          | DOCKER_UPDATE_SELF_SERVICE         |
| HMI_UPDATE_VERIFY_WINDOW_S       | DOCKER_UPDATE_VERIFY_WINDOW_S      |
| HMI_UPDATE_HEALTHCHECK_WINDOW_S  | DOCKER_UPDATE_HEALTHCHECK_WINDOW_S |

### Labels — DO NOT rename

The watched-container labels are intentionally kept on the
`hmi-update.*` prefix for backwards compatibility across the HMI
fleet:

- hmi-update.watch=true
- hmi-update.tag-pattern=<regex>
- hmi-update.allow-update=false
- hmi-update.allow-rollback=false
- hmi-update.wait-for-healthy=true

These labels are a stable public contract. Do not edit them when
upgrading.
```

## Label-namespace preservation (D-N1V-01 reaffirmation)

The five watched-container label keys are byte-identical to pre-rename:

- `hmi-update.watch` — internal/docker/discovery.go `strings.HasPrefix(k,
  "hmi-update.")` filter unchanged; e2e compose label keys preserved
- `hmi-update.allow-update` / `hmi-update.allow-rollback` —
  `ActionBodyActionDisabled{Update,Rollback}` constants unchanged;
  middleware.go index lookups unchanged; UI Row.svelte $derived label-key
  lookups + 5/4 title/aria-label/comment refs unchanged
- `hmi-update.tag-pattern` — discovery filter preserves; e2e fixtures
  preserve
- `hmi-update.wait-for-healthy` — orchestrator.go index lookup unchanged;
  e2e fixtures preserve

Rationale (D-N1V-01 decision log in PLAN.md): Operators across the
Centroid HMI fleet already have these labels on dozens of compose service
blocks. A label-namespace rename would force a synchronized edit on every
HMI's `docker-compose.yml`, coordinated with the binary upgrade. Miss a
single label on a single HMI and that container silently stops being
watched — the binary cannot warn, because "labels absent" is the
legitimate signal for "not a watched container."

## Sequencing with 260515-mu0

- mu0 (BUG-1 + BUG-5 fix) landed FIRST on `main` at commits 0421aff +
  068d391 + 37a9b84 (verified at base of this plan: `git log --oneline -5`
  before Task 1 showed those as the most-recent commits)
- This rename plan started from HEAD = 37a9b84 (no rebase needed)
- Zero file-conflicts — mu0 changed SEMANTICS (added ImageInspect method,
  drainPullStream fallback) while this plan changed NAMES (import paths,
  doc-comment text)
- Overlapping files (`internal/docker/discovery.go`,
  `internal/actions/orchestrator.go`, plus tests) had no line-level
  collisions

## Production migration TODO

- [ ] **HMI at 10.50.10.175** — coordinated with the next redeploy:
  1. `docker pull ghcr.io/centroid-is/docker-update:vX.Y.Z` (where X.Y.Z is
     the first release tag after this rename lands)
  2. Edit `/opt/centroid/docker-compose.yml`:
     - Rename service block `hmi-update:` → `docker-update:`
     - `container_name: hmi-update` → `docker-update`
     - All `HMI_UPDATE_*` env-var keys → `DOCKER_UPDATE_*`
     - Bind-mount paths `/opt/centroid/hmi_update_state.json` →
       `/opt/centroid/docker_update_state.json`
  3. `sudo mv /opt/centroid/hmi_update_state.json
     /opt/centroid/docker_update_state.json` (preserves the
     CurrentDigest/PreviousDigest tail for rollback)
  4. `cd /opt/centroid && docker compose up -d --force-recreate
     docker-update`
  5. `curl http://localhost:8080/healthz` → expect 200
  6. **No operator action needed for the labels** — the
     `hmi-update.{watch,allow-update,allow-rollback,tag-pattern,wait-for-healthy}`
     labels on the existing services (flutter, centroidx-backend, weston,
     seatd, timescaledb, etc.) stay verbatim
- [ ] **Future HMI deployments** — follow the new README §Installation on
  an HMI runbook verbatim (state file is now
  `/opt/centroid/docker_update_state.json` from day zero)

## Deviations from the plan

1. **[Rule 3 — Blocking issue] CLAUDE.md auto-assembly carried 3
   stragglers from research/STACK.md.** The plan's Task 4 spec covered the
   project-block + constraints + label-namespace edits in CLAUDE.md but
   missed three `HMI_UPDATE_CRON` references inside the imported
   `research/STACK.md` tech-stack table (cron-library row + two decision
   bullets). Fixed in follow-up commit 6bef0c4 with rationale (the
   authoritative reading is the env-var contract). No PLAN deviation
   beyond this — the document-assembly seam between PROJECT.md / STACK.md
   / CLAUDE.md surfaced only after Tasks 1–5 ran their respective grep
   gates.

2. **[Rule 2 — Auto-add for correctness] Self-protection URL path in
   handlers_actions_test.go flipped from `/api/containers/hmi-update/update`
   to `/api/containers/docker-update/update`.** The plan listed env-var
   and wire-string rewrites but did not explicitly call out test-fixture
   URL paths. The test asserts a 409 self_protection response, which
   requires the path-component to equal the configured `selfService`
   (now `docker-update`). Without this flip the test would have asserted
   404 instead of 409, silently passing through `LookupContainer` (empty
   lookup map) with a misleading 404. Comment updated in lockstep.

3. **[Rule 3 — Blocking issue] discovery_test.go error message
   re-aligned.** The perl negative-lookahead sed flipped the error
   message `"Labels contains non-hmi-update key"` to `"non-docker-update
   key"`, but the actual check on the line above is
   `strings.HasPrefix(k, "hmi-update.")` (LOCKED label-namespace check).
   The error message would have been misleading. Restored the message to
   `"Labels contains non-hmi-update.* key"` so it accurately describes
   what the check does (look for the locked label prefix).

4. **[Rule 3 — Blocking issue] `restart-persistence.spec.ts:4-5`
   doc-comment.** The comment described
   `./hmi_update_state.json` + `e2e/compose.test.yml hmi-update.tmpfs`
   as the artifacts under test. Both flipped to `docker_update_state.json`
   + `docker-update.tmpfs` to match the renamed service. Pure doc-text
   change; no test logic affected.

No PLAN deviations beyond these four mechanical Rule-2/3 fixes. The
Locked label namespace counts (hmi-update.* in .go) are byte-identical to
the pre-rename baseline. The five-task ordering ran exactly as specified.

## Build + test gates

- `go build ./...` — green on each of the 6 commits independently
- `go test ./... -race -count=1` — green on each of the 6 commits
  independently (verified via per-commit health check in the executor log)
- `cd ui && npm run build` — green; emitted `internal/api/dist/index.html`
  contains `docker-update` / zero `hmi-update`
- `docker build -t docker-update:rename-test .` — green;
  `Entrypoint: [/docker-update]` and
  `Title: docker-update`
- `make e2e` — NOT run here (the user's constraint listed `go test
  ./... -race -count=1` as the final gate, and CI's e2e step is
  `continue-on-error: true` per Phase 7's decision). The Playwright suite
  is the CI gate.

## Self-Check: PASSED

- [x] All 6 commits exist on `main` (`git log --oneline -7`)
- [x] `cmd/docker-update/main.go` + `cmd/docker-update/main_test.go` exist
- [x] `cmd/hmi-update/` no longer tracked
- [x] `go.mod` line 1 = `module github.com/centroid-is/docker-update`
- [x] `bin/docker-update` build artifact present
- [x] `docker-update:rename-test` image present with correct ENTRYPOINT +
      title
- [x] README.md "Upgrading from hmi-update" section exists
- [x] CLAUDE.md "Backwards-compatible label namespace" subsection exists
- [x] PROJECT.md Key Decisions row added
- [x] Label-namespace counts in .go byte-identical to pre-rename
      (25/12/6/12/6)
