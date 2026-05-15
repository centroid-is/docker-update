# Phase 8 — CI / CD / Release Hardening — Summary

**Scope:** Land `.github/workflows/ci.yml`, `.github/workflows/publish.yml`,
`.golangci.yml`, and `RELEASING.md`. Connect the local repo to the GitHub remote
at `centroid-is/docker-update` and push `main`.

**Note on image-path rebrand:** the original brief used the image path
`ghcr.io/centroid-is/hmi-update` but the actual GitHub repo is
`centroid-is/docker-update`. The publish target was rebranded to
`ghcr.io/centroid-is/docker-update` in `publish.yml` and in `RELEASING.md`. The
Go module path, binary name, service name, and slog event strings remain
`hmi-update`. Phase 7's `docker-compose.example.yml` still references
`ghcr.io/centroid-is/hmi-update:latest`; this is owned by a concurrent agent and
needs a follow-up one-line edit after Plan 07-02 completes.

## Part 1 — Image path rebrand (limited)

**Changed:**
- `.github/workflows/publish.yml` — `images: ghcr.io/centroid-is/docker-update`,
  smoke-job ref string, step-summary heading, image label `source` URL.
- `RELEASING.md` — every operator-facing image reference uses
  `ghcr.io/centroid-is/docker-update`; the runbook explicitly calls out that the
  binary/service name remain `hmi-update`.

**Not changed:** `go.mod` module path, all internal Go imports, the binary name
`hmi-update`, slog event field names, CLAUDE.md.

**Deferred (Phase 7 concurrent-agent boundary):** the single image-path line in
`docker-compose.example.yml` should be updated after Plan 07-02 lands.

## Part 2 — GitHub Actions workflows

### `.github/workflows/ci.yml`

Single `build-test` job runs on push to main and on pull_request. Concurrency
`group: ci-${{ github.ref }}, cancel-in-progress: true`. Top-level
`permissions: contents: read`.

Step order (sequential, fail-fast):
1. `actions/checkout@v4`
2. `actions/setup-go@v5` (go-version: '1.26')
3. `actions/setup-node@v4` (node-version: '22')
4. `go vet ./...`
5. `golangci/golangci-lint-action@v6` (version: v1.62)
6. `go install github.com/gzuidhof/tygo@latest` + `make check-types`
7. `go test ./... -race`
8. `npm --prefix ui ci && npm --prefix ui run build`
9. `docker/setup-buildx-action@v3`
10. `docker/metadata-action@v5` (three-tag grammar; for shape validation)
11. `docker/build-push-action@v6` (push: false, load: true, tags: hmi-update:ci)
12. Image-size gate (`docker image inspect` — fail if >30,000,000 bytes)
13. Install oras + Playwright deps
14. `make e2e-cron-fast`
15. Upload `playwright-report/` on failure

### `.github/workflows/publish.yml`

Two jobs: `build-and-push` and `ghcr-smoke-published`. Triggers:
- `workflow_run` of `ci` (types: [completed], branches: [main]) with
  `if: github.event.workflow_run.conclusion == 'success'`
- `push: tags: ['v*.*.*']`

`build-and-push` (permissions: `contents: read, packages: write`):
- `actions/checkout@v4` with `ref: ${{ github.event.workflow_run.head_sha || github.ref }}`
  — race-safe; the publish builds from the exact SHA CI tested green.
- `docker/login-action@v3` using `${{ secrets.GITHUB_TOKEN }}` and `${{ github.actor }}`.
- `docker/metadata-action@v5` emitting the three tags:
  - `type=raw,value=latest,enable={{is_default_branch}}` (only on main push)
  - `type=semver,pattern={{version}}` (only on tag push)
  - `type=sha,prefix=sha-,format=short` (every push)
- `docker/build-push-action@v6` (push: true, platforms: linux/amd64, GHA cache,
  provenance: false, sbom: false).
- Outputs `primary_tag` (parsed from first metadata-action tag line) and
  `digest` (from build step).

`ghcr-smoke-published` (permissions: `contents: read` ONLY — NO packages: write):
- Asserts `$GITHUB_TOKEN` is unset AND `~/.docker/config.json` does not exist.
- `go install github.com/google/go-containerregistry/cmd/crane@v0.20.8`
- `crane digest ghcr.io/centroid-is/docker-update:<primary_tag>` — exit 0 required.
- `docker run --rm --pull always ghcr.io/centroid-is/docker-update:<primary_tag> --help`
  — exit codes 125/127 fail; other exits pass (proves the daemon pulled
  anonymously and the binary started).
- Load-bearing for Pitfall 2: the anonymous bearer-token flow against the
  newly-published image is exercised on every publish.

Concurrency `group: publish-${{ github.ref }}, cancel-in-progress: false`
(independent of ci.yml's group; do not cancel in-flight publishes).

### `.golangci.yml`

`disable-all: true` + explicit enables: errcheck, govet, ineffassign,
staticcheck, unused, gosimple, gofmt, goimports. Timeout 5m.
`exclude-files: ui/src/lib/types.d.ts`. `exclude-use-default: false`.

## Part 3 — RELEASING.md

Six-section operator runbook at repo root:
1. Pre-release checklist (green CI + green publish + clean status + HMI-like host).
2. Manual-smoke procedure with the Pitfall 5 display-blackout warning.
3. SMOKE.md recording template (Candidate tag / Image digest / Host / Operator /
   Result / Notes).
4. Tagging step with `git tag -s -a vX.Y.Z` and the `v*.*.*` trigger pattern.
5. Failure response.
6. Evidence trail (CI run URL, publish run URL, SMOKE.md entry, GHCR package URL).

All image references use `ghcr.io/centroid-is/docker-update`. The runbook calls
out explicitly that the binary/service name remain `hmi-update`.

## Part 4 — Git remote + push

See the agent's final response. The local commits are landed; the push outcome
is recorded there.

## Part 5 — Verification

- `python3 -c "import yaml; yaml.safe_load(open(...))"` passes for ci.yml,
  publish.yml, and .golangci.yml.
- `actionlint` (v1, installed via `go install
  github.com/rhysd/actionlint/cmd/actionlint@latest`) returns no warnings for
  ci.yml or publish.yml.
- All action version pins match Phase 8 RESEARCH.md:
  - actions/checkout@v4
  - actions/setup-go@v5 (go-version 1.26)
  - actions/setup-node@v4 (node-version 22)
  - docker/setup-buildx-action@v3
  - docker/login-action@v3
  - docker/metadata-action@v5
  - docker/build-push-action@v6
  - golangci/golangci-lint-action@v6 (version v1.62)
  - actions/upload-artifact@v4
  - crane @v0.20.8

## Commits

1. `feat(ci): main CI workflow` — ci.yml
2. `feat(ci): publish workflow with post-publish smoke` — publish.yml
3. `feat(ci): golangci-lint config` — .golangci.yml
4. `docs(ci): RELEASING.md manual smoke gate` — RELEASING.md

(STATE.md and ROADMAP.md not touched — owned by orchestrator.)

## Next steps for the user

1. Confirm `https://github.com/centroid-is/docker-update` exists (create it if
   not). Push outcome below in the agent's final report.
2. Once pushed, watch the `ci` workflow run on main. After it goes green, the
   `publish` workflow fires automatically via `workflow_run`.
3. Verify the three image tags appear at
   `https://github.com/centroid-is/docker-update/pkgs/container/docker-update`:
   `:latest`, `:sha-<short>`.
4. After Plan 07-02 lands, edit
   `docker-compose.example.yml`'s single image-path line to
   `ghcr.io/centroid-is/docker-update:latest` in a follow-up commit.
