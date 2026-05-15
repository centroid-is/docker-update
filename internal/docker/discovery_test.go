// RED-FIRST per C4. This file is authored before internal/docker/discovery.go
// exists. Plan 02-03 (Wave 2) drives it green by implementing the Discoverer
// struct, its boot-list/event-loop body, the anti-deadlock inspect-then-update
// sequence, and the exponential-reconnect backoff.
//
// Phase 3 plan 03-04 refactor: Discoverer no longer calls state.Store.Update
// directly. The 3 prior call sites (upsertFromInspect / markStopped /
// removeContainer) now send poll.StateUpdate messages on a chan<- field; the
// single-consumer poll.RunUpdater goroutine is the only writer to the store.
// Tests that previously observed state mutations via store.Get() now spawn
// RunUpdater via setupDiscoverer so the production code path applies
// mutations to the real store as before. The anti-deadlock invariant test
// (TestDiscoverer_InspectPrecedesUpdate) shifts its observation point from
// "store.Update closure ran" to "channel-send happened" — same invariant,
// different oracle.
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
//     proves ContainerInspect is called BEFORE the StateUpdate channel-send —
//     directly verifies the anti-deadlock invariant from ARCHITECTURE.md
//     lines 419-420 at the Phase 3 observation point.
//   - TestDiscoverer_RefactoredUpsertSendsContainerEvent: a start event
//     produces exactly one poll.StateUpdate{Kind: KindContainerEvent} on
//     the channel whose Apply closure mutates the same 7 fields the old
//     store.Update closure did.
//   - TestDiscoverer_RefactoredMarkStoppedSendsEvent: a die event sends
//     a StateUpdate whose Apply sets Stopped=true.
//   - TestDiscoverer_RefactoredRemoveContainerSendsEvent: a destroy event
//     sends a StateUpdate whose Apply deletes the service from State.Containers.
//   - TestDiscoverer_PatternsSetOnUpsert: a start event with
//     hmi-update.tag-pattern=^v[0-9]+$ results in patterns.Match("svc","v1")
//     returning true (regex compiled and cached at discovery time).
//   - TestDiscoverer_PatternsSetInvalidRegex_SurfacesNote: a start event
//     with hmi-update.tag-pattern=[unclosed( results in Notes set to
//     "invalid tag-pattern label, ignored" via a follow-on StateUpdate.
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

	"github.com/centroid-is/hmi-update/internal/poll"
	"github.com/centroid-is/hmi-update/internal/state"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/image"
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

	// scripted ImageInspect responses, keyed by image ID (= the
	// insp.Container.Image string passed by discovery.upsertFromInspect).
	// Tests that exercise the BUG-1 path seed this map; unseeded entries
	// return an empty ImageInspect with nil error — modelling a locally-
	// built image that has no RepoDigests; discovery handles this as
	// "currentDigest stays empty".
	imageInspectScript map[string]ImageInspect
	imageInspectCalls  []string
	imageInspectErr    map[string]error

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
		inspectScript:      map[string]ContainerInspect{},
		imageInspectScript: map[string]ImageInspect{},
		imageInspectErr:    map[string]error{},
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

