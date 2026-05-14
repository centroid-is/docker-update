// RED-FIRST per C4. These tests are authored before internal/registry/errors.go
// has a body. Plan 03-02 Task 2 GREEN drives them green by implementing the
// ErrPermanent / ErrTransient sentinels and the classify() helper.
//
// What these tests guard:
//
//   - TestClassify_Permanent: any error whose message carries an HTTP 401,
//     403, or 404 substring (the shape crane wraps registry errors with)
//     resolves to errors.Is(_, ErrPermanent) == true. Branching on the
//     sentinel is the poller's load-bearing decision (do-not-retry).
//   - TestClassify_Transient: 500 / 502 / 503 / 504 + context.DeadlineExceeded
//     + context.Canceled resolve to errors.Is(_, ErrTransient) == true.
//     The poller retries once after 2s.
//   - TestClassify_Wraps: the original error is preserved as the Unwrap
//     target so callers can still read the original message via fmt.Errorf
//     formatters. Closes the gap where classify() would otherwise mask the
//     true upstream cause from operator logs.
//   - TestClassify_DefaultsToTransient: an unknown error class (no HTTP
//     status signal) defaults to ErrTransient. Better to retry than to
//     fail silently — CONTEXT.md Area 1 "Timeout + retry" makes this the
//     defensive default.
//   - TestClassify_Nil: classify(nil) returns nil — defensive no-op so
//     callers can wrap unconditionally.
//
// Goroutine assertion contract: all tests in this file are synchronous;
// no goroutine-spawned assertions. (transport_test.go uses goroutines via
// httptest handler dispatch; that file has its own t.Errorf discipline.)
package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestClassify_Permanent verifies that the three HTTP status codes the
// CONTEXT.md Area 1 decision maps to "do not retry" — 401, 403, 404 —
// land on ErrPermanent. Threat T-03-02-02 + DETECT-03 acceptance.
func TestClassify_Permanent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   error
	}{
		{name: "401 unauthorized", in: errors.New("GET https://ghcr.io/v2/foo/bar/manifests/latest: unexpected status code 401 Unauthorized")},
		{name: "403 forbidden", in: errors.New("GET https://ghcr.io/v2/foo/bar/manifests/latest: unexpected status code 403 Forbidden")},
		{name: "404 not found", in: errors.New("GET https://ghcr.io/v2/foo/bar/manifests/latest: unexpected status code 404 Not Found")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classify(tc.in)
			if got == nil {
				t.Fatalf("classify(%v) = nil, want non-nil ErrPermanent-wrapping error", tc.in)
			}
			if !errors.Is(got, ErrPermanent) {
				t.Errorf("classify(%v): errors.Is(_, ErrPermanent) = false, want true; got: %v", tc.in, got)
			}
			if errors.Is(got, ErrTransient) {
				t.Errorf("classify(%v): errors.Is(_, ErrTransient) = true, want false (must not be both)", tc.in)
			}
		})
	}
}

// TestClassify_Transient verifies that the four 5xx codes plus context
// timeout/cancellation land on ErrTransient (retry-once class). CONTEXT.md
// Area 1 + DETECT-03 acceptance.
func TestClassify_Transient(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   error
	}{
		{name: "500 internal", in: errors.New("GET https://reg/v2/foo/bar/manifests/latest: unexpected status code 500 Internal Server Error")},
		{name: "502 bad gateway", in: errors.New("GET https://reg/v2/foo/bar/manifests/latest: unexpected status code 502 Bad Gateway")},
		{name: "503 unavailable", in: errors.New("GET https://reg/v2/foo/bar/manifests/latest: unexpected status code 503 Service Unavailable")},
		{name: "504 gateway timeout", in: errors.New("GET https://reg/v2/foo/bar/manifests/latest: unexpected status code 504 Gateway Timeout")},
		{name: "context deadline exceeded", in: context.DeadlineExceeded},
		{name: "context canceled", in: context.Canceled},
		{name: "context deadline wrapped", in: fmt.Errorf("crane: %w", context.DeadlineExceeded)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classify(tc.in)
			if got == nil {
				t.Fatalf("classify(%v) = nil, want non-nil ErrTransient-wrapping error", tc.in)
			}
			if !errors.Is(got, ErrTransient) {
				t.Errorf("classify(%v): errors.Is(_, ErrTransient) = false, want true; got: %v", tc.in, got)
			}
			if errors.Is(got, ErrPermanent) {
				t.Errorf("classify(%v): errors.Is(_, ErrPermanent) = true, want false (must not be both)", tc.in)
			}
		})
	}
}

// TestClassify_Wraps verifies that classify() preserves the original
// error as an Unwrap target so callers logging via fmt.Errorf still see
// the upstream cause. Load-bearing for operator log-readability — if the
// sentinel ate the original message, slog would emit only
// "registry: transient error (5xx/timeout; retry once)" with no clue
// which registry / which status code surfaced.
func TestClassify_Wraps(t *testing.T) {
	t.Parallel()

	orig := errors.New("GET https://ghcr.io/v2/centroid-is/hmi-update/manifests/latest: unexpected status code 503 Service Unavailable")
	got := classify(orig)
	if got == nil {
		t.Fatalf("classify(orig) = nil, want non-nil")
	}
	// Sentinel identity
	if !errors.Is(got, ErrTransient) {
		t.Errorf("errors.Is(classify(orig), ErrTransient) = false, want true")
	}
	// Original message survives via the wrap
	if msg := got.Error(); !strings.Contains(msg, "503") {
		t.Errorf("classify(orig).Error() = %q, want it to contain the original '503' substring", msg)
	}
	if msg := got.Error(); !strings.Contains(msg, "ghcr.io") {
		t.Errorf("classify(orig).Error() = %q, want it to contain the original 'ghcr.io' substring", msg)
	}
}

// TestClassify_DefaultsToTransient verifies the defensive default: an
// error whose message gives no HTTP-status signal still resolves to
// ErrTransient (retry-once class). Better to over-retry than to miss an
// upstream image push because we couldn't pattern-match the error.
func TestClassify_DefaultsToTransient(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   error
	}{
		{name: "bare network error", in: errors.New("dial tcp: lookup ghcr.io on 127.0.0.53:53: no such host")},
		{name: "EOF", in: errors.New("unexpected EOF")},
		{name: "tls handshake", in: errors.New("tls: handshake failure")},
		{name: "generic", in: errors.New("something went wrong")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classify(tc.in)
			if got == nil {
				t.Fatalf("classify(%v) = nil, want non-nil", tc.in)
			}
			if !errors.Is(got, ErrTransient) {
				t.Errorf("classify(%v): errors.Is(_, ErrTransient) = false, want true; got: %v", tc.in, got)
			}
		})
	}
}

// TestClassify_Nil verifies the defensive no-op so callers can wrap
// unconditionally without an `if err != nil` guard at the call site.
func TestClassify_Nil(t *testing.T) {
	t.Parallel()
	if got := classify(nil); got != nil {
		t.Errorf("classify(nil) = %v, want nil", got)
	}
}

