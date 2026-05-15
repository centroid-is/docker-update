# hmi-update

A single Go container that detects when `:latest` Docker images have been re-pushed for the containers running on Centroid's elevator HMI boxes, and gives Centroid field engineers per-container **Update** and **Rollback** buttons via a small Svelte web UI on the HMI LAN. Replaces a fragile patched WUD 8.2.2 setup and a heavier Komodo-based alternative with a tool that has rollback built in, ships as one image, and persists everything in a single JSON file alongside the compose stack.

Web UI: `http://<hmi>:8080/`

## Quick start

Drop the `hmi-update` service block into your existing `docker-compose.yml` and run:

```sh
docker compose up -d hmi-update
```

The full install runbook — including the `id -g docker` step required for the distroless nonroot user to reach the docker socket — is documented in the Phase 7 deployment runbook. See `.planning/PROJECT.md` "Installation prerequisites".

## Before you click Update on flutter or weston

The `flutter` and `weston` containers draw the operator's elevator display. Recreating either of them blanks the screen for 5-30 seconds while the new container starts and reaches first paint. The exact duration depends on whether the new image's layers are already extracted locally (faster) or need a cold pull (slower), and on the application's own cold-start time (a Flutter app typically takes 2-10s to draw its first frame on HMI hardware).

**Recreating `weston` is worse than recreating `flutter`** — `weston` is the Wayland compositor, and tearing it down disconnects every Wayland client (`flutter` and any others), so all of them restart together.

The web UI will show a **"display may flicker" confirmation toast** before the recreate fires when the targeted service name contains `flutter` or `weston` (case-insensitive substring match). The operator can cancel from the toast — nothing happens until the toast is confirmed.

If the operator confirms and the blackout is unwanted, **Rollback returns the container to the previous digest in under 15 seconds, entirely from the local image cache** — it works even with no network connection to the registry. Rollback is the safety net for "I clicked Update and got a blackout I didn't want."

If the new image's local cache was accidentally removed (a `docker image prune` mishap), **Force Pull** re-pulls the `:latest` image without recreating the container — it's the recovery path if Update fails to find the local image.

Full failure-mode analysis: `.planning/research/PITFALLS.md` Pitfall 5.

## Container labels

| Label | Purpose | Default if absent |
|-------|---------|-------------------|
| `hmi-update.watch=true` | Mark a container as watched | Not watched |
| `hmi-update.tag-pattern=<regex>` | Constrain upstream tag candidacy (e.g. `^latest-pg17$` on timescaledb) | Any tag matches |
| `hmi-update.allow-update=false` | Server refuses Update for this container (SAFE-01) | Update allowed |
| `hmi-update.allow-rollback=false` | Server refuses Rollback for this container | Rollback allowed |
| `hmi-update.wait-for-healthy=true` | Extend verify-after-recreate to wait for `State.Health.Status == "healthy"` (60s window) | 15s consecutive-Running window |

See `.planning/PROJECT.md` "Container labels reference" for the canonical table.

## Project pointers

- **Full requirements + decisions:** `.planning/PROJECT.md`
- **Roadmap + phase plans:** `.planning/ROADMAP.md`
- **HTTP API:** `API.md` (Phase 4 — `/api/state`, `/api/containers/{service}/update`, `/rollback`, `/force-pull`)
- **Research (pitfalls, registry mechanics, distroless GID, atomic writes):** `.planning/research/`
