# Phase 9: Architectural Hardening (post-v0.1 bug-cluster) — Research

**Researched:** 2026-05-16
**Domain:** Daemon-side container orchestration via `github.com/moby/moby/client`; GitHub Actions parallel job topology; Watchtower-style self-update
**Confidence:** HIGH on (a), (c), (d); MEDIUM-HIGH on (b) interim fallback (only relevant if (a) is split, which Locked Dependencies say it won't be)

## Summary

Phase 9 surgically removes the `docker compose ... up -d --force-recreate <svc>` shell-out (`internal/compose/runner.go`) and replaces it with an in-process `ContainerInspect → ContainerRemove → ContainerCreate (+ extra NetworkConnect) → ContainerStart` sequence on the existing `internal/docker.Client` facade. Once that lands, three things follow mechanically: the `./relative-path` resolution split disappears (no compose CLI to mis-resolve paths), the runtime base image returns to `gcr.io/distroless/static-debian12:nonroot` (~20 MB shrink, no dynamic linker needed because no host CLI is exec'd), and the bind-mounts for `/usr/bin/docker` + `/usr/libexec/docker/cli-plugins` are deleted from `docker-compose.example.yml`. Self-update follows the same primitive: a Watchtower-style `--self-update-orchestrator` helper, spawned by docker-update itself from its own image, drives the same Remove+Create+Start sequence against docker-update's container and exits. CI parallelization is independent and lands first — a `tests` job and an `image+downstream` job share neither artifacts nor cache nor wall-time order; a `publish` step that already runs in a separate workflow needs no change.

**Primary recommendation:** Implement Phase 9 in three waves. Wave 1 ships (c) CI 2-job split (zero Go code touched). Wave 2 ships (a) socket-only recreate via three new `docker.Client` methods (`ContainerCreate`, `ContainerRemove`, `ContainerStart`) plus a field-translation helper in `internal/actions/recreate.go`; deletes `internal/compose/runner.go`'s body but keeps `compose.Reader` (drift detection still wanted); reverts the Dockerfile base image and trims `docker-compose.example.yml`. (b) is satisfied automatically. Wave 3 ships (d) self-update via a `--self-update-orchestrator` flag mode of the same docker-update binary plus a new `POST /api/self-update` route that bypasses `CheckSelfProtection` by routing to the orchestrator-spawn handler. Tests for all four locked bug classes (compose_file_moved, COMPOSE_PROJECT_NAME, relative-path, 409 self_protection) MUST be written RED-first before any production code change in their respective wave.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Container recreate (Update/Rollback path) | Daemon API (in-process via `moby/moby/client`) | — | Wave 2 removes the subprocess; the daemon socket is the only seam |
| Field translation `InspectResponse → Create{Config, HostConfig, NetworkingConfig}` | Backend Go package (`internal/actions/recreate.go` or new `internal/recreate/`) | — | Pure data transform; sits inside the orchestrator's recreate phase |
| Compose-file drift detection (412 ErrComposeFileMoved) | Backend Go package (`internal/compose/reader.go`) | — | Still useful even without the runner — drift means a future re-discovery would target the wrong file. Keeps the operator-visible 412 |
| CI test execution | GitHub Actions runner (`tests` job, ubuntu-24.04) | — | Independent of image; needs `internal/api/dist/.gitkeep` so `//go:embed all:dist` parses without `npm run build` |
| Image build + e2e + idle-RAM + portability | GitHub Actions runner (`image-downstream` job, ubuntu-24.04) | — | Shares the buildx layer cache with itself; needs no artifact handoff to `tests` |
| Self-update orchestrator helper | Sidecar one-shot container (same docker-update image, `--self-update-orchestrator` flag) | — | One-binary constraint C1 preserved: ONE image, two entry-point modes |
| Self-update verification | Daemon API (restart-count delta + `/healthz` HTTP probe by the helper before it exits) | — | The helper, not the running docker-update, signals success |

## Standard Stack

### Core (existing — no changes)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/moby/moby/client` | v0.4.1 | Daemon API client | Already wrapped by `internal/docker/client.go`; CLAUDE.md "Technology Stack" pins this module. [VERIFIED: existing go.mod and `internal/docker/_sdk_shape.txt`] |
| `github.com/moby/moby/api/types/container` | matched | Config, HostConfig, RestartPolicy, InspectResponse | Direct typed access to the field shapes we need to translate. [VERIFIED: `go doc` inspection of types this session] |
| `github.com/moby/moby/api/types/network` | matched | NetworkingConfig, EndpointSettings | Multi-network support via NetworkConnect (post-Create) for any networks beyond the first. [VERIFIED: SDK doc inspection] |
| Go stdlib `net/http`, `log/slog`, `os/exec` (only for boot self-exec in d) | std | — | Already in use |

### Supporting (existing — keep)

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/google/go-containerregistry` | v0.20+ | crane.Digest, registry resolution | Phase 3 baseline; not affected by Phase 9 |
| `github.com/google/renameio/v2` | v2.0.2 | Atomic state writes | Not affected |
| `robfig/cron/v3` | v3.0.1 | Cron schedule | Not affected |

### NEW dependencies for Phase 9

**None.** The three new docker.Client methods (`ContainerCreate`, `ContainerRemove`, `ContainerStart`) are already exposed by the existing `github.com/moby/moby/client` import (we just don't wrap them in the facade yet). No new modules. Bloat budget delta: **+0 MB** (confirmed in HANDOFF.md "Bloat measurement"). Base-image revert delta: **~−20 MB** (HANDOFF.md, also see Dockerfile comment lines 68–87 which document base-debian12's ~22 MB vs. static-debian12's ~1.9 MB).

### Alternatives Considered (rejected)

| Instead of | Could Use | Tradeoff — REJECTED because |
|------------|-----------|-----------------------------|
| Socket-only via `moby/moby/client` | `compose-spec/compose-go/v2` (parser only) | +3.6 MB, +26 modules. Useful only if we kept compose semantics; we don't need them after (a). [VERIFIED: HANDOFF.md bloat measurement] |
| Socket-only via `moby/moby/client` | `docker/compose/v2` Go SDK (full orchestrator) | +53 MB, +395 modules. **Blows the 30 MB budget** on dependencies alone. [VERIFIED: HANDOFF.md] |
| Same-binary helper (`--self-update-orchestrator` mode) | Separate ~5 MB statically-linked Go helper image | Violates "one container, one binary" C1 spirit — would force a second build target, second CI publish step, second versioning surface. The same-binary flag-mode pattern is exactly what Watchtower's ephemeral-self-update uses. [CITED: watchtower.nickfedor.com/dev/getting-started/updating-watchtower/] |
| Stop+Remove+Create+Start (4 calls) | Rename-based swap (compose's default) | Rename leaves the old container around in stopped state; less clean for the HMI's fixed-name compose service. Watchtower's ephemeral-self-update uses Stop+Create+Start for exactly this reason (handles port reuse atomically). [CITED: watchtower docs] |
| `ContainerRemove(Force:true)` (kill+remove in one call) | `ContainerStop` then `ContainerRemove` | Force is the right primitive here — we're recreating, the SIGTERM grace lives on the orchestrator level not the API call. Compose itself uses Force=true on `up -d --force-recreate`. [CITED: moby/moby/v29 source review patterns] |
| Linear `Remove → Create → Start` | Stop the old container BEFORE removing the new container's name (avoids name collision) | Required when the new container has the same `--name`. The sequence MUST be: Stop(old) → Remove(old) → Create(new with same name) → Start(new). If Remove succeeds but Create fails, the original is gone — see "Atomic recreate semantics" below |

**Installation:** No new packages — the methods already exist on `*client.Client` via the existing import path.

**Version verification:** `moby/moby/client v0.4.1` is already pinned; the methods used are stable across the Engine API 1.40–1.54 range the SDK supports. [VERIFIED: go.mod lockfile + `_sdk_shape.txt` lines 132, 150, 158]

## Architecture Patterns

### System Architecture Diagram

```
        ┌────────────────────────────┐
        │  Operator clicks Update    │
        │  in browser (Svelte UI)    │
        └─────────────┬──────────────┘
                      │ POST /api/containers/{svc}/update
                      ▼
        ┌────────────────────────────┐
        │  api.handleUpdate          │
        │  ValidateService→SelfProt  │
        │  →LookupContainer→Safety   │
        └─────────────┬──────────────┘
                      ▼
        ┌────────────────────────────┐
        │  actions.Orchestrator      │
        │   .Update(ctx, svc)        │
        │  1. composeReader.CheckU.  │ ← compose.Reader stays
        │  2. lockService(svc)       │   (drift detection
        │  3. pullAndVerifyDigest    │    still needed)
        │  4. recreate.Service(...)  │ ← NEW — was runner.UpdateService
        │  5. inspectAndVerify       │
        └─────────────┬──────────────┘
                      ▼
        ┌────────────────────────────┐
        │  recreate.Service          │ ← NEW PACKAGE / file
        │  1. Inspect(oldID)         │
        │  2. translateFields →      │
        │      Config/Host/Net       │
        │  3. Stop(oldID, grace=10s) │
        │  4. Remove(oldID, force=t) │
        │  5. Create(name=svc, ...)  │
        │  6. NetworkConnect(extra)  │ ← if >1 network
        │  7. Start(newID)           │
        │  → returns newID or error  │
        └─────────────┬──────────────┘
                      ▼
              docker daemon
              (no subprocess)

----------------------- self-update path (Phase 9 d) -----------------------

        ┌────────────────────────────┐
        │  Operator clicks Update    │
        │  on docker-update row      │
        └─────────────┬──────────────┘
                      │ POST /api/update/docker-update OR new POST /api/self-update
                      ▼
        ┌────────────────────────────┐
        │  api.handleSelfUpdate      │
        │  (NO CheckSelfProtection — │
        │   that's the whole point)  │
        └─────────────┬──────────────┘
                      ▼
        ┌────────────────────────────┐
        │  selfupdate.Spawn(ctx)     │ ← NEW package
        │  1. resolve new image      │
        │     digest via registry    │
        │  2. ContainerCreate(       │
        │       image=newImage,      │
        │       cmd=[--self-update-  │
        │             orchestrator   │
        │             --target=docker│
        │             -update        │
        │             --parent-pid=  │
        │             os.Getpid()],  │
        │       binds=[/var/run/     │
        │              docker.sock]) │
        │  3. ContainerStart(helper) │
        │  4. respond 202 Accepted   │
        │     to operator            │
        │  5. exit cleanly when      │
        │     SIGTERM arrives from   │
        │     helper                 │
        └─────────────┬──────────────┘
                      ▼
            helper container (same image, different flag)
            1. wait 1s (let parent return 202)
            2. recreate.Service("docker-update") via the same
               primitive used in (a)
            3. poll new container /healthz over docker sock-
               via-network until 200 OR timeout 60s
            4. on success: exit 0; on failure: log + exit non-zero
            5. when helper exits, daemon GC's it via auto-remove
```

### Recommended Project Structure

```
internal/
├── docker/
│   ├── client.go           # +3 methods: ContainerCreate, ContainerRemove, ContainerStart
│   ├── moby.go             # +3 thin SDK adapters
│   └── _sdk_shape.txt      # APPEND ContainerCreate/Remove/Start identifier index
├── recreate/               # NEW PACKAGE — Phase 9 (a)
│   ├── recreate.go         # Service(ctx, dockerClient, svcName) (newID, error)
│   ├── translate.go        # InspectResponse → (Config, HostConfig, NetworkingConfig)
│   ├── recreate_test.go    # field-translation table tests, name-collision recovery
│   └── translate_test.go   # 30+ field-by-field translation cases
├── compose/
│   ├── reader.go           # KEEP — drift detection still wanted
│   ├── errors.go           # KEEP — ErrComposeFileMoved still emitted
│   └── runner.go           # DELETE — body replaced by recreate.Service
├── selfupdate/             # NEW PACKAGE — Phase 9 (d)
│   ├── spawn.go            # parent-side: ContainerCreate helper
│   ├── orchestrate.go      # helper-side: drives the recreate, polls /healthz
│   └── spawn_test.go
├── actions/
│   └── orchestrator.go     # one line changes: o.runner.UpdateService → recreate.Service
├── api/
│   ├── handlers_actions.go # KEEP existing handlers
│   └── handlers_self.go    # NEW — POST /api/self-update (or update handler branch)
└── cmd/docker-update/
    └── main.go             # +flag.Bool("self-update-orchestrator", ...)
                            # if set → run selfupdate.Orchestrate then exit
                            # else  → run HTTP server (existing path)
```

### Pattern 1: Inspect-then-Recreate (Phase 9 a core primitive)

**What:** Single-instance container recreation using the daemon API, preserving every config field the daemon round-trips on inspect.

**When to use:** All four entry points — Update, Rollback, ForcePull(recreate=true), and the self-update helper's recreate of docker-update itself.

**Example sequence** (refined from `compose-go` v2's `internal/recreate.go` patterns and Watchtower's ephemeral orchestrator):

```go
// Source: pattern derived from moby/moby/client v0.4.1 SDK + Watchtower ephemeral-self-update
// internal/recreate/recreate.go
package recreate

import (
    "context"
    "fmt"
    "time"

    "github.com/centroid-is/docker-update/internal/docker"
    "github.com/moby/moby/api/types/container"
)

// Service performs Stop → Remove → Create (same name) → NetworkConnect extras → Start
// against a compose service identified by com.docker.compose.service=<svc>.
// Returns the new container ID on success.
//
// Atomic-recreate failure modes (see "Atomic recreate semantics" below):
//   - Stop fails           → return err, old container untouched
//   - Remove fails         → return err, old container in stopped state (operator can investigate)
//   - Create fails         → return err, OLD CONTAINER IS GONE — caller logs loudly
//   - NetworkConnect fails → call ContainerRemove(newID, force) to clean up + return err
//   - Start fails          → call ContainerRemove(newID, force) to clean up + return err
func Service(ctx context.Context, cli docker.Client, svcName string) (string, error) {
    oldID, err := lookupByComposeService(ctx, cli, svcName) // existing helper in orchestrator.go
    if err != nil {
        return "", fmt.Errorf("recreate.Service: lookup %q: %w", svcName, err)
    }
    inspect, err := cli.ContainerInspect(ctx, oldID)
    if err != nil {
        return "", fmt.Errorf("recreate.Service: inspect %s: %w", oldID, err)
    }

    cfg, hostCfg, netCfg, extraNets := translateFields(inspect.Container)

    // Stop with 10s grace (matches compose's default stop_grace_period)
    stopTimeout := 10
    if err := cli.ContainerStop(ctx, oldID, container.StopOptions{Timeout: &stopTimeout}); err != nil {
        return "", fmt.Errorf("recreate.Service: stop %s: %w", oldID, err)
    }

    // Remove with Force=true so a "stopped but still attached" container doesn't block.
    // RemoveVolumes=false because compose-managed volumes belong to the compose project,
    // not this individual container.
    if err := cli.ContainerRemove(ctx, oldID, docker.ContainerRemoveOptions{Force: true}); err != nil {
        return "", fmt.Errorf("recreate.Service: remove %s: %w", oldID, err)
    }

    // Create — the container Name MUST match the inspect.Name to preserve `container_name:`
    // compose semantics. inspect.Name has a leading "/" the SDK strips on Create.
    name := strings.TrimPrefix(inspect.Container.Name, "/")
    res, err := cli.ContainerCreate(ctx, docker.ContainerCreateOptions{
        Config:           cfg,
        HostConfig:       hostCfg,
        NetworkingConfig: netCfg, // first network only (see translateFields)
        Name:             name,
    })
    if err != nil {
        // OLD container is GONE — this is the unrecoverable branch. Log loudly;
        // the operator must manually `docker compose up -d <svc>` to recover.
        return "", fmt.Errorf("recreate.Service: create %s: %w", name, err)
    }

    // Connect extra networks (NetworkingConfig.EndpointsConfig in Create accepts only
    // ONE endpoint pre-API-1.44; though Engine v29 supports many, we use the safer
    // create-then-connect-extras pattern for compatibility with older daemons).
    for netName, eps := range extraNets {
        if err := cli.NetworkConnect(ctx, netName, docker.NetworkConnectOptions{
            Container:       res.ID,
            EndpointSettings: eps,
        }); err != nil {
            _ = cli.ContainerRemove(ctx, res.ID, docker.ContainerRemoveOptions{Force: true})
            return "", fmt.Errorf("recreate.Service: connect %s to %s: %w", res.ID, netName, err)
        }
    }

    if err := cli.ContainerStart(ctx, res.ID, docker.ContainerStartOptions{}); err != nil {
        _ = cli.ContainerRemove(ctx, res.ID, docker.ContainerRemoveOptions{Force: true})
        return "", fmt.Errorf("recreate.Service: start %s: %w", res.ID, err)
    }
    return res.ID, nil
}
```

**Confidence:** HIGH on the sequence; the only piece that needs an empirical check is whether the daemon (API 1.54) on the elevator-hmi accepts multi-endpoint NetworkingConfig in Create (engine v29 should, per PR #45906). The safer create+connect-extras path documented above is portability insurance.

### Pattern 2: Field translation (InspectResponse → Create inputs)

**What:** Pure data transform mapping `container.InspectResponse` → `(*container.Config, *container.HostConfig, *network.NetworkingConfig, map[string]*network.EndpointSettings)`.

**When to use:** Once per recreate.Service call; pure function, easy to unit-test.

**Source:** `github.com/moby/moby/api/types/container.InspectResponse` (lines 717–751 of `_sdk_shape.txt`) and Config/HostConfig shapes documented at the bottom of this section.

**The translation table** (load-bearing — every field that DIFFERS between Inspect output and Create input must be explicitly handled):

| Inspect field | Create destination | Translation rule | Notes / gotcha |
|---------------|--------------------|------------------|----------------|
| `inspect.Config.*` | `Config.*` | Direct copy of pointer (`Config` is `*container.Config`) | No transformation needed — the daemon round-trips `Config` symmetrically [VERIFIED: types/container.Config doc] |
| `inspect.HostConfig.*` | `HostConfig.*` | Direct copy of pointer | Same symmetry; INCLUDES Mounts, Binds, RestartPolicy, NetworkMode |
| `inspect.HostConfig.Binds` | `HostConfig.Binds` | Pass-through. **THIS IS THE FIX FOR THE (b) RELATIVE-PATH BUG.** | `inspect.HostConfig.Binds` carries the ABSOLUTE host paths the daemon resolved at original-create time (e.g. `/home/centroid/wayland-socket:/run/wayland`). When docker-update calls Create with this verbatim, the new container gets the SAME absolute host paths. No `./relative` ever touches docker-update's CWD. [VERIFIED: SDK Bind semantics — Binds are absolute host paths in InspectResponse] |
| `inspect.HostConfig.Mounts` | `HostConfig.Mounts` | Pass-through | Mounts are the structured form (`mount.Mount{Type, Source, Target, ...}`); also carries absolute host paths after the daemon's compose-time resolution |
| `inspect.HostConfig.NetworkMode` | `HostConfig.NetworkMode` | Pass-through | `host`, `bridge`, `container:abc`, or a custom-network name — all daemon-resolved already |
| `inspect.HostConfig.RestartPolicy.Name` | `HostConfig.RestartPolicy.Name` | Pass-through; empty string and `"no"` are SEMANTICALLY IDENTICAL but render differently | **Gotcha #1:** If `inspect.HostConfig.RestartPolicy.Name == ""` (no restart policy set), the daemon may accept it as "no" but some Engine versions reject empty. **Mitigation:** if empty, normalize to `container.RestartPolicyDisabled` (which is the string `"no"` per `_sdk_shape.txt` line ~RestartPolicyMode). [VERIFIED: go doc RestartPolicyMode] |
| `inspect.Config.Healthcheck` | `Config.Healthcheck` | Pass-through if non-nil; preserve nil-vs-empty distinction | **Gotcha #2:** `Healthcheck: &container.HealthConfig{Test: nil}` and `Healthcheck: nil` are NOT equivalent. The first means "use image's HEALTHCHECK"; the second means "no healthcheck override." Preserve the pointer-nil vs struct-with-nil-Test distinction. |
| `inspect.NetworkSettings.Networks` | `NetworkingConfig.EndpointsConfig` | Iterate the map; pass `Networks[name].Aliases`, `IPAddress`, `IPAMConfig` through to a new `*EndpointSettings`. **For the FIRST network only.** | **Gotcha #3:** Pre-Engine-1.44 daemons reject multi-endpoint NetworkingConfig in Create. Engine v29 supports it but we'd lock in API ≥1.44. The safer pattern is: put the FIRST network in Create's NetworkingConfig, then loop over the rest with `ContainerConnect`. [CITED: moby/moby PR #45906] |
| `inspect.NetworkSettings.Networks[*].Aliases` | EndpointSettings.Aliases | Pass-through, but FILTER out the auto-generated short-ID alias (`container.Name[:12]`) | **Gotcha #4:** The daemon auto-adds the container's short-ID as an alias; passing it back to Create works but pollutes the alias list. Compose strips it; we should too. [CITED: docker/cli issue #1854 — analogous behavior] |
| `inspect.NetworkSettings.Networks[*].IPAMConfig.IPv4Address` | EndpointSettings.IPAMConfig.IPv4Address | Pass-through ONLY if the original was explicitly assigned (non-empty in IPAMConfig); skip if daemon-auto-assigned | **Gotcha #5:** If the network uses DHCP/auto-assignment, `inspect.NetworkSettings.Networks[*].IPAMConfig` is often nil even though `IPAddress` has a value. Distinction: `IPAMConfig != nil` means operator-pinned; `IPAddress` populated with `IPAMConfig == nil` means daemon-assigned. Don't re-pin daemon-assigned IPs — let the daemon re-assign |
| `inspect.Config.Image` | `Config.Image` | **CRITICAL:** Set to the post-pull `image:tag` reference, NOT the image ID from inspect | **Gotcha #6:** `inspect.Config.Image` is whatever the operator wrote in compose (`flutter:latest`). After our pull, the daemon has resolved that to a new ID, but we want the NEW container to use the SAME tag reference. Use the pre-pull `snapshot.Image + ":" + snapshot.Tag`. The orchestrator already has this value. |
| `inspect.HostConfig.Init` | `HostConfig.Init` | Pass-through *pointer* (nil = daemon default, false = explicit off, true = explicit on) | Pointer-bool tri-state. Easy to flatten by accident |
| `inspect.Config.User` | `Config.User` | Pass-through verbatim | Operator's compose `user: "65532:998"` lives here |
| `inspect.Config.Env` | `Config.Env` | Pass-through verbatim | Carries all compose-injected env vars (operator config, no secrets at the inspect level for our use case) |
| `inspect.Config.Labels` | `Config.Labels` | Pass-through verbatim — INCLUDING `com.docker.compose.*` and `hmi-update.*` | **Critical** — the new container MUST keep the compose labels so post-recreate `ContainerList(label=com.docker.compose.service=<svc>)` finds it. orchestrator.lookupContainerIDByService relies on this. |
| `inspect.HostConfig.Resources.*` (memory, cpu, etc.) | `HostConfig.Resources.*` | Pass-through; `Resources` is embedded in `HostConfig` | Compose-set memory_limit / cpus all live here |

**Fields NOT in Create input (Inspect-only — IGNORE them):**

| Inspect-only field | Why ignore |
|--------------------|------------|
| `inspect.ID`, `inspect.Created`, `inspect.Path`, `inspect.Args` | Runtime identity; new container will have its own |
| `inspect.State` | Runtime state — the new container starts fresh |
| `inspect.RestartCount` | Daemon counter, resets on Create |
| `inspect.LogPath`, `inspect.ResolvConfPath`, `inspect.HostnamePath`, `inspect.HostsPath` | Daemon-managed files; daemon recreates them |
| `inspect.Image` (the top-level image ID) | Different field from `inspect.Config.Image`; this is the resolved ID. Use `inspect.Config.Image` (the reference) or the orchestrator's `snapshot.Image + ":" + snapshot.Tag` |
| `inspect.GraphDriver`, `inspect.Mounts` (top-level) | Top-level Mounts is REDUNDANT with `inspect.HostConfig.Mounts` — use the HostConfig version |
| `inspect.NetworkSettings.SandboxID`, `SandboxKey`, `Ports` (with allocated host ports) | Sandbox identity; daemon allocates new sandbox |

**Confidence:** HIGH for the 12 mapped fields (each verified against the SDK shape in `_sdk_shape.txt` lines 717–751 and the `go doc` output captured this session). MEDIUM-HIGH for the alias-filter gotcha (#4) — pattern is well-documented in compose's own source but not in a stable public reference. **The plan MUST include a translate_test.go with one test case per row in the translation table above** so a regression here surfaces at unit-test speed rather than at the next manual smoke on the elevator-hmi.

### Pattern 3: Atomic recreate semantics (failure-mode catalog)

**What:** Each step in the Remove→Create→NetworkConnect→Start sequence can fail; the recovery posture differs per step.

**Failure-mode catalog:**

| Failure step | Old container state | New container state | Recovery action | Operator visibility |
|--------------|---------------------|---------------------|-----------------|---------------------|
| Stop fails | Still running | N/A | Return err, do nothing | `action.compose_failed` → 500 to UI, "see logs" |
| Remove fails (after Stop) | Stopped, not removed | N/A | Return err, OLD container is offline — operator must `docker rm` and `docker compose up -d` manually | `action.recreate_failed` (NEW slog event) → 500; UI shows stopped state on next poll |
| **Create fails** (after Stop+Remove) | **GONE** | Never created | Return err. **The original container is GONE.** State.previous_digest still records the OLD image, so a subsequent Rollback would `docker tag` + recreate from the OLD image — this is the existing rollback path. Operator can also manually `docker compose up -d <svc>` | `action.recreate_failed` → 500; UI shows MISSING container (cron poll will surface "not in compose state") |
| NetworkConnect fails (after Create+first network) | Gone | Created, only first network attached, NOT started | Call `ContainerRemove(newID, force=true)` to clean up; return err. Operator can `docker compose up -d <svc>` to re-create cleanly | `action.recreate_failed` → 500 |
| Start fails (after Create + all networks) | Gone | Created, all networks, not running | Call `ContainerRemove(newID, force=true)` to clean up; return err. Same as NetworkConnect-fail path | `action.recreate_failed` → 500 |
| Start succeeds, container immediately exits | Gone | Started + exited (no longer running) | NOT a recreate failure — `inspectAndVerify` (existing path) catches this via the verify-after-recreate loop and surfaces `verify_failed` | `action.verify_failed` → 500 with structured body (existing) |

**Recommended sequence with explicit error handling:** see the `recreate.Service` code block above. Key invariants:
1. **No partial cleanup of the OLD container** — if Stop or Remove succeeds, the old container is gone and that's final.
2. **Best-effort cleanup of the NEW container** — if Create succeeds but a later step fails, attempt `ContainerRemove(newID, force=true)` and tolerate that cleanup's own errors.
3. **A new slog event class** — `action.recreate_failed` (vs. the existing `action.compose_failed`) — surfaces the new failure surface. The wire body stays `actionBodyComposeFailed` ("see logs") since the UI semantics are identical from the operator's perspective; the slog event differentiates the cause.

**Compose's own internal pattern** (referenced for design parity): compose-go v2 uses a similar Stop→Remove→Create sequence with `--force-recreate`, except it ALSO offers a `--no-recreate` and a `--renew-anon-volumes` mode we don't need. The error-recovery posture is the same: if Create fails after Remove, compose surfaces the error and tells the operator to retry. We match this exactly.

**Confidence:** HIGH — the failure-mode catalog is derived directly from the docker daemon API's atomicity guarantees (each call is independently atomic; there is no transactional grouping). [CITED: moby/moby `daemon/containerd/image_pull.go` and similar daemon endpoints]

### Pattern 4: Same-binary self-update orchestrator (Phase 9 d)

**What:** docker-update spawns a copy of its own image with the `--self-update-orchestrator` flag; the helper recreates docker-update from outside, verifies, and exits.

**Source pattern:** Watchtower's `ephemeral-self-update` mechanism — short-lived container, same image, internal flag `--self-update-orchestrator`. [CITED: watchtower.nickfedor.com/dev/getting-started/updating-watchtower/]

**Sequence:**

```
[ docker-update (running) ]                [ helper (new container) ]                [ docker-update (new) ]
   POST /api/self-update
   │
   ├─ resolve target image via              <not yet running>                          <not yet running>
   │  registry.Digest("ghcr.io/...")
   │
   ├─ ContainerCreate(                       
   │    image=NEW_DIGEST,                    
   │    cmd=["docker-update",                
   │         "--self-update-orchestrator",   
   │         "--target=docker-update",       
   │         "--target-image=ghcr.io/...",   
   │         "--target-digest=NEW_DIGEST"],  
   │    binds=["/var/run/docker.sock"],      
   │    auto-remove=true,                    
   │    labels={                             
   │      "centroid.docker-update.helper":   
   │      "true"})                           
   │
   ├─ ContainerStart(helperID) ──────────►   helper.main(): detect --self-update-orchestrator
   │                                          │
   ├─ 202 Accepted to operator                 ├─ wait 1s (let parent return 202)
   │                                          │
   ├─ continue serving until SIGTERM           ├─ recreate.Service(ctx, cli, "docker-update")  
   │                                          │  ↑ uses the SAME primitive from (a):
   │                                          │    Stop→Remove→Create+SameLabels→Start
   │                                          │
   │ ◄─── SIGTERM via ContainerStop ─────────┤
   │                                          │
   exit 0                                     │
                                              ├─ poll docker daemon: ContainerInspect(newID)
                                              │  loop {
                                              │    Running == true
                                              │    AND RestartCount unchanged for 15s
                                              │    AND /healthz over container's
                                              │      bound port returns 200
                                              │  }                                          ◄─── new docker-update boots
                                              │  timeout: 60s
                                              │
                                              ├─ on success: log + exit 0 (auto-remove
                                              │              GC's the helper)
                                              ├─ on failure: log "self_update_failed" + exit 1
                                              │              (operator must manually recover)
                                              │
                                              exit
```

**Critical guarantees the helper needs:**

1. **Wait-then-act timer** — at least 1s between Create and the recreate call, so the parent's 202-Accepted response is in flight. Tunable via env (`DOCKER_UPDATE_SELF_UPDATE_DELAY`).
2. **Exit non-zero on failure** — so any operator inspecting `docker logs <helper>` or `docker ps -a` sees the failure cleanly.
3. **Use auto-remove (`HostConfig.AutoRemove: true`)** — the helper is one-shot; no need for an operator to GC it manually.
4. **Label the helper** (`centroid.docker-update.helper=true`) — so a future docker-update boot can detect orphaned helpers (Watchtower uses the same `com.centurylinklabs.watchtower.ephemeral-orchestrator` label pattern for the same purpose).
5. **No state inheritance** — the helper does NOT bind-mount the state file or compose file; it only needs the docker socket and the in-memory CLI args. State carries forward in the bind-mounted state file across the recreate just like every other compose service.

**Should the helper be the same binary or a separate one?**

| Approach | Pros | Cons | Verdict |
|----------|------|------|---------|
| Same binary, flag-mode (`--self-update-orchestrator`) | One image, one CI pipeline, one versioning surface; helper code can `import "github.com/.../internal/recreate"` directly | main.go grows a top-level branch | **RECOMMENDED** — Watchtower uses this exact pattern; preserves C1 spirit |
| Separate ~5 MB Go binary in its own image | Smallest helper image | Two build targets, two publish steps, two versioning surfaces; the helper image would lag behind in updates | REJECTED — violates the "one container" ethos of C1 |
| Alpine + crane CLI + bash script | No Go code | Brittle; introduces a second runtime dependency (alpine + crane); reasoning across two languages | REJECTED — increases test surface and runtime risk |

**Confidence:** HIGH for the flow; MEDIUM for the 60s health-poll timeout — the elevator-hmi's flutter recreate can take 5-15s, and docker-update's recreate is much faster (<3s for a Go binary), so 60s is generous. The plan should expose this as a tunable env var (`DOCKER_UPDATE_SELF_VERIFY_TIMEOUT`).

### Pattern 5: CheckSelfProtection bypass for self-update path (Phase 9 d UI surface)

**What:** Currently `internal/actions/middleware.go::CheckSelfProtection` rejects any action where `svc == o.selfService` with 409. Phase 9 (d) introduces a path that legitimately recreates docker-update; that path must NOT trip CheckSelfProtection.

**Decision: separate endpoint, not header/route signal.**

Three design options were considered:

| Option | Pros | Cons | Verdict |
|--------|------|------|---------|
| (A) New endpoint `POST /api/self-update` (no service in path) | Clear separation of concerns; CheckSelfProtection stays as-is and only protects the per-service endpoints; new endpoint is its own thing and doesn't invoke the orchestrator's Update method | One more HTTP route; one more handler; tygo types regen | **RECOMMENDED** |
| (B) Extend `POST /api/containers/{svc}/update` with a special-case for `svc == selfService` | One endpoint | Conflates two semantically different operations (recreate-watched vs. recreate-self); breaks the "watched container" mental model | REJECTED |
| (C) Header (`X-Self-Update: true`) or query param (`?self=true`) on existing endpoint | One endpoint, side-channel signal | Magic header that's easy to miss in the wire contract; harder to test; breaks the principle of least surprise | REJECTED |

**Implementation sketch:**

```go
// internal/api/handlers_self.go (NEW)
// POST /api/self-update
//
// Returns 202 Accepted once the helper container is spawned and started.
// The operator polls /healthz on the post-update docker-update to confirm.
// Body shape: {"status":"helper_spawned","helper_id":"abc123"}
func (s *Server) handleSelfUpdate(w http.ResponseWriter, r *http.Request) {
    if s.selfUpdater == nil {
        writeActionBody(w, http.StatusServiceUnavailable, actionBodySelfUpdaterUnwired)
        return
    }
    helperID, err := s.selfUpdater.Spawn(r.Context())
    if err != nil {
        slog.Error("self_update.spawn_failed", "err", err)
        writeActionBody(w, http.StatusInternalServerError, actionBodySelfUpdateFailed)
        return
    }
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    w.WriteHeader(http.StatusAccepted)
    _, _ = w.Write([]byte(`{"status":"helper_spawned","helper_id":"` + helperID + `"}`))
}
```

**Important:** the UI's docker-update row should ALSO be able to display an Update button that POSTs to `/api/self-update` instead of `/api/containers/docker-update/update`. The Phase 5 UI plan needs to learn about the self-row. For Phase 9 it's enough to ship the endpoint + the helper; UI integration can ship in a follow-up (cf. Phase 5 plans 05-02, 05-04).

**Confidence:** HIGH on the endpoint shape; the body fields are conventional 202-Accepted.

### Anti-Patterns to Avoid

- **Re-introducing any `os/exec` to docker compose** — the whole point of (a) is to delete this surface. The `compose.Reader` (drift detection via `os.Stat`) STAYS; the runner GOES. Phase 9's locked invariant is: `grep -r "docker compose\|compose up\|exec.Command" internal/actions internal/recreate` returns ZERO production-code hits.
- **Trying to "patch" the compose-path bug with `--project-directory`** — that's the interim (b) fix LISTED only as a fallback if (a) is split. The dependency lock says (a) implies (b); do NOT ship (b) interim if (a) is going forward.
- **Adding a "compose helper" container** to provide `docker` CLI — same anti-pattern. The whole reason base-debian12 grew the image by 20 MB was the bind-mounted docker CLI's dynamic linker; static-debian12 + no CLI is the destination.
- **Holding the state.Store.mu across daemon calls in `recreate.Service`** — the existing actions/orchestrator anti-deadlock invariant (ARCHITECTURE.md 419–420) STILL APPLIES. recreate.Service is called from inside the orchestrator's Update body; the orchestrator already does its state mutations via the channel. recreate.Service itself never touches the state store.
- **Putting the self-update helper's polling in the parent** — the parent is being recreated; it can't poll itself. The helper polls; the parent just spawns and exits.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Container recreate logic | Custom rewrite of compose's recreate loop | `moby/moby/client` ContainerInspect+Stop+Remove+Create+Start | The daemon API is the abstraction; doing it via subprocess was the original mistake |
| Field translation (Inspect → Create) | "Easy mode" shortcuts that copy half the fields | Comprehensive table-test (one row per field in Translation Table above) | This is exactly the surface where bugs hide; test every field explicitly |
| Network re-attachment | Re-parsing the compose file to find networks | `inspect.NetworkSettings.Networks` map → loop over and call `NetworkConnect` for endpoints beyond the first | Daemon has already resolved network names + IDs; just pass them back |
| Restart-policy preservation | Defaulting to "always" or "no" | Read `inspect.HostConfig.RestartPolicy` verbatim, normalize empty `Name` → `"no"` | Compose-set restart policies are operator intent; don't second-guess |
| Self-update orchestration | Custom orchestrator container with bash + crane | Same-binary `--self-update-orchestrator` flag mode | Watchtower's documented pattern; minimal new surface; preserves C1 |
| 202 + polling pattern | Long-polling, SSE, websockets | Plain 202 Accepted + operator-side `/healthz` poll | The OBS-03 contract already exists for /healthz; no new infra needed |

**Key insight:** Every "complication" Phase 9 introduces (recreate semantics, self-update helper, multi-network handling) maps directly to a well-trodden pattern in moby/moby itself or in Watchtower's ephemeral-self-update path. The plan should NOT invent new patterns where existing ones suffice.

## Runtime State Inventory

> Phase 9 is partly a refactor (replace runner.go) and partly a feature add (self-update). State inventory is on the refactor portion.

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| **Stored data** | `./docker_update_state.json` per-container records carry `ContainerID` (the daemon's runtime ID, which CHANGES every time docker-update recreates a container). | **No data migration.** The existing discovery loop already re-binds ContainerID from the daemon events after every recreate; Phase 9 doesn't change that. |
| **Live service config** | No live external configs reference the runner. compose.Runner.composePath is captured from `DOCKER_UPDATE_COMPOSE_PATH` at boot; compose.Reader still reads the same path post-Phase-9 (drift detection stays). | None. The env-var contract is unchanged. |
| **OS-registered state** | None — docker-update runs as a single container, not a systemd/launchd service. Compose itself is the OS-side registration and it does NOT change. | None. |
| **Secrets and env vars** | `DOCKER_UPDATE_COMPOSE_PATH` (still consumed by compose.Reader), `DOCKER_UPDATE_CRON` (unchanged), `DOCKER_UPDATE_SELF_SERVICE` (still used by CheckSelfProtection for the per-service endpoints). NEW for (d): `DOCKER_UPDATE_SELF_UPDATE_DELAY` (default 1s), `DOCKER_UPDATE_SELF_VERIFY_TIMEOUT` (default 60s). | Document the two NEW env vars in PROJECT.md and README. |
| **Build artifacts / installed packages** | The Dockerfile change (base-debian12 → static-debian12) drops the implicit dependency on the host's docker CLI bind-mount. `docker-compose.example.yml` lines 54–55 ("/usr/bin/docker" + "/usr/libexec/docker/cli-plugins") must be DELETED in lockstep with the Dockerfile change. Operators upgrading to Phase 9 will need to remove those two bind-mounts from their installed `docker-compose.yml`. | Plan must include: (1) `docker-compose.example.yml` edit, (2) README upgrade note: "Phase 9 removes the docker CLI bind-mount requirement; operators upgrading should delete the two `:ro` mounts for `/usr/bin/docker` and `/usr/libexec/docker/cli-plugins` from their installed compose file." |

**Nothing found in category — explicitly:** No databases, no message queues, no external scheduler integrations, no shared filesystem caches. docker-update's footprint stays "one container, one state file, one compose file, one socket."

## Common Pitfalls

### Pitfall 1: Forgetting the compose label on the recreated container

**What goes wrong:** Phase 4's existing `actions.orchestrator.lookupContainerIDByService` filters `ContainerList` by `com.docker.compose.service=<svc>`. If recreate.Service doesn't propagate the original container's `com.docker.compose.service`, `com.docker.compose.project`, `com.docker.compose.project.working_dir`, etc. into the new Config.Labels, the lookup returns zero results and verify_after_recreate surfaces a misleading "no container found for service" error.

**Why it happens:** Tempting "minimal labels" thinking — "we're not part of compose, why do we need compose's labels?" — but the LOOKUP path treats those labels as the identity contract.

**How to avoid:** Pass-through `inspect.Config.Labels` VERBATIM. Test: TestRecreate_PreservesComposeLabels (the recreated container's `com.docker.compose.service` MUST equal the original's; assert by re-running ContainerList and finding the new ID).

**Warning signs:** Every Update on the elevator-hmi would surface `verify_failed` with reason `post-recreate ContainerList(<svc>) failed: no container found`.

### Pitfall 2: Lose track of which container ID is "current" mid-recreate

**What goes wrong:** discovery.go's docker events goroutine sees the destroy event for the OLD container and removes it from state. Then sees the create+start event for the NEW container and adds it back. Between those two events, the per-service action mutex is HELD (we're mid-Update). If discovery's "destroy" handler is allowed to wipe ContainerID or set Stopped=true while Update is still running, the orchestrator's post-recreate `inspectAndVerify` does a re-lookup via `com.docker.compose.service=<svc>` — but if discovery has already added the new ID by then, all is well. If discovery is between events, lookupContainerIDByService gets All=true containers and picks newest-by-Created. The race is benign IF the test verifies it.

**Why it happens:** Phase 2's discovery loop uses Phase 3's StateUpdate channel (`KindContainerDestroyed`, `KindContainerStarted`). Action orchestrator's lookupContainerIDByService is the seam.

**How to avoid:** TestRecreate_DiscoveryRaceTolerated — a fake docker.Client returns ContainerList results with a brief window where neither old nor new container is visible; assert lookupContainerIDByService either (a) returns the new ID once it's there, or (b) returns "no container" cleanly and the orchestrator retries inside the verify window.

**Warning signs:** Intermittent verify_failed on the elevator-hmi during cron-triggered races. The existing BUG-7b fix already addresses one such race; document this one if it surfaces.

### Pitfall 3: Multi-network containers fail to attach the SECOND network

**What goes wrong:** ContainerCreate's NetworkingConfig accepts only ONE endpoint pre-Engine-API-1.44 (Engine 25+). If the operator's compose service has 2+ networks (rare on HMI but possible — flutter has `wayland-socket` mount but only one network in practice), we ship Create with the first network in NetworkingConfig and silently lose the rest.

**Why it happens:** The SDK doesn't loudly reject multi-network in NetworkingConfig on older engines; it just silently picks one. [CITED: moby/moby issue #44613]

**How to avoid:** Use the "Create with first network, then NetworkConnect the rest" pattern documented in Pattern 1 above. Even though Engine v29 supports multi-endpoint Create, the create-then-connect-extras path is the portability-safe pattern that has worked since Docker 1.21.

**Warning signs:** A two-network container would lose connectivity on one of its networks after Update. Currently no HMI container has two networks; the elevator-hmi compose stack uses one network plus host bind-mounts. But the test (TestRecreate_TwoNetworks_BothAttached) should fixture this case so it doesn't regress in future operator compose configs.

### Pitfall 4: AutoRemove on the helper means we can't read its logs after it exits

**What goes wrong:** Phase 9 (d)'s helper container has `HostConfig.AutoRemove: true` so it doesn't accumulate stopped containers. If self-update fails and the helper exits non-zero, the daemon GCs it before an operator can `docker logs <helper>`.

**Why it happens:** AutoRemove + non-zero exit + post-mortem investigation are in tension.

**How to avoid:** The helper SHOULD `slog.Info`/`slog.Error` every milestone to stdout, which the daemon captures as long as the container exists. For post-mortem: docker-update (the parent) records the helperID in slog at spawn time; an operator can `docker logs <helperID>` IFF they catch the helper before AutoRemove fires (typically <1s after exit). For long-term diagnosis, switch off AutoRemove via `DOCKER_UPDATE_SELF_UPDATE_KEEP_HELPER=true` env var.

**Warning signs:** Operator reports "self-update failed but I can't see why."

**Recommendation:** Default to `AutoRemove: true`; expose the env var; document the env var in the self-update runbook.

### Pitfall 5: Static-debian12 revert breaks Phase-7 idle-RAM measurement

**What goes wrong:** The Dockerfile change from base-debian12 → static-debian12 also removes `tzdata`, `ca-certificates`, and a handful of runtime libs. ca-certificates is needed for crane.Digest's HTTPS to public registries; tzdata is needed for any code that calls `time.LoadLocation`. Static-debian12 ships **both** (per distroless README, the `:nonroot` variants of all four base images include CA certs + tzdata + nonroot user).

**Why it happens:** Easy to confuse `distroless/static:latest` (no certs) with `distroless/static-debian12:nonroot` (has certs).

**How to avoid:** Verify in the plan's verify gate: `docker run --rm docker-update:phase9 ls /etc/ssl/certs/ca-certificates.crt` must succeed. Or run a smoke test that hits a public registry (crane.Digest against ghcr.io) inside the new image.

**Warning signs:** Phase 9 (a) lands → first Update against a ghcr.io image surfaces `x509: certificate signed by unknown authority` in the slog. [CITED: GoogleContainerTools/distroless README — static-debian12:nonroot includes /etc/ssl/certs/ca-certificates.crt]

### Pitfall 6: //go:embed all:dist with an empty dist/ in the tests job

**What goes wrong:** Phase 9 (c) splits CI into `tests` (no UI build) and `image+downstream` (UI build). The `tests` job runs `go vet`, `go test -race`, etc. `internal/api/static.go` has `//go:embed all:dist`. If dist/ is missing or empty, Go fails at PARSE TIME (before vet runs) with "pattern dist: no matching files found".

**Why it happens:** Phase 1 plan 01-01 already accounted for this — `.gitignore` uses `internal/api/dist/*` (not `internal/api/dist/`) so `.gitkeep` IS tracked, and `all:dist` matches it. **The pattern `all:dist` matches `.gitkeep` (dotfile), unlike `dist/*` which would not.** [CITED: pkg.go.dev/embed — "all: prefix changes the rule for walking directories to include those files beginning with . or _"; golang/go#43854]

**How to avoid:** Verify `ls internal/api/dist/` shows `.gitkeep` is tracked (which it currently is — `assets/` and `index.html` come from `npm run build` but `.gitkeep` is committed). The `tests` job needs NO extra `mkdir -p` step — git already provides the dir + .gitkeep.

**Warning signs:** `tests` job fails with `pattern dist: no matching files found` if .gitkeep gets accidentally untracked.

**Note for the plan:** the ROADMAP.md text says "Test job needs `mkdir -p internal/api/dist` stub" but this is unnecessary if `.gitkeep` stays committed. Including the `mkdir -p` step in the workflow is harmless belt-and-braces and makes the workflow self-contained for fresh worktrees. Recommended: include the `mkdir` step.

### Pitfall 7: The recreate test doesn't catch the actual elevator-hmi pattern

**What goes wrong:** The relative-path bug surfaced because flutter's compose service has `./wayland-socket:/run/wayland` in its volumes. The test in Phase 9's Wave 2 needs to fixture EXACTLY this pattern: a service in the test compose with a `./relative-path` volume, then a Phase 9 recreate, then assert the resulting container's `HostConfig.Binds[0].Source` matches the operator's host path, NOT docker-update's CWD.

**Why it happens:** Easy to write a passing test that uses absolute paths (which always work in inspect roundtrip) and miss the actual bug.

**How to avoid:** TestRecreate_RelativeBindMount_ResolvesToOperatorHostPath — the e2e compose.test.yml needs a new service with a `./test-relative-mount` volume. Pre-Phase-9 fails (because the compose-CLI runner mis-resolves); post-Phase-9 passes (because recreate.Service uses inspect.HostConfig.Binds which already carries the daemon-resolved absolute path).

**Warning signs:** The locked Success Criterion 2 in ROADMAP.md is specific: "two services with `./relative-path` bind-mounts (mirroring the wayland-socket layout)". The test MUST mirror this.

## Code Examples

### Example 1: Adding three methods to `internal/docker/client.go`

```go
// Source: pattern derived from existing facade in internal/docker/client.go +
// SDK reference _sdk_shape.txt + go doc verification this session
// internal/docker/client.go (additions)

// In the type aliases block:
type (
    ContainerCreateOptions = client.ContainerCreateOptions
    ContainerRemoveOptions = client.ContainerRemoveOptions
    ContainerStartOptions  = client.ContainerStartOptions
    ContainerStopOptions   = container.StopOptions
    NetworkConnectOptions  = client.NetworkConnectOptions
)

// In the Client interface — append:
type Client interface {
    // ... existing methods ...

    // ContainerCreate creates a new container with the given options.
    // Phase 9 (a). The returned ContainerCreateResult.ID is the new ID.
    ContainerCreate(ctx context.Context, opts ContainerCreateOptions) (client.ContainerCreateResult, error)

    // ContainerRemove removes a container. Phase 9 (a).
    ContainerRemove(ctx context.Context, id string, opts ContainerRemoveOptions) error

    // ContainerStart starts a created container. Phase 9 (a).
    ContainerStart(ctx context.Context, id string, opts ContainerStartOptions) error

    // ContainerStop sends SIGTERM with a grace timeout, then SIGKILL. Phase 9 (a).
    ContainerStop(ctx context.Context, id string, opts ContainerStopOptions) error

    // NetworkConnect attaches a container to a network. Phase 9 (a) — used for
    // the second-and-later networks when recreating a multi-network container.
    NetworkConnect(ctx context.Context, networkID string, opts NetworkConnectOptions) error
}
```

**Coordinated edit:** `moby_test.go::TestClient_InterfaceMethodCount` currently pins the interface to 8 methods (per the existing godoc on Client — Ping, ContainerList, ContainerInspect, Events, ImagePull, ImageInspect, ImageTag, ImageList). Phase 9 grows it to 13. The plan MUST include the method-count guard update; it's pinned at the value-of-the-day.

**Source:** existing `internal/docker/client.go` lines 60–150; SDK verified via `go doc github.com/moby/moby/client ContainerCreate` (this session).

### Example 2: handlers wiring for `POST /api/self-update`

```go
// Source: pattern derived from existing internal/api/server.go route registration
// internal/api/server.go (additions in NewServer)

mux.HandleFunc("POST /api/self-update", s.handleSelfUpdate)
// existing routes unchanged
```

```go
// internal/api/handlers_self.go (NEW)
package api

import (
    "log/slog"
    "net/http"

    "github.com/centroid-is/docker-update/internal/selfupdate"
)

const (
    actionBodySelfUpdaterUnwired = `{"error":"self_updater_not_wired","detail":"restart docker-update; check boot logs"}`
    actionBodySelfUpdateFailed   = `{"error":"self_update_failed","detail":"see logs for self_update.spawn_failed event"}`
)

// SelfUpdater is the narrow interface the server needs.
type SelfUpdater interface {
    Spawn(ctx context.Context) (helperID string, err error)
}

func (s *Server) handleSelfUpdate(w http.ResponseWriter, r *http.Request) {
    if s.selfUpdater == nil {
        writeActionBody(w, http.StatusServiceUnavailable, actionBodySelfUpdaterUnwired)
        return
    }
    helperID, err := s.selfUpdater.Spawn(r.Context())
    if err != nil {
        slog.Error("self_update.spawn_failed", "err", err)
        writeActionBody(w, http.StatusInternalServerError, actionBodySelfUpdateFailed)
        return
    }
    slog.Info("self_update.spawn_succeeded", "helper_id", helperID)
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    w.WriteHeader(http.StatusAccepted)
    _, _ = w.Write([]byte(`{"status":"helper_spawned","helper_id":"` + helperID + `"}`))
}
```

### Example 3: CI 2-job split (Phase 9 c)

```yaml
# Source: pattern from existing .github/workflows/ci.yml + parallelization pattern
# from github docs jobs.<job_id>.needs
# .github/workflows/ci.yml (replacement)

name: ci

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true

jobs:
  tests:
    # ~3 min target wall time. No UI build, no docker build.
    # //go:embed all:dist parses against the committed .gitkeep — no stub needed
    # because git tracks .gitkeep. mkdir -p is defensive belt-and-braces.
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
          cache: true

      - name: defensive dist stub (belt-and-braces for fresh worktrees)
        run: mkdir -p internal/api/dist

      - name: go vet
        run: go vet ./...

      - name: install tygo
        run: go install github.com/gzuidhof/tygo@latest

      - uses: actions/setup-node@v4
        with:
          node-version: '22'
      - name: tygo drift check
        run: make check-types

      - name: go test (race)
        run: go test ./... -race

  image-downstream:
    # ~5-6 min target wall time. Owns image build + e2e + idle-RAM + portability.
    # Runs IN PARALLEL with tests (no jobs.needs declaration).
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.26' }
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: 'npm'
          cache-dependency-path: ui/package-lock.json
      - uses: docker/setup-buildx-action@v3

      - name: ui install
        run: npm --prefix ui ci
      - name: ui build
        run: npm --prefix ui run build

      - id: meta
        uses: docker/metadata-action@v5
        with:
          images: ghcr.io/centroid-is/docker-update
          tags: |
            type=raw,value=latest,enable={{is_default_branch}}
            type=semver,pattern={{version}}
            type=sha,prefix=sha-,format=short

      - name: build image (no push)
        uses: docker/build-push-action@v6
        with:
          context: .
          file: Dockerfile
          platforms: linux/amd64
          push: false
          load: true
          tags: docker-update:ci
          labels: ${{ steps.meta.outputs.labels }}
          cache-from: type=gha
          cache-to: type=gha,mode=max
          provenance: false
          sbom: false

      - name: image-size gate
        run: |
          # existing inline script — keep unchanged
          ...

      - name: install playwright
        run: |
          cd e2e
          npm ci
          npx playwright install --with-deps chromium

      - name: e2e (cron-fast)
        continue-on-error: true
        run: make e2e-cron-fast

      - name: idle-ram gate
        # ... existing inline script
      - name: portability gate
        continue-on-error: true
        env: { DEPLOY_PORTABILITY: "1" }
        run: |
          cd e2e
          npx playwright test deploy-portability.spec.ts --reporter=list
```

**Publish gating:** `.github/workflows/publish.yml` already runs in a separate workflow on push-to-main (decoupled in commit `b45730a`). It does NOT depend on ci.yml's success — that's intentional ("image availability shouldn't wait on go test"). Phase 9 (c) is consistent with this — both `tests` and `image-downstream` run in parallel; publish runs in parallel with both via the separate workflow. No `needs:` chain needed.

**If we WANTED publish to wait on both jobs:** add a third job `publish-gate` with `needs: [tests, image-downstream]` that just emits a status, then make publish.yml `workflow_run` on this workflow's completion. But that REVERSES the b45730a decoupling decision — not recommended without operator buy-in.

**Confidence:** HIGH — direct pattern from GitHub Actions docs (jobs.<id>.needs). [CITED: docs.github.com/actions/writing-workflows/choosing-what-your-workflow-does/control-the-concurrency-of-workflows-and-jobs]

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `os/exec` to `docker compose up -d --force-recreate <svc>` | In-process Stop+Remove+Create+Start via daemon API | Phase 9 (a) — 2026-05-16+ | Closes 4 bug classes (path resolution, COMPOSE_PROJECT_NAME, dynamic linker, compose_file_moved 412). Image shrinks ~20 MB. |
| Self-update blocked by CheckSelfProtection 409 | One-shot helper container (Watchtower-style) | Phase 9 (d) | Operators can update docker-update from the UI. Removes a footgun documented in Pitfall 6. |
| Single-job 7-8 min CI | 2-job parallel CI (~5-6 min wall) | Phase 9 (c) | Faster feedback loop. Tests and image build are independent surfaces. |
| `gcr.io/distroless/base-debian12:nonroot` (with dynamic linker for the bind-mounted docker CLI) | `gcr.io/distroless/static-debian12:nonroot` (no bind-mounted docker CLI needed) | Phase 9 (a) → reverts the 2026-05-15 hotfix | Image shrinks from ~22 MB to ~2 MB + binary (~8 MB) + UI (~1 MB) = ~11 MB total. Well under 30 MB budget. |
| Docker compose CLI bind-mounts in operator's `docker-compose.yml` | Bind-mounts deleted; docker.sock-only | Phase 9 (a) operator-side breaking change | Operators upgrading to Phase 9 must edit their installed compose file. Document in README upgrade note. |

**Deprecated / outdated by Phase 9:**

- `internal/compose/runner.go`'s body — DELETE; the file may stay as an empty placeholder for an interim while plans test in parallel, but by end of Wave 2 the file is gone and `compose.Runner` no longer exists in the Orchestrator constructor signature.
- The two `:ro` bind-mounts in `docker-compose.example.yml` lines 54–55 — DELETE.
- The Dockerfile comment block on lines 68–87 explaining the base-debian12 dynamic-linker fix — replace with a comment explaining the static-debian12 revert.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Engine API 1.54 (Engine v29) on the elevator-hmi accepts the create-then-NetworkConnect-extras pattern. | Pattern 1 | LOW — this pattern has worked since Engine 1.21 (2016). Verification: check `docker version` on 10.50.10.175 in the manual smoke. |
| A2 | `inspect.HostConfig.Binds` in moby/moby/client v0.4.1 always carries absolute host paths post-daemon-resolution. | Pattern 2 Translation Table | LOW — this is the documented invariant of `HostConfig.Binds`. Verification: TestRecreate_RelativeBindMount_ResolvesToOperatorHostPath. |
| A3 | The auto-generated short-ID network alias is filtered by compose AND should be filtered by us. | Pattern 2 Gotcha #4 | LOW-MEDIUM — based on docker/cli#1854 analogy. Verification: TestTranslate_FiltersShortIDAlias against a fixture inspect payload. |
| A4 | Watchtower's `--self-update-orchestrator` flag and `centurylinklabs.watchtower.ephemeral-orchestrator` label pattern are stable and well-understood. | Pattern 4 | LOW — direct citation from Watchtower's current docs. |
| A5 | `static-debian12:nonroot` ships `/etc/ssl/certs/ca-certificates.crt`. | Pitfall 5 | LOW — explicitly documented in GoogleContainerTools/distroless README. Verification: smoke test in CI. |
| A6 | The current `internal/api/dist/.gitkeep` is tracked in git (so the `tests` job's `//go:embed all:dist` parses without `npm run build`). | Pitfall 6 | LOW — verified this session via `ls internal/api/dist/`. The defensive `mkdir -p` in the tests job covers fresh-clone edge cases. |
| A7 | The elevator-hmi's `flutter` and `weston` compose services use a single network (no multi-network). | Pattern 1 + Pitfall 3 | LOW — observable from existing HANDOFF.md descriptions ("wayland-socket bind-mount", no mention of multi-network). Verification: pre-Phase-9 manual smoke step — run `docker inspect flutter` on 10.50.10.175 and confirm `len(NetworkSettings.Networks) == 1`. |
| A8 | Operators are willing to edit their installed `/home/centroid/docker-compose.yml` to remove the two CLI bind-mounts during the Phase 9 upgrade. | Runtime State Inventory | MEDIUM — this IS an operator-visible breaking change. Mitigation: a clear README upgrade note + the operator can keep the bind-mounts (they're ignored by docker-update post-Phase-9; they only add ~0 MB to the operator's surface) until they're comfortable removing them. |
| A9 | The 60s self-verify-timeout for the helper is sufficient. | Pattern 4 | LOW — docker-update boot is <3s on the elevator-hmi (observed in HANDOFF.md). 60s is 20x safety margin. Tunable via env. |

## Open Questions (RESOLVED)

1. **Should the helper container live alongside docker-update permanently as a 0-replica service, or be on-demand spawned from the parent?**
   - What we know: Watchtower ephemeral-self-update spawns on-demand. Compose doesn't natively support "0 replicas, spawn on signal."
   - What's unclear: Whether the helper needs ANY presence in `docker-compose.example.yml` (probably not — it's spawned via the daemon API, not via compose).
   - Recommendation: NO presence in `docker-compose.example.yml`. The helper image is the SAME image as docker-update (same image-pull, same digest); it's invoked from the parent via ContainerCreate + the `--self-update-orchestrator` flag. Document this in PROJECT.md "Self-update procedure."
   - **RESOLVED:** NO; daemon-spawned at runtime via `selfupdate.Spawn`. Verified by 09-04 acceptance criterion grep on `docker-compose.example.yml` (`! grep -E 'centroid\.docker-update\.helper|self-update-orchestrator' docker-compose.example.yml`).

2. **What happens if the operator clicks Self-Update twice in quick succession?**
   - What we know: The parent's spawn handler can capture in-flight-spawning state in a per-process bool (no need for the per-service mutex since this is a different code path).
   - What's unclear: How fast can the second click happen — race window between Spawn returning and SIGTERM arriving from the helper.
   - Recommendation: Add a `selfUpdate.inFlight atomic.Bool` to the SelfUpdater; second-click while in-flight returns 409 service_busy with a hint about waiting for the helper to exit. Surfaces as a UI toast "self-update already in progress."
   - **RESOLVED:** `selfUpdate.inFlight atomic.Bool` in 09-04 Task 1 `internal/selfupdate/spawn.go`; sentinel `ErrSelfUpdateInFlight` mapped to 409 with body `actionBodySelfUpdateBusy` in `internal/api/handlers_self.go`.

3. **Does Phase 9 (a) need to support `docker compose down/up` of multi-service stacks?**
   - What we know: docker-update only ever recreates ONE service at a time (per the Update / Rollback / Force-pull contract).
   - What's unclear: Whether any HMI compose file has inter-service dependencies that require a coordinated multi-service recreate (e.g. flutter depends on weston).
   - Recommendation: NO multi-service support in Phase 9. The single-service recreate is the documented contract. If a future operator requirement needs multi-service, that's a Phase 10 conversation.
   - **RESOLVED:** Out of scope. `recreate.Service(ctx, cli, svcName)` takes one `svcName`; single-service contract documented in 09-03 Task 1 Step 6 inline skeleton and its godoc comment.

4. **Should we keep `compose.Reader` (drift detection) at all after Phase 9 (a)?**
   - What we know: compose.Reader is invoked at the START of every action (412 ErrComposeFileMoved). Phase 9 (a) doesn't touch the compose file — but the operator might edit/rotate it manually, and a stale-inode situation still represents an operational discrepancy worth flagging.
   - What's unclear: Whether the 412 still serves a useful operator signal once docker-update no longer reads the compose file at action-time.
   - Recommendation: KEEP compose.Reader. The drift detection still surfaces a "compose file was edited mid-session" warning to operators, which is operationally useful. Compose.Runner goes; compose.Reader stays.
   - **RESOLVED:** YES; 09-03 Task 2 explicitly preserves `internal/compose/reader.go` (only `internal/compose/runner.go` body is deleted). Plan 09-02 Task 2 (A) `TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly` is the regression guard.

5. **What happens to in-progress non-self actions when the operator triggers Self-Update?**
   - What we know: The parent is being recreated; in-flight actions targeting OTHER services would be interrupted by the parent's SIGTERM.
   - What's unclear: Should the spawn-self-update endpoint check the per-service mutex map for in-flight actions and refuse with 409 if any are running?
   - Recommendation: YES — refuse the Spawn with `{"error":"actions_in_flight","detail":"wait for in-flight actions to complete"}` (409) if `len(o.locks) > 0` and any are currently held. Operators can retry once the actions finish. The check is cheap (single mutex acquisition) and prevents a real footgun (operator clicks Update on flutter, then clicks Self-Update, then docker-update SIGTERMS itself mid-flutter-update — flutter's state is unknown).
   - **RESOLVED:** YES; `actionsInFlightFn func() int` is injected into `selfupdate.NewSpawner` from the orchestrator's per-service mutex-map cardinality (09-04 Task 1 `spawn.go` + Task 2 wires `Orchestrator.ActionsInFlightFn()`). Sentinel `ErrActionsInFlight` mapped to 409 with body `actionBodyActionsInFlight` in `internal/api/handlers_self.go`.
## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Docker daemon API | All four locked items | ✓ on dev box | Daemon-resolved (Engine v29 on elevator-hmi) | — |
| `github.com/moby/moby/client` v0.4.1 | (a) recreate, (d) helper-side recreate | ✓ in go.mod | v0.4.1 pinned | — |
| `gcr.io/distroless/static-debian12:nonroot` | (a) base-image revert | ✓ (was the base pre-2026-05-15 hotfix) | latest from gcr.io | — |
| GitHub Actions ubuntu-24.04 runners | (c) | ✓ | latest | — |
| Engine API 1.40+ on target | (a) — minimum API for the methods used | Confirmed (Engine v29 = API 1.54 on elevator-hmi) | 1.54 | — |
| Operator willingness to edit installed compose file | (a) operator-side cleanup | Pending (assumption A8) | — | Operators can leave the two CLI bind-mounts in place; they become unused but harmless |

**Missing dependencies with no fallback:** None — every dependency for Phase 9 is already present in the codebase or on the target.

**Missing dependencies with fallback:** None.

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework (unit / table) | Go `testing` (stdlib); existing helpers in `internal/actions/`, `internal/docker/`, `internal/compose/` |
| Framework (e2e) | Playwright `@playwright/test` 1.60 with `docker compose up -d --wait` globalSetup |
| Config file | `go.mod` (root); `e2e/playwright.config.ts` |
| Quick run command | `go test ./... -race -run <TestName>` (single-test, sub-second on a watched subset) |
| Full suite command | `go test ./... -race` + `make e2e-cron-fast` |

### Phase Requirements → Test Map

Phase 9 has NO formal REQ-IDs (architectural hardening, incident-driven). The 7 Success Criteria in ROADMAP.md are the goal-backward anchors. Mapping each to its test:

| SC # | Behavior | Test Type | Automated Command | File Exists? |
|------|----------|-----------|-------------------|--------------|
| SC-1 | docker-update no longer invokes `docker compose` / `exec.Command` in `internal/actions/` | static check | `grep -r "exec\.Command\|docker compose\|compose up" internal/actions/ internal/recreate/ \| grep -v _test.go ; [ $? -ne 0 ]` (i.e. zero matches in production code) | Wave 0 — `make grep-no-compose` shell helper, runs in CI tests job |
| SC-2 (a) | Flutter/weston relative-path bind-mounts resolve to operator host paths | unit | `go test ./internal/recreate/ -run TestTranslate_HostConfig_Binds_AbsoluteAfterDaemonResolution -race -v` | Wave 0 — `internal/recreate/translate_test.go` |
| SC-2 (b) | Two services with `./relative-path` bind-mounts recover after a Phase-9-driven Update | e2e | `cd e2e && npx playwright test relative-bind-mount.spec.ts` | Wave 0 — `e2e/relative-bind-mount.spec.ts` |
| SC-3 (a) | Base image is static-debian12, not base-debian12 | static check | `grep '^FROM gcr.io/distroless/static-debian12:nonroot' Dockerfile` (exact match) | Wave 0 — CI tests job inline grep |
| SC-3 (b) | Final image size <12 MB | size gate | existing `image-size gate` script with tightened threshold | Existing — re-tune threshold in `.github/workflows/ci.yml` |
| SC-3 (c) | `docker-compose.example.yml` has no `/usr/bin/docker` or `/usr/libexec/docker/cli-plugins` lines | static check | `! grep -E '/usr/(bin/docker\|libexec/docker/cli-plugins)' docker-compose.example.yml` | Wave 0 — CI tests job inline grep |
| SC-4 (a) | `POST /api/self-update` returns 202 + helper-spawned body | unit | `go test ./internal/api/ -run TestHandleSelfUpdate_202_HelperSpawned -race` | Wave 0 — `internal/api/handlers_self_test.go` |
| SC-4 (b) | Self-update succeeds end-to-end (parent exits, helper drives recreate, new parent boots, /healthz=200) | e2e | `cd e2e && npx playwright test self-update.spec.ts` | Wave 0 — `e2e/self-update.spec.ts` |
| SC-5 | CI wall time on main ≤6 min | measurement | observed on `gh run list --workflow=ci.yml --branch main --limit 5` post-merge | Wave 0 — informal measurement; no test file. Recommend a CI status badge instead |
| SC-6 (i) | `compose_file_moved` 412 regression test passes RED on pre-9 codebase, GREEN post-9 | unit | `go test ./internal/actions/ -run TestUpdate_ComposeFileMoved_StillReturns412_PostSocketOnly -race` | Wave 0 — `internal/actions/orchestrator_test.go` add |
| SC-6 (ii) | `COMPOSE_PROJECT_NAME` collision regression test (was operator hot-fix on HMI; should be impossible post-9) | unit | `go test ./internal/recreate/ -run TestRecreate_NoComposeProjectNameEnvDependency -race` | Wave 0 — assert recreate.Service uses zero compose env vars |
| SC-6 (iii) | `./relative-path` regression test (RED pre-9, GREEN post-9) | unit + e2e | as in SC-2 above | covered |
| SC-6 (iv) | `CheckSelfProtection` 409 still applies to /api/containers/{svc}/update for self, AND new /api/self-update bypasses it | unit | `go test ./internal/api/ -run "TestHandleUpdate_DockerUpdateSvc_StillReturns409|TestHandleSelfUpdate_BypassesCheckSelfProtection" -race` | Wave 0 — `internal/api/handlers_self_test.go` |
| SC-7 | Manual smoke on elevator-hmi: full Update → verify → Rollback → verify cycle via UI, no terminal interaction | manual | manual entry in SMOKE.md | Existing template |

### Sampling Rate

- **Per task commit:** `go test ./internal/{recreate,docker,actions,api,selfupdate}/ -race -run "$(grep -oP 'func \K(Test\w+)' "${affected_test_files}" | paste -sd'|')"`
- **Per wave merge:** Full `go test ./... -race` + the wave's e2e specs (`make e2e-cron-fast` filtered).
- **Phase gate:** Full suite green + manual smoke checkpoint on elevator-hmi 10.50.10.175 before `/gsd-verify-work`.

### Wave 0 Gaps

- [ ] `internal/recreate/recreate_test.go` — atomic recreate sequence cases (Stop fails, Remove fails, Create fails, NetworkConnect fails, Start fails)
- [ ] `internal/recreate/translate_test.go` — 13+ field-translation cases (one per row in Translation Table)
- [ ] `internal/api/handlers_self_test.go` — 202 response, bypass CheckSelfProtection, 503 on unwired selfUpdater, 409 on actions_in_flight (per Open Question 5)
- [ ] `internal/selfupdate/spawn_test.go` — Spawn returns helperID; ContainerCreate args carry --self-update-orchestrator flag; AutoRemove=true; helper-label preset
- [ ] `internal/selfupdate/orchestrate_test.go` — orchestrator-mode main path verifies via /healthz polling; timeout exits non-zero
- [ ] `e2e/relative-bind-mount.spec.ts` — fixture two services with `./test-relative-mount` volumes; Update via API; assert new container's HostConfig.Binds[0] is absolute and matches operator host path
- [ ] `e2e/self-update.spec.ts` — fixture a docker-update container; POST /api/self-update; poll /healthz on new container; assert restart-count delta and image-tag change
- [ ] `e2e/compose.test.yml` — add a relative-bind-mount service fixture (mirroring flutter's wayland-socket pattern)
- [ ] CI `tests` job — `mkdir -p internal/api/dist` defensive step (belt-and-braces for fresh worktrees)
- [ ] Static-check scripts: `make grep-no-compose`, image-size threshold, `docker-compose.example.yml` no-CLI-mounts grep

**Framework install:** Already present (Go testing + Playwright). No new framework needed.

## Project Constraints (from CLAUDE.md)

| Constraint | Phase 9 compliance |
|------------|---------------------|
| **C1 — one container, one binary** | (a)+(b)+(c) trivially preserve. (d) introduces a SIDECAR helper but it's THE SAME IMAGE invoked with `--self-update-orchestrator` — one image, one binary, two entry-point modes. Preserved. |
| **C2 — file-based persistence only** | Phase 9 adds NO new state stores. State.json unchanged. Preserved. |
| **C3 — self-contained compose deployment** | (a) operator-side: removes two bind-mounts → COMPOSE FILE GETS SIMPLER. (d): no compose changes needed; helper spawned via daemon API. Preserved. |
| **C4 — TDD: verify → implement → verify → implement** | SC-6 explicitly requires RED-first regression tests for all four bug classes. The HANDOFF.md "TDD callout" section is the worked example of what happens when this is skipped. The plan MUST include the RED-first commit per failure class. |
| **Platform: amd64 only for v1** | (a)+(d) introduce no platform-specific assumptions. Preserved. |
| **Security: LAN-only, unauthenticated** | (d) introduces a new endpoint POST /api/self-update; same LAN-only / unauthenticated posture as existing endpoints. Preserved. |
| **Footprint: <30 MB image, <30 MB RAM idle** | (a) reverts ~20 MB → image lands at ~11 MB. Preserved (improved). |
| **Tech stack — `github.com/moby/moby/client` (NOT `github.com/docker/docker/client`)** | Phase 9 uses ONLY moby/moby/client. No new docker/docker imports. Preserved. |
| **Tech stack — `docker compose` via `os/exec` (NOT Compose Go SDK)** | Phase 9 REMOVES the os/exec path entirely. Does NOT add the Compose Go SDK. Preserved (improved). |
| **Backwards-compatible `hmi-update.*` label namespace** | (a) preserves all container labels including `hmi-update.*` via the translate table. Preserved. |

## Sources

### Primary (HIGH confidence)
- `internal/docker/_sdk_shape.txt` lines 132 (ContainerCreateOptions/Result), 150 (ContainerRemoveOptions/Result), 158 (ContainerStartOptions/Result), 717–751 (container.InspectResponse) — verbatim `go doc` capture of moby/moby/client v0.4.1
- `go doc github.com/moby/moby/client {ContainerCreate,ContainerRemove,ContainerStart,ContainerStop,ContainerWait,NetworkConnect,NetworkDisconnect}` — verified this session
- `go doc github.com/moby/moby/api/types/container {Config,HostConfig,RestartPolicy,RestartPolicyMode,NetworkSettings}` — verified this session
- `go doc github.com/moby/moby/api/types/network {NetworkingConfig,EndpointSettings}` — verified this session
- Existing source: `internal/actions/orchestrator.go` (Update/Rollback/ForcePull bodies + lookupContainerIDByService pattern)
- Existing source: `internal/actions/middleware.go` (CheckSelfProtection implementation)
- Existing source: `internal/docker/client.go`, `moby.go`, `discovery.go`
- Existing source: `internal/compose/runner.go` (the file Phase 9 (a) replaces)
- Existing source: `Dockerfile` (the file Phase 9 (a) reverts the base of)
- Existing source: `docker-compose.example.yml` (the file Phase 9 (a) trims)
- Existing source: `.github/workflows/ci.yml` (the file Phase 9 (c) splits)
- Existing source: `.github/workflows/publish.yml` (decoupled in b45730a — keep decoupled)
- [pkg.go.dev/embed](https://pkg.go.dev/embed) — `all:` prefix matches dotfiles and .gitkeep
- [GoogleContainerTools/distroless README](https://github.com/GoogleContainerTools/distroless) — static-debian12:nonroot ships CA certs + tzdata + nonroot UID
- [.planning/HANDOFF.md](.planning/HANDOFF.md) — bloat measurement, root cause of the four bug classes, TDD callout
- [.planning/ROADMAP.md Phase 9 section](.planning/ROADMAP.md) — Goal, Success Criteria, Locked Items, Locked Dependencies

### Secondary (MEDIUM-HIGH confidence)
- [Watchtower — Updating Watchtower (ephemeral-self-update)](https://watchtower.nickfedor.com/dev/getting-started/updating-watchtower/) — same-binary `--self-update-orchestrator` flag pattern, ephemeral-orchestrator label, sequence (Stop→Create→Start→Verify→Remove-old)
- [Watchtower configuration arguments](https://watchtower.nickfedor.com/dev/configuration/arguments/) — flag definition
- [moby/moby PR #45906](https://github.com/moby/moby/pull/45906) — multi-endpoint NetworkingConfig in Create for API ≥ 1.44
- [GitHub Actions: `jobs.<job_id>.needs` and parallel jobs](https://docs.github.com/actions/writing-workflows/choosing-what-your-workflow-does/control-the-concurrency-of-workflows-and-jobs)
- [golang/go#43854](https://github.com/golang/go/issues/43854) — `all:` prefix support for `//go:embed`

### Tertiary (LOW confidence — flagged)
- [moby/moby issue #44613](https://github.com/moby/moby/issues/44613) — silent multi-network rejection on older API (used to justify the safer create+connect-extras pattern; LOW because we can't easily verify against current Engine v29 except via the manual smoke)

## Metadata

**Confidence breakdown:**
- Standard stack — HIGH (zero new dependencies; everything is wraps over existing moby/moby/client)
- Architecture / Pattern 1 (recreate) — HIGH (sequence verified against SDK docs; failure-mode catalog derived from documented atomicity guarantees)
- Architecture / Pattern 2 (translation table) — HIGH on 12 of 13 mapped fields; MEDIUM-HIGH on alias filter (gotcha #4) — recommend the plan adds an explicit test for it
- Architecture / Pattern 4 (self-update) — HIGH on the flow (direct citation from Watchtower); MEDIUM on the 60s timeout (heuristic, env-tunable)
- Architecture / Pattern 5 (handler routing) — HIGH (conventional 202 Accepted with new endpoint)
- CI 2-job split — HIGH (standard GitHub Actions pattern)
- Pitfalls — HIGH on 1-3, 5-7; MEDIUM on Pitfall 4 (AutoRemove tradeoff is a design decision, not a verified fact)
- Validation Architecture — HIGH (test files map directly to success criteria; framework already in place)

**Research date:** 2026-05-16
**Valid until:** 30 days (stable surface — moby/moby/client v0.4.1 and Engine API 1.54 are not actively churning; the project is mid-implementation so internal patterns may shift slightly but the external surface is locked)
