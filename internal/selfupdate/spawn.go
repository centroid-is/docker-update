// Package selfupdate ships the Watchtower-style helper-container path for
// recreating the docker-update process itself without weakening the
// per-service CheckSelfProtection guard.
//
// Two halves:
//
//   - spawn.go (parent-side): Spawner builds + starts a one-shot helper
//     container via the daemon socket. The helper is the SAME docker-update
//     image (C1 preserved — one binary, one image) launched with the
//     --self-update-orchestrator flag.
//   - orchestrate.go (helper-side): Orchestrate is the helper's main-flow
//     entry point. Waits a configurable delay, calls recreate.Service to
//     recreate the parent, polls /healthz, exits.
//
// The new POST /api/self-update HTTP endpoint (internal/api/handlers_self.go)
// drives the parent side; the --self-update-orchestrator flag branch in
// cmd/docker-update/main.go drives the helper side.
//
// Rationale (09-RESEARCH.md § Architecture Patterns / Pattern 4 + Pattern 5):
//
//	The existing CheckSelfProtection middleware returns 409 self_protection
//	for POST /api/containers/docker-update/update because docker-update
//	cannot recreate itself in-process (it would commit suicide mid-recreate
//	— Pitfall 6 / ACT-09). The Watchtower pattern works around this by
//	spawning a one-shot helper container that lives just long enough to
//	drive the recreate via the docker socket and then exits. The new
//	/api/self-update route bypasses CheckSelfProtection because the helper
//	(not the parent) is the one calling recreate.Service; the per-service
//	endpoint STILL returns 409 — the bypass is route-scoped, not
//	middleware-removal.
//
// Per-process state (inFlight atomic.Bool) is acceptable per
// 09-RESEARCH.md Open Question 2 RESOLVED: operator double-clicking the
// Update button on the docker-update row hits the same process; the
// atomic.Bool short-circuits the second click to 409 before any helper
// container is spawned.
//
// Refusal when other actions are in flight (actionsInFlightFn) per
// 09-RESEARCH.md Open Question 5 RESOLVED: spawning the recreate-helper
// while another per-service action is mid-flight would race the per-service
// mutex held by that action. The helper would either be SIGKILL'd by the
// parent's recreate (data loss) or would race the action's verify loop.
// Refusing self-update with 409 actions_in_flight forces the operator to
// wait for the in-flight action to drain.
package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/centroid-is/docker-update/internal/docker"
	"github.com/moby/moby/api/types/container"
)

// Spawner is the parent-side surface consumed by internal/api's
// handleSelfUpdate handler. Spawn builds the helper container's
// ContainerCreateOptions, calls docker.Client.ContainerCreate +
// ContainerStart, and returns the helper id.
//
// Failure-mode contract (mapped by handleSelfUpdate to HTTP status):
//
//	ErrActionsInFlight     → 409 actions_in_flight        (RESEARCH.md OQ5)
//	ErrSelfUpdateInFlight  → 409 self_update_in_flight    (RESEARCH.md OQ2)
//	any other error        → 500 self_update_failed
//	(nil)                  → 202 helper_spawned + helperID
type Spawner interface {
	Spawn(ctx context.Context) (helperID string, err error)
}

// ErrSelfUpdateInFlight is returned when a self-update is already in
// progress (the inFlight atomic.Bool guard). Per RESEARCH.md Open Question
// 2 RESOLVED: a second concurrent Spawn call from the same process must
// fail fast rather than spawn a second helper that races the first.
var ErrSelfUpdateInFlight = errors.New("selfupdate: a self-update is already in flight")

// ErrActionsInFlight is returned when actionsInFlightFn reports a non-zero
// per-service action count. Per RESEARCH.md Open Question 5 RESOLVED:
// spawning the recreate-helper while another action is mid-flight would
// race the per-service mutex; refuse and let the operator wait.
var ErrActionsInFlight = errors.New("selfupdate: per-service actions in flight; wait for them to drain")

// HelperLabel is the daemon-side label the parent stamps onto the helper
// container at Create time. Exposed so operators (and tests) can grep
// `docker ps --filter label=centroid.docker-update.helper=true` to identify
// in-flight helpers. The label is the canonical post-mortem hook when
// DOCKER_UPDATE_SELF_UPDATE_KEEP_HELPER=true disables AutoRemove
// (RESEARCH.md Pitfall 4).
const HelperLabel = "centroid.docker-update.helper"

// HelperCmdFlag is the CLI flag the helper image is invoked with. The
// flag string is the contract between the parent (Spawn) and the helper
// (cmd/docker-update/main.go's flag.Bool branch) — both sides must agree
// on the exact spelling.
const HelperCmdFlag = "--self-update-orchestrator"

