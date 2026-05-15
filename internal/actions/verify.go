// Package actions (continued). verify.go owns the verify-after-recreate
// poll loop (Pitfalls 4 + 12 — recreate-succeeds-but-crash-loop and
// recreate-returns-0-but-startup-probe-fails). Once compose.Runner.UpdateService
// returns exit 0, the orchestrator MUST NOT trust that exit code alone; it
// must observe the recreated container's State for 15 consecutive seconds
// (default) or for up to 60 seconds if the container has opted into
// healthcheck-based verification via the hmi-update.wait-for-healthy=true
// label.
//
// Architectural anchor: mirrors internal/poll/poller.go::Run lifecycle
// (lines 215–240 — Start + ctx.Done + drain) and internal/docker/discovery.go::
// ctxAwareSleep (lines 125–137 — select { ctx.Done(); t.C }). The
// consecutive-success-counter is the Phase-4-novel piece; the underlying
// ticker shape transfers verbatim.
//
// Decision semantics (CONTEXT.md Area 3, locked):
//
//   - Default (no opt-in): 15 consecutive successful 1-second ticks
//     required. Fail-fast on !Running OR RestartCount delta > 0.
//   - Healthcheck opt-in (label hmi-update.wait-for-healthy=true):
//     extended 60s window. "healthy" status short-circuits success
//     unconditionally; "unhealthy" fail-fast; "starting" / "" keeps
//     polling. After the 60s deadline with no health status reported,
//     soft-success (don't block indefinitely).
//   - ctx.Done → ErrVerifyCanceled (distinct from ErrVerifyFailed).
//
// Typed inner error contract (B2 fix from plan revision):
//
//	Verify-failure branches wrap a *VerifyDetail with ErrVerifyFailed
//	using the DOUBLE-WRAP pattern:
//
//	    return fmt.Errorf("%w: %w", ErrVerifyFailed, &VerifyDetail{...})
//
//	Plan 04-04's handlers_actions.go uses errors.As(err, &detail) to
//	extract the structured fields (RestartCount, Running, ContainerID,
//	Reason) for the response body shape locked in CONTEXT.md Area 3:
//
//	    {
//	      "error": "verify_failed",
//	      "reason": "container restarted 3 times in 15s",
//	      "restart_count": 3,
//	      "running": false,
//	      "container_id": "abc123def456"
//	    }
//
//	The ErrVerifyCanceled branch does NOT carry a VerifyDetail — there
//	are no diagnostic fields; the 503 body is a verbatim constant.
//
// Test seam: verifyTickInterval is a package-private VAR (not const) so
// verify_test.go can override it to 1*time.Millisecond via t.Cleanup-
// restored assignment and run the 15-tick happy path in <50ms. Documented
// in verify_test.go's header.
package actions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/moby/moby/api/types/container"
)

const (
	defaultVerifyWindow      = 15 * time.Second
	defaultHealthcheckWindow = 60 * time.Second
)

// verifyTickInterval is the per-tick cadence. PACKAGE-PRIVATE VAR (not
// const) so verify_test.go can speed up to 1µs/1ms via t.Cleanup-restored
// override. Production never changes this — the value is the default
// tick rate of 1s that the consecutive-success counter is calibrated
// against (15 ticks at 1s each = 15s default window).
//
// In production, target = verifyWindow / verifyTickInterval = 15s / 1s = 15
// consecutive successful ticks. Deadline is t+verifyWindow with the loop
// body taking microseconds — plenty of slack.
//
// In TEST mode, verifyTickInterval is shrunk to microseconds and tests
// pin VerifyWindow accordingly so target stays reasonable AND the loop
// completes well before deadline. See setFastTick in verify_test.go.
var verifyTickInterval = 1 * time.Second

// verifySnapshot captures pre-action state for comparison during the
// verify loop. The orchestrator constructs this BEFORE invoking
// compose.Runner.UpdateService so RestartCount is the value the daemon
// reported on the PREVIOUS container; if the recreate produces a new
// container with RestartCount==0 and the verify loop observes a value
// > snap.RestartCount mid-window, that's the crash-loop signal.
type verifySnapshot struct {
	// ContainerID is the post-recreate container ID. The orchestrator
	// looks this up via docker.ContainerInspect after
	// compose.Runner.UpdateService returns and BEFORE entering
	// verifyAfterRecreate so the loop always queries the new container.
	ContainerID string
	// RestartCount is the threshold above which the loop fails-fast.
	// For a freshly-recreated container this is typically 0 — any
	// observed increment means the container crashed and the daemon
	// restarted it within the verify window.
	RestartCount int
	// HealthcheckOptIn flips the loop into the opt-in mode (60s window
	// + healthcheck status checks). Read from
	// Labels["hmi-update.wait-for-healthy"]=="true" by the orchestrator
	// (from the CACHED state.Container; no docker.Inspect for label
	// retrieval per OBS-03 discipline).
	HealthcheckOptIn bool
	// VerifyWindow is the default-mode window (defaultVerifyWindow if
	// zero). The number of consecutive successful ticks required is
	// int(VerifyWindow / verifyTickInterval).
	VerifyWindow time.Duration
	// HealthcheckWindow is the opt-in-mode window
	// (defaultHealthcheckWindow if zero).
	HealthcheckWindow time.Duration
}

