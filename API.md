# hmi-update HTTP API

Operator-facing reference for the `hmi-update` HTTP surface. The
process listens on `:8080` inside its container; on a default HMI install
the LAN routes traffic from the field engineer's laptop to `:8080` on
the box.

**Security posture:** LAN-only, unauthenticated (matches WUD 8.2.2's
posture per CLAUDE.md "Constraints — Security"). Do NOT expose this
port to the public internet.

See also:

- `CLAUDE.md` — project constraints (single binary, file-based state,
  compose deployment, TDD-first)
- `PROJECT.md` — operator runbook (Manual self-upgrade procedure,
  Installation prerequisites, Container labels reference, Configuration
  knobs)

## Endpoints at a glance

| Method | Path                                              | Purpose                                                                  |
| ------ | ------------------------------------------------- | ------------------------------------------------------------------------ |
| GET    | `/healthz`                                         | Liveness probe (state store + docker socket + docker daemon Ping)        |
| GET    | `/api/state`                                       | In-memory state snapshot (no-I/O — OBS-03; 5-second UI poll target)      |
| POST   | `/api/containers/{service}/update`                 | Pull `:latest`, recreate the service, verify-after-recreate              |
| POST   | `/api/containers/{service}/rollback`               | Re-tag to `previous_digest`, recreate the service, verify-after-recreate |
| POST   | `/api/containers/{service}/force-pull[?recreate=true]` | Force docker pull (optionally also recreate via the Update flow)     |

The three Phase 4 action endpoints (POST `…/update`, `…/rollback`,
`…/force-pull`) are the focus of this document.

## Service-name allowlist (ACT-10)

The `{service}` path-parameter is validated against the regex
`^[a-zA-Z0-9._-]+$` before anything else runs. Anything outside the
allowlist returns:

```http
HTTP/1.1 400 Bad Request
Content-Type: application/json; charset=utf-8

{"error":"invalid_service_name","detail":"service name must match ^[a-zA-Z0-9._-]+$"}
```

Argv discipline carries through: the validated service name is the 8th
argv element of the underlying `docker compose ... -- <service>` call;
it is NEVER interpolated into a shell string (Pitfall 13 defense).

## Middleware chain (load-bearing order)

All three action endpoints run the same chain in the SAME order:

```
ValidateServiceName → CheckSelfProtection → LookupContainer → CheckSafetyLabel → orchestrator.<Action>
```

`CheckSelfProtection` runs BEFORE `LookupContainer` because `hmi-update`
itself is NOT in the watched-containers state cache by default (the
self container ships with `hmi-update.watch=false`). If `LookupContainer`
ran first, a probe of `POST /api/containers/hmi-update/update` would
return 404 (misleading) instead of 409 self_protection (operator-
actionable) — ACT-09.

The `CheckSafetyLabel` step is the SAFE-03 carve-out point for
`force-pull` (see below).

---

## POST /api/containers/{service}/update

Pull `:latest`, cross-check the pulled digest against the registry digest
(Pitfall 1), recreate the service via `docker compose -f <path> up -d
--force-recreate <service>`, then run the 15-second verify-after-recreate
poll loop (ACT-01, ACT-02, ACT-06, ACT-11).

**Request:**

```http
POST /api/containers/svc-a/update HTTP/1.1
Host: hmi.local:8080
```

Empty body. The handler ignores any request body.

**Success — 200 OK:**

```json
{
  "current_digest": "sha256:0bf3b7…",
  "previous_digest": "sha256:5a9c4e…"
}
```

`current_digest` is the digest now running (post-recreate); `previous_digest`
is the digest the container was running before the update (will be the
target of a subsequent `/rollback` call). Both fields are always present
on success — ACT-11.

**Idempotency — 200 OK with no_op (ACT-06):**

