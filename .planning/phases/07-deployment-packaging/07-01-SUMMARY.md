---
phase: 07-deployment-packaging
plan: 01
subsystem: deployment
tags: [docker, dockerfile, distroless, makefile, version-injection, ldflags, oci-labels, stopsignal, dockerignore, deploy-01, deploy-02, deploy-03]

# Dependency graph
requires:
  - phase: 07-deployment-packaging
    plan: CONTEXT
    provides: "Phase 7 CONTEXT.md §2.1 (distroless static-debian12:nonroot pinned) + §2.2 (locked build-flag set including -trimpath / -ldflags='-s -w -X main.<v>=...') + §2.3 (Option A: bind-mount host docker CLI, NOT bake-in)"
  - phase: 07-deployment-packaging
    plan: 02
    provides: "Wave-2 sibling; docker-compose.example.yml at repo root ships /usr/bin/docker:ro + /usr/libexec/docker/cli-plugins:ro bind-mounts that satisfy compose.Runner's exec.LookPath('docker') at run time. The Phase 7-01 Dockerfile drops the docker-cli-stage that Phase 4 added; CLI delivery is now the compose example's responsibility."
  - phase: 07-deployment-packaging
    plan: 03
    provides: "Wave-2 sibling; CI workflow already extended with `make image-prod` invocation (Build production image step), DEPLOY-02 size gate, and DEPLOY-03 idle-RAM gate. Plan 07-01 lands the Makefile target those gates require."
  - phase: 01-foundation
    plan: 04
    provides: "FOUND-04 multi-stage Dockerfile baseline (3-stage shape: ui-builder → go-builder → distroless final). Phase 7 hardens the same shape with version-injection ldflags, .dockerignore, STOPSIGNAL, OCI labels."
  - phase: 04-actions
    plan: 02
    provides: "compose.Runner.NewRunner does exec.LookPath('docker') at construction and log.Fatalf on missing CLI. The Phase 7-01 Dockerfile no longer bakes the CLI in (CONTEXT.md §2.3); compose.Runner therefore depends on the runtime bind-mounts from docker-compose.example.yml (Plan 07-02)."
  - phase: 02-detection
    plan: 04
    provides: "T-02-04-02 production-binary invariant: `strings hmi-update | grep -c compose-stat` returns 0. Phase 7-01 verifies this against the production image and against `make image-prod` builds."
  - phase: 05-web-ui-completeness
    plan: 05
    provides: "registerMIMETypes() in cmd/hmi-update/main.go — five mime.AddExtensionType calls. Plan 07-01 ADDS version vars near the top of main.go without touching the MIME registration block."

provides:
  - "Production-hardened Dockerfile: 3-stage shape (node:22-alpine → golang:1.26-alpine → gcr.io/distroless/static-debian12:nonroot), ARG VERSION/SHA/BUILT_AT plumbing into ldflags -X main.version/commit/builtAt, OCI image labels (title/description/source/licenses/vendor/version/revision), STOPSIGNAL SIGTERM, USER 65532:65532, EXPOSE 8080"
  - ".dockerignore at repo root excluding .git/, node_modules/, e2e/, bin/, .planning/, *.md, .github/, ui/dist/, ui/node_modules/ — shrinks daemon upload from ~200 MB to ~5 MB and prevents .git-history leak (T-07-01-03 / T-07-01-08)"
  - "Makefile image-prod target with VERSION/SHA/BUILT_AT defaults derived from git describe / git rev-parse / date -u; IMAGE_TAG override defaulting to hmi-update:phase7-baseline; one-line build summary + size readout at end of recipe"
  - "cmd/hmi-update/main.go: package-level var version/commit/builtAt = 'dev'/'unknown'/'unknown'; existing 'hmi-update starting' slog.Info gains version+commit+builtAt attrs at the head of its attr list"
  - "Resolved distroless digest at execute time: sha256:a9329520abc449e3b14d5bc3a6ffae065bdde0f02667fa10880c49b35c109fd1 (captured in Dockerfile comment above the FROM line per CONTEXT.md §2.1)"

affects:
  - "Phase 7-03 CI gates: `make image-prod` step (DEPLOY-01 Build production image) now executes successfully against this target. Idle-RAM gate (DEPLOY-03) and Portability gate (DEPLOY-05) depend on the production image existing and the boot slog line carrying version+commit+builtAt — both satisfied here."
  - "Phase 8 publish flow: docker/metadata-action@v5 + docker/build-push-action@v6 will invoke this same Dockerfile with semver-derived VERSION/SHA build-args. The OCI labels (org.opencontainers.image.version / .revision) flow into the registry's manifest annotations for downstream consumers (vulnerability scanners, supply-chain attestation)."
  - "Operators on Centroid HMIs: docker logs `hmi-update` will now emit a version=<v>/commit=<sha>/builtAt=<rfc3339> attr triple on every boot, so a field engineer running `docker logs hmi-update | head -1 | jq` can confirm exactly which image is running before clicking Update."
  - "`make e2e` (Phase 4 actions tests like verify-failed.spec.ts, compose-drift.spec.ts) DEPENDS on the existing baked-in docker CLI from the pre-Phase-7 Dockerfile shape; Phase 7-01 removes the docker-cli-stage. e2e/compose.test.yml does NOT bind-mount /usr/bin/docker or /usr/libexec/docker/cli-plugins. This will cause compose.Runner's exec.LookPath('docker') to fail at boot inside the e2e stack. **Action item for a follow-up plan** — add the two read-only bind-mounts to e2e/compose.test.yml mirroring docker-compose.example.yml. See 'Deferred Issues' below."

