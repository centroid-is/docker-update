// Package actions sequences the Update / Rollback / Force-pull workflows
// that the web UI exposes as per-row buttons. orchestrator.go is the
// THIRD producer of state mutations in the codebase: Phase 2's docker
// events goroutine is the first; Phase 3's cron poller is the second;
// Plan 04-03 lands this third producer. All three feed the single
// state-update channel defined in internal/poll/channel.go.
//
// Architectural anchor (mirror of internal/poll/poller.go's anti-
// deadlock invariant — see ARCHITECTURE.md lines 419-420):
//
//	actionOrchestrator NEVER calls state.Store.Update directly. Action
//	handlers compute their I/O (compose drift check, image pull, registry
//	digest verify, compose recreate, post-recreate inspect, verify loop)
//	OUTSIDE any state lock and send pure-map-mutation closures through
//	the existing channel. The single consumer goroutine (RunUpdater in
//	channel.go) is the only writer to state.Store — DETECT-10 invariant
//	carries forward verbatim.
//
// Per-D-Area-1 linear sequence (verbatim from CONTEXT.md Area 1 lines
// 32–46) the Update body implements:
//
//  1. composeReader.CheckUnchanged → 412 ErrComposeFileMoved
//  2. unlock, err := lockService(svc) → 409 ErrServiceBusy
//  3. defer unlock()
//  4. snapshot = store.Get().Containers[svc]; idempotency short-circuit
//     (current == upstream → NoOp:true, return 200)
//  5. send KindActionStart (ActionInFlight="updating", clear ActionError)
//  6. ImagePull → drainPullStream → aux digest (Option A path)
//  7. resolver.Digest cross-check (pulled == registry; Pitfall 1)
//  8. send KindActionProgress (Phase=pulled, NewDigest)
//  9. recreate.Service(ctx, dockerClient, svc); non-zero → action.recreate_failed
//     (Phase 9 (a): socket-only recreate replaced the deleted compose runner;
//     ErrComposeFailed is retained as the sentinel for backward-compat with
//     handlers_actions.writeActionError 500-mapping.)
//  10. post-recreate inspect + verifyAfterRecreate
//  11. send KindActionResult (success: swap digests, clear in-flight;
//      failure: ActionError = "<phase>_failed: <reason>")
//
// On any failure step 5–10 the orchestrator sends a failure
// KindActionResult BEFORE returning the wrapped error so the UI's
// per-row spinner state always converges to idle + action_error
// populated.
//
// drainPullStream (Option A — Assumption A1 path):
//
//	The moby SDK's ImagePullResponse exposes JSONMessages — a stream of
//	pull-progress objects each carrying an optional `aux` field. The
//	terminal message's aux carries the pulled digest in either an `ID`
//	or `Digest` field (sha256: prefixed). We decode the io.ReadCloser
//	returned by docker.Client.ImagePull as a stream of these messages
//	and extract the digest from the final aux. If Assumption A1 is
//	refuted (the A1 probe test surfaces a different shape), the plan
//	pivots to Option B — adding ImageInspect to the docker facade.
//	The A1 probe in probe_aux_digest_test.go currently t.Skips (no
//	daemon on dev box) so Option A remains the design lean per
//	RESEARCH.md A1 mitigation.
//
// Slog event schema (OBS-01, dotted convention — Pattern G):
//
//	action.start            (service, action)
//	action.phase            (service, action, phase, new_digest|...)
//	action.complete         (service, action, before, after, exit_code, duration_ms)
//	action.pull_failed      (service, err)
//	action.compose_failed   (service, err)            — compose.Reader-emitted (drift, 412 path)
//	action.recreate_failed  (service, err)            — recreate.Service-emitted (500 path; Phase 9 a)
//	action.verify_failed    (service, restart_count, running, err)
package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/centroid-is/docker-update/internal/docker"
	"github.com/centroid-is/docker-update/internal/poll"
	"github.com/centroid-is/docker-update/internal/recreate"
	"github.com/centroid-is/docker-update/internal/registry"
	"github.com/centroid-is/docker-update/internal/state"
)

// Orchestrator is the public interface Plan 04-04's internal/api server
// binds. Plan-04-04 wiring constructs one via NewOrchestrator and the
// HTTP handlers invoke Update / Rollback / ForcePull plus the three
// middleware-helper methods (LookupContainer, CheckSelfProtection,
// SelfService) before delegating to the action body.
//
// WR-04: the constructor returns this interface (not *actionOrchestrator)
// so callers cannot reach into the concrete struct's internals and so
// tests can substitute a fakeOrchestrator without import-cycle pain.
type Orchestrator interface {
	Update(ctx context.Context, service string) (ActionResult, error)
	Rollback(ctx context.Context, service string) (ActionResult, error)
	ForcePull(ctx context.Context, service string, recreate bool) (ActionResult, error)

	// Middleware-facing helpers (consumed by Plan 04-04 handlers BEFORE
	// invoking the action body — the middleware chain runs in this
	// order: ValidateServiceName → CheckSelfProtection → LookupContainer
	// → CheckSafetyLabel. CheckSelfProtection runs BEFORE LookupContainer
	// because docker-update is not in the watched-containers cache by
	// default).
	LookupContainer(svc string) (state.Container, bool)
	CheckSelfProtection(w http.ResponseWriter, svc string) bool
	SelfService() string
}

