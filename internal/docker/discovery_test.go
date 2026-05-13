// RED-FIRST per C4. This file is authored before internal/docker/discovery.go
// exists. Plan 02-03 (Wave 2) drives it green by implementing the Discoverer
// struct, its boot-list/event-loop body, the anti-deadlock inspect-then-update
// sequence, and the exponential-reconnect backoff.
//
// What this test file guards:
//
//   - TestDiscoverer_BootList_PopulatesState: the boot ContainerList call
//     correctly maps a ContainerSummary into a state.Container row, with the
//     four new Phase-2 fields (ContainerID/Labels/Pinned/Stopped) populated.
//   - TestDiscoverer_StartEvent_UpsertsContainer: a `start` event triggers
//     ContainerInspect and upserts the result via state.Store.Update.
//   - TestDiscoverer_DieEvent_SetsStopped: a `die` event sets Stopped=true
//     and preserves every other field on the row.
//   - TestDiscoverer_DestroyEvent_RemovesRow: a `destroy` event deletes the
//     row entirely.
//   - TestDiscoverer_PinnedDetection: an image ref containing @sha256:
//     sets Container.Pinned=true.
//   - TestParseImageRef_RegistryPrefixed: the parseImageRef helper handles
//     registry-prefixed refs (port-colon vs tag-colon ambiguity).
//   - TestDiscoverer_LabelFilter: only hmi-update.* labels flow into
//     Container.Labels; compose.* and OCI labels are stripped.
//   - TestDiscoverer_ReconnectBackoff: exponential backoff progression
//     1s, 2s, 4s, 8s, 16s, 30s-cap, asserting on 10 consecutive failures.
//   - TestDiscoverer_ReconnectTriggersBootList: after a successful reconnect
//     the boot ContainerList is run again to recover state changes that
//     happened during the disconnect.
//   - TestDiscoverer_InspectPrecedesUpdate: instrumented channel ordering
//     proves ContainerInspect is called BEFORE state.Store.Update —
//     directly verifies the anti-deadlock invariant from ARCHITECTURE.md
//     lines 419-420.
//
// Goroutine assertion contract (per persist_test.go lines 29-31): assertions
// fired off-goroutine use t.Errorf, NEVER t.Fatal — t.Fatal inside a goroutine
// only halts the goroutine that calls it and leaves the test to pass falsely.
package docker

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centroid-is/hmi-update/internal/state"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
)

// ----------------------------------------------------------------------------
// fakeClient — implements docker.Client, scripted by the test.
// ----------------------------------------------------------------------------

// fakeClient is a programmable docker.Client implementation used by the
// Discoverer tests. It records call counts/arguments, returns scripted
// ContainerList / ContainerInspect responses, and exposes a hand-driven
// event channel pair so the test can push events and close the stream
// with an error to exercise the reconnect path.
//
// All script fields are guarded by mu. The events channel is recreated on
// every Events() call so the reconnect test can observe N consecutive
// subscription attempts.
type fakeClient struct {
	mu sync.Mutex

	// scripted ContainerList responses, one per call. Repeats the last
	// entry if the script runs out — so a single-element script seeds the
	// boot list and any subsequent reconnect-triggered re-list.
	listScript [][]ContainerSummary
	listCalls  int

	// scripted ContainerInspect responses, keyed by container ID.
	inspectScript map[string]ContainerInspect
	inspectCalls  []string // captured IDs in order

	// scripted Events behaviour:
	//   eventsReturn governs the events/err channel pair returned by Events().
	//   eventsCalls counts every Events subscription attempt.
	//   eventsErrToSend is the error to push on the err channel before
	//     closing it (so the reconnect path fires). nil = "do not push;
	//     just leave the channels open for the test to drive."
	eventsCalls     int
	eventsErrToSend error
	eventsCh        chan EventMessage
	errCh           chan error

	// Optional hook called at the entry of ContainerInspect — used by
	// TestDiscoverer_InspectPrecedesUpdate to instrument call ordering.
	inspectHook func(id string)
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		inspectScript: map[string]ContainerInspect{},
	}
}

func (f *fakeClient) Ping(ctx context.Context) error { return nil }

func (f *fakeClient) ContainerList(ctx context.Context, opts ContainerListOptions) ([]ContainerSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := f.listCalls
	f.listCalls++
	if idx >= len(f.listScript) {
		if len(f.listScript) == 0 {
			return nil, nil
		}
		return f.listScript[len(f.listScript)-1], nil
	}
	return f.listScript[idx], nil
}

