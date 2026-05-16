// Package recreate (test). recreate_test.go covers the failure-mode
// catalog from 09-RESEARCH.md § Architecture Patterns / Pattern 3:
//
//   - happy path                        → returns new ID, Stop+Remove+Create+Start in order
//   - Stop fails                        → OLD untouched, no Create
//   - Remove fails (after Stop)         → no Create
//   - Create fails (after Stop+Remove)  → error contains "old GONE", no Start
//   - NetworkConnect fails              → ContainerRemove(newID) cleanup
//   - Start fails                       → ContainerRemove(newID) cleanup
//
// The tests use a recording fakeClient that satisfies docker.Client.
// It records every method call in arrival order so the call-sequence
// assertions are exact.
//
// Goroutine assertion contract: all tests run synchronously on the
// goroutine that called Service; no t.Errorf-vs-t.Fatal discipline
// concern (Pattern I does not apply).
package recreate

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/centroid-is/docker-update/internal/docker"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

// ----------------------------------------------------------------------------
// fakeClient — recording docker.Client implementation
// ----------------------------------------------------------------------------

// callRecord tags a single recorded call by name so assertions can
// scan the slice for ordering invariants. The string convention is
// "Method[/arg]" — e.g. "Stop/old-id-1", "Create/svc-a", "Remove/new-id-2".
type callRecord struct {
	op string
}

// fakeClient implements docker.Client. Behaviour is purely scripted:
//
//   - listResult drives ContainerList (single response, same on every call).
//   - listErr injects a list-time error.
//   - inspectResult drives ContainerInspect for the OLD id; absence
//     surfaces a hard test error (recreate.Service should only inspect
//     the OLD id, never the NEW one).
//   - stopErr / removeErrs[id] / createErr / connectErr / startErr
//     drive each step's failure mode.
//   - newID is the id ContainerCreate returns on success.
//
// All recorded calls land in `calls` in order.
type fakeClient struct {
	listResult []docker.ContainerSummary
	listErr    error

	inspectResult docker.ContainerInspect
	inspectErr    error

	stopErr     error
	removeErrs  map[string]error // keyed by id
	createErr   error
	connectErr  error
	startErr    error

	newID string

	calls []callRecord
}

func newFake() *fakeClient {
	return &fakeClient{
		removeErrs: map[string]error{},
	}
}

// Compile-time guard: fakeClient must satisfy docker.Client.
var _ docker.Client = (*fakeClient)(nil)

// ---- docker.Client interface methods (alphabetical, recreate-relevant first) ----

func (f *fakeClient) ContainerList(ctx context.Context, opts docker.ContainerListOptions) ([]docker.ContainerSummary, error) {
	f.calls = append(f.calls, callRecord{op: "List"})
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listResult, nil
}

func (f *fakeClient) ContainerInspect(ctx context.Context, id string) (docker.ContainerInspect, error) {
	f.calls = append(f.calls, callRecord{op: "Inspect/" + id})
	if f.inspectErr != nil {
		return docker.ContainerInspect{}, f.inspectErr
	}
	return f.inspectResult, nil
}

func (f *fakeClient) ContainerStop(ctx context.Context, id string, opts docker.ContainerStopOptions) error {
	f.calls = append(f.calls, callRecord{op: "Stop/" + id})
	return f.stopErr
}

func (f *fakeClient) ContainerRemove(ctx context.Context, id string, opts docker.ContainerRemoveOptions) error {
	f.calls = append(f.calls, callRecord{op: "Remove/" + id})
	if err, ok := f.removeErrs[id]; ok {
		return err
	}
	return nil
}

func (f *fakeClient) ContainerCreate(ctx context.Context, opts docker.ContainerCreateOptions) (docker.ContainerCreateResult, error) {
	f.calls = append(f.calls, callRecord{op: "Create/" + opts.Name})
	if f.createErr != nil {
		return docker.ContainerCreateResult{}, f.createErr
	}
	return docker.ContainerCreateResult{ID: f.newID}, nil
}

func (f *fakeClient) ContainerStart(ctx context.Context, id string, opts docker.ContainerStartOptions) error {
	f.calls = append(f.calls, callRecord{op: "Start/" + id})
	return f.startErr
}

func (f *fakeClient) NetworkConnect(ctx context.Context, networkID string, opts docker.NetworkConnectOptions) error {
	f.calls = append(f.calls, callRecord{op: "NetworkConnect/" + networkID + "->" + opts.Container})
	return f.connectErr
}

// ---- unused interface methods (return zero values; recreate.Service never calls them) ----

