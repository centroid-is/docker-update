// RED-FIRST per C4. This file is authored before internal/poll/poller.go's
// cronPoller body exists (Phase 1 ships an empty Poller interface stub).
// Plan 03-03 Task 3 drives it green by implementing cronPoller +
// NewPoller + sweep + eligibleContainers + refForContainer.
//
// What this test file guards (DETECT-05/08/09 + Phase-3 pitfall):
//
//   - TestPoller_SatisfiesPoller: compile-time interface guard
//     `var _ Poller = (*cronPoller)(nil)`.
//   - TestNewPoller_FailFastOnInvalidCron: invalid HMI_UPDATE_CRON spec
//     returns a non-nil error containing both "invalid HMI_UPDATE_CRON"
//     and "5-field" (paste-ready remediation hint).
//   - TestPoller_TickInvokesSweep (DETECT-05): @every 100ms scheduler
//     ticks at least twice within 350ms wall-clock; fakeResolver
//     records >= 2 calls.
//   - TestPoller_SkipsPinnedContainers (DETECT-09): pinned containers
//     do NOT get resolver.Digest called; a StateUpdate sets Notes
//     "pinned: opt-out".
//   - TestPoller_SkipsStoppedContainers: Stopped=true containers are
//     filtered out of eligibleContainers (no digest to compare).
//   - TestPoller_AppliesTagPatternFilter (DETECT-08 happy): running
//     tag matches the pattern; resolver IS called.
//   - TestPoller_TagPatternRunningTagMismatch (DETECT-08 misconfig):
//     running tag does NOT match the pattern; resolver is NOT called;
//     Notes set to "running tag does not match tag-pattern label".
//   - TestPoller_ErrgroupSetLimitBeforeGo (Phase-3 pitfall): with
//     concurrency=4 and 10 eligible containers, max in-flight is <= 4.
//   - TestPoller_FetchSendsDigestResolvedUpdate: a successful resolver
//     call results in a StateUpdate that sets AvailableDigest,
//     LastPolledAt, and UpdateAvailable correctly.
//   - TestPoller_RespectsContext: Run(ctx) returns within 1s after
//     ctx.Cancel.
//   - TestPoller_PermanentErrorSurfacesNote: a resolver returning
//     ErrPermanent results in a StateUpdate whose Notes field carries
//     "registry error: permanent (check image ref)".
//
// Goroutine assertion contract (per discovery_test.go line 33): assertions
// fired off-goroutine use t.Errorf, NEVER t.Fatal — the sweep dispatches
// up to 4 worker goroutines under errgroup.
package poll

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centroid-is/docker-update/internal/registry"
	"github.com/centroid-is/docker-update/internal/state"
)

// fakeResolver implements registry.Resolver with scripted responses
// and concurrency instrumentation (atomic in-flight counter + peak
// tracker for the SetLimit ordering test).
type fakeResolver struct {
	mu              sync.Mutex
	digestScript    map[string]string // image:tag -> digest
	digestErrScript map[string]error  // image:tag -> nil/ErrPermanent/ErrTransient
	digestCalls     []string          // captured refs in order
	digestHook      func(ref string)  // optional ordering hook
	inFlight        int32             // atomic: workers currently inside Digest
	maxInFlight     int32             // atomic: peak observed in-flight
	delay           time.Duration     // simulated registry latency per call
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{
		digestScript:    map[string]string{},
		digestErrScript: map[string]error{},
		delay:           20 * time.Millisecond,
	}
}

func (f *fakeResolver) Digest(ctx context.Context, ref string) (string, error) {
	cur := atomic.AddInt32(&f.inFlight, 1)
	for {
		peak := atomic.LoadInt32(&f.maxInFlight)
		if cur <= peak {
			break
		}
		if atomic.CompareAndSwapInt32(&f.maxInFlight, peak, cur) {
			break
		}
	}
	f.mu.Lock()
	f.digestCalls = append(f.digestCalls, ref)
	hook := f.digestHook
	d := f.digestScript[ref]
	e := f.digestErrScript[ref]
	delay := f.delay
	f.mu.Unlock()
	if hook != nil {
		hook(ref)
	}
	if delay > 0 {
		select {
		case <-ctx.Done():
			atomic.AddInt32(&f.inFlight, -1)
			return "", ctx.Err()
		case <-time.After(delay):
		}
	}
	atomic.AddInt32(&f.inFlight, -1)
	return d, e
}

