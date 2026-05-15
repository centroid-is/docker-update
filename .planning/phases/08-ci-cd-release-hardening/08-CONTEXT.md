# Phase 8: CI/CD & Release Hardening - Context

**Gathered:** 2026-05-15
**Status:** Ready for planning
**Mode:** Locked design — decisions encoded directly from the parent brief

<domain>
## Phase Boundary

Phase 8 ships the green-CI-and-manual-smoke release gate. After Phase 7 ships the production
Dockerfile, the compose deployment block, the install runbook and the <30 MB image/RAM budget
verification, Phase 8 wraps the binary in a GitHub Actions pipeline that turns "green commit on
main" into "image at `ghcr.io/centroid-is/hmi-update:<three-tags>` that an HMI operator can pull
and run."

This phase delivers:

1. **Main CI workflow** (`.github/workflows/ci.yml` — rewritten on top of Phase 1's baseline).
   Stages run sequentially; any failure stops the pipeline. Triggers: `pull_request` (lint + unit
   + tygo + frontend + docker-build + e2e — NO publish) and `push: branches: [main]` (full chain
   into the publish job).
2. **Publish workflow** (`.github/workflows/publish.yml`). Runs after `ci.yml` passes on `main` or
   on a Git tag push matching `v[0-9]+.[0-9]+.[0-9]+`. Uses `docker/metadata-action@v5` to emit
   the three canonical tags (`:latest`, `:vX.Y.Z`, `:sha-<short>`), `docker/login-action@v3` for
   GHCR auth (no secret needed for the `centroid-is/hmi-update` org repo — `GITHUB_TOKEN` is
   enough), and `docker/build-push-action@v6` for the actual build+push. Runs the real-GHCR
   anonymous-token-flow smoke job afterward.
3. **Real-GHCR anonymous smoke job** — `crane digest <frozen-public-image>` with NO
   `GITHUB_TOKEN`/credentials/secrets in the job environment. On PRs (no publish) the smoke
   targets a stable public anchor (`ghcr.io/distroless/static-debian12:nonroot`). After a publish
   on `main`, the smoke ALSO probes the just-pushed `ghcr.io/centroid-is/hmi-update:latest`
   anonymously to prove the published image is publicly readable through the same anonymous
   token flow.
4. **`RELEASING.md`** at repo root — operator-readable runbook documenting the manual-smoke gate
   (C4: a release is tagged only after green CI + a recorded smoke note on an HMI-like stack).
   Cross-linked from PROJECT.md.

The phase exists because the brief's §C4 process constraint ("TDD: verify → implement → verify
→ implement … manual smoke on HMI-like stack is required before 'done'") only holds at the
project level if CI itself enforces the test order and the release process documents the
manual-smoke step. Without this phase, Phase 3's Pitfall 2 unit-test regression guard catches
the bug at code-review time but not at publish-time; without this phase, an operator can hand-tag
a release that hasn't been touched on a real HMI.

Out of scope for this phase: V2-ARM64 buildx flip (deferred per CLAUDE.md "amd64 only for v1");
private-registry credentials (V2-PRIV-REG); SBOM / supply-chain signing (out of scope for v1
per brief §F4 / N5); GitHub Releases automation beyond a documented manual-tag step (Phase 8.5
candidate if the team wants it later); CodeQL / dependabot beyond what GitHub Actions provides
out of the box.

</domain>

<decisions>
## Implementation Decisions (locked)

### Area 1 — Pipeline shape: sequential gates, fail-fast

The pipeline runs as a **single linear DAG** with one optional parallel split (`go` and `ui`
prerequisite jobs both feed into `e2e`, mirroring Phase 1's baseline). Stage order (any failure
stops everything downstream):

1. `actions/checkout@v4`
2. `actions/setup-go@v5` with `go-version: '1.26'`
3. `actions/setup-node@v4` with `node-version: '22'`
4. **Lint:** `go vet ./...` + `golangci-lint run` (separate run-step in the `lint` job;
   `golangci/golangci-lint-action@v6` for the cache + version pin — `version: v1.62.x`)
5. **Tygo diff:** `make check-types` (Phase 1 plan 01-03 wired this; CI re-asserts)
6. **Unit tests:** `go test ./... -race` (race detector required; Phase 4's STATE-04 SIGKILL
   suite stays opt-in via `make test-sigkill` and is NOT run in main CI — too slow, OS-coupled,
   already covered by the unit gate it guards)
7. **Frontend build:** `npm --prefix ui ci && npm --prefix ui run build`
8. **Docker build:** multi-stage build per Phase 7's Dockerfile. Tags the image with the three
   canonical tags via `docker/metadata-action@v5`. The build job ALSO runs the Phase 7 image-size
   gate: `docker image inspect --format '{{.Size}}' hmi-update:ci | awk '$1 > 30000000 {exit 1}'`
   asserts <30,000,000 bytes (idle-RAM measurement happens later in the e2e job via
   `docker stats --no-stream`)
9. **Playwright e2e:** `make e2e-cron-fast` against the just-built image. The `e2e-cron-fast`
   target sets `HMI_UPDATE_CRON="@every 5s"` so the Phase 3 detect-* specs flip within ~10 s per
   assertion, keeping CI wall-clock to ~3–4 min for the full suite. Phase 4's e2e specs (verify-
   after-recreate, self-protection, double-click, restart-mid-flight) layer on top of the same
   target with no additional overrides. Phase 5's e2e specs (in-place upgrade, asset MIME) layer
   too. The Playwright report is uploaded as an artifact on failure (already in Phase 1's
   baseline; retain).
