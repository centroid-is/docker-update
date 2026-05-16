# docker-update

A single Go container that detects when `:latest` Docker images have been re-pushed for the containers running on Centroid's elevator HMI boxes, and gives Centroid field engineers per-container **Update** and **Rollback** buttons via a small Svelte web UI on the HMI LAN. Replaces a fragile patched WUD 8.2.2 setup and a heavier Komodo-based alternative with a tool that has rollback built in, ships as one image, and persists everything in a single JSON file alongside the compose stack.

Web UI: `http://<hmi>:8080/`

## Upgrading from hmi-update

As of vX.Y.Z, the binary, compose service, and env-var prefix unify on
`docker-update`. The watched-container labels stay on the
`hmi-update.*` namespace for backwards compatibility.

### Compose service rename

```yaml
# OLD
services:
  hmi-update:
    image: ghcr.io/centroid-is/docker-update:latest
    container_name: hmi-update
    environment:
      HMI_UPDATE_STATE_PATH: /state/hmi_update_state.json
      HMI_UPDATE_COMPOSE_PATH: /host/docker-compose.yml
      # ... etc
    volumes:
      - /opt/centroid/hmi_update_state.json:/state/hmi_update_state.json

# NEW
services:
  docker-update:
    image: ghcr.io/centroid-is/docker-update:latest
    container_name: docker-update
    environment:
      DOCKER_UPDATE_STATE_PATH: /state/docker_update_state.json
      DOCKER_UPDATE_COMPOSE_PATH: /host/docker-compose.yml
      # ... etc
    volumes:
      - /opt/centroid/docker_update_state.json:/state/docker_update_state.json
```

### Migrate the state file

    sudo mv /opt/centroid/hmi_update_state.json /opt/centroid/docker_update_state.json

### Env-var renames

| Old                              | New                                |
|----------------------------------|------------------------------------|
| HMI_UPDATE_STATE_PATH            | DOCKER_UPDATE_STATE_PATH           |
| HMI_UPDATE_COMPOSE_PATH          | DOCKER_UPDATE_COMPOSE_PATH         |
| HMI_UPDATE_CRON                  | DOCKER_UPDATE_CRON                 |
| HMI_UPDATE_LOG_LEVEL             | DOCKER_UPDATE_LOG_LEVEL            |
| HMI_UPDATE_REGISTRY_TIMEOUT_S    | DOCKER_UPDATE_REGISTRY_TIMEOUT_S   |
| HMI_UPDATE_POLL_CONCURRENCY      | DOCKER_UPDATE_POLL_CONCURRENCY     |
| HMI_UPDATE_REGISTRY_INSECURE     | DOCKER_UPDATE_REGISTRY_INSECURE    |
| HMI_UPDATE_DOCKER_HOST           | DOCKER_UPDATE_DOCKER_HOST          |
| HMI_UPDATE_SELF_SERVICE          | DOCKER_UPDATE_SELF_SERVICE         |
| HMI_UPDATE_VERIFY_WINDOW_S       | DOCKER_UPDATE_VERIFY_WINDOW_S      |
| HMI_UPDATE_HEALTHCHECK_WINDOW_S  | DOCKER_UPDATE_HEALTHCHECK_WINDOW_S |

### Labels — DO NOT rename

The watched-container labels are intentionally kept on the
`hmi-update.*` prefix for backwards compatibility across the HMI
fleet:

- `hmi-update.watch=true`
- `hmi-update.tag-pattern=<regex>`
- `hmi-update.allow-update=false`
- `hmi-update.allow-rollback=false`
- `hmi-update.wait-for-healthy=true`

These labels are a stable public contract. Do not edit them when
upgrading.

### Phase 9 upgrade — remove docker CLI bind-mounts

Phase 9 removes the docker CLI bind-mount requirement because
`docker-update` now drives container recreate via the daemon socket
(socket-only recreate via `internal/recreate.Service`) instead of
shelling out to `docker compose`. The old bind-mounts are now unused
and SHOULD be removed from your installed `docker-compose.yml` — they
serve no purpose post-Phase-9 and clutter the service block.