// ActionResult is the success payload returned to the HTTP handler. The
// handler emits a JSON object containing CurrentDigest + PreviousDigest
// + NoOp on the wire. ACT-11 requires both digests in every success
// response.
type ActionResult struct {
	CurrentDigest  string
	PreviousDigest string
	// NoOp is true when the action short-circuited due to idempotency
	// (ACT-06 Update with current==upstream, ACT-07 Rollback with
	// current==previous). The handler emits {"no_op": true, ...}.
	NoOp bool
}

// stateReader is the narrow seam the middleware needs from *state.Store.
// LookupContainer only reads; production passes *state.Store concretely.
// Tests inject a fake. Mirrors internal/poll/poller.go::storeReader
// pattern (lines 86-89).
type stateReader interface {
	Get() state.State
}

// dockerInspector is the narrow seam verifyAfterRecreate needs from
// docker.Client. Only ContainerInspect is required for the verify loop;
// scoping the interface narrowly means verify_test.go's fake doesn't have
// to stub Ping/ContainerList/Events/ImagePull/ImageTag.
type dockerInspector interface {
	ContainerInspect(ctx context.Context, id string) (docker.ContainerInspect, error)
}

// actionOrchestrator is the concrete Orchestrator implementation. The
// struct lives here (not mutex.go) because Task 3 (this plan) consolidates
// it with all the action body's dependencies; Task 1/2 originally placed
// it in mutex.go behind a sequence-of-tasks comment, but the final shape
// belongs here.
//
// Field roles:
//
//   - mu + locks: per-service mutex map (ACT-08). lockService in mutex.go
//     operates on these two fields.
//   - store: read-only snapshot via Get; LookupContainer + Update body
//     read the cached state. All writes go via the channel sender.
//   - dockerInspector: narrow seam for verify_test.go (one method); in
//     production this is the same value as dockerClient (a docker.Client
//     satisfies dockerInspector).
//   - dockerClient: full docker.Client surface (ImagePull, ImageTag,
//     ContainerInspect, ContainerCreate/Remove/Start/Stop/NetworkConnect).
//     Used by pull+tag action bodies, recreate.Service (Phase 9 a) for
//     the socket-only recreate, and indirectly via dockerInspector for
//     verify.
//   - resolver: registry.Resolver.Digest (Pitfall 1 cross-check).
//   - composeReader: composeUnchangedChecker.CheckUnchanged invoked
//     BEFORE lockService so a drifted compose file surfaces 412 without
//     holding the mutex. compose.Reader survives Phase 9; only
//     the compose-side recreate path went away (replaced by recreate.Service).
//   - sender: ctx-aware StateUpdate send wrapper. Production wraps the
//     channel; tests inject a recordingSender to observe every send.
//   - selfService: env-captured at NewOrchestrator (DOCKER_UPDATE_SELF_SERVICE,
//     default "docker-update"). CheckSelfProtection compares against this.
//   - verifyWindow / healthcheckWindow: tunable via env at boot; default
//     15s / 60s.
type actionOrchestrator struct {
	mu    sync.RWMutex
	locks map[string]*sync.Mutex

	store             stateReader
	dockerInspector   dockerInspector
	dockerClient      docker.Client
	resolver          registry.Resolver
	composeReader     composeUnchangedChecker
	sender            updateSender
	selfService       string
	verifyWindow      time.Duration
	healthcheckWindow time.Duration
}

// composeUnchangedChecker is the narrow seam the orchestrator needs from
// *compose.Reader. Production passes *compose.Reader concretely; tests
// pass a fake. Only CheckUnchanged is invoked; ComposePath() is read at
// boot for diagnostics by other consumers.
type composeUnchangedChecker interface {
	CheckUnchanged(ctx context.Context) error
}

// updateSender is the narrow seam — both production (chan<- poll.StateUpdate)
// and tests (recordingSender) satisfy this via the send method.
//
// We deliberately do NOT keep the channel as a chan field because Task 3's
// test code must observe sends without spawning a RunUpdater drain — the
// recordingSender below captures every StateUpdate in a slice for direct
// inspection.
type updateSender interface {
	send(ctx context.Context, u poll.StateUpdate)
}

// channelSender wraps a chan<- poll.StateUpdate so production code uses
// it transparently. Implements updateSender.
type channelSender struct {
	ch chan<- poll.StateUpdate
}

func (c *channelSender) send(ctx context.Context, u poll.StateUpdate) {
	select {
	case c.ch <- u:
	case <-ctx.Done():
	}
}

// NewOrchestrator constructs the production actionOrchestrator from its
// dependencies. Fail-fast on nil deps (cmd/docker-update/main.go is expected
// to wire all of them; nil indicates a wiring fault — better to surface
// at boot than at first action click).
//
// selfService defaults to "docker-update" if empty (matches the
// DOCKER_UPDATE_SELF_SERVICE env-var convention captured in CONTEXT.md
// Area 4). verifyWindow / healthcheckWindow default to 15s / 60s when
// zero.
//
// Phase 9 (a) signature change: the runner parameter is GONE.
// recreate.Service (via the existing docker.Client dependency) replaces
// it. cmd/docker-update/main.go is updated in lockstep — see boot
// order step 5.9 in main.go.
func NewOrchestrator(
	dockerClient docker.Client,
	resolver registry.Resolver,
	composeReader composeUnchangedChecker,
	store *state.Store,
	updates chan<- poll.StateUpdate,
	selfService string,
	verifyWindow time.Duration,
	healthcheckWindow time.Duration,
) (Orchestrator, error) {
	if dockerClient == nil {
		return nil, fmt.Errorf("actions.NewOrchestrator: nil docker.Client")
	}
	if resolver == nil {
		return nil, fmt.Errorf("actions.NewOrchestrator: nil registry.Resolver")
	}
	if store == nil {
		return nil, fmt.Errorf("actions.NewOrchestrator: nil *state.Store")
	}
	if updates == nil {
		return nil, fmt.Errorf("actions.NewOrchestrator: nil updates channel")
	}
	if selfService == "" {
		selfService = "docker-update"
	}
	if verifyWindow <= 0 {
		verifyWindow = defaultVerifyWindow
	}
	if healthcheckWindow <= 0 {
		healthcheckWindow = defaultHealthcheckWindow
	}
	return &actionOrchestrator{
		locks:             map[string]*sync.Mutex{},
		store:             store,
		dockerInspector:   dockerClient,
		dockerClient:      dockerClient,
		resolver:          resolver,
		composeReader:     composeReader,
		sender:            &channelSender{ch: updates},
		selfService:       selfService,
		verifyWindow:      verifyWindow,
		healthcheckWindow: healthcheckWindow,
	}, nil
}