10. **Real-GHCR anonymous smoke** (always; both PR and push paths). Pinned `crane` via
    `go install github.com/google/go-containerregistry/cmd/crane@v0.20.8`. The job's `env:`
    block has NO `GITHUB_TOKEN`, NO `REGISTRY_USERNAME`, NO `REGISTRY_PASSWORD` — verified by
    `env | grep -E '(GITHUB_TOKEN|REGISTRY_)'` returning empty in the smoke step. The job
    `permissions:` block is `contents: read` only (no `packages: read` — anonymous flow must NOT
    fall back to authenticated). On PR: probe `ghcr.io/distroless/static-debian12:nonroot`. On
    main push + tag push: probe both the static-debian12 anchor AND
    `ghcr.io/centroid-is/hmi-update:<tag-just-pushed>` (the publish job emits the tag as a step
    output that this job consumes).
11. **Publish** (NEVER on PR — `if: github.event_name != 'pull_request' && success()` gate).
    `docker/login-action@v3` to `ghcr.io` using `${{ secrets.GITHUB_TOKEN }}` + `${{ github.actor }}`.
    `docker/build-push-action@v6` with `push: true` and `tags: ${{ steps.meta.outputs.tags }}`
    from `docker/metadata-action@v5`. Cache via `cache-from: type=gha` + `cache-to: type=gha,mode=max`
    (GitHub Actions cache backend — keeps multi-stage Dockerfile builds fast across runs).

### Area 2 — Three-tag publishing convention

`docker/metadata-action@v5` configured exactly as:

```yaml
- id: meta
  uses: docker/metadata-action@v5
  with:
    images: ghcr.io/centroid-is/hmi-update
    tags: |
      type=raw,value=latest,enable={{is_default_branch}}
      type=semver,pattern={{version}}
      type=sha,prefix=sha-,format=short
```

Resulting tag matrix:

| Trigger | Emitted tags |
|---------|--------------|
| `push: branches: [main]` (commit on main) | `:latest`, `:sha-<short>` |
| `push: tags: ['v*.*.*']` (Git semver tag) | `:vX.Y.Z`, `:latest` (if also on main), `:sha-<short>` |
| `pull_request` | (none — publish job is gated off) |
| Manual `workflow_dispatch` | `:sha-<short>` only (no `:latest` rewrite from a non-default branch) |

The semver guard MUST be `pattern={{version}}` (not `pattern={{raw}}`), so a tag like
`v1.2.3-rc1` strips to `1.2.3-rc1` for the image tag — matching the brief's "vX.Y.Z per release"
contract while supporting pre-release semver. `:sha-<short>` uses `format=short` (7-char SHA)
to match the brief's literal `:sha-<short>` shape.

### Area 3 — Real-GHCR anonymous smoke job (Pitfall 2 belt-and-braces)

The smoke runs `crane digest <ref>` and asserts exit 0. The job's invariants:

- **No credentials in the environment.** `permissions: contents: read` only; no `secrets:`
  passed to the step. Verified by the smoke step itself running
  `[ -z "$GITHUB_TOKEN" ] || (echo "FAIL: anonymous smoke saw GITHUB_TOKEN" && exit 1)` BEFORE
  the `crane digest` call. This guarantees the test exercises the anonymous bearer-token flow,
  not the authenticated one — which is exactly where Pitfall 2's `Authorization: Basic Og==`
  regression would hide.
- **Frozen anchor for PR runs:** `ghcr.io/distroless/static-debian12:nonroot`. This is a public
  image that GHCR serves anonymously today; if anonymous-token-flow breaks at the registry side,
  Phase 8 CI catches the regression on every PR before publish.
- **Live published image for main/tag runs:** in addition to the frozen anchor, the smoke
  probes `ghcr.io/centroid-is/hmi-update:<just-pushed-tag>` to prove the freshly-pushed image is
  publicly anonymous-readable. The tag is plumbed via a step output from the publish job.
- **Single call per anchor; no retry.** A flaky smoke would mask real regressions. If GHCR is
  having a bad day, the job fails loud and the operator re-runs the workflow.
- **Pinned `crane` version.** `go install github.com/google/go-containerregistry/cmd/crane@v0.20.8`.
  This matches Phase 3's `internal/registry` library pin so the smoke and the production code
  resolve manifests through the same `pkg/v1/remote` code path. A floating `@latest` would
  silently drift between phases.
- **Output:** the smoke job emits the resolved digest to the GitHub Actions step summary
  (`echo "frozen-anchor=$DIGEST" >> $GITHUB_STEP_SUMMARY`) so a maintainer reviewing a published
  release can see at a glance what digest the smoke asserted against.

The smoke exists at the CI surface even though it's a Phase 3 concern semantically. Phase 3
ships the unit-test regression guard (`internal/registry/transport_test.go` with `httptest`).
Phase 8 ships the LIVE-network counterpart that catches:

- GHCR upstream changing its `WWW-Authenticate` shape between runs.
- A future code change to `internal/registry` accidentally re-introducing
  `Authorization: Basic Og==` via a refactor (the unit test would catch it if the test was
  updated; the live smoke catches it even if the test was NOT updated, by virtue of using the
  real registry).
- A dependency bump in `google/go-containerregistry` regressing the bearer-token flow.

### Area 4 — Release process & manual-smoke gate (C4)

`RELEASING.md` at repo root contains:

1. **Pre-release checklist** (5 bullets): all v1 phase plans complete, green CI on `main`,
   `make test-sigkill` green locally (Phase 4 STATE-04 fault injection — not in CI), README
   reflects the current install runbook, no uncommitted local edits.
2. **Manual-smoke procedure** (numbered steps): identify the candidate `:sha-<short>` tag,
   `docker compose pull` on an HMI-like stack (or a clean Debian 12 box matching the brief),
   run through Phase 7's install runbook (Pitfall 9 `id -g docker` step included), perform
   one Update + one Rollback round-trip on a non-display-blackout service (per Pitfall 5
   guidance), confirm `/api/state` reflects the expected digest transitions, screenshot the
   UI for the release record.
3. **Tagging step:** `git tag -s vX.Y.Z -m "..."` + `git push origin vX.Y.Z`. The signed
   annotated tag triggers the publish workflow with the `vX.Y.Z` image tag emission.
4. **Manual-smoke record:** a SMOKE.md file (per release directory or a single repo-root
   ledger — Plan 08-03 picks the simpler shape) where the operator pastes the date, the
   `:sha-<short>` tested, the host OS+docker version, and a one-paragraph result note.
   This file is the C4 "recorded manual smoke note" referenced by Phase 8 success criterion 4.

`RELEASING.md` is cross-linked from PROJECT.md (Key Decisions / Release Process section).
Without that link the file is discoverable only through GitHub's filesystem browse — the
parent brief's "single-binary, single-source-of-truth" ethos applies to docs too.

### Area 5 — Concurrency & ref scoping

Both workflows declare:

```yaml
concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: true
```

