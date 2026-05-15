# Phase 7 — Deployment & Packaging: CONTEXT

**Phase:** 07-deployment-packaging
**Depends on:** Phase 6 (Display-Blackout UX Checkpoint)
**Drives requirements:** DEPLOY-01..DEPLOY-09
**Output discipline:** Production-grade single OCI image (<30 MB), production compose deployment block, and an `Installation on an HMI` runbook in README that takes a clean Debian 12 box to a working `:8080` UI with one `id -g docker` step.

---

## 1. What ships in Phase 7

| Artifact | Path | Purpose |
|----------|------|---------|
| Production Dockerfile (hardened) | `Dockerfile` | Reuses Phase 1's multi-stage shape; adds version-injection ldflags, `.dockerignore`, `STOPSIGNAL`, OCI labels, exact base-image SHA pins |
| `.dockerignore` | `.dockerignore` | Excludes `.git/`, `node_modules/`, `e2e/`, `bin/`, `*.md` to shrink build context + final image |
| Production compose example | `docker-compose.example.yml` | The reference deployment block the brief §F7 mandates; operators copy + edit before `docker compose up -d` |
| README install runbook | `README.md` | "Installation on an HMI" section + manual self-upgrade pointer to PROJECT.md |
| Portability e2e spec | `e2e/tests/deploy-portability.spec.ts` | Red-first: brings up the production compose example on a clean directory, asserts table renders at `:8080` |
| CI image-size + idle-RAM gates | `.github/workflows/ci.yml` additions | After image build, `docker image inspect ... --format '{{.Size}}'` < 30_000_000; idle-RAM probe < 30 MiB |
| Makefile target | `make image-prod` (or `make release-image`) | Stamps `:vX.Y.Z` via `VERSION` env-var; same Dockerfile as dev image, different build args |

**Phase 7 does NOT ship:**
- arm64 builds (deferred to V2-ARM64; CI is amd64-only per CLAUDE.md "Platform")
- Authentication / TLS (LAN-only per N5)
- GitHub Actions semver tag-publishing flow (Phase 8 owns CI-02 / CI-03)
- A self-upgrade endpoint in the binary (ACT-09 forbids it; Phase 4 already documented the host-shell procedure in PROJECT.md)

---

## 2. Locked design decisions

### 2.1 Final stage: `gcr.io/distroless/static-debian12:nonroot`

Pinned to `static-debian12` (NOT the unversioned `static:nonroot`, which silently follows whichever Debian is current). STACK.md §"Container image" + Pitfall research confirm this. Migration to `static-debian13:nonroot` is a future-milestone concern.

The base image SHOULD additionally be pinned by digest (`gcr.io/distroless/static-debian12:nonroot@sha256:...`) for full reproducibility — Plan 07-01 captures the current digest at execute time and commits it.

### 2.2 Build flags (Go)

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${SHA} -X main.builtAt=${BUILT_AT}" \
    -tags="" \
    -o /out/hmi-update ./cmd/hmi-update
```

| Flag | Purpose |
|------|---------|
| `CGO_ENABLED=0` | Static binary — needed for distroless static-debian12 (no glibc) |
| `-trimpath` | Removes `/Users/jonb/Projects/tmp/...` from stack traces; aids reproducibility |
| `-ldflags="-s -w"` | Strips debug symbols + DWARF; ~30 % size reduction |
| `-X main.version=...` | Stamps semver into binary; surfaces in `/api/version` (Phase 7 may add) or in slog boot line |
| `-tags=""` (explicit empty) | Enforces production build excludes the `debug` build tag — `internal/api/debug_compose.go` (`//go:build debug`) MUST be absent from the final image (T-02-04-02 invariant) |

**Production-binary invariant test (Plan 07-01 acceptance):** `strings hmi-update | grep -c compose-stat` returns 0 against the final image.

### 2.3 `docker compose` CLI presence inside the container — bind-mount the host binary

Phase 4's `internal/compose.Runner` shells out to `docker compose <subcommand>` via `exec.CommandContext`. The distroless image has no `docker` binary. Three options were evaluated:

