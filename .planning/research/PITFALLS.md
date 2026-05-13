# Pitfalls Research

**Domain:** Single-binary container update/rollback manager for OCI registries (Docker Compose-driven, distroless, bind-mounted state)
**Researched:** 2026-05-13
**Confidence:** HIGH on registry / Compose / `go:embed` / distroless mechanics (corroborated by Docker, distroless, vitejs, registry-spec issue trackers). MEDIUM on operational specifics for the elevator HMI display (no direct community precedent — derived from Weston/Wayland lifecycle behaviour).

This file is the safety net for a tool whose entire job is to recreate other people's containers. The pitfalls below are sorted by blast radius. The two named WUD 8.2.2 bugs from `hmi-update-brief.md` §1 are explicit reference points — Pitfall 1 and Pitfall 2 directly correspond.

---

## Critical Pitfalls

### Pitfall 1: Single-arch manifest digest extracted from the wrong field (the WUD 8.2.2 reference bug)

**What goes wrong:**
When `HEAD /v2/<repo>/manifests/<tag>` returns a *single-arch* image manifest (not a multi-arch index), the digest you compare against `RepoDigests[0]` must come from the `Docker-Content-Digest` **response header**, not from any field inside the parsed body. If you instead try to compute or re-derive the digest from the manifest JSON, you get a different sha256 because (a) whitespace/canonicalisation matters for the body hash and (b) the registry's authoritative digest is the one it stamped on the wire. WUD 8.2.2 ships with the wrong path for the single-arch branch — the fix-by-`sed` extracts `Docker-Content-Digest` properly.

**Why it happens:**
The OCI distribution spec lets the client derive the digest by hashing the (canonical) manifest, but in practice clients ship subtle JSON re-serialisation that doesn't match what the registry computed. Two registries (GHCR, Docker Hub) often round-trip the same body slightly differently. The code path that handles the multi-arch index branch tends to be exercised in tests; the single-arch fallback is the cold path and ships broken.

**How to avoid:**
- Always read `Docker-Content-Digest` from the HEAD/GET response headers, regardless of whether the body is an index or a manifest.
- HTTP header names are case-insensitive — use `resp.Header.Get("Docker-Content-Digest")` (Go normalises) but write a unit test that verifies behaviour against a server that returns `docker-content-digest` (lowercase) and `Docker-Content-Digest` (canonical).
- Never recompute the digest by re-hashing the body unless `Docker-Content-Digest` is missing — and when it is, that's a hard error, not a "fall back to body hash" silent path.
- Write a fake-registry test fixture that serves both branches: index → platform manifest, and direct single-arch manifest. Both must produce the digest the registry stamped.

**Warning signs:**
- The "update available" banner never clears even after running Update, OR clears immediately even when the registry has been bumped.
- `current_digest` in `hmi_update_state.json` differs from what `docker inspect` shows in `RepoDigests[0]`.
- Tests pass against ghcr.io but fail against `registry:2` (or vice versa).

**Phase to address:** Phase 2 — Registry / digest fetching. Failing Playwright test for F1 against the fake registry pushing both index *and* single-arch manifests is the gate.

---

### Pitfall 2: Anonymous token flow breaks when an empty-credentials header is sent (the second WUD 8.2.2 reference bug)

**What goes wrong:**
GHCR (and several other registries) advertise authentication via a `WWW-Authenticate: Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:<org>/<repo>:pull"` header on a 401. The expected anonymous flow is:
1. GET `/v2/<repo>/manifests/<tag>` with no `Authorization` header → 401 with `WWW-Authenticate`.
2. GET the `realm` URL with `service` + `scope` query params, **no** `Authorization` header.
3. Receive `{"token":"..."}` (anonymous JWT) → use it as `Authorization: Bearer …`.

WUD's bug is sending an `Authorization: Basic` header that base64-encodes an *empty* `username:password` string ("placeholder") during the token request. GHCR responds 403, not 401, and the layer-pull / manifest fetch then fails confusingly. The `sed` patch removes the empty-credentials header.

**Why it happens:**
A naive HTTP client helper always adds an `Authorization` header from a struct, even when both fields are empty strings, producing `Authorization: Basic Og==` (base64 of `:`). Most registries tolerate it; GHCR does not. Worse, the token endpoint with empty creds returns a *different* token shape (one with no `repository:…:pull` claim), so subsequent requests fail with "denied: requested access to the resource is denied" — not an obvious auth error.

**How to avoid:**
- Send `Authorization` headers only when credentials are non-empty. Build the HTTP request without the header for the anonymous case.
- For each registry, the *first* request is unauthenticated, parse `WWW-Authenticate` from the 401, request the token from `realm` with the scope from the challenge.
- Treat 403 as a configuration bug (wrong scope / wrong header / placeholder creds), not as "image is private." 401 means "you need a token"; 403 means "your token / header is wrong."
- Test matrix must include: GHCR public, Docker Hub public, `registry:2` (used as the fake registry in e2e), Quay.io (read-only smoke). All four must succeed anonymously.

