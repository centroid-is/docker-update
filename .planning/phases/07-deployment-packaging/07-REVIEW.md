---
phase: 07-deployment-packaging
reviewed: 2026-05-15T12:16:29Z
depth: standard
files_reviewed: 8
files_reviewed_list:
  - Dockerfile
  - .dockerignore
  - Makefile
  - cmd/hmi-update/main.go
  - docker-compose.example.yml
  - README.md
  - e2e/tests/deploy-portability.spec.ts
  - .github/workflows/ci.yml
  - e2e/compose.test.yml
findings:
  critical: 2
  warning: 5
  info: 4
  total: 11
status: issues_found
fixed:
  at: 2026-05-15T12:20:00Z
  scope: critical_warning
  critical_fixed: 2
  warnings_fixed: 5
  info_deferred: 4
  fixes:
    - id: CR-01
      status: fixed
      commit: a56c52d
      files: [CLAUDE.md, .planning/PROJECT.md, Dockerfile, API.md, .planning/REQUIREMENTS.md, internal/registry/resolver.go]
      note: |
        Operative artifacts aligned with ghcr.io/centroid-is/docker-update.
        PROJECT.md Key Decisions entry added documenting the image-path / binary-name split.
        Historical phase docs (PLAN/RESEARCH/SUMMARY for prior phases) intentionally left
        untouched as a historical record; ROADMAP.md and STATE.md untouched per fix-pass
        constraints.
    - id: CR-02
      status: fixed
      commit: 45a17f8
      files: [README.md]
    - id: WR-01
      status: fixed
      commit: dd28329
      files: [.github/workflows/ci.yml]
      note: awk logic manually verified across KiB/MiB/GiB/garbage/empty inputs
    - id: WR-02
      status: fixed
      commit: 3e5ef43
      files: [e2e/tests/deploy-portability.spec.ts]
    - id: WR-03
      status: fixed
      commit: 60fd80d
      files: [e2e/tests/deploy-portability.spec.ts]
    - id: WR-04
      status: fixed
      commit: 5690de5
      files: [.dockerignore]
    - id: WR-05
      status: fixed
      commit: a682693
      files: [e2e/compose.test.yml]
      note: env-var-overridable bind-mounts default to Linux paths; macOS dev opt-in
---

# Phase 7: Code Review Report

**Reviewed:** 2026-05-15T12:16:29Z
**Depth:** standard
**Files Reviewed:** 8 (+1 cross-reference: e2e/compose.test.yml)
**Status:** issues_found

## Summary

Phase 7 ships the production hardening: 3-stage Dockerfile (node → golang → distroless-static-debian12:nonroot), `.dockerignore`, `make image-prod`, version-injection via `-ldflags -X`, OCI labels, STOPSIGNAL, nonroot USER 65532, `docker-compose.example.yml`, README install runbook, env-gated portability spec, and three new CI gates (image-size, idle-RAM, portability). Measured image size 4.4 MB — well under the 30 MB cap.

However, the implementation contains two **BLOCKER**-class defects:

1. **Image-name rebrand violates the project's locked Constraint** in CLAUDE.md (the canonical image path is `ghcr.io/centroid-is/hmi-update`, NOT `ghcr.io/centroid-is/docker-update`). The compose example, CI metadata, and publish.yml all use the rebranded path while the Dockerfile OCI label (`org.opencontainers.image.source`) still references `github.com/centroid-is/hmi-update` — i.e. the artifacts are internally inconsistent AND drift from the brief. Operators following the README will pull from a non-existent / unauthorized GHCR path.
2. **The README install runbook contains a misleading code snippet** that, copied verbatim by an operator, produces an invalid `user:` value at runtime — defeating DEPLOY-08's load-bearing acceptance surface.