// VerifyDetail carries structured fields from a verify-after-recreate
// failure. Wrapped with ErrVerifyFailed so callers can errors.As to
// extract the fields for the HTTP response body.
//
// EXPORTED (uppercase) intentionally: Plan 04-04's
// internal/api/handlers_actions_test.go cross-imports this type so its
// TestHandleUpdate_VerifyFailed_500_StructuredBody can construct a fake
// orchestrator that returns
//
//	fmt.Errorf("%w: %w", ErrVerifyFailed, &VerifyDetail{...})
//
// and assert the handler emits the locked CONTEXT.md Area 3 body shape
// verbatim.
type VerifyDetail struct {
	RestartCount int
	Running      bool
	ContainerID  string
	// Reason is human-readable: "container restarted 3 times in 15s",
	// "container not running", "healthcheck unhealthy". Forms the
	// `reason` field of the locked response body.
	Reason string
}

// Error returns the Reason — VerifyDetail satisfies the error interface
// so fmt.Errorf("%w: %w", sentinel, detail) attaches it to the wrap chain.
func (v *VerifyDetail) Error() string { return v.Reason }

// Unwrap returns ErrVerifyFailed so errors.Is(err, ErrVerifyFailed) is
// true on any err that contains a *VerifyDetail in its wrap chain.
// This is the load-bearing contract for Plan 04-04's error-class
// branching: the HTTP handler tests errors.Is first to dispatch the
// status code, then errors.As to extract the typed body fields.
func (v *VerifyDetail) Unwrap() error { return ErrVerifyFailed }

