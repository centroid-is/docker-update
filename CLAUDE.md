<!-- GSD:project-start source:PROJECT.md -->
## Project

**docker-update**

A single Go container that detects when `:latest` Docker images have been re-pushed for the containers running on Centroid's elevator HMI boxes, and gives Centroid field engineers per-container **Update** and **Rollback** buttons via a small Svelte web UI on the HMI LAN. Replaces a fragile patched WUD 8.2.2 setup and a heavier Komodo-based alternative with a tool that has rollback built in, ships as one image, and persists everything in a single JSON file alongside the compose stack.

**Core Value:** A Centroid field engineer can confidently pull a fresh image to an HMI **and** roll it back to the previous digest, from one button per container in a browser, with no external services or extra state stores in the loop.

### Constraints

- **Tech stack — Backend**: Go 1.23+, `net/http` (stdlib) or `chi` router, `docker/docker/client`, `log/slog`, `robfig/cron/v3` — single binary
- **Tech stack — Frontend**: Svelte 5 + Vite + TypeScript + Tailwind, embedded into the Go binary via `//go:embed`, single page, no SPA router
- **Tech stack — Image**: Multi-stage Dockerfile, final stage `gcr.io/distroless/static:nonroot`, target <30 MB
- **Tech stack — Testing**: Playwright (`@playwright/test`) e2e + Go `testing` table-driven unit tests
- **Tech stack — CI/CD**: GitHub Actions → build → unit → e2e → publish to `ghcr.io/centroid-is/docker-update`
- **Architecture — C1. One container, one binary**: whole tool is a single OCI image with one process. No sidecars/init/helpers. Frontend bundle embedded.
- **Architecture — C2. File-based persistence only**: all state in `./hmi_update_state.json` (bind-mounted). Atomic writes. No SQLite/Mongo/Redis.
- **Architecture — C3. Self-contained compose deployment**: a single service block in the existing `docker-compose.yml` is all the on-HMI configuration required.
- **Process — C4. TDD: verify → implement → verify → implement**: every F-requirement starts as a failing Playwright test; implementation drives it green; manual smoke on HMI-like stack is required before "done."
- **Platform**: amd64 only for v1 (matches current HMI hardware). arm64 is a CI buildx flip later.
- **Security**: LAN-only, unauthenticated, matches WUD posture. Database (timescaledb) is `allow-update=false` / `allow-rollback=false` server-enforced.
- **Footprint**: <30 MB image, <30 MB RAM idle.
- **Repo**: Git repo `centroid-is/docker-update`. Image published to `ghcr.io/centroid-is/docker-update` with `:latest` tracking main, `:vX.Y.Z` per release, `:sha-<short>` per commit. The binary name, compose service name, healthz banner, log subject, and env-var prefix are all `docker-update` / `DOCKER_UPDATE_*`. The watched-container label namespace stays on `hmi-update.*` for backwards compatibility (see "Backwards-compatible label namespace" below).

### Backwards-compatible label namespace

The watched-container labels — `hmi-update.watch`,
`hmi-update.allow-update`, `hmi-update.allow-rollback`,
`hmi-update.tag-pattern`, `hmi-update.wait-for-healthy` — are
intentionally NOT renamed. Operators across the HMI fleet already have
these labels on dozens of compose service blocks; renaming the
namespace would force a coordinated edit on every HMI's
docker-compose.yml. Treat `hmi-update.*` as a stable public contract.
See `.planning/quick/260515-n1v-rename-hmi-update-docker-update-across-m/`
for the decision log.
<!-- GSD:project-end -->

<!-- GSD:stack-start source:research/STACK.md -->
## Technology Stack