Five additional WARNING-class defects: idle-RAM gate sed parser misfires on KiB/GiB MemUsage output; portability spec uses `--timeout` on `docker compose up` where it has no effect; Phase 7-01's removal of the docker-cli-stage breaks `make e2e` on platforms where the host paths don't exist (mitigated by the `e2e/compose.test.yml` bind-mount add but unverified on macOS); `.dockerignore` `*.md` exclusion is over-broad; portability spec `.replace()` substitutions are brittle to compose-file edits. T-02-04-02 invariant verified preserved; `mime.AddExtensionType` calls intact (5 calls, lines 127/130/132/136/139).

## Critical Issues

### CR-01: Image-name rebrand contradicts locked CLAUDE.md constraint and creates internal label inconsistency

**Files:**
- `docker-compose.example.yml:21` — `image: ghcr.io/centroid-is/docker-update:latest`
- `README.md:21` — `The published image lives at ghcr.io/centroid-is/docker-update:latest.`
- `.github/workflows/ci.yml:64` — `images: ghcr.io/centroid-is/docker-update`
- `.github/workflows/publish.yml:51,59,67,88,132,150` — multiple references to `ghcr.io/centroid-is/docker-update`
- `Dockerfile:85` — `LABEL org.opencontainers.image.source="https://github.com/centroid-is/hmi-update"` (this one is correct per CLAUDE.md)

**Issue:** CLAUDE.md (the locked project constraint) names the published image as `ghcr.io/centroid-is/hmi-update` ("Image published to `ghcr.io/centroid-is/hmi-update` with `:latest` tracking main, `:vX.Y.Z` per release, `:sha-<short>` per commit") and the repo as `centroid-is/hmi-update`. Phase 7 artifacts ship `ghcr.io/centroid-is/docker-update`, citing a "rebrand" in 07-02-SUMMARY key-decisions — but neither CLAUDE.md nor any phase CONTEXT documents the rebrand as a locked decision. Worse, the Dockerfile's OCI label still references the original `hmi-update` path while the compose example + CI metadata reference the rebranded path. The result:

- An operator copy-pasting `docker-compose.example.yml` will try to pull `ghcr.io/centroid-is/docker-update:latest`, which (a) does not match the brief, (b) does not match the Dockerfile's `org.opencontainers.image.source` label, and (c) will not exist on GHCR until/unless someone publishes there.
- Vulnerability scanners that follow `image.source` will look at `github.com/centroid-is/hmi-update` for the supply-chain trail and find no Phase-7+ tagged image at the corresponding `ghcr.io/centroid-is/hmi-update` path.
- The portability spec's substitution targets `ghcr.io/centroid-is/docker-update:latest` — if a future maintainer fixes the rebrand by reverting the compose example to `hmi-update`, the spec silently no-ops on the `.replace()` and tests against the published image (wrong).

The "rebrand" cannot self-authorize against a LOCKED constraint in CLAUDE.md. Either CLAUDE.md must be updated with an explicit decision-record entry naming the rebrand and rationale, or all Phase 7 artifacts must be brought back to `ghcr.io/centroid-is/hmi-update`.

**Fix:** Either:

(a) Revert the rebrand across `docker-compose.example.yml`, `README.md`, `.github/workflows/ci.yml`, `.github/workflows/publish.yml` to use `ghcr.io/centroid-is/hmi-update`. Update `e2e/tests/deploy-portability.spec.ts` line 88 substitution target accordingly. This is the constraint-conformant fix.

(b) If the rebrand is intentional and authoritative, update CLAUDE.md's "Repo" constraint line, update PROJECT.md Key Decisions with a documented decision-record entry, and fix `Dockerfile:85` `org.opencontainers.image.source` to point at the new repo URL.

**Severity rationale:** BLOCKER because (1) it violates a LOCKED project constraint without a decision-record entry, (2) the internal artifact inconsistency (Dockerfile label vs. compose example vs. CI publish target) will produce a confusing supply-chain trail, and (3) the portability spec's silent-noop-on-mismatch behavior means CI cannot detect a future correction.

### CR-02: README runbook snippet uses `${HOST_DOCKER_GID}` in the example user line, producing invalid `user:` at runtime