func (f *fakeClient) ContainerInspect(ctx context.Context, id string) (ContainerInspect, error) {
	f.mu.Lock()
	hook := f.inspectHook
	insp, ok := f.inspectScript[id]
	f.inspectCalls = append(f.inspectCalls, id)
	f.mu.Unlock()
	if hook != nil {
		hook(id)
	}
	if !ok {
		return ContainerInspect{}, errors.New("fakeClient: no inspect scripted for id " + id)
	}
	return insp, nil
}

// Events returns a fresh channel pair on every call. If eventsErrToSend is
// set, it is dispatched on err before the channels are returned, so the
// drainEvents loop sees an immediate failure and the reconnect-backoff
// machinery kicks in.
func (f *fakeClient) Events(ctx context.Context, opts EventsListOptions) (<-chan EventMessage, <-chan error) {
	f.mu.Lock()
	f.eventsCalls++
	f.eventsCh = make(chan EventMessage, 8)
	f.errCh = make(chan error, 1)
	if f.eventsErrToSend != nil {
		f.errCh <- f.eventsErrToSend
	}
	ch := f.eventsCh
	errCh := f.errCh
	f.mu.Unlock()
	return ch, errCh
}

func (f *fakeClient) ImagePull(ctx context.Context, ref string, opts ImagePullOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeClient) ImageTag(ctx context.Context, src, dst string) error { return nil }

// pushEvent sends a synthetic event over the latest Events channel. The
// caller should ensure Events() has already been invoked (the discovery
// goroutine subscribes at Run() entry, then again on every reconnect).
func (f *fakeClient) pushEvent(ev EventMessage) {
	f.mu.Lock()
	ch := f.eventsCh
	f.mu.Unlock()
	if ch != nil {
		ch <- ev
	}
}

func (f *fakeClient) callCounts() (listCalls, eventsCalls int, inspectIDs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, len(f.inspectCalls))
	copy(ids, f.inspectCalls)
	return f.listCalls, f.eventsCalls, ids
}

// ----------------------------------------------------------------------------
// helpers — store + Discoverer scaffolding
// ----------------------------------------------------------------------------

func newTestStore(t *testing.T) *state.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := state.NewStore(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("state.NewStore: %v", err)
	}
	return store
}

// safeStore wraps a *state.Store with its own RWMutex so the tests can
// take a DEEP-COPIED snapshot of the Containers map without racing the
// Discoverer's Update calls.
//
// Background: state.Store.Get returns a shallow snapshot whose inner
// Containers map is the SAME reference the Store mutates. The package's
// doc comment notes this is safe for the http /api/state handler (which
// json.Marshals the snapshot immediately), but a test goroutine that
// holds the snapshot pointer and reads it via map indexing while the
// Discoverer goroutine concurrently runs state.Store.Update trips the
// race detector. This wrapper serializes Get and Update through its own
// lock so Get can deep-copy the map under exclusive access. The deep
// copy itself is the only writer to the new map, so subsequent reads of
// the returned snapshot are race-clean.
//
// safeStore satisfies the package-private stateStore interface, so the
// tests construct the Discoverer via newDiscovererWithStore(fc, sstore).
type safeStore struct {
	mu    sync.RWMutex
	inner *state.Store
}

func newSafeStore(t *testing.T) *safeStore {
	t.Helper()
	return &safeStore{inner: newTestStore(t)}
}

// Get returns a snapshot whose Containers map is a freshly-allocated deep
// copy of the inner store's map. Holding the wrapper's write lock for the
// copy ensures no concurrent Update is running.
func (s *safeStore) Get() state.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.inner.Get()
	out := state.State{
		Version:    src.Version,
		Containers: make(map[string]state.Container, len(src.Containers)),
	}
	for k, v := range src.Containers {
		// Container is a value type with no nested pointer slices that
		// the discovery code mutates after handoff; v's Labels map is
		// the only inner reference. Phase 2 discovery writes a fresh
		// Labels map per Update (filterHmiLabels returns a new map),
		// so we shallow-copy the Labels reference here — it will not
		// be mutated post-write.
		out.Containers[k] = v
	}
	return out
}

