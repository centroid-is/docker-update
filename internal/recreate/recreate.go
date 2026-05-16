// Package recreate (continued). recreate.go composes the
// Stop → Remove → Create → NetworkConnect → Start sequence around the
// pure-function Translate (see translate.go).
//
// recreate.Service is the ONE primitive that replaces the deleted
// compose.Runner.UpdateService. internal/actions/orchestrator.go's
// Update / Rollback / ForcePull all delegate to this function instead
// of shelling out to `docker compose -f ... up -d --force-recreate <svc>`.
package recreate

import (
	"context"
	"fmt"
	"strings"

	"github.com/centroid-is/docker-update/internal/docker"
)

// composeServiceLabel is the canonical compose-assigned label whose value
// identifies the service the daemon attached this container to. Same
// constant the orchestrator's lookupContainerIDByService uses; mirrored
// here so recreate.Service does not import internal/actions (would
// create an import cycle since actions imports recreate).
const composeServiceLabel = "com.docker.compose.service"

// stopGraceSeconds matches compose's default stop_grace_period: SIGTERM
// is sent, the daemon waits this many seconds, then SIGKILL fires.
// recreate.Service uses this for the Stop step so operators see the
// same shutdown behavior they had under the compose-CLI path.
const stopGraceSeconds = 10