// send forwards a StateUpdate through the configured updateSender,
// preserving the ctx-aware semantics (see channelSender.send). The
// indirection through the sender field lets tests capture every send
// without standing up a RunUpdater goroutine.
func (o *actionOrchestrator) send(ctx context.Context, u poll.StateUpdate) {
	o.sender.send(ctx, u)
}

// ----------------------------------------------------------------------------
// Update
// ----------------------------------------------------------------------------

// Update implements ACT-01/02/06/11. Follows the verbatim 11-step
// sequence from CONTEXT.md Area 1.
func (o *actionOrchestrator) Update(ctx context.Context, service string) (ActionResult, error) {
	start := time.Now()

	// Step 1: compose drift check BEFORE acquiring the mutex (a stale
	// inode means the recreate would target the wrong file; surface 412
	// to the operator without holding the lock).
	if o.composeReader != nil {
		if err := o.composeReader.CheckUnchanged(ctx); err != nil {
			return ActionResult{}, fmt.Errorf("actions.Update: %w", err)
		}
	}

	// Step 2: per-service mutex. 409 ErrServiceBusy on contention.
	unlock, err := o.lockService(service)
	if err != nil {
		return ActionResult{}, fmt.Errorf("actions.Update %s: %w", service, err)
	}
	// Step 3: defer unlock.
	defer unlock()

	// Step 4: snapshot + idempotency.
	snapshot, ok := o.store.Get().Containers[service]
	if !ok {
		// Defensive — middleware LookupContainer should have caught this.
		return ActionResult{}, fmt.Errorf("actions.Update: container %q not in state", service)
	}
	if snapshot.AvailableDigest != "" && snapshot.CurrentDigest == snapshot.AvailableDigest {
		slog.Info("action.complete",
			"service", service,
			"action", string(ActionUpdate),
			"before", snapshot.CurrentDigest,
			"after", snapshot.CurrentDigest,
			"exit_code", 0,
			"duration_ms", time.Since(start).Milliseconds(),
			"no_op", true,
		)
		return ActionResult{
			CurrentDigest:  snapshot.CurrentDigest,
			PreviousDigest: snapshot.PreviousDigest,
			NoOp:           true,
		}, nil
	}

	// From here on, failures send a KindActionResult with phase=failed
	// so the UI's per-row spinner state always converges to idle.
	slog.Info("action.start", "service", service, "action", string(ActionUpdate))
	o.send(ctx, poll.StateUpdate{
		Kind:    poll.KindActionStart,
		Service: service,
		Apply: func(s *state.State) {
			c, ok := s.Containers[service]
			if !ok {
				return
			}
			c.ActionInFlight = "updating"
			c.ActionError = ""
			s.Containers[service] = c
		},
	})

	// Step 6 + 7: pull + drain + digest cross-check.
	pulledDigest, err := o.pullAndVerifyDigest(ctx, snapshot.Image, snapshot.Tag)
	if err != nil {
		o.sendFailureResult(ctx, service, "pull", err)
		slog.Error("action.pull_failed", "service", service, "err", err)
		return ActionResult{}, fmt.Errorf("actions.Update %s: %w", service, err)
	}

	// Step 8: progress = pulled.
	o.send(ctx, poll.StateUpdate{
		Kind:    poll.KindActionProgress,
		Service: service,
		Apply: func(s *state.State) {
			// No state mutation on progress; reserved for future UI
			// breadcrumbs. Kept for observability symmetry with
			// start+result.
		},
	})
	slog.Info("action.phase",
		"service", service,
		"action", string(ActionUpdate),
		"phase", "pulled",
		"new_digest", pulledDigest)

	// Step 9: socket-only recreate via internal/recreate.Service
	// (Phase 9 (a)). Replaced the compose-CLI subprocess that shelled
	// out to `docker compose -f ... up -d --force-recreate <svc>`.
	// recreate.Service does Stop → Remove → Create → NetworkConnect → Start
	// via the daemon socket; failure modes per 09-RESEARCH.md Pattern 3.
	//
	// We map the failure to ErrComposeFailed (for backward-compat with
	// handlers_actions.writeActionError's 500 sentinel dispatch) but
	// emit a distinct slog event class — action.recreate_failed — so
	// operators can grep for the new failure surface vs. the legacy
	// compose-CLI surface (which no longer fires from this code path
	// but still might from compose.Reader-emitted drift errors).
	if _, err := recreate.Service(ctx, o.dockerClient, service); err != nil {
		wrapped := fmt.Errorf("%w: %w", ErrComposeFailed, err)
		o.sendFailureResult(ctx, service, "compose", wrapped)
		slog.Error("action.recreate_failed", "service", service, "err", err)
		return ActionResult{}, fmt.Errorf("actions.Update %s: %w", service, wrapped)
	}

	// Step 9.5 (BUG-7 fix): record the digest swap AS SOON AS recreate
	// returns nil. The container is now on the new image regardless
	// of whether the subsequent verify succeeds; recording PreviousDigest
	// here means an operator can /api/rollback even when verify fails
	// (e.g. the new image crashes on start). Pre-fix, verify_failed left
	// PreviousDigest empty and Rollback returned 400 no_previous_digest
	// exactly when the operator most needed it.
	oldDigest := snapshot.CurrentDigest
	newDigest := pulledDigest
	o.send(ctx, poll.StateUpdate{
		Kind:    poll.KindActionProgress,
		Service: service,
		Apply: func(s *state.State) {
			c, ok := s.Containers[service]
			if !ok {
				return
			}
			c.PreviousDigest = oldDigest
			c.CurrentDigest = newDigest
			c.UpdateAvailable = false
			s.Containers[service] = c
		},
	})

	// Step 10: post-recreate inspect + verify. State digests already
	// reflect the on-disk reality from Step 9.5, so verify_failed leaves
	// the swap intact and only adds ActionError.
	if err := o.inspectAndVerify(ctx, service, snapshot); err != nil {
		o.sendFailureResult(ctx, service, "verify", err)
		var detail *VerifyDetail
		if errors.As(err, &detail) {
			slog.Error("action.verify_failed",
				"service", service,
				"restart_count", detail.RestartCount,
				"running", detail.Running,
				"err", err)
		} else {
			slog.Error("action.verify_failed", "service", service, "err", err)
		}
		return ActionResult{}, fmt.Errorf("actions.Update %s: %w", service, err)
	}

	// Step 11: success — digests already swapped at Step 9.5; here we
	// only clear in-flight + error.
	o.send(ctx, poll.StateUpdate{
		Kind:    poll.KindActionResult,
		Service: service,
		Apply: func(s *state.State) {
			c, ok := s.Containers[service]
			if !ok {
				return
			}
			c.ActionInFlight = ""
			c.ActionError = ""
			s.Containers[service] = c
		},
	})
	slog.Info("action.complete",
		"service", service,
		"action", string(ActionUpdate),
		"before", oldDigest,
		"after", newDigest,
		"exit_code", 0,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return ActionResult{CurrentDigest: newDigest, PreviousDigest: oldDigest}, nil
}

// ----------------------------------------------------------------------------
// Rollback
// ----------------------------------------------------------------------------

// Rollback implements ACT-03/04/07/11. Same middleware/mutex/idempotency
// setup as Update but the body uses ImageTag (local, offline-capable)
// instead of ImagePull (ACT-04: offline rollback is the load-bearing
// differentiator from WUD).
//
// PreviousDigest=="" is a programmer/operator error class — the handler
// surfaces 400 no_previous_digest. Idempotency: CurrentDigest ==
// PreviousDigest → NoOp:true.
func (o *actionOrchestrator) Rollback(ctx context.Context, service string) (ActionResult, error) {
	start := time.Now()

	if o.composeReader != nil {
		if err := o.composeReader.CheckUnchanged(ctx); err != nil {
			return ActionResult{}, fmt.Errorf("actions.Rollback: %w", err)
		}
	}

	unlock, err := o.lockService(service)
	if err != nil {
		return ActionResult{}, fmt.Errorf("actions.Rollback %s: %w", service, err)
	}
	defer unlock()

	snapshot, ok := o.store.Get().Containers[service]
	if !ok {
		return ActionResult{}, fmt.Errorf("actions.Rollback: container %q not in state", service)
	}

	// BUG-7d fix: if state.PreviousDigest points to an image that no longer
	// exists in the local daemon cache (e.g. it was pruned, or compose's
	// recreate flow cleared its tag binding), ImageTag will fail later with
	// an opaque "no such image" error. Detect that here and reset to the
	// empty case so the fallback path can try to find an alternative target.
	// The 2026-05-16 production sequence that surfaced this:
	//   1. flutter on broken b64c35a5; state.previous_digest=""
	//   2. Rollback #1: fallback finds 18136d85, swaps state to
	//      current=18136d85, previous=b64c35a5
	//   3. Operator clicks Rollback again hoping to recover further
	//   4. Orchestrator tries ImageTag(image@b64c35a5 → :latest) but the
	//      daemon has GC'd that image already → pull_failed (misleading)
	if snapshot.PreviousDigest != "" {
		ref := snapshot.Image + "@" + snapshot.PreviousDigest
		if _, err := o.dockerClient.ImageInspect(ctx, ref); err != nil {
			slog.Warn("rollback.previous_digest.missing",
				"service", service,
				"image", snapshot.Image,
				"missing_digest", snapshot.PreviousDigest,
				"err", err,
				"reason", "state.previous_digest points to an image no longer present locally; falling through to local-cache fallback (BUG-7d)")
			snapshot.PreviousDigest = ""
		}
	}

	if snapshot.PreviousDigest == "" {
		// BUG-7c fix: state.PreviousDigest is empty OR pointed at a
		// missing image (BUG-7d). Try the local image cache as a
		// fallback — the docker daemon retains previously-pulled-but-now-
		// untagged images, and the most recent one matching this repo is
		// the natural rollback target ("undo the last pull"). If the
		// daemon has nothing, fall through to the original
		// ErrNoPreviousDigest.
		fallback, ferr := o.findFallbackRollbackTarget(ctx, snapshot.Image, snapshot.CurrentDigest)
		if ferr != nil {
			slog.Warn("rollback.fallback.lookup_failed",
				"service", service,
				"image", snapshot.Image,
				"err", ferr)
		}
		if fallback == "" {
			// WARNING-02 fix: wrap the dedicated sentinel rather than emitting
			// a bare string. handlers_actions.writeActionError uses errors.Is
			// to dispatch (no substring scan).
			return ActionResult{}, fmt.Errorf("actions.Rollback %s: %w", service, ErrNoPreviousDigest)
		}
		slog.Info("rollback.fallback.used",
			"service", service,
			"image", snapshot.Image,
			"target", fallback,
			"reason", "state.previous_digest empty or missing locally; using most-recent non-current local image of same repo")
		snapshot.PreviousDigest = fallback
	}
	if snapshot.CurrentDigest == snapshot.PreviousDigest {
		slog.Info("action.complete",
			"service", service,
			"action", string(ActionRollback),
			"before", snapshot.CurrentDigest,
			"after", snapshot.CurrentDigest,
			"exit_code", 0,
			"duration_ms", time.Since(start).Milliseconds(),
			"no_op", true,
		)
		return ActionResult{
			CurrentDigest:  snapshot.CurrentDigest,
			PreviousDigest: snapshot.PreviousDigest,
			NoOp:           true,
		}, nil
	}

	slog.Info("action.start", "service", service, "action", string(ActionRollback))
	o.send(ctx, poll.StateUpdate{
		Kind:    poll.KindActionStart,
		Service: service,
		Apply: func(s *state.State) {
			c, ok := s.Containers[service]
			if !ok {
				return
			}
			c.ActionInFlight = "rolling_back"
			c.ActionError = ""
			s.Containers[service] = c
		},
	})

	// Local re-tag — image@previous_digest → image:tag. Offline-capable
	// (no resolver call); ACT-04 e2e detaches the registry network to
	// pin this contract.
	src := snapshot.Image + "@" + snapshot.PreviousDigest
	dst := snapshot.Image + ":" + snapshot.Tag
	if snapshot.Tag == "" {
		dst = snapshot.Image + ":latest"
	}
	if err := o.dockerClient.ImageTag(ctx, src, dst); err != nil {
		wrapped := fmt.Errorf("%w: %w", ErrPullFailed, err)
		o.sendFailureResult(ctx, service, "pull", wrapped)
		slog.Error("action.pull_failed", "service", service, "err", err, "stage", "image_tag")
		return ActionResult{}, fmt.Errorf("actions.Rollback %s: %w", service, wrapped)
	}

	o.send(ctx, poll.StateUpdate{
		Kind:    poll.KindActionProgress,
		Service: service,
		Apply:   func(s *state.State) {},
	})
	slog.Info("action.phase",
		"service", service,
		"action", string(ActionRollback),
		"phase", "retagged",
		"target_digest", snapshot.PreviousDigest)

	// Phase 9 (a): socket-only recreate. Same change as Update step 9.
	if _, err := recreate.Service(ctx, o.dockerClient, service); err != nil {
		wrapped := fmt.Errorf("%w: %w", ErrComposeFailed, err)
		o.sendFailureResult(ctx, service, "compose", wrapped)
		slog.Error("action.recreate_failed", "service", service, "err", err)
		return ActionResult{}, fmt.Errorf("actions.Rollback %s: %w", service, wrapped)
	}

	// BUG-7 fix (Rollback symmetric to Update): record the swap AS SOON
	// AS recreate returns nil. State now reflects the on-disk reality;
	// a subsequent verify_failed leaves the swap intact and only adds
	// ActionError. Single-slot toggle per PROJECT.md F3. UpdateAvailable
	// re-flips to true because the upstream :latest is unchanged.
	oldCurrent := snapshot.CurrentDigest
	newCurrent := snapshot.PreviousDigest
	o.send(ctx, poll.StateUpdate{
		Kind:    poll.KindActionProgress,
		Service: service,
		Apply: func(s *state.State) {
			c, ok := s.Containers[service]
			if !ok {
				return
			}
			c.PreviousDigest = oldCurrent
			c.CurrentDigest = newCurrent
			if c.AvailableDigest != "" && c.CurrentDigest != c.AvailableDigest {
				c.UpdateAvailable = true
			}
			s.Containers[service] = c
		},
	})

	if err := o.inspectAndVerify(ctx, service, snapshot); err != nil {
		o.sendFailureResult(ctx, service, "verify", err)
		var detail *VerifyDetail
		if errors.As(err, &detail) {
			slog.Error("action.verify_failed",
				"service", service,
				"restart_count", detail.RestartCount,
				"running", detail.Running,
				"err", err)
		} else {
			slog.Error("action.verify_failed", "service", service, "err", err)
		}
		return ActionResult{}, fmt.Errorf("actions.Rollback %s: %w", service, err)
	}

	// Verify succeeded — digests already swapped above, here we only
	// clear in-flight + error.
	o.send(ctx, poll.StateUpdate{
		Kind:    poll.KindActionResult,
		Service: service,
		Apply: func(s *state.State) {
			c, ok := s.Containers[service]
			if !ok {
				return
			}
			c.ActionInFlight = ""
			c.ActionError = ""
			s.Containers[service] = c
		},
	})
	slog.Info("action.complete",
		"service", service,
		"action", string(ActionRollback),
		"before", oldCurrent,
		"after", newCurrent,
		"exit_code", 0,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return ActionResult{CurrentDigest: newCurrent, PreviousDigest: oldCurrent}, nil
}

// ----------------------------------------------------------------------------
// ForcePull
// ----------------------------------------------------------------------------

// ForcePull implements ACT-05. With recreate=false (default), just pulls
// the image and returns immediately — no compose call, no verify. With
// recreate=true, runs the full Update flow.
//
// SAFE-03 carve-out: recreate=false is exempt from safety labels (the
// handler middleware skips CheckSafetyLabel for ActionForcePull). When
// the handler routes recreate=true, it explicitly opts into the Update
// safety check (RESEARCH.md OQ#5 — recreate IS a recreate operation;
// SAFE-01 applies). Either way, this method just does what's asked —
// the label check lives in the handler.
func (o *actionOrchestrator) ForcePull(ctx context.Context, service string, recreate bool) (ActionResult, error) {
	if recreate {
		// Delegate to the full Update flow.
		return o.Update(ctx, service)
	}

	start := time.Now()
	unlock, err := o.lockService(service)
	if err != nil {
		return ActionResult{}, fmt.Errorf("actions.ForcePull %s: %w", service, err)
	}
	defer unlock()

	snapshot, ok := o.store.Get().Containers[service]
	if !ok {
		return ActionResult{}, fmt.Errorf("actions.ForcePull: container %q not in state", service)
	}

	slog.Info("action.start", "service", service, "action", string(ActionForcePull))
	o.send(ctx, poll.StateUpdate{
		Kind:    poll.KindActionStart,
		Service: service,
		Apply: func(s *state.State) {
			c, ok := s.Containers[service]
			if !ok {
				return
			}
			c.ActionInFlight = "force_pulling"
			c.ActionError = ""
			s.Containers[service] = c
		},
	})

	pulledDigest, err := o.pullAndVerifyDigest(ctx, snapshot.Image, snapshot.Tag)
	if err != nil {
		o.sendFailureResult(ctx, service, "pull", err)
		slog.Error("action.pull_failed", "service", service, "err", err)
		return ActionResult{}, fmt.Errorf("actions.ForcePull %s: %w", service, err)
	}

	// No recreate, no verify — just update AvailableDigest and clear
	// in-flight. ForcePull is read-only with respect to the running
	// container; it refreshes the local image cache. The container's
	// CurrentDigest does NOT change.
	//
	// WARNING-06 (Phase 4 review) — accepted cron-vs-action race:
	// the single-consumer channel applies messages serially, but the
	// ORDERING between this KindActionResult and an in-flight
	// KindFetchResult from the cron poller is undefined. If the cron
	// message arrives AFTER this one with a stale digest, AvailableDigest
	// briefly reverts; UpdateAvailable may flicker. State eventually
	// converges on the next cron tick (typically <60s) since both
	// producers read the same registry. The Phase 5 UI MUST NOT assume
	// monotonic UpdateAvailable; documented in API.md "Race semantics."
	o.send(ctx, poll.StateUpdate{
		Kind:    poll.KindActionResult,
		Service: service,
		Apply: func(s *state.State) {
			c, ok := s.Containers[service]
			if !ok {
				return
			}
			c.AvailableDigest = pulledDigest
			if c.CurrentDigest != "" && c.CurrentDigest != pulledDigest {
				c.UpdateAvailable = true
			}
			c.ActionInFlight = ""
			c.ActionError = ""
			s.Containers[service] = c
		},
	})
	slog.Info("action.complete",
		"service", service,
		"action", string(ActionForcePull),
		"before", snapshot.CurrentDigest,
		"after", snapshot.CurrentDigest,
		"new_available", pulledDigest,
		"exit_code", 0,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return ActionResult{
		CurrentDigest:  snapshot.CurrentDigest,
		PreviousDigest: snapshot.PreviousDigest,
	}, nil
}

// ----------------------------------------------------------------------------
// Shared helpers
// ----------------------------------------------------------------------------

// findFallbackRollbackTarget scans the local docker daemon's image cache
// for previously-pulled-but-untagged images of the same repo as `image`
// and returns the manifest digest of the most-recent-non-current one as
// a rollback target — used when state.PreviousDigest is empty.
//
// Heuristic: ImageList(reference=<image>, All=true) matches any local
// image whose RepoTags or RepoDigests reference the repo. Skip any
// image whose RepoDigests include the currentDigest suffix (that's the
// container's current image, not a rollback target). Among the rest,
// sort by image.Summary.Created (Unix timestamp) descending and return
// the first whose RepoDigests has an `@sha256:` suffix.
//
// Returns ("", nil) if no candidates exist (the daemon hasn't seen any
// other version of this repo, or every candidate matches current).
// Returns ("", err) on daemon errors — caller may log and fall through.
//
// This unblocks the "container is broken, state never recorded a
// previous digest" case (BUG-7c) without operator-side hand-editing.
// The daemon's image cache is the source of truth for "what version
// did this host previously run" — querying it directly is more reliable
// than asking the operator to remember.
func (o *actionOrchestrator) findFallbackRollbackTarget(ctx context.Context, image, currentDigest string) (string, error) {
	if image == "" {
		return "", nil
	}
	imgs, err := o.dockerClient.ImageList(ctx, docker.ImageListOptions{
		All: true,
		Filters: docker.Filters{
			"reference": {image: true},
		},
	})
	if err != nil {
		return "", fmt.Errorf("findFallbackRollbackTarget ImageList %s: %w", image, err)
	}
	type candidate struct {
		digest  string
		created int64
	}
	var cands []candidate
	for _, img := range imgs {
		// Skip candidates whose RepoDigests INCLUDE the current digest —
		// that's the running image, not a rollback target. The "@sha256:"
		// suffix match is exact-string; partial overlap is not a problem
		// because the digest is content-addressable.
		isCurrent := false
		var pickedDigest string
		for _, rd := range img.RepoDigests {
			at := strings.Index(rd, "@sha256:")
			if at < 0 {
				continue
			}
			d := rd[at+1:] // "sha256:<hex>"
			if currentDigest != "" && d == currentDigest {
				isCurrent = true
				break
			}
			if pickedDigest == "" {
				pickedDigest = d
			}
		}
		if isCurrent || pickedDigest == "" {
			continue
		}
		cands = append(cands, candidate{digest: pickedDigest, created: img.Created})
	}
	if len(cands) == 0 {
		return "", nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].created > cands[j].created })
	return cands[0].digest, nil
}

// pullAndVerifyDigest pulls image:tag through docker.Client.ImagePull,
// drains the JSONMessages stream to extract the aux digest, and cross-
// checks it against registry.Resolver.Digest. Returns the pulled digest
// on success; wrapped ErrPullFailed on any failure.
//
// The registry cross-check is the Pitfall 1 prevention — never trust
// local re-hash; both the docker daemon and the registry resolver read
// Docker-Content-Digest from the registry, so they must agree.
func (o *actionOrchestrator) pullAndVerifyDigest(ctx context.Context, image, tag string) (string, error) {
	if tag == "" {
		tag = "latest"
	}
	ref := image + ":" + tag

	rc, err := o.dockerClient.ImagePull(ctx, ref, docker.ImagePullOptions{})
	if err != nil {
		// WARNING-04 fix: the moby SDK can return both a non-nil rc AND
		// a non-nil err on certain partial-failure paths (observed on
		// auth / registry errors). drainPullStream's defer rc.Close()
		// only fires when we hand the rc to it; on the err branch we
		// have to close defensively or the underlying HTTP connection
		// + file descriptor leak.
		if rc != nil {
			_ = rc.Close()
		}
		return "", fmt.Errorf("%w: ImagePull %s: %v", ErrPullFailed, ref, err)
	}
	pulledDigest, err := drainPullStream(rc)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrPullFailed, err)
	}

	registryDigest, err := o.resolver.Digest(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("%w: registry.Digest %s: %v", ErrPullFailed, ref, err)
	}
	if pulledDigest != registryDigest {
		return "", fmt.Errorf("%w: pulled digest %s does not match registry digest %s (Pitfall 1)",
			ErrPullFailed, pulledDigest, registryDigest)
	}
	return pulledDigest, nil
}

