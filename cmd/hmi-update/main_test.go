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
	// slog encodes time.Duration as a string. The redactor still inspects
	// string-kinded values, but the value "350ms" does not match the regex
	// or substring fallback, so it should pass through.
	if got, want := m["elapsed"], "350ms"; got != want {
		t.Fatalf("duration attr was mutated: got %q want %q", got, want)
	}
}

func TestSlogReplaceAttr_PreservesCleanString(t *testing.T) {
	m := emit(t, "service", "hello world")
	if got, want := m["service"], "hello world"; got != want {
		t.Fatalf("clean string was mutated: got %q want %q", got, want)
	}
}