Rationale: a fast operator pushing two commits to the same PR branch shouldn't pile up two
parallel CI runs both racing to bind ports 8080/5000 on the runner. Cancellation is safe — the
later commit's run will produce a green build on the latest tree, which is what matters for the
publish gate. The `main` branch concurrency group is keyed on `refs/heads/main` so a fast-merge
of two consecutive PRs cancels the first run's publish attempt and reruns from scratch on the
merged-up `main` tree (matches the brief's "linear release timeline").

### Area 6 — Job permissions (least-privilege)

The CI workflow's top-level `permissions:` block is `contents: read` only. Individual jobs
escalate when needed:

- `publish` job: `contents: read, packages: write` (write needed for `docker push` to GHCR).
- `ghcr-smoke` job: `contents: read` only (anonymous flow — explicit no-packages).
- All other jobs: inherit `contents: read`.

This matches GitHub's recommended hardening posture for org repos and prevents an accidental
`GITHUB_TOKEN` exposure in a job that doesn't need it.

### File Layout

- `.github/workflows/ci.yml` — REWRITE on top of Phase 1's baseline. Adds golangci-lint,
  image-size gate, e2e-cron-fast invocation, and the publish-prerequisite gate flag.
  (Plan 08-01)
- `.github/workflows/publish.yml` — NEW. Triggers off `workflow_run` from ci.yml on `main` or
  off `push: tags: ['v*.*.*']` directly. Uses metadata-action + login-action + build-push-action.
  Includes the real-GHCR smoke job after the push step. (Plan 08-02)
- `.github/workflows/ghcr-smoke.yml` — embedded as a job inside `publish.yml` rather than a
  standalone workflow, so the post-publish smoke runs only after a successful push (one fewer
  cross-workflow dependency). The PR-side smoke runs as a job in `ci.yml`. (Plans 08-01 +
  08-02)
- `RELEASING.md` — NEW at repo root. (Plan 08-03)
- `SMOKE.md` — NEW at repo root, manual-smoke ledger. (Plan 08-03)
- `PROJECT.md` — MODIFY to cross-link RELEASING.md. (Plan 08-03)
- `.github/dependabot.yml` — OUT OF SCOPE for v1 (no decision recorded yet on cadence).
  Document in `<deferred>`.
- `.golangci.yml` — NEW. Minimal config: enable `errcheck`, `govet`, `ineffassign`, `staticcheck`,
  `gosimple`, `unused` (the v1.62 default fastlinters set). Phase 8 ships the file so CI has
  a stable config to consume; if a team member later wants to disable a check, the file is the
  single point of edit. (Plan 08-01)

### Pipeline-build vs. published-image identity

The e2e job in `ci.yml` builds a local image tagged `hmi-update:ci` (Phase 7 Dockerfile, no
push). The publish job in `publish.yml` builds the same Dockerfile but pushes to GHCR with the
three canonical tags. To prevent drift between "what e2e tested" and "what we published," the
publish job re-uses the GitHub Actions build cache from the CI workflow (`cache-from: type=gha`)
so the layer hashes are byte-identical when the source tree is unchanged. The brief's success
criterion 5 (manual smoke pulls `:sha-<short>` and runs Phase 7's runbook) is the operator-level
defense against any residual drift; a planned future hardening (out of scope for v1) is to use
the OCI image digest from the CI build as the `tag` for the publish push, eliminating the
re-build step entirely.

### Configuration Knobs (introduced this phase)

- `HMI_UPDATE_CI_IMAGE_SIZE_BUDGET` — image-size budget in bytes for the CI gate. Default
  `30000000` (30 MB). Exposed as an env var so a future arm64 build that exceeds the budget by
  a few MB can be unblocked with a documented bump without editing the workflow.
- (Implicit) Phase 7 carries the Dockerfile-level idle-RAM budget; Phase 8 just measures it via
  `docker stats --no-stream` in the e2e job's tail step.

### Claude's Discretion

- Whether `ghcr-smoke` runs as a separate job in `ci.yml` (parallel with `e2e`) or as a tail
  step inside `e2e`. Lean separate job — keeps the smoke's "no credentials" invariant visible
  in the job-level `permissions:` block, which a `step` inside a credentialed `e2e` job can't
  cleanly assert.
- Whether `golangci-lint-action` pins to `v1.62.0` exactly or accepts the floor `v1.62.x`. Lean
  the floor — minor patches of golangci-lint are non-breaking and the action handles cache
  invalidation. A breaking minor (`v1.63`) would surface as a CI fail and trigger a deliberate
  bump.
- Whether the publish trigger is `workflow_run` from `ci.yml` (chains automatically) or a
  direct `push: branches: [main]` (re-runs the chain in `publish.yml`). Lean `workflow_run` —
  no duplicate lint/unit/e2e work; the publish only runs if the CI it observed is green. The
  one downside is `workflow_run` runs on the default branch of the repo, not the PR's branch
  — that's the correct behavior for a publish trigger (publishes are always from `main`).
- Whether the manual-smoke record (`SMOKE.md`) is a single repo-root file (append-only) or a
  per-release directory (`releases/vX.Y.Z/SMOKE.md`). Lean single file — simpler, matches the
  "no extra state stores" ethos; older entries are git-history-recoverable if a future audit
  needs them.
- Whether to include a `permissions: id-token: write` line on the publish job (OIDC for future
  cosign / sigstore). Lean NO — out of scope per Area 1; if signing lands in V2, that line is
  the right add. Today, omitting it preserves least-privilege.

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets

- `.github/workflows/ci.yml` (Phase 1) — three jobs (`go`, `ui`, `e2e`) with the baseline
  shape. Phase 8 rewrites this in place, KEEPING the job names so existing PR status checks
  don't break, and ADDING `lint`, `image-build`, `publish-prereq` jobs around them.
- `Makefile` (Phase 1 + Phase 3) — `make check-types`, `make ui`, `make build`, `make test`,
  `make e2e`, `make e2e-cron-fast`, `make image` already exist. Phase 8 invokes these from CI
  rather than re-implementing the equivalent shell. The image-size gate is a NEW one-line
  step in CI; consider promoting to `make image-size-check` as a Phase 8 Make target so
  developers can run the same gate locally.
- `Dockerfile` (Phase 7) — multi-stage on `distroless/static-debian12:nonroot`. Phase 8 builds
  via `docker/build-push-action@v6` referencing this file; no Dockerfile edits in Phase 8.
- `e2e/compose.test.yml` + `e2e/compose.test.override.cron-fast.yml` (Phase 1 + Phase 3) — the
  test stack the e2e job brings up via `make e2e-cron-fast`. Phase 8 does not modify these;
  any new test infrastructure for CI lives in `.github/workflows/`.
- `tygo.yaml` + `make check-types` (Phase 1) — Phase 8 adds a CI step that invokes
  `make check-types` directly. No new tygo config.

### Established Patterns

- **Sequential gates with `needs:` graph** (Phase 1 baseline) — `e2e` declares `needs: [go, ui]`.
  Phase 8 extends: `lint` becomes a prerequisite of `go`; `image-build` becomes a prerequisite
  of `e2e`; `publish` declares `needs: [e2e, ghcr-smoke-pr]` and runs only when
  `github.event_name != 'pull_request'`.
- **Compose-based e2e in CI** (Phase 1) — `docker compose -f e2e/compose.test.yml up -d --wait`
  works on `ubuntu-24.04` runners out of the box (Docker + Compose v2 preinstalled). Phase 8
  does not change this; just invokes `make e2e-cron-fast`.
- **Tygo source-of-truth in CI** (Phase 1) — `make check-types` is a fail-on-diff gate. Phase 8
  promotes it from the `go` job's tail step into a top-level `lint` job step so a tygo drift
  fails fast before unit tests even run.
- **`ubuntu-24.04` runner pin** (Phase 1) — already in Phase 1's baseline; Phase 8 keeps it.
  Avoids `ubuntu-latest` drift.
- **Cache key per-package-lock-path** (Phase 1's `setup-node@v4` config) — already in Phase 1;
  Phase 8 inherits.

### Integration Points

- `RELEASING.md` (NEW) ← cross-linked from `PROJECT.md` (Plan 08-03 touches both).
- `SMOKE.md` (NEW) ← referenced from `RELEASING.md` as the append-only manual-smoke ledger.
- `.golangci.yml` (NEW) ← referenced from `ci.yml` `lint` job. Default location auto-detected
  by golangci-lint.
- `Makefile` ← Plan 08-01 adds an `image-size-check` target so developers can run the CI gate
  locally. Optional; if the team prefers the gate stay in CI only, drop the Make target.

</code_context>

<specifics>
## Specific Implementation Choices

- **No `workflow_dispatch` button for ad-hoc publishes in v1.** The brief is explicit that
  releases follow `main` + semver tag. A manual-dispatch button is a footgun if it lets a
  maintainer publish a `:latest` from a non-main branch. If a future hotfix workflow needs
  it, add a dedicated `hotfix.yml` with a branch allowlist (out of scope here).
- **Image-size gate runs in the `image-build` job, not the `e2e` job.** Keeps the gate fast-
  failing — if the image is over budget, no point running the 3–4 minute e2e suite.
- **`docker stats --no-stream`** for the idle-RAM measurement runs AFTER the e2e suite
  finishes its first poll cycle (~10 s with `e2e-cron-fast`'s `@every 5s` cron). This catches
  baseline RAM after the binary has run through the full discovery → poll → state-write loop,
  not the cold-start figure. The number is logged to the step summary but is NOT a hard gate
  for v1 (RAM measurement on ephemeral runners has too much noise to gate releases reliably
  — Phase 7 owns the production-host RAM budget verification). Phase 8 captures the CI-side
  number as a regression-watch signal only.
- **No matrix builds.** Single platform (`ubuntu-24.04`), single Go version (`1.26`), single
  Node version (`22`). The brief explicitly defers arm64 to V2; carrying a matrix here would
  ship an empty cell and burn CI minutes.
- **Real-GHCR smoke runs on EVERY workflow trigger, not just publish.** Catches the
  anonymous-token-flow regression on PRs (where the publish job is gated off), so a bad
  dependency bump fails on the PR that introduced it rather than after merge. The PR-side
  variant targets the frozen anchor only; the post-publish variant adds the
  just-pushed-image probe.
- **`docker/build-push-action@v6` `provenance: false` + `sbom: false`** for v1. The brief and
  STACK.md don't call for build provenance / SBOM artifacts; carrying them now adds
  20–40 MB to the image (provenance + SBOM are attached as referrer manifests in the OCI 1.1
  layout) for no v1 benefit. V2-SUPPLY-CHAIN tracks the future add.
- **PR builds NEVER push.** The publish job's `if:` clause is
  `if: github.event_name != 'pull_request' && success() && (github.ref == 'refs/heads/main' || startsWith(github.ref, 'refs/tags/v'))`.
  Belt-and-braces: even if a forked-PR malicious workflow tried to set `push: true`, the
  `permissions:` block denies it (PR forks get read-only tokens by GitHub's policy).

</specifics>

<deferred>
## Deferred Ideas

- **Cosign / sigstore image signing** — V2. Adds `permissions: id-token: write` to publish
  job + a `sigstore/cosign-installer@v3` step. Plumbing is well-understood but the brief
  does not require it for v1's LAN-only deployment posture.
- **SBOM (CycloneDX / SPDX) generation** — V2-SUPPLY-CHAIN. Hook into `build-push-action@v6`
  via `sbom: true` once the consuming side (operator-installed scanner) is decided.
- **arm64 image builds** — V2-ARM64. One-line `platforms: linux/amd64,linux/arm64` flip on
  `build-push-action@v6` once arm64 HMI hardware lands. Distroless multi-arch tags already
  work.
- **Private registry credentials in CI** — V2-PRIV-REG. Not needed for the all-public-image
  v1 deployment.
- **Dependabot / Renovate** — out of scope, no team decision yet on cadence. Easy add later;
  Phase 8 does not block it.
- **CodeQL / GHAS security scanning** — out of scope for v1. The repo is org-managed under
  `centroid-is`; CodeQL would surface from the org settings if enabled. Phase 8 doesn't add
  or remove the workflow.
- **GitHub Releases automation** (release-please / auto-changelog) — out of scope. The
  manual-tag step in `RELEASING.md` produces a Git tag; the operator can write a GitHub
  Release body by hand. If automation lands later, the publish workflow's `workflow_run`
  chain is the natural extension point.
- **Test-result XML upload to a dashboard** (junit-reporter etc.) — out of scope.
  Playwright's HTML report is already uploaded on failure; the operator can browse it from
  the workflow run page. No team dashboard exists yet.
- **Per-PR ephemeral `:pr-<n>` image tags** for preview deployments — out of scope. The
  brief's deployment model is "field engineer pulls from GHCR onto an HMI"; preview tags
  would burden GHCR storage with no consumer.
- **Self-hosted runners** — out of scope. `ubuntu-24.04`-hosted runs the full pipeline in
  ~6–8 minutes; the cost of self-hosting infra exceeds the value at this team size.

</deferred>
</content>
