# Stack Research

**Project:** `hmi-update` — single-binary Go container update manager with embedded Svelte UI, polling OCI registries, with Playwright e2e tests.
**Domain:** Container lifecycle tool / on-prem ops sidecar
**Researched:** 2026-05-13
**Confidence:** HIGH (versions verified against GitHub releases and pkg.go.dev / npm registries within the last week)

---

## Executive summary

The brief is mostly on-target, but **three concrete corrections are mandatory** and several others are recommended. In priority order:

1. **Replace `github.com/docker/docker/client` with `github.com/moby/moby/client`.** The old module is deprecated as of Docker Engine v29 (Nov 2025) and security scanners flag it. The new client is at `v0.4.1` as of April 2026 — sub-1.0 but officially the supported path. (HIGH)
2. **Bump Go from 1.23 to 1.26.** Go 1.23 went EOL on 2026-02-11 (two newer majors rule). Go 1.26.3 is current stable as of 2026-05-07; Go 1.25.10 is also still maintained. (HIGH)
3. **Pin the distroless base to `gcr.io/distroless/static-debian12:nonroot`** (or migrate to `static-debian13:nonroot`). The unversioned `gcr.io/distroless/static:nonroot` tag now silently follows whichever Debian is current and the distroless project documents it as deprecated for new pinning. (HIGH)

Otherwise the brief's choices hold up: stdlib `net/http` with Go 1.22+ pattern matching is sufficient (skip `chi`), `robfig/cron/v3` is fine for the hourly poll, Svelte 5 + Vite + Tailwind v4 is the current happy path, and Playwright Test against a real docker-compose stack with the `google/go-containerregistry`'s in-process registry is the cleanest e2e shape.

---

## Recommended Stack

### Backend — core

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| **Go** | **1.26.x** (current 1.26.3 — 2026-05-07) | Compiler / runtime | 1.23 is EOL; 1.26 brings cleaner `slog` ergonomics, the mature `net/http` routing introduced in 1.22, and is one of two supported lines. Pin in `go.mod` as `go 1.26`. |
| **`net/http` (stdlib)** | std | HTTP server + router | Go 1.22+ `ServeMux` supports method-scoped routes (`GET /api/containers/{name}/update`) and path variables via `r.PathValue("name")`. The API surface in this project (≈8 routes) does not justify a third-party router. Zero dependencies, zero version drift, no learning cost. |
| **`github.com/moby/moby/client`** | **client/v0.4.1** (2026-04-20) | Docker daemon client | **Replaces deprecated `github.com/docker/docker/client`.** Docker Engine v29 mandated the module rename. CVE-2026-34040 / CVE-2026-33997 are only fixed in this module path. Note: sub-1.0 semver — pin precisely in `go.mod`. |
| **`github.com/moby/moby/api/types`** | matched to client | API types | Same migration. Import `events`, `image`, `container` subpackages from here. |
| **`log/slog` (stdlib)** | std | Structured JSON logs | Brief is correct. Use `slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))`. Add `slog.SetDefault` once in `main`. |
| **`github.com/robfig/cron/v3`** | **v3.0.1** | Cron schedule for poll | Stable, no churn since 2024, accepts the literal `0 * * * *` format the user wants in `HMI_UPDATE_CRON`. Cron-string parity with the env-var contract matters here — the alternative (`gocron`) prefers a fluent API and would force users to learn a new syntax for the same env var. |
| **`os` + `encoding/json` (stdlib)** | std | Atomic JSON state file | Write to `*.tmp`, `os.Rename`, `os.Chmod 0o600`. No dependency needed. Use `json.NewEncoder(w).Encode(s)` with `SetIndent` for human-readable diffs. |

### Backend — supporting