**File:** `README.md:42-47`
**Issue:** Step 2 of the install runbook tells the operator to "edit `/opt/centroid/docker-compose.yml` and replace `<docker-gid>` in the `user:` line with the GID from step 1." The displayed example code block, however, shows:

```yaml
    user: "65532:${HOST_DOCKER_GID}"   # e.g. "65532:998"
```

An operator copy-pasting this literally into the compose file produces a compose file that does NOT have a hardcoded GID — it has a `${HOST_DOCKER_GID}` variable reference. Docker Compose v2 will attempt to interpolate this variable from the shell environment at `docker compose up` time. The runbook's preceding step sets `HOST_DOCKER_GID=$(id -g docker)` in step 1 — but if the operator (1) starts a fresh shell between step 1 and step 3, (2) reboots, (3) runs `docker compose up` from a systemd unit that doesn't inherit the env var, or (4) follows the substitute literally without realizing they need to substitute, compose will resolve `${HOST_DOCKER_GID}` to the empty string, producing `user: "65532:"` — which is rejected by the daemon (or, depending on compose version, silently dropped, leaving the container running as the distroless default user without supplementary docker GID → `/healthz` returns 503 EACCES on docker.sock).

The intent of `<docker-gid>` in `docker-compose.example.yml` is to be a literal placeholder — the operator substitutes the resolved integer. The README example fragment defeats this by showing `${HOST_DOCKER_GID}` as if it were the substitution.

**Fix:** Change the README example to show the substituted literal:
```diff
 ```yaml
-    user: "65532:${HOST_DOCKER_GID}"   # e.g. "65532:998"
+    user: "65532:998"   # replace 998 with the value of ${HOST_DOCKER_GID}
 ```
```

Or, if the intent is to allow env-var interpolation, document that the operator must `export HOST_DOCKER_GID` in the shell that runs `docker compose up` AND in any systemd / supervisor unit that auto-restarts the stack — and add a verification step.

**Severity rationale:** BLOCKER because DEPLOY-08's load-bearing acceptance surface is "the `id -g docker` step is the only host-side edit required" (07-03 SUMMARY threat T-07-03-01 manual smoke checklist item 4). The README snippet, as written, can produce a runtime failure that the operator will not understand without reading PROJECT.md Pitfall 9. This is the exact "operator-surprise on a clean Debian 12 box" failure mode the Phase 7 portability gate exists to prevent.

## Warnings

### WR-01: Idle-RAM CI gate sed regex spuriously fails on non-MiB MemUsage output

**File:** `.github/workflows/ci.yml:173`
**Issue:** The gate parses `docker stats --no-stream --format '{{.MemUsage}}'` output with the regex `^([0-9]+)(\.[0-9]+)?MiB$`. If MemUsage is reported in any unit other than MiB (which is what `docker stats` does for memory below ~1 MiB → KiB; above ~1 GiB → GiB), the sed expression returns the input unchanged. The downstream integer check then fails with "FAIL: could not parse" — exit 1 — even when the actual memory usage is FAR under the 30 MiB cap. Verified empirically:

| Input | sed output | grep `^[0-9]+$` |
|-------|-----------|-----------------|
| `512KiB` | `512KiB` | fails → false-FAIL the gate |
| `12.34MiB` | `12` | passes (correct) |
| `1.2GiB` | `1.2GiB` | fails → caught, but wrong error message |
| `12MiB` | `12` | passes (correct) |

A Go binary with very low idle resident set (which STACK.md predicts as plausible) could legitimately land at e.g. `8.5MiB` or `850KiB` and false-FAIL CI. The 07-03 SUMMARY's note "If [the gate fails], Phase 8 may want to widen to 35 MiB" misses this entirely — the failure mode it predicts is "exceeds cap"; the actual failure mode is "regex doesn't match."