## Executive summary
## Recommended Stack
### Backend — core
| Technology | Version | Purpose | Why |
|------------|---------|---------|-----|
| **Go** | **1.26.x** (current 1.26.3 — 2026-05-07) | Compiler / runtime | 1.23 is EOL; 1.26 brings cleaner `slog` ergonomics, the mature `net/http` routing introduced in 1.22, and is one of two supported lines. Pin in `go.mod` as `go 1.26`. |
| **`net/http` (stdlib)** | std | HTTP server + router | Go 1.22+ `ServeMux` supports method-scoped routes (`GET /api/containers/{name}/update`) and path variables via `r.PathValue("name")`. The API surface in this project (≈8 routes) does not justify a third-party router. Zero dependencies, zero version drift, no learning cost. |
| **`github.com/moby/moby/client`** | **client/v0.4.1** (2026-04-20) | Docker daemon client | **Replaces deprecated `github.com/docker/docker/client`.** Docker Engine v29 mandated the module rename. CVE-2026-34040 / CVE-2026-33997 are only fixed in this module path. Note: sub-1.0 semver — pin precisely in `go.mod`. |
| **`github.com/moby/moby/api/types`** | matched to client | API types | Same migration. Import `events`, `image`, `container` subpackages from here. |
| **`log/slog` (stdlib)** | std | Structured JSON logs | Brief is correct. Use `slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))`. Add `slog.SetDefault` once in `main`. |
| **`github.com/robfig/cron/v3`** | **v3.0.1** | Cron schedule for poll | Stable, no churn since 2024, accepts the literal `0 * * * *` format the user wants in `DOCKER_UPDATE_CRON`. Cron-string parity with the env-var contract matters here — the alternative (`gocron`) prefers a fluent API and would force users to learn a new syntax for the same env var. |
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
| **`docker/login-action@v3`** | GHCR push | Use `GITHUB_TOKEN` for GHCR; no secret needed for the `centroid-is/docker-update` org repo. |
## Installation
# Backend
# Frontend (run inside ./ui)
# E2E (run inside ./e2e)
## Detailed decisions — answers to the brief's questions
### 1. Go version (1.23 vs 1.24 vs 1.25/1.26)
### 2. Best Docker Go client
- **`containerd` client** — wrong layer; you'd lose Docker-specific labels, the events stream you actually want, and `RepoDigests` semantics.
- **Pure HTTP against the Docker socket** — gains you nothing, costs you typed responses.
### 3. Drive `docker compose` via CLI or Go library?
- **Surface area:** you only need three commands — `up -d --force-recreate <service>`, optionally `pull`, and `ps`/`config` for introspection. The Compose Go SDK (`github.com/docker/compose/v2`) brings in **hundreds** of transitive dependencies (buildx, BuildKit, containerd client, OTel, etc.) and would push the binary well past the 30 MB budget you have to amortize against the embedded Svelte bundle.
- **Compose version coupling:** subprocess uses the host's `docker compose` binary, which on a Debian HMI is whatever ships with Docker Engine. That's a feature, not a bug — your tool stays consistent with whatever Compose the operator already uses for everything else on the box.
- **Debugging:** an operator can run the exact same command by hand. With the SDK, they can't.
- **Testability:** swap `exec.Command` for a `Runner` interface in tests; trivial.
- Parse `docker compose` exit codes carefully, and capture stderr into the slog event.
- Pin the minimum Compose version (v2.20+) in README for the `--wait` flag and stable JSON output of `compose ps --format=json`.
### 4. HTTP router: stdlib `net/http` vs `chi`
### 5. Cron library
- Accepts the standard 5-field cron expression `0 * * * *` directly — matches the `DOCKER_UPDATE_CRON` env-var contract verbatim. Operators familiar with Linux cron read this and know exactly what it does.
- Stable, low-churn, single-author project but battle-tested across the Go ecosystem (Prometheus, Caddy, Kubernetes operators all use it).
- Supports timezone via `cron.New(cron.WithLocation(loc))`.
- **`go-co-op/gocron/v2`** — supports cron strings (`s.NewJob(gocron.CronJob("0 * * * *", false), ...)`) and has a richer fluent API. But the only use here is one hourly job, plus an event-driven kick from the docker events stream. The extra surface buys nothing.
- **`time.Ticker`** — would work fine if the schedule were always `time.Hour`, but the brief explicitly exposes `DOCKER_UPDATE_CRON` as a cron expression. A ticker would need a parser anyway, so just use the parser that already exists.
### 6. Frontend: Svelte 5, Vite, Tailwind versions
- **Svelte 5.55.x with runes.** Stable for over 18 months. Use `$state` for the table data, `$derived` for status badges, `$effect` for the 5-second polling timer. No stores needed for a single-page app.
- **Vite 7.x.** Don't jump to Vite 8 yet; `@sveltejs/vite-plugin-svelte@7` requires it and the ecosystem hasn't caught up. Vite 7 with `vite-plugin-svelte@6.x` is the calm path.
- **Tailwind v4.3.x with `@tailwindcss/vite`.** v4 is stable and the Vite plugin is the documented v4 install path. Config moves into CSS (`@theme { --color-primary: ...; }`) so you can delete `tailwind.config.js`. Removes `postcss-import` and `autoprefixer` deps.
### 7. Playwright + docker-compose pattern
### 8. Fake OCI registry
### 9. Distroless variant
- **Always pin the Debian suffix.** The unversioned `gcr.io/distroless/static:nonroot` tag is documented as "currently following `-debian13`, will change in the future." That's a footgun for an unattended HMI.
- **`static-debian12:nonroot`** is the conservative pick today: ~1.9 MB, includes `/etc/ssl/certs/ca-certificates.crt`, tzdata, and a `nonroot` user at UID 65532. Statically-linked Go binaries built with `CGO_ENABLED=0` run on it with no extra setup.
- **Migrate to `static-debian13:nonroot`** at a future milestone once CI has stabilized — the Debian 12 image will receive support for ~1 year after Debian 13's GA, then enter EOL.
- **Don't use `:nonroot-amd64`** unless you actually want to disable multi-arch manifest selection at the registry. Stick to `:nonroot`.
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
## Stack patterns by variant
- Add `linux/arm64` to the `docker/build-push-action@v6` `platforms:` list.
- Distroless multi-arch tags (`static-debian12:nonroot`) already work on arm64 without changes.
- Set `GOARCH=arm64` matrix in the Go build job.
- No application code changes required.
- `go-containerregistry` already supports credentials via `authn.Keychain`. Wire to a credentials file in the bind-mount volume.
- Add `RegistryAuth` (base64-encoded JSON) to `client.ImagePullOptions` when calling Moby.
- Document `~/.docker/config.json` mount path.
- Don't introduce a notification framework. Emit a webhook on action completion — operators bring their own dispatcher (ntfy, Slack incoming webhook, MQTT bridge).
- Use stdlib `http.BasicAuth` for v1 of auth — single shared credential in env var. Keeps the no-deps ethos.
- Only reach for an OAuth library if you grow multi-user / RBAC, which is out of scope.
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
<!-- GSD:stack-end -->

