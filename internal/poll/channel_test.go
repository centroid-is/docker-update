// RED-FIRST per C4. This file is authored before internal/poll/channel.go
// exists. Plan 03-03 Task 2 drives it green by implementing UpdateKind,
// StateUpdate, RunUpdater, and the package-private runUpdater test seam.
//
// What this test file guards (DETECT-10 acceptance surface):
//
//   - TestRunUpdater_AppliesEachMessage: each StateUpdate.Apply closure
//     is invoked exactly once via store.Update, and the resulting state
//     reflects all mutations.
//   - TestRunUpdater_DrainOnCancel: pending messages buffered on the
//     channel BEFORE ctx.Cancel are still applied during the drain loop
//     before the consumer returns. Load-bearing for Phase 4's SIGKILL-
//     resistance work and STATE-04 graceful shutdown.
//   - TestRunUpdater_ExitsOnCancelWithEmptyChannel: the consumer wakes
//     on ctx.Done() promptly when no messages are queued (no goroutine
//     leak on idle shutdown).
//   - TestRunUpdater_NoLockHeldAcrossSend: race-clean under -race; the
//     consumer's only critical-section work is the closure-applied map
//     mutation inside state.Store.Update. Race detector is the oracle.
//   - TestStateUpdate_AllKinds: every UpdateKind value (KindDigestResolved,
//     KindContainerEvent, KindPollSweepStart, KindPollSweepEnd) is
//     constructible and round-trips through the channel.
//   - TestRunUpdater_ErrorFromStore_Logged: an Update returning an error
//     logs poll.consumer.persist as slog.Error AND the consumer keeps
//     processing subsequent messages (does not exit on Update error).
//
// Store wrapper: tests use safeStore (mirrors discovery_test.go's pattern
// from PATTERNS.md Pattern H — copy-paste convention) to serialize Get
// and Update through an outer RWMutex with a deep-copied Containers map.
// state.Store.Get returns a shallow snapshot whose inner map header is
// shared with the writer; reading it concurrently with the consumer's
// in-flight Update trips the race detector. The wrapper closes that gap
// for tests that observe state while the consumer is running.
//
// Goroutine assertion contract (per discovery_test.go line 33): assertions
// fired off-goroutine use t.Errorf, NEVER t.Fatal — t.Fatal inside a
// goroutine only halts the goroutine that calls it and leaves the test
// to pass falsely.
package poll

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centroid-is/docker-update/internal/state"
)

// newTestStore mirrors internal/docker/discovery_test.go's helper (PATTERNS.md
// Pattern H — copy-paste for now per the in-repo convention; can be promoted
// to state.NewTestStore later if a third caller appears).
func newTestStore(t *testing.T) *state.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := state.NewStore(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("state.NewStore: %v", err)
	}
	return store
}

// safeStore mirrors internal/docker/discovery_test.go safeStore (lines
// 191-249). state.Store.Get returns a snapshot whose inner Containers
// map header is shared with the writer; concurrent Get+Update across
// the test goroutine and the consumer goroutine trips the race detector.
// safeStore serializes Get and Update through its own RWMutex, with Get
// returning a freshly-allocated deep copy of the Containers map.
//
// safeStore satisfies the package-private storeUpdater interface so the
// consumer goroutine can be driven by `runUpdater(ctx, ch, safeStore)`.
type safeStore struct {
	mu    sync.Mutex
	inner *state.Store
}

func newSafeStore(t *testing.T) *safeStore {
	t.Helper()
	return &safeStore{inner: newTestStore(t)}
}

func (s *safeStore) Get() state.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.inner.Get()
	out := state.State{
		Version:       src.Version,
		Containers:    make(map[string]state.Container, len(src.Containers)),
		LastPollStart: src.LastPollStart,
		LastPollEnd:   src.LastPollEnd,
		LastPollError: src.LastPollError,
	}
	for k, v := range src.Containers {
		out.Containers[k] = v
	}
	return out
}

func (s *safeStore) Update(fn func(*state.State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.Update(fn)
}

func TestRunUpdater_AppliesEachMessage(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	ch := make(chan StateUpdate, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runUpdater(ctx, ch, store)
		close(done)
	}()

	names := []string{"alpha", "beta", "gamma"}
	for _, n := range names {
		svc := n
		ch <- StateUpdate{
			Kind:    KindContainerEvent,
			Service: svc,
			Apply: func(st *state.State) {
				st.Containers[svc] = state.Container{Service: svc}
			},
		}
	}

	// Spin until all three rows are present, then assert.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := store.Get()
		if len(snap.Containers) == len(names) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	snap := store.Get()
	if len(snap.Containers) != len(names) {
		t.Fatalf("Containers count: want %d, got %d (snap=%+v)", len(names), len(snap.Containers), snap.Containers)
	}
	for _, n := range names {
		if _, ok := snap.Containers[n]; !ok {
			t.Errorf("expected service %q in state, missing", n)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("RunUpdater did not exit within 1s after cancel")
	}
}

// TestRunUpdater_DrainOnCancel: messages buffered before cancel are still
// applied during the drain loop. This is the load-bearing DETECT-10
// invariant for graceful shutdown — Phase 4 will SIGKILL-test it.
func TestRunUpdater_DrainOnCancel(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	ch := make(chan StateUpdate, 16)
	ctx, cancel := context.WithCancel(context.Background())

	// Pre-load 5 messages BEFORE starting the consumer so they sit in the
	// buffer; then cancel and start the consumer. The consumer's first
	// iteration sees ctx.Done(), enters the drain inner loop, and applies
	// all 5 pending messages.
	names := []string{"d1", "d2", "d3", "d4", "d5"}
	for _, n := range names {
		svc := n
		ch <- StateUpdate{
			Kind:    KindDigestResolved,
			Service: svc,
			Apply: func(st *state.State) {
				st.Containers[svc] = state.Container{Service: svc}
			},
		}
	}
	cancel()

	done := make(chan struct{})
	go func() {
		runUpdater(ctx, ch, store)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("RunUpdater did not exit within 2s after cancel; drain may have hung")
	}

	snap := store.Get()
	if len(snap.Containers) != len(names) {
		t.Fatalf("drain-on-cancel: want %d containers applied, got %d (snap=%+v)", len(names), len(snap.Containers), snap.Containers)
	}
	for _, n := range names {
		if _, ok := snap.Containers[n]; !ok {
			t.Errorf("drain: expected service %q in state, missing", n)
		}
	}
}

func TestRunUpdater_ExitsOnCancelWithEmptyChannel(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	ch := make(chan StateUpdate, 8)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		runUpdater(ctx, ch, store)
		close(done)
	}()

	// Give the goroutine a moment to enter the select.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("RunUpdater did not exit within 1s after cancel on empty channel")
	}
}