func (f *fakeClient) Ping(ctx context.Context) error { return nil }
func (f *fakeClient) Events(ctx context.Context, opts docker.EventsListOptions) (<-chan docker.EventMessage, <-chan error) {
	ev := make(chan docker.EventMessage)
	er := make(chan error)
	go func() { <-ctx.Done(); close(ev); close(er) }()
	return ev, er
}
func (f *fakeClient) ImagePull(ctx context.Context, ref string, opts docker.ImagePullOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (f *fakeClient) ImageInspect(ctx context.Context, ref string) (docker.ImageInspect, error) {
	return docker.ImageInspect{}, nil
}
func (f *fakeClient) ImageTag(ctx context.Context, src, dst string) error { return nil }
func (f *fakeClient) ImageList(ctx context.Context, opts docker.ImageListOptions) ([]docker.ImageSummary, error) {
	return nil, nil
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// singleNetworkInspect returns a minimal but valid InspectResponse with
// one network ("net-a"). Use when the failure-mode under test doesn't
// care about extras-network plumbing.
func singleNetworkInspect(name string) docker.ContainerInspect {
	return docker.ContainerInspect{
		Container: container.InspectResponse{
			ID:         "old-full-id-1234567890abcdef",
			Name:       "/" + name,
			Config:     &container.Config{Image: "ghcr.io/x/" + name + ":latest"},
			HostConfig: &container.HostConfig{},
			NetworkSettings: &container.NetworkSettings{
				Networks: map[string]*network.EndpointSettings{
					"net-a": {Aliases: []string{name}},
				},
			},
		},
	}
}

// twoNetworkInspect returns an InspectResponse with TWO networks so the
// NetworkConnect-fails branch has an extras-network to drive through.
func twoNetworkInspect(name string) docker.ContainerInspect {
	return docker.ContainerInspect{
		Container: container.InspectResponse{
			ID:         "old-full-id-aabbccddeeff00112233",
			Name:       "/" + name,
			Config:     &container.Config{Image: "ghcr.io/x/" + name + ":latest"},
			HostConfig: &container.HostConfig{},
			NetworkSettings: &container.NetworkSettings{
				Networks: map[string]*network.EndpointSettings{
					"net-a": {Aliases: []string{name}},
					"net-b": {Aliases: []string{name + "-b"}},
				},
			},
		},
	}
}

// summaryFor builds a one-element ContainerList result that satisfies
// the lookup step (oldest-by-Created == newest because there's only one).
func summaryFor(id string) []docker.ContainerSummary {
	return []docker.ContainerSummary{{ID: id, Created: 1}}
}

// callOps extracts the recorded op strings in order; convenience for
// assertion error messages.
func (f *fakeClient) callOps() []string {
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.op)
	}
	return out
}

// ----------------------------------------------------------------------------
// Test 1: happy path — Stop, Remove, Create, Start (no extras → no NetworkConnect)
// ----------------------------------------------------------------------------

func TestService_HappyPath_ReturnsNewID(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.listResult = summaryFor("old-1")
	f.inspectResult = singleNetworkInspect("svc-a")
	f.newID = "new-1"

	id, err := Service(context.Background(), f, "svc-a")
	if err != nil {
		t.Fatalf("Service: %v (calls=%v)", err, f.callOps())
	}
	if id != "new-1" {
		t.Errorf("returned ID: want %q, got %q", "new-1", id)
	}

	want := []string{
		"List",
		"Inspect/old-1",
		"Stop/old-1",
		"Remove/old-1",
		"Create/svc-a",
		"Start/new-1",
	}
	got := f.callOps()
	if len(got) != len(want) {
		t.Fatalf("call sequence length: want %d, got %d (calls=%v)", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("call[%d]: want %q, got %q (full=%v)", i, w, got[i], got)
		}
	}
}

// ----------------------------------------------------------------------------
// Test 2: Stop fails — OLD untouched, NO Remove / Create / Start
// ----------------------------------------------------------------------------

func TestService_StopFails_OldUntouched_NoCreate(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.listResult = summaryFor("old-2")
	f.inspectResult = singleNetworkInspect("svc-a")
	f.stopErr = errors.New("daemon: stop failed: device or resource busy")

	_, err := Service(context.Background(), f, "svc-a")
	if err == nil {
		t.Fatalf("Service: want non-nil err on Stop-fails, got nil")
	}
	if !strings.Contains(err.Error(), "stop") {
		t.Errorf("err message: want to mention 'stop', got %q", err.Error())
	}

	for _, op := range f.callOps() {
		if strings.HasPrefix(op, "Remove/") {
			t.Errorf("post-Stop-fail: Remove MUST NOT be called; got %v", f.callOps())
		}
		if strings.HasPrefix(op, "Create/") {
			t.Errorf("post-Stop-fail: Create MUST NOT be called; got %v", f.callOps())
		}
		if strings.HasPrefix(op, "Start/") {
			t.Errorf("post-Stop-fail: Start MUST NOT be called; got %v", f.callOps())
		}
	}
}

// ----------------------------------------------------------------------------
// Test 3: Remove fails (after Stop succeeds) — NO Create / Start
// ----------------------------------------------------------------------------