<!-- GSD:conventions-start source:CONVENTIONS.md -->
## Conventions

Conventions not yet established. Will populate as patterns emerge during development.
<!-- GSD:conventions-end -->

<!-- GSD:architecture-start source:ARCHITECTURE.md -->
## Architecture

Architecture not yet mapped. Follow existing patterns found in the codebase.
<!-- GSD:architecture-end -->

<!-- GSD:skills-start source:skills/ -->
## Project Skills

No project skills found. Add skills to any of: `.claude/skills/`, `.agents/skills/`, `.cursor/skills/`, `.github/skills/`, or `.codex/skills/` with a `SKILL.md` index file.
<!-- GSD:skills-end -->

<!-- GSD:workflow-start source:GSD defaults -->
## GSD Workflow Enforcement

Before using Edit, Write, or other file-changing tools, start work through a GSD command so planning artifacts and execution context stay in sync.

Use these entry points:
- `/gsd-quick` for small fixes, doc updates, and ad-hoc tasks
- `/gsd-debug` for investigation and bug fixing
- `/gsd-execute-phase` for planned phase work

Do not make direct repo edits outside a GSD workflow unless the user explicitly asks to bypass it.
<!-- GSD:workflow-end -->



<!-- GSD:profile-start -->
## Developer Profile

> Profile not yet configured. Run `/gsd-profile-user` to generate your developer profile.
> This section is managed by `generate-claude-profile` -- do not edit manually.
<!-- GSD:profile-end -->