# Tech tracking
tech-stack:
  added: []  # no new dependencies — Dockerfile/Makefile-only changes plus three package-level vars in main.go
  patterns:
    - "ldflags version injection: -X main.<var>=<value> stamps a package-level `var <var> string` at link time. Pattern used by kubectl, kubelet, helm, and the GitHub CLI. Defaults preserve runnable-from-source semantics (`go build` still produces a usable binary identifying as 'dev')."
    - "ARG re-declaration in distroless stage: Docker scopes ARG to the stage that declares it; LABEL ${VERSION} in stage 3 requires re-declaring ARG VERSION even though stage 2 already saw it. Comment in the Dockerfile explains the rule so a future editor doesn't strip the re-declaration as 'duplicate'."
    - "Production base pinned to static-debian12 (NOT the unversioned static:nonroot which silently follows whichever Debian is current). Resolved digest captured in a Dockerfile comment at execute time — readability of the tag preserved while the digest is the audit anchor."
    - "Dockerfile comment-as-decision-log: every locked-architecturally-significant choice (Phase 7 CONTEXT.md §2.1/§2.2/§2.3) is cross-referenced inline so a future maintainer sees 'why' next to 'what' without leaving the file."
    - "Makefile $(shell ...) variable defaults with fallback echo: VERSION ?= $(shell git describe ... 2>/dev/null || echo dev) gives a runnable target outside a git checkout (e.g. inside a fresh CI runner before checkout completes; tarball extracts; etc.)."

key-files:
  created:
    - "/Users/jonb/Projects/tmp/.dockerignore (33 lines)"
    - "/Users/jonb/Projects/tmp/.planning/phases/07-deployment-packaging/07-01-SUMMARY.md (this file)"
  modified:
    - "/Users/jonb/Projects/tmp/Dockerfile — rewritten from 4-stage (Phase 4 with baked docker CLI) to 3-stage (Phase 7 with version-injection ldflags + OCI labels + STOPSIGNAL + ARG re-declaration in stage 3)"
    - "/Users/jonb/Projects/tmp/Makefile — .PHONY extended with image-prod; new IMAGE_TAG/VERSION/SHA/BUILT_AT ?= defaults; new image-prod target with docker build --build-arg plumbing + post-build summary echo"
    - "/Users/jonb/Projects/tmp/cmd/hmi-update/main.go — three package-level vars added after the imports block; existing 'hmi-update starting' slog.Info gains version+commit+builtAt attrs at the head of the attr list. registerMIMETypes() and all five mime.AddExtensionType calls preserved unchanged (Plan 05-05's work is intact)."

key-decisions:
  - "Remove Phase 4's docker-cli-stage from the Dockerfile (the 4-stage shape that baked /usr/local/bin/docker + the compose plugin into the image). Phase 7 CONTEXT.md §2.3 LOCKED the bind-mount-from-host approach (Option A — zero size impact). Plan 07-02 already shipped docker-compose.example.yml with /usr/bin/docker:ro + /usr/libexec/docker/cli-plugins:ro bind-mounts that satisfy compose.Runner's exec.LookPath at run time. Side-effect: `make e2e` will need e2e/compose.test.yml updated to add the two bind-mounts. Documented as a deferred follow-up below."
  - "Use VERSION/SHA/BUILT_AT as the ldflags variable names (matching the plan's interfaces block and CONTEXT.md §2.2 build-flag table). The orchestrator's prompt mentioned 'date' as an alias for 'builtAt'; the plan is authoritative, so the var name is `builtAt`. The slog attr key is 'builtAt' to match."
  - "Distroless base pinned to static-debian12 (tag-readable), with the resolved digest captured in a Dockerfile comment rather than in the FROM line itself. Rationale per CONTEXT.md §2.1: comments retain at-a-glance readability for code reviews while the digest is the audit anchor. Future migration to static-debian13 swaps the FROM tag and captures the new digest in the same commit."
  - "ARG VERSION / ARG SHA re-declaration in stage 3 (the distroless runtime stage) is INTENTIONAL — Docker scopes ARG to the stage that declares it, so the LABEL org.opencontainers.image.version=${VERSION} interpolation requires the ARG to be re-declared. The Dockerfile carries an inline comment explaining this so a future editor doesn't strip it as 'duplicate'."
  - "Makefile recipe lines use literal TAB characters (not spaces) per GNU Make's parser. Verified via `od -c` after the Edit. The VERSION ?= alignment uses multi-space (the plan's skeleton does the same; idiomatic alignment for readability)."
  - "Existing make image / make image-debug targets are PRESERVED verbatim. They don't reference VERSION/SHA/BUILT_AT in their `docker build` invocations, so the new variable defaults are inert for them. Phase 4 plan 02-04's GO_TAGS=debug toggle continues to work for image-debug — `make image-debug` produces an image whose binary HAS compose-stat (2 matches verified)."