Edit your installed `/opt/centroid/docker-compose.yml` (or wherever you
keep it) and delete the two `:ro` bind-mounts from the docker-update
service block:

```yaml
    volumes:
      # DELETE these two lines:
      - /usr/bin/docker:/usr/bin/docker:ro
      - /usr/libexec/docker/cli-plugins/docker-compose:/usr/libexec/docker/cli-plugins/docker-compose:ro
```

After saving, run:

```sh
docker compose up -d --force-recreate docker-update
```

once to apply the change. From this point onward, all future updates
flow through the in-app **Update** button (including `docker-update`'s
own self-update via the new `POST /api/self-update` endpoint, which
spawns a one-shot helper container that drives the recreate). This is
the LAST host-shell `docker compose` command you should need on this
HMI.

See `docker-compose.example.yml` in the repo for the canonical
post-Phase-9 service block shape.

## Quick start

Drop the `docker-update` service block into your existing `docker-compose.yml` and run:

```sh
docker compose up -d docker-update
```

The full install runbook (with the `id -g docker` step required for the
distroless nonroot user to reach the docker socket) is the next section.

## Installation on an HMI

Tested on Debian 12 with Docker Engine v29+ and the docker-compose-plugin.
The published image lives at `ghcr.io/centroid-is/docker-update:latest`.

### 1. Get the docker group GID

The container runs as the distroless `nonroot` UID (65532) and needs the host
docker group GID as a supplementary group to read `/var/run/docker.sock`.
Run `id -g docker` on the HMI host and note the integer it prints:

```sh
id -g docker        # prints e.g. 998
```

### 2. Place the compose snippet and state file

```sh
sudo mkdir -p /opt/centroid
sudo cp docker-compose.example.yml /opt/centroid/docker-compose.yml
sudo touch /opt/centroid/docker_update_state.json
sudo chown 65532:65532 /opt/centroid/docker_update_state.json
```

Then edit `/opt/centroid/docker-compose.yml` and replace the literal
placeholder `<docker-gid>` in the `user:` line with the integer from step 1
(NOT a `${HOST_DOCKER_GID}` shell variable — compose does not re-resolve
env vars from the operator's shell on systemd restart, so a literal integer
is the only safe form):

```yaml
    user: "65532:998"   # replace 998 with the value of `id -g docker` from step 1
```

