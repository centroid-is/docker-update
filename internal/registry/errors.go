// Package registry (continued). errors.go defines the two sentinel
// errors the Phase 3 poller branches on. See compose/errors.go for the
// codebase's first instance of this pattern (Phase 2 plan 02-02); this
// file mirrors its shape with two sentinels instead of one, since the
// registry layer has both a retry-once class and a fail-fast class.
//
// Callers test with errors.Is so the sentinel identity survives any
// number of fmt.Errorf("registry: %w", ...) wraps:
//
//	if _, err := resolver.Digest(ctx, ref); err != nil {
//	    if errors.Is(err, registry.ErrPermanent) {
//	        // skip retry; surface Notes on Container
//	        continue
//	    }
//	    if errors.Is(err, registry.ErrTransient) {
//	        // 1 retry after 2s sleep (CONTEXT.md Area 1)
//	        time.Sleep(2 * time.Second)
//	        // retry once...
//	    }
//	}
//
// Phase 4 will additionally map ErrPermanent to a 4xx response on
// POST /api/containers/:svc/update; that mapping is not in scope here.
package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrPermanent signals a non-retryable upstream condition (401, 403,
// 404). The poll loop logs once, surfaces a Note on the container, and
// skips this container until the next discovery event reshapes its
// labels or image ref. crane's transport reports HTTP-status errors with
// a string of the form "GET https://...: unexpected status code 401
// Unauthorized"; classify() greps for the status-code substring.
//
// Threat T-03-02-02 (Pitfall 2 prevention) hooks here: a 401 against
// GHCR with authn.Anonymous indicates an actual permission failure (the
// repo is private, the registry is misconfigured, etc.) — NOT the
// "Basic Og==" footgun. The transport_test.go regression guard
// independently asserts the Basic Og== string never crosses the wire.
var ErrPermanent = errors.New("registry: permanent error (401/403/404; do not retry)")

// ErrTransient signals a retryable condition (5xx, timeout, network
// error). The poll loop performs exactly 1 retry after a 2s sleep
// (cron's next tick covers anything beyond that). Default
// classification for unknown errors is ErrTransient — better to retry
// than to fail silently and miss an upstream image push.
var ErrTransient = errors.New("registry: transient error (5xx/timeout; retry once)")

// classify maps a raw error from crane.Digest into either ErrPermanent
// or ErrTransient, wrapping the original via fmt.Errorf so the sentinel
// survives errors.Is. Callers should not need to inspect the wrapped
// error directly; the sentinel is the load-bearing signal.
//
// Mapping (per CONTEXT.md Area 1 "Timeout + retry"):
//
//	401 / 403 / 404                 -> ErrPermanent
//	500 / 502 / 503 / 504           -> ErrTransient
//	context.DeadlineExceeded        -> ErrTransient
//	context.Canceled                -> ErrTransient
//	any other                       -> ErrTransient (defensive default)
//
// classify(nil) returns nil so callers can wrap unconditionally.
func classify(err error) error {
	if err == nil {
		return nil
	}
	// Context errors must be checked via errors.Is BEFORE the substring
	// match, since a context-cancellation message may also contain the
	// upstream status code as the cause chain unwinds.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %v", ErrTransient, err)
	}
	msg := err.Error()
	// crane wraps registry errors with the upstream HTTP status code
	// inside the message body. The exact format is "unexpected status
	// code 401" / "unexpected status code 503" / etc. We match on that
	// substring; both forms ("status code NNN" and the bare " NNN ")
	// are accepted to survive minor format drift on the SDK side.
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		if strings.Contains(msg, fmt.Sprintf("status code %d", code)) ||
			strings.Contains(msg, fmt.Sprintf(" %d ", code)) {
			return fmt.Errorf("%w: %v", ErrPermanent, err)
		}
	}
	for _, code := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		if strings.Contains(msg, fmt.Sprintf("status code %d", code)) ||
			strings.Contains(msg, fmt.Sprintf(" %d ", code)) {
			return fmt.Errorf("%w: %v", ErrTransient, err)
		}
	}
	// Default: better to retry than miss an upstream push (CONTEXT.md
	// Area 1 — "defensive default"). cron's next tick will catch
	// anything that survives the 1-retry budget.
	return fmt.Errorf("%w: %v", ErrTransient, err)
}