func TestService_RemoveFails_NoCreate(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.listResult = summaryFor("old-3")
	f.inspectResult = singleNetworkInspect("svc-a")
	f.removeErrs["old-3"] = errors.New("daemon: remove failed: container still in use")

	_, err := Service(context.Background(), f, "svc-a")
	if err == nil {
		t.Fatalf("Service: want non-nil err on Remove-fails, got nil")
	}
	if !strings.Contains(err.Error(), "remove") {
		t.Errorf("err message: want to mention 'remove', got %q", err.Error())
	}

	for _, op := range f.callOps() {
		if strings.HasPrefix(op, "Create/") {
			t.Errorf("post-Remove-fail: Create MUST NOT be called; got %v", f.callOps())
		}
		if strings.HasPrefix(op, "Start/") {
			t.Errorf("post-Remove-fail: Start MUST NOT be called; got %v", f.callOps())
		}
	}
}

// ----------------------------------------------------------------------------
// Test 4: Create fails AFTER Stop+Remove — error message contains "old GONE";
// NO Start.
// ----------------------------------------------------------------------------

func TestService_CreateFails_OldGone_NoLeak(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.listResult = summaryFor("old-4")
	f.inspectResult = singleNetworkInspect("svc-a")
	f.createErr = errors.New("daemon: create failed: name already in use")

	_, err := Service(context.Background(), f, "svc-a")
	if err == nil {
		t.Fatalf("Service: want non-nil err on Create-fails, got nil")
	}
	if !strings.Contains(err.Error(), "old GONE") {
		t.Errorf("err message: want 'old GONE' marker (Pattern 3 unrecoverable boundary), got %q", err.Error())
	}

	for _, op := range f.callOps() {
		if strings.HasPrefix(op, "Start/") {
			t.Errorf("post-Create-fail: Start MUST NOT be called; got %v", f.callOps())
		}
	}
}

// ----------------------------------------------------------------------------
// Test 5: NetworkConnect (on the SECOND network) fails — best-effort
// ContainerRemove(newID, Force) cleanup; NO Start.
// ----------------------------------------------------------------------------

func TestService_NetworkConnectFails_CleanupNewContainer(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.listResult = summaryFor("old-5")
	f.inspectResult = twoNetworkInspect("svc-a")
	f.newID = "new-5"
	f.connectErr = errors.New("daemon: network not found")

	_, err := Service(context.Background(), f, "svc-a")
	if err == nil {
		t.Fatalf("Service: want non-nil err on NetworkConnect-fails, got nil")
	}

	// The cleanup MUST issue ContainerRemove against the NEW id (new-5),
	// NOT the OLD id (old-5, which is already gone).
	var sawNewRemove bool
	for _, op := range f.callOps() {
		if op == "Remove/new-5" {
			sawNewRemove = true
		}
		if strings.HasPrefix(op, "Start/") {
			t.Errorf("post-NetworkConnect-fail: Start MUST NOT be called; got %v", f.callOps())
		}
	}
	if !sawNewRemove {
		t.Errorf("post-NetworkConnect-fail: Remove(new-5, Force) cleanup MUST be called; got %v", f.callOps())
	}
}

// ----------------------------------------------------------------------------
// Test 6: Start fails — best-effort ContainerRemove(newID, Force) cleanup.
// ----------------------------------------------------------------------------

func TestService_StartFails_CleanupNewContainer(t *testing.T) {
	t.Parallel()
	f := newFake()
	f.listResult = summaryFor("old-6")
	f.inspectResult = singleNetworkInspect("svc-a")
	f.newID = "new-6"
	f.startErr = errors.New("daemon: start failed: OCI runtime exec failure")

	_, err := Service(context.Background(), f, "svc-a")
	if err == nil {
		t.Fatalf("Service: want non-nil err on Start-fails, got nil")
	}

	var sawNewRemove bool
	for _, op := range f.callOps() {
		if op == "Remove/new-6" {
			sawNewRemove = true
		}
	}
	if !sawNewRemove {
		t.Errorf("post-Start-fail: Remove(new-6, Force) cleanup MUST be called; got %v", f.callOps())
	}
}

// ----------------------------------------------------------------------------
// Bonus: no container found (lookup returns empty) — clean error, no work
// ----------------------------------------------------------------------------

func TestService_NoContainerForService_ReturnsCleanError(t *testing.T) {
	t.Parallel()
	f := newFake()
	// listResult left empty.

	_, err := Service(context.Background(), f, "ghost-svc")
	if err == nil {
		t.Fatalf("Service on absent service: want non-nil err, got nil")
	}
	if !strings.Contains(err.Error(), "no container for service") {
		t.Errorf("err message: want 'no container for service', got %q", err.Error())
	}
	if len(f.calls) != 1 || f.calls[0].op != "List" {
		t.Errorf("calls on absent service: want exactly [List], got %v", f.callOps())
	}
}