| Option | Image-size impact | Verdict |
|--------|-------------------|---------|
| **A. Bind-mount host docker binary** (`/usr/bin/docker:/usr/bin/docker:ro`) | 0 MB | **Chosen** — matches the docker.sock bind-mount precedent. Modern Debian ships a statically-linked `docker` CLI; the `compose` CLI plugin lives at `/usr/libexec/docker/cli-plugins/docker-compose` (also static) and is also bind-mounted read-only. |
| B. Install `docker.io` in the image via `apt-get install --no-install-recommends docker.io` (forces `cc-debian12:nonroot`) | +60–80 MB | Falls back to this if Option A surfaces brittleness during portability e2e on a real HMI. Documented as `<deferred>` in §6. |
| C. Statically link `docker` + `compose` into the binary | Massive: BuildKit + containerd Go deps, blows budget | Rejected — same dependency-tree argument as the Compose-SDK rejection in STACK.md. |

The compose example MUST mount BOTH `/usr/bin/docker:ro` AND `/usr/libexec/docker/cli-plugins:ro` because Compose v2 is a CLI plugin, not a sub-command of the docker binary.

### 2.4 `docker-compose.example.yml` — the reference deployment block

Lives at repo root. Operators copy this file to their HMI, edit the bind-mount paths to match local layout, then `docker compose up -d`. Must match brief §F7 shape exactly:

```yaml
# docker-compose.example.yml
# Copy to your HMI host (e.g. /opt/centroid/docker-compose.yml), edit the
# bind-mount paths to match your existing compose stack, then:
#
#   chown 65532:65532 /opt/centroid/hmi-update_state.json   # one-time
#   docker compose up -d hmi-update
#
# See README.md §Installation on an HMI for the full runbook.

services:
  hmi-update:
    image: ghcr.io/centroid-is/hmi-update:latest
    container_name: hmi-update
    restart: unless-stopped
    ports:
      - "8080:8080"
    # 65532 = distroless nonroot UID; <docker-gid> = `id -g docker` on the host.
    # See README.md §Installation on an HMI step 2.
    user: "65532:<docker-gid>"
    volumes:
      # Docker socket — required for the daemon-side facade
      - /var/run/docker.sock:/var/run/docker.sock
      # Compose file (read-only — never mutated by hmi-update)
      - /opt/centroid/docker-compose.yml:/host/docker-compose.yml:ro
      # State file (bind-mounted, persists across recreate)
      - /opt/centroid/hmi-update_state.json:/state/hmi_update_state.json
      # Host docker CLI + compose plugin (read-only; see §2.3)
      - /usr/bin/docker:/usr/bin/docker:ro
      - /usr/libexec/docker/cli-plugins:/usr/libexec/docker/cli-plugins:ro
    environment:
      HMI_UPDATE_CRON: "0 * * * *"
      HMI_UPDATE_COMPOSE_PATH: "/host/docker-compose.yml"
      HMI_UPDATE_STATE_PATH: "/state/hmi_update_state.json"
    labels:
      # Self-protection: hmi-update will NOT watch or attempt to recreate itself.
      # (Server-enforced via ACT-09 self-protection 409 regardless of this label,
      # but the label keeps the row out of the watched-list display.)
      hmi-update.watch: "false"
```

Exact label / env / ports / image-ref / bind-mount set is the DEPLOY-04 acceptance surface.

### 2.5 README "Installation on an HMI" section

Drop-in canonical content (Plan 07-03 writes it; minor wording allowed):