// Update delegates to the inner store while holding the wrapper's write
// lock. Serializing Get and Update through the wrapper's mu means a
// concurrent Get's deep-copy step never overlaps with an in-flight Update
// closure (which mutates the inner map without the wrapper's awareness).
func (s *safeStore) Update(fn func(*state.State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inner.Update(fn)
}

// seedSafeStore mutates the inner store directly (no wrapper lock needed
// pre-Discoverer-start) and returns the wrapper. Used for tests that need
// to seed state before launching Discoverer.
func seedSafeStore(t *testing.T, fn func(*state.State)) *safeStore {
	t.Helper()
	s := newSafeStore(t)
	if err := s.inner.Update(fn); err != nil {
		t.Fatalf("seed Update: %v", err)
	}
	return s
}

// makeSummary builds a container.Summary with the fields Discoverer reads.
func makeSummary(id, image string, labels map[string]string) ContainerSummary {
	return container.Summary{
		ID:     id,
		Image:  image,
		Labels: labels,
	}
}

// makeInspect builds a ContainerInspect (= client.ContainerInspectResult)
// with the typed fields the Discoverer reads. The Config sub-struct holds
// the Image and Labels.
func makeInspect(id, image string, labels map[string]string) ContainerInspect {
	return ContainerInspect{
		Container: container.InspectResponse{
			ID: id,
			Config: &container.Config{
				Image:  image,
				Labels: labels,
			},
		},
	}
}

// eventually polls fn every 10ms until it returns true or the deadline
// elapses. Reports via t.Fatalf on timeout with the supplied message.
func eventually(t *testing.T, deadline time.Duration, msg string, fn func() bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("eventually timed out after %v: %s", deadline, msg)
}

// ----------------------------------------------------------------------------
// Tests
// ----------------------------------------------------------------------------

// Test 1: TestDiscoverer_BootList_PopulatesState — A FakeClient returns one
// ContainerSummary at boot. After Discoverer.Run(ctx) starts, state.Containers
// eventually contains the row with the correct mapped fields.
func TestDiscoverer_BootList_PopulatesState(t *testing.T) {
	fc := newFakeClient()
	id := "abc123def4567ffffffff"
	composeLabels := map[string]string{
		"com.docker.compose.service": "stub-watched-container",
		"hmi-update.watch":           "true",
		"org.opencontainers.image.title": "stub",
	}
	fc.listScript = [][]ContainerSummary{
		{makeSummary(id, "busybox:latest", composeLabels)},
	}
	fc.inspectScript[id] = makeInspect(id, "busybox:latest", composeLabels)

	store := newSafeStore(t)
	d := newDiscovererWithStore(fc, store)
	d.SetSleeperForTest(func(context.Context, time.Duration) {})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()

	eventually(t, 2*time.Second, "stub-watched-container did not appear in state", func() bool {
		_, ok := store.Get().Containers["stub-watched-container"]
		return ok
	})

	got := store.Get().Containers["stub-watched-container"]
	if got.Service != "stub-watched-container" {
		t.Errorf("Service: want stub-watched-container, got %q", got.Service)
	}
	if got.Image != "busybox" {
		t.Errorf("Image: want busybox, got %q", got.Image)
	}
	if got.Tag != "latest" {
		t.Errorf("Tag: want latest, got %q", got.Tag)
	}
	if got.ContainerID != id[:12] {
		t.Errorf("ContainerID: want %q, got %q", id[:12], got.ContainerID)
	}
	if got.Pinned {
		t.Errorf("Pinned: want false for tagged image, got true")
	}
	if got.Stopped {
		t.Errorf("Stopped: want false at boot, got true")
	}
	if got.Labels["hmi-update.watch"] != "true" {
		t.Errorf("Labels[hmi-update.watch]: want true, got %q", got.Labels["hmi-update.watch"])
	}
}

// Test 2: TestDiscoverer_StartEvent_UpsertsContainer — Boot returns 0 containers;
// FakeClient then emits a `start` event. Discoverer must call ContainerInspect
// exactly once for that ID and upsert the resulting Container.
func TestDiscoverer_StartEvent_UpsertsContainer(t *testing.T) {
	fc := newFakeClient()
	id := "abc123def4567ffffff"
	fc.listScript = [][]ContainerSummary{{}}
	fc.inspectScript[id] = makeInspect(id, "ghcr.io/centroid-is/svc:v1", map[string]string{
		"com.docker.compose.service": "svc",
		"hmi-update.watch":           "true",
	})

	store := newSafeStore(t)
	d := newDiscovererWithStore(fc, store)
	d.SetSleeperForTest(func(context.Context, time.Duration) {})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()

	// Wait for boot to land (zero containers).
	eventually(t, 1*time.Second, "discoverer did not subscribe to events", func() bool {
		_, evCalls, _ := fc.callCounts()
		return evCalls >= 1
	})

	fc.pushEvent(EventMessage{
		Type:   events.ContainerEventType,
		Action: events.ActionStart,
		Actor:  events.Actor{ID: id},
	})

	eventually(t, 2*time.Second, "svc did not appear after start event", func() bool {
		_, ok := store.Get().Containers["svc"]
		return ok
	})

	_, _, ids := fc.callCounts()
	inspectsFor := 0
	for _, calledID := range ids {
		if calledID == id {
			inspectsFor++
		}
	}
	if inspectsFor != 1 {
		t.Errorf("ContainerInspect call count for %s: want 1, got %d (all=%v)", id, inspectsFor, ids)
	}

	got := store.Get().Containers["svc"]
	if got.ContainerID != id[:12] {
		t.Errorf("ContainerID: want %q, got %q", id[:12], got.ContainerID)
	}
	if got.Image != "ghcr.io/centroid-is/svc" {
		t.Errorf("Image: want ghcr.io/centroid-is/svc, got %q", got.Image)
	}
	if got.Tag != "v1" {
		t.Errorf("Tag: want v1, got %q", got.Tag)
	}
}

// Test 3: TestDiscoverer_DieEvent_SetsStopped — Seed state with one container
// (Stopped=false); FakeClient emits `die` for that container's service. After
// processing, Stopped must be true; all other fields preserved.
func TestDiscoverer_DieEvent_SetsStopped(t *testing.T) {
	fc := newFakeClient()
	id := "abc123def4567aaaa"
	fc.listScript = [][]ContainerSummary{{}}

	store := seedSafeStore(t, func(st *state.State) {
		st.Containers["svc"] = state.Container{
			Service:       "svc",
			Image:         "img",
			Tag:           "v1",
			CurrentDigest: "sha256:beef",
			ContainerID:   id[:12],
		}
	})

	d := newDiscovererWithStore(fc, store)
	d.SetSleeperForTest(func(context.Context, time.Duration) {})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()

	eventually(t, 1*time.Second, "events subscribe", func() bool {
		_, evCalls, _ := fc.callCounts()
		return evCalls >= 1
	})

	fc.pushEvent(EventMessage{
		Type:   events.ContainerEventType,
		Action: events.ActionDie,
		Actor:  events.Actor{ID: id},
	})

	eventually(t, 2*time.Second, "Stopped did not flip true", func() bool {
		return store.Get().Containers["svc"].Stopped
	})

	got := store.Get().Containers["svc"]
	if !got.Stopped {
		t.Errorf("Stopped: want true, got false")
	}
	if got.CurrentDigest != "sha256:beef" {
		t.Errorf("CurrentDigest: want sha256:beef preserved, got %q", got.CurrentDigest)
	}
	if got.Service != "svc" || got.Image != "img" || got.Tag != "v1" {
		t.Errorf("other fields not preserved on die: %+v", got)
	}
}

// Test 4: TestDiscoverer_DestroyEvent_RemovesRow — Seed state with one container;
// emit `destroy`; the row must be removed.
func TestDiscoverer_DestroyEvent_RemovesRow(t *testing.T) {
	fc := newFakeClient()
	id := "abc123def4567xxxx"
	fc.listScript = [][]ContainerSummary{{}}

	store := seedSafeStore(t, func(st *state.State) {
		st.Containers["svc"] = state.Container{
			Service:     "svc",
			ContainerID: id[:12],
		}
	})

	d := newDiscovererWithStore(fc, store)
	d.SetSleeperForTest(func(context.Context, time.Duration) {})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()

	eventually(t, 1*time.Second, "events subscribe", func() bool {
		_, evCalls, _ := fc.callCounts()
		return evCalls >= 1
	})

	fc.pushEvent(EventMessage{
		Type:   events.ContainerEventType,
		Action: events.ActionDestroy,
		Actor:  events.Actor{ID: id},
	})

	eventually(t, 2*time.Second, "destroy did not remove row", func() bool {
		_, ok := store.Get().Containers["svc"]
		return !ok
	})
}

// Test 5: TestDiscoverer_PinnedDetection — Boot returns a ContainerSummary
// whose image reference contains @sha256:. After boot, Container.Pinned must
// be true.
func TestDiscoverer_PinnedDetection(t *testing.T) {
	fc := newFakeClient()
	id := "pinpinpinpinpin1234"
	ref := "ghcr.io/centroid-is/some-svc@sha256:abc1234deadbeefcafe"
	labels := map[string]string{
		"com.docker.compose.service": "pinned-svc",
		"hmi-update.watch":           "true",
	}
	fc.listScript = [][]ContainerSummary{
		{makeSummary(id, ref, labels)},
	}
	fc.inspectScript[id] = makeInspect(id, ref, labels)

	store := newSafeStore(t)
	d := newDiscovererWithStore(fc, store)
	d.SetSleeperForTest(func(context.Context, time.Duration) {})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()

	eventually(t, 2*time.Second, "pinned-svc did not appear", func() bool {
		_, ok := store.Get().Containers["pinned-svc"]
		return ok
	})
	got := store.Get().Containers["pinned-svc"]
	if !got.Pinned {
		t.Errorf("Pinned: want true for @sha256: ref, got false (row=%+v)", got)
	}
}

// Test 5b: TestParseImageRef_RegistryPrefixed — direct unit test exercising the
// last-colon-not-followed-by-slash heuristic for registry-prefixed refs.
func TestParseImageRef_RegistryPrefixed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ref       string
		wantImage string
		wantTag   string
	}{
		// Port colon must NOT be split: tag would be "5000/foo" and the
		// Phase 3 digest poller would issue manifest requests against the
		// wrong upstream.
		{"localhost:5000/foo", "localhost:5000/foo", "latest"},
		// Final colon DOES split — no slash follows it.
		{"ghcr.io:443/centroid-is/svc:v1", "ghcr.io:443/centroid-is/svc", "v1"},
		// Bare image: defaults to latest.
		{"busybox", "busybox", "latest"},
		// Standard image:tag.
		{"busybox:1.36", "busybox", "1.36"},
		// Pinned ref: @sha256: terminator wins; tag emptied.
		{"img@sha256:abc", "img", ""},
	}
	for _, tc := range cases {
		t.Run(tc.ref, func(t *testing.T) {
			gotImage, gotTag := parseImageRef(tc.ref)
			if gotImage != tc.wantImage || gotTag != tc.wantTag {
				t.Errorf("parseImageRef(%q) = (%q, %q), want (%q, %q)",
					tc.ref, gotImage, gotTag, tc.wantImage, tc.wantTag)
			}
		})
	}
}

