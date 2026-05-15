// Package actions (continued). mutex.go owns the per-service mutex map
// (ACT-08) — a sync.RWMutex protecting a map[string]*sync.Mutex. lockService
// returns an unlock closure on success and ErrServiceBusy on contention; the
// orchestrator's Update / Rollback / ForcePull bodies wrap the action in
//
//	unlock, err := o.lockService(svc)
//	if err != nil { return ErrServiceBusy }
//	defer unlock()
//
// Architectural anchor (parallel of internal/poll/patterns.go's RWMutex-
// around-map pattern at lines 50–58, and internal/state/store.go's RWMutex-
// around-value pattern at lines 23–27):
//
//	The outer RWMutex protects the map; per-entry sync.Mutex protects the
//	per-service action body. Read-mostly access — the fast path is RLock +
//	map lookup. Slow path (new-entry creation) escalates to Lock with a
//	double-check.
//
// Why double-checked locking on entry creation is load-bearing:
//
//	Two concurrent requests for a previously-unseen service would otherwise
//	race between the RUnlock and the Lock — both observe ok==false under
//	RLock, both try to Lock, both write a fresh *sync.Mutex to o.locks[svc],
//	and one of the two TryLock calls happens on a DIFFERENT mutex instance
//	than the one stored in the map. Subsequent requests would then see the
//	"stored" mutex in the locked state held by the OTHER goroutine, which
//	will never release it. Re-reading inside the write lock catches the
//	race: only one goroutine writes; both end up calling TryLock on the
//	same mutex instance.
//
// Why sync.Mutex.TryLock and not a buffered channel "permit":
//
//	TryLock (Go 1.18+) is the load-bearing primitive — non-blocking, returns
//	immediately on contention. ACT-08 is explicit: "double-click on the same
//	row returns 409 immediately, no queueing." A channel-based permit pool
//	would require additional state to know when a permit was "stuck" (no
//	timeout, no cancellation propagation). sync.Mutex's TryLock matches the
//	semantic perfectly.
//
// Map-growth budget: the service set is bounded by the compose file's
// service count (typically <20 on an HMI). Entries are tiny (a *sync.Mutex
// header). We never delete entries — a destroyed service may still receive
// a stale Update request, and getting ErrServiceBusy (instead of a panic
// from a deleted key + lazy-recreation race) is the safer outcome.
//
// Plan boundary: Task 1 of plan 04-03 lands lockService here on a STUB
// actionOrchestrator struct carrying only the two fields it needs (mu +
// locks). Task 3 MOVES the actionOrchestrator declaration to orchestrator.go
// and EXTENDS it with all dependencies (docker, runner, resolver, etc.);
// lockService stays here with the doc comment "// struct declared in
// orchestrator.go" at the top of this file.
package actions

import "sync"

// actionOrchestrator carries the per-service mutex map. Task 3 of plan
// 04-03 moves this declaration to orchestrator.go and adds the remaining
// dependency fields (docker.Client, compose.Runner, registry.Resolver,
// state.Store, updates chan, selfService, verifyWindow, healthcheckWindow).
// Until Task 3 lands, this stub carries only the fields lockService
// touches so the mutex_test.go file compiles in isolation.
type actionOrchestrator struct {
	mu    sync.RWMutex
	locks map[string]*sync.Mutex
}

// lockService attempts to acquire the per-service mutex without blocking.
// On success, returns an unlock closure for `defer unlock()`. On contention
// returns (nil, ErrServiceBusy) so the caller can map to HTTP 409.
//
// Fast path: read existing mutex under RLock. Slow path: create the entry
// under Lock with double-check (another goroutine may have created the entry
// between the RUnlock and the Lock — see file doc-comment for why).
//
// The TryLock call happens OUTSIDE the outer RWMutex so a goroutine holding
// a per-service mutex does NOT block other goroutines that need to look up
// a different service's mutex. Cross-service parallelism is the design
// intent (ACT-08): updating svc-a does not delay svc-b.
//
// Source: RESEARCH.md Pattern 4 (lines 568–591) — canonical body.
func (o *actionOrchestrator) lockService(svc string) (func(), error) {
	// Fast path: read existing mutex under RLock.
	o.mu.RLock()
	m, ok := o.locks[svc]
	o.mu.RUnlock()

	if !ok {
		// Slow path: create the entry under Lock with double-check.
		// Another goroutine may have raced us between RUnlock and Lock;
		// re-read under the write lock and re-test before allocating.
		o.mu.Lock()
		m, ok = o.locks[svc]
		if !ok {
			m = &sync.Mutex{}
			o.locks[svc] = m
		}
		o.mu.Unlock()
	}

	// TryLock is non-blocking — 409 on contention. (sync.Mutex.TryLock
	// was added in Go 1.18; our go.mod pins go 1.26.)
	if !m.TryLock() {
		return nil, ErrServiceBusy
	}
	return m.Unlock, nil
}