```markdown
## Installation on an HMI

Tested on Debian 12 with Docker Engine v29+ and the docker-compose-plugin.

### 1. Get the docker group GID

    HOST_DOCKER_GID=$(id -g docker)
    echo "docker GID is ${HOST_DOCKER_GID}"

### 2. Place the compose snippet and state file

    sudo mkdir -p /opt/centroid
    sudo cp docker-compose.example.yml /opt/centroid/docker-compose.yml
    sudo touch /opt/centroid/hmi-update_state.json
    sudo chown 65532:65532 /opt/centroid/hmi-update_state.json

Edit `/opt/centroid/docker-compose.yml` and replace `<docker-gid>` in the
`user:` line with the GID from step 1.

### 3. Start

    cd /opt/centroid
    docker compose up -d hmi-update

### 4. Verify

    curl -s http://localhost:8080/healthz   # → 200
    xdg-open http://localhost:8080          # table view; empty until watched containers boot

### 5. Manual self-upgrade

`hmi-update` cannot recreate itself via the API (it is the process being
recreated — it would commit suicide mid-recreate, see Pitfall 6 / ACT-09).
The documented upgrade procedure lives in
[PROJECT.md §Manual self-upgrade procedure](.planning/PROJECT.md).
```

### 2.6 Image-size CI gate (DEPLOY-02)

After `docker build`, CI runs:

```bash
SIZE=$(docker image inspect ghcr.io/centroid-is/hmi-update:sha-${SHA} --format '{{.Size}}')
echo "image size: ${SIZE} bytes"
test "${SIZE}" -lt 30000000 || { echo "FAIL: image > 30 MB"; exit 1; }
```

If this assertion fails consistently with `static-debian12`, the fallback is documented in §6 `<deferred>`.

### 2.7 Idle-RAM CI gate (DEPLOY-03)

After `compose up -d --wait`, wait 60 s for the cron to settle, then:

```bash
MEM=$(docker stats --no-stream --format '{{.MemUsage}}' hmi-update | awk '{print $1}')
# Parse 12.34MiB / 56.78MiB into bytes-ish; threshold is 30 MiB.
```

The 60 s settle is important: poll workers warm up under cron the first tick, allocate the slog buffer, and the working-set stabilises by the second cron tick. Measuring at t=10 s catches startup transients.

### 2.8 Self-upgrade procedure — already documented (Phase 4)

Phase 4's Plan 04-05 added `## Manual self-upgrade procedure` to PROJECT.md (already on disk). Phase 7 README links to it; does not duplicate the content. The wording in PROJECT.md is the canonical source.

---

## 3. The portability acceptance test (DEPLOY-05 / Acceptance criterion 6)

This is the Phase 7 RED-first e2e. Spec lives at `e2e/tests/deploy-portability.spec.ts`. Sketch:

```ts
import { test, expect } from "@playwright/test";
import { execSync } from "child_process";
import * as fs from "fs";
import * as path from "path";
import * as os from "os";

test("DEPLOY-05: production compose example boots cleanly on a fresh dir", async ({ request }) => {
  // 1. Create a fresh tempdir representing a "clean Debian 12 host"
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "hmi-portability-"));

  // 2. Copy docker-compose.example.yml in, substituting the docker GID
  const dockerGid = execSync(
    `docker run --rm -v /var/run/docker.sock:/var/run/docker.sock --entrypoint stat alpine -c '%g' /var/run/docker.sock`,
    { encoding: "utf-8" }
  ).trim();
  const compose = fs.readFileSync("../docker-compose.example.yml", "utf-8")
    .replace("<docker-gid>", dockerGid)
    .replace("ghcr.io/centroid-is/hmi-update:latest", "hmi-update:portability"); // local build, not GHCR
  // Also swap bind-mount paths to point INTO the tempdir
  // ... (full text manipulation, see Plan 07-03 acceptance)
  fs.writeFileSync(path.join(tmp, "docker-compose.yml"), compose);
  fs.writeFileSync(path.join(tmp, "hmi_update_state.json"), "");

  // 3. Bring it up
  execSync(`docker compose -f ${path.join(tmp, "docker-compose.yml")} up -d --wait`);

  try {
    // 4. Assert /healthz + table at :8081 (port-shifted to avoid e2e collision)
    const resp = await request.get("http://localhost:8081/healthz");
    expect(resp.status()).toBe(200);

    const page = await request.get("http://localhost:8081/");
    expect(page.status()).toBe(200);
    expect(await page.text()).toContain("hmi-update"); // page title or table header
  } finally {
    execSync(`docker compose -f ${path.join(tmp, "docker-compose.yml")} down -v`);
    fs.rmSync(tmp, { recursive: true, force: true });
  }
});
```