// inspectAndVerify captures the post-recreate container's RestartCount
// (which is the new baseline — typically 0 for a freshly-recreated
// container) and runs verifyAfterRecreate.
//
// snapshot is the PRE-action state.Container (used to read
// hmi-update.wait-for-healthy=true from labels — the healthcheck opt-in
// label lives on the operator's compose definition which doesn't change
// across the recreate).
//
// BLOCKER-01 fix (Phase 4 review): the PRE-action snapshot.ContainerID
// refers to the OLD container, which `docker compose up -d
// --force-recreate <svc>` destroys. We re-resolve the NEW container ID
// here via lookupContainerIDByService (ContainerList filtered by the
// com.docker.compose.service label) before handing it to
// verifyAfterRecreate. Without this re-resolution every successful
// recreate would surface as verify_failed (the OLD ID 404s on inspect).
func (o *actionOrchestrator) inspectAndVerify(ctx context.Context, service string, snapshot state.Container) error {
	newID, err := o.lookupContainerIDByService(ctx, service)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrVerifyFailed, &VerifyDetail{
			ContainerID: snapshot.ContainerID,
			Reason:      fmt.Sprintf("post-recreate ContainerList(%s) failed: %v", service, err),
		})
	}
	snap := verifySnapshot{
		ContainerID:       newID,
		RestartCount:      0, // post-recreate baseline; OLD container's count doesn't apply
		HealthcheckOptIn:  snapshot.Labels["hmi-update.wait-for-healthy"] == "true",
		VerifyWindow:      o.verifyWindow,
		HealthcheckWindow: o.healthcheckWindow,
	}
	return o.verifyAfterRecreate(ctx, snap)
}

