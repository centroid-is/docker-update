// Package selfupdate (continued). orchestrate.go is the helper-side
// entry point invoked by cmd/docker-update/main.go when the binary is
// launched with the --self-update-orchestrator flag.
//
// Lifecycle per 09-RESEARCH.md § Architecture Patterns / Pattern 4:
//
//  1. Wait `delay` (default 1s, env DOCKER_UPDATE_SELF_UPDATE_DELAY) so
//     the parent has a clean window to write the HTTP 202 response back
//     to the operator before the recreate disconnects.
//  2. Call recreate.Service(ctx, cli, target) — the SAME socket-only
//     recreate primitive Plan 09-03 wired into actions/orchestrator.go
//     for Update + Rollback. Identical Stop → Remove → Create →
//     NetworkConnect → Start sequence; no special-casing for self-update
//     beyond the helper-spawned-by-parent provenance.
//  3. Poll the NEW parent's /healthz endpoint until 200 OR until
//     `verifyTimeout` elapses (default 60s, env
//     DOCKER_UPDATE_SELF_VERIFY_TIMEOUT). Tick period 2s — gives a 30-
//     attempt budget at the default timeout, well past the typical
//     parent boot time of ~2s.
//  4. Exit 0 on success (AutoRemove GCs the helper); exit 1 on any
//     failure (helper logs preserved when KEEP_HELPER=true).
package selfupdate

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/centroid-is/docker-update/internal/docker"
	"github.com/centroid-is/docker-update/internal/recreate"
)

// healthzPollTick is the period between /healthz polls in step 3. 2s is
// the canonical interval — fast enough that a quick boot is observed
// within one tick; slow enough that we don't hammer the new parent
// during its own startup. Tunable only by re-compiling; tests inject a
// shorter interval via the verifyTimeout parameter (a 200ms timeout
// effectively gates the loop to a single tick).
const healthzPollTick = 2 * time.Second

// healthzClientTimeout is the per-HTTP-request timeout for the /healthz
// poll. Shorter than healthzPollTick so a wedged new parent doesn't
// block subsequent ticks.
const healthzClientTimeout = 3 * time.Second