When the cached state already shows `current_digest == available_digest`
(the registry hasn't moved since the last poll), the handler short-
circuits with:

```json
{
  "current_digest": "sha256:0bf3b7…",
  "previous_digest": "sha256:5a9c4e…",
  "no_op": true
}
```

**Error codes:**

| Status | error code                | When                                                                                                                     |
| ------ | -------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| 400    | `invalid_service_name`     | `{service}` does not match `^[a-zA-Z0-9._-]+$`                                                                            |
| 404    | `container_not_found`      | `{service}` is not in the cached state map                                                                                |
| 409    | `self_protection`          | `{service}` equals the value of `HMI_UPDATE_SELF_SERVICE` (default `hmi-update`); see Manual self-upgrade procedure below |
| 409    | `action_disabled_by_label` | Container has label `hmi-update.allow-update=false`                                                                       |
| 409    | `service_busy`             | A previous action on the same service is still in flight (per-service `sync.Mutex.TryLock` returned false — ACT-08)       |
| 412    | `compose_file_moved`       | The compose file's inode/mtime/size drifted from boot snapshot; restart `hmi-update` to pick up the new file (Pitfall 10) |
| 500    | `pull_failed`              | `docker pull` failed OR pulled digest does not match registry digest (Pitfall 1). See `action.pull_failed` slog event     |
| 500    | `compose_failed`           | `docker compose ... up -d --force-recreate` exited non-zero. See `action.compose_failed` slog event for stderr            |
| 500    | `verify_failed`            | Verify-after-recreate detected `!Running` OR `RestartCount` incremented OR `healthcheck=unhealthy` — see structured body  |
| 503    | `orchestrator_not_wired`   | Defensive nil-guard; production main.go `log.Fatalf`s on `actions.NewOrchestrator` errors so this branch is test-only    |
| 503    | `verify_canceled`          | Verify-after-recreate was canceled by `ctx.Done` (SIGTERM or request abandon)                                              |

**`verify_failed` body shape (CONTEXT Area 3 — LOCKED):**

```json
{
  "error": "verify_failed",
  "reason": "container restarted 3 times in 15s",
  "exit_code": null,
  "restart_count": 3,
  "running": false,
  "container_id": "abc123def456"
}
```

The `reason` field is one of:

- `container restarted N times in 15s` — RestartCount delta observed
- `container not running` — `State.Running == false` mid-window
- `healthcheck unhealthy` — opt-in `hmi-update.wait-for-healthy=true`
  and `State.Health.Status == "unhealthy"`
- `did not reach 15 consecutive healthy ticks within 15s` — deadline expired
- `ContainerInspect failed: <reason>` — daemon-side failure during poll
- `container state nil` — defensive nil-check on moby's pointer-nested State

`restart_count` / `running` / `container_id` reflect the snapshot at the
moment of failure. `exit_code` is reserved (`null` in v1; populated by a
future enhancement if the daemon surfaces it).

## POST /api/containers/{service}/rollback

Local re-tag `image@previous_digest → image:tag` (offline-capable — no
registry call; ACT-04 differentiator vs. WUD), recreate the service,
verify-after-recreate (ACT-03, ACT-07, ACT-11).

**Request:** same shape as `/update` (empty body).

**Success — 200 OK:**

Same envelope as `/update`. `current_digest` and `previous_digest` are
swapped:

```json
{
  "current_digest": "sha256:5a9c4e…",
  "previous_digest": "sha256:0bf3b7…"
}
```

A second rollback flips them back — the slot is a single-slot toggle
(PROJECT.md F3; no history).

**Idempotency — 200 OK with no_op (ACT-07):**

When `current_digest == previous_digest` (the rollback target equals
the running digest):

```json
{
  "current_digest": "sha256:0bf3b7…",
  "previous_digest": "sha256:0bf3b7…",
  "no_op": true
}
```

**Error codes:** same matrix as `/update`, plus one rollback-specific
error:

| Status | error code             | When                                                                                |
| ------ | ----------------------- | ----------------------------------------------------------------------------------- |
| 400    | `no_previous_digest`    | Container has never been updated; no `previous_digest` recorded                     |
| 409    | `action_disabled_by_label` | Container has label `hmi-update.allow-rollback=false` (not `allow-update`)        |

Otherwise identical (400 invalid_service_name; 404 container_not_found;
409 self_protection / service_busy; 412 compose_file_moved; 500
pull_failed (ImageTag); 500 compose_failed; 500 verify_failed; 503
verify_canceled; 503 orchestrator_not_wired).

The `pull_failed` 500 here surfaces `docker.Client.ImageTag` failures —
the local re-tag step, not a registry pull. ACT-04: rollback works
with the registry network detached.

## POST /api/containers/{service}/force-pull[?recreate=true]

Refresh the local image cache for `image:tag` (ACT-05). Two modes:

**Default — `?recreate=true` absent or `recreate=false`:**

Calls `docker.Client.ImagePull(ctx, image:tag)` and updates the cached
`available_digest`. The running container is unaffected — no compose
call, no verify loop. Useful for recovering from `docker image prune`
that removed a base image (F8).

- **SAFE-03 carve-out:** force-pull-no-recreate is EXEMPT from safety
  labels. A container labeled `hmi-update.allow-update=false` can still
  be force-pulled in this mode (the running container is read-only
  with respect to a local image cache refresh).

**Recreate — `?recreate=true`:**

Delegates to the full Update flow (pull → digest cross-check → compose
recreate → verify-after-recreate). The handler explicitly opts INTO the
Update safety-label check before delegating (RESEARCH.md Open Question
#5 — the recreate IS a recreate operation; SAFE-01 applies).

**Success — 200 OK:**

```json
{
  "current_digest": "sha256:0bf3b7…",
  "previous_digest": "sha256:5a9c4e…"
}
```

Note: in default (no-recreate) mode `current_digest` is unchanged from
the running container's digest; the meaningful update is to the cached
`available_digest` field in `GET /api/state`. In `?recreate=true` mode
the digests update as for `/update`.

**Error codes:** same as `/update`. The `action_disabled_by_label` 409
fires ONLY when `?recreate=true` is set; without it, force-pull bypasses
the safety label per SAFE-03.

## GET /api/state

Returns the in-memory state snapshot as JSON. Read-only,
no-I/O — does not touch the docker socket, does not stat the compose
file, does not perform any registry calls (OBS-03 invariant; pinned by
`TestGetState_NoIO` which injects a panicking docker.Client and proves
no docker method is invoked across 100 GETs).

This is the **5-second UI poll target**. The Phase 5 web UI polls this
endpoint to refresh the per-container button states.

```http
GET /api/state HTTP/1.1
```

Response (truncated):

```json
{
  "version": 1,
  "containers": {
    "svc-a": {
      "service": "svc-a",
      "image": "centroid-is/stub",
      "tag": "latest",
      "current_digest": "sha256:5a9c4e…",
      "available_digest": "sha256:0bf3b7…",
      "previous_digest": "",
      "update_available": true,
      "action_in_flight": "",
      "action_error": "",
      "labels": { "hmi-update.watch": "true" },
      "container_id": "abc123def456",
      "running": true,
      "last_polled_at": "2026-05-15T08:30:00Z"
    }
  }
}
```

## Slog event schema (OBS-01)

Every action emits structured JSON log lines via `log/slog` (handler
installed at boot step 1 in `cmd/hmi-update/main.go`). Operators tail
`journalctl -u hmi-update -f` or `docker logs -f hmi-update`.

| Event name              | Level | Required fields                                                                       | When                                              |
| ----------------------- | ----- | ------------------------------------------------------------------------------------- | ------------------------------------------------- |
| `action.start`          | Info  | `service`, `action`                                                                   | Action handler enters the body                    |
| `action.phase`          | Info  | `service`, `action`, `phase` (`pulled` / `retagged` / `verified`), `new_digest` or `target_digest` | Each inter-step waypoint               |
| `action.complete`       | Info  | `service`, `action`, `before`, `after`, `exit_code`, `duration_ms` (optional `no_op`) | Action exits successfully (or idempotent no-op)   |
| `action.pull_failed`    | Error | `service`, `err` (optional `stage`)                                                   | docker pull or ImageTag failure                   |
| `action.compose_failed` | Error | `service`, `err` (stderr lives in `compose.run` event below)                          | `docker compose ... up -d --force-recreate` exited non-zero |
| `action.verify_failed`  | Error | `service`, `restart_count`, `running`, `err`                                          | Verify loop fail-fast OR deadline expiry          |
| `compose.run`           | Info / Error | `service`, `exit_code`, `duration_ms` (Error level adds `err`, `stderr_snippet`) | Every `docker compose ... up -d --force-recreate` invocation (Plan 04-02) |

Bearer tokens, Authorization headers, and base64-encoded credentials
are stripped by the slog `ReplaceAttr` regex (boot step 1's
`newRedactingHandler` — OBS-04 output-side defense; partners with
`internal/registry`'s redacting transport for the request-side defense).

## Manual self-upgrade procedure

`hmi-update` refuses to recreate itself via the API (`POST
/api/containers/hmi-update/{update,rollback,force-pull?recreate=true}`
returns 409 self_protection — ACT-09). To upgrade hmi-update itself:

1. On the HMI host: `docker pull ghcr.io/centroid-is/hmi-update:vX.Y.Z`
2. `docker compose -f /opt/centroid/docker-compose.yml up -d --force-recreate hmi-update`
3. Wait ~10 seconds; verify `curl http://localhost:8080/healthz` returns 200.

The HMI's web UI will be unreachable for ~5–15 seconds during step 2.
The state file (`hmi_update_state.json`) persists across the recreate.

The Phase 4 STATE-04 fault-injection test (`make test-sigkill`) verifies
the state file remains parseable across SIGKILL mid-write — operators
do NOT need to manually back up the state file before self-upgrade.

## Configuration knobs

All env vars below are read once at boot. Restart `hmi-update` after
changing any of them.

| Env var                            | Default              | Purpose                                                                                                                       |
| ---------------------------------- | -------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| `HMI_UPDATE_LOG_LEVEL`             | `info`               | `debug` / `info` / `warn` / `error`                                                                                            |
| `HMI_UPDATE_STATE_PATH`            | `./hmi_update_state.json` | Path to the JSON state file (atomic-writes via `renameio.WriteFile` + parent-dir fsync)                                  |
| `HMI_UPDATE_COMPOSE_PATH`          | (required)           | Path to the docker-compose.yml the runner targets via `-f`. Must point at the bind-mounted copy inside the container.        |
| `HMI_UPDATE_CRON`                  | `0 * * * *`          | 5-field cron expression for the polling sweep (`@every 5s` in e2e). Hourly default per CLAUDE.md "Constraints".               |
| `HMI_UPDATE_REGISTRY_TIMEOUT_S`    | `30`                 | Per-call timeout for `registry.Resolver.Digest` (crane.Digest under the hood)                                                  |
| `HMI_UPDATE_POLL_CONCURRENCY`      | `5`                  | Semaphore size for the cron sweep (parallel digest fetches across watched containers)                                          |
| `HMI_UPDATE_SELF_SERVICE`          | `hmi-update`         | Compose service name THIS process runs as; refuses self-action with 409 self_protection (ACT-09)                              |
| `HMI_UPDATE_VERIFY_WINDOW_S`       | `15`                 | Verify-after-recreate poll window (seconds). 15 consecutive healthy 1-second ticks required for success                       |
| `HMI_UPDATE_HEALTHCHECK_WINDOW_S`  | `60`                 | Extended verify window for containers opting in via `hmi-update.wait-for-healthy=true` label. Soft-success if no health status reported within this window |
| `HMI_UPDATE_DOCKER_HOST`           | `/var/run/docker.sock` | Docker socket path; used by `/healthz` socket-stat step. Override for tests; production HMIs use the default bind-mount.    |

## Container labels reference (excerpted)

The following labels on watched containers affect Phase 4 action
behavior. See PROJECT.md "Container labels reference" for the full
roster.

| Label                          | Value     | Effect                                                                                              |
| ------------------------------ | --------- | --------------------------------------------------------------------------------------------------- |
| `hmi-update.watch`             | `true`    | Container is included in the cached state map; action endpoints can target it                       |
| `hmi-update.watch`             | `false`   | Container is excluded (used on `hmi-update` itself — SAFE-03 + ACT-09)                              |
| `hmi-update.allow-update`      | `false`   | `POST /api/containers/<svc>/update` returns 409 action_disabled_by_label (SAFE-01)                  |
| `hmi-update.allow-rollback`    | `false`   | `POST /api/containers/<svc>/rollback` returns 409 action_disabled_by_label (SAFE-02)                |
| `hmi-update.wait-for-healthy`  | `true`    | Verify-after-recreate uses the 60s healthcheck window instead of the default 15s consecutive-ticks  |
| `hmi-update.tag-pattern`       | `^v\d+`   | Cron poller treats matching tags as "available digests"; default behavior tracks `:latest` literally |

## Threat model notes

The verify-failed structured body (the SOLE Pattern K exception in
this surface) is tracked as T-04-04-03. Inputs are pre-trimmed by the
orchestrator — `Reason` is constructed via `fmt.Sprintf` over integer
counters + duration; no operator paths are in the trim domain. The
`TestHandleActions_PathLeakGuard` test (every error branch) AND
`TestHandleUpdate_VerifyFailed_500_StructuredBody` (the structured-body
branch) pin: the response body MUST NOT echo a temp-dir prefix, an
absolute path, or any operator-supplied filesystem string.

All other error bodies are package-private verbatim string constants
(Pattern K). Any future branch that needs a dynamic field must be added
to the threat model first.