func (f *fakeResolver) callCounts() (n int, refs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.digestCalls))
	copy(out, f.digestCalls)
	return len(f.digestCalls), out
}

// drainUpdater is the test consumer: applies every StateUpdate to the
// supplied safeStore and exposes a counter for synchronization. Mirrors
// runUpdater's no-error path (tests for the error path live in
// channel_test.go).
type drainUpdater struct {
	store   *safeStore
	applied int32
}

func newDrainUpdater(store *safeStore) *drainUpdater {
	return &drainUpdater{store: store}
}

func (d *drainUpdater) Run(ctx context.Context, ch <-chan StateUpdate) {
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case msg := <-ch:
					_ = d.store.Update(msg.Apply)
					atomic.AddInt32(&d.applied, 1)
				default:
					return
				}
			}
		case msg := <-ch:
			_ = d.store.Update(msg.Apply)
			atomic.AddInt32(&d.applied, 1)
		}
	}
}

func (d *drainUpdater) Applied() int32 { return atomic.LoadInt32(&d.applied) }

// seedContainer is a tiny helper for placing a Container into safeStore.
func seedContainer(t *testing.T, s *safeStore, c state.Container) {
	t.Helper()
	if err := s.Update(func(st *state.State) {
		st.Containers[c.Service] = c
	}); err != nil {
		t.Fatalf("seed Update: %v", err)
	}
}

// ----------------------------------------------------------------------------
// Tests
// ----------------------------------------------------------------------------

func TestPoller_SatisfiesPoller(t *testing.T) {
	t.Parallel()
	var _ Poller = (*cronPoller)(nil)
}