// Service performs a single-container recreate via the daemon socket:
//
//	1. ContainerList(label=com.docker.compose.service=<svc>, All=true)
//	   → resolve the OLD container id (newest-by-Created if multiple)
//	2. ContainerInspect(oldID) → full descriptor
//	3. Translate(inspect) → Create-side Config / HostConfig / NetworkingConfig + extras
//	4. ContainerStop(oldID, timeout=10s) — SIGTERM grace
//	5. ContainerRemove(oldID, Force=true) — old container is GONE after this
//	6. ContainerCreate(name=inspect.Name) — failure here is unrecoverable; error message contains "old GONE"
//	7. for each extra network: NetworkConnect(newID, network) — on fail, ContainerRemove(newID) + return
//	8. ContainerStart(newID) — on fail, ContainerRemove(newID) + return
//
// Returns the new container ID on success. The failure-mode catalog is
// canonical at 09-RESEARCH.md § Architecture Patterns / Pattern 3:
//
//	Stop fails           → return err, OLD untouched
//	Remove fails         → return err, OLD stopped (operator can investigate)
//	Create fails         → return err with "old GONE" marker; orchestrator surfaces
//	                       action.recreate_failed (state.previous_digest still
//	                       lets the operator Rollback to the OLD image)
//	NetworkConnect fails → best-effort ContainerRemove(newID, Force) cleanup; return err
//	Start fails          → same best-effort cleanup; return err
//
// Single-service contract per RESEARCH.md Open Question 3 (RESOLVED):
// this function recreates exactly ONE service per call; multi-service
// coordination is out of scope for Phase 9. The lookup uses the SAME
// container.ListOptions / Filters shape as the existing
// actions/orchestrator.go::lookupContainerIDByService and the SAME
// most-recently-created tie-break — there is exactly one canonical
// pattern in the codebase for "find the container backing this
// compose service" and recreate.Service mirrors it.
func Service(ctx context.Context, cli docker.Client, svcName string) (string, error) {
	// Step 1: lookup OLD container by compose-service label.
	list, err := cli.ContainerList(ctx, docker.ContainerListOptions{
		// All=true so a briefly-exited container is still visible — without
		// it, an exited container is filtered out and the lookup falsely
		// reports "no container", surfacing as a misleading error.
		All: true,
		Filters: docker.Filters{
			"label": {composeServiceLabel + "=" + svcName: true},
		},
	})
	if err != nil {
		return "", fmt.Errorf("recreate.Service: list %q: %w", svcName, err)
	}
	if len(list) == 0 {
		return "", fmt.Errorf("recreate.Service: no container for service %q", svcName)
	}
	// Most-recently-created wins (defensive against an in-flight prior
	// recreate that left a dying container behind). Same heuristic as
	// orchestrator.go::lookupContainerIDByService: pick max(Created),
	// tie-break by first occurrence (stable iteration on the SDK slice).
	newest := list[0]
	for _, c := range list[1:] {
		if c.Created > newest.Created {
			newest = c
		}
	}
	oldID := newest.ID

	// Step 2: full descriptor for the OLD container.
	inspect, err := cli.ContainerInspect(ctx, oldID)
	if err != nil {
		return "", fmt.Errorf("recreate.Service: inspect %s: %w", oldID, err)
	}

	// Step 3: pure-function translation. Translate returns NEW pointers,
	// so any subsequent mutation (none, in this function) does not race
	// with the daemon's returned values.
	cfg, hostCfg, netCfg, extraNets := Translate(inspect.Container)
	// inspect.Container.Name has a leading "/" on every response (daemon
	// convention since Docker 1.0); ContainerCreate's Name field expects
	// the bare name. TrimPrefix is a no-op if the leading "/" is absent.
	name := strings.TrimPrefix(inspect.Container.Name, "/")

	// Step 4: Stop with a 10s SIGTERM grace (matches compose's default
	// stop_grace_period). The timeout is a *int because the SDK's
	// ContainerStopOptions distinguishes "no timeout" (nil → engine
	// default 10s), "-1" (wait indefinitely), "0" (SIGKILL immediately)
	// and "N" (wait N seconds then SIGKILL). We pass 10 explicitly so
	// the behavior is independent of the engine's default.
	stopTimeout := stopGraceSeconds
	if err := cli.ContainerStop(ctx, oldID, docker.ContainerStopOptions{Timeout: &stopTimeout}); err != nil {
		return "", fmt.Errorf("recreate.Service: stop %s: %w", oldID, err)
	}

	// Step 5: Remove. Force=true so a stopped-but-still-attached container
	// does not block. RemoveVolumes=false because compose-managed volumes
	// belong to the compose project, not to this individual container.
	if err := cli.ContainerRemove(ctx, oldID, docker.ContainerRemoveOptions{Force: true}); err != nil {
		return "", fmt.Errorf("recreate.Service: remove %s: %w", oldID, err)
	}

	// Step 6: Create — CRITICAL UNRECOVERABLE BOUNDARY. The OLD container
	// is now GONE; if Create fails, there is no container running for
	// this service until the operator manually runs `docker compose up
	// -d <svc>` (or until the orchestrator's existing state.previous_digest
	// path lets them /api/rollback to the prior image).
	//
	// The error message MUST contain the literal "old GONE" so
	// (a) the orchestrator's slog event includes the loud marker,
	// (b) the failure-mode regression test
	//     TestService_CreateFails_OldGone_NoLeak can assert on it.
	res, err := cli.ContainerCreate(ctx, docker.ContainerCreateOptions{
		Config:           cfg,
		HostConfig:       hostCfg,
		NetworkingConfig: netCfg,
		Name:             name,
	})
	if err != nil {
		return "", fmt.Errorf("recreate.Service: create %s (old GONE): %w", name, err)
	}

	// Step 7: connect extra networks. On failure, best-effort cleanup
	// of the NEW container (ContainerRemove with Force; tolerate cleanup
	// errors — the operator can `docker compose up -d <svc>` to recover
	// either way). NetworkConnectOptions in moby v0.4.1 uses
	// `EndpointConfig` (not EndpointSettings) as the field name.
	for netName, eps := range extraNets {
		if err := cli.NetworkConnect(ctx, netName, docker.NetworkConnectOptions{
			Container:      res.ID,
			EndpointConfig: eps,
		}); err != nil {
			_ = cli.ContainerRemove(ctx, res.ID, docker.ContainerRemoveOptions{Force: true})
			return "", fmt.Errorf("recreate.Service: connect %s to %s: %w", res.ID, netName, err)
		}
	}

	// Step 8: Start. Same best-effort cleanup pattern on failure.
	if err := cli.ContainerStart(ctx, res.ID, docker.ContainerStartOptions{}); err != nil {
		_ = cli.ContainerRemove(ctx, res.ID, docker.ContainerRemoveOptions{Force: true})
		return "", fmt.Errorf("recreate.Service: start %s: %w", res.ID, err)
	}
	return res.ID, nil
}