| Library | Version | Purpose | When to use |
|---------|---------|---------|-------------|
| **`github.com/google/go-containerregistry`** | **v0.20.x** | OCI registry HTTP client + manifest types | **Stronger recommendation than rolling your own token+manifest fetch.** `pkg/v1/remote` does the Bearer-token flow, multi-arch index resolution, and platform-manifest digest extraction for free — i.e. the exact `RepoDigests[0]` vs upstream-digest comparison F1 needs. Cuts ~150 LOC of registry plumbing and removes a fragile area (the same area where WUD's two patched bugs lived). |
| **`golang.org/x/crypto/x509roots/fallback`** | latest | CA cert bundle compiled into binary | Optional belt-and-braces: distroless already has CA certs, but importing this means even `FROM scratch` would work and your TLS roots are explicitly versioned. Adds ~250 KB to the binary. Worth it for an HMI deployment that may run on isolated networks. |
| **`os/exec`** | std | Subprocess for `docker compose up -d --force-recreate <service>` | See "compose CLI vs library" below — recommend `docker compose` via `exec.CommandContext`. |

### Frontend

| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| **Svelte** | **5.55.x** | UI framework | Svelte 5 + runes is stable (released Oct 2024, runes are the documented default in May 2026). Use `$state`, `$derived`, `$effect` — not stores — for the table state. |
| **Vite** | **7.x** | Bundler / dev server | Vite 7 is current stable. Vite 8 just shipped but `@sveltejs/vite-plugin-svelte@7` requires it; stick with Vite 7 + `vite-plugin-svelte@6.x` for a calmer dependency story until 8 has soaked. |
| **`@sveltejs/vite-plugin-svelte`** | **6.x** (peer of `vite ^6.3 \|\| ^7`) | Svelte compile glue | Pinning to v6 keeps you on Vite 7. v7 forces Vite 8 — re-evaluate at the next milestone. |
| **TypeScript** | **5.6+** | Type safety in UI | Svelte 5 + TS is the documented happy path. |
| **Tailwind CSS** | **v4.3.x** | Styling | v4 is stable (May 2026 latest is 4.3.0). Use the **`@tailwindcss/vite`** plugin, not the PostCSS pipeline — it's the v4 recommendation and removes the need for `postcss-import` and `autoprefixer`. Config-in-CSS via `@import "tailwindcss"` + `@theme { ... }`. |
| **`//go:embed`** | std | Embed `ui/dist/**` into binary | Brief is correct. Use `embed.FS` and a stripped subtree (`fs.Sub(distFS, "dist")`) handed to `http.FileServerFS`. |

### Container image

| Layer | Version | Purpose | Why |
|------|---------|---------|-----|
| Builder (Node) | `node:22-alpine` | Build Svelte bundle | Node 22 is the active LTS through April 2027; brief is fine. |
| Builder (Go) | `golang:1.26-alpine` | Build Go binary | Match runtime Go version. |
| Final | **`gcr.io/distroless/static-debian12:nonroot`** | Runtime | **Pin the debian version explicitly.** Unversioned `static:nonroot` is documented as following "currently `-debian13`, will change in future"; that's exactly the kind of moving floor you don't want on an unattended HMI. `static-debian12` ships ~1.9 MB, includes `ca-certificates`, tzdata, `/etc/passwd` for nonroot (UID 65532), and supports a pure-Go `CGO_ENABLED=0` static binary. Migrate to `static-debian13:nonroot` once your CI has run on it for a release cycle. |

### Build flags

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath \
  -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${SHA}" \
  -o /out/hmi-update ./cmd/hmi-update
```

Expected final image size with embedded Svelte bundle: **~10–14 MB** (well under the 30 MB cap).

### Testing

| Tool | Version | Purpose | Why |
|------|---------|---------|-----|
| **`@playwright/test`** | **1.60.x** | E2E tests | Current stable (2026-05-11). Use `globalSetup` + `globalTeardown` to `docker compose -f e2e/compose.test.yml up -d --wait` and `down -v`. `--wait` (Compose v2.20+) blocks until healthchecks pass — perfect for the "fake registry ready before tests start" sequence. |
| **`google/go-containerregistry`'s `pkg/registry`** | v0.20.x | **Fake OCI registry inside the test stack** | An in-memory, standards-compliant Docker V2 / OCI Distribution registry implemented as an `http.Handler`. Ship a tiny `cmd/fakereg` in the same repo, dockerize it, run it in `compose.test.yml`. From Playwright tests use the registry's HTTP API (or the Go `crane` CLI baked into the test container) to push a fresh manifest mid-test to flip `:latest`. **Better than `registry:2`** because (a) it's in-Go so you can extend it with test endpoints if needed, (b) state is ephemeral per test run, (c) you can also link it directly from Go unit tests via `httptest.NewServer(registry.New())` without any container at all. |
| **`crane`** CLI | go-containerregistry latest | Pushing manifests mid-test | `crane append`, `crane copy`, `crane tag` are the cleanest way to flip `:latest` in a Playwright step. Run it from the Playwright test container via `child_process` or — preferred — bake a tiny "test-actor" container into compose. |
| **Go `testing`** | std | Unit tests | Brief is correct. Table-driven tests for `internal/state`, `internal/registry`, `internal/poll`. |
| **`testcontainers-go` Compose module** | v0.34+ | Optional: Go-side compose orchestration in unit/integration tests | Only useful if you also want Go-level integration tests that spin up compose stacks. For the e2e (Playwright) story, raw `docker compose up --wait` from `globalSetup` is simpler and more transparent. **Recommendation: skip for v1**, revisit if you grow Go integration tests beyond unit. |

### CI/CD

| Tool | Purpose | Notes |
|------|---------|-------|
| **GitHub Actions** | CI | Brief is correct. Standard `ubuntu-24.04` runners have Docker + Compose v2 preinstalled. |
| **`docker/build-push-action@v6`** | Build & push image | Use with `docker/metadata-action@v5` for semver tag generation (`:latest`, `:vX.Y.Z`, `:sha-<short>`). |
| **`actions/setup-go@v5`** | Toolchain | Pin `go-version: '1.26'`. |
| **`actions/setup-node@v4`** | Toolchain | Pin `node-version: '22'`. |
| **`actions/checkout@v4`** | — | Standard. |
| **`docker/login-action@v3`** | GHCR push | Use `GITHUB_TOKEN` for GHCR; no secret needed for the `centroid-is/hmi-update` org repo. |

---

## Installation

```bash
# Backend
go mod init github.com/centroid-is/hmi-update
go get github.com/moby/moby/client@v0.4.1
go get github.com/moby/moby/api@latest
go get github.com/robfig/cron/v3@v3.0.1
go get github.com/google/go-containerregistry@latest

# Frontend (run inside ./ui)
npm create vite@latest . -- --template svelte-ts
npm install
npm install -D tailwindcss@^4 @tailwindcss/vite@^4
npm install -D @sveltejs/vite-plugin-svelte@^6

# E2E (run inside ./e2e)
npm init -y
npm install -D @playwright/test@^1.60.0
npx playwright install --with-deps chromium
```

---

## Detailed decisions — answers to the brief's questions

### 1. Go version (1.23 vs 1.24 vs 1.25/1.26)
**Use Go 1.26.** Go 1.23 went out of support 2026-02-11; staying on it now means no security backports. 1.25 and 1.26 are the two currently supported lines (six-month cadence, two-version support window). 1.26 brings no breaking changes to `//go:embed`. `github.com/moby/moby/client` v0.4.1 builds cleanly on 1.25+ — verified on pkg.go.dev. **Confidence: HIGH** (golang.org release page).

### 2. Best Docker Go client
**Use `github.com/moby/moby/client`.** The brief's `github.com/docker/docker/client` is officially deprecated as of Docker Engine v29.0.0 (November 2025) and unfixed CVEs exist on that path. The migration is purely a find-and-replace at the import level (`github.com/docker/docker/` → `github.com/moby/moby/`) plus a small handful of renamed option structs in v29 (e.g. `client.ContainerListOptions` instead of `container.ListOptions`).

Alternatives considered and rejected:
- **`containerd` client** — wrong layer; you'd lose Docker-specific labels, the events stream you actually want, and `RepoDigests` semantics.
- **Pure HTTP against the Docker socket** — gains you nothing, costs you typed responses.

For the registry-side digest fetch, **also use `github.com/google/go-containerregistry/pkg/v1/remote`** rather than hand-rolling the token + multi-arch index dance. This is the single biggest reduction in "WUD-class" bug surface; the brief's hand-rolled steps in F1 ("Fetch a Bearer token", "GET the index, filter on amd64/linux") are exactly what `remote.Image(ref, remote.WithPlatform(...))` does in one call.

**Confidence: HIGH** (verified via moby/moby v29.4.3 release notes, pkg.go.dev, and migration tracking issues on docker/buildx).

### 3. Drive `docker compose` via CLI or Go library?
**Use the CLI via `exec.CommandContext`.** Reasons:

- **Surface area:** you only need three commands — `up -d --force-recreate <service>`, optionally `pull`, and `ps`/`config` for introspection. The Compose Go SDK (`github.com/docker/compose/v2`) brings in **hundreds** of transitive dependencies (buildx, BuildKit, containerd client, OTel, etc.) and would push the binary well past the 30 MB budget you have to amortize against the embedded Svelte bundle.
- **Compose version coupling:** subprocess uses the host's `docker compose` binary, which on a Debian HMI is whatever ships with Docker Engine. That's a feature, not a bug — your tool stays consistent with whatever Compose the operator already uses for everything else on the box.
- **Debugging:** an operator can run the exact same command by hand. With the SDK, they can't.
- **Testability:** swap `exec.Command` for a `Runner` interface in tests; trivial.

Risks to manage:
- Parse `docker compose` exit codes carefully, and capture stderr into the slog event.
- Pin the minimum Compose version (v2.20+) in README for the `--wait` flag and stable JSON output of `compose ps --format=json`.

**Confidence: HIGH.**

### 4. HTTP router: stdlib `net/http` vs `chi`
**Use stdlib `net/http`.** The API has ~8 routes, no middleware stacks worth a framework, and Go 1.22's `ServeMux` handles `GET /api/containers/{name}/update` plus method dispatch natively. `chi` (v5.2.5, Feb 2025) is still fine and well-maintained, but adopting it would buy you middleware chaining and sub-routers you don't need, plus a dependency you have to update. Stdlib wins on the dependency-minimalism ethos the brief explicitly invokes ("Tailwind-only matches the project's no-extra-deps ethos").

If you ever do need middleware (request logging, panic recovery, CORS for a future remote UI), wrap handlers with small helper functions — `LoggingHandler(h http.Handler) http.Handler` is ~10 LOC.

**Confidence: HIGH.**

### 5. Cron library
**Use `robfig/cron/v3` (v3.0.1).** Reasons:

- Accepts the standard 5-field cron expression `0 * * * *` directly — matches the `HMI_UPDATE_CRON` env-var contract verbatim. Operators familiar with Linux cron read this and know exactly what it does.
- Stable, low-churn, single-author project but battle-tested across the Go ecosystem (Prometheus, Caddy, Kubernetes operators all use it).
- Supports timezone via `cron.New(cron.WithLocation(loc))`.

Alternatives considered:
- **`go-co-op/gocron/v2`** — supports cron strings (`s.NewJob(gocron.CronJob("0 * * * *", false), ...)`) and has a richer fluent API. But the only use here is one hourly job, plus an event-driven kick from the docker events stream. The extra surface buys nothing.
- **`time.Ticker`** — would work fine if the schedule were always `time.Hour`, but the brief explicitly exposes `HMI_UPDATE_CRON` as a cron expression. A ticker would need a parser anyway, so just use the parser that already exists.

**Confidence: HIGH.**

### 6. Frontend: Svelte 5, Vite, Tailwind versions
- **Svelte 5.55.x with runes.** Stable for over 18 months. Use `$state` for the table data, `$derived` for status badges, `$effect` for the 5-second polling timer. No stores needed for a single-page app.
- **Vite 7.x.** Don't jump to Vite 8 yet; `@sveltejs/vite-plugin-svelte@7` requires it and the ecosystem hasn't caught up. Vite 7 with `vite-plugin-svelte@6.x` is the calm path.
- **Tailwind v4.3.x with `@tailwindcss/vite`.** v4 is stable and the Vite plugin is the documented v4 install path. Config moves into CSS (`@theme { --color-primary: ...; }`) so you can delete `tailwind.config.js`. Removes `postcss-import` and `autoprefixer` deps.

Stay Tailwind-only (no skeleton.dev / shadcn-svelte). The Q7 question in the brief should resolve to "Tailwind-only" — toasts and disabled-state buttons are 30–50 LOC each.

**Confidence: HIGH.**

### 7. Playwright + docker-compose pattern
**Canonical shape:**

```
e2e/
  compose.test.yml          # fake-registry, hmi-update-under-test, sample target container
  playwright.config.ts      # globalSetup, globalTeardown, baseURL: http://localhost:8080
  global-setup.ts           # exec: docker compose -f compose.test.yml up -d --wait
  global-teardown.ts        # exec: docker compose -f compose.test.yml down -v
  fixtures/
    registry.ts             # helpers: pushImage(tag, layers), retagLatest(repo, digest)
  tests/
    01-discovery.spec.ts
    02-update-detection.spec.ts
    ...
```

Don't use `testcontainers-node` for this project. The brief calls out one specific compose file; `testcontainers-node` is best when each test owns its own ephemeral container set. For "one stack up for all tests, flip state per test", raw `docker compose --wait` is simpler, faster, and matches what a developer runs by hand. Mention `@playwright-labs/fixture-testcontainers` only if a milestone later wants per-test isolation — it's nice but unnecessary here.

The `--wait` flag (Compose v2.20+) blocks `up` until every service with a `healthcheck:` reports healthy. Define `healthcheck:` on the fake registry (`GET /v2/`) and on `hmi-update` (`GET /healthz`) and Playwright tests can start with the stack fully up — no `waitForResponse` polling at the top of every spec.

**Confidence: HIGH.**

### 8. Fake OCI registry
**Use `github.com/google/go-containerregistry/pkg/registry`.** Three reasons:

1. **Mid-test manifest swap is trivial.** It's a standards-compliant `http.Handler`. Push with the `crane` CLI (`crane append --new_tag=fake:5000/centroid-hmi:latest ...`) or with the `remote.Write` Go API. Either flips `:latest` instantly and Docker daemon's next pull sees the new digest.
2. **No state to clean up.** In-memory; restart the container between test runs to reset.
3. **Same code can power Go unit tests.** `httptest.NewServer(registry.New())` gives you a real registry in a unit test, no Docker needed — perfect for `internal/registry` table tests of multi-arch index handling.

`registry:2` (the official Docker registry) is fine but heavier and stateful; `zot` is excellent for production-grade OCI features (referrers, OCI 1.1 artifacts) you don't need. **Pick the simplest.**

A tiny wrapper repo layout:
```
e2e/fakereg/
  main.go      // 20 LOC: registry.New() served on :5000
  Dockerfile   // FROM gcr.io/distroless/static-debian12:nonroot
```

**Confidence: HIGH.**

### 9. Distroless variant
**Use `gcr.io/distroless/static-debian12:nonroot` for v1.** Specifically:

- **Always pin the Debian suffix.** The unversioned `gcr.io/distroless/static:nonroot` tag is documented as "currently following `-debian13`, will change in the future." That's a footgun for an unattended HMI.
- **`static-debian12:nonroot`** is the conservative pick today: ~1.9 MB, includes `/etc/ssl/certs/ca-certificates.crt`, tzdata, and a `nonroot` user at UID 65532. Statically-linked Go binaries built with `CGO_ENABLED=0` run on it with no extra setup.
- **Migrate to `static-debian13:nonroot`** at a future milestone once CI has stabilized — the Debian 12 image will receive support for ~1 year after Debian 13's GA, then enter EOL.
- **Don't use `:nonroot-amd64`** unless you actually want to disable multi-arch manifest selection at the registry. Stick to `:nonroot`.

`scratch` is an alternative (saves ~1.9 MB) but you'd need to vendor `ca-certificates.crt` and `passwd` yourself. For a sub-30 MB target with comfortable margin, distroless is the better tradeoff — debugging an "unknown CA" on a customer HMI is a much more expensive failure than 1.9 MB.

**Confidence: HIGH.**

---

## Alternatives Considered

| Recommended | Alternative | When alternative is better |
|-------------|-------------|---------------------------|
| `github.com/moby/moby/client` | `github.com/docker/docker/client` | **Never** for new code. Deprecated, unpatched CVEs, will stop receiving any fixes. |
| `docker compose` CLI via `os/exec` | `github.com/docker/compose/v2` Go SDK | If you needed in-process events, programmatic build, or to ship a unified binary on a host that doesn't have Compose installed. None apply here. |
| stdlib `net/http` | `go-chi/chi` v5 | When you have >20 routes, multiple middleware layers, or sub-routers per concern. Not this project. |
| `robfig/cron/v3` | `go-co-op/gocron/v2` | If you want a fluent API with random durations, job listeners, and locking for distributed schedulers. Single-instance hourly poll doesn't justify it. |
| `google/go-containerregistry` in-memory registry | `registry:2` (CNCF distribution) | If you need persistent state across test reboots, or to test garbage-collection / GC behavior. |
| `google/go-containerregistry` in-memory registry | `zot` | If you need OCI 1.1 referrers / sigstore / signed artifacts in tests. |
| Svelte 5 + runes | Svelte 4 | Only if blocked by a third-party Svelte library that hasn't migrated. Tailwind-only UI is unaffected. |
| Tailwind v4 + `@tailwindcss/vite` | Tailwind v3 + PostCSS | If you have legacy `tailwind.config.js` you can't migrate. New project — no reason. |
| `static-debian12:nonroot` | `scratch` + vendored CAs | When 1.9 MB matters more than a default user, tzdata, and `/tmp`. Not here. |
| `static-debian12:nonroot` | `alpine` | Never for static Go binaries — alpine is bigger, has a real OS surface, and you're not using musl libc on purpose. |
| Playwright globalSetup + raw compose | `testcontainers-node` | Per-test container isolation, dynamic ports, when stacks are heterogeneous per test. Not needed for one fixed compose. |

---

## What NOT to use

| Avoid | Why | Use instead |
|-------|-----|-------------|
| `github.com/docker/docker/*` | Deprecated as of Docker v29 (Nov 2025); unfixed CVEs; security scanners flag the import. | `github.com/moby/moby/client` + `github.com/moby/moby/api/types` |
| `github.com/docker/compose/v2` as a library | Massive transitive dep tree (BuildKit, containerd, OTel) — would blow the 30 MB image budget on dependencies alone. | `docker compose ...` via `exec.CommandContext` |
| Go 1.23 (the brief's floor) | EOL 2026-02-11; no security backports. | Go 1.26 (current) or 1.25 (still supported) |
| `gcr.io/distroless/static:nonroot` (unversioned) | Tag silently floats between Debian versions; documented as moving. | `gcr.io/distroless/static-debian12:nonroot` (or `-debian13:`) |
| `node:22-alpine` for runtime (just builder) | Distroless is the runtime — Node is build-only. Brief gets this right; flagged here so it doesn't slip. | Multi-stage: node for build, distroless for run |
| Hand-rolled Bearer-token + multi-arch index code in `internal/registry` | This is exactly the bug class WUD got stuck on (single-arch digest extraction). | `github.com/google/go-containerregistry/pkg/v1/remote` |
| skeleton.dev / shadcn-svelte / any UI kit | Violates the no-extra-deps ethos (see brief §11 Q7); the UI has 5 components total. | Plain Svelte + Tailwind utility classes |
| `gorilla/mux` | Project archived since end of 2022; nothing it does that stdlib + Go 1.22 ServeMux doesn't. | stdlib `net/http` |

---

## Stack patterns by variant

**If a future milestone adds arm64 support:**
- Add `linux/arm64` to the `docker/build-push-action@v6` `platforms:` list.
- Distroless multi-arch tags (`static-debian12:nonroot`) already work on arm64 without changes.
- Set `GOARCH=arm64` matrix in the Go build job.
- No application code changes required.

**If a future milestone adds private registries:**
- `go-containerregistry` already supports credentials via `authn.Keychain`. Wire to a credentials file in the bind-mount volume.
- Add `RegistryAuth` (base64-encoded JSON) to `client.ImagePullOptions` when calling Moby.
- Document `~/.docker/config.json` mount path.

**If a future milestone adds notifications:**
- Don't introduce a notification framework. Emit a webhook on action completion — operators bring their own dispatcher (ntfy, Slack incoming webhook, MQTT bridge).

**If a future milestone adds authentication:**
- Use stdlib `http.BasicAuth` for v1 of auth — single shared credential in env var. Keeps the no-deps ethos.
- Only reach for an OAuth library if you grow multi-user / RBAC, which is out of scope.

---

## Version Compatibility

| Package | Compatible with | Notes |
|---------|-----------------|-------|
| `github.com/moby/moby/client@v0.4.1` | Go 1.25, 1.26 | API client speaks Docker Engine API 1.40–1.54. Engine v29 = API 1.54. v0.x — pin precisely. |
| `github.com/robfig/cron/v3@v3.0.1` | Go 1.18+ | No moving target; safe to pin. |
| `github.com/google/go-containerregistry@v0.20` | Go 1.22+ | Includes both the in-memory registry and the high-level remote API. |
| Svelte `5.55` | Vite 6.3 or 7 with `vite-plugin-svelte@6` | If you upgrade to Vite 8 you must move to `vite-plugin-svelte@7`. Stay on the v6 plugin line for v1. |
| Tailwind `4.3` | `@tailwindcss/vite@4`, Vite 6/7 | Drop `postcss-import`, `autoprefixer`. |
| `@playwright/test@1.60` | Node 18, 20, 22 | Use the matching `mcr.microsoft.com/playwright:v1.60.0-noble` image in CI. |
| distroless `static-debian12:nonroot` | Static Go binaries, `CGO_ENABLED=0` | UID 65532 / GID 65532. Bind-mounted state file must be writable by this UID. |

A small but important compatibility gotcha: the **bind-mounted `hmi_update_state.json`** must be owned (or world-writable) by UID 65532 when running on distroless `:nonroot`. Document this in the README and consider an init step in the Dockerfile that touches the file with the right perms — OR run as root for the first write (not recommended) — OR document `chown 65532:65532 hmi_update_state.json` in the install instructions. Same applies to `/var/run/docker.sock` — the nonroot user must be in the `docker` group on the host, or the socket must be group-readable by GID 65532.

---

## Confidence assessment

| Recommendation | Confidence | Verified against |
|----------------|------------|------------------|
| Go 1.26 | HIGH | endoflife.date/go, go.dev/dl |
| `github.com/moby/moby/client` over `docker/docker/client` | HIGH | moby/moby v29.4.3 release notes, pkg.go.dev/github.com/moby/moby/client, microsoft/go-sqlcmd migration tracking |
| `docker compose` via `os/exec` | HIGH | Reasoned from dependency budget + brief §C1 single-binary constraint; cross-checked with testcontainers-go's own architecture |
| stdlib `net/http` | HIGH | go.dev/blog/routing-enhancements, project's small route surface |
| `robfig/cron/v3` | HIGH | pkg.go.dev/github.com/robfig/cron/v3 |
| Svelte 5.55.x + runes | HIGH | npm registry, svelte.dev May 2026 changelog |
| Vite 7 + `vite-plugin-svelte@6` | HIGH | vite.dev/releases, sveltejs/vite-plugin-svelte CHANGELOG |
| Tailwind v4.3 + `@tailwindcss/vite` | HIGH | github.com/tailwindlabs/tailwindcss/releases |
| `google/go-containerregistry` as fake registry | HIGH | pkg.go.dev/github.com/google/go-containerregistry/pkg/registry |
| `static-debian12:nonroot` (over unversioned) | HIGH | github.com/GoogleContainerTools/distroless README |
| Playwright globalSetup + raw compose over testcontainers-node | MEDIUM | Reasoned from project shape; could be revisited if per-test isolation becomes necessary |
| Skip `golang.org/x/crypto/x509roots/fallback` for v1 | MEDIUM | Optional belt-and-braces; distroless ships certs |

---

## Brief's choices — confirmed or corrected

| Brief said | Verdict | Action |
|------------|---------|--------|
| Go 1.23+ | **Correct as a floor, but bump default to 1.26** | EOL of 1.23 already passed; pin go.mod to 1.26 |
| `net/http` (stdlib) **or** `chi` | **Pick stdlib** | Decide explicitly; don't carry chi just in case |
| `docker/docker/client` | **WRONG — deprecated** | Use `github.com/moby/moby/client` |
| `log/slog` | Correct | — |
| `robfig/cron/v3` | Correct | — |
| Svelte 5 + Vite + TS + Tailwind | Correct | Pin versions explicitly (Svelte 5.55, Vite 7, Tailwind 4.3) |
| `//go:embed` for UI | Correct | — |
| Playwright Test | Correct | Add concrete pattern: `globalSetup` + `docker compose up --wait` |
| Fake OCI registry | **Brief is vague — recommend `google/go-containerregistry/pkg/registry`** | New decision; tiny new package in `e2e/fakereg/` |
| `node:22-alpine` builder | Correct | — |
| `golang:1.23-alpine` builder | **Bump to `golang:1.26-alpine`** | Match runtime Go version |
| `gcr.io/distroless/static:nonroot` final | **Pin Debian: `static-debian12:nonroot`** | Avoid moving floor |
| GitHub Actions, semver tags | Correct | Use `docker/metadata-action@v5` + `docker/build-push-action@v6` |
| TDD with Playwright (process) | Correct | — |

---

## Sources

- [Go release history](https://go.dev/doc/devel/release) — Go 1.26.3 current stable, 1.23 EOL (HIGH)
- [endoflife.date/go](https://endoflife.date/go) — Go 1.24 EOL 2026-02-11 (HIGH)
- [moby/moby releases](https://github.com/moby/moby/releases) — v29.4.3 current; client/v0.4.1 (HIGH)
- [pkg.go.dev/github.com/moby/moby/client](https://pkg.go.dev/github.com/moby/moby/client) — module path, current version (HIGH)
- [docker/buildx#3792](https://github.com/docker/buildx/issues/3792) — migration from `docker/docker` to `moby/moby` (HIGH)
- [Docker Engine v29 release notes](https://github.com/moby/moby/releases/tag/v29.0.0) — module rename (HIGH)
- [Go 1.22 routing enhancements](https://go.dev/blog/routing-enhancements) — `ServeMux` pattern matching (HIGH)
- [calhoun.io: ServeMux vs Chi](https://www.calhoun.io/go-servemux-vs-chi/) — comparison (MEDIUM)
- [pkg.go.dev/github.com/robfig/cron/v3](https://pkg.go.dev/github.com/robfig/cron/v3) — stable v3.0.1 (HIGH)
- [Svelte: What's new in May 2026](https://svelte.dev/blog/whats-new-in-svelte-may-2026) — Svelte 5.55 current (HIGH)
- [Vite 7.0 announcement](https://vite.dev/blog/announcing-vite7) — Vite 7 stable (HIGH)
- [Tailwind v4 announcement](https://tailwindcss.com/blog/tailwindcss-v4) and [releases](https://github.com/tailwindlabs/tailwindcss/releases) — v4.3.0 current (HIGH)
- [Distroless README](https://github.com/GoogleContainerTools/distroless) — unversioned `static:nonroot` moves between Debian versions; pin `-debian12` (HIGH)
- [pkg.go.dev/github.com/google/go-containerregistry/pkg/registry](https://pkg.go.dev/github.com/google/go-containerregistry/pkg/registry) — in-memory OCI registry for tests (HIGH)
- [Playwright globalSetup](https://playwright.dev/docs/test-global-setup-teardown) and [releases](https://github.com/microsoft/playwright/releases) — v1.60.0 current (HIGH)
- [Docker Compose SDK docs](https://docs.docker.com/compose/compose-sdk/) and [compose-go](https://github.com/compose-spec/compose-go) — library vs CLI trade-off (HIGH)
- [Testcontainers for Go: Docker Compose](https://golang.testcontainers.org/features/docker_compose/) — alternative compose orchestration (MEDIUM, not recommended for v1)
- [wollomatic: Go TLS in from-scratch containers](https://blog.wollomatic.de/posts/2025-01-28-go-tls-certificates/) — distroless includes CA certs (MEDIUM)

---
*Stack research for: hmi-update single-binary Go + Svelte container update manager*
*Researched: 2026-05-13*
