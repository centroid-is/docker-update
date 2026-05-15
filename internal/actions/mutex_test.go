// RED-FIRST per C4. This file is authored as part of Task 1 of plan
// 04-03 and drives lockService body in mutex.go green.
//
// What this test file guards (ACT-08 acceptance surface + Phase-4-specific
// double-checked-locking pitfall):
//
//   - TestLockService_FirstAcquireSucceeds: a fresh orchestrator's first
//     lockService("a") returns (non-nil unlock, nil err).
//   - TestLockService_SecondAcquireReturnsErrServiceBusy: a second
//     lockService("a") without unlocking the first returns errors.Is
//     ErrServiceBusy.
//   - TestLockService_UnlockAllowsReacquire: after unlock, a subsequent
//     lockService("a") succeeds again.
//   - TestLockService_CrossServiceParallelism: lockService("a") held while
//     lockService("b") is also acquired — both succeed. The mutex map is
//     per-service, NOT global.
//   - TestLockService_Concurrent: 100 goroutines × atomic.Int32 tally;
//     assert a mix of acquired+rejected so the race detector observes
//     contention. RESEARCH.md lines 605–636 canonical body.
//   - TestLockService_DoubleCheckedLocking_NoDuplicateMutex: 100 goroutines
//     hit lockService("brand-new") simultaneously; after all unlocks, the
//     map carries EXACTLY ONE entry for "brand-new" (RESEARCH.md lines
//     533–540 — the double-check is the load-bearing invariant against
//     two goroutines installing two distinct *sync.Mutex pointers).
//
// Goroutine assertion contract (per state/persist_test.go lines 29-31 +
// poll/patterns_test.go lines 27-30): assertions fired off-goroutine use
// t.Errorf, NEVER t.Fatal — t.Fatal inside a goroutine only halts the
// goroutine that calls it and leaves the test to pass falsely.
package actions

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestOrchestrator returns an actionOrchestrator stub with the locks
// map initialized. Task 3 of plan 04-03 extends actionOrchestrator with
// additional fields; this helper stays callable because Go struct
// initialization is field-name-tagged.
func newTestOrchestrator() *actionOrchestrator {
	return &actionOrchestrator{
		locks: map[string]*sync.Mutex{},
	}
}

func TestLockService_FirstAcquireSucceeds(t *testing.T) {
	t.Parallel()
	o := newTestOrchestrator()
	unlock, err := o.lockService("svc-a")
	if err != nil {
		t.Fatalf("first lockService: want nil err, got %v", err)
	}
	if unlock == nil {
		t.Fatalf("first lockService: want non-nil unlock closure, got nil")
	}
	unlock()
}

func TestLockService_SecondAcquireReturnsErrServiceBusy(t *testing.T) {
	t.Parallel()
	o := newTestOrchestrator()
	unlock, err := o.lockService("svc-a")
	if err != nil {
		t.Fatalf("first lockService: %v", err)
	}
	defer unlock()

	_, err2 := o.lockService("svc-a")
	if err2 == nil {
		t.Fatalf("second lockService: want non-nil err, got nil")
	}
	if !errors.Is(err2, ErrServiceBusy) {
		t.Errorf("second lockService: want errors.Is ErrServiceBusy, got %v", err2)
	}
}

func TestLockService_UnlockAllowsReacquire(t *testing.T) {
	t.Parallel()
	o := newTestOrchestrator()
	unlock, err := o.lockService("svc-a")
	if err != nil {
		t.Fatalf("first lockService: %v", err)
	}
	unlock()

	unlock2, err := o.lockService("svc-a")
	if err != nil {
		t.Errorf("re-acquire after unlock: want nil err, got %v", err)
	}
	if unlock2 != nil {
		unlock2()
	}
}

func TestLockService_CrossServiceParallelism(t *testing.T) {
	t.Parallel()
	o := newTestOrchestrator()
	unlockA, err := o.lockService("svc-a")
	if err != nil {
		t.Fatalf("lockService(svc-a): %v", err)
	}
	defer unlockA()

	unlockB, err := o.lockService("svc-b")
	if err != nil {
		t.Errorf("lockService(svc-b) while svc-a held: want nil err (cross-service parallelism), got %v", err)
	}
	if unlockB != nil {
		unlockB()
	}
}

func TestLockService_Concurrent(t *testing.T) {
	t.Parallel()
	o := newTestOrchestrator()
	var wg sync.WaitGroup
	var acquired atomic.Int32
	var rejected atomic.Int32

	const goroutines = 100
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := o.lockService("svc-a")
			if err != nil {
				if !errors.Is(err, ErrServiceBusy) {
					// t.Errorf, NOT t.Fatal — off-goroutine.
					t.Errorf("unexpected error class: want ErrServiceBusy, got %v", err)
				}
				rejected.Add(1)
				return
			}
			acquired.Add(1)
			// Hold briefly so others see contention. Microsecond is
			// long enough that under -race -count=5 we observe both
			// classes; short enough the test finishes in <50ms.
			time.Sleep(time.Microsecond)
			unlock()
		}()
	}
	wg.Wait()

	if acquired.Load() < 1 || rejected.Load() < 1 {
		t.Errorf("want mix of acquired+rejected; got acquired=%d rejected=%d (total=%d)",
			acquired.Load(), rejected.Load(), goroutines)
	}
	if int(acquired.Load()+rejected.Load()) != goroutines {
		t.Errorf("acquired+rejected != total: %d+%d != %d",
			acquired.Load(), rejected.Load(), goroutines)
	}
}

func TestLockService_DoubleCheckedLocking_NoDuplicateMutex(t *testing.T) {
	t.Parallel()
	o := newTestOrchestrator()
	var wg sync.WaitGroup

	const goroutines = 100
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := o.lockService("brand-new")
			if err != nil {
				if !errors.Is(err, ErrServiceBusy) {
					t.Errorf("unexpected error class: want ErrServiceBusy, got %v", err)
				}
				return
			}
			// Hold briefly to surface contention; release before exit.
			time.Sleep(time.Microsecond)
			unlock()
		}()
	}
	wg.Wait()

	o.mu.RLock()
	defer o.mu.RUnlock()
	if got := len(o.locks); got != 1 {
		t.Errorf("double-checked locking: want exactly 1 entry for 'brand-new', got %d entries (%v)",
			got, o.locks)
	}
	if _, ok := o.locks["brand-new"]; !ok {
		t.Errorf("double-checked locking: 'brand-new' entry missing from map")
	}
}