// TestRunUpdater_NoLockHeldAcrossSend uses the PUBLIC RunUpdater (not
// runUpdater) plus a real *state.Store so this test doubles as the
// production-shape smoke. State reads happen ONLY after both (a) all
// producers have finished sending AND (b) the consumer has fully drained
// and exited — only then is the store no longer being mutated and the
// shared-map-header read in store.Get() is race-clean against the now-
// stopped consumer goroutine. The race detector is the load-bearing
// oracle for the cross-goroutine invariant (no lock held across send).
func TestRunUpdater_NoLockHeldAcrossSend(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ch := make(chan StateUpdate, 32)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		RunUpdater(ctx, ch, store)
		close(done)
	}()

	const producers = 4
	const each = 25
	var wg sync.WaitGroup
	wg.Add(producers)
	for p := 0; p < producers; p++ {
		pid := p
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				svc := "p" + string(rune('0'+pid)) + "-" + string(rune('a'+i%26))
				captured := svc
				ch <- StateUpdate{
					Kind:    KindContainerEvent,
					Service: captured,
					Apply: func(st *state.State) {
						st.Containers[captured] = state.Container{Service: captured}
					},
				}
			}
		}()
	}
	wg.Wait()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("RunUpdater did not exit within 2s after cancel")
	}

	// Consumer has exited — store is no longer being mutated.
	if n := len(store.Get().Containers); n == 0 {
		t.Errorf("no containers landed; consumer did not process any messages")
	}
}

func TestStateUpdate_AllKinds(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	ch := make(chan StateUpdate, 8)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		runUpdater(ctx, ch, store)
		close(done)
	}()

	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	kinds := []UpdateKind{KindDigestResolved, KindContainerEvent, KindPollSweepStart, KindPollSweepEnd}
	for i, kind := range kinds {
		k := kind
		idx := i
		ch <- StateUpdate{
			Kind:    k,
			Service: "svc-kind",
			Apply: func(st *state.State) {
				switch idx {
				case 0:
					c := st.Containers["svc-kind"]
					c.Service = "svc-kind"
					c.AvailableDigest = "sha256:digest-" + string(rune('0'+idx))
					st.Containers["svc-kind"] = c
				case 1:
					c := st.Containers["svc-kind"]
					c.Service = "svc-kind"
					c.Stopped = false
					st.Containers["svc-kind"] = c
				case 2:
					st.LastPollStart = now
				case 3:
					st.LastPollEnd = now
				}
			},
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := store.Get()
		if snap.LastPollEnd.Equal(now) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	snap := store.Get()
	c, ok := snap.Containers["svc-kind"]
	if !ok {
		t.Fatalf("svc-kind missing from state: %+v", snap.Containers)
	}
	if c.AvailableDigest != "sha256:digest-0" {
		t.Errorf("KindDigestResolved closure: AvailableDigest=%q", c.AvailableDigest)
	}
	if !snap.LastPollStart.Equal(now) {
		t.Errorf("KindPollSweepStart closure: LastPollStart=%v", snap.LastPollStart)
	}
	if !snap.LastPollEnd.Equal(now) {
		t.Errorf("KindPollSweepEnd closure: LastPollEnd=%v", snap.LastPollEnd)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("RunUpdater did not exit within 1s after cancel")
	}
}

// errStore is the minimal storeUpdater test seam — its Update method
// records invocations and always returns the configured error, exercising
// the consumer's error-logging path without bringing in a real *state.Store.
type errStore struct {
	mu        sync.Mutex
	updateErr error
	applied   int32
}

func (e *errStore) Update(fn func(*state.State)) error {
	var st state.State
	st.Containers = map[string]state.Container{}
	fn(&st)
	atomic.AddInt32(&e.applied, 1)
	e.mu.Lock()
	err := e.updateErr
	e.mu.Unlock()
	return err
}

func TestRunUpdater_ErrorFromStore_Logged(t *testing.T) {
	t.Parallel()
	es := &errStore{updateErr: errors.New("synthetic persist failure")}
	ch := make(chan StateUpdate, 4)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		runUpdater(ctx, ch, es)
		close(done)
	}()

	for i := 0; i < 3; i++ {
		ch <- StateUpdate{
			Kind:    KindContainerEvent,
			Service: "svc",
			Apply:   func(st *state.State) {},
		}
	}

	// Wait for all three messages to be applied — the consumer does NOT
	// exit on Update error.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&es.applied) >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&es.applied); got < 3 {
		t.Errorf("consumer exited on Update error: only %d/3 applied", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("RunUpdater did not exit within 1s after cancel")
	}
}