func TestNewPoller_FailFastOnInvalidCron(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	ch := make(chan StateUpdate, 8)
	_, err := newPollerForTest("not-a-cron-expr", newFakeResolver(), NewPatterns(), store, ch, 4)
	if err == nil {
		t.Fatalf("NewPoller: want non-nil error for bad cron, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "invalid HMI_UPDATE_CRON") {
		t.Errorf("error message missing 'invalid HMI_UPDATE_CRON': %q", msg)
	}
	if !strings.Contains(msg, "5-field") {
		t.Errorf("error message missing '5-field' remediation hint: %q", msg)
	}
}

func TestPoller_TickInvokesSweep(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	seedContainer(t, store, state.Container{
		Service: "svc1",
		Image:   "ghcr.io/centroid-is/svc1",
		Tag:     "latest",
	})

	fr := newFakeResolver()
	fr.digestScript["ghcr.io/centroid-is/svc1:latest"] = "sha256:fakedigest1"

	ch := make(chan StateUpdate, 64)
	ctx, cancel := context.WithCancel(context.Background())
	updater := newDrainUpdater(store)
	updaterDone := make(chan struct{})
	go func() {
		updater.Run(ctx, ch)
		close(updaterDone)
	}()
	// Cleanup ordering matters here: t.Cleanup runs in LIFO order so
	// this updater-wait must be registered BEFORE the poller-wait below
	// (the poller-wait registers later → runs first → cancel() inside
	// it stops the poller → the cron Stop drains in-flight ticks →
	// after the poller exits, the updater-wait below cancels(ctx) again
	// and waits for the channel drain to finish).
	t.Cleanup(func() {
		select {
		case <-updaterDone:
		case <-time.After(2 * time.Second):
			t.Errorf("updater did not exit within 2s on cleanup")
		}
	})

	p, err := newPollerForTest("@every 100ms", fr, NewPatterns(), store, ch, 4)
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	pollerDone := make(chan struct{})
	go func() { _ = p.Run(ctx); close(pollerDone) }()
	// LIFO: this cleanup runs FIRST (before the updater-wait above).
	// We cancel the ctx and wait for the poller's cron.Stop().Done()
	// drain to complete before the updater finishes consuming queued
	// StateUpdates.
	t.Cleanup(func() {
		cancel()
		select {
		case <-pollerDone:
		case <-time.After(2 * time.Second):
			t.Errorf("poller did not exit within 2s on cleanup")
		}
	})

	// Wait for at least 2 ticks. @every 100ms means first tick at t+100ms,
	// second at t+200ms. Allow generous slack — robfig/cron's internal
	// scheduler goroutine plus the 20ms fakeResolver delay can stretch
	// the wall-clock budget noticeably on a loaded test machine. Poll
	// rather than fixed-sleep so the fast path completes quickly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := fr.callCounts(); n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	n, refs := fr.callCounts()
	if n < 2 {
		t.Errorf("DETECT-05: want >= 2 resolver calls within 2s, got %d (refs=%v)", n, refs)
	}
}

func TestPoller_SkipsPinnedContainers(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	seedContainer(t, store, state.Container{
		Service: "live",
		Image:   "ghcr.io/centroid-is/live",
		Tag:     "latest",
	})
	seedContainer(t, store, state.Container{
		Service: "pinned",
		Image:   "ghcr.io/centroid-is/pinned",
		Tag:     "",
		Pinned:  true,
	})

	fr := newFakeResolver()
	fr.digestScript["ghcr.io/centroid-is/live:latest"] = "sha256:livedigest"

	ch := make(chan StateUpdate, 64)
	ctx, cancel := context.WithCancel(context.Background())
	updater := newDrainUpdater(store)
	updaterDone := make(chan struct{})
	go func() {
		updater.Run(ctx, ch)
		close(updaterDone)
	}()
	// Cleanup ordering matters here: t.Cleanup runs in LIFO order so
	// this updater-wait must be registered BEFORE the poller-wait below
	// (the poller-wait registers later → runs first → cancel() inside
	// it stops the poller → the cron Stop drains in-flight ticks →
	// after the poller exits, the updater-wait below cancels(ctx) again
	// and waits for the channel drain to finish).
	t.Cleanup(func() {
		select {
		case <-updaterDone:
		case <-time.After(2 * time.Second):
			t.Errorf("updater did not exit within 2s on cleanup")
		}
	})

	p, err := newPollerForTest("@every 100ms", fr, NewPatterns(), store, ch, 4)
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	pollerDone := make(chan struct{})
	go func() { _ = p.Run(ctx); close(pollerDone) }()
	// LIFO: this cleanup runs FIRST (before the updater-wait above).
	// We cancel the ctx and wait for the poller's cron.Stop().Done()
	// drain to complete before the updater finishes consuming queued
	// StateUpdates.
	t.Cleanup(func() {
		cancel()
		select {
		case <-pollerDone:
		case <-time.After(2 * time.Second):
			t.Errorf("poller did not exit within 2s on cleanup")
		}
	})

	// Wait for the pinned-opt-out Notes to land (proves at least one
	// sweep ran), then assert the resolver was never called for the
	// pinned container.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, ok := store.Get().Containers["pinned"]; ok && c.Notes == "pinned: opt-out" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c := store.Get().Containers["pinned"]; c.Notes != "pinned: opt-out" {
		t.Errorf("DETECT-09: pinned container Notes: want 'pinned: opt-out', got %q", c.Notes)
	}

	_, refs := fr.callCounts()
	for _, r := range refs {
		if strings.HasPrefix(r, "ghcr.io/centroid-is/pinned") {
			t.Errorf("DETECT-09: pinned container should NOT be polled, got ref %q", r)
		}
	}
}

