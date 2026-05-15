// Package actions (continued). errors.go defines the seven sentinel errors
// that the Plan 04-04 HTTP handler layer maps to HTTP status codes. The
// orchestrator wraps these via fmt.Errorf("%w") at every failure branch so
// callers test with errors.Is and the sentinel identity survives any number
// of context-adding wraps.
//
// See internal/registry/errors.go for the codebase's two-sentinel precedent
// (Pattern B from PATTERNS.md) and internal/compose/errors.go for the
// single-sentinel precedent (Phase 2 plan 02-02 — the codebase's first
// sentinel-error file). This file mirrors their shape with SEVEN sentinels.
//
// Callers test with errors.Is so the sentinel identity survives any number
// of fmt.Errorf("actions: ... %w", ...) wraps:
//
//	if _, err := orch.Update(ctx, svc); err != nil {
//	    if errors.Is(err, actions.ErrServiceBusy) {
//	        // -> 409 service_busy
//	    }
//	    if errors.Is(err, actions.ErrVerifyFailed) {
//	        var detail *actions.VerifyDetail
//	        if errors.As(err, &detail) {
//	            // structured 500 body — see verify.go::VerifyDetail
//	        }
//	    }
//	    // ... other branches ...
//	}
//
// Plan 04-04's internal/api/handlers_actions.go imports this package and
// is the single point of error-to-HTTP-status mapping. The HTTP status code
// for each sentinel is recorded in the godoc below so that mapping table
// stays in lockstep with the test expectations.
//
// Note on the compose sentinel namespace: internal/compose/errors.go also
// declares ErrComposeFailed (Phase 2 plan 02-02). The action-layer sentinel
// here is a DISTINCT package-private value; the action layer wraps the
// compose-layer sentinel rather than re-exporting it. Both remain reachable
// via errors.Is on a wrapped action error because the wrap chain preserves
// both. The grep gate in the verifier suite expects both sentinels to be
// present in their respective packages.
package actions

import "errors"

// ErrServiceBusy is returned (wrapped) when lockService(svc) finds the
// per-service mutex already held. The orchestrator returns immediately —
// no blocking, no queueing. Plan 04-04 maps to HTTP 409.
//
// Wrap pattern:
//
//	if !m.TryLock() {
//	    return nil, ErrServiceBusy
//	}
//
// Branch example:
//
//	if errors.Is(err, actions.ErrServiceBusy) {
//	    writeJSONError(w, http.StatusConflict, actionBodyServiceBusy)
//	    return
//	}
var ErrServiceBusy = errors.New("actions: service busy (per-service mutex held)")

// ErrSelfProtection is returned (wrapped) when the operator attempts an
// action against the compose service that hmi-update itself is running as
// (HMI_UPDATE_SELF_SERVICE, default "hmi-update"). The middleware
// CheckSelfProtection returns false BEFORE the LookupContainer step so the
// 409 fires even when hmi-update is not in the watched-containers cache
// (default: hmi-update.watch=false on the self container). Plan 04-04 maps
// to HTTP 409 with body referencing PROJECT.md's "Manual self-upgrade
// procedure" section.
//
// Wrap pattern: the middleware writes the response directly via
// writeJSONError; callers branch via the helper's return bool, not via
// errors.Is. The sentinel exists so future programmatic consumers (e.g.
// a CLI client) can branch.
var ErrSelfProtection = errors.New("actions: refusing self-action (see PROJECT.md self-upgrade procedure)")

// ErrActionDisabledByLabel is returned (wrapped) when CheckSafetyLabel
// finds hmi-update.allow-update=false (for Update) or
// hmi-update.allow-rollback=false (for Rollback). Force-pull is EXEMPT —
// SAFE-03 carve-out (the running container is unaffected by a local image
// cache refresh). Plan 04-04 maps to HTTP 409.
//
// SAFE-03 invariant: the poll loop ignores these labels entirely; only
// the action middleware honors them. internal/actions/middleware_test.go's
// TestSAFE03_PollIgnoresActionLabels code-grep test enforces this.
//
// Branch example:
//
//	if errors.Is(err, actions.ErrActionDisabledByLabel) {
//	    writeJSONError(w, http.StatusConflict, body) // body distinguishes update/rollback
//	    return
//	}
var ErrActionDisabledByLabel = errors.New("actions: action disabled by hmi-update.allow-* label")

