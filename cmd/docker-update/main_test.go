// Unit tests for the OBS-04 output-side defense: the slog JSON handler
// installed via newRedactingHandler must elide any string-kinded attr
// whose value matches ^(Bearer|Basic)\s OR contains "Bearer "/"Basic "
// as a substring, replacing it with the literal "REDACTED".
//
// Partners with internal/registry's redactingTransport (request-side
// defense). Either alone is sufficient under the threat model in
// CONTEXT.md Area 4; both together survive a future careless logger
// call that bypasses the transport.
//
// Plan 03-05 Task 1 RED — these tests fail until newRedactingHandler
// is added to main.go.
//
// Goroutine assertion contract: these are single-goroutine tests; no
// off-goroutine assertions to worry about.

package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

// emit constructs a handler with newRedactingHandler, writes one
// slog.Info record carrying the supplied attr, and decodes the JSON
// line back into a map so individual fields can be inspected.
func emit(t *testing.T, key string, val any) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	h := newRedactingHandler(&buf, slog.LevelDebug)
	logger := slog.New(h)
	logger.Info("msg", slog.Any(key, val))
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("failed to decode emitted log line %q: %v", buf.String(), err)
	}
	return m
}

func TestSlogReplaceAttr_RedactsBearer(t *testing.T) {
	m := emit(t, "authn", "Bearer abc.def.ghi")
	if got, want := m["authn"], "REDACTED"; got != want {
		t.Fatalf("Bearer prefix not redacted: got %q want %q", got, want)
	}
}

func TestSlogReplaceAttr_RedactsBasic(t *testing.T) {
	// The exact Pitfall 2 regression literal: Basic Og== is the empty-creds
	// placeholder that DefaultKeychain emits when docker login was run with
	// an empty username.
	m := emit(t, "authn", "Basic Og==")
	if got, want := m["authn"], "REDACTED"; got != want {
		t.Fatalf("Basic prefix not redacted: got %q want %q", got, want)
	}
}

func TestSlogReplaceAttr_RedactsSubstring(t *testing.T) {
	// The Contains() fallback catches "Bearer "/"Basic " embedded inside a
	// longer string (e.g. a logger that joined key+value: Authorization=Bearer xyz).
	m := emit(t, "header", "Authorization=Bearer xyz")
	if got, want := m["header"], "REDACTED"; got != want {
		t.Fatalf("substring Bearer not redacted: got %q want %q", got, want)
	}
}

func TestSlogReplaceAttr_PreservesNonString(t *testing.T) {
	// Non-string attrs (ints, durations, times, bools) must pass through
	// untouched. The redactor only inspects string-kinded values.
	m := emit(t, "count", 42)
	if got, want := m["count"], float64(42); got != want {
		t.Fatalf("int attr was mutated: got %v want %v", got, want)
	}

	m = emit(t, "elapsed", 350*time.Millisecond)
	// slog's default JSONHandler encodes time.Duration as a number
	// (nanoseconds). The redactor inspects only string-kinded attrs, so
	// the int64 ns value passes through untouched — exactly the
	// non-string preservation invariant we want to assert.
	if got, want := m["elapsed"], float64(350*time.Millisecond); got != want {
		t.Fatalf("duration attr was mutated: got %v want %v", got, want)
	}
}

func TestSlogReplaceAttr_PreservesCleanString(t *testing.T) {
	m := emit(t, "service", "hello world")
	if got, want := m["service"], "hello world"; got != want {
		t.Fatalf("clean string was mutated: got %q want %q", got, want)
	}
}

// TestSlogReplaceAttr_RedactsBareBase64Credentials covers the CR-03 gap:
// a logger that strips the "Basic " prefix before passing the value to
// slog would have leaked the credentials through the prefix-only regex.
// The base64-credentials probe in newRedactingHandler decodes the value
// and redacts when the decoded payload contains a ':' — the
// "username:password" shape RFC 7617 §2 mandates for Basic auth.
//
// "Og==" decodes to ":" — the empty-creds placeholder DefaultKeychain
// emits when docker login was run with an empty username (the Pitfall 2
// regression literal). It MUST be redacted even without the "Basic "
// prefix.
func TestSlogReplaceAttr_RedactsBareBase64Credentials(t *testing.T) {
	cases := []struct {
		name string
		val  string
	}{
		{name: "empty creds (Og==)", val: "Og=="},       // decodes to ":"
		{name: "user:pass padded", val: "Zm9vOmJhcg=="}, // decodes to "foo:bar"
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			m := emit(t, "authn", tc.val)
			if got, want := m["authn"], "REDACTED"; got != want {
				t.Errorf("bare base64 credential not redacted: input=%q got=%v want=%q",
					tc.val, got, want)
			}
		})
	}
}

// TestSlogReplaceAttr_PreservesNonCredentialBase64 asserts the base64
// probe does NOT redact strings that happen to decode without a colon —
// the probe must be tight enough that "Y2xlYW4=" (decodes to "clean")
// passes through untouched. Negative-case guard for CR-03.
func TestSlogReplaceAttr_PreservesNonCredentialBase64(t *testing.T) {
	// "clean" base64-encoded → "Y2xlYW4="; decoded payload has no ':'.
	m := emit(t, "payload", "Y2xlYW4=")
	if got, want := m["payload"], "Y2xlYW4="; got != want {
		t.Errorf("non-credential base64 was mutated: got %v want %q", got, want)
	}
}