Notes:
- The test uses host port 8081 (not 8080) to avoid colliding with the main e2e compose stack which already binds 8080 via `compose.test.yml`.
- The test runs in CI on `ubuntu-24.04` (Debian-derived, has `docker` + `docker compose` plugin preinstalled). For a stricter "different host" simulation, Plan 07-03 may opt to spin up a Debian 12 container with Docker-in-Docker — left as a `<deferred>` enhancement.
- The local image is built once by the e2e job (`docker build -t hmi-update:portability .`); portability test then references it locally.

---

## 4. Build matrix (production vs. dev image)

The same `Dockerfile` produces all variants — only `--build-arg` differs.

| Make target | Build args | Image tag | Purpose |
|-------------|------------|-----------|---------|
| `make image` | (none) | `hmi-update:dev` | Phase 1 dev image; same Dockerfile, no debug routes |
| `make image-debug` | `GO_TAGS=debug` | `hmi-update:dev-debug` | Plan 02-04 debug build for `/debug/compose-stat` |
| `make image-prod` (NEW — Plan 07-01) | `VERSION=${VERSION}` `SHA=${SHA}` | `hmi-update:v${VERSION}` | Production build with version/commit/builtAt ldflags |
| CI image-build step | `VERSION=${{semver}}` `SHA=${{shortsha}}` | `ghcr.io/centroid-is/hmi-update:{sha,semver,latest}` | Phase 8's publish flow (Phase 7 only provides the Dockerfile hooks; tags come in Phase 8) |

---

## 5. STATE-05 / Pitfall 9 — the install-time gotchas

The state file (host bind-mount) and docker socket access are the two operator-facing failure modes:

### State file UID/GID

The container runs as UID 65532 (distroless nonroot). The bind-mounted host state file MUST be owned by `65532:65532` or the container hits `EACCES` on first write. Install runbook step 2 documents `chown 65532:65532 /opt/centroid/hmi-update_state.json` (already covered in PROJECT.md from Phase 4; README install runbook reiterates).

### docker.sock GID

The bind-mounted `/var/run/docker.sock` is owned by `root:docker` (GID varies per host — `id -g docker` resolves it). The container must run with that supplementary GID or `docker.NewClient()` returns `permission denied`. Install runbook step 1 (`id -g docker`) → step 3's `user: "65532:<docker-gid>"` is the canonical pattern. `/healthz` emits the Pitfall 9 remediation hint when this is wrong.

---

## 6. Open items / `<deferred>`

<deferred>
## Phase 7 deferred items