// Test 6: TestDiscoverer_LabelFilter — Container.Labels must contain ONLY
// hmi-update.* keys. compose.* and OCI labels must NOT appear.
func TestDiscoverer_LabelFilter(t *testing.T) {
	fc := newFakeClient()
	id := "labeltest123456abcd"
	labels := map[string]string{
		"com.docker.compose.service":     "labeled-svc",
		"com.docker.compose.project":     "test",
		"org.opencontainers.image.title": "noise",
		"hmi-update.watch":               "true",
		"hmi-update.tag-pattern":         "^latest-pg17$",
		"hmi-update.allow-update":        "false",
	}
	fc.listScript = [][]ContainerSummary{{makeSummary(id, "img:tag", labels)}}
	fc.inspectScript[id] = makeInspect(id, "img:tag", labels)

	store := newSafeStore(t)
	d := newDiscovererWithStore(fc, store)
	d.SetSleeperForTest(func(context.Context, time.Duration) {})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()

	eventually(t, 2*time.Second, "labeled-svc did not appear", func() bool {
		_, ok := store.Get().Containers["labeled-svc"]
		return ok
	})

	got := store.Get().Containers["labeled-svc"]
	if len(got.Labels) != 3 {
		t.Errorf("Labels count: want 3 hmi-update.* keys, got %d (labels=%v)", len(got.Labels), got.Labels)
	}
	for k := range got.Labels {
		if !strings.HasPrefix(k, "hmi-update.") {
			t.Errorf("Labels contains non-hmi-update key %q (full=%v)", k, got.Labels)
		}
	}
	// Verify specific noise keys are excluded.
	for _, noise := range []string{
		"com.docker.compose.service",
		"com.docker.compose.project",
		"org.opencontainers.image.title",
	} {
		if _, ok := got.Labels[noise]; ok {
			t.Errorf("Labels leaked noise key %q (full=%v)", noise, got.Labels)
		}
	}
}

