// Package poll (continued). patterns.go owns the in-memory compiled-regex
// cache that filters which upstream tag's digest the poller fetches per
// container (DETECT-08).
//
// Architectural anchor (see .planning/phases/03-registry-polling-update-detection/03-CONTEXT.md
// Area 3 "Tag-Pattern & Digest-Pin Handling"):
//
//	The raw pattern string lives on state.Container.Labels["hmi-update.tag-pattern"]
//	(persisted source of truth, set by Phase 2's discovery goroutine). The
//	compiled *regexp.Regexp is a derived in-memory artifact (regexp.Regexp
//	is not JSON-serializable) that this cache maintains, keyed by compose
//	service name. The discovery goroutine (Plan 03-04 wiring) calls Set on
//	every upsert.
//
// Semantics:
//
//	No pattern set for service       -> Match returns true (permissive default)
//	Pattern compiled successfully    -> Match returns re.MatchString(tag)
//	Pattern fails to compile         -> Set returns the regex compile error;
//	                                    the entry is DELETED from the cache
//	                                    (Match thereafter returns true);
//	                                    caller surfaces "invalid tag-pattern
//	                                    label, ignored" on Container.Notes.
//	Empty pattern                    -> entry deleted, no error (documented
//	                                    as "no constraint" — same behaviour
//	                                    as never-set).
//
// Boot is NEVER crashed by a bad regex; the poller stays useful even when
// operators paste malformed labels (CONTEXT.md Area 3 invariant).
//
// Concurrency: Patterns uses an RWMutex. Set/Delete take the write lock;
// Match takes the read lock. Read-mostly access pattern — Set is called at
// discovery time (per container start event), Match is called per cron
// tick per eligible container.
//
// Threat model alignment: T-03-03-01 (ReDoS via malformed regex) — Go's
// regexp package is RE2 by construction; no catastrophic backtracking. The
// invalid-regex path is exercised by TestPatterns_InvalidRegex_PermissiveWithWarning.
package poll

import (
	"log/slog"
	"regexp"
	"sync"
)

// Patterns is a thread-safe map of compose-service-name -> compiled
// tag-pattern regex. The zero value is NOT usable (nil map); use
// NewPatterns to construct.
type Patterns struct {
	mu sync.RWMutex
	m  map[string]*regexp.Regexp
}

// NewPatterns returns an empty Patterns ready for Set/Match/Delete.
func NewPatterns() *Patterns {
	return &Patterns{m: map[string]*regexp.Regexp{}}
}

// Set compiles pattern for service. Returns the regex compile error if
// the pattern is invalid; the entry is DELETED on error so subsequent
// Match returns true (permissive fallthrough). Empty pattern is treated
// as "no constraint" — entry deleted, no error.
//
// Callers surface invalid-regex errors as a Note on Container.Notes
// (per CONTEXT.md Area 3); the slog warning is emitted here so the
// caller does not have to.
func (p *Patterns) Set(service, pattern string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pattern == "" {
		delete(p.m, service)
		return nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		slog.Warn("tag_pattern.invalid_regex",
			"service", service, "pattern", pattern, "err", err)
		delete(p.m, service)
		return err
	}
	p.m[service] = re
	return nil
}

// Delete removes any compiled pattern for service. Safe to call when no
// entry exists.
func (p *Patterns) Delete(service string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.m, service)
}

// Match returns true if tag matches the compiled pattern for service,
// or true if no pattern is set (permissive default). Never returns
// false for an unknown service — the absence of a constraint is itself
// the green-light.
func (p *Patterns) Match(service, tag string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	re, ok := p.m[service]
	if !ok || re == nil {
		return true
	}
	return re.MatchString(tag)
}