// ErrVerifyFailed is returned (wrapped) when verifyAfterRecreate concludes
// the recreate did not stabilize: container not running, RestartCount
// incremented, healthcheck unhealthy (when opted-in), or 15s consecutive-
// success window not reached before deadline. Plan 04-04 maps to HTTP 500
// with a STRUCTURED body — the *VerifyDetail typed inner error carries
// RestartCount, Running, ContainerID, Reason for the response body.
//
// Wrap pattern (DOUBLE-WRAP):
//
//	return fmt.Errorf("%w: %w", ErrVerifyFailed,
//	    &VerifyDetail{
//	        RestartCount: insp.Container.RestartCount,
//	        Running:      insp.Container.State.Running,
//	        ContainerID:  snap.ContainerID,
//	        Reason:       "container restarted N times in 15s",
//	    })
//
// Plan 04-04 extracts the detail via errors.As:
//
//	var detail *actions.VerifyDetail
//	if errors.As(err, &detail) {
//	    body := verifyFailedBody{
//	        Error:        "verify_failed",
//	        Reason:       detail.Reason,
//	        RestartCount: detail.RestartCount,
//	        Running:      detail.Running,
//	        ContainerID:  detail.ContainerID,
//	    }
//	    writeJSON(w, http.StatusInternalServerError, body)
//	}
var ErrVerifyFailed = errors.New("actions: verify-after-recreate failed")

// ErrVerifyCanceled is DISTINCT from ErrVerifyFailed and signals ctx
// cancellation during the verify loop (SIGTERM, request-scoped cancel).
// Operator-actionable signal: "the recreate may or may not have succeeded;
// retry after the cause of cancellation is resolved." Plan 04-04 maps to
// HTTP 503 (server is shutting down or the request was abandoned).
//
// The verify loop returns this sentinel without a *VerifyDetail — there
// are no diagnostic fields to report; the body is a verbatim constant.
//
// Wrap pattern:
//
//	case <-ctx.Done():
//	    return ErrVerifyCanceled
var ErrVerifyCanceled = errors.New("actions: verify-after-recreate canceled by context")

// ErrComposeFailed is the ACTION-LAYER sentinel for "compose.Runner.UpdateService
// returned non-zero exit." It is DISTINCT from compose.ErrComposeFailed (Phase 2
// plan 02-02): the action layer wraps the compose-layer error so callers may
// errors.Is either sentinel. Plan 04-04 maps actions.ErrComposeFailed to
// HTTP 500 with body containing the stderr snippet.
//
// Wrap pattern (the compose runner already wraps compose.ErrComposeFailed;
// the action layer adds its own sentinel on top):
//
//	if err := runner.UpdateService(ctx, svc); err != nil {
//	    return fmt.Errorf("%w: %w", ErrComposeFailed, err)
//	}
//
// errors.Is(returnedErr, actions.ErrComposeFailed) -> true
// errors.Is(returnedErr, compose.ErrComposeFailed) -> true
var ErrComposeFailed = errors.New("actions: compose runner returned non-zero exit")

// ErrPullFailed is returned (wrapped) for any failure of the docker pull
// path: ImagePull network error, aux-digest extraction failure, or
// digest-mismatch when the pulled digest does not equal the registry
// digest (Pitfall 1 verify). Plan 04-04 maps to HTTP 500.
//
// Wrap pattern (the digest-mismatch case is the load-bearing branch — never
// trust the local re-hash; cross-check against registry.Resolver.Digest):
//
//	if pulledDigest != registryDigest {
//	    return fmt.Errorf("%w: pulled digest %s does not match registry digest %s (Pitfall 1)",
//	        ErrPullFailed, pulledDigest, registryDigest)
//	}
var ErrPullFailed = errors.New("actions: docker pull failed or digest mismatch")

// ErrNoPreviousDigest is returned (wrapped) by Rollback when the
// container's state has no recorded PreviousDigest. The operator never
// performed an Update on this container so there is nothing to roll
// back to. Plan 04-04 maps to HTTP 400 (operator error class, not a
// server fault).
//
// Wrap pattern:
//
//	if snapshot.PreviousDigest == "" {
//	    return ActionResult{}, fmt.Errorf("actions.Rollback %s: %w", service, ErrNoPreviousDigest)
//	}
//
// Promoted to a proper sentinel (WARNING-02 of the Phase 4 review) so
// the writeActionError dispatch uses errors.Is alongside the other
// sentinels instead of a substring scan. The substring contract was a
// drift surface: a future change that wrapped this error inside another
// sentinel could route to the wrong HTTP status. errors.Is is robust.
// ErrNoPreviousDigest.Error() must contain the literal token
// "no_previous_digest" so the api/handlers_actions.go fallback substring
// scan (isNoPreviousDigest) keeps working for any wrap chain that
// reaches it without the sentinel, AND so operators grepping slog
// see a consistent token across the wire body and the log line.
var ErrNoPreviousDigest = errors.New("actions: rollback requires a recorded previous digest (no_previous_digest)")