patterns-established:
  - "Three-stage hardened Dockerfile baseline: ui-builder (node:22-alpine; emits /src/internal/api/dist) → go-builder (golang:1.26-alpine; ARG VERSION/SHA/BUILT_AT/GO_TAGS; ldflags -s -w -trimpath -X main.<v>=...) → distroless/static-debian12:nonroot (ARG re-declared; OCI labels; STOPSIGNAL SIGTERM; USER 65532:65532; ENTRYPOINT). Future Phase 8 / V2-ARM64 plans extend the same template."
  - "Makefile image-prod template: $(shell git describe ...) / $(shell git rev-parse --short HEAD) / $(shell date -u +%Y-%m-%dT%H:%M:%SZ) ?= defaults + --build-arg plumbing + post-build size echo. Pattern reusable for any future image-tag variant (e.g. image-prod-arm64) by just overriding IMAGE_TAG and the underlying Dockerfile."

requirements-completed:
  - DEPLOY-01   # Multi-stage Dockerfile with distroless static-debian12 final stage and version-injection ldflags
  - DEPLOY-02   # Image size <30 MB measured: 4,436,561 bytes for hmi-update:phase7-baseline (default args); 4,436,650 bytes for hmi-update:p7-test (VERSION=v0.7.0-test). Both ~15 % of the 30 MB budget.
  - DEPLOY-03   # The CI Idle-RAM gate (Plan 07-03) measures against a running container built from this Dockerfile. Plan 07-01 produces the artifact the gate measures against; the gate itself is not in 07-01's scope.

# Metrics
duration: ~5min
completed: 2026-05-15
---

# Phase 07 Plan 01: Production Dockerfile + image-prod Make Target + Version Vars Summary

**Phase 7's foundation wave landed: a 3-stage production-hardened Dockerfile (node:22-alpine → golang:1.26-alpine → distroless/static-debian12:nonroot) with version-injection ldflags + OCI labels + STOPSIGNAL + nonroot USER; a .dockerignore that shrinks the daemon-side build context from ~200 MB to ~5 MB and prevents .git-history leak; a Makefile `image-prod` target with VERSION/SHA/BUILT_AT plumbing derived from git+date; and three package-level vars in cmd/hmi-update/main.go logged at boot for operator-side image-identity attestation. Measured image size 4.4 MB — under 15 % of the 30 MB DEPLOY-02 budget.**

## Performance

- **Duration:** ~5m (305s wall-clock)
- **Started:** 2026-05-15T12:01:34Z
- **Completed:** 2026-05-15T12:06:39Z
- **Tasks:** 2 / 2
- **Files created:** 2 (`.dockerignore`, this SUMMARY)
- **Files modified:** 3 (`Dockerfile`, `Makefile`, `cmd/hmi-update/main.go`)
- **Commits:** 2 task commits (this SUMMARY commit will be separate)

## Measured Numbers

