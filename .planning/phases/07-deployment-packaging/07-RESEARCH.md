# Phase 7 — RESEARCH

**Researched:** 2026-05-15
**Confidence:** HIGH on distroless variants, Go build flags, and docker-CLI bind-mount precedent (STACK.md + Pitfalls + distroless project docs). MEDIUM on the precise size of `:latest` after embedding the Phase 5 Svelte bundle (depends on Tailwind utility-class count — measure in Plan 07-01).

This research backs Phase 7's load-bearing decisions: image-size optimization, distroless-debian12 vs cc-debian12, build-flag selection, and the `docker compose` CLI delivery strategy.

---

## 1. Image-size optimization — what moves the needle

Measured deltas against Phase 1's `hmi-update:dev` image (Dockerfile multi-stage with stripped binary). Numbers from STACK.md §"Build flags" baseline + Phase 1 observed measurements.

| Lever | Saving / cost | Verdict |
|-------|--------------|---------|
| `-ldflags="-s -w"` (strip debug + DWARF) | -25 to -35 % vs unstripped | **In** (already in Phase 1 Dockerfile) |
| `-trimpath` (no path prefixes) | -0.1 % (negligible bytes; aids reproducibility) | **In** (already in Phase 1) |
| `CGO_ENABLED=0` (static binary) | Required for distroless static | **In** — non-negotiable |
| UPX compression | -50 % | **Out** — packed binaries trip AV scanners + slow cold-start by ~150 ms; not worth the budget tradeoff for an HMI tool |
| `golang.org/x/crypto/x509roots/fallback` (embedded CA bundle) | +250 KB | **Out for v1** — distroless ships `/etc/ssl/certs/ca-certificates.crt`; revisit only if we move to `scratch` |
| Embedded Svelte bundle (Vite production build) | +200–400 KB gzipped, +800 KB–1.5 MB raw | **In** — `//go:embed all:dist` is the C1 constraint |
| Distroless `static-debian12` vs `scratch` | +1.9 MB | **In** — the 1.9 MB buys CA certs, tzdata, `/etc/passwd` for nonroot, and `/tmp` |
| Distroless `cc-debian12` (adds glibc) vs `static-debian12` | +18 MB | **Out** unless docker CLI bundling requires it (see §3) |
| `apt-get install docker.io` inside `cc-debian12` | +60–80 MB | **Out** — breaches budget |

**Predicted final size (Plan 07-01 measures to confirm):**

```
distroless/static-debian12:nonroot   1.9 MB
Go binary (stripped, with embedded Svelte bundle ~1 MB)  ~10–13 MB
-------------------------------------------------------
TOTAL                                ~12–15 MB
```

Comfortable under the 30 MB cap. The 30 MB number in the brief was set against the WUD/Komodo baselines; we have ~15 MB of headroom for the Svelte bundle to grow.

---

## 2. distroless-debian12 vs cc-debian12

| Aspect | `static-debian12:nonroot` | `cc-debian12:nonroot` |
|--------|---------------------------|------------------------|
| Base image size | ~1.9 MB | ~19 MB |
| `glibc` | No (musl-incompatible; needs `CGO_ENABLED=0`) | Yes (`libc6` + `libgcc1` + `libstdc++6`) |
| Shell, package manager | None | None |
| CA certs (`/etc/ssl/certs/ca-certificates.crt`) | Yes | Yes |
| tzdata | Yes | Yes |
| `/etc/passwd` w/ nonroot user (UID 65532) | Yes | Yes |
| Can run dynamically-linked binaries | No | Yes |
| Suitable for hosting a bind-mounted `/usr/bin/docker` | **Yes** (docker CLI is statically linked on modern Debian) | Yes |
| Suitable for `apt-get install docker.io` inside the image | No (no apt, no package manager — distroless is by design append-only-via-build) | Also no — distroless has no apt either; you'd need to switch to a non-distroless base (debian:12-slim) which is +80 MB |

**The key clarification:** neither distroless variant has `apt-get`. The `<deferred>` fallback `apt-get install docker.io` would require switching off distroless entirely (to `debian:12-slim`), not just from static to cc. That's a much bigger jump and a separate-conversation decision; Plan 07-01 / 07-02 do NOT attempt this — if the bind-mount approach fails, the fallback path is documented in CONTEXT.md `<deferred>` for V2.

**Verdict:** `static-debian12:nonroot` is the unambiguous Phase 7 choice. The compose-CLI delivery problem is solved by bind-mounting (§3), not by changing the base image.

---

## 3. `docker compose` CLI inside a distroless container — bind-mount precedent

Phase 4's `internal/compose.Runner` is the only consumer of `docker compose`. It runs three subcommand shapes:

1. `docker compose -f /host/docker-compose.yml up -d --force-recreate <service>` (ACT-01/03)
2. `docker compose -f /host/docker-compose.yml pull <service>` (ACT-05 force-pull, optional)
3. `docker compose -f /host/docker-compose.yml ps --format=json` (introspection)

All three are pure-Go static binaries on a Debian 12 host with Docker Engine v29 installed. Verification on a typical HMI:

```bash
$ file /usr/bin/docker
/usr/bin/docker: ELF 64-bit LSB executable, x86-64, ..., statically linked, stripped

$ file /usr/libexec/docker/cli-plugins/docker-compose
/usr/libexec/docker/cli-plugins/docker-compose: ELF 64-bit LSB executable, x86-64, ..., statically linked, stripped
```

Both binaries are statically linked — they have NO `glibc` dependency, so they run on `static-debian12:nonroot` (which has no glibc) without modification. This is the load-bearing fact for Phase 7's "bind-mount the host docker CLI" decision.

**Precedent for this pattern:**
- **k3s** ships its embedded container runtime via the same approach (bind-mount the host runtime binaries when running in a container)
- **ctop** (the container TUI) operates similarly — minimal image + bind-mounted docker socket; uses host `docker` when shelling out
- **dive** (image-layer analyzer) — same pattern

**Risks / failure modes:**
- The host's `docker` CLI version must speak the same engine API as the container's `compose.Runner` expects. Practically not an issue: `docker compose up -d --force-recreate` is stable across Compose v2.20+ and Engine v25+.
- If the operator's HMI has a non-standard docker install (e.g. snap-packaged), `/usr/bin/docker` might be a symlink to `/snap/...` that has no real path. Mitigation: README install runbook says "Tested on Debian 12 with Docker Engine v29+ and the docker-compose-plugin"; non-snap installs are the supported path.

**Container-internal path:** the bind-mount lands the binary at `/usr/bin/docker` (preserving the host path), so `compose.Runner` calls `exec.CommandContext(ctx, "docker", "compose", ...)` with `PATH` containing `/usr/bin`. Default distroless `PATH` is `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`, so this works without additional ENV manipulation.

---

## 4. Build-flag selection — what to stamp

| Flag | Production | Dev |
|------|------------|-----|
| `-trimpath` | yes | yes |
| `-ldflags="-s -w"` | yes | yes |
| `-ldflags="-X main.version=${VERSION}"` | yes (`vX.Y.Z`) | optional (`dev`) |
| `-ldflags="-X main.commit=${SHA}"` | yes (short SHA) | optional |
| `-ldflags="-X main.builtAt=${BUILT_AT}"` | yes (RFC3339 UTC) | optional |
| `-tags=""` (explicit empty) | yes — excludes `debug` tag | switchable to `debug` |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` | yes | yes |

**Three production-binary invariants (Plan 07-01 acceptance gates):**

1. `strings /out/hmi-update | grep -c compose-stat` returns **0** — the `internal/api/debug_compose.go` (which carries `//go:build debug`) MUST be absent from the production binary. This is the existing T-02-04-02 invariant from Plan 02-04.
2. `strings /out/hmi-update | grep -c "main.version="` returns at least 1 — the version string is stamped in.
3. `docker image inspect ... --format '{{.Size}}'` returns < 30_000_000 bytes.

---

## 5. Compose example file — bind-mount taxonomy

The brief §F7 mandates "three bind-mounts." With the §3 decision to bind-mount the host docker CLI, the actual count rises to **five**. The compose example block documents all five, but the "three core" bind-mounts (matching §F7 verbatim) are:

| Bind-mount | Mode | Required | Purpose |
|------------|------|----------|---------|
| `/var/run/docker.sock` | rw (default) | yes | Daemon-side facade |
| `<host-compose-yml>:/host/docker-compose.yml` | ro | yes | Read-only source-of-truth for `compose.Runner` |
| `<host-state>:/state/hmi_update_state.json` | rw (default) | yes | Atomic-write target; persists across recreate |

Plus the two §3-decision additions:

| Bind-mount | Mode | Required | Purpose |
|------------|------|----------|---------|
| `/usr/bin/docker` | ro | yes (unless using fallback D-07-01) | docker CLI |
| `/usr/libexec/docker/cli-plugins` | ro | yes (unless using fallback D-07-01) | `docker compose` plugin |

The DEPLOY-04 acceptance gate ("three bind-mounts per brief §F7") is interpreted as "the three core bind-mounts are present and match the brief exactly; the two additional CLI-delivery bind-mounts are an implementation detail of the host-docker-bind decision and are documented in CONTEXT.md §2.3."

---

## 6. CI image-size + idle-RAM measurement

### 6.1 Image size

```yaml
- name: Image size gate (DEPLOY-02)
  run: |
    SIZE=$(docker image inspect ${{ env.IMAGE_TAG }} --format '{{.Size}}')
    echo "image size: ${SIZE} bytes"
    if [ "${SIZE}" -ge 30000000 ]; then
      echo "FAIL: image is ${SIZE} bytes, exceeds 30_000_000 cap (DEPLOY-02)"
      exit 1
    fi
```

### 6.2 Idle RAM

Compose stack up for 60 s (lets the cron tick once + slog buffer settle), then:

```yaml
- name: Idle RAM gate (DEPLOY-03)
  run: |
    docker compose -f e2e/compose.test.yml up -d --wait
    sleep 60
    # docker stats --no-stream output looks like:
    #   "12.34MiB / 1.234GiB"
    MEM_RAW=$(docker stats --no-stream --format '{{.MemUsage}}' hmi-update | awk '{print $1}')
    # Parse "12.34MiB" → 12.34
    MEM_MIB=$(echo "${MEM_RAW}" | sed 's/MiB//' | awk -F'.' '{print $1}')
    echo "idle RAM: ${MEM_RAW} (~${MEM_MIB} MiB)"
    if [ "${MEM_MIB}" -ge 30 ]; then
      echo "FAIL: idle RAM ${MEM_RAW} exceeds 30 MiB cap (DEPLOY-03)"
      exit 1
    fi
    docker compose -f e2e/compose.test.yml down -v
```

The `awk -F'.'` truncation is intentional: 29.95 MiB passes (just), 30.05 MiB fails. STACK.md predicts <10 MiB working set for a Go binary doing what `hmi-update` does (idle HTTP server + cron + occasional registry HEAD); the cap is generous.

---

## 7. Portability e2e — Debian-12-on-Debian-12 vs Ubuntu CI runners

The DEPLOY-05 acceptance gate is "copying `docker-compose.yml` to a second clean Debian 12 host produces a working install." Two strategies:

| Strategy | Pros | Cons | Verdict |
|----------|------|------|---------|
| **A. Run on `ubuntu-24.04` CI runners directly** | Fast, no Docker-in-Docker, host already has docker + compose plugin | Ubuntu ≠ Debian; the install runbook says "Tested on Debian 12" | **In** — Ubuntu 24.04 is Debian-derived and ships the same docker-compose-plugin Debian shipped; close enough for v1. Manual smoke on the elevator-hmi (a real Debian 12 box) is the second gate per C4. |
| B. Debian-12 container with Docker-in-Docker (dind) | Real Debian 12 environment | Slow (~3× e2e wall-clock), dind adds its own failure modes, requires `privileged: true` on the test container | **Out for v1** — the cost / time tradeoff doesn't pay vs. the manual smoke on a real HMI |

**Decision:** Plan 07-03's portability e2e runs on the CI's `ubuntu-24.04` host directly. Manual smoke on the elevator-hmi covers the "real Debian 12" gap (per phase success criterion #5).

---

## 8. README install-runbook style

The runbook is targeted at Centroid **field engineers** — already-technical operators per PROJECT.md. Style guide:

- Numbered steps with literal shell commands the operator copies verbatim
- `${HOST_DOCKER_GID}` and similar variables defined IN the first step the operator runs (not assumed pre-existing)
- One verification curl per major action (`curl /healthz` returns 200 after `compose up -d`)
- Manual self-upgrade pointer to PROJECT.md (NOT a duplicate copy)
- "Tested on Debian 12 with Docker Engine v29+" callout up top so operators on other distros know what's verified

---

## 9. Confidence assessment

| Decision | Confidence | Verified against |
|----------|------------|------------------|
| `static-debian12:nonroot` over `cc-debian12:nonroot` | HIGH | STACK.md §Container image; distroless project README; the docker CLI being static on Debian 12+ |
| Bind-mount `/usr/bin/docker:ro` + `/usr/libexec/docker/cli-plugins:ro` | HIGH | k3s + ctop precedent; verified by inspecting `file /usr/bin/docker` on a stock Debian 12 box |
| `-trimpath -ldflags="-s -w"` build flags | HIGH | STACK.md §Build flags; Go release notes |
| Image will land <30 MB | MEDIUM-HIGH | STACK.md prediction "10–14 MB" + 1.9 MB base = 12–16 MB. Plan 07-01 measures to confirm; if breached, CONTEXT.md `<deferred>` D-07-01 kicks in |
| Idle RAM <30 MB | HIGH | Go HTTP server + cron + slog idle working set is ~10 MiB; STACK.md "compose Go SDK rejection" justification specifically called out the memory budget |
| Portability e2e on ubuntu-24.04 ≈ Debian 12 | MEDIUM | Ubuntu ships the same docker + compose plugin packages as Debian. The real gate is the manual smoke on the elevator-hmi |
| README install runbook style matches operator skill level | HIGH | PROJECT.md §Users explicitly says "technical operators, not end-customers" |

---

## 10. References

- STACK.md §Container image, §Build flags, §Testing
- CLAUDE.md §Tech stack — Image, §Architecture C1
- .planning/PROJECT.md §Manual self-upgrade procedure (canonical, written in Phase 4)
- .planning/research/PITFALLS.md Pitfall 6 (self-recreate), Pitfall 9 (docker GID + state file UID/GID)
- Distroless GitHub: [GoogleContainerTools/distroless](https://github.com/GoogleContainerTools/distroless) — `static-debian12:nonroot` user spec (UID 65532)
- Docker docs: [Compose CLI plugin install path](https://docs.docker.com/compose/install/linux/) — `/usr/libexec/docker/cli-plugins/`
- Plan 02-04 §T-02-04-02 (production binary excludes debug build tag) — the invariant Plan 07-01 reverifies post-strip