// spawner is the concrete Spawner. Fields are documented in NewSpawner.
type spawner struct {
	cli docker.Client
	// selfImage is the image reference the helper is launched from. Per
	// C1 (CLAUDE.md), this is the SAME docker-update image as the parent.
	// In v1, main.go resolves this via DOCKER_UPDATE_SELF_IMAGE env or
	// inspecting the parent's own container at boot. Recorded as a struct
	// field so the value is captured once at NewSpawner time and does not
	// drift if the env later changes.
	selfImage string
	// selfContainer is the compose-service name of the parent (the
	// "target" the helper recreates). Defaults to DOCKER_UPDATE_SELF_SERVICE
	// at boot. Passed via the --target flag to the helper.
	selfContainer string
	// actionsInFlightFn returns the count of per-service mutexes currently
	// held by the orchestrator. >0 means refuse with ErrActionsInFlight.
	// Captured as a closure (not the orchestrator type) to avoid an import
	// cycle: internal/actions imports internal/recreate which now lives in
	// the same dependency level as internal/selfupdate.
	actionsInFlightFn func() int
	// helperKeepAlive disables HostConfig.AutoRemove on the helper so
	// `docker logs <helper-id>` is available for post-mortem. Default
	// false. Operators set DOCKER_UPDATE_SELF_UPDATE_KEEP_HELPER=true to
	// enable. RESEARCH.md Pitfall 4.
	helperKeepAlive bool
	// inFlight is the process-local atomic guard against concurrent
	// Spawn calls. CAS(false → true) on entry; reset to false on every
	// failure path so a transient daemon error doesn't permanently
	// poison the endpoint. NOT reset on success because the helper
	// is about to recreate us — the success path's "reset" is the
	// SIGTERM that kills this process.
	inFlight atomic.Bool
}

// NewSpawner constructs a production Spawner. All five arguments are
// required:
//
//   - cli: the same docker.Client wired throughout the rest of the
//     binary. The Spawner uses ContainerCreate, ContainerStart, and
//     (on cleanup) ContainerRemove. No new methods are required on
//     the Client interface beyond what Plan 09-03 already extended.
//
//   - selfImage: the OCI image reference the helper is launched from
//     (e.g. "ghcr.io/centroid-is/docker-update:v1.2.3" or
//     "ghcr.io/centroid-is/docker-update@sha256:..."). Per C1 this MUST
//     be the same image as the running parent so the helper carries
//     the same code (it's the recreate target's NEW image, which the
//     parent has just become — or the parent's current image if the
//     operator is using a digest-pinned tag). cmd/docker-update/main.go
//     resolves this from DOCKER_UPDATE_SELF_IMAGE env (or, in a future
//     enhancement, by inspecting the parent's own container at boot).
//
//   - selfContainer: the compose-service name (e.g. "docker-update")
//     the helper passes to recreate.Service. Must equal the value of
//     DOCKER_UPDATE_SELF_SERVICE for CheckSelfProtection consistency.
//
//   - actionsInFlightFn: closure exposing the orchestrator's per-service
//     mutex-map cardinality. Wired in main.go via
//     actions.Orchestrator.ActionsInFlightFn() (added in this plan).
//     Pass a nil closure to opt out of the guard — primarily for tests;
//     production main.go always passes a real closure.
//
//   - keepHelper: when true, sets HostConfig.AutoRemove=false on the
//     helper so its logs are preserved after exit. Production default
//     false; operators flip via DOCKER_UPDATE_SELF_UPDATE_KEEP_HELPER=true.
func NewSpawner(
	cli docker.Client,
	selfImage, selfContainer string,
	actionsInFlightFn func() int,
	keepHelper bool,
) Spawner {
	return &spawner{
		cli:               cli,
		selfImage:         selfImage,
		selfContainer:     selfContainer,
		actionsInFlightFn: actionsInFlightFn,
		helperKeepAlive:   keepHelper,
	}
}