**Warning signs:**
- Manifest HEAD returns 200 but layer pull (when force-pulling) returns 401/403.
- The token endpoint returns 200 but the resulting token, when decoded, has an empty `access` claim.
- Works against `registry:2` (which doesn't enforce auth) but fails against GHCR.

**Phase to address:** Phase 2 — Registry / digest fetching, same phase as Pitfall 1. Write a Playwright test that uses the **real** GHCR public read for a known stable public image (e.g. `ghcr.io/centroid-is/centroid-hmi:latest` or a frozen sha256 of a public image) before relying on the fake registry alone.

---

### Pitfall 3: Multi-arch index served when single-arch manifest expected (and vice versa)

**What goes wrong:**
The `Accept` header determines what the registry returns. Send only `application/vnd.docker.distribution.manifest.v2+json` and you get the platform-specific manifest *if the image is single-arch* — but a multi-arch image may give you a 404 ("manifest unknown") or, worse, a v1 manifest digest that doesn't match anything. Send only `application/vnd.oci.image.index.v1+json` and a single-arch image returns 404. The correct Accept header is a multi-valued comma-separated list including:
- `application/vnd.docker.distribution.manifest.list.v2+json` (Docker manifest list)
- `application/vnd.oci.image.index.v1+json` (OCI image index)
- `application/vnd.docker.distribution.manifest.v2+json` (Docker manifest)
- `application/vnd.oci.image.manifest.v1+json` (OCI manifest)

After the response arrives, inspect `Content-Type` to decide whether you got an index (drill in for `linux/amd64`) or a manifest (use the `Docker-Content-Digest` directly).

**Why it happens:**
The OCI Distribution spec doesn't mandate that registries handle missing/partial Accept gracefully. CloudFront-fronted registries actively reject "wrong order" Accept headers. Devs assume that omitting Accept means "registry will pick for you" — it does not. Single-arch test fixtures behave differently from real GHCR which serves indexes by default for `ghcr.io/centroid-is/*` builds.

**How to avoid:**
- Always send the full Accept matrix above on every manifest request.
- Branch on `Content-Type` of the response, not on what you expected.
- For an index: filter `manifests[]` to `platform.os=="linux" && platform.architecture=="amd64"` (`hmi-update` is amd64-only for v1 per the brief). Reject the digest if no matching platform — that's a real misconfiguration, not a silent skip.
- For a single manifest: read `Docker-Content-Digest` from the response (Pitfall 1).
- Test fixture: the fake registry must serve **both** an index and a direct manifest in the same test run, parameterised per test case.

**Warning signs:**
- "update available" works in dev (fake registry serves single-arch) but fails on the real HMI where GHCR serves an index.
- HEAD returns 200 but the body is empty / the Content-Type is unexpected.
- Different behaviour between `:latest-pg17` (timescaledb on Docker Hub, multi-arch) and `:latest` (centroid-hmi on GHCR, possibly single-arch depending on the build).

**Phase to address:** Phase 2 — Registry / digest fetching. The e2e test stack must push both shapes to the fake registry.

---

### Pitfall 4: `docker compose up -d --force-recreate <service>` doesn't pull, and silently uses the locally-tagged image

**What goes wrong:**
`up --force-recreate <service>` stops and recreates the named service container, but **does not pull** unless `pull_policy: always` is set on the service OR `--pull always` is passed. The update flow described in the brief is:
1. `docker pull <image>:<tag>`
2. Record prior digest.
3. `docker compose up -d --force-recreate <service>`.

If step 1 fails silently (network hiccup, registry 5xx, COMPOSE_HTTP_TIMEOUT trip), step 3 recreates the container against the *old* local image, the state file records the new digest as `current_digest`, and the running container is silently stale. Worse, after rollback, `docker compose up -d --force-recreate` *will* try to pull again if `pull_policy: always` is set, undoing the local re-tag and dragging the new image back. Pull policy interacts subtly with the re-tag step in rollback.

**Why it happens:**
- Compose treats `image:` as a target; if a local tag matches the spec it uses it.
- `pull_policy` defaults to "missing" — present tags are not re-pulled.
- The pull step in F2 is a separate `docker pull`, not `docker compose pull` — Compose has no idea it was run.
- COMPOSE_HTTP_TIMEOUT defaults to 60s; slow GHCR pulls of large images can trip it.

**How to avoid:**
- After `docker pull`, **verify** the local image's `RepoDigests[0]` matches the digest you fetched from the registry *before* you proceed to the compose recreate. If they don't match, abort with a clear error and don't touch state.
- Set `pull_policy: missing` (the default) explicitly in the compose for watched services — never `always`, because it will fight rollback.
- For rollback: after `docker tag <image>@<prev_digest> <image>:<tag>`, verify the local resolution. Then `docker compose up -d --force-recreate --no-pull <service>` (or rely on `pull_policy: missing`). Test that rollback survives even when there's no network.
- Set `COMPOSE_HTTP_TIMEOUT=300` (or pass `--timeout`) for the recreate step, document why.
- Record `current_digest` *only after* verifying the recreated container's `RepoDigests[0]` matches the target.

**Warning signs:**
- UI shows new digest but `docker inspect <container> | jq .Image` shows the old image ID.
- Rollback button "works" in dev but produces the wrong digest in production.
- "Container recreated" log line, but operator reports no behaviour change.

**Phase to address:** Phase 3 — Update/rollback execution. The e2e acceptance test for F2 must assert the running container's `RepoDigests[0]` after recreate, not just the state file content.

---

### Pitfall 5: Recreating HMI UI containers blanks the operator's display

**What goes wrong:**
The HMI stack runs `flutter` and `weston` containers that draw the operator's screen. `docker compose up -d --force-recreate flutter` stops the running container — at that instant the Wayland client disconnects from the Weston compositor and the screen goes black (or shows the previous frame frozen) until the new container starts, re-binds the Wayland socket, and the Flutter renderer reaches first paint. Worst case (cold-pull large image, slow disk) this is 5–30 seconds of a blank elevator screen with a field engineer standing next to it. Recreating `weston` itself is worse — it tears down the compositor, taking every Wayland client (flutter, others) with it.

**Why it happens:**
Compose treats containers as fungible processes; it has no concept of "this container draws the screen." The operator-visible window between stop-old and run-new is dominated by image extraction (if the new layers aren't already pulled-and-prepared) and the application's own cold-start time, not by `docker` itself. Field engineers expect "click button → thing updates" — they don't expect a 30-second blackout.

**How to avoid:**
- Pre-pull and extract the new image **before** any container stop happens. The F2 flow already does `docker pull` first; ensure it waits until the local image is fully present (not just the manifest) before recreate.
- Display a clear "Updating <service>: display will refresh in ~Xs" toast in the UI *before* triggering recreate, so the engineer is not surprised. The brief mentions toasts in F6 — repurpose them.
- For `weston` specifically: document explicitly that updating it will black out all clients. Recommend (in README) that `weston` carries `hmi-update.allow-update=false` unless the engineer is on-site and ready for the blackout, OR provide a per-service "danger" flag in the UI that requires double-confirm.
- Log the wall-clock duration of the stop→running gap for every recreate so operator-visible downtime is measurable (N7 logging).

**Warning signs:**
- Field engineer reports "screen went black for ages."
- The action-completion toast fires before the new container has actually drawn its first frame.
- The new container exits before producing output and the screen stays black indefinitely (the restart policy might be `unless-stopped` but the operator sees "frozen").

**Phase to address:** Phase 3 — Update/rollback execution. UX warning belongs in Phase 5 — UI polish. Document the `weston` caveat in Phase 6 — deployment docs.

---

### Pitfall 6: Self-update — `hmi-update` cannot recreate itself

**What goes wrong:**
The brief sets `hmi-update.watch=false` on the `hmi-update` service in F7. Good — but the field engineer will eventually want to update `hmi-update` itself when a new version ships. If they click "Update" on a row that *does* monitor itself, the container running the update logic gets killed mid-recreate; `docker compose up -d --force-recreate` started by the dying process orphans, and you're left with either a stale container or no `hmi-update` container at all. Watchtower hit the same issue and ended up adding "ephemeral self-update" (a short-lived orchestrator container) — outside this project's scope.

**Why it happens:**
A process cannot fork-and-replace itself via `docker compose` because the `docker` CLI's child process is reparented when the calling process dies. `compose up -d` returns quickly, but the recreate workflow includes "stop the old container" which kills our own process before it can confirm success.

**How to avoid:**
- **Hard ban self-update via the UI.** With `hmi-update.watch=false`, the service does not appear in the table — already correct per F7. But also: server-side, refuse `POST /api/containers/hmi-update/update` (or whatever the service is named) with 409 even if a caller fakes the label.
- The actual update path for `hmi-update` itself is documented as: `docker compose pull hmi-update && docker compose up -d hmi-update` from a shell on the host. This is a one-time field-engineer step, not a UI button.
- Detect the self-service name at startup (read `HOSTNAME`, match it against the running container's compose service via `docker inspect` and the `com.docker.compose.service` label). Store the result; reject any update/rollback request targeting that service.
- Don't try to support self-update in v1. If a future version needs it, follow Watchtower's ephemeral pattern (a one-shot orchestrator container) — but that's a whole new milestone.

**Warning signs:**
- 500 errors when `POST /api/containers/<own-service>/update` is called.
- `hmi-update` container disappears from `docker ps` after a self-update attempt; operator has to SSH in.
- Two `hmi-update` containers running simultaneously with the same port mapping (port conflict; both crash).

**Phase to address:** Phase 3 — Update/rollback execution. Self-protection check belongs in the API handler. Document the manual update path in Phase 6 — deployment.

---

### Pitfall 7: JSON state file corruption from non-atomic write or concurrent writers

**What goes wrong:**
Two updates fired within milliseconds (operator double-click, or cron + manual concurrent), or a power loss / `docker kill` mid-write, leave `hmi_update_state.json` either truncated (zero or partial bytes) or with stale-then-new content interleaved. On next boot the JSON parser fails ("unexpected EOF" or "invalid character"), and the service either crashes loop or — if the recovery path is "ignore and start fresh" — silently loses every `previous_digest`, meaning rollback no longer works for any container until the next update establishes a new previous.

**Why it happens:**
- `os.WriteFile` does `open(O_CREAT|O_TRUNC|O_WRONLY)` + `write` — not atomic, the file is empty between truncate and write.
- Multiple goroutines (cron poller + HTTP handler) racing to write the same path produce interleaved output.
- The brief mandates atomic temp-then-rename (F4, C2). The risk is implementing it wrong: writing to `state.json.tmp` without `fsync` before rename → rename succeeds, kernel hasn't flushed the new bytes, host crash leaves an empty `state.json`.

**How to avoid:**
- Use the canonical atomic-write pattern, with `fsync` on both the temp file and the containing directory:
  1. Create unique temp file in the same directory as the target (rename is only atomic on the same filesystem).
  2. Write full contents.
  3. `fsync` the temp file.
  4. `os.Rename(tmp, target)`.
  5. Open the parent directory, `fsync` it.
- Serialise writes through a single goroutine fed by a channel, or use a `sync.Mutex` around the write path. Multiple writers cannot share an atomic-rename file safely without coordination — the rename "wins" but the loser's content is lost.
- On boot, if `state.json` parses fail, look for `state.json.tmp` and try to recover from it (validate JSON, then promote with atomic rename). If both are corrupt, log loudly and start with empty state — but never silently overwrite a corrupt file without a backup copy (`state.json.bak.YYYYMMDD-HHMMSS`).
- Schema-version every write (`"version": 1`). On read, if `version` is missing or > known, abort with a clear error rather than misinterpreting.
- Validate the parsed structure (every container has `image`, `tag`, at least one of `current_digest`/`previous_digest`) before accepting it.

**Warning signs:**
- Service starts but the API returns no containers / empty table.
- `docker logs hmi-update` shows "invalid character" or "unexpected EOF" at startup.
- The state file's mtime updates but contents are zero bytes.
- Rollback button is disabled (no `previous_digest`) after a service restart that previously had one.

**Phase to address:** Phase 4 — State persistence. The acceptance test for N2 (stateless self-restart) covers this if the test deliberately kills the container mid-write — add a fault-injection variant.

---

### Pitfall 8: `//go:embed` + Vite hashed assets — wrong MIME type and SPA fallback eating assets

**What goes wrong:**
Two distinct failures, both fatal for the embedded SPA:

1. **MIME type:** Serving `embed.FS` via `http.FileServer` with a too-aggressive SPA fallback returns `index.html` (Content-Type: `text/html`) for `/assets/index-a1b2c3d4.js`. The browser blocks the module load with "Failed to load module script: Expected a JavaScript module script but the server responded with a MIME type of 'text/html'." The UI is blank.
2. **Cache busting on binary upgrade:** Vite emits hashed filenames (`index-<hash>.js`). After a `hmi-update` upgrade the new bundle has new hashes, but the operator's browser is still on the old `index.html` from `Cache-Control: max-age=…` or a service worker — it requests the old hashed assets, which the new binary doesn't have, → 404 → blank UI. Or worse: the new `index.html` is fetched but references the new hashed JS, while the *old* SW intercepts and returns the old cached chunk — chunk-load error.

**Why it happens:**
- Default `http.FileServer` over `embed.FS` doesn't always set the right Content-Type if the file extension lookup misses (some Go versions, some build environments).
- The naïve SPA-fallback pattern (`if file not found → serve index.html`) matches everything — including `/assets/foo.js` if the embed didn't include `assets/` for some reason — and returns HTML for what should be 404.
- `index.html` is often served with no cache headers, so the browser revalidates. Hashed assets are immutable so they're sent with long max-age. If `index.html` is *also* sent with long max-age (a misconfiguration), the browser keeps loading the old SPA after a binary upgrade.

**How to avoid:**
- Use `fs.Sub(embed, "ui/dist")` to root the FS at the build output dir. Route registration:
  - `/api/*` → API handler.
  - `/assets/*` → strict static serve from the sub-FS, with Content-Type derived from `mime.TypeByExtension` on the extension. If not found in the FS, return 404 — **never** fall back to `index.html` for `/assets/*`.
  - Everything else → `index.html` (the SPA fallback).
- Explicitly register MIME types at init() time: `.js → application/javascript; charset=utf-8`, `.css → text/css; charset=utf-8`, `.svg → image/svg+xml`, `.wasm → application/wasm`. Go's stdlib `mime` package may not include all of these in distroless minimal envs.
- Set `Cache-Control: public, max-age=31536000, immutable` for `/assets/*` (hashed, safe).
- Set `Cache-Control: no-cache` (or `max-age=0, must-revalidate`) for `index.html`. The page must always be re-fetched so post-upgrade asset references are current.
- Do not register a service worker in v1 — they massively complicate the upgrade story for marginal benefit on a LAN page.
- Build-time check: Playwright e2e test asserts the served `/assets/<hash>.js` has Content-Type `application/javascript` and is non-empty.

**Warning signs:**
- Open DevTools, browser shows "MIME type ('text/html') is not executable" for an `/assets/*.js` URL.
- After upgrading the `hmi-update` image, the UI breaks until a hard refresh (Ctrl-Shift-R).
- Operator reports "the page is blank but the API works."

**Phase to address:** Phase 5 — UI / embedding. The Playwright test suite must include a "post-upgrade in-place" scenario: upgrade `hmi-update` image while the page is open, soft-refresh, page works.

---

### Pitfall 9: Distroless `nonroot` cannot access `docker.sock` because of GID mismatch

**What goes wrong:**
`gcr.io/distroless/static:nonroot` runs as UID/GID 65532. On the host, `/var/run/docker.sock` is owned by `root:docker` (or `root:998`, etc., depending on the distro). When the socket is bind-mounted into the container, the container sees `srw-rw---- root:<host-docker-gid>`. The nonroot user isn't a member of any GID matching the host's `docker` group, so `connect()` returns `EACCES`. Every Docker API call fails with "permission denied while trying to connect to the Docker daemon socket."

**Why it happens:**
Distroless is built for security and ships with `/etc/group` containing only `nonroot:x:65532:`. There's no way to `addgroup` inside the distroless image, and the host's `docker` GID varies (998, 999, sometimes 100). Even if you fix the GID at build time it'll mismatch on a different HMI.

**How to avoid (pick one):**
- **Option A — pass the GID at runtime via `user:` in compose:** Compose service block sets `user: "65532:<docker-gid-on-host>"`. The container starts as nonroot UID but with the host's docker GID as its primary group. Document `id -g docker` on the HMI as the install-time check. (Best fit for the brief's posture.)
- **Option B — drop to `static-debian12` (still distroless, has CA certs and tzdata) and pre-create a `docker` group with a GID matching the most common Debian value (999), then provide an override env var to switch.**
- **Option C — socket proxy sidecar.** Violates C1 ("one container, one binary"). Don't.
- Document the failure mode in the README. The check at boot: open the socket; on EACCES, log "Docker socket permission denied. Likely cause: container GID does not match host docker group. Run `id -g docker` on the host and set `user: \"65532:<that-gid>\"` in compose."
- Also: CA certs are needed for outbound TLS to `ghcr.io`. `gcr.io/distroless/static:nonroot` ships ca-certificates per the official README, but some forks/builds don't — verify by curl-ing against ghcr.io in CI's smoke test.

**Warning signs:**
- `/healthz` returns 500 with "permission denied" referencing `docker.sock`.
- Local dev works (developer's UID matches docker group); HMI install fails on first boot.
- The error message is `dial unix /var/run/docker.sock: connect: permission denied`.

**Phase to address:** Phase 1 — Docker client / scaffolding. The boot-time health check (N8) must distinguish "socket unreachable" from "socket EACCES" and log the remediation hint.

---

### Pitfall 10: Compose file path drift — bind-mount points at a stale file

**What goes wrong:**
The compose service mounts `./docker-compose.yml:/host/docker-compose.yml:ro` and the binary invokes `docker compose -f /host/docker-compose.yml up -d --force-recreate <service>`. If the operator edits the compose file on the host after `hmi-update` starts, the bind-mount reflects the new content (bind-mounts are pass-through). But — if they *rename or replace* the file (some editors do "save as temp + rename"), the bind-mount may point to the deleted inode, and the container sees a file that no longer matches the host. Worse, if they `docker compose up -d` from a *different* compose file (after moving the HMI directory) the `hmi-update` container is still acting on the old path.

**Why it happens:**
Bind mounts pin an inode at mount time. Editors that do atomic save (vim with `:w`, VS Code, Helix) replace the file, which can break the bind-mount link until the container is restarted. Operators may relocate the HMI install directory without realising `hmi-update` was started from the old location.

**How to avoid:**
- On every cron tick and at the start of every Update/Rollback action, `stat` the compose file. Compare its mtime to the cached one — if changed, re-read it and validate it still has the services we care about.
- If the compose file is unreadable (inode gone, ENOENT), refuse to perform updates with a clear UI message: "Compose file at /host/docker-compose.yml not found — restart hmi-update after fixing the path."
- Document in the README: "If you edit `docker-compose.yml` with an editor that does atomic save, restart `hmi-update` afterward." (Or: bind-mount the *directory*, not the file — but that mounts everything in the dir.)
- Compose service name as the API identifier (already a key decision) is robust against container renames, but stale compose files defeat it from a different angle.

**Warning signs:**
- Update succeeds but the wrong service is recreated (the compose `services:` block has drifted).
- `docker compose up` from `hmi-update` errors with "service X not found in compose file" — service was renamed or removed.
- `compose down` followed by `up` on the host orphans `hmi-update` from the network.

**Phase to address:** Phase 1 — Docker client / scaffolding (compose-file reader); Phase 3 — Update/rollback execution (stat-before-act).

---

### Pitfall 11: Concurrent updates from a double-click or cron-vs-manual race

**What goes wrong:**
Operator clicks Update, sees the toast appear, isn't sure it registered, clicks again. Two `POST /api/containers/flutter/update` requests fire. The first one is mid-`docker pull`; the second one starts a *second* `docker pull` (idempotent), then both proceed to `docker compose up -d --force-recreate`. Compose serialises at the daemon level, but the *state writes* from the two handlers can race: handler A writes `previous=X, current=Y`; handler B reads the file before A writes, writes `previous=X, current=Y` again (or worse, `previous=Y, current=Y`, losing rollback target).

**Why it happens:**
HTTP handlers run concurrently by default. The state file lock is a separate concern from per-service action exclusivity. Cron pollers running at the same moment as a manual update can interleave reads of the registry with the in-progress local state mutations.

**How to avoid:**
- Per-service mutex (a `map[string]*sync.Mutex` keyed on service name). All Update/Rollback handlers take the service's mutex before touching anything; concurrent requests for the same service either queue or return 409 "Update already in progress."
- Return 409 immediately for concurrent requests on the same service rather than queueing — the UI's optimistic state makes queuing confusing for the operator ("did my second click work or not?"). Re-enable the button only after the first action completes (UI side) AND the server-side mutex is released.
- The cron poller never *writes* operational state (only `current_digest_available` / `update_available` flags) — its read of registry digests is purely informational. The Update handler is the only path that writes `previous_digest`.
- Disable the Update button in the UI from click → action completion. Toast on success/failure.

**Warning signs:**
- Two near-simultaneous "Updated X" log entries in `slog`.
- `previous_digest == current_digest` after a sequence of clicks.
- Rollback fails 409 "no previous_digest" right after what looked like a successful update.

**Phase to address:** Phase 3 — Update/rollback execution (mutexes); Phase 5 — UI (button disable + toast on completion).

---

### Pitfall 12: Restart policy interactions during update

**What goes wrong:**
The watched containers (flutter, weston, etc.) have `restart: unless-stopped`. During Update, `docker compose up -d --force-recreate` stops the old container — Docker honours the policy by *not* restarting the old stopped container (it was an explicit stop, not a crash). Good. But: if the new container, just created, exits immediately (bad image, missing env var, missing socket on first try), the restart policy will keep restarting it in a loop. The Update HTTP handler returns 200 OK based on "compose up returned 0," but the container is in a crash loop a second later. The state file is written claiming success; the UI shows green. Field engineer thinks the update worked.

**Why it happens:**
`docker compose up -d` is fire-and-forget; exit code 0 only means "Docker accepted the spec." It says nothing about container health.

**How to avoid:**
- After `compose up -d --force-recreate <service>`, poll `docker inspect <container>` for up to 15 seconds and assert `State.Running == true && State.Health.Status != "unhealthy"` (if a healthcheck exists), AND `State.RestartCount` hasn't incremented since the recreate.
- If the new container is not healthy after the deadline, log the failure with the container's last logs (read `docker logs --tail 100`) and the user-visible toast says "Update failed: new container is crashing — rollback recommended." The state file records the attempt but the `current_digest` field is *not* updated to the new digest.
- Show `RestartCount` in the UI as a column when nonzero (it's a strong signal).
- Per acceptance criterion 3 in the brief: "container is recreated on the new digest within 30 s" — the e2e test must verify the container is *running*, not just present.

**Warning signs:**
- Update returns 200, then a few seconds later operator notices the screen is broken / service unreachable.
- `docker ps` shows the watched container with status "Restarting (1) 5 seconds ago" but `hmi-update` reports it as green.
- `RestartCount` climbs every 10 seconds.

**Phase to address:** Phase 3 — Update/rollback execution (verify-recreate-succeeded loop); Phase 5 — UI (show restart count).

---

### Pitfall 13: SSRF / path traversal via API parameters

**What goes wrong:**
Two attack surfaces, both small but real on a LAN-only deployment:

1. **Path traversal in `<service>` parameter:** `POST /api/containers/../../../../etc/passwd/update` — if the handler interpolates the service name into a shell command, into a file path, or into a Docker label query without strict validation, you can hit unexpected code paths. Even on a LAN, a malicious DHCP rogue on the same VLAN can probe.
2. **SSRF via custom registry URLs:** Although the brief currently watches only GHCR and Docker Hub, a future "custom registry" feature could let a label `hmi-update.registry=http://169.254.169.254/latest/meta-data/` send requests to cloud metadata services or internal-only HTTP endpoints.

**Why it happens:**
- LAN-only / unauthenticated (N5) reduces but doesn't eliminate risk — anyone on the elevator HMI's network (a contractor's laptop, a switch with PoE-attached IoT) can hit the API.
- Service names are operator-controlled in the compose file but parsed from the URL.

**How to avoid:**
- Validate `<service>` against `^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$` at the router layer. Reject anything else with 400.
- Look up the service name in the in-memory watched-containers map, not in any filesystem path or shell command. If the name isn't a known watched service → 404. (Don't echo the unvalidated name in the error message.)
- Never construct shell commands with operator-supplied strings. Use `exec.Command("docker", "compose", "-f", composeFile, "up", "-d", "--force-recreate", svc)` — args, not strings. (Idiomatic in Go but worth a code-review checklist item.)
- For the (deferred) custom-registry case: validate the registry URL is `https://` (or `http://` for explicitly allowed test registries), DNS-resolve it once, reject if it resolves to RFC1918 private addresses unless explicitly allowed. Today this is out-of-scope (private registry creds deferred), but document the constraint for when it lands.
- `GET /api/state` exposes everything — digests, image names, action timestamps. Digests are not secret. Image names are not secret. But document this: anything in the state file is readable by any LAN client.

**Warning signs:**
- Logs show requests for service names not in the compose file.
- 4xx rates climb suddenly.
- `/api/state` queried from unexpected IPs.

**Phase to address:** Phase 3 — API surface (input validation). Phase 6 — security hardening review before release.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Skip atomic write, use `os.WriteFile` directly | Code is 3 lines, ships in v0.0.1 | Power loss / `docker kill` truncates state, rollback target lost for every container — silently | Never. C2 mandates atomic temp+rename and the brief is explicit. |
| Hardcode `linux/amd64` filtering without making it a constant | Works for v1, brief says amd64 only | Adding arm64 later (Q1 in brief) means hunting magic strings | Acceptable if `runtime.GOARCH` is used as the source of truth, so an arm64 binary just works |
| Use single global mutex for all state writes instead of per-service | Simpler code | Serialises all updates; a slow registry call blocks unrelated operations | Acceptable for v1 (low concurrency, single operator) — promote to per-service when noticeable |
| Skip the verify-after-recreate poll (Pitfall 12) | One fewer code path | "Updated" lies; field engineers stop trusting the UI | Never. Brief acceptance criterion 3 demands it. |
| Hardcode docker GID (e.g. 999) in the image | Container "just works" on Debian 12 | Breaks on Ubuntu (varies), HMIs that use a different distro family | Acceptable if `user:` override in compose is documented and the README forces the install-time check |
| Skip Vite hashed-asset SPA-fallback exclusion (Pitfall 8) | Code is one route handler | Browser breaks after binary upgrade until hard refresh; intermittent blank UIs | Never. Make `/assets/*` strictly served, no fallback. |
| Use `WATCHTOWER`-style "watch everything unless labeled off" | Less label boilerplate | One unlabeled compose file with `traefik` or `seatd` gets updated and breaks the HMI | Never. Explicit opt-in via `hmi-update.watch=true` is the design (F1). |
| Defer healthcheck verification on recreate (Pitfall 12) to a post-MVP iteration | Faster F2 ship | The first time a real update goes wrong (bad image push), the engineer trusts the green UI | Never — Pitfall 12 prevention is part of F2's "done." |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|------------------|
| GHCR anonymous pull | Send empty `Authorization: Basic Og==` header → 403 instead of 401 | Send no `Authorization` header for first probe; respond to `WWW-Authenticate` by hitting `/token` with the exact scope from the challenge |
| Docker Hub multi-arch | Assume single-arch response, look for digest in body | Send the full Accept-header matrix, branch on `Content-Type`, drill into `linux/amd64` if index |
| Docker Hub rate limits | Anonymous pull rate-limit (100/6h per IP) — silent 429 after a noisy push window | For Docker Hub specifically: detect 429, back off and log; don't spam-poll. Brief's default 1h cron is fine. |
| `registry:2` fake registry in e2e | Single-arch behaviour only — passes tests, real GHCR breaks | Test stack must include manifests pushed via `docker buildx imagetools create` so both index and manifest paths are exercised |
| Docker socket from distroless nonroot | Container can't connect: EACCES | `user: "65532:<host-docker-gid>"` in compose, document the install-time `id -g docker` check (Pitfall 9) |
| `docker compose` CLI vs `docker/docker/client` Go SDK | Mixing both — calling the CLI for compose, the SDK for inspect — works but spawns subprocesses for every recreate | Acceptable for v1 (compose API isn't first-class in the SDK). Document the dual-client choice. Log every CLI invocation with the full argv. |
| Vite `base` config | Defaults to `/`; if served behind a subpath later, asset URLs break | Set Vite `base: '/'` explicitly; document that `hmi-update` is always served at root |
| Compose file editing while running | Atomic-save editors break the bind-mount inode | Document; also `stat` the file before each action (Pitfall 10) |
| `docker pull` mid-update | Network timeout → silent half-pull → recreate against stale local image | Verify `RepoDigests[0]` of the local image matches the registry digest *before* recreate (Pitfall 4) |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| Cron poll fan-out: parallel `HEAD /manifests/<tag>` to all watched repos with no rate limit | Spikes of 5+ requests/sec; Docker Hub rate-limits after 100 anonymous calls in 6h on the HMI's egress IP | Sequential polls with small jitter; cache the bearer token per registry for its TTL | At ~5 watched containers polled hourly that's 5/h — fine. At an HMI with 20 services polling every 5 minutes (operator misconfigures cron), throttling appears |
| State file written on every poll regardless of change | Disk wear on SSD-based HMIs, slow writes due to fsync on every tick | Only write when something actually changed (compute hash of in-memory state, compare) | After months of uptime: SSD wear-out warnings, slow `/healthz` response time |
| Goroutine per watched container | Easy concurrency, scales by service count | A bounded worker pool (default GOMAXPROCS) feeding from a poll queue | At 50+ watched services on a tiny HMI, memory pressure |
| Embedded UI not gzipped at build time | 30 MB image, fine — but the JS bundle is 200kB uncompressed served raw | `gzip` the bundle at build time (Vite `viteCompression` or embed-time gzip), serve with `Content-Encoding: gzip` | Browser-side: ~300ms vs ~50ms first paint over a slow LAN |
| `docker inspect` called once per container per UI refresh | At 5s UI refresh and 10 watched containers, 2 inspect/s | Cache inspect results for the duration of one UI refresh (5s) | Daemon CPU at 0.5% just from inspect spam at idle — fine, but noisy in `/var/log` |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Mount `docker.sock` read-write into the `hmi-update` container | Container has root-equivalent on the host. Anyone who can hit the API has root. (Already part of design — acknowledge it explicitly in README.) | Document the trust model. LAN-only (N5). Never expose port 8080 outside the LAN. No external auth proxy in v1, but document that adding one is the v2 upgrade path. |
| Trust the compose file's `image:` field as the registry to query | A malicious compose edit pointing `image:` at `attacker.com/evil:latest` could be polled with creds (future) or hit attacker-controlled endpoints | Today: anonymous-only polls, low risk. Future: allow-list of registries in `hmi-update` config. |
| Echo unvalidated path components in error messages | Reflected XSS if the UI renders error text as HTML, log poisoning | Strict service-name validation (Pitfall 13). UI renders errors as text, never `innerHTML`. |
| Leave `/api/state` open to LAN | Anyone on the LAN can read every container's digests, image names, and timestamps | Acceptable per N5 (matches WUD posture). Document. Add basic auth as a future requirement, not v1. |
| Allow custom registry URLs without validation (future feature) | SSRF to `169.254.169.254` (cloud metadata), internal-only services, file:// schemes | Reject non-`https://` URLs; reject RFC1918 IPs unless explicitly allowed; reject `file://` (Pitfall 13) |
| Run as root in the container "to keep things simple" | Defeats distroless `nonroot` posture | Always run as nonroot (UID 65532) with the host-docker GID as supplementary group |
| Log full bearer tokens in `slog` lines (for debugging) | Tokens in `docker logs` rotate into the host's journal | Redact `Authorization` headers and `token` fields before logging; log scope + expiry instead |

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|-------------|-----------------|
| Auto-refresh every 5s wipes the optimistic state of a just-clicked button | Field engineer clicks "Update," button reverts to enabled before the action visibly completes — they click again | Disable the button on click, hold it disabled until the *server* confirms completion (not just the next 5s refresh) |
| Show only the short digest (first 12 chars) with no copy-to-full action | Engineer can't paste a full digest into Slack or a bug report | Per F6: copy-icon per digest, full string copied (already in brief — verify it makes it into the build) |
| Show "Update available" the instant the cron fires, with no indication of when the registry was last polled | Engineer wonders "is this stale?" | Always render `last-poll timestamp` in the header (already in F6 — keep it prominent) |
| Recreate-during-display-active screen blackout with no warning (Pitfall 5) | Operator stands next to a black elevator screen for 10–30s | Pre-action toast: "Updating <service>. Display may flicker." Plus duration logging post-action. |
| Show one big green checkmark for "update succeeded" before the new container is verified healthy (Pitfall 12) | Engineer trusts a lie | Verify-after-recreate. Toast says "Update succeeded — new container healthy" only after the poll passes. |
| "Rollback" button enabled even when there's no `previous_digest` | Engineer clicks, gets 409 with a confusing error | UI hides the button when `previous_digest` is null. Server still returns 409 for direct API hits (defense in depth, N4). |
| No distinction between "registry unreachable" and "no update available" | Engineer sees green and assumes things are fine — but the poller has been failing for hours | Last-poll-status: green = "OK", yellow = "stale", red = "last N polls failed." Visible in the header. |
| Confusing "Force Pull" vs "Update" labelling | Engineer doesn't know which button to use when a local image was accidentally removed | Distinct icons + tooltip. Force-pull is a "recovery" action; Update is the normal flow. |
| Database row shows "Update" button even though `allow-update=false` is set | Engineer clicks, gets 409, doesn't understand | Hide the button (per N4). Show a small lock icon with a tooltip "Updates disabled by label." |

## "Looks Done But Isn't" Checklist

- [ ] **F1 update detection:** Multi-arch index handling works against GHCR — verify with a real `ghcr.io/centroid-is/*` repo *and* a single-arch `registry:2` fake. Both must produce a digest that `docker inspect` agrees with after a pull.
- [ ] **F2 update execution:** After `compose up -d --force-recreate`, the handler polls `docker inspect` for `State.Running == true` and `State.RestartCount` unchanged from before the recreate (Pitfall 12). Don't just trust the compose exit code.
- [ ] **F3 rollback:** Tested with the network unplugged from the HMI. Rollback must work entirely from local images. (Pitfall 4)
- [ ] **F4 state persistence:** Atomic write tested with `kill -9` on the hmi-update process mid-write — verify file is either old-and-valid or new-and-valid, never truncated. (Pitfall 7)
- [ ] **F4 state persistence:** Schema-version (`"version": 1`) honored on read — a `"version": 2` state file from a future binary is rejected with a clear error, not silently coerced.
- [ ] **F5 tag pattern:** Works for the timescaledb case (`^latest-pg17$` suppresses `latest-pg18-oss`). Regex compile errors fail-loudly at startup, not at first match.
- [ ] **F6 UI:** Browser DevTools shows correct Content-Type for every `/assets/*.js` (`application/javascript`), `/assets/*.css` (`text/css`), `/assets/*.svg` (`image/svg+xml`). (Pitfall 8)
- [ ] **F6 UI:** After upgrading `hmi-update` itself, soft-refresh in a still-open tab loads the new UI; old asset hashes do not 404. (Pitfall 8 / cache headers)
- [ ] **F7 deployment:** A first-time HMI install with the documented `id -g docker` step works on a clean Debian 12 box. (Pitfall 9)
- [ ] **F7 deployment:** `hmi-update.watch=false` on `hmi-update` itself is enforced server-side too, not just by the label. (Pitfall 6)
- [ ] **F8 force-pull:** Verifies the pulled image's digest matches the registry's `Docker-Content-Digest` before reporting success. (Pitfall 4)
- [ ] **N2 stateless self-restart:** `docker compose restart hmi-update` mid-update — verify no partial-state corruption; if the restart happens after `docker pull` but before `compose up -d`, the state file should not have written the new digest yet.
- [ ] **N4 allow-update=false:** Direct `curl` to the API with `allow-update=false` set returns 409 — UI behaviour is not enough.
- [ ] **N7 logging:** Bearer tokens redacted in every log line. `grep "Bearer "` in `docker logs hmi-update` returns nothing.
- [ ] **N8 healthz:** Returns 500 with a distinct error message for "socket EACCES" vs "socket missing" vs "state file unreadable." (Pitfall 9)
- [ ] **CI:** Playwright e2e suite runs against both `registry:2` and a snapshot of real GHCR responses (recorded fixtures) to catch single-arch-vs-index regressions. (Pitfalls 1, 3)

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| Pitfall 1: wrong digest extracted, state file says wrong `current_digest` | LOW | Click Update again on the affected row once the digest-extraction code is fixed. The next poll will reconcile. State is self-healing. |
| Pitfall 2: 403 from GHCR — entire poller dead | LOW | Restart `hmi-update`. Tokens are short-lived; the next request retries. Fix the empty-header bug in code. |
| Pitfall 4: silent stale image after Update | MEDIUM | Manually `docker pull` + `docker compose up -d --force-recreate <service>` from the host. The next poll will report the correct digest. Add verify-after-recreate to prevent. |
| Pitfall 5: display blackout caught operator by surprise | LOW (one-time UX) | Add the pre-action toast; document. No data loss. |
| Pitfall 6: self-update attempted, `hmi-update` container missing | MEDIUM | SSH to the HMI: `docker compose up -d hmi-update`. Add server-side self-protection to prevent. |
| Pitfall 7: corrupt state file | HIGH (data loss for `previous_digest`) | If `.tmp` exists and is valid, promote it. Otherwise, restore from the most recent `.bak.*` (if backups were written). Otherwise, accept that rollback is unavailable until the next manual Update establishes a new `previous_digest`. The current digest can always be recovered from `docker inspect`. |
| Pitfall 8: UI blank after binary upgrade | LOW | Hard-refresh (Ctrl-Shift-R) on the operator's browser. Fix cache headers + SPA-fallback to prevent. |
| Pitfall 9: `docker.sock` EACCES after install | LOW | Set `user: "65532:<docker-gid>"` in compose, `docker compose up -d hmi-update`. README documents. |
| Pitfall 10: stale compose file path | MEDIUM | Restart `hmi-update`. Verify with `/healthz` that the compose path is now valid. |
| Pitfall 11: state corrupted from concurrent writes (`previous == current`) | MEDIUM | Manually edit `hmi_update_state.json` (with the service stopped) to reset `previous_digest` to null. Next Update will populate it correctly. |
| Pitfall 12: silent crash-loop after Update | LOW–MEDIUM | Rollback (assuming `previous_digest` is intact). If rollback also fails, manually `docker tag <image>@<known-good-sha> <image>:<tag>` from the host. |
| Pitfall 13: malicious LAN client probes API | LOW | Block source IP at the HMI firewall. Hardening + auth is a v2 milestone. |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| 1. Single-arch digest from wrong field | Phase 2 — Registry / digest fetching | Playwright e2e: fake registry serves single-arch manifest, `current_digest` in state file equals `docker inspect`'s `RepoDigests[0]` |
| 2. Empty-credentials placeholder breaks anonymous flow | Phase 2 — Registry / digest fetching | Integration test against real GHCR public repo; CI step asserts a 200 manifest fetch |
| 3. Wrong Accept header / index-vs-manifest confusion | Phase 2 — Registry / digest fetching | Test matrix: index + single manifest both produce correct digest; assert `Content-Type` of registry response is inspected |
| 4. `compose up` doesn't pull / silent stale image | Phase 3 — Update/rollback execution | After-recreate assertion: `docker inspect <container>`.`Image` SHA matches target digest; F2 e2e checks this |
| 5. Display blackout during recreate | Phase 3 — Update/rollback execution (logging duration); Phase 5 — UI (pre-action toast); Phase 6 — README warning | Operator-visible duration in slog; toast text reviewed; README has a "before you click Update on flutter/weston" callout |
| 6. Self-update breaks `hmi-update` | Phase 3 — Update/rollback execution (server-side self-protection) | Playwright test: `POST /api/containers/hmi-update/update` returns 409; README documents manual self-upgrade path |
| 7. State file corruption | Phase 4 — State persistence | Fault-injection test: `SIGKILL` mid-write, verify file is either old-valid or new-valid; recovery promotes `.tmp` if valid |
| 8. `go:embed` + Vite MIME / SPA fallback / cache | Phase 5 — UI / embedding | Playwright test: every asset Content-Type, post-upgrade soft-refresh works without hard-refresh |
| 9. distroless nonroot vs docker.sock GID | Phase 1 — Docker client / scaffolding | Boot-time `/healthz` distinguishes EACCES; README install step documents `id -g docker`; CI smoke test does an end-to-end install on Debian 12 |
| 10. Compose file path drift | Phase 1 — Docker client / scaffolding (compose-file reader); Phase 3 — pre-action stat | Stat-before-act in handlers; refusal log line on missing/changed inode |
| 11. Concurrent updates | Phase 3 — Update/rollback execution (per-service mutex); Phase 5 — UI button disable | Test: two near-simultaneous POSTs — first 200, second 409. UI test: button disables on click. |
| 12. Restart-policy hides crashing recreate | Phase 3 — Update/rollback execution (verify-after-recreate) | F2 acceptance test verifies `State.Running == true` and `RestartCount` unchanged after recreate |
| 13. SSRF / path traversal in API | Phase 3 — API surface (validation); Phase 6 — security review | Negative tests: malformed service names rejected with 400; future custom-registry validates URL scheme + private IP |

## Sources

**Reference bugs (WUD 8.2.2 — Pitfalls 1 and 2):**
- [WUD Local Registry Digest Not Displaying — Issue #391](https://github.com/getwud/wud/issues/391)
- [WUD does not detect updates from custom self-hosted registry — Issue #819](https://github.com/getwud/wud/issues/819)
- [Incorrect Docker-Content-Digest when manifest.v2 is requested — distribution/distribution Issue #2395](https://github.com/docker/distribution/issues/2395)

**OCI registry / digest mechanics (Pitfalls 1, 2, 3):**
- [Authenticating with OCI Registries — GHCR Implementation (ToddySM)](https://toddysm.com/2024/02/12/authenticating-with-oci-registries-github-container-registry-ghcr-implementation/)
- [Cannot pull public image from GHCR anonymously — k3s Issue #2401](https://github.com/k3s-io/k3s/issues/2401)
- [Fixing 403 errors from ghcr.io with helm pull (jon.sprig.gs)](https://jon.sprig.gs/blog/post/3141)
- [Failed to pull OCI Image from GitLab Container Registry (Accept header)](https://support.gitlab.com/hc/en-us/articles/20331100094364-Failed-to-pull-OCI-Image-from-GitLab-Container-Registry)
- [Container images, multi-architecture, manifests, ids, digests (Open Sourcerers)](https://www.opensourcerers.org/2020/11/16/container-images-multi-architecture-manifests-ids-digests-whats-behind/)
- [Docker manifest unknown: tags vs digests explained (cr0x.net)](https://cr0x.net/en/docker-manifest-unknown-tags-digests/)

**Docker Compose update / recreate semantics (Pitfalls 4, 10, 11, 12):**
- [docker compose up — official docs](https://docs.docker.com/reference/cli/docker/compose/up/)
- [docker compose pull — official docs](https://docs.docker.com/reference/cli/docker/compose/pull/)
- [up --force-recreate uses volumes from previous launch — compose Issue #4476](https://github.com/docker/compose/issues/4476)
- [Compose v2 up doesn't use new image if pull_policy causes it to pull — Issue #9617](https://github.com/docker/compose/issues/9617)
- [Compose Pull Timeout — Issue #9360](https://github.com/docker/compose/issues/9360)
- [How to Get Docker-Compose to Always Use the Latest Image (Baeldung)](https://www.baeldung.com/ops/docker-compose-latest-image)

**Watchtower precedent (Pitfalls 5, 6, container-that-manages-containers class):**
- [Watchtower — containrrr.dev](https://containrrr.dev/watchtower/)
- [Watchtower ignoring disable labels — Discussion #605](https://github.com/containrrr/watchtower/discussions/605)
- [Watchtower Container Selection / Labels](https://containrrr.dev/watchtower/container-selection/)
- [Watchtower linked containers / rolling restarts](https://watchtower.nickfedor.com/v1.13.1/advanced-features/linked-containers/)
- [Watchtower self-update / ephemeral mode](https://watchtower.nickfedor.com/dev/getting-started/updating-watchtower/)

**Distroless + docker.sock (Pitfall 9):**
- [GoogleContainerTools/distroless — base README](https://github.com/GoogleContainerTools/distroless/blob/main/base/README.md)
- [Building Distroless Go Containers (Alex Rhea)](https://www.arhea.net/posts/2023-09-12-building-distroless-go-containers/)
- [Troubleshooting TLS handshake failures with Docker distroless images (Luca Baggi)](https://lucabaggi.com/posts/ssl-docker/)
- [distroless x509 root cert issue — kubebuilder #1928](https://github.com/kubernetes-sigs/kubebuilder/issues/1928)
- [Docker socket mount permission — forums.docker.com](https://forums.docker.com/t/docker-sock-mount-permission/118720)
- [Implementing Docker-from-Docker for Non-Root Users (Ken Muse)](https://www.kenmuse.com/blog/implementing-docker-from-docker-for-nonroot-users/)

**Single-binary Go + embedded SPA (Pitfall 8):**
- [Vite Static Asset Handling](https://vite.dev/guide/assets)
- [Failed to load module script — Vite issue #8073](https://github.com/vitejs/vite/issues/8073)
- [Resolving Vite MIME type errors (Dan Edwards)](https://danedwardsdeveloper.com/articles/resolving-vite-react-mime-type-errors)
- [Serving Single-Page Apps From Go (hackandsla.sh)](https://hackandsla.sh/posts/2021-11-06-serve-spa-from-go/)
- [Go Embed Vite (Feng's Notes)](https://ofeng.org/posts/go-embed-vite/)
- [Fileserver example for SPA — chi Issue #611](https://github.com/go-chi/chi/issues/611)

**Atomic writes / state file corruption (Pitfall 7):**
- [.claude.json corrupted by concurrent writes — claude-code Issue #29051](https://github.com/anthropics/claude-code/issues/29051)
- [.claude.json becomes corrupted (Unexpected EOF) — claude-code Issue #28809](https://github.com/anthropics/claude-code/issues/28809)
- [Race condition: .claude.json corruption on Windows — claude-code Issue #29036](https://github.com/anthropics/claude-code/issues/29036)
- [OverlayFS storage driver — Docker docs (rename / EXDEV limitations)](https://docs.docker.com/engine/storage/drivers/overlayfs-driver/)

**Playwright + docker-compose e2e (general flakiness):**
- [Real Docker Containers in Playwright Tests — Zero Boilerplate (dev.to)](https://dev.to/vitalicset/real-docker-containers-in-playwright-tests-zero-boilerplate-4ml7)
- [E2E testing with Playwright and Docker (Beppe Catanese)](https://medium.com/geekculture/e2e-testing-with-playwright-and-docker-91dd7eb11793)
- [End-to-End Testing with Playwright and Docker (BrowserStack)](https://www.browserstack.com/guide/playwright-docker)

**Security / docker.sock exposure (Pitfall 13):**
- [Docker Engine security — official docs](https://docs.docker.com/engine/security/)
- [Protect the Docker daemon socket — official docs](https://docs.docker.com/engine/security/protect-access/)
- [Docker Security Cheat Sheet — OWASP](https://cheatsheetseries.owasp.org/cheatsheets/Docker_Security_Cheat_Sheet.html)
- [Docker Socket Security: A Critical Vulnerability Guide](https://medium.com/@instatunnel/docker-socket-security-a-critical-vulnerability-guide-76f4137a68c5)

---
*Pitfalls research for: container update/rollback manager on Debian HMI boxes — `hmi-update`*
*Researched: 2026-05-13*