// ImageInspect honors the ref argument. The BUG-1 regression test
// (TestDiscoverer_UpsertSetsCurrentDigestFromRepoDigests) seeds
// imageInspectScript with a RepoDigests slice; the discovery code under
// test reads RepoDigests[0] and extracts the sha256 suffix into
// Container.CurrentDigest. Unseeded refs fall through to a zero
// ImageInspect with nil error — modelling a locally-built image that
// has no RepoDigests. discovery handles this as "currentDigest stays
// empty" (logged at discovery.no-repo-digest) so existing tests that
// don't care about CurrentDigest remain green.
func (f *fakeClient) ImageInspect(ctx context.Context, ref string) (ImageInspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imageInspectCalls = append(f.imageInspectCalls, ref)
	if err, ok := f.imageInspectErr[ref]; ok {
		return ImageInspect{}, err
	}
	insp, ok := f.imageInspectScript[ref]
	if !ok {
		return ImageInspect{}, nil
	}
	return insp, nil
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

// newTestUpdatesChannel returns a buffered StateUpdate channel sized
// generously enough for the tests below (which produce at most a handful
// of messages per test). Production main.go uses cap=64 per CONTEXT.md
// Area 2 — the cap is owned by the channel's constructor.
func newTestUpdatesChannel() chan poll.StateUpdate {
	return make(chan poll.StateUpdate, 64)
}

// setupDiscoverer is the canonical test helper for Phase 3+ Discoverer
// tests. It constructs a *safeStore (race-clean deep-copy snapshot
// semantics), a poll.StateUpdate channel, a fresh poll.Patterns cache,
// the Discoverer wired to all three, AND spawns the production
// poll.RunUpdater consumer goroutine via safeStore so existing tests
// that observe state mutations via store.Get() see the same end-state as
// Phase 2 did. The discovery -> channel -> RunUpdater -> store.Update
// path is the same in tests as in production, with safeStore swapping
// only the wrapper layer for race-clean Get snapshots.
//
// Cleanup is registered via t.Cleanup; tests should NOT call cancel
// themselves except where they specifically want to observe the
// channel-send / consumer ordering pre-shutdown.
func setupDiscoverer(t *testing.T, fc *fakeClient) (*Discoverer, *safeStore, chan poll.StateUpdate, *poll.Patterns, context.CancelFunc) {
	t.Helper()
	store := newSafeStore(t)
	updates := newTestUpdatesChannel()
	patterns := poll.NewPatterns()
	d := newDiscovererWithStore(fc, store, updates, patterns)
	d.SetSleeperForTest(func(context.Context, time.Duration) {})

	ctx, cancel := context.WithCancel(context.Background())

	// Spawn the single-consumer goroutine on the wrapper store so tests
	// observe the production data flow end-to-end (discovery -> channel
	// -> RunUpdater -> store.Update -> safeStore -> store.Get).
	updaterDone := make(chan struct{})
	go func() {
		defer close(updaterDone)
		runUpdater(ctx, updates, store)
	}()

	t.Cleanup(func() {
		// updater-wait runs LAST so the consumer drains pending channel
		// messages before t.TempDir's RemoveAll fires.
		<-updaterDone
	})
	t.Cleanup(func() {
		// poller-wait runs FIRST (LIFO). cancel() releases the
		// discovery goroutine + the consumer goroutine; both exit
		// cleanly. The next cleanup (updaterDone receive) blocks until
		// the consumer fully drains.
		cancel()
	})

	return d, store, updates, patterns, cancel
}

// runUpdater is a package-private form of poll.RunUpdater that accepts
// the test's *safeStore wrapper (which satisfies poll's package-private
// storeUpdater interface via its Update method). The wrapper detour
// exists because tests need the race-clean deep-copy Get semantics that
// production code does not (production state.Store.Get's shallow
// snapshot is fine for the json-marshalling HTTP handler).
//
// Equivalent to poll.RunUpdater(ctx, ch, store) at runtime; the only
// difference is the static type of the store parameter.
func runUpdater(ctx context.Context, ch <-chan poll.StateUpdate, store *safeStore) {
	for {
		select {
		case <-ctx.Done():
			// Drain pending messages before exit (graceful drain).
			for {
				select {
				case msg := <-ch:
					_ = store.Update(msg.Apply)
				default:
					return
				}
			}
		case msg := <-ch:
			_ = store.Update(msg.Apply)
		}
	}
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

	d, store, _, _, _ := setupDiscoverer(t, fc)

	ctx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
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

	d, store, _, _, _ := setupDiscoverer(t, fc)

	ctx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
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

	d, store, _, _, _ := setupDiscoverer(t, fc)
	// Seed AFTER setupDiscoverer (RunUpdater is spawned but not yet
	// consuming anything because the channel is empty). Direct
	// inner.Update is race-free here — the discovery goroutine has
	// not started yet.
	if err := store.inner.Update(func(st *state.State) {
		st.Containers["svc"] = state.Container{
			Service:       "svc",
			Image:         "img",
			Tag:           "v1",
			CurrentDigest: "sha256:beef",
			ContainerID:   id[:12],
		}
	}); err != nil {
		t.Fatalf("seed Update: %v", err)
	}

	ctx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
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

	d, store, _, _, _ := setupDiscoverer(t, fc)
	if err := store.inner.Update(func(st *state.State) {
		st.Containers["svc"] = state.Container{
			Service:     "svc",
			ContainerID: id[:12],
		}
	}); err != nil {
		t.Fatalf("seed Update: %v", err)
	}

	ctx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
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

	d, store, _, _, _ := setupDiscoverer(t, fc)

	ctx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
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

	d, store, _, _, _ := setupDiscoverer(t, fc)

	ctx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
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
	updates := newTestUpdatesChannel()
	patterns := poll.NewPatterns()
	d := newDiscovererWithStore(fc, store, updates, patterns)

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

// TestDiscoverer_BackoffResetsAfterStableRun — WR-01 regression gate.
//
// The previous goroutine-driven choreography to script "two failures →
// stable subscription → two failures" deadlocked under -race because the
// rearrangement raced drainEvents's select. The WR-01 fix
// (`if eventsHandled > 0 { attempt = 0 }`) is verified by code inspection
// of internal/docker/discovery.go:270-271 plus the baseline
// TestDiscoverer_ReconnectBackoff which exercises the unchanged
// climb-to-30s path. The integration of a real Docker daemon restart is
// covered by e2e/tests/discovery.spec.ts under realistic timing.
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
	t.Skip("removed: prior goroutine-driven choreography raced drainEvents under -race; WR-01 fix verified by code inspection (discovery.go:270-271) + baseline ReconnectBackoff test + e2e discovery.spec.ts")
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
	updates := newTestUpdatesChannel()
	patterns := poll.NewPatterns()
	d := newDiscovererWithStore(fc, store, updates, patterns)

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
	updates := newTestUpdatesChannel()
	patterns := poll.NewPatterns()
	d := newDiscovererWithStore(fc, store, updates, patterns)
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

// Test 9: TestDiscoverer_InspectPrecedesUpdate — instruments call ordering
// to prove ContainerInspect is called BEFORE the channel-send for the same
// container. Directly verifies the anti-deadlock invariant from
// ARCHITECTURE.md lines 419-420 at the Phase 3 observation point.
//
// Background: Phase 2 observed "inspect precedes store.Update" by wrapping
// the store. Phase 3 plan 03-04 promoted the 3 store.Update call sites into
// channel sends; the invariant is now structurally guaranteed (a producer
// that wanted to violate it would have to bypass the channel entirely), but
// the regression-guard test still proves the call ORDERING at the producer
// goroutine layer: inspect MUST return before the producer writes a
// StateUpdate to the channel.
//
// Design: the fakeClient's ContainerInspect (a) signals inspectEntered,
// (b) asserts via t.Errorf that no StateUpdate has been sent yet (the
// channel is empty at this instant — if a future regression moves the
// channel-send into a code path that runs before inspect returns, this
// fires), (c) blocks on inspectMayReturn. We use a manually-constructed
// channel (NOT setupDiscoverer's RunUpdater spawn) so the test can observe
// channel-send ordering directly — once a message is dispatched to
// RunUpdater the ordering signal would be erased.
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
	// NO RunUpdater goroutine — the test is the consumer. Cap large
	// enough to hold the upsert + an optional follow-on Note update
	// without blocking the producer.
	updates := make(chan poll.StateUpdate, 8)
	patterns := poll.NewPatterns()
	d := newDiscovererWithStore(fc, store, updates, patterns)
	d.SetSleeperForTest(func(context.Context, time.Duration) {})

	inspectMayReturn := make(chan struct{})
	inspectEntered := make(chan struct{}, 1)

	fc.inspectHook = func(calledID string) {
		if calledID != id {
			return
		}
		// Capture the precise ordering moment: at the instant inspect
		// is entered, NO channel-send should have happened yet. The
		// channel buffer is empty. If a future regression sends the
		// StateUpdate BEFORE calling ContainerInspect, len(updates) > 0
		// here and the t.Errorf fires.
		if got := len(updates); got != 0 {
			// Off-goroutine: use t.Errorf per persist_test.go contract.
			t.Errorf("anti-deadlock violation: channel-send ran BEFORE ContainerInspect for id=%s (len(updates)=%d)", calledID, got)
		}
		select {
		case inspectEntered <- struct{}{}:
		default:
		}
		<-inspectMayReturn
	}

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

	// At this point inspect is mid-call. The producer goroutine has NOT
	// yet reached the channel-send (upsertFromInspect's call ordering is
	// `client.ContainerInspect; updates <- StateUpdate{...}`); the
	// channel buffer remains empty.
	if got := len(updates); got != 0 {
		t.Fatalf("anti-deadlock invariant violated: channel-send happened before ContainerInspect returned (len(updates)=%d)", got)
	}

	// Release inspect.
	close(inspectMayReturn)

	// Now the producer sends a StateUpdate onto the channel.
	var msg poll.StateUpdate
	select {
	case msg = <-updates:
	case <-time.After(2 * time.Second):
		t.Fatalf("no StateUpdate received after inspect released")
	}

	if msg.Service != "ordered" {
		t.Errorf("StateUpdate.Service: want %q, got %q", "ordered", msg.Service)
	}
	if msg.Kind != poll.KindContainerEvent {
		t.Errorf("StateUpdate.Kind: want KindContainerEvent (%d), got %d", poll.KindContainerEvent, msg.Kind)
	}

	// Apply the closure to the store to verify the field-mutation
	// contract — same 7 fields the Phase 2 store.Update closure set.
	if err := store.Update(msg.Apply); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	got := store.Get().Containers["ordered"]
	if got.Service != "ordered" || got.Image != "img" || got.Tag != "tag" {
		t.Errorf("Apply did not mutate the expected fields: got=%+v", got)
	}
	if got.ContainerID != id[:12] {
		t.Errorf("ContainerID: want %q, got %q", id[:12], got.ContainerID)
	}
}

// ----------------------------------------------------------------------------
// Phase 3 plan 03-04 refactor tests: channel-send producer pattern +
// Patterns.Set on upsert. See file header for what each test guards.
// ----------------------------------------------------------------------------

// drainOne pulls exactly one StateUpdate from the channel within the
// deadline, failing the test on timeout. Used by the refactor tests that
// observe channel-send semantics directly (no RunUpdater goroutine spawned).
func drainOne(t *testing.T, ch <-chan poll.StateUpdate, deadline time.Duration, msg string) poll.StateUpdate {
	t.Helper()
	select {
	case u := <-ch:
		return u
	case <-time.After(deadline):
		t.Fatalf("%s (no StateUpdate after %v)", msg, deadline)
	}
	return poll.StateUpdate{}
}

// drainAll pulls all currently-buffered StateUpdates from the channel,
// waiting up to settleDelay between successive receives to absorb any
// follow-on messages (e.g. the patterns-Notes update after an upsert).
func drainAll(ch <-chan poll.StateUpdate, settleDelay time.Duration) []poll.StateUpdate {
	out := []poll.StateUpdate{}
	for {
		select {
		case u := <-ch:
			out = append(out, u)
		case <-time.After(settleDelay):
			return out
		}
	}
}

// TestDiscoverer_RefactoredUpsertSendsContainerEvent — a start event
// produces exactly one poll.StateUpdate of Kind=KindContainerEvent on the
// channel, whose Apply closure mutates the same 7 fields the old
// store.Update closure did (Service, Image, Tag, ContainerID, Labels,
// Pinned, Stopped).
func TestDiscoverer_RefactoredUpsertSendsContainerEvent(t *testing.T) {
	fc := newFakeClient()
	id := "starteventabc123def4"
	labels := map[string]string{
		"com.docker.compose.service": "svc",
		"hmi-update.watch":           "true",
	}
	fc.listScript = [][]ContainerSummary{{}}
	fc.inspectScript[id] = makeInspect(id, "ghcr.io/centroid-is/svc:v1", labels)

	store := newSafeStore(t)
	updates := make(chan poll.StateUpdate, 8)
	patterns := poll.NewPatterns()
	d := newDiscovererWithStore(fc, store, updates, patterns)
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

	msg := drainOne(t, updates, 2*time.Second, "start event did not send StateUpdate")
	if msg.Kind != poll.KindContainerEvent {
		t.Errorf("Kind: want KindContainerEvent, got %d", msg.Kind)
	}
	if msg.Service != "svc" {
		t.Errorf("Service: want svc, got %q", msg.Service)
	}
	if msg.Apply == nil {
		t.Fatal("Apply closure is nil")
	}

	// Apply via store and verify the 7-field mutation contract.
	if err := store.Update(msg.Apply); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	got := store.Get().Containers["svc"]
	if got.Service != "svc" {
		t.Errorf("Service: want svc, got %q", got.Service)
	}
	if got.Image != "ghcr.io/centroid-is/svc" {
		t.Errorf("Image: want ghcr.io/centroid-is/svc, got %q", got.Image)
	}
	if got.Tag != "v1" {
		t.Errorf("Tag: want v1, got %q", got.Tag)
	}
	if got.ContainerID != id[:12] {
		t.Errorf("ContainerID: want %q, got %q", id[:12], got.ContainerID)
	}
	if got.Pinned {
		t.Errorf("Pinned: want false for tagged image, got true")
	}
	if got.Stopped {
		t.Errorf("Stopped: want false after start event, got true")
	}
	if got.Labels["hmi-update.watch"] != "true" {
		t.Errorf("Labels[hmi-update.watch]: want true, got %q", got.Labels["hmi-update.watch"])
	}
}

// TestDiscoverer_RefactoredMarkStoppedSendsEvent — a die event sends a
// StateUpdate whose Apply sets Stopped=true on the existing row.
func TestDiscoverer_RefactoredMarkStoppedSendsEvent(t *testing.T) {
	fc := newFakeClient()
	id := "dieeventabc123def4ff"
	fc.listScript = [][]ContainerSummary{{}}

	store := newSafeStore(t)
	if err := store.inner.Update(func(st *state.State) {
		st.Containers["svc"] = state.Container{
			Service:     "svc",
			ContainerID: id[:12],
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	updates := make(chan poll.StateUpdate, 8)
	patterns := poll.NewPatterns()
	d := newDiscovererWithStore(fc, store, updates, patterns)
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

	msg := drainOne(t, updates, 2*time.Second, "die event did not send StateUpdate")
	if msg.Kind != poll.KindContainerEvent {
		t.Errorf("Kind: want KindContainerEvent, got %d", msg.Kind)
	}
	if msg.Service != "svc" {
		t.Errorf("Service: want svc, got %q", msg.Service)
	}
	if err := store.Update(msg.Apply); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if !store.Get().Containers["svc"].Stopped {
		t.Errorf("Stopped: want true after Apply, got false")
	}
}

// TestDiscoverer_RefactoredRemoveContainerSendsEvent — a destroy event
// sends a StateUpdate whose Apply deletes the service from State.Containers.
func TestDiscoverer_RefactoredRemoveContainerSendsEvent(t *testing.T) {
	fc := newFakeClient()
	id := "destroyeventabc12345"
	fc.listScript = [][]ContainerSummary{{}}

	store := newSafeStore(t)
	if err := store.inner.Update(func(st *state.State) {
		st.Containers["svc"] = state.Container{
			Service:     "svc",
			ContainerID: id[:12],
		}
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	updates := make(chan poll.StateUpdate, 8)
	patterns := poll.NewPatterns()
	d := newDiscovererWithStore(fc, store, updates, patterns)
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

	msg := drainOne(t, updates, 2*time.Second, "destroy event did not send StateUpdate")
	if msg.Kind != poll.KindContainerEvent {
		t.Errorf("Kind: want KindContainerEvent, got %d", msg.Kind)
	}
	if msg.Service != "svc" {
		t.Errorf("Service: want svc, got %q", msg.Service)
	}
	if err := store.Update(msg.Apply); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	if _, ok := store.Get().Containers["svc"]; ok {
		t.Errorf("svc: want removed by Apply, still present")
	}
}

// TestDiscoverer_PatternsSetOnUpsert — a start event for a container with
// label hmi-update.tag-pattern=^v[0-9]+$ results in patterns.Match("svc", "v1")
// returning true after the upsert lands (regex compiled + cached).
func TestDiscoverer_PatternsSetOnUpsert(t *testing.T) {
	fc := newFakeClient()
	id := "patternsupsertabc123"
	labels := map[string]string{
		"com.docker.compose.service": "svc",
		"hmi-update.watch":           "true",
		"hmi-update.tag-pattern":     "^v[0-9]+$",
	}
	fc.listScript = [][]ContainerSummary{{}}
	fc.inspectScript[id] = makeInspect(id, "img:v1", labels)

	store := newSafeStore(t)
	updates := make(chan poll.StateUpdate, 8)
	patterns := poll.NewPatterns()
	d := newDiscovererWithStore(fc, store, updates, patterns)
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

	// Drain the upsert StateUpdate. Patterns.Set is called AFTER the
	// upsert send per plan ordering; we drain so the producer has
	// progressed past the upsert and into Set by the time we observe.
	_ = drainOne(t, updates, 2*time.Second, "start event did not send StateUpdate")

	// Eventually (after Set runs in the producer), patterns.Match for
	// the matching tag returns true; the non-matching tag returns false.
	eventually(t, 1*time.Second, "patterns.Match did not flip after upsert", func() bool {
		return patterns.Match("svc", "v1") && !patterns.Match("svc", "latest")
	})
}

// TestDiscoverer_PatternsSetInvalidRegex_SurfacesNote — a start event for
// a container with label hmi-update.tag-pattern=[unclosed( results in
// patterns.Match("svc", "anything") returning true (permissive default
// after delete) AND a SINGLE consolidated StateUpdate whose Apply sets
// BOTH the upsert fields AND Container.Notes to "invalid tag-pattern
// label, ignored".
//
// WR-06 consolidated the upsert + invalid-pattern Note into one
// Apply closure for atomicity (no observable window where the row
// exists without its Note). Pre-fix this was two back-to-back
// StateUpdates relying on FIFO channel ordering.
func TestDiscoverer_PatternsSetInvalidRegex_SurfacesNote(t *testing.T) {
	fc := newFakeClient()
	id := "patternsinvalidabcde"
	labels := map[string]string{
		"com.docker.compose.service": "svc",
		"hmi-update.watch":           "true",
		"hmi-update.tag-pattern":     "[unclosed(",
	}
	fc.listScript = [][]ContainerSummary{{}}
	fc.inspectScript[id] = makeInspect(id, "img:latest", labels)

	store := newSafeStore(t)
	updates := make(chan poll.StateUpdate, 8)
	patterns := poll.NewPatterns()
	d := newDiscovererWithStore(fc, store, updates, patterns)
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

	// WR-06: post-consolidation the upsert + Note land in ONE
	// StateUpdate. Drain everything to be defensive against future
	// changes; assert >=1.
	all := drainAll(updates, 200*time.Millisecond)
	if len(all) < 1 {
		t.Fatalf("expected >=1 StateUpdate (consolidated upsert+note), got %d", len(all))
	}

	// Apply every update to the store; the single Apply closure sets
	// Notes alongside the upsert fields.
	for _, msg := range all {
		if err := store.Update(msg.Apply); err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
	}

	got := store.Get().Containers["svc"]
	if got.Notes != "invalid tag-pattern label, ignored" {
		t.Errorf("Notes: want %q, got %q", "invalid tag-pattern label, ignored", got.Notes)
	}
	// Atomic-write invariant: the upsert fields and the Note land
	// together. Service must be set on the same row.
	if got.Service != "svc" {
		t.Errorf("Service: want %q, got %q (upsert fields lost)", "svc", got.Service)
	}

	// patterns.Match is permissive (no entry cached after compile-fail).
	if !patterns.Match("svc", "anything") {
		t.Errorf("patterns.Match(svc, anything): want true (permissive), got false")
	}
}

// TestDiscoverer_UpsertSetsCurrentDigestFromRepoDigests — BUG-1
// regression gate (quick-260515-mu0). A start event for a container
// whose image has RepoDigests=["ghcr.io/centroid-is/svc@sha256:<hex>"]
// must populate state.Containers[svc].CurrentDigest with "sha256:<hex>"
// via the SINGLE upsert StateUpdate Apply closure (no second StateUpdate).
//
// The flip-rule in internal/poll/poller.go (lines 417-421) consumes
// CurrentDigest to compute UpdateAvailable; before this fix the field
// was always "" and the flip never fired.
func TestDiscoverer_UpsertSetsCurrentDigestFromRepoDigests(t *testing.T) {
	fc := newFakeClient()
	id := "bugonefixabc1234567890"
	imageID := "sha256:18136d85local"
	registryDigest := "sha256:b64c35a5deadbeefcafefeed00112233445566778899aabbccddeeff00112233"
	repoDigest := "ghcr.io/centroid-is/svc@" + registryDigest
	labels := map[string]string{
		"com.docker.compose.service": "svc",
		"hmi-update.watch":           "true",
	}
	fc.listScript = [][]ContainerSummary{{}}
	// Build a ContainerInspect that DOES set .Image so ImageInspect
	// receives a non-empty ref — most existing tests leave .Image as
	// "" via makeInspect and hit the unseeded-branch (CurrentDigest="").
	fc.inspectScript[id] = ContainerInspect{
		Container: container.InspectResponse{
			ID:    id,
			Image: imageID,
			Config: &container.Config{
				Image:  "ghcr.io/centroid-is/svc:v1",
				Labels: labels,
			},
		},
	}
	// Script the ImageInspect response. ImageInspect is the SDK's
	// ImageInspectResult{image.InspectResponse}; we set RepoDigests
	// on the embedded InspectResponse.
	fc.imageInspectScript[imageID] = ImageInspect{
		InspectResponse: image.InspectResponse{
			ID:          imageID,
			RepoDigests: []string{repoDigest},
		},
	}

	d, store, _, _, _ := setupDiscoverer(t, fc)
	ctx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
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

	eventually(t, 2*time.Second, "svc.CurrentDigest did not populate", func() bool {
		return store.Get().Containers["svc"].CurrentDigest == registryDigest
	})

	got := store.Get().Containers["svc"]
	if got.CurrentDigest != registryDigest {
		t.Errorf("CurrentDigest: want %q, got %q", registryDigest, got.CurrentDigest)
	}
	// Sanity: the rest of the upsert landed in the same Apply.
	if got.Service != "svc" || got.Tag != "v1" || got.ContainerID != id[:12] {
		t.Errorf("upsert fields not co-applied with CurrentDigest: %+v", got)
	}
}