The state-file `chown 65532:65532` is the same Pitfall 9 remediation
documented in PROJECT.md — see
[PROJECT.md §Installation prerequisites](.planning/PROJECT.md#installation-prerequisites)
for the underlying rationale (do NOT duplicate the chown step elsewhere; this
runbook is the single operator-facing reference).

### 3. Start

```sh
cd /opt/centroid
docker compose up -d docker-update
```

### 4. Verify

```sh
curl -s http://localhost:8080/healthz   # → {"status":"ok"}, HTTP 200
xdg-open http://localhost:8080          # table view in the browser
```

The table is empty until watched containers boot (`hmi-update.watch=true`
label on the services you want managed). See
[PROJECT.md §Container labels reference](.planning/PROJECT.md#container-labels-reference)
for the five labels you can set on watched containers.

### 5. Manual self-upgrade

`docker-update` cannot recreate itself via its own API (it is the process being
recreated — it would commit suicide mid-recreate, see PITFALLS.md Pitfall 6
and ACT-09). The documented host-shell upgrade procedure lives in
[PROJECT.md §Manual self-upgrade procedure](.planning/PROJECT.md#manual-self-upgrade-procedure).

## Configuration

`docker-update` is configured via environment variables in the compose service
block. The minimum production set is the three in `docker-compose.example.yml`
(`DOCKER_UPDATE_CRON`, `DOCKER_UPDATE_COMPOSE_PATH`, `DOCKER_UPDATE_STATE_PATH`). The
full list (registry timeout, log level, verify window, etc.) lives in
[PROJECT.md §Configuration knobs (env vars)](.planning/PROJECT.md#configuration-knobs-env-vars).

Container labels controlling per-service behaviour (watch / tag-pattern /
allow-update / allow-rollback / wait-for-healthy) are documented in
[PROJECT.md §Container labels reference](.planning/PROJECT.md#container-labels-reference).
The label namespace remains `hmi-update.*` for backwards compatibility — see
["Upgrading from hmi-update"](#upgrading-from-hmi-update) above for the
rationale and the "Labels — DO NOT rename" callout.

## Before you click Update on flutter or weston

The `flutter` and `weston` containers draw the operator's elevator display. Recreating either of them blanks the screen for 5–30 seconds while the new container starts and reaches first paint. The exact duration depends on whether the new image's layers are already extracted locally (faster) or need a cold pull (slower), and on the application's own cold-start time (a Flutter app typically takes 2–10s to draw its first frame on HMI hardware).

**Recreating `weston` is worse than recreating `flutter`** — `weston` is the Wayland compositor, and tearing it down disconnects every Wayland client (`flutter` and any others), so all of them restart together.

The web UI will show a **"display may flicker" confirmation toast** before the recreate fires when the targeted service name contains `flutter` or `weston` (case-insensitive substring match). The operator can cancel from the toast — nothing happens until the toast is confirmed.

If the operator confirms and the blackout is unwanted, **Rollback returns the container to the previous digest in under 15 seconds, entirely from the local image cache** — it works even with no network connection to the registry. Rollback is the safety net for "I clicked Update and got a blackout I didn't want."

If the new image's local cache was accidentally removed (a `docker image prune` mishap), **Force Pull** re-pulls the `:latest` image without recreating the container — it's the recovery path if Update fails to find the local image.

Full failure-mode analysis: `.planning/research/PITFALLS.md` Pitfall 5.

## Container labels

> **Backwards-compat note:** the label keys below are intentionally namespaced
> `hmi-update.*` even though the binary, image, and service are now
> `docker-update`. Operators across the Centroid HMI fleet already have these
> labels on dozens of compose service blocks; renaming would force a
> coordinated edit on every HMI's docker-compose.yml. Treat `hmi-update.*`
> as a stable public contract.

| Label | Purpose | Default if absent |
|-------|---------|-------------------|
| `hmi-update.watch=true` | Mark a container as watched | Not watched |
| `hmi-update.tag-pattern=<regex>` | Constrain upstream tag candidacy (e.g. `^latest-pg17$` on timescaledb) | Any tag matches |
| `hmi-update.allow-update=false` | Server refuses Update for this container (SAFE-01) | Update allowed |
| `hmi-update.allow-rollback=false` | Server refuses Rollback for this container | Rollback allowed |
| `hmi-update.wait-for-healthy=true` | Extend verify-after-recreate to wait for `State.Health.Status == "healthy"` (60s window) | 15s consecutive-Running window |

See [PROJECT.md §Container labels reference](.planning/PROJECT.md#container-labels-reference) for the canonical table.

## Development

```sh
make           # build UI + Go binary into ./bin/docker-update
make test      # Go unit tests with -race
make e2e       # Playwright e2e against the test compose stack
make image-prod   # production-hardened container image (Phase 7 packaging)
```

The full developer pointers (architecture notes, pitfalls, research) live in
`.planning/`.

## Project pointers

- **Full requirements + decisions:** `.planning/PROJECT.md`
- **Roadmap + phase plans:** `.planning/ROADMAP.md`
- **HTTP API:** `API.md` (Phase 4 — `/api/state`, `/api/containers/{service}/update`, `/rollback`, `/force-pull`)
- **Research (pitfalls, registry mechanics, distroless GID, atomic writes):** `.planning/research/`

## License

MIT — see `LICENSE` (Phase 8 publish flow lands the file alongside the GHCR
release).
