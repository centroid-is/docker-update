# Phase 8: CI/CD & Release Hardening - Research

**Researched:** 2026-05-15
**Confidence:** HIGH on GitHub Actions surface (verified against marketplace + repos within
the last week). HIGH on GHCR anonymous-token-flow mechanics (corroborated by GHCR docs +
go-containerregistry source). MEDIUM on the post-publish smoke pattern (the technique is
well-established but documentation is fragmented across blog posts and Action READMEs).

This file is the supporting research for Phase 8. It answers three specific questions that
the parent CONTEXT.md treats as locked but that benefit from a paper trail:

1. What does `docker/build-push-action@v6` + `docker/metadata-action@v5` actually emit, and
   how do you wire them to produce the brief's three-tag matrix?
2. How does the anonymous bearer-token flow work against GHCR today, and what is the
   minimum smoke that catches a Pitfall 2 regression?
3. What's the canonical sequencing pattern for a publish workflow that depends on a CI
   workflow passing?

---

## Executive summary

The brief's three-tag publishing contract maps cleanly onto `docker/metadata-action@v5`'s
declarative tag grammar — no shell-script tag derivation needed. `docker/build-push-action@v6`
consumes the metadata-action's outputs verbatim. `actions/setup-go@v5` and
`actions/setup-node@v4` are unchanged from Phase 1's baseline. The only Phase-8-specific
research target is the **real-GHCR anonymous smoke**: how to assert, in CI, that a single
`crane digest` against a public GHCR image succeeds with NO `Authorization` headers being
sent (i.e. the bearer-token flow stays anonymous all the way through). The answer is to use
`go-containerregistry`'s pinned `crane` CLI, set `permissions: contents: read` on the job,
explicitly assert `$GITHUB_TOKEN` is empty in the job env, and rely on the upstream
implementation's correctness (since it's the SAME library the production binary uses through
Phase 3's `internal/registry` package — a divergence between smoke and prod is structurally
hard to introduce).

---

## Recommended action versions

| Action | Version | Purpose | Why this pin |
|--------|---------|---------|--------------|
| `actions/checkout@v4` | v4 | Source checkout | Phase 1 baseline; STACK.md confirms. |
| `actions/setup-go@v5` | v5 | Go toolchain | Phase 1 baseline; matches STACK.md. `go-version: '1.26'` matches the brief's pin. |
| `actions/setup-node@v4` | v4 | Node toolchain | Phase 1 baseline; matches STACK.md. `node-version: '22'`. |
| `golangci/golangci-lint-action@v6` | v6 | Lint cache + runner | Current major (v6 released early 2025). `version: v1.62.x` floor matches golangci-lint's current stable. v7 of the action exists but bumps the minimum Go to 1.23 — irrelevant for us. |
| `docker/setup-buildx-action@v3` | v3 | BuildKit driver | Required peer for `build-push-action@v6` (multi-stage caching needs buildx, even on a single-arch build). |
| `docker/login-action@v3` | v3 | GHCR auth | STACK.md pin; current major. Used in `publish.yml` only. |
| `docker/metadata-action@v5` | v5 | Tag generation | STACK.md pin; current major. Emits the brief's three-tag matrix declaratively. |
| `docker/build-push-action@v6` | v6 | Build + push | STACK.md pin; current major (v6 released 2024). Consumes metadata-action outputs. |
| `actions/upload-artifact@v4` | v4 | Playwright HTML report on failure | Phase 1 baseline. |
| `actions/cache@v4` | v4 | Optional crane install cache | Only needed if `go install crane` becomes a hot spot in CI minutes. |

Notes:
- The `@v5`/`@v6`/`@v4` pin scheme matches GitHub's documented practice — these are stable
  major refs that the action maintainers move forward only for non-breaking changes. For
  STRICT immutability, replace with full commit SHAs in V2 (out of scope per CONTEXT.md
  Area 6); the major-version pin is the brief's accepted posture.
- The `setup-buildx-action@v3` is new to this phase relative to Phase 1's baseline. Phase 1's
  `make image` target builds with the classic `docker build` driver, which is fine for local
  dev. CI needs buildx for `cache-from: type=gha` + `cache-to: type=gha` to function.

---

## Question 1 — Three-tag emission via `metadata-action@v5`

The brief's contract:

| Tag | Trigger |
|-----|---------|
| `:latest` | Tracks `main` |
| `:vX.Y.Z` | When a Git tag matches `v[0-9]+.[0-9]+.[0-9]+` |
| `:sha-<short>` | Every commit on `main` |

The corresponding `metadata-action` config:

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

How each line works:

| Line | What it produces | When it fires |
|------|------------------|---------------|
| `type=raw,value=latest,enable={{is_default_branch}}` | Tag string `latest`. | Only when the workflow's ref is the default branch (`main`). On a tag push to `vX.Y.Z`, the default-branch condition is false → `:latest` is NOT re-emitted from a tag push alone. If the tag IS on the main HEAD (the common case for a release), the `main` push that landed the merge already emitted `:latest`; the tag push then adds `:vX.Y.Z`. |
| `type=semver,pattern={{version}}` | Strips a leading `v` from a tag like `v1.2.3` → image tag `1.2.3`. Supports pre-release semver (`v1.2.3-rc1` → `1.2.3-rc1`). | On any tag push that parses as semver. PR pushes don't carry semver tags so this is a no-op there. |
| `type=sha,prefix=sha-,format=short` | Tag string `sha-<7chars>`. | On every push. PRs use the merge-commit SHA, main uses the commit SHA, tag pushes use the SHA the tag points to. |

What happens on each trigger:

- **PR push:** `metadata-action` emits `sha-<short>`. The `publish` job is gated off
  (`if: github.event_name != 'pull_request'`), so nothing gets pushed. The tag string is
  computed but discarded.
- **`main` push:** `metadata-action` emits `latest` + `sha-<short>`. The publish job runs;
  `build-push-action` pushes BOTH tags to GHCR.