**Fix:** Generalize the parser to convert KiB/MiB/GiB to a comparable unit (e.g., bytes) before comparing. Example:
```bash
# Convert "12.34MiB" / "512KiB" / "1.2GiB" → KiB integer
MEM_KIB=$(echo "${MEM_RAW}" | awk '
  /KiB$/ { sub(/KiB$/,""); printf "%d", $1; exit }
  /MiB$/ { sub(/MiB$/,""); printf "%d", $1 * 1024; exit }
  /GiB$/ { sub(/GiB$/,""); printf "%d", $1 * 1024 * 1024; exit }
  { print "unparseable"; exit 1 }
')
THRESHOLD_KIB=$((30 * 1024))
if [ "${MEM_KIB}" -ge "${THRESHOLD_KIB}" ]; then
  echo "FAIL: idle RAM ${MEM_RAW} exceeds 30 MiB cap (DEPLOY-03)" >&2
  exit 1
fi
```
**Severity rationale:** Warning rather than Blocker because in practice the binary likely lands in the MiB range so the gate works for the expected case. But the failure mode (CI fails noisily when the binary is actually well-behaved) is non-trivial and indistinguishable from a real "exceeds cap" failure to a casual reader.

### WR-02: `deploy-portability.spec.ts` uses `--timeout 60` on `docker compose up`, which is not the wait-timeout