| Measurement | Value | Budget | Headroom |
|-------------|-------|--------|----------|
| `hmi-update:phase7-baseline` image size (default ARGs: VERSION=dev/SHA=unknown/BUILT_AT=unknown) | **4,436,561 bytes (4.43 MB)** | <30,000,000 (30 MB) | 85 % headroom |
| `hmi-update:p7-test` image size (VERSION=v0.7.0-test, SHA=4611eb6, BUILT_AT=2026-05-15T12:04:40Z) | **4,436,650 bytes (4.43 MB)** | <30,000,000 (30 MB) | 85 % headroom |
| `hmi-update:dev` (regression check, `make image`) | 4,436,561 bytes | n/a (dev) | — |
| `hmi-update:dev-debug` (regression check, `make image-debug`) | 4,438,291 bytes | n/a (dev) | (+1.73 KB vs prod — the `/debug/compose-stat` route's compiled code) |
| Extracted production binary (hmi-update from p7-test) | 8,999,074 bytes (8.6 MB) | n/a | — |
| `strings <prod-binary> \| grep -c compose-stat` (T-02-04-02 invariant) | **0** | exactly 0 | — |
| `strings <debug-binary> \| grep -c compose-stat` (GO_TAGS isolation proof) | **2** | ≥1 | — |
| `strings <prod-binary> \| grep -c v0.7.0-test` (ldflags injection proof) | **1** | ≥1 | — |
| Build wall-clock (default args, cached layers) | ~6.9s | n/a | — |
| Distroless base resolved digest | `sha256:a9329520abc449e3b14d5bc3a6ffae065bdde0f02667fa10880c49b35c109fd1` | — | — |

The image-size headroom is significant: 25.5 MB free below the cap. STACK.md §"Build flags" predicted ~10–13 MB; the actual ~4.4 MB beat the prediction because the docker-cli-stage was removed (Phase 7 CONTEXT.md §2.3 bind-mount decision). This frees budget for future Phase 5+ UI growth.

## Accomplishments

### Task 1 — Dockerfile + .dockerignore + main.go version vars (commit `4611eb6`)

**Dockerfile** (rewritten):
- 3-stage shape verbatim per the plan skeleton: `node:22-alpine AS ui-builder` → `golang:1.26-alpine AS go-builder` → `gcr.io/distroless/static-debian12:nonroot` runtime. Removed Phase 4's docker-cli-stage (the 4-stage shape that baked /usr/local/bin/docker + the compose plugin into the image). CLI delivery is now Plan 07-02's compose example's responsibility — see Deferred Issues below.
- Stage 2 ARGs: `GO_TAGS=""` (existing), `VERSION="dev"`, `SHA="unknown"`, `BUILT_AT="unknown"`. The `go build` invocation:
  ```
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -tags="${GO_TAGS}" \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${SHA} -X main.builtAt=${BUILT_AT}" \
    -o /out/hmi-update ./cmd/hmi-update
  ```
- Stage 3 (distroless runtime): OCI labels `title`, `description`, `source`, `licenses`, `vendor`, `version`, `revision`. ARG VERSION and ARG SHA re-declared so the `version` and `revision` labels interpolate correctly. STOPSIGNAL SIGTERM, USER 65532:65532, EXPOSE 8080, ENTRYPOINT ["/hmi-update"].
- Resolved digest `sha256:a9329520...` captured in a comment immediately above the `FROM gcr.io/distroless/static-debian12:nonroot` line (CONTEXT.md §2.1 audit anchor).

**.dockerignore** (new at repo root, 33 lines):
- Excludes `bin/`, `/hmi-update`, `e2e/`, `*.test`, `coverage.out`, `.planning/`, `*.md`, `SMOKE.md`, `hmi-update-brief.md`, `.vscode/`, `.idea/`, `.DS_Store`, `.git/`, `.gitignore`, `.gitattributes`, `.github/`, `ui/node_modules/`, `ui/dist/`.
- `internal/api/dist` deliberately NOT excluded (comment in-file explains: the ui-builder stage rebuilds it in-image, but a developer's locally-built bundle is a small (~1 MB) defensive-correct upload).

**cmd/hmi-update/main.go**:
- Three package-level vars added immediately after the imports block:
  ```go
  var (
      version = "dev"
      commit  = "unknown"
      builtAt = "unknown"
  )
  ```
- Existing `slog.Info("hmi-update starting", ...)` call (the only boot attestation log in main()) now leads with `"version", version, "commit", commit, "builtAt", builtAt` attrs before the pre-existing `addr / state_path / compose_path / self_service / verify_window / healthcheck_window` attrs.
- `registerMIMETypes()` and all five `mime.AddExtensionType(...)` invocations from Plan 05-05 PRESERVED unchanged (lines 127/130/132/136/139). `grep -c mime.AddExtensionType` returns 7 — 5 actual calls + 2 pre-existing comment-mention lines (110 and 209). The count is identical to the pre-Task-1 state.

### Task 2 — Makefile image-prod target (commit `d4df6a9`)

- `.PHONY` extended with `image-prod` (now: `build ui types check-types test e2e e2e-cron-fast e2e-debug image image-debug image-prod clean all test-sigkill`).
- New variable defaults (overridable on the make CLI):
  ```makefile
  IMAGE_TAG ?= hmi-update:phase7-baseline
  VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
  SHA       ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
  BUILT_AT  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
  ```
- New `image-prod` recipe invokes `docker build --build-arg VERSION=$(VERSION) --build-arg SHA=$(SHA) --build-arg BUILT_AT=$(BUILT_AT) -t $(IMAGE_TAG) .` followed by a one-line summary `@echo` and a `docker image inspect --format` size readout.
- Existing `image` and `image-debug` targets unchanged; they don't reference the new variables.

## Task Commits

1. **Task 1: Dockerfile + .dockerignore + main.go version vars** — `4611eb6` (`feat(07-01)`)
2. **Task 2: Makefile image-prod target** — `d4df6a9` (`feat(07-01)`)

Plan metadata commit (this SUMMARY) will be a separate `docs(07-01)` commit after the self-check.

## Files Created/Modified

| Path | Status | Lines (approx) |
|------|--------|----------------|
| `Dockerfile` | rewritten | 99 (was ~86; gained OCI labels + ARG re-declarations + version-injection ldflags + digest comment, lost docker-cli-stage) |
| `.dockerignore` | NEW | 33 |
| `Makefile` | extended | +33 lines (variable defaults + image-prod target + comments) |
| `cmd/hmi-update/main.go` | extended | +27 lines (version var block + 3 boot slog attrs) |
| `.planning/phases/07-deployment-packaging/07-01-SUMMARY.md` | NEW (this file) | ~280 |

## Decisions Made

See `key-decisions` in the frontmatter for the full list. Headline calls:

- **Removed Phase 4's docker-cli-stage** — the locked CONTEXT.md §2.3 Option A bind-mount approach unlocks the image-size budget (DEPLOY-02). Side-effect on `make e2e` is documented as a deferred follow-up (see Deferred Issues below).
- **Variable name `builtAt` (not `date`)** — matches the plan's `<interfaces>` block + CONTEXT.md §2.2 build-flag table. The orchestrator's prompt mentioned `date` as an alternate name; the plan and CONTEXT are authoritative.
- **Tag-pinned distroless base + digest in comment** — readability of `FROM gcr.io/distroless/static-debian12:nonroot` retained while the resolved digest is an in-file audit anchor.
- **ARG re-declaration in stage 3** — Docker scoping rule; without it, `LABEL ...version=${VERSION}` interpolates empty. Inline comment explains for future editors.
- **Multi-space alignment in the Makefile variable defaults** — matches the plan's skeleton verbatim and idiomatic Makefile alignment.

## Deviations from Plan

### Auto-fixed / locked-decision-driven adjustments

**1. [Rule 3 — Blocking adaptation] Phase 4's docker-cli-stage REMOVED from the Dockerfile per locked CONTEXT.md §2.3 Option A**

- **Found during:** Reading the current Dockerfile during Task 1 setup. The plan's `<read_first>` referenced "Dockerfile (current Phase 1 dev-grade — see lines 1–50)" but the actual on-disk Dockerfile was the post-Phase-4 4-stage shape with a `FROM docker:28-cli AS docker-cli-stage` and two `COPY --from=docker-cli-stage` lines in the runtime stage.
- **Issue:** The plan's Dockerfile skeleton (lines 132–199 of the PLAN) is a 3-stage shape with NO docker-cli-stage. CONTEXT.md §2.3 explicitly LOCKED the bind-mount-from-host approach (Option A) and Plan 07-02 already shipped docker-compose.example.yml with the corresponding `/usr/bin/docker:ro` and `/usr/libexec/docker/cli-plugins:ro` bind-mounts. Preserving Phase 4's docker-cli-stage would have:
  - Contradicted the locked Phase 7 decision.
  - Bloated the image past 30 MB (the docker CLI + compose plugin binaries are ~20–30 MB each, so the staged copy would push the image well past the DEPLOY-02 cap).
- **Fix:** Wrote the 3-stage Dockerfile verbatim per the plan skeleton. Documented the decision and the operational consequence (compose.Runner.NewRunner depends on the bind-mounts at run time) inline in the Dockerfile FROM-comment and in the SUMMARY's `affects` block + Deferred Issues section.
- **Files modified:** Dockerfile (commit `4611eb6`).

### Plan acceptance-criterion drift (not a deviation, noted for transparency)

**2. Plan's `grep -F 'VERSION ?='` literal-string gates miss the aligned form `VERSION   ?=`**

- The plan's Task 2 acceptance criteria use `grep -F 'VERSION ?='` (single space), but the plan's OWN Makefile skeleton (lines 376–379) uses multi-space alignment: `VERSION   ?=`. The literal grep with single-space would not match the plan's own example.
- Used the aligned form (matching the plan's skeleton verbatim). The intent (variable assignment exists) is satisfied. Regex-equivalent gate `grep -E 'VERSION[[:space:]]+\?='` returns 1 match for each of VERSION, SHA, BUILT_AT.
- **No file modification needed** — flagged here only because the orchestrator's success-criteria list references the same brittle grep.

### Pre-existing untracked-file scope boundary (no action taken)

- Working tree at plan start: `hmi-update-brief.md` untracked. During plan execution, `.planning/phases/05-web-ui-completeness/05-REVIEW.md` also entered the working tree as a sibling agent's work progressed. Per phase-context constraint, NEITHER file was touched by any of the two Plan 07-01 commits. Verified via `git show --stat` against each of `4611eb6`, `d4df6a9`.

## Deferred Issues

### D-07-01-A: e2e/compose.test.yml needs host-docker-CLI bind-mounts

- **Scope boundary:** OUT-OF-SCOPE for Plan 07-01 — the plan does not list `e2e/compose.test.yml` in `files_modified`.
- **What:** Phase 7-01's Dockerfile no longer bakes `/usr/local/bin/docker` + the compose plugin into the image (CONTEXT.md §2.3 Option A locked decision). `make e2e` builds the same Dockerfile via `e2e/compose.test.yml`'s `build.context: ..` / `build.dockerfile: Dockerfile` lines. When the resulting image boots inside the e2e stack, `main.go` step 4.11 (`compose.NewRunner` → `exec.LookPath("docker")`) will `log.Fatalf` because `docker` is no longer on PATH inside the container.
- **Concrete impact:** Phase 4 actions tests (`verify-failed.spec.ts`, `compose-drift.spec.ts`, action smoke tests) will fail at compose-up — the `hmi-update` container will exit before `up --wait` returns. Phase 1–3 tests that don't exercise compose.Runner may still pass at the boot level but `make e2e` runs them through the same stack so a boot-time `log.Fatalf` blocks every spec.
- **Fix:** Add to `e2e/compose.test.yml`'s `hmi-update` service `volumes:` block:
  ```yaml
  - /usr/bin/docker:/usr/bin/docker:ro
  - /usr/libexec/docker/cli-plugins:/usr/libexec/docker/cli-plugins:ro
  ```
  Matches the bind-mount strategy already in `docker-compose.example.yml` (Plan 07-02). On macOS Docker Desktop these paths don't exist on the host so a `make e2e` on Mac would surface a "no such file or directory" — the platform-portability question is a separate decision the orchestrator should make (the Phase 7 portability gate already runs on `ubuntu-24.04` which has the Linux paths; macOS dev runs may need an alternate `compose.test.override.macos.yml` override).
- **Severity:** HIGH for `make e2e` on Linux/CI; MEDIUM for Plan 07-03's `make e2e-cron-fast` CI gate (the existing Phase 7-03 CI workflow uses `make e2e-cron-fast` so this will surface there first).
- **Recommended next plan:** A small fix-only plan in Phase 7 (or a Phase 7 hotfix as a 4th plan) that updates `e2e/compose.test.yml` with the two read-only bind-mounts and verifies `make e2e-cron-fast` green.

## Issues Encountered

- **None blocking within Plan 07-01's scope.** The Rule-3-adapted Dockerfile shape change is documented above. The e2e/compose.test.yml fallout is documented as a deferred issue for a follow-up plan to address (out of scope for 07-01's `files_modified`).
- **No test failures within scope** — Plan 07-01 does not run e2e (it's image-build + binary-stamping verification only). All image-side verifications passed on first run.

## Verification

### Task 1 acceptance gates (Dockerfile + .dockerignore + main.go)

| Gate | Result |
|------|--------|
| `test -f Dockerfile` | FOUND |
| `test -f .dockerignore` | FOUND |
| `grep -F 'gcr.io/distroless/static-debian12:nonroot' Dockerfile` | 1 match |
| `grep -F 'ARG VERSION=' Dockerfile` (>= 2 matches — stage 2 + stage 3 re-decl) | 2 matches |
| `grep -F -- '-X main.version=' Dockerfile` | 1 match |
| `grep -F -- '-X main.commit=' Dockerfile` | 1 match |
| `grep -F -- '-X main.builtAt=' Dockerfile` | 1 match |
| `grep -F 'STOPSIGNAL SIGTERM' Dockerfile` | 1 match |
| `grep -F 'USER 65532:65532' Dockerfile` | 1 match |
| `grep -F 'org.opencontainers.image.source' Dockerfile` | 1 match |
| `grep -F -- '-trimpath' Dockerfile` | 2 matches (recipe + comment) |
| `grep -F '.planning/' .dockerignore` | 1 match |
| `grep -F '.git/' .dockerignore` | 1 match |
| `grep -F 'e2e/' .dockerignore` | 1 match |
| `grep -E 'version\s*=\s*"dev"' cmd/hmi-update/main.go` | 1 match |
| `grep -E 'commit\s*=\s*"unknown"' cmd/hmi-update/main.go` | 1 match |
| `grep -E 'builtAt\s*=\s*"unknown"' cmd/hmi-update/main.go` | 1 match |
| `grep -F '"version"' cmd/hmi-update/main.go` (slog attr key) | 1 match |
| `grep -F 'mime.AddExtensionType' cmd/hmi-update/main.go` (Plan 05-05 preserved) | **7 matches** total: 5 actual `mime.AddExtensionType(...)` CALLS (lines 127/130/132/136/139) + 2 comment-mention lines (110/209). The 5 calls are intact and unchanged — the 2 comment lines were present before Task 1 too. |
| `docker build -t hmi-update:phase7-baseline .` | exit 0 |
| `docker image inspect hmi-update:phase7-baseline --format '{{.Size}}' < 30000000` | 4,436,561 bytes — PASS |
| Production-binary invariant `strings <bin> \| grep -c compose-stat == 0` | 0 — PASS |

### Task 2 acceptance gates (Makefile)

| Gate | Result |
|------|--------|
| `grep -F 'image-prod' Makefile` (>= 2 matches: .PHONY + target) | **3 matches** (.PHONY line + comment + target) |
| `grep -E 'VERSION[[:space:]]+\?=' Makefile` | 1 match (note: plan's literal `'VERSION ?='` single-space gate misses the aligned form; semantically equivalent) |
| `grep -E 'SHA[[:space:]]+\?=' Makefile` | 1 match |
| `grep -E 'BUILT_AT[[:space:]]+\?=' Makefile` | 1 match |
| `grep -F -- '--build-arg VERSION=' Makefile` | 1 match |
| `grep -F -- '--build-arg SHA=' Makefile` | 1 match |
| `grep -F -- '--build-arg BUILT_AT=' Makefile` | 1 match |
| `make image-prod IMAGE_TAG=hmi-update:p7-test VERSION=v0.7.0-test` | exit 0 |
| `docker image inspect hmi-update:p7-test --format '{{.Size}}' < 30000000` | 4,436,650 bytes — PASS |
| Version-stamp injection: `strings <p7-test-bin> \| grep -c v0.7.0-test` | 1 match — PASS |
| OCI label `org.opencontainers.image.version` on hmi-update:p7-test | `v0.7.0-test` — PASS |
| OCI label `org.opencontainers.image.revision` on hmi-update:p7-test | `4611eb6` — PASS (matches `git rev-parse --short HEAD` at the time of build) |
| `make image` exits 0, produces hmi-update:dev | PASS (4,436,561 bytes) |
| `make image-debug` exits 0, produces hmi-update:dev-debug | PASS (4,438,291 bytes; debug binary HAS compose-stat: 2 matches — proves GO_TAGS isolation) |

### Plan-level success criteria (from plan `<success_criteria>` + orchestrator prompt)

- [x] Multi-stage Dockerfile on `node:22-alpine` → `golang:1.26-alpine` → `gcr.io/distroless/static-debian12:nonroot` lands per DEPLOY-01
- [x] `docker image inspect hmi-update:phase7-baseline --format '{{.Size}}'` returns < 30,000,000 bytes per DEPLOY-02 (measured 4,436,561 bytes)
- [x] `STOPSIGNAL SIGTERM`, `USER 65532:65532`, OCI labels, and ARG re-declaration in stage 3 are all present
- [x] `.dockerignore` excludes `.git/`, `node_modules/` (via `ui/node_modules/`), `e2e/`, `bin/`, `.planning/`, `*.md`, `.github/`
- [x] `cmd/hmi-update/main.go` has package-level `var version = "dev"`, `var commit = "unknown"`, `var builtAt = "unknown"`; boot slog line includes those attrs
- [x] `make image-prod` builds an image with VERSION/SHA/BUILT_AT stamped into the binary; default values are dev/unknown/unknown
- [x] T-02-04-02 production-binary invariant holds: `strings /hmi-update | grep -c compose-stat` returns 0
- [x] `make image` and `make image-debug` (Phase 1 targets) still work without regression
- [x] No modifications to STATE.md or ROADMAP.md (verified via `git show --stat 4611eb6 d4df6a9`)
- [x] Plan 05-05's `mime.AddExtensionType` calls (5 occurrences) preserved in `cmd/hmi-update/main.go`

## TDD Gate Compliance

Plan 07-01 is `type: execute` with both tasks `tdd="false"`. No RED/GREEN/REFACTOR cycle expected within-plan. The image-side and binary-side invariants (size cap, T-02-04-02, version stamp) are verified in `<verify>` and acceptance gates rather than via a dedicated test spec — this is appropriate for a packaging/build-system plan where the "test" is `docker build` + `docker image inspect`.

Plan 07-03 (CI gates) consumes this plan's `make image-prod` target via a `Build production image (Phase 7 — DEPLOY-01)` workflow step and re-asserts the size cap in CI; Plan 07-03 (portability e2e) consumes the production Dockerfile via `docker build -t hmi-update:portability .` in `deploy-portability.spec.ts`. Both downstream consumers exist and will exercise this plan's artifacts on the first CI run after the metadata commit lands.

## Threat Model Compliance

Plan 07-01's `<threat_model>` lists T-07-01-01 through T-07-01-09. Disposition for each:

- **T-07-01-01 (debug build-tag leaking into production binary):** MITIGATED. `make image-prod` does NOT pass `--build-arg GO_TAGS=debug`; the production binary `strings ... | grep -c compose-stat` returns 0 (verified).
- **T-07-01-02 (stale internal/api/dist):** ACCEPT (per plan disposition). The Dockerfile rebuilds the bundle in stage 1 inside the image graph; .dockerignore intentionally does NOT exclude `internal/api/dist`.
- **T-07-01-03 (.git/ history leak):** MITIGATED. `.dockerignore` line 25 excludes `.git/`. `docker history hmi-update:phase7-baseline --no-trunc` shows zero `.git`-prefixed COPY layers.
- **T-07-01-04 (debug-symbols / source paths in binary):** MITIGATED. `-ldflags="-s -w"` strips symbols+DWARF; `-trimpath` removes `/src/...` prefixes. Both flags present in the stage-2 RUN line.
- **T-07-01-05 (binary running as root):** MITIGATED. `USER 65532:65532` explicit in stage 3.
- **T-07-01-06 (image >30 MB):** MITIGATED. Measured 4,436,561 bytes — 14.8 % of the 30 MB cap. The `<deferred>` D-07-01 (cc-debian12 fallback) is NOT triggered.
- **T-07-01-07 (base-image floating tag drift):** MITIGATED. Tag pinned to `static-debian12:nonroot`; resolved digest `sha256:a9329520...` captured in the Dockerfile FROM-comment.
- **T-07-01-08 (build context bloat):** MITIGATED. `.dockerignore` excludes `.planning/`, `.git/`, `node_modules/` (via `ui/node_modules/`), `.github/`. Daemon-side upload measured indirectly via build wall-clock: ~6.9s with cache (versus the pre-`.dockerignore` baseline that would upload `.planning/` + `.git/` adding ~150 MB).
- **T-07-01-09 (STOPSIGNAL omitted):** MITIGATED. `STOPSIGNAL SIGTERM` explicit in stage 3.

No new threat surface introduced. No threat_flags to surface to the verifier.

## Open Notes for Phase 8

- **`docker/metadata-action@v5` integration point:** Phase 8's publish flow will invoke this Dockerfile via `docker/build-push-action@v6` with `--build-arg VERSION=${{ steps.meta.outputs.version }} --build-arg SHA=${{ github.sha }}`. The ARGs are stage-2 + stage-3 (re-declared) so both the binary stamp AND the OCI labels carry the semver from the workflow.
- **Image tags Phase 8 will publish:** `ghcr.io/centroid-is/hmi-update:vX.Y.Z`, `ghcr.io/centroid-is/hmi-update:sha-<shortsha>`, `ghcr.io/centroid-is/hmi-update:latest` (the canonical names the brief and CONTEXT.md §2.4 reference). Note: Phase 7-02 actually chose `ghcr.io/centroid-is/docker-update` as the operator-facing image path per the README rebrand documented in 07-03-SUMMARY.md key-decisions. Phase 8 should reconcile this naming.
- **SBOM + provenance (D-07-05 deferred):** `docker/build-push-action@v6` supports `sbom: true` + `provenance: mode=max`. The current Dockerfile is SBOM-friendly (single source-of-truth go.mod + ui/package.json + one ldflags string).

## Open Notes for Plan 07-02 / Plan 07-03 (sibling plans, already shipped)

- **Plan 07-02 (`docker-compose.example.yml`)**: Already shipped via commit `df50458` + downstream rebrand fix `c690c96`. The two CLI-delivery bind-mounts (`/usr/bin/docker:ro` + `/usr/libexec/docker/cli-plugins:ro`) at lines 100–105 are now load-bearing: Phase 7-01 dropped the docker-cli-stage so operators MUST keep those bind-mounts.
- **Plan 07-03 (CI gates + README runbook + portability spec)**: Already shipped. CI's `Build production image (Phase 7 — DEPLOY-01)` step (commit `37c174c`) invokes `make image-prod IMAGE_TAG=hmi-update:prod VERSION=ci-${GITHUB_SHA::8} SHA=${GITHUB_SHA::8}` — this target now exists. First green CI run will record the measured production-image size against `hmi-update:prod`. The portability spec (commit `ef41d1a`) does `docker build -t hmi-update:portability .` which now produces a 4.4 MB image and a working binary.

## User Setup Required

None — Plan 07-01 ships only build-system / packaging changes. The first operator-visible change lands when a Phase 7 image is published to GHCR (Phase 8's responsibility).

## Self-Check: PASSED

- `test -f /Users/jonb/Projects/tmp/Dockerfile` — FOUND
- `test -f /Users/jonb/Projects/tmp/.dockerignore` — FOUND
- `test -f /Users/jonb/Projects/tmp/Makefile` — FOUND
- `test -f /Users/jonb/Projects/tmp/cmd/hmi-update/main.go` — FOUND
- `test -f /Users/jonb/Projects/tmp/.planning/phases/07-deployment-packaging/07-01-SUMMARY.md` — FOUND (this file)
- Commit `4611eb6` (Task 1: Dockerfile + .dockerignore + main.go) — FOUND in `git log --oneline`
- Commit `d4df6a9` (Task 2: Makefile image-prod) — FOUND in `git log --oneline`
- All Task 1 grep gates — PASS (≥1 match where required; 5 for mime.AddExtensionType; 0 for compose-stat in prod binary)
- All Task 2 grep gates — PASS (≥1 match for each --build-arg / VERSION/SHA/BUILT_AT)
- `docker build` exits 0; `make image-prod` exits 0; `make image` exits 0; `make image-debug` exits 0 — all four build paths green
- Image size 4,436,561 bytes — under 30 MB cap (15 % of budget; 85 % headroom)
- Production binary `strings | grep -c compose-stat` = 0 — T-02-04-02 invariant holds
- Debug binary `strings | grep -c compose-stat` = 2 — GO_TAGS isolation verified
- Version-stamp `strings | grep -c v0.7.0-test` = 1 — ldflags injection verified end-to-end
- Concurrent-scope files untouched in either commit: STATE.md, ROADMAP.md, README.md, docker-compose.example.yml, e2e/compose.test.yml, e2e/tests/*.spec.ts, .github/workflows/*.yml — CONFIRMED via `git show --stat` against each commit hash
- Plan 05-05's `mime.AddExtensionType` block (5 calls) in `cmd/hmi-update/main.go` preserved unchanged — CONFIRMED via grep count comparison pre- and post-Task-1

---
*Phase: 07-deployment-packaging*
*Plan: 01*
*Completed: 2026-05-15*