func TestPoller_SkipsStoppedContainers(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	seedContainer(t, store, state.Container{
		Service: "running",
		Image:   "ghcr.io/centroid-is/running",
		Tag:     "latest",
	})
	seedContainer(t, store, state.Container{
		Service: "stopped",
		Image:   "ghcr.io/centroid-is/stopped",
		Tag:     "latest",
		Stopped: true,
	})

	fr := newFakeResolver()
	fr.digestScript["ghcr.io/centroid-is/running:latest"] = "sha256:run"

	ch := make(chan StateUpdate, 64)
	ctx, cancel := context.WithCancel(context.Background())
	updater := newDrainUpdater(store)
	updaterDone := make(chan struct{})
	go func() {
		updater.Run(ctx, ch)
		close(updaterDone)
	}()
	// Cleanup ordering matters here: t.Cleanup runs in LIFO order so
	// this updater-wait must be registered BEFORE the poller-wait below
	// (the poller-wait registers later → runs first → cancel() inside
	// it stops the poller → the cron Stop drains in-flight ticks →
	// after the poller exits, the updater-wait below cancels(ctx) again
	// and waits for the channel drain to finish).
	t.Cleanup(func() {
		select {
		case <-updaterDone:
		case <-time.After(2 * time.Second):
			t.Errorf("updater did not exit within 2s on cleanup")
		}
	})

	p, err := newPollerForTest("@every 100ms", fr, NewPatterns(), store, ch, 4)
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	pollerDone := make(chan struct{})
	go func() { _ = p.Run(ctx); close(pollerDone) }()
	// LIFO: this cleanup runs FIRST (before the updater-wait above).
	// We cancel the ctx and wait for the poller's cron.Stop().Done()
	// drain to complete before the updater finishes consuming queued
	// StateUpdates.
	t.Cleanup(func() {
		cancel()
		select {
		case <-pollerDone:
		case <-time.After(2 * time.Second):
			t.Errorf("poller did not exit within 2s on cleanup")
		}
	})

	// Poll until the running container has been polled at least once.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := fr.callCounts(); n >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	_, refs := fr.callCounts()
	for _, r := range refs {
		if strings.HasPrefix(r, "ghcr.io/centroid-is/stopped") {
			t.Errorf("stopped containers should NOT be polled, got ref %q", r)
		}
	}
	saw := false
	for _, r := range refs {
		if r == "ghcr.io/centroid-is/running:latest" {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("running container not polled at all (refs=%v)", refs)
	}
}

func TestPoller_AppliesTagPatternFilter(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	seedContainer(t, store, state.Container{
		Service: "db",
		Image:   "timescale/timescaledb",
		Tag:     "latest-pg17",
	})

	patterns := NewPatterns()
	if err := patterns.Set("db", "^latest-pg17$"); err != nil {
		t.Fatalf("patterns.Set: %v", err)
	}

	fr := newFakeResolver()
	fr.digestScript["timescale/timescaledb:latest-pg17"] = "sha256:pg17"

	ch := make(chan StateUpdate, 64)
	ctx, cancel := context.WithCancel(context.Background())
	updater := newDrainUpdater(store)
	updaterDone := make(chan struct{})
	go func() {
		updater.Run(ctx, ch)
		close(updaterDone)
	}()
	// Cleanup ordering matters here: t.Cleanup runs in LIFO order so
	// this updater-wait must be registered BEFORE the poller-wait below
	// (the poller-wait registers later → runs first → cancel() inside
	// it stops the poller → the cron Stop drains in-flight ticks →
	// after the poller exits, the updater-wait below cancels(ctx) again
	// and waits for the channel drain to finish).
	t.Cleanup(func() {
		select {
		case <-updaterDone:
		case <-time.After(2 * time.Second):
			t.Errorf("updater did not exit within 2s on cleanup")
		}
	})

	p, err := newPollerForTest("@every 100ms", fr, patterns, store, ch, 4)
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	pollerDone := make(chan struct{})
	go func() { _ = p.Run(ctx); close(pollerDone) }()
	// LIFO: this cleanup runs FIRST (before the updater-wait above).
	// We cancel the ctx and wait for the poller's cron.Stop().Done()
	// drain to complete before the updater finishes consuming queued
	// StateUpdates.
	t.Cleanup(func() {
		cancel()
		select {
		case <-pollerDone:
		case <-time.After(2 * time.Second):
			t.Errorf("poller did not exit within 2s on cleanup")
		}
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := fr.callCounts(); n >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	_, refs := fr.callCounts()
	saw := false
	for _, r := range refs {
		if r == "timescale/timescaledb:latest-pg17" {
			saw = true
			break
		}
	}
	if !saw {
		t.Errorf("DETECT-08 happy: want resolver called with latest-pg17 ref, refs=%v", refs)
	}
}

func TestPoller_TagPatternRunningTagMismatch(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	seedContainer(t, store, state.Container{
		Service: "mis",
		Image:   "timescale/timescaledb",
		Tag:     "latest", // running tag DOES NOT match the operator-set pattern
	})

	patterns := NewPatterns()
	if err := patterns.Set("mis", "^latest-pg17$"); err != nil {
		t.Fatalf("patterns.Set: %v", err)
	}

	fr := newFakeResolver()

	ch := make(chan StateUpdate, 64)
	ctx, cancel := context.WithCancel(context.Background())
	updater := newDrainUpdater(store)
	updaterDone := make(chan struct{})
	go func() {
		updater.Run(ctx, ch)
		close(updaterDone)
	}()
	// Cleanup ordering matters here: t.Cleanup runs in LIFO order so
	// this updater-wait must be registered BEFORE the poller-wait below
	// (the poller-wait registers later → runs first → cancel() inside
	// it stops the poller → the cron Stop drains in-flight ticks →
	// after the poller exits, the updater-wait below cancels(ctx) again
	// and waits for the channel drain to finish).
	t.Cleanup(func() {
		select {
		case <-updaterDone:
		case <-time.After(2 * time.Second):
			t.Errorf("updater did not exit within 2s on cleanup")
		}
	})

	p, err := newPollerForTest("@every 100ms", fr, patterns, store, ch, 4)
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	pollerDone := make(chan struct{})
	go func() { _ = p.Run(ctx); close(pollerDone) }()
	// LIFO: this cleanup runs FIRST (before the updater-wait above).
	// We cancel the ctx and wait for the poller's cron.Stop().Done()
	// drain to complete before the updater finishes consuming queued
	// StateUpdates.
	t.Cleanup(func() {
		cancel()
		select {
		case <-pollerDone:
		case <-time.After(2 * time.Second):
			t.Errorf("poller did not exit within 2s on cleanup")
		}
	})

	// Wait for the mismatch Notes to land (proves at least one sweep
	// ran), then assert the resolver was never called.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, ok := store.Get().Containers["mis"]; ok &&
			c.Notes == "running tag does not match tag-pattern label" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if c := store.Get().Containers["mis"]; c.Notes != "running tag does not match tag-pattern label" {
		t.Errorf("DETECT-08 misconfig: want Notes 'running tag does not match tag-pattern label', got %q", c.Notes)
	}

	n, refs := fr.callCounts()
	if n > 0 {
		t.Errorf("DETECT-08 misconfig: resolver should NOT be called for non-matching running tag, refs=%v", refs)
	}
}

func TestPoller_ErrgroupSetLimitBeforeGo(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	for i := 0; i < 10; i++ {
		svc := fmt.Sprintf("svc%02d", i)
		seedContainer(t, store, state.Container{
			Service: svc,
			Image:   fmt.Sprintf("ghcr.io/centroid-is/%s", svc),
			Tag:     "latest",
		})
	}

	fr := newFakeResolver()
	fr.delay = 30 * time.Millisecond // long enough that workers stack up
	for i := 0; i < 10; i++ {
		fr.digestScript[fmt.Sprintf("ghcr.io/centroid-is/svc%02d:latest", i)] = "sha256:fake"
	}

	ch := make(chan StateUpdate, 64)
	ctx, cancel := context.WithCancel(context.Background())
	updater := newDrainUpdater(store)
	updaterDone := make(chan struct{})
	go func() {
		updater.Run(ctx, ch)
		close(updaterDone)
	}()
	// Cleanup ordering matters here: t.Cleanup runs in LIFO order so
	// this updater-wait must be registered BEFORE the poller-wait below
	// (the poller-wait registers later → runs first → cancel() inside
	// it stops the poller → the cron Stop drains in-flight ticks →
	// after the poller exits, the updater-wait below cancels(ctx) again
	// and waits for the channel drain to finish).
	t.Cleanup(func() {
		select {
		case <-updaterDone:
		case <-time.After(2 * time.Second):
			t.Errorf("updater did not exit within 2s on cleanup")
		}
	})

	p, err := newPollerForTest("@every 200ms", fr, NewPatterns(), store, ch, 4)
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	pollerDone := make(chan struct{})
	go func() { _ = p.Run(ctx); close(pollerDone) }()
	// LIFO: this cleanup runs FIRST (before the updater-wait above).
	// We cancel the ctx and wait for the poller's cron.Stop().Done()
	// drain to complete before the updater finishes consuming queued
	// StateUpdates.
	t.Cleanup(func() {
		cancel()
		select {
		case <-pollerDone:
		case <-time.After(2 * time.Second):
			t.Errorf("poller did not exit within 2s on cleanup")
		}
	})

	// Wait for at least one full sweep to complete (10 calls * 30ms / 4
	// workers = ~75ms minimum; generous wall-clock budget for loaded
	// test machines, especially under `go test ./...`).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := fr.callCounts(); n >= 10 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()

	peak := atomic.LoadInt32(&fr.maxInFlight)
	if peak > 4 {
		t.Errorf("Phase-3 pitfall: errgroup.SetLimit(4) not respected — peak in-flight = %d", peak)
	}
	if peak < 1 {
		t.Errorf("no workers observed in-flight; resolver was not called concurrently")
	}
	n, _ := fr.callCounts()
	if n < 10 {
		t.Errorf("want >= 10 resolver calls (one per container), got %d", n)
	}
}

func TestPoller_FetchSendsDigestResolvedUpdate(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	seedContainer(t, store, state.Container{
		Service:       "svc",
		Image:         "ghcr.io/centroid-is/svc",
		Tag:           "latest",
		CurrentDigest: "sha256:olddigest",
	})

	fr := newFakeResolver()
	fr.digestScript["ghcr.io/centroid-is/svc:latest"] = "sha256:fakedigest"

	ch := make(chan StateUpdate, 64)
	ctx, cancel := context.WithCancel(context.Background())
	updater := newDrainUpdater(store)
	updaterDone := make(chan struct{})
	go func() {
		updater.Run(ctx, ch)
		close(updaterDone)
	}()
	// Cleanup ordering matters here: t.Cleanup runs in LIFO order so
	// this updater-wait must be registered BEFORE the poller-wait below
	// (the poller-wait registers later → runs first → cancel() inside
	// it stops the poller → the cron Stop drains in-flight ticks →
	// after the poller exits, the updater-wait below cancels(ctx) again
	// and waits for the channel drain to finish).
	t.Cleanup(func() {
		select {
		case <-updaterDone:
		case <-time.After(2 * time.Second):
			t.Errorf("updater did not exit within 2s on cleanup")
		}
	})

	p, err := newPollerForTest("@every 100ms", fr, NewPatterns(), store, ch, 4)
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	pollerDone := make(chan struct{})
	go func() { _ = p.Run(ctx); close(pollerDone) }()
	// LIFO: this cleanup runs FIRST (before the updater-wait above).
	// We cancel the ctx and wait for the poller's cron.Stop().Done()
	// drain to complete before the updater finishes consuming queued
	// StateUpdates.
	t.Cleanup(func() {
		cancel()
		select {
		case <-pollerDone:
		case <-time.After(2 * time.Second):
			t.Errorf("poller did not exit within 2s on cleanup")
		}
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, ok := store.Get().Containers["svc"]
		if ok && c.AvailableDigest == "sha256:fakedigest" {
			if !c.UpdateAvailable {
				t.Errorf("UpdateAvailable: want true (currentDigest != availableDigest), got false")
			}
			if c.LastPolledAt.IsZero() {
				t.Errorf("LastPolledAt: want non-zero, got zero")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := store.Get().Containers["svc"]
	t.Fatalf("svc.AvailableDigest never reached sha256:fakedigest within 1s; got=%+v", got)
}

func TestPoller_RespectsContext(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	fr := newFakeResolver()
	ch := make(chan StateUpdate, 64)
	ctx, cancel := context.WithCancel(context.Background())
	updater := newDrainUpdater(store)
	updaterDone := make(chan struct{})
	go func() {
		updater.Run(ctx, ch)
		close(updaterDone)
	}()
	t.Cleanup(func() {
		select {
		case <-updaterDone:
		case <-time.After(2 * time.Second):
			t.Errorf("updater did not exit within 2s on cleanup")
		}
	})

	p, err := newPollerForTest("@every 5s", fr, NewPatterns(), store, ch, 4)
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		// We expect ctx.Err() back; the exact identity (Canceled) is fine.
		if err == nil {
			t.Errorf("Run(ctx): want non-nil err on ctx cancel, got nil")
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("Run(ctx) did not return within 1s after cancel")
	}
}

// TestPoller_TagMismatchNote_ClearedOnSuccessfulFetch (WR-04) asserts
// the symmetric clear behaviour for noteTagMismatch: once the operator
// fixes the running tag (or the regex), the next successful sweep MUST
// remove the stale note via clearStaleErrorNotes. Implementation was
// already correct (handleFetchResult invokes clearStaleErrorNotes on
// success); this test pins the contract so a future regression is
// caught.
//
// Scenario:
//  1. Container "svc" has Tag="latest" and Notes pre-seeded to the
//     mismatch literal (simulating an earlier sweep that surfaced the
//     mismatch). No tag-pattern set on the patterns cache — so the
//     poller WILL fetch on the next tick (running tag matches the
//     "no constraint" default).
//  2. fakeResolver returns a digest successfully.
//  3. After at least one successful sweep, Notes is empty.
func TestPoller_TagMismatchNote_ClearedOnSuccessfulFetch(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	seedContainer(t, store, state.Container{
		Service: "svc",
		Image:   "ghcr.io/centroid-is/svc",
		Tag:     "latest",
		Notes:   "running tag does not match tag-pattern label",
	})

	fr := newFakeResolver()
	fr.digestScript["ghcr.io/centroid-is/svc:latest"] = "sha256:fakedigest"

	ch := make(chan StateUpdate, 64)
	ctx, cancel := context.WithCancel(context.Background())
	updater := newDrainUpdater(store)
	updaterDone := make(chan struct{})
	go func() {
		updater.Run(ctx, ch)
		close(updaterDone)
	}()
	t.Cleanup(func() {
		select {
		case <-updaterDone:
		case <-time.After(2 * time.Second):
			t.Errorf("updater did not exit within 2s on cleanup")
		}
	})

	// No pattern set on the patterns cache — running tag "latest"
	// passes the permissive default, so the sweep WILL call resolver.
	p, err := newPollerForTest("@every 100ms", fr, NewPatterns(), store, ch, 4)
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	pollerDone := make(chan struct{})
	go func() { _ = p.Run(ctx); close(pollerDone) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-pollerDone:
		case <-time.After(2 * time.Second):
			t.Errorf("poller did not exit within 2s on cleanup")
		}
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, ok := store.Get().Containers["svc"]
		if ok && c.AvailableDigest == "sha256:fakedigest" && c.Notes == "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := store.Get().Containers["svc"]
	t.Errorf("WR-04: want Notes='' (cleared on successful fetch) after digest resolved, got Notes=%q AvailableDigest=%q",
		got.Notes, got.AvailableDigest)
}

// TestPoller_PersistentInvalidTagPatternNote_PreservedAcrossFetch (WR-07)
// pins the symmetric invariant on the error path:
//   - sendFetchError MUST preserve the persistent
//     "invalid tag-pattern label, ignored" note when the cron sweep's
//     fetch fails for a container whose Notes already carry that
//     literal.
//   - On a successful fetch, clearStaleErrorNotes ALSO preserves the
//     invalid-tag-pattern note (it's not a stale-error class — it
//     reflects a static container property).
//
// Implementation already correct (sendFetchError has an explicit
// pinned-OR-invalid early-return; clearStaleErrorNotes only removes
// noteTagMismatch and noteRegistryPrefix-prefixed notes). This test
// pins both contracts.
func TestPoller_PersistentInvalidTagPatternNote_PreservedAcrossFetch(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	const persistentNote = "invalid tag-pattern label, ignored"
	seedContainer(t, store, state.Container{
		Service: "svc",
		Image:   "ghcr.io/centroid-is/svc",
		Tag:     "latest",
		Notes:   persistentNote,
	})

	fr := newFakeResolver()
	// Successful fetch — exercises clearStaleErrorNotes preserve path.
	fr.digestScript["ghcr.io/centroid-is/svc:latest"] = "sha256:fakedigest"

	ch := make(chan StateUpdate, 64)
	ctx, cancel := context.WithCancel(context.Background())
	updater := newDrainUpdater(store)
	updaterDone := make(chan struct{})
	go func() {
		updater.Run(ctx, ch)
		close(updaterDone)
	}()
	t.Cleanup(func() {
		select {
		case <-updaterDone:
		case <-time.After(2 * time.Second):
			t.Errorf("updater did not exit within 2s on cleanup")
		}
	})

	// No tag-pattern set → permissive default → resolver IS called.
	p, err := newPollerForTest("@every 100ms", fr, NewPatterns(), store, ch, 4)
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	pollerDone := make(chan struct{})
	go func() { _ = p.Run(ctx); close(pollerDone) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-pollerDone:
		case <-time.After(2 * time.Second):
			t.Errorf("poller did not exit within 2s on cleanup")
		}
	})

	// Wait until the digest has resolved (proves a full sweep ran +
	// handleFetchResult committed). The Notes MUST still carry the
	// persistent literal AFTER the successful fetch.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, ok := store.Get().Containers["svc"]
		if ok && c.AvailableDigest == "sha256:fakedigest" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := store.Get().Containers["svc"]
	if got.AvailableDigest != "sha256:fakedigest" {
		t.Fatalf("AvailableDigest never resolved; got=%+v", got)
	}
	if got.Notes != persistentNote {
		t.Errorf("WR-07: persistent invalid-tag-pattern note must survive successful fetch; want %q, got %q",
			persistentNote, got.Notes)
	}
}

func TestPoller_PermanentErrorSurfacesNote(t *testing.T) {
	t.Parallel()
	store := newSafeStore(t)
	seedContainer(t, store, state.Container{
		Service: "svc",
		Image:   "ghcr.io/centroid-is/notfound",
		Tag:     "latest",
	})

	fr := newFakeResolver()
	// Wrap so errors.Is(err, registry.ErrPermanent) succeeds — the
	// resolver's own classify() wraps with %w, but here we synthesize
	// the wrapped form directly to avoid pulling crane into the test.
	fr.digestErrScript["ghcr.io/centroid-is/notfound:latest"] = fmt.Errorf("simulated 404: %w", registry.ErrPermanent)

	ch := make(chan StateUpdate, 64)
	ctx, cancel := context.WithCancel(context.Background())
	updater := newDrainUpdater(store)
	updaterDone := make(chan struct{})
	go func() {
		updater.Run(ctx, ch)
		close(updaterDone)
	}()
	// Cleanup ordering matters here: t.Cleanup runs in LIFO order so
	// this updater-wait must be registered BEFORE the poller-wait below
	// (the poller-wait registers later → runs first → cancel() inside
	// it stops the poller → the cron Stop drains in-flight ticks →
	// after the poller exits, the updater-wait below cancels(ctx) again
	// and waits for the channel drain to finish).
	t.Cleanup(func() {
		select {
		case <-updaterDone:
		case <-time.After(2 * time.Second):
			t.Errorf("updater did not exit within 2s on cleanup")
		}
	})

	p, err := newPollerForTest("@every 100ms", fr, NewPatterns(), store, ch, 4)
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	pollerDone := make(chan struct{})
	go func() { _ = p.Run(ctx); close(pollerDone) }()
	// LIFO: this cleanup runs FIRST (before the updater-wait above).
	// We cancel the ctx and wait for the poller's cron.Stop().Done()
	// drain to complete before the updater finishes consuming queued
	// StateUpdates.
	t.Cleanup(func() {
		cancel()
		select {
		case <-pollerDone:
		case <-time.After(2 * time.Second):
			t.Errorf("poller did not exit within 2s on cleanup")
		}
	})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, ok := store.Get().Containers["svc"]
		if ok && strings.HasPrefix(c.Notes, "registry error: permanent") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := store.Get().Containers["svc"]
	t.Errorf("want Notes prefix 'registry error: permanent', got %q", got.Notes)
}