**File:** `e2e/tests/deploy-portability.spec.ts:123`
**Issue:** The spec runs:
```ts
execSync(`docker compose -f ${composeOut} up -d --wait --timeout 60`, ...);
```
`docker compose up --timeout <seconds>` is the **stop**-timeout (SIGTERM-to-SIGKILL grace for any existing containers being recreated), NOT the `--wait` timeout. The flag has no effect on how long `--wait` blocks for healthchecks. The correct flag is `--wait-timeout 60`. With no wait-timeout set, `--wait` uses its compose-default behavior (typically infinite or implementation-defined). On CI runners with no test budget enforcement, a stuck container would hang `execSync` indefinitely until the Playwright global test timeout fires.
**Fix:**
```diff
-      execSync(
-        `docker compose -f ${composeOut} up -d --wait --timeout 60`,
-        { stdio: 'inherit' },
-      );
+      execSync(
+        `docker compose -f ${composeOut} up -d --wait --wait-timeout 60`,
+        { stdio: 'inherit' },
+      );
```
**Severity rationale:** Warning because the spec has its own 60s healthz-polling budget that bounds the test, and the `down --timeout 30` usage on line 162 IS correct (down's `--timeout` IS the stop-timeout). But the misused flag is a bug-shaped misunderstanding and a future maintainer touching this spec will likely copy the wrong pattern.

### WR-03: Portability spec `.replace()` substitutions are brittle to compose-file edits

**File:** `e2e/tests/deploy-portability.spec.ts:86-100`
**Issue:** The substitution pipeline:
```ts
compose = compose
  .replace('ghcr.io/centroid-is/docker-update:latest', 'hmi-update:portability')
  .replace('<docker-gid>', dockerGid)
  .replace('"8080:8080"', '"8081:8080"')
  .replace('/opt/centroid/docker-compose.yml:/host/docker-compose.yml:ro', `${composeOut}:/host/docker-compose.yml:ro`)
  .replace('/opt/centroid/hmi_update_state.json:/state/hmi_update_state.json', `${stateOut}:/state/hmi_update_state.json`);
```
Each `.replace()` with a string-literal target performs a single, exact-match substitution. If a future maintainer:
- changes quoting from `"8080:8080"` to `'8080:8080'` or `8080:8080` (unquoted),
- adds whitespace around the placeholder (`< docker-gid >`),
- changes the bind-mount path (e.g., to `/srv/centroid/`),
- changes the image ref (and CR-01 above is fixed),

— the `.replace()` silently no-ops (returns the unchanged string). The spec then runs against an unsubstituted compose file: the image ref points at GHCR (which may not exist locally), or the port collides with the main e2e stack on 8080, or the bind-mount sources don't exist. Several of these would produce hard-to-debug spec failures.

**Fix:** Either:

(a) Assert each replacement actually happened:
```ts
const before = compose;
compose = compose.replace('ghcr.io/centroid-is/docker-update:latest', 'hmi-update:portability');
if (before === compose) throw new Error('expected to substitute image ref but found none');
// ... repeat for each substitution
```

(b) Use regex with anchors and assert match count, e.g.:
```ts
const out = compose.replace(/^(\s*image:\s*).*$/m, '$1hmi-update:portability');
```

(c) Construct the test compose file programmatically rather than munging the operator-facing example.

**Severity rationale:** Warning because the spec is RED-against-future-drift by design, but the failure mode (silent no-op then mysterious test failure) is the worst-shape of "RED" — it produces a failure whose root cause is invisible. Asserting substitutions happen would turn the silent no-op into a clear, immediate error message.

### WR-04: `.dockerignore` `*.md` exclusion is over-broad and hides legitimate files from future stages

**File:** `.dockerignore:12`
**Issue:** The single `*.md` pattern at line 12 excludes ALL markdown files in the build context root (and recursively, depending on Docker BuildKit context interpretation — modern BuildKit treats `*.md` as matching anywhere in the tree). This currently excludes `README.md`, `API.md`, `RELEASING.md`, `SMOKE.md`, `hmi-update-brief.md`, and the `.planning/**/*.md` tree (the latter is already excluded by line 11). The pattern works today because no Go/UI source path depends on a `.md` file, but the build is one careless `//go:embed` away from a silent failure (e.g., a future "embed the API.md into the binary" change would silently produce an empty embedded file with no build error).

`.dockerignore`'s well-known idiom for documentation is to exclude top-level docs explicitly (`README.md`, `API.md`, etc.) — that's narrower and surfaces a clear failure if anything else tries to copy a markdown file.

**Fix:**
```diff
-# Planning + docs
-.planning/
-*.md
-SMOKE.md
-hmi-update-brief.md
+# Planning + docs (excluded — never needed inside the image)
+.planning/
+/README.md
+/API.md
+/RELEASING.md
+/SMOKE.md
+/hmi-update-brief.md
+/CLAUDE.md
```
The leading `/` anchors each pattern to the build-context root so a future internal package can ship a `doc.md` without surprises.
**Severity rationale:** Warning because there is no current breakage but the pattern is a latent footgun.

### WR-05: e2e/compose.test.yml bind-mounts `/usr/libexec/docker/cli-plugins` unconditionally; macOS Docker Desktop hosts have no such path

**File:** `e2e/compose.test.yml:282-283`
**Issue:** The base e2e compose file (which `make e2e`, `make e2e-cron-fast`, and the CI Idle-RAM gate all use) bind-mounts `/usr/libexec/docker/cli-plugins:/usr/libexec/docker/cli-plugins:ro`. On Linux hosts (CI), this path exists. On macOS Docker Desktop, the host filesystem does NOT have `/usr/libexec/docker/cli-plugins` — the path lives inside the Docker Desktop VM, not on the host. `docker compose up -d` with a bind-mount whose host source is missing will (a) silently create an empty directory at the host path on some Docker versions, or (b) fail with "no such file or directory" depending on Docker Desktop version.

The 07-01 SUMMARY flags this exact problem as a future follow-up: "On macOS Docker Desktop these paths don't exist on the host so a `make e2e` on Mac would surface a 'no such file or directory'" — but the patch landed without a macOS override, and the development workflow CLAUDE.md TDD constraint (C4) explicitly requires manual smoke on HMI-like stacks, plausibly run from developer Macs.
**Fix:** Either:

(a) Add a `compose.test.override.macos.yml` that omits the two CLI-delivery bind-mounts and either skips the actions tests or substitutes the alpine-stat-discovered in-VM paths.

(b) Make the bind-mounts conditional via env-var substitution:
```yaml
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
  - ./compose.test.yml:/host/docker-compose.yml:ro
  - ${HOST_DOCKER_CLI:-/usr/bin/docker}:/usr/bin/docker:ro
  - ${HOST_DOCKER_PLUGINS:-/usr/libexec/docker/cli-plugins}:/usr/libexec/docker/cli-plugins:ro
```
…and document the macOS override.

(c) Bake the docker CLI back into the image for development builds (revert Phase 7-01's removal of the docker-cli-stage, but only behind a build-arg).

**Severity rationale:** Warning because CI (Linux) is unaffected, but the failure mode for a macOS developer running `make e2e` is opaque — and the Phase 6 SUMMARY's deferred-items.md note ("`make e2e` is broken on main") suggests Mac-dev experience is already degraded. Confirming this is a current development-workflow regression would promote to Blocker.

## Info

### IN-01: `image-prod` Makefile recipe omits `--no-cache` even though VERSION/SHA change frequently

**File:** `Makefile:147-154`
**Issue:** Each `make image-prod` invocation passes new `VERSION`, `SHA`, `BUILT_AT` build-args. Docker BuildKit invalidates the RUN layer that consumes these ARGs, but earlier layers (including the entire UI build and `go mod download`) are cached. This is intentional and correct. However, there is no `--no-cache` escape hatch documented for forcing a clean rebuild — useful when a base-image floating-digest update is suspected.
**Fix:** Add a `make image-prod-clean` (or `NO_CACHE=1` env-var support) variant that prepends `--no-cache` to the docker build invocation.
**Severity rationale:** Info — this is a workflow convenience, not a correctness issue.

### IN-02: Dockerfile `COPY . .` precedes the production ARG declarations, so editing source files invalidates the version-injection layer cache

**File:** `Dockerfile:26`
**Issue:** Stage 2's order is `COPY go.mod go.sum* . ./` → `RUN go mod download` → `COPY . .` → `COPY --from=ui-builder ...` → `ARG GO_TAGS` → `ARG VERSION` → `RUN go build`. The `COPY . .` step invalidates on every source change, which is fine, but the ARG declarations live AFTER the COPY. This is actually correct (ARGs invalidate the next RUN, not the COPY) — flagging for verification only because the 07-01 SUMMARY's "build wall-clock: ~6.9s with cache" suggests the cache is mostly working. No fix needed.
**Severity rationale:** Info — verified-correct on closer reading. Recording the trace for a future maintainer.

### IN-03: `weston-stub` in `e2e/compose.test.yml` does not assert `pull_policy: never` consistency in a comment block above the line

**File:** `e2e/compose.test.yml:190-199`
**Issue:** The weston-stub service block follows the offline-resilient pattern correctly (`zot:5000/...` image + `pull_policy: never`), and the comment header explains the deviation from the original plan. But the comment header mentions "matches the offline-resilient pattern used by every other stub in this file" without explicitly grepping/listing those stubs. A future maintainer adding a new stub may miss the contract.
**Fix:** Either add a comment summary at the top of the file listing the offline-resilient invariant ("All watched stubs MUST use `zot:5000/...` image + `pull_policy: never`"), or rely on a CI lint that asserts the invariant. Defer the lint to a separate phase.
**Severity rationale:** Info — documentation polish.

### IN-04: README `## License` references a `LICENSE` file with a parenthetical that contradicts current state

**File:** `README.md:138-141`
**Issue:** Same as Phase 6 IN-02: README says "MIT — see `LICENSE` (Phase 8 publish flow lands the file alongside the GHCR release)" but a LICENSE file already exists at the repo root. Either remove the parenthetical or verify the LICENSE content matches the MIT declaration.
**Fix:**
```diff
-MIT — see `LICENSE` (Phase 8 publish flow lands the file alongside the GHCR
-release).
+MIT — see [`LICENSE`](./LICENSE).
```
**Severity rationale:** Info — overlap with Phase 6 finding; recorded here because Phase 7 extended the README and could have caught it.

---

_Reviewed: 2026-05-15T12:16:29Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_

## REVIEW COMPLETE