// lookupContainerIDByService finds the post-recreate container ID for a
// compose service by listing containers filtered on the
// com.docker.compose.service label. Compose assigns the daemon-side label
// at recreate time; the most recently created match wins (defensive guard
// against an in-flight prior recreate that left a dying container behind).
//
// BLOCKER-01 mitigation. Without this lookup, verifyAfterRecreate would
// query the OLD container ID (destroyed by --force-recreate) and 404 on
// every successful recreate. The compose label is the canonical
// daemon-side identifier — see CONTEXT.md "service identity comes from
// the com.docker.compose.service container label" (compose/errors.go
// godoc preamble).
//
// We deliberately use ContainerList rather than adding a new method to
// the docker.Client facade: the existing surface already exposes
// ContainerList and the moby_test.go::TestClient_InterfaceMethodCount
// guard pins the interface at six methods. A seventh method would
// require coordinated edits to that guard, the interface doc, and the
// threat register (T-02-01-04).
func (o *actionOrchestrator) lookupContainerIDByService(ctx context.Context, service string) (string, error) {
	opts := docker.ContainerListOptions{
		// Include All so a container that briefly exits during recreate is
		// still visible (the verify loop will then catch the !Running
		// branch). Without All, an exited container is filtered out and
		// the lookup falsely reports "no container" — surfacing as
		// post-recreate ContainerList failed rather than the more
		// diagnostic "container not running" branch.
		All: true,
		Filters: docker.Filters{
			"label": {"com.docker.compose.service=" + service: true},
		},
	}
	containers, err := o.dockerClient.ContainerList(ctx, opts)
	if err != nil {
		return "", err
	}
	if len(containers) == 0 {
		return "", fmt.Errorf("no container found for service %q", service)
	}
	// Most-recently-created wins. The moby SDK does not guarantee
	// ordering on the wire so we pick max(Created) explicitly. Created
	// is a unix timestamp on container.Summary; ties (same-second
	// recreate) are extremely unlikely on an HMI but we deterministically
	// resolve them by picking the first occurrence (stable iteration on
	// the SDK-returned slice).
	newest := containers[0]
	for _, c := range containers[1:] {
		if c.Created > newest.Created {
			newest = c
		}
	}
	return newest.ID, nil
}