// Orchestrate is the helper-side main flow. Returns nil on a successful
// recreate + verify; returns an error (with context) on any failure.
// main.go's --self-update-orchestrator branch maps a non-nil error to
// os.Exit(1).
//
// Arguments:
//
//   - ctx          : a cancellable context (helper main.go passes
//     context.Background since the helper has no other concerns);
//     used only for cooperative cancellation in the poll loop.
//   - cli          : a docker.Client. The helper opens its own client
//     via docker.NewClient (same /var/run/docker.sock the parent uses,
//     bind-mounted by Spawn).
//   - target       : the compose-service name of the parent (typically
//     "docker-update"; matches the parent's DOCKER_UPDATE_SELF_SERVICE).
//     Passed verbatim to recreate.Service.
//   - healthzURL   : the URL the helper polls to verify the new parent
//     came up. Production main.go builds this as
//     "http://<target>:8080/healthz" (the target's compose-service name
//     resolves via docker DNS on the project network — same name the
//     operator would use from another container in the stack).
//   - delay        : pre-recreate sleep so the parent can flush its 202
//     response. Default 1s; env-tunable.
//   - verifyTimeout: deadline for the /healthz poll loop. Default 60s;
//     env-tunable. RESEARCH.md Runtime State Inventory pegs this as
//     "60s = 20x safety margin over typical 3s boot".
func Orchestrate(
	ctx context.Context,
	cli docker.Client,
	target string,
	healthzURL string,
	delay time.Duration,
	verifyTimeout time.Duration,
) error {
	slog.Info("self_update.orchestrate.start",
		"target", target,
		"healthz_url", healthzURL,
		"delay", delay.String(),
		"verify_timeout", verifyTimeout.String(),
	)

	// Step 1: wait `delay` so the parent flushes its 202 response.
	// Cooperative cancellation: a parent-side ctx cancel during the
	// delay aborts the helper before it touches anything.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
	}

	// Step 1.5 (9-04-G fix): pull the parent's image ref BEFORE recreate.
	// Without this, recreate.Service uses Docker's locally-cached image
	// for the parent's tag (e.g. ":latest"). The daemon does NOT
	// re-resolve the tag against the registry at ContainerCreate time, so
	// self-update would only ever "restart with cached image" — never
	// "upgrade to the registry's current image." The operator's expected
	// workflow (push new image to ghcr → POST /api/self-update) would
	// silently no-op until they did a manual `docker pull` first.
	//
	// We inspect the parent NOW (not at Spawn time) so the helper observes
	// the parent's current ref directly; the parent's image ref hasn't
	// changed since Spawn, but reading it here keeps Orchestrate's
	// dependencies minimal (no extra parameter on the signature).
	preInspect, err := cli.ContainerInspect(ctx, target)
	if err != nil {
		slog.Error("self_update.orchestrate.pre_inspect_failed",
			"target", target,
			"err", err,
		)
		return fmt.Errorf("selfupdate.Orchestrate: pre-recreate inspect %s: %w", target, err)
	}
	imageRef := ""
	if preInspect.Container.Config != nil {
		imageRef = preInspect.Container.Config.Image
	}
	if imageRef == "" {
		slog.Error("self_update.orchestrate.no_image_ref",
			"target", target,
			"hint", "parent container has no Config.Image — cannot pull a fresh manifest",
		)
		return fmt.Errorf("selfupdate.Orchestrate: parent %s has no Config.Image", target)
	}
	slog.Info("self_update.orchestrate.pull_start",
		"target", target,
		"image_ref", imageRef,
	)
	pullResp, err := cli.ImagePull(ctx, imageRef, docker.ImagePullOptions{})
	if err != nil {
		slog.Error("self_update.orchestrate.pull_failed",
			"target", target,
			"image_ref", imageRef,
			"err", err,
		)
		return fmt.Errorf("selfupdate.Orchestrate: pull %s: %w", imageRef, err)
	}
	// Drain the pull stream so the daemon finishes the transfer before
	// ContainerCreate. Discarding the body is sufficient — successful
	// pulls always close the stream cleanly; mid-pull errors surface
	// via ContainerCreate's "image not found" failure path which the
	// recreate sequence handles.
	if _, err := io.Copy(io.Discard, pullResp); err != nil {
		_ = pullResp.Close()
		slog.Error("self_update.orchestrate.pull_drain_failed",
			"target", target,
			"image_ref", imageRef,
			"err", err,
		)
		return fmt.Errorf("selfupdate.Orchestrate: drain pull %s: %w", imageRef, err)
	}
	_ = pullResp.Close()
	slog.Info("self_update.orchestrate.pull_done",
		"target", target,
		"image_ref", imageRef,
	)

	// Step 2: call recreate.Service — same primitive as Update/Rollback.
	// This is the moment the parent dies; from here on we're talking to
	// the NEW container's stack.
	newID, err := recreate.Service(ctx, cli, target)
	if err != nil {
		slog.Error("self_update.orchestrate.recreate_failed",
			"target", target,
			"err", err,
		)
		return fmt.Errorf("selfupdate.Orchestrate: recreate %s: %w", target, err)
	}
	slog.Info("self_update.orchestrate.recreate_done",
		"target", target,
		"new_id", newID,
	)

	// Step 3: poll /healthz until 200 or deadline.
	client := &http.Client{Timeout: healthzClientTimeout}
	deadline := time.Now().Add(verifyTimeout)
	tick := time.NewTicker(healthzPollTick)
	defer tick.Stop()

	// First attempt without waiting for the initial tick — the new
	// parent may already be up if it booted fast.
	if ok := pollHealthzOnce(ctx, client, healthzURL); ok {
		slog.Info("self_update.orchestrate.verify_ok",
			"new_id", newID,
			"attempt", 1,
		)
		return nil
	}

	attempt := 1
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		attempt++
		if ok := pollHealthzOnce(ctx, client, healthzURL); ok {
			slog.Info("self_update.orchestrate.verify_ok",
				"new_id", newID,
				"attempt", attempt,
			)
			return nil
		}
	}

	slog.Error("self_update.orchestrate.verify_timeout",
		"new_id", newID,
		"timeout", verifyTimeout.String(),
		"attempts", attempt,
	)
	return fmt.Errorf("selfupdate.Orchestrate: verify timeout after %v (%d attempts to %s)",
		verifyTimeout, attempt, healthzURL)
}

// pollHealthzOnce executes a single GET against healthzURL. Returns true
// when the response is exactly 200. Any network error, non-200, or read
// failure is treated as "not yet ready" and we keep polling. The function
// closes the response body unconditionally — leaking response bodies
// during a 30-attempt loop would exhaust the helper's small FD budget.
func pollHealthzOnce(ctx context.Context, client *http.Client, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		// Malformed URL is a programmer error; surface via slog so the
		// operator sees it but don't stop polling — the next attempt
		// will also fail and the verifyTimeout will short-circuit.
		slog.Warn("self_update.orchestrate.healthz.req_build_failed", "err", err)
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		// Connection refused / DNS not yet resolving for the new
		// container — normal during early boot. Don't log every miss;
		// a polluted log would dwarf the success event.
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
