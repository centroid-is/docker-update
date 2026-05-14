// RED-FIRST per C4. This file is authored before internal/poll/patterns.go's
// Set/Match/Delete bodies exist (Phase 1 ships an empty interface stub for
// poll.Poller; the Patterns type itself is brand new to plan 03-03).
//
// What this test file guards (DETECT-08 acceptance surface):
//
//   - TestPatterns_ValidRegex_Match: a compiled pattern matches a tag string
//     that satisfies the regex (^latest-pg17$ against "latest-pg17").
//   - TestPatterns_ValidRegex_NoMatch: the same pattern rejects an unrelated
//     tag ("latest-pg18-oss"); DETECT-08 happy-path filter discipline.
//   - TestPatterns_NoPatternSet_PermissiveDefault: Match against a service
//     that was never Set returns true — "no constraint" semantics.
//   - TestPatterns_InvalidRegex_PermissiveWithWarning: Set with a malformed
//     regex returns a non-nil error AND the entry is deleted from the cache
//     so subsequent Match calls return true (permissive fallthrough).
//     CONTEXT.md Area 3: "Log structured warning, treat as no constraint
//     (permissive — container still polled), surface notes 'invalid
//     tag-pattern label, ignored'. Never crash boot."
//   - TestPatterns_EmptyPattern_DeletesEntry: Set with "" removes any prior
//     pattern; documented as "no constraint" branch.
//   - TestPatterns_Concurrent_RaceClean: 8 goroutines × 100 Match calls
//     under -race report no race; results consistent.
//   - TestPatterns_DeleteRemovesPattern: explicit Delete removes a prior
//     pattern; subsequent Match returns true.
//
// Goroutine assertion contract (per discovery_test.go line 33): assertions
// fired off-goroutine use t.Errorf, NEVER t.Fatal — t.Fatal inside a
// goroutine only halts the goroutine that calls it and leaves the test to
// pass falsely.
package poll

import (
	"sync"
	"testing"
)

func TestPatterns_ValidRegex_Match(t *testing.T) {
	t.Parallel()
	p := NewPatterns()
	if err := p.Set("svc", "^latest-pg17$"); err != nil {
		t.Fatalf("Set: unexpected err: %v", err)
	}
	if !p.Match("svc", "latest-pg17") {
		t.Errorf("Match(latest-pg17): want true, got false")
	}
}

func TestPatterns_ValidRegex_NoMatch(t *testing.T) {
	t.Parallel()
	p := NewPatterns()
	if err := p.Set("svc", "^latest-pg17$"); err != nil {
		t.Fatalf("Set: unexpected err: %v", err)
	}
	if p.Match("svc", "latest-pg18-oss") {
		t.Errorf("Match(latest-pg18-oss): want false, got true")
	}
}

func TestPatterns_NoPatternSet_PermissiveDefault(t *testing.T) {
	t.Parallel()
	p := NewPatterns()
	// No Set call for "never-set-svc": Match must default to permissive (true).
	if !p.Match("never-set-svc", "any-tag") {
		t.Errorf("Match on unset service: want true (permissive default), got false")
	}
}

func TestPatterns_InvalidRegex_PermissiveWithWarning(t *testing.T) {
	t.Parallel()
	p := NewPatterns()
	err := p.Set("svc", "[invalid(")
	if err == nil {
		t.Fatalf("Set with invalid regex: want non-nil error, got nil")
	}
	// Subsequent Match must be permissive (true) — entry deleted on compile
	// failure per CONTEXT.md Area 3 "Tag-pattern regex semantics".
	if !p.Match("svc", "anything") {
		t.Errorf("Match after invalid-regex Set: want true (permissive fallthrough), got false")
	}
}

func TestPatterns_EmptyPattern_DeletesEntry(t *testing.T) {
	t.Parallel()
	p := NewPatterns()
	if err := p.Set("svc", "^x$"); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if p.Match("svc", "anything") {
		t.Errorf("Match after restrictive Set: want false, got true")
	}
	if err := p.Set("svc", ""); err != nil {
		t.Fatalf("empty-pattern Set: want nil err, got %v", err)
	}
	if !p.Match("svc", "anything") {
		t.Errorf("Match after empty Set: want true (entry deleted), got false")
	}
}

func TestPatterns_Concurrent_RaceClean(t *testing.T) {
	t.Parallel()
	p := NewPatterns()
	if err := p.Set("svc", "^v[0-9]+$"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	const goroutines = 8
	const callsPer = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < callsPer; i++ {
				if !p.Match("svc", "v1") {
					// t.Errorf, NOT t.Fatal — off-goroutine assertion contract.
					t.Errorf("concurrent Match(v1): want true, got false")
					return
				}
				if p.Match("svc", "garbage") {
					t.Errorf("concurrent Match(garbage): want false, got true")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestPatterns_DeleteRemovesPattern(t *testing.T) {
	t.Parallel()
	p := NewPatterns()
	if err := p.Set("svc", "^x$"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if p.Match("svc", "anything") {
		t.Errorf("Match after Set(^x$): want false for 'anything', got true")
	}
	p.Delete("svc")
	if !p.Match("svc", "anything") {
		t.Errorf("Match after Delete: want true (permissive fallthrough), got false")
	}
	// Delete on already-absent entry is safe.
	p.Delete("svc")
}