// sendFailureResult sends a KindActionResult that clears ActionInFlight
// and populates ActionError with "<phase>_failed: <reason>". The UI's
// per-row spinner thereby converges to idle + a toast on every failure
// path.
func (o *actionOrchestrator) sendFailureResult(ctx context.Context, service, phase string, err error) {
	reason := err.Error()
	// Trim a noisy "actions:" prefix if present so the wire string stays
	// readable; the slog event carries the full err.
	o.send(ctx, poll.StateUpdate{
		Kind:    poll.KindActionResult,
		Service: service,
		Apply: func(s *state.State) {
			c, ok := s.Containers[service]
			if !ok {
				return
			}
			c.ActionInFlight = ""
			c.ActionError = phase + "_failed: " + reason
			s.Containers[service] = c
		},
	})
}

// ----------------------------------------------------------------------------
// drainPullStream — Option A path (Assumption A1)
// ----------------------------------------------------------------------------

// pullJSONMessage matches the docker pull progress wire format. The
// daemon emits one JSON object per progress event; the terminal object
// carries the pulled digest in an aux field. Source:
// pkg.go.dev/github.com/moby/moby/pkg/jsonmessage.
type pullJSONMessage struct {
	Status string          `json:"status,omitempty"`
	ID     string          `json:"id,omitempty"`
	Error  string          `json:"error,omitempty"`
	Aux    json.RawMessage `json:"aux,omitempty"`
}