- **`v1.2.3` tag push:** `metadata-action` emits `1.2.3` + `sha-<short>` (no `latest` unless
  the workflow re-runs on the default branch, which it doesn't for a tag-only push). The
  publish job runs; `build-push-action` pushes BOTH tags. Operators who want `:latest` to
  track the release tag rely on the SEPARATE `main` push that brought the release commit in,
  which already emitted `:latest` to the same image SHA.

The brief's literal language is "`:latest` tracks `main`, `:vX.Y.Z` per release, `:sha-<short>`
per commit." This config matches that semantics exactly.

### Source verification

- `docker/metadata-action` README, "Tags" section: documents `type=raw`, `type=semver`,
  `type=sha` with the exact options used above. Confirmed against the `main` branch of
  `docker/metadata-action` on 2026-05-15.
- `{{is_default_branch}}` and `{{version}}` are documented as expression contexts in the
  action's "Conditional tags" examples.

### Alternative considered — shell-derived tags

A common pattern is to compute tags in a shell step and pass them as a string to
`build-push-action`'s `tags:` input. We rejected this because:

1. The shell logic for "main → latest, tag → vX.Y.Z, every → sha-<short>" reproduces what
   `metadata-action` ships out of the box.
2. The shell version is brittle on the `:latest` condition (matching `refs/heads/main`
   correctly across pull_request_target / merge_group / workflow_run contexts is a known
   footgun).
3. `metadata-action` emits OCI labels (`org.opencontainers.image.*`) for free — version,
   revision, source URL, created timestamp — which a shell variant would need to bolt on
   manually. These labels matter for the operator-side `docker inspect` debugging story.

---

## Question 2 — GHCR anonymous bearer-token flow & the Pitfall 2 smoke

### What the flow looks like on the wire (anonymous read of a public image)

1. **Initial request:**
   ```
   GET /v2/<owner>/<image>/manifests/<tag> HTTP/1.1
   Host: ghcr.io
   Accept: application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json
   ```
   No `Authorization` header.

2. **Server response (401 Unauthorized):**
   ```
   HTTP/1.1 401 Unauthorized
   Www-Authenticate: Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:<owner>/<image>:pull"
   ```

3. **Token exchange:**
   ```
   GET /token?service=ghcr.io&scope=repository:<owner>/<image>:pull HTTP/1.1
   Host: ghcr.io
   ```
   Still no `Authorization` header (anonymous flow).

4. **Token response (200 OK):**
   ```json
   {"token": "<short-lived-jwt>", "expires_in": 300, "issued_at": "2026-05-15T..."}
   ```
   The JWT's `access` claim contains `[{"type":"repository","name":"<owner>/<image>","actions":["pull"]}]`.

5. **Authenticated retry of step 1:**
   ```
   GET /v2/<owner>/<image>/manifests/<tag> HTTP/1.1
   Host: ghcr.io
   Authorization: Bearer <short-lived-jwt>
   ```

6. **Server response (200 OK):**
   ```
   HTTP/1.1 200 OK
   Content-Type: application/vnd.oci.image.index.v1+json
   Docker-Content-Digest: sha256:<digest>
   ```

The Pitfall 2 regression is in step 3: a buggy client sends
`Authorization: Basic Og==` (base64 of `:`, i.e. empty username + empty password) at the
token endpoint, and GHCR responds 403 with no token. The CI smoke catches this because
`crane digest` exit code is non-zero when any step in the chain fails.

### Why the smoke must run with NO `GITHUB_TOKEN` in env

`crane` (and the underlying `go-containerregistry/pkg/v1/remote`) supports authn via several
keychains, including the `DefaultKeychain` which reads `~/.docker/config.json`. If
`GITHUB_TOKEN` is in the job env AND a prior step ran `docker login ghcr.io -u $GITHUB_ACTOR -p $GITHUB_TOKEN`,
the keychain picks up the credentials and the smoke runs the AUTHENTICATED bearer flow
instead of the anonymous one. The Pitfall 2 regression — which only manifests on the
ANONYMOUS code path — would slip through.

Mitigation in CI:

```yaml
ghcr-smoke:
  runs-on: ubuntu-24.04
  permissions:
    contents: read   # NO packages:read, NO id-token:write
  steps:
    - name: assert no credentials in env
      run: |
        if [ -n "$GITHUB_TOKEN" ]; then
          echo "FAIL: smoke job saw GITHUB_TOKEN; bug in job permissions" >&2
          exit 1
        fi
        if [ -f "$HOME/.docker/config.json" ]; then
          echo "FAIL: smoke job saw ~/.docker/config.json; a prior step logged in" >&2
          exit 1
        fi
    - uses: actions/setup-go@v5
      with: { go-version: '1.26' }
    - run: go install github.com/google/go-containerregistry/cmd/crane@v0.20.8
    - name: anonymous digest probe
      run: |
        crane digest ghcr.io/distroless/static-debian12:nonroot
```

`GITHUB_TOKEN` is implicitly unset when no `secrets:` are referenced in the step's `env:`
or `with:` blocks AND no `permissions:` keys grant write access. The `if [ -n "$GITHUB_TOKEN" ]`
check is belt-and-braces — it catches a future job-config drift where someone adds a token
ref to the job-level `env:` block.

The `~/.docker/config.json` check catches the same regression via a different path: a prior
step running `docker login` would write that file, and the keychain would pick it up.

### Source verification

- GHCR docs: "Authenticating to the Container registry" — documents the anonymous-read flow
  for public images (Bearer token from `/token` endpoint, no client auth).
- `go-containerregistry/pkg/v1/remote` source: `transport.NewWithContext` constructs the
  bearer-token round-tripper; with `authn.Anonymous` the request to `/token` is plain GET
  with no `Authorization` header.
- `crane` CLI source (`cmd/crane/cmd/digest.go`): default keychain is `authn.DefaultKeychain`,
  which falls through to `authn.Anonymous` when no credentials are configured. The
  no-`~/.docker/config.json` invariant is what guarantees the fall-through.
- The Pitfall 2 reference bugs are documented in `.planning/research/PITFALLS.md` Pitfall 2;
  WUD issue references in that file.

### Why a frozen anchor + a live post-publish probe

The PR-side smoke uses `ghcr.io/distroless/static-debian12:nonroot` (the same image Phase 7
uses as the production base). This is the frozen anchor — GHCR has served it anonymously for
years and the project has the same operational interest in it that we do. If anonymous flow
breaks against this anchor, the entire production deployment story is in trouble, and Phase 8
CI is the first line of defense.

The post-publish smoke ADDITIONALLY probes
`ghcr.io/centroid-is/hmi-update:<just-pushed-tag>`. This catches a different failure mode: a
visibility-misconfigured org repo (private by default vs. public). If we ever flip the repo
to private by accident, anonymous reads return 401 (not 403), `crane digest` exits non-zero,
the publish workflow fails loudly, and the operator knows to restore the public visibility
before tagging a release.

### Alternative considered — recording the request and asserting on headers

A "stronger" smoke would proxy the `crane digest` request through `mitmproxy` or a custom
http.RoundTripper and assert that the captured request to `/token` had no `Authorization`
header. We rejected this because:

1. The unit test `internal/registry/transport_test.go` (Phase 3) ALREADY does this via
   `httptest.NewServer`. CI runs it as part of the unit-test job.
2. Adding a proxy in CI introduces a new failure mode (mitmproxy cert setup, hostname
   matching) that has nothing to do with the regression being guarded.
3. The simple exit-code-based smoke is faster, more reliable, and catches the SAME class of
   regression — if `Authorization: Basic Og==` leaks, GHCR's response is 403, and
   `crane digest` exit code is non-zero. The unit test catches the WHY; the live smoke
   catches the WHAT.

The two together form a complete defense.

---

## Question 3 — Publish workflow sequencing pattern

### Pattern: `workflow_run` trigger off a successful CI run

```yaml
# .github/workflows/publish.yml
name: publish
on:
  workflow_run:
    workflows: ["ci"]
    types: [completed]
    branches: [main]
  push:
    tags: ['v*.*.*']

jobs:
  publish:
    if: |
      (github.event_name == 'workflow_run' && github.event.workflow_run.conclusion == 'success') ||
      (github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v'))
    runs-on: ubuntu-24.04
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4
      - uses: docker/setup-buildx-action@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - id: meta
        uses: docker/metadata-action@v5
        with:
          images: ghcr.io/centroid-is/hmi-update
          tags: |
            type=raw,value=latest,enable={{is_default_branch}}
            type=semver,pattern={{version}}
            type=sha,prefix=sha-,format=short
      - id: build
        uses: docker/build-push-action@v6
        with:
          context: .
          file: Dockerfile
          platforms: linux/amd64
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          cache-from: type=gha
          cache-to: type=gha,mode=max
          provenance: false
          sbom: false
```

How this composes:

- A merged PR triggers `ci.yml` on `main`. When `ci.yml` finishes successfully, GitHub fires
  `workflow_run` with `conclusion: success`, which triggers `publish.yml`.
- A direct tag push (`git push origin v1.2.3`) triggers `publish.yml` via the `push: tags:`
  path. `ci.yml` does NOT run on tag pushes (its trigger filter is `push: branches: [main] + pull_request`),
  so the tag-push publish runs without re-running CI. This is intentional: the tag is created
  against a commit that already passed CI on `main`. If the operator tags a commit that did
  NOT pass CI on main, they are bypassing the gate deliberately — that's their call, and
  `RELEASING.md`'s manual-smoke step is the human-side defense.
- `workflow_run` runs on the default branch's HEAD, not the CI workflow's branch ref. For a
  push to `main` this is the same commit; for any other branch the `workflow_run` trigger
  doesn't fire (the `branches: [main]` filter on the trigger limits it).

### Permission separation across workflows

- `ci.yml`: `permissions: contents: read` (default). No tokens needed.
- `publish.yml`: `permissions: { contents: read, packages: write }`. Needed for the
  `docker push` to GHCR.
- `ghcr-smoke` job inside `ci.yml` AND inside `publish.yml`: each is its own job with
  `permissions: contents: read` — no `packages:` access, no token. This is the load-bearing
  invariant for the Pitfall 2 smoke.

### Source verification

- GitHub Actions docs: "Events that trigger workflows" → `workflow_run` section. Confirms
  the `conclusion: success` filter and `branches:` filter behavior.
- `docker/build-push-action@v6` README "Examples" section: documents the GHA cache backend
  (`type=gha,mode=max`) and the `provenance: false` / `sbom: false` opt-outs.
- `docker/login-action@v3` README: documents the `${{ secrets.GITHUB_TOKEN }}` pattern for
  GHCR (no PAT needed for the org-owned repo).

### Alternative considered — embed publish steps in `ci.yml`

A simpler shape is to add the publish steps as a final job in `ci.yml` gated on
`if: github.event_name != 'pull_request' && success() && (github.ref == 'refs/heads/main' || startsWith(github.ref, 'refs/tags/v'))`.

Tradeoffs:

| | Two-workflow (`workflow_run`) | Single-workflow gated `if` |
|--|------------------------------|---------------------------|
| Tag-push path | `publish.yml` direct trigger, no CI re-run | `ci.yml` re-runs the lint/unit/e2e gates before publishing |
| Failure isolation | Publish failures don't show in PR status checks (good) | Publish failures show in `ci.yml` status; could be confusing on a PR that triggered a Test Lint failure followed by a (gated-out) skipped publish |
| Operator visibility | Two workflow runs to read for a green release | One unified run |
| Cancel-on-new-push | `publish.yml` has its own `concurrency:` group | `ci.yml`'s `concurrency:` group cancels in-progress publishes when a new PR push lands — risk of half-pushed images |

We pick **two-workflow** for v1 because the cancel-on-new-push isolation is the right
default for a publish job: cancelling a build-push-action mid-push can leave GHCR in a state
where some tags reference a manifest that wasn't fully uploaded (rare, but possible). Putting
publish in its own workflow with its own concurrency group avoids the issue.

---

## What NOT to use

| Avoid | Why | Use instead |
|-------|-----|-------------|
| `actions/cache@v3` | EOL; v4 is the current major. | `actions/cache@v4` |
| `docker/build-push-action@v5` | Pre-buildx-cache-improvements; `type=gha` backend works but slower; v6 is the current major per STACK.md. | `@v6` |
| `docker/metadata-action@v4` | Older flavor doesn't support `enable={{is_default_branch}}`; v5 added the expression context. | `@v5` |
| `actions/setup-go@v4` | Phase 1 baseline is v5; v4 is EOL on the Go-version side (doesn't know about 1.26). | `@v5` |
| `ubuntu-latest` runner | Floats between Ubuntu versions; CI behavior changes silently on a new GitHub runner image. | `ubuntu-24.04` (Phase 1 baseline) |
| `crane @latest` | Floats with go-containerregistry releases; could regress between phases. | `@v0.20.8` (matches Phase 3's library pin) |
| `golangci-lint --no-config` | Skips the project's `.golangci.yml` — different developers see different lint outputs. | `golangci-lint run` against the committed `.golangci.yml` |
| GitHub Personal Access Tokens for GHCR | Long-lived; rotation overhead; the org repo doesn't need one. | `${{ secrets.GITHUB_TOKEN }}` |
| `docker push` in a manual shell step | Loses the build-push-action features (cache, labels, multi-tag emission). | `docker/build-push-action@v6` |
| Shell-script tag derivation (`if [[ $GITHUB_REF == refs/tags/v* ]] ...`) | Brittle; reimplements `metadata-action`. | `docker/metadata-action@v5` |
| `actions/upload-artifact@v3` | EOL April 2024. | `@v4` |
| `docker login` in a non-publish job | Adds credentials to the keychain; can leak into smoke jobs. | `docker/login-action@v3` scoped to the publish job only |
| `permissions:` at workflow level without job-level overrides | Spreads write capabilities across all jobs; smoke job loses its anonymous invariant. | Default workflow `permissions: contents: read`; job-level override on `publish` only |

---

## Version Compatibility

| Action | Compatible with | Notes |
|--------|-----------------|-------|
| `docker/build-push-action@v6` | `setup-buildx-action@v3`, Docker Engine v23+ | Buildx is bundled in `ubuntu-24.04` runners (Docker Engine 26 as of May 2026). |
| `docker/metadata-action@v5` | Tag format strings used here (`type=raw`, `type=semver`, `type=sha`) | All three are stable since v4; v5 added expression contexts we use. |
| `docker/login-action@v3` | GHCR (`ghcr.io`), Docker Hub, ACR, ECR | We use GHCR only. |
| `golangci/golangci-lint-action@v6` | `golangci-lint v1.55+` | Floor is well below our v1.62.x pin. |
| `actions/setup-go@v5` | Go 1.22, 1.23, 1.24, 1.25, 1.26 | We pin `1.26`. |
| `actions/setup-node@v4` | Node 16, 18, 20, 22, 24 | We pin `22` (Phase 1 baseline). |
| `crane` v0.20.8 | go-containerregistry library at the matching tag | Same module/tag the Phase 3 binary depends on — single point of truth for anonymous bearer-token flow behavior. |

---

## Confidence assessment

| Recommendation | Confidence | Verified against |
|----------------|------------|------------------|
| `metadata-action@v5` three-tag config | HIGH | `docker/metadata-action` README, tested patterns in widespread use |
| `build-push-action@v6` + GHA cache backend | HIGH | `docker/build-push-action` README, multiple production users |
| `workflow_run` for publish chaining | HIGH | GitHub Actions docs, used in `actions/toolkit` itself |
| Anonymous-flow invariants (no `GITHUB_TOKEN`, no `~/.docker/config.json`) | HIGH | `go-containerregistry/pkg/authn` source, GHCR docs |
| Frozen anchor choice (`ghcr.io/distroless/static-debian12:nonroot`) | HIGH | The image is the project's production base; if it ever breaks, we have bigger problems |
| `golangci-lint-action@v6` with `v1.62.x` floor | MEDIUM-HIGH | Action README; minor-version floor is conservative |
| `provenance: false` / `sbom: false` for v1 | HIGH | brief explicitly defers supply-chain artifacts |
| Two-workflow split over single gated workflow | MEDIUM | Reasoned from concurrency isolation; could be re-litigated if the team prefers single-workflow simplicity |

---

## Brief's choices — confirmed or corrected

| Brief / CONTEXT said | Verdict | Action |
|----------------------|---------|--------|
| `docker/build-push-action@v6` | Correct | Use; pin `@v6`. |
| `docker/metadata-action@v5` | Correct | Use; pin `@v5`; config above. |
| `actions/setup-go@v5` with `go-version: '1.26'` | Correct | Match Phase 1 baseline. |
| `actions/setup-node@v4` with `node-version: '22'` | Correct | Match Phase 1 baseline. |
| `docker/login-action@v3` | Correct | Use in `publish.yml` only. |
| `actions/checkout@v4` | Correct | Phase 1 baseline. |
| Real-GHCR smoke via `crane digest` | Correct | Pin `@v0.20.8` (Phase 3 parity). |
| Smoke job with no `GITHUB_TOKEN` | **Underspecified — add `if [ -n "$GITHUB_TOKEN" ]` assertion** | Belt-and-braces assertion at top of smoke step. |
| Frozen anchor for PR smoke | Correct | Use `ghcr.io/distroless/static-debian12:nonroot`. |
| Manual-smoke gate in `RELEASING.md` | Correct | Plan 08-03 ships the doc. |

---

## Sources

- [docker/metadata-action](https://github.com/docker/metadata-action) — tag grammar, expression contexts (HIGH)
- [docker/build-push-action](https://github.com/docker/build-push-action) — GHA cache, push, provenance/sbom flags (HIGH)
- [docker/login-action](https://github.com/docker/login-action) — GHCR auth via `GITHUB_TOKEN` (HIGH)
- [GitHub Actions: workflow_run](https://docs.github.com/en/actions/using-workflows/events-that-trigger-workflows#workflow_run) — chaining workflows (HIGH)
- [GitHub Actions: permissions](https://docs.github.com/en/actions/using-jobs/assigning-permissions-to-jobs) — job-level least-privilege (HIGH)
- [GitHub Container Registry: authenticating](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry) — anonymous read flow (HIGH)
- [go-containerregistry: pkg/v1/remote transport](https://pkg.go.dev/github.com/google/go-containerregistry/pkg/v1/remote) — bearer-token round-tripper, `authn.Anonymous` (HIGH)
- [go-containerregistry: cmd/crane](https://github.com/google/go-containerregistry/tree/main/cmd/crane) — `crane digest` source (HIGH)
- [golangci/golangci-lint-action](https://github.com/golangci/golangci-lint-action) — action README, version compatibility (MEDIUM-HIGH)
- [.planning/research/STACK.md](../../research/STACK.md) — project-level CI/CD pins (HIGH, internal)
- [.planning/research/PITFALLS.md](../../research/PITFALLS.md) — Pitfall 2 reference (HIGH, internal)
- [.planning/phases/03-registry-polling-update-detection/03-CONTEXT.md](../03-registry-polling-update-detection/03-CONTEXT.md) — Phase 3 unit-test guard for Pitfall 2, lives upstream of Phase 8's live smoke (HIGH, internal)

---
*Research for: hmi-update Phase 8 — CI/CD & Release Hardening*
*Researched: 2026-05-15*