// verifyAfterRecreate polls docker.ContainerInspect once per
// verifyTickInterval, requiring `target` consecutive successful ticks
// (= snap.VerifyWindow / verifyTickInterval) before returning nil. On
// any anomaly (not running, RestartCount++, healthcheck unhealthy),
// returns fmt.Errorf("%w: %w", ErrVerifyFailed, &VerifyDetail{...}). On
// ctx cancellation, returns ErrVerifyCanceled.
//
// Body shape per RESEARCH.md Pattern 5 (lines 649–771). Defensive nil-
// check on insp.Container.State (moby types pointer-nest the State
// struct; a nil daemon response would otherwise nil-deref).
func (o *actionOrchestrator) verifyAfterRecreate(ctx context.Context, snap verifySnapshot) error {
	// Apply defaults so callers don't have to.
	verifyWindow := snap.VerifyWindow
	if verifyWindow <= 0 {
		verifyWindow = defaultVerifyWindow
	}
	healthcheckWindow := snap.HealthcheckWindow
	if healthcheckWindow <= 0 {
		healthcheckWindow = defaultHealthcheckWindow
	}

	// WARNING-05 fix: deadline gets a larger safety factor (3× tick
	// instead of 2×) AND the first inspect fires immediately rather
	// than waiting for the first ticker.C event. The prior loop shape
	// (ticker-first, inspect-second) meant the 15th inspect happened
	// at t=15×tickInterval — racing the deadline.After() boundary on
	// loaded CI machines where scheduler jitter can exceed 2×
	// verifyTickInterval. Now the loop is "inspect, then wait" which
	// fits N inspects in (N-1) tick intervals, leaving comfortable
	// slack against the deadline.
	deadline := time.Now().Add(verifyWindow + 3*verifyTickInterval)
	if snap.HealthcheckOptIn {
		deadline = time.Now().Add(healthcheckWindow + 3*verifyTickInterval)
	}

	// target is the number of consecutive successful ticks the default
	// path requires (15 for the 15s/1s default). Opt-in path may
	// short-circuit on "healthy" before reaching target.
	target := int(verifyWindow / verifyTickInterval)
	if target < 1 {
		target = 1
	}
	consecutive := 0
	sawHealthDeclared := false // opt-in only — tracked for soft-success eligibility

	// firstTick is true on the very first iteration so we inspect
	// immediately rather than waiting tickInterval first. After the
	// first iteration we wait tickInterval between inspects.
	firstTick := true
	ticker := time.NewTicker(verifyTickInterval)
	defer ticker.Stop()

	for {
		if firstTick {
			firstTick = false
			// Check ctx before the first inspect so a pre-canceled ctx
			// short-circuits without an extra docker call.
			if err := ctx.Err(); err != nil {
				return ErrVerifyCanceled
			}
			// Fall through to the inspect block below.
		} else {
			select {
			case <-ctx.Done():
				return ErrVerifyCanceled

			case <-ticker.C:
			}
		}

		// Deadline + inspect block (shared between first-tick and
		// subsequent-tick paths).
		{
			if time.Now().After(deadline) {
				// Deadline expired.
				//
				// Opt-in soft-success branch: in healthcheck-opt-in mode,
				// if no Health status was ever Healthy/Unhealthy and the
				// container has been Running throughout (no fast-fail
				// fired), treat as soft-success. Containers without a
				// HEALTHCHECK directive that the operator labeled
				// wait-for-healthy=true should not block indefinitely —
				// "no health status reported" after the 60s window is
				// the operator's signal that healthcheck-opt-in was
				// misconfigured. Per CONTEXT.md Area 3.
				if snap.HealthcheckOptIn && !sawHealthDeclared {
					slog.Info("action.phase",
						"phase", "verified",
						"mode", "healthcheck_opt_in_soft_success")
					return nil
				}
				return fmt.Errorf("%w: %w", ErrVerifyFailed,
					&VerifyDetail{
						ContainerID: snap.ContainerID,
						Reason: fmt.Sprintf("did not reach %d consecutive healthy ticks within %s",
							target, verifyWindow),
					})
			}

			insp, err := o.dockerInspector.ContainerInspect(ctx, snap.ContainerID)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return ErrVerifyCanceled
				}
				// Container disappeared (compose down + up race?) or
				// daemon unreachable — fail-fast.
				return fmt.Errorf("%w: %w", ErrVerifyFailed,
					&VerifyDetail{
						ContainerID: snap.ContainerID,
						Reason:      fmt.Sprintf("ContainerInspect failed: %v", err),
					})
			}

			// Defensive nil-check; moby types pointer-nest State+Health.
			if insp.Container.State == nil {
				return fmt.Errorf("%w: %w", ErrVerifyFailed,
					&VerifyDetail{
						ContainerID: snap.ContainerID,
						Reason:      "container state nil",
					})
			}

			// Fast-fail: container not running.
			if !insp.Container.State.Running {
				return fmt.Errorf("%w: %w", ErrVerifyFailed,
					&VerifyDetail{
						RestartCount: insp.Container.RestartCount,
						Running:      false,
						ContainerID:  snap.ContainerID,
						Reason:       "container not running",
					})
			}

			// Fast-fail: RestartCount incremented (crash loop signal).
			if insp.Container.RestartCount > snap.RestartCount {
				delta := insp.Container.RestartCount - snap.RestartCount
				return fmt.Errorf("%w: %w", ErrVerifyFailed,
					&VerifyDetail{
						RestartCount: insp.Container.RestartCount,
						Running:      insp.Container.State.Running,
						ContainerID:  snap.ContainerID,
						Reason:       fmt.Sprintf("container restarted %d times in %s", delta, verifyWindow),
					})
			}

			// Opt-in healthcheck branch.
			if snap.HealthcheckOptIn && insp.Container.State.Health != nil {
				switch insp.Container.State.Health.Status {
				case container.Unhealthy:
					sawHealthDeclared = true
					return fmt.Errorf("%w: %w", ErrVerifyFailed,
						&VerifyDetail{
							RestartCount: insp.Container.RestartCount,
							Running:      insp.Container.State.Running,
							ContainerID:  snap.ContainerID,
							Reason:       "healthcheck unhealthy",
						})
				case container.Healthy:
					sawHealthDeclared = true
					// Healthcheck IS the readiness signal — unconditional
					// success. Skip the consecutive-ticks requirement.
					slog.Info("action.phase",
						"phase", "verified",
						"mode", "healthcheck_healthy",
						"ticks", consecutive)
					return nil
					// container.Starting / "" / container.NoHealthcheck -
					// keep polling.
				}
			}

			// Healthy tick — increment counter.
			consecutive++
			if consecutive >= target {
				slog.Info("action.phase",
					"phase", "verified",
					"ticks", consecutive)
				return nil
			}
		}
	}
}