// pullAuxDigest is the candidate unmarshal target for the aux JSON. The
// daemon emits either {"ID":"sha256:..."} (older API versions) or
// {"Digest":"sha256:..."} (newer); we accept either.
type pullAuxDigest struct {
	ID     string `json:"ID,omitempty"`
	Digest string `json:"Digest,omitempty"`
}

// drainPullStream reads the io.ReadCloser returned by
// docker.Client.ImagePull as a stream of JSON pull-progress messages,
// extracts the aux digest from the terminal message, and returns it.
//
// Closes rc on return (the SDK contract requires draining + closing the
// stream so the daemon doesn't accumulate buffered progress messages).
//
// Per Assumption A1 (RESEARCH.md lines 1564), the digest is in either
// aux.ID or aux.Digest. The A1 probe test (probe_aux_digest_test.go)
// validates this shape against a real daemon when one is available;
// when no daemon is available the probe t.Skip's and Option A remains
// the design lean per RESEARCH.md A1 mitigation.
//
// BUG-5 fix (quick-260515-mu0, 2026-05-15): no-op pulls (":latest"
// already up to date) emit ONLY Status messages — no aux. drainPullStream
// now also scans msg.Status for the literal prefix "Digest: sha256:" as
// a FALLBACK digest source. Aux remains primary (real-pull path); the
// Status scan fires only when aux never arrived. See
// TestDrainPullStream_NoOpPull_DigestFromStatus for the production
// stream-shape regression gate.
func drainPullStream(rc io.ReadCloser) (string, error) {
	defer rc.Close()
	dec := json.NewDecoder(rc)
	var digest string
	for {
		var msg pullJSONMessage
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", fmt.Errorf("drain pull stream: %w", err)
		}
		if msg.Error != "" {
			return "", fmt.Errorf("docker pull stream error: %s", msg.Error)
		}
		// BUG-5 fix: no-op pulls (":latest" already up to date) emit a
		// Status message of form "Digest: sha256:<hex>" with NO aux
		// payload. Capture that as a FALLBACK digest source. Aux remains
		// primary — if aux fires later in the stream, its digest will
		// overwrite this one in the aux branch below.
		//
		// Production observation (HMI box, 2026-05-15 16:26:34): every
		// Update / Force-pull on already-up-to-date :latest images
		// produced 3 Status messages and no aux, causing
		// drainPullStream to return "docker pull stream ended without
		// aux digest" and the orchestrator to surface action.pull_failed.
		// The same daemon, same stream shape, manual `docker pull` from
		// the shell — works fine. The bug was purely in this parser.
		if digest == "" && strings.HasPrefix(msg.Status, "Digest: sha256:") {
			digest = strings.TrimPrefix(msg.Status, "Digest: ")
		}
		if len(msg.Aux) == 0 {
			continue
		}
		var aux pullAuxDigest
		if err := json.Unmarshal(msg.Aux, &aux); err != nil {
			// Surface the unmarshal error explicitly — a future SDK
			// version may emit a different aux shape and we want the
			// error to be diagnostic.
			return "", fmt.Errorf("drain pull stream: aux unmarshal: %w", err)
		}
		switch {
		case aux.Digest != "":
			digest = aux.Digest
		case aux.ID != "":
			digest = aux.ID
		}
	}
	if digest == "" {
		return "", errors.New("docker pull stream ended without aux digest")
	}
	return digest, nil
}