// Spawn is the parent-side entry point. Sequence per
// 09-RESEARCH.md § Architecture Patterns / Pattern 4:
//
//  1. If actionsInFlightFn returns > 0 → return ErrActionsInFlight
//     (RESEARCH.md Open Question 5 RESOLVED).
//  2. CAS inFlight false → true. If already true → return
//     ErrSelfUpdateInFlight (RESEARCH.md Open Question 2 RESOLVED).
//  3. Build ContainerCreateOptions for the helper:
//     - Config.Image  = selfImage (same as parent)
//     - Config.Cmd    = ["--self-update-orchestrator", "--target=<selfContainer>"]
//     (binary path is supplied by the image's Entrypoint=["/docker-update"];
//     including it in Cmd produces a positional argv[1] that defeats flag.Parse)
//     - Config.Labels = {centroid.docker-update.helper: "true"}
//     - HostConfig.AutoRemove = !helperKeepAlive
//     - HostConfig.Binds      = ["/var/run/docker.sock:/var/run/docker.sock"]
//     - Name = "" (let daemon assign; AutoRemove cleans up)
//  4. ContainerCreate → resolve helper id.
//  5. ContainerStart → returns 202 to the operator; the helper takes over.
//
// Note: inFlight is NOT reset on success. The success path means the
// helper is about to recreate this process — SIGTERM will fire shortly
// and the atomic resets via process death. inFlight IS reset on any
// failure path so a transient daemon error doesn't permanently poison
// the endpoint.
func (s *spawner) Spawn(ctx context.Context) (string, error) {
	// Step 1: refuse if any per-service action is in flight.
	if s.actionsInFlightFn != nil && s.actionsInFlightFn() > 0 {
		return "", ErrActionsInFlight
	}

	// Step 2: CAS inFlight false → true.
	if !s.inFlight.CompareAndSwap(false, true) {
		return "", ErrSelfUpdateInFlight
	}

	// Step 2.5: inspect the parent to inherit its User. The docker socket
	// on production HMIs is mode 0660 root:docker; the parent runs as
	// "65532:<docker-gid>" so it can talk to the daemon. The helper needs
	// the same UID:GID — without User inheritance it would run as
	// nonroot:nogroup (65532:65532) and hit "permission denied while trying
	// to connect to the docker API at unix:///var/run/docker.sock" the
	// moment it called ContainerList. HMI smoke 2026-05-16 defect 9-04-B.
	parentInspect, err := s.cli.ContainerInspect(ctx, s.selfContainer)
	if err != nil {
		s.inFlight.Store(false)
		return "", fmt.Errorf("selfupdate.Spawn: inspect parent %q: %w", s.selfContainer, err)
	}
	helperUser := ""
	if parentInspect.Container.Config != nil {
		helperUser = parentInspect.Container.Config.User
	}

	// Step 3: build helper ContainerCreateOptions.
	//
	// The image's Entrypoint=["/docker-update"] is preserved at runtime,
	// so Cmd MUST be flags only — including the binary name as Cmd[0]
	// produces argv ["/docker-update", "docker-update", "--self-update-orchestrator",
	// "--target=..."] where Go's flag.Parse() stops at the spurious "docker-update"
	// positional at argv[1], leaving both helper-mode flags at their defaults.
	// The helper then falls through to server-mode startup and dies.
	opts := docker.ContainerCreateOptions{
		Config: &container.Config{
			Image: s.selfImage,
			User:  helperUser, // inherited from parent (9-04-B)
			Cmd: []string{
				HelperCmdFlag,
				"--target=" + s.selfContainer,
			},
			Labels: map[string]string{
				HelperLabel: "true",
			},
		},
		HostConfig: &container.HostConfig{
			// AutoRemove=true (default) GCs the helper on exit so
			// successful self-updates leave no debris. Operators can
			// flip DOCKER_UPDATE_SELF_UPDATE_KEEP_HELPER=true to keep
			// the helper around for post-mortem (Pitfall 4).
			AutoRemove: !s.helperKeepAlive,
			// The helper needs the docker socket to drive the recreate.
			// Same bind-mount the parent already has — privilege parity
			// with the parent process (T-09-04-05 accept).
			Binds: []string{
				"/var/run/docker.sock:/var/run/docker.sock",
			},
		},
		// Empty Name → daemon assigns; AutoRemove cleans up.
		// Naming the helper would collide on a rapid second self-update
		// (rare, but observed in the 09-RESEARCH discussion).
		Name: "",
	}

	// Step 4: Create.
	res, err := s.cli.ContainerCreate(ctx, opts)
	if err != nil {
		// Reset inFlight so a transient daemon error (network blip,
		// daemon restart) doesn't permanently poison the endpoint —
		// the operator can retry.
		s.inFlight.Store(false)
		return "", fmt.Errorf("selfupdate.Spawn: create helper: %w", err)
	}

	// Step 5: Start. Best-effort cleanup on failure.
	if err := s.cli.ContainerStart(ctx, res.ID, docker.ContainerStartOptions{}); err != nil {
		// Best-effort: try to remove the just-created (but not started)
		// helper. Don't fail the user-visible error path on the cleanup
		// error — the operator's primary diagnostic is the original
		// start failure.
		_ = s.cli.ContainerRemove(ctx, res.ID, docker.ContainerRemoveOptions{Force: true})
		s.inFlight.Store(false)
		return "", fmt.Errorf("selfupdate.Spawn: start helper %s: %w", res.ID, err)
	}

	// Success — leave inFlight=true. The helper will SIGTERM this
	// process shortly; the atomic resets via process death.
	slog.Info("self_update.helper_spawned",
		"helper_id", res.ID,
		"target", s.selfContainer,
		"image", s.selfImage,
		"auto_remove", !s.helperKeepAlive,
	)
	return res.ID, nil
}