// Test 7: TestDiscoverer_ReconnectBackoff — FakeClient's Events channel closes
// with an error immediately. Reconnect must invoke Events again after a
// back-off delay following 1s, 2s, 4s, 8s, 16s, 30s-cap.
func TestDiscoverer_ReconnectBackoff(t *testing.T) {
	fc := newFakeClient()
	fc.listScript = [][]ContainerSummary{{}}
	fc.eventsErrToSend = errors.New("synthetic disconnect")

	store := newSafeStore(t)
	d := newDiscovererWithStore(fc, store)

	var (
		mu     sync.Mutex
		sleeps []time.Duration
		done   = make(chan struct{})
	)
	d.SetSleeperForTest(func(_ context.Context, dur time.Duration) {
		mu.Lock()
		sleeps = append(sleeps, dur)
		stop := len(sleeps) >= 10
		mu.Unlock()
		if stop {
			select {
			case <-done:
			default:
				close(done)
			}
			// Block until ctx cancels — keeps the goroutine alive for
			// the test's cancel deferred call.
			time.Sleep(50 * time.Millisecond)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("did not observe 10 reconnect backoffs in 5s; sleeps=%v", sleeps)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		30 * time.Second,
		30 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}
	if len(sleeps) < 10 {
		t.Fatalf("not enough sleeps captured: want >=10, got %d (%v)", len(sleeps), sleeps)
	}
	for i, w := range want {
		if sleeps[i] != w {
			t.Errorf("sleeps[%d]: want %v, got %v (all=%v)", i, w, sleeps[i], sleeps)
		}
	}
}

// TestDiscoverer_BackoffResetsAfterStableRun — WR-01 regression.
//
// Pre-fix behaviour: `attempt` climbed monotonically for the life of the
// process. A 12-hour-stable subscription that finally lost its stream
// would inherit the climbing counter from the boot-time reconnect cluster
// and start the next backoff at 30s — even though the prior subscription
// had been healthy for hours.
//
// Post-fix behaviour: when drainEvents handled >=1 event during the
// subscription window, the eventsLoop resets `attempt` to 0 BEFORE the
// next increment. The next backoff therefore starts from 1s, matching
// the spec's "1s, 2s, 4s, up to 30s" progression on each fresh failure
// cluster.
//
// Test design: drive two failure clusters separated by a stable
// subscription that handles one real event. Sequence:
//
//  1. Boot list (empty), Events() #1 fails immediately → sleep 1s.
//  2. Events() #2 fails immediately → sleep 2s.
//  3. Events() #3 — disarmed (eventsErrToSend cleared); after Events()
//     hands back the channels, the test pushes a real start event;
//     handleEvent ticks eventsHandled to 1; then the test re-arms
//     eventsErrToSend and closes the channel to force a fresh drain
//     exit. eventsHandled > 0 → attempt resets → sleep 1s.
//  4. Events() #4 fails immediately → attempt is now 1 again → sleep 2s.
//
// The assertion that sleeps[2] == 1s (not 4s) is the WR-01 fix gate.
func TestDiscoverer_BackoffResetsAfterStableRun(t *testing.T) {
	fc := newFakeClient()
	fc.listScript = [][]ContainerSummary{{}}

	// inspectScript for the event we will push during the "stable"
	// subscription window. The discoverer's upsertFromInspect path
	// calls ContainerInspect → state.Store.Update — only then does
	// drainEvents tick eventsHandled to 1 (handleEvent returns).
	id := "stableabcdef0123456789"
	fc.inspectScript[id] = makeInspect(id, "img:tag", map[string]string{
		"com.docker.compose.service": "stable-svc",
		"hmi-update.watch":           "true",
	})

	store := newSafeStore(t)
	d := newDiscovererWithStore(fc, store)

	var (
		mu     sync.Mutex
		sleeps []time.Duration
		done   = make(chan struct{})
	)

	syntheticErr := errors.New("synthetic")
	fc.mu.Lock()
	fc.eventsErrToSend = syntheticErr
	fc.mu.Unlock()

	// stableArmed gates the one-shot rearrangement: when the SECOND
	// sleep is being recorded, the loop is about to make its THIRD
	// Events() call. We disarm the error, push a real event, wait for
	// it to be handled (state.Containers["stable-svc"] populated), then
	// re-arm the error and close the channel pair so drainEvents exits
	// with eventsHandled=1.
	var stableArmed atomic.Bool

	d.SetSleeperForTest(func(_ context.Context, dur time.Duration) {
		mu.Lock()
		sleeps = append(sleeps, dur)
		n := len(sleeps)
		mu.Unlock()

		if n == 2 && !stableArmed.Swap(true) {
			// Run the rearrangement in a separate goroutine — the
			// sleeper itself must return promptly so the loop can
			// progress to Events() call #3.
			go func() {
				// 1. Disarm so Events() #3 returns clean channels.
				fc.mu.Lock()
				fc.eventsErrToSend = nil
				fc.mu.Unlock()

				// 2. Wait until Events() #3 has been called.
				deadline := time.Now().Add(2 * time.Second)
				for time.Now().Before(deadline) {
					_, ec, _ := fc.callCounts()
					if ec >= 3 {
						break
					}
					time.Sleep(2 * time.Millisecond)
				}

				// 3. Push a real event so drainEvents handles it.
				fc.pushEvent(EventMessage{
					Type:   events.ContainerEventType,
					Action: events.ActionStart,
					Actor:  events.Actor{ID: id},
				})

				// 4. Wait until the event has been handled (state row
				//    appears).
				deadline = time.Now().Add(2 * time.Second)
				for time.Now().Before(deadline) {
					if _, ok := store.Get().Containers["stable-svc"]; ok {
						break
					}
					time.Sleep(2 * time.Millisecond)
				}

				// 5. Re-arm the error AND inject it onto the current
				//    errCh so drainEvents exits this subscription with
				//    eventsHandled=1.
				fc.mu.Lock()
				fc.eventsErrToSend = syntheticErr
				ch := fc.errCh
				fc.mu.Unlock()
				if ch != nil {
					ch <- syntheticErr
				}
			}()
		}

		if n >= 4 {
			select {
			case <-done:
			default:
				close(done)
			}
			time.Sleep(20 * time.Millisecond)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		mu.Lock()
		t.Fatalf("did not observe 4 reconnect backoffs in 8s; sleeps=%v", sleeps)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sleeps) < 4 {
		t.Fatalf("not enough sleeps: want >=4, got %d (%v)", len(sleeps), sleeps)
	}
	// Expected progression with WR-01 fix:
	//   sleeps[0] = 1s  (first failure after boot)
	//   sleeps[1] = 2s  (second failure — exponential continues)
	//   sleeps[2] = 1s  (third subscription handled one event before
	//                    failing → attempt resets → backoff = 1s)
	//   sleeps[3] = 2s  (fourth subscription failed with 0 events → no
	//                    reset → attempt = 2 → backoff = 2s)
	want := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		1 * time.Second, // reset signal — this is the WR-01 fix
		2 * time.Second,
	}
	for i, w := range want {
		if sleeps[i] != w {
			t.Errorf("sleeps[%d]: want %v, got %v (all=%v) — WR-01 regression?", i, w, sleeps[i], sleeps)
		}
	}
}

// Test 8: TestDiscoverer_ReconnectTriggersBootList — After Events errors and
// the reconnect path fires, ContainerList is invoked again to recover state
// changes that happened during the gap.
func TestDiscoverer_ReconnectTriggersBootList(t *testing.T) {
	fc := newFakeClient()
	fc.listScript = [][]ContainerSummary{{}, {}}

	// Send one error then stop erroring so the loop progresses past the
	// first reconnect and the test can observe the second ContainerList
	// call without spinning forever.
	var sentOne atomic.Bool
	fc.eventsErrToSend = errors.New("first disconnect")

	store := newSafeStore(t)
	d := newDiscovererWithStore(fc, store)
	d.SetSleeperForTest(func(context.Context, time.Duration) {
		// After the first sleep, stop scripting errors so the next
		// Events() subscription returns clean channels.
		if !sentOne.Swap(true) {
			fc.mu.Lock()
			fc.eventsErrToSend = nil
			fc.mu.Unlock()
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()

	eventually(t, 3*time.Second, "ContainerList not called twice after reconnect", func() bool {
		lc, _, _ := fc.callCounts()
		return lc >= 2
	})
}

// ----------------------------------------------------------------------------
// recordingStore — wraps a *state.Store to signal when Update's closure
// has been invoked. Used by TestDiscoverer_InspectPrecedesUpdate.
// ----------------------------------------------------------------------------

// recordingStore is a thin wrapper around safeStore that signals when
// Update's closure has been invoked. Update flips an atomic.Bool the
// moment its closure starts executing — giving the test a race-free
// signal that ordering can be asserted against. Get delegates to safeStore
// (which deep-copies under its own lock, so the test never races on the
// inner map).
//
// Rationale: state.Store.Get returns a snapshot whose inner Containers
// map is the SAME reference as the store's working copy. Reading that
// map after Get returns happens without any lock; concurrent
// state.Store.Update writes to the same map trip the Go race detector.
// safeStore wraps Get with its own serialization + deep-copy step.
type recordingStore struct {
	inner         *safeStore
	updateInvoked *atomic.Bool
}

func (r *recordingStore) Get() state.State { return r.inner.Get() }

func (r *recordingStore) Update(fn func(*state.State)) error {
	// Flip the signal BEFORE delegating so the test's
	// "did Update enter the call frame?" check fires deterministically
	// even if the closure body is empty or panics.
	r.updateInvoked.Store(true)
	return r.inner.Update(fn)
}

// Test 9: TestDiscoverer_InspectPrecedesUpdate — instruments call ordering to
// prove ContainerInspect is called BEFORE state.Store.Update. Directly
// verifies the anti-deadlock invariant from ARCHITECTURE.md lines 419-420.
//
// Design: the fakeClient's ContainerInspect (a) signals inspectEntered,
// (b) asserts via t.Errorf that recordingStore.updateInvoked is still
// false, (c) blocks on inspectMayReturn. The recordingStore flips its
// atomic the instant Update's closure runs — which can only happen AFTER
// inspect returns (because Discoverer.upsertFromInspect's call ordering
// is `client.ContainerInspect; store.Update`). A future regression that
// moves inspect INTO the Update closure flips updateInvoked first, and
// the t.Errorf at step (b) fires.
func TestDiscoverer_InspectPrecedesUpdate(t *testing.T) {
	fc := newFakeClient()
	fc.listScript = [][]ContainerSummary{{}}

	id := "ordertesttarget01234"
	labels := map[string]string{
		"com.docker.compose.service": "ordered",
		"hmi-update.watch":           "true",
	}
	fc.inspectScript[id] = makeInspect(id, "img:tag", labels)

	store := newSafeStore(t)
	var updateInvoked atomic.Bool
	rec := &recordingStore{inner: store, updateInvoked: &updateInvoked}

	inspectMayReturn := make(chan struct{})
	inspectEntered := make(chan struct{}, 1)

	fc.inspectHook = func(calledID string) {
		if calledID != id {
			return
		}
		// Capture the precise ordering moment: at the instant inspect
		// is entered, updateInvoked MUST still be false. If a future
		// regression moves ContainerInspect into store.Update's
		// closure, recordingStore.Update sets updateInvoked FIRST and
		// this t.Errorf fires.
		if updateInvoked.Load() {
			// Off-goroutine: use t.Errorf per persist_test.go contract.
			t.Errorf("anti-deadlock violation: store.Update ran BEFORE ContainerInspect for id=%s", calledID)
		}
		select {
		case inspectEntered <- struct{}{}:
		default:
		}
		<-inspectMayReturn
	}

	d := newDiscovererWithStore(fc, rec)
	d.SetSleeperForTest(func(context.Context, time.Duration) {})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()

	eventually(t, 1*time.Second, "events subscribe", func() bool {
		_, evCalls, _ := fc.callCounts()
		return evCalls >= 1
	})

	fc.pushEvent(EventMessage{
		Type:   events.ContainerEventType,
		Action: events.ActionStart,
		Actor:  events.Actor{ID: id},
	})

	// Wait until inspect parks inside the hook.
	select {
	case <-inspectEntered:
	case <-time.After(2 * time.Second):
		t.Fatalf("ContainerInspect was never called for id=%s", id)
	}

	// At this point inspect is mid-call. updateInvoked MUST still be false.
	if updateInvoked.Load() {
		t.Fatalf("anti-deadlock invariant violated: store.Update ran before ContainerInspect returned")
	}

	// Release inspect.
	close(inspectMayReturn)

	// Now store.Update fires (recordingStore.Update flips updateInvoked).
	eventually(t, 1*time.Second, "store.Update did not fire after inspect released", func() bool {
		return updateInvoked.Load()
	})
}