- **D-07-01:** Falling back to `gcr.io/distroless/cc-debian12:nonroot` + `apt-get install --no-install-recommends docker.io` ONLY IF (a) Plan 07-01's image-size measurement against `static-debian12` + the bind-mount-docker compose pattern still trips the 30 MB cap, or (b) the portability e2e surfaces brittleness from the host-docker-bind approach on a real HMI. Both are MEDIUM-probability — STACK.md predicts ~10–14 MB for `static-debian12` with the embedded Svelte bundle; the bind-mount approach is precedented (k3s, ctop) but un-tested on Centroid's HMI image. If we fall back, the cc-debian12 variant adds ~60–80 MB and we will breach the N6 image-size cap — at that point the cap itself becomes a `<deferred>` item to renegotiate with the user (the brief's 30 MB number was set against a hypothetical "binary-only" image; an image that ships the docker CLI is a different shape).

- **D-07-02:** arm64 multi-arch builds. amd64-only per CLAUDE.md "Platform"; arm64 is a `docker buildx build --platform linux/amd64,linux/arm64` flip in Phase 8's CI workflow when ARM HMI hardware lands. Mentioned here only because the production Dockerfile is the artifact that needs to be arm64-clean (no architecture-specific tools in the builder stages; `CGO_ENABLED=0` + `GOARCH=$TARGETARCH` is sufficient).

- **D-07-03:** TLS / auth — out of scope per N5 (LAN-only, unauthenticated). If a future site requires LAN-edge TLS, the answer is a reverse proxy (Caddy / Traefik) on the HMI; `hmi-update` itself stays unauthenticated on `:8080` over plain HTTP.

- **D-07-04:** Healthcheck baked into the binary (`hmi-update --healthcheck` flag). Currently the production compose example omits `healthcheck:` because the distroless image has no `wget`/`curl`/shell to invoke. A self-probe flag would let compose's `healthcheck.test: ["CMD", "/hmi-update", "--healthcheck"]` work and surface `unhealthy` status at the daemon layer. Left for Phase 8 or V2; not blocking DEPLOY-* acceptance.

- **D-07-05:** SBOM emission + signing. `docker/build-push-action@v6` supports `sbom: true` + `provenance: mode=max`. Defer to Phase 8's CI hardening — Phase 7 produces the Dockerfile and compose deployment block; Phase 8 wraps the publish-side hardening.

- **D-07-06:** Detection-only mode for HMIs without docker socket access. Out of scope for v1.
</deferred>

---

## 7. Files-on-disk inventory (what Phase 7 changes)

```
Dockerfile                       — REWRITE (Phase 1 dev → Phase 7 production-hardened)
.dockerignore                    — NEW
docker-compose.example.yml       — NEW (the brief §F7 reference deployment block)
README.md                        — REWRITE or NEW (Phase 1 is empty; Phase 7 adds Installation runbook)
e2e/tests/deploy-portability.spec.ts — NEW (DEPLOY-05 red-first spec)
Makefile                         — APPEND (new `image-prod` target)
.github/workflows/ci.yml         — APPEND (size + RAM gates)
```

Files Phase 7 explicitly does NOT touch:
- `cmd/hmi-update/main.go` — version-injection ldflags target a `main.version` variable that may or may not already exist; if absent, Plan 07-01 adds a single `var version = "dev"` line (no functional change)
- `internal/**` — production hardening is a build/packaging concern, not a code concern
- `PROJECT.md` — Phase 4's self-upgrade section is canonical; README links to it
- `e2e/compose.test.yml` — the test stack stays unchanged; the portability spec uses its OWN compose file (loaded from `docker-compose.example.yml`)

---

## 8. Sequencing inside the phase

```
Wave 1: 07-01 — Production Dockerfile + .dockerignore + image-prod Make target + version ldflags
                  (foundation; nothing else can be size-measured until this lands)

Wave 2: 07-02 — docker-compose.example.yml (brief §F7 exact match)
        07-03 — README install runbook + portability e2e + CI size/RAM gates
                  (07-02 and 07-03 run in parallel — different file scopes; 07-03 references 07-02's
                   docker-compose.example.yml in its portability spec, but the spec is RED-first
                   anyway and waits for 07-02's file to exist on disk via the wave gate)
```

Phase 7's final acceptance gate is `make e2e` green (deploy-portability.spec.ts) + the CI image-size gate green on the same image.

---

## 9. Cross-phase references

- **Phase 1 (FOUND-04, FOUND-05):** Existing Dockerfile is the starting point; Phase 7 hardens it. `//go:embed all:dist` is unchanged.
- **Phase 4 (ACT-09 self-protection, PROJECT.md self-upgrade):** README points to PROJECT.md's existing self-upgrade section; no duplication.
- **Phase 4 (STATE-05):** State file `chown 65532:65532` is already documented in PROJECT.md; README install runbook reiterates inside the step-by-step.
- **Phase 8 (CI-02 publish flow):** Phase 7 leaves placeholder values (`hmi-update:portability` for local; `ghcr.io/centroid-is/hmi-update:latest` for the compose example). Phase 8's `docker/metadata-action@v5` + `docker/build-push-action@v6` then fills in semver / sha / latest tag publishing.
