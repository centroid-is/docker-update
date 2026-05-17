// Package selfupdate (test). spawn_test.go covers the parent-side
// Spawner contract per 09-04-PLAN.md Task 1 + 09-RESEARCH.md
// § Architecture Patterns / Pattern 4.
//
// All tests use a recording fakeClient that records every docker.Client
// call in arrival order so the call-sequence + ContainerCreateOptions
// shape assertions are exact.
package selfupdate

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/centroid-is/docker-update/internal/docker"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

// ----------------------------------------------------------------------------
// fakeClient — recording docker.Client implementation
// ----------------------------------------------------------------------------

// fakeClient implements docker.Client for spawn_test.go. Only
// ContainerCreate / ContainerStart / ContainerRemove are recorded with
// argument detail; the other methods are zero-value stubs (Spawn never
// calls them).
type fakeClient struct {
	mu sync.Mutex

	// recordings
	createOpts   []docker.ContainerCreateOptions
	startCalls   []string // ids passed to ContainerStart
	removeCalls  []string // ids passed to ContainerRemove
	inspectCalls []string // ids passed to ContainerInspect (9-04-B)

	// injected behavior
	createErr             error
	startErr              error
	inspectErr            error  // makes ContainerInspect fail; used to verify Spawn surfaces the error
	returnID              string
	parentUser            string                                     // 9-04-B: returned via parentInspect.Container.Config.User
	parentHostNetworkMode string                                     // 9-04-C: returned via parentInspect.Container.HostConfig.NetworkMode
	parentNetworks        map[string]*network.EndpointSettings // 9-04-C: NetworkSettings.Networks fallback
}

func newFakeClient(returnID string) *fakeClient {
	return &fakeClient{returnID: returnID}
}

// compile-time guard
var _ docker.Client = (*fakeClient)(nil)

func (f *fakeClient) ContainerCreate(ctx context.Context, opts docker.ContainerCreateOptions) (docker.ContainerCreateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createOpts = append(f.createOpts, opts)
	if f.createErr != nil {
		return docker.ContainerCreateResult{}, f.createErr
	}
	return docker.ContainerCreateResult{ID: f.returnID}, nil
}

func (f *fakeClient) ContainerStart(ctx context.Context, id string, opts docker.ContainerStartOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls = append(f.startCalls, id)
	return f.startErr
}

func (f *fakeClient) ContainerRemove(ctx context.Context, id string, opts docker.ContainerRemoveOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeCalls = append(f.removeCalls, id)
	return nil
}

// ---- unused docker.Client methods — zero-value stubs ----

func (f *fakeClient) Ping(ctx context.Context) error { return nil }
func (f *fakeClient) ContainerList(ctx context.Context, opts docker.ContainerListOptions) ([]docker.ContainerSummary, error) {
	return nil, nil
}
func (f *fakeClient) ContainerInspect(ctx context.Context, id string) (docker.ContainerInspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspectCalls = append(f.inspectCalls, id)
	if f.inspectErr != nil {
		return docker.ContainerInspect{}, f.inspectErr
	}
	resp := container.InspectResponse{
		Config: &container.Config{User: f.parentUser},
	}
	if f.parentHostNetworkMode != "" {
		resp.HostConfig = &container.HostConfig{
			NetworkMode: container.NetworkMode(f.parentHostNetworkMode),
		}
	}
	if f.parentNetworks != nil {
		resp.NetworkSettings = &container.NetworkSettings{
			Networks: f.parentNetworks,
		}
	}
	return docker.ContainerInspect{Container: resp}, nil
}
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
func (f *fakeClient) ContainerStop(ctx context.Context, id string, opts docker.ContainerStopOptions) error {
	return nil
}
func (f *fakeClient) NetworkConnect(ctx context.Context, networkID string, opts docker.NetworkConnectOptions) error {
	return nil
}

// ----------------------------------------------------------------------------
// Test 1: BuildsCorrectContainerCreateOpts
//
// Pins the helper container's wire shape per RESEARCH.md Pattern 4 + the
// plan's truths block:
//   - Config.Cmd contains exactly ["--self-update-orchestrator", "--target=docker-update"]
//     (binary path is supplied by the image's Entrypoint=["/docker-update"];
//     including "docker-update" here produces a positional argv[1] that defeats
//     flag.Parse — see HMI smoke 2026-05-16 defect 9-04-A)
//   - Config.Labels has centroid.docker-update.helper="true"
//   - HostConfig.AutoRemove=true (keepHelper=false default)
//   - HostConfig.Binds includes /var/run/docker.sock:/var/run/docker.sock
//   - Image == selfImage
// ----------------------------------------------------------------------------

func TestSpawner_Spawn_BuildsCorrectContainerCreateOpts(t *testing.T) {
	t.Parallel()
	const (
		image  = "ghcr.io/centroid-is/docker-update:v1.2.3"
		target = "docker-update"
	)
	f := newFakeClient("helper-abc")
	sp := NewSpawner(f, image, target, nil, false)

	id, err := sp.Spawn(context.Background())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if id != "helper-abc" {
		t.Errorf("returned id: want %q, got %q", "helper-abc", id)
	}

	if len(f.createOpts) != 1 {
		t.Fatalf("ContainerCreate calls: want 1, got %d", len(f.createOpts))
	}
	opts := f.createOpts[0]

	// Config.Image
	if opts.Config == nil {
		t.Fatalf("opts.Config is nil")
	}
	if opts.Config.Image != image {
		t.Errorf("Config.Image: want %q, got %q", image, opts.Config.Image)
	}

	// Config.Cmd — exact match (no binary-name positional; Entrypoint provides it)
	wantCmd := []string{HelperCmdFlag, "--target=" + target}
	if len(opts.Config.Cmd) != len(wantCmd) {
		t.Fatalf("Config.Cmd length: want %d, got %d (%v)", len(wantCmd), len(opts.Config.Cmd), opts.Config.Cmd)
	}
	for i, w := range wantCmd {
		if opts.Config.Cmd[i] != w {
			t.Errorf("Config.Cmd[%d]: want %q, got %q (full=%v)", i, w, opts.Config.Cmd[i], opts.Config.Cmd)
		}
	}

	// Config.Labels — helper label present
	if v := opts.Config.Labels[HelperLabel]; v != "true" {
		t.Errorf("Config.Labels[%q]: want %q, got %q (full=%v)", HelperLabel, "true", v, opts.Config.Labels)
	}

	// HostConfig.AutoRemove — true when keepHelper=false
	if opts.HostConfig == nil {
		t.Fatalf("opts.HostConfig is nil")
	}
	if !opts.HostConfig.AutoRemove {
		t.Errorf("HostConfig.AutoRemove: want true (keepHelper=false default), got false")
	}

	// HostConfig.Binds — docker.sock bind-mount
	foundBind := false
	for _, b := range opts.HostConfig.Binds {
		if b == "/var/run/docker.sock:/var/run/docker.sock" {
			foundBind = true
			break
		}
	}
	if !foundBind {
		t.Errorf("HostConfig.Binds missing docker.sock bind-mount; got %v", opts.HostConfig.Binds)
	}

	// Start was called with the returned id
	if len(f.startCalls) != 1 || f.startCalls[0] != "helper-abc" {
		t.Errorf("ContainerStart: want [%q], got %v", "helper-abc", f.startCalls)
	}
}

// ----------------------------------------------------------------------------
// Test 2: ReturnsHelperID_OnSuccess (smoke for the happy-path return value)
// ----------------------------------------------------------------------------

func TestSpawner_Spawn_ReturnsHelperID_OnSuccess(t *testing.T) {
	t.Parallel()
	f := newFakeClient("helper-xyz-9999")
	sp := NewSpawner(f, "img:v1", "docker-update", nil, false)

	id, err := sp.Spawn(context.Background())
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if id != "helper-xyz-9999" {
		t.Errorf("returned id: want %q, got %q", "helper-xyz-9999", id)
	}
}

// ----------------------------------------------------------------------------
// Test 3: RefusesWhenActionsInFlight
//
// Per RESEARCH.md Open Question 5 RESOLVED: when actionsInFlightFn returns
// > 0, Spawn returns ErrActionsInFlight and NEVER calls ContainerCreate.
// ----------------------------------------------------------------------------

func TestSpawner_Spawn_RefusesWhenActionsInFlight(t *testing.T) {
	t.Parallel()
	f := newFakeClient("never-used")
	inFlight := func() int { return 1 }
	sp := NewSpawner(f, "img:v1", "docker-update", inFlight, false)

	id, err := sp.Spawn(context.Background())
	if !errors.Is(err, ErrActionsInFlight) {
		t.Fatalf("Spawn: want ErrActionsInFlight, got err=%v id=%q", err, id)
	}
	if id != "" {
		t.Errorf("Spawn: want empty id on refusal, got %q", id)
	}
	if len(f.createOpts) != 0 {
		t.Errorf("ContainerCreate calls: want 0 (short-circuit), got %d", len(f.createOpts))
	}
}

// ----------------------------------------------------------------------------
// Test 4: RefusesWhenAlreadyInFlight
//
// Per RESEARCH.md Open Question 2 RESOLVED: the inFlight atomic.Bool guard
// fails the SECOND concurrent Spawn call with ErrSelfUpdateInFlight.
// ----------------------------------------------------------------------------

func TestSpawner_Spawn_RefusesWhenAlreadyInFlight(t *testing.T) {
	t.Parallel()
	f := newFakeClient("helper-first")
	sp := NewSpawner(f, "img:v1", "docker-update", nil, false)

	// First Spawn — succeeds; leaves inFlight=true.
	id1, err := sp.Spawn(context.Background())
	if err != nil {
		t.Fatalf("first Spawn: %v", err)
	}
	if id1 != "helper-first" {
		t.Errorf("first Spawn id: want %q, got %q", "helper-first", id1)
	}

	// Second Spawn — refused via the inFlight guard.
	id2, err := sp.Spawn(context.Background())
	if !errors.Is(err, ErrSelfUpdateInFlight) {
		t.Fatalf("second Spawn: want ErrSelfUpdateInFlight, got err=%v id=%q", err, id2)
	}
	if id2 != "" {
		t.Errorf("second Spawn: want empty id on refusal, got %q", id2)
	}
	// Only the first call should have hit Create.
	if len(f.createOpts) != 1 {
		t.Errorf("ContainerCreate calls: want 1 (second refused), got %d", len(f.createOpts))
	}
}

// ----------------------------------------------------------------------------
// Test 5: KeepHelperFalse_SetsAutoRemoveTrue
//
// keepHelper=false → HostConfig.AutoRemove=true (default operator UX:
// successful self-updates leave no debris).
// ----------------------------------------------------------------------------

func TestSpawner_Spawn_KeepHelperFalse_SetsAutoRemoveTrue(t *testing.T) {
	t.Parallel()
	f := newFakeClient("h-arm-false")
	sp := NewSpawner(f, "img:v1", "docker-update", nil, false /* keepHelper */)

	if _, err := sp.Spawn(context.Background()); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(f.createOpts) != 1 {
		t.Fatalf("createOpts len: %d", len(f.createOpts))
	}
	if !f.createOpts[0].HostConfig.AutoRemove {
		t.Errorf("HostConfig.AutoRemove: want true, got false")
	}
}

// ----------------------------------------------------------------------------
// Test 6: KeepHelperTrue_SetsAutoRemoveFalse
//
// keepHelper=true (operator set DOCKER_UPDATE_SELF_UPDATE_KEEP_HELPER=true)
// → HostConfig.AutoRemove=false so `docker logs <helper>` is available
// post-mortem (RESEARCH.md Pitfall 4).
// ----------------------------------------------------------------------------

func TestSpawner_Spawn_KeepHelperTrue_SetsAutoRemoveFalse(t *testing.T) {
	t.Parallel()
	f := newFakeClient("h-arm-true")
	sp := NewSpawner(f, "img:v1", "docker-update", nil, true /* keepHelper */)

	if _, err := sp.Spawn(context.Background()); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(f.createOpts) != 1 {
		t.Fatalf("createOpts len: %d", len(f.createOpts))
	}
	if f.createOpts[0].HostConfig.AutoRemove {
		t.Errorf("HostConfig.AutoRemove: want false (KEEP_HELPER), got true")
	}
}

// ----------------------------------------------------------------------------
// Test 7: CreateFails_ResetsInFlight
//
// Defensive: a transient Create error must NOT permanently poison the
// endpoint. inFlight resets so the operator's retry succeeds.
// ----------------------------------------------------------------------------

func TestSpawner_Spawn_CreateFails_ResetsInFlightForRetry(t *testing.T) {
	t.Parallel()
	f := newFakeClient("would-be-id")
	f.createErr = errors.New("daemon: connection refused (transient)")
	sp := NewSpawner(f, "img:v1", "docker-update", nil, false)

	_, err := sp.Spawn(context.Background())
	if err == nil {
		t.Fatalf("first Spawn: want non-nil err, got nil")
	}
	if errors.Is(err, ErrSelfUpdateInFlight) {
		t.Fatalf("first Spawn returned ErrSelfUpdateInFlight; want underlying create error")
	}

	// Clear the injected error; retry should now succeed (inFlight reset).
	f.createErr = nil
	id, err := sp.Spawn(context.Background())
	if err != nil {
		t.Fatalf("retry Spawn: %v", err)
	}
	if id != "would-be-id" {
		t.Errorf("retry id: want %q, got %q", "would-be-id", id)
	}
}

// ----------------------------------------------------------------------------
// Test 8: StartFails_CleansUpHelper
//
// On Start failure, the just-created (but not started) helper is best-effort
// removed so we don't leak a useless container.
// ----------------------------------------------------------------------------

func TestSpawner_Spawn_StartFails_RemovesHelperBestEffort(t *testing.T) {
	t.Parallel()
	f := newFakeClient("orphan-helper")
	f.startErr = errors.New("daemon: failed to set up container networking")
	sp := NewSpawner(f, "img:v1", "docker-update", nil, false)

	_, err := sp.Spawn(context.Background())
	if err == nil {
		t.Fatalf("Spawn: want non-nil err on Start failure, got nil")
	}
	if !strings.Contains(err.Error(), "start helper") {
		t.Errorf("err message: want to mention 'start helper', got %q", err.Error())
	}

	// ContainerRemove must have been called with the orphan helper id.
	if len(f.removeCalls) != 1 || f.removeCalls[0] != "orphan-helper" {
		t.Errorf("ContainerRemove: want [%q], got %v", "orphan-helper", f.removeCalls)
	}
}

// ----------------------------------------------------------------------------
// Test 9: InheritsParentUser (defect 9-04-B)
//
// On HMI deployment the parent docker-update runs as "65532:<docker-gid>"
// (e.g. "65532:1001") so it can read /var/run/docker.sock (mode 0660
// root:docker). The helper MUST inherit that User string — otherwise it
// runs as nonroot:nogroup (65532:65532) and the first ContainerList call
// fails with "permission denied". Verified in the 2026-05-16 elevator-hmi
// smoke (SMOKE.md defect 9-04-B).
//
// Implementation: Spawn calls ContainerInspect(selfContainer) BEFORE
// ContainerCreate and copies parentInspect.Container.Config.User into the
// helper's Config.User.
// ----------------------------------------------------------------------------

func TestSpawner_Spawn_InheritsParentUser(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		parentUser string
	}{
		{"hmi-production-style", "65532:1001"},
		{"root", "0:0"},
		{"empty-when-image-default-sufficient", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeClient("helper-user-test")
			f.parentUser = tc.parentUser
			sp := NewSpawner(f, "img:v1", "docker-update", nil, false)

			if _, err := sp.Spawn(context.Background()); err != nil {
				t.Fatalf("Spawn: %v", err)
			}

			// ContainerInspect must have been called BEFORE ContainerCreate.
			if len(f.inspectCalls) != 1 || f.inspectCalls[0] != "docker-update" {
				t.Errorf("ContainerInspect: want one call with %q, got %v", "docker-update", f.inspectCalls)
			}
			if len(f.createOpts) != 1 {
				t.Fatalf("ContainerCreate: want 1 call, got %d", len(f.createOpts))
			}
			got := f.createOpts[0].Config.User
			if got != tc.parentUser {
				t.Errorf("helper Config.User: want %q (inherited from parent), got %q", tc.parentUser, got)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// Test 10: InspectFails_SurfacesErrorAndResetsInFlight (defect 9-04-B)
//
// If the parent inspect fails (network blip, parent renamed mid-flight),
// Spawn must return the error AND reset inFlight so the operator can
// retry. The helper would silently fail with permission_denied later if
// we proceeded with an empty User — fail loudly upfront.
// ----------------------------------------------------------------------------

func TestSpawner_Spawn_InspectFails_SurfacesErrorAndResetsInFlight(t *testing.T) {
	t.Parallel()
	f := newFakeClient("never-used")
	f.inspectErr = errors.New("simulated daemon error")
	sp := NewSpawner(f, "img:v1", "docker-update", nil, false)

	_, err := sp.Spawn(context.Background())
	if err == nil {
		t.Fatalf("Spawn: want error, got nil")
	}
	if !strings.Contains(err.Error(), "inspect parent") {
		t.Errorf("error message: want substring %q, got %q", "inspect parent", err.Error())
	}
	if len(f.createOpts) != 0 {
		t.Errorf("ContainerCreate must NOT be called when inspect fails; got %d calls", len(f.createOpts))
	}

	// inFlight must be reset for retry — calling Spawn again must succeed
	// (now with a working inspect).
	f.inspectErr = nil
	if _, err := sp.Spawn(context.Background()); err != nil {
		t.Errorf("Spawn after inspect-failure recovery: want nil, got %v", err)
	}
}

// ----------------------------------------------------------------------------
// Test 11: Spawn_InheritsParentNetworkMode (defect 9-04-C)
//
// Helper must join the parent's compose network so its verify-poll
// (http://<target>:8080/healthz in Orchestrate) can resolve <target>
// via docker DNS. Pre-fix the helper joined the default `bridge`
// network (isolated from the parent's project network), the poll
// DNS-failed for the full verifyTimeout, and the helper exited 1
// even though the recreate itself succeeded. HMI repro 2026-05-17.
//
// Resolution preference:
//  1. parentInspect.HostConfig.NetworkMode if it names a real network
//  2. First non-"bridge" entry in NetworkSettings.Networks
//  3. Empty (= "default" — same broken behavior, but logged)
// ----------------------------------------------------------------------------

func TestSpawner_Spawn_InheritsParentNetworkMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		hostNetworkMode   string
		networks          map[string]*network.EndpointSettings
		wantHelperNetMode string
	}{
		{
			name:              "compose-style HostConfig.NetworkMode wins",
			hostNetworkMode:   "centroid_default",
			networks:          map[string]*network.EndpointSettings{"centroid_default": {}, "bridge": {}},
			wantHelperNetMode: "centroid_default",
		},
		{
			name:              "default-NetworkMode falls back to non-bridge NetworkSettings entry",
			hostNetworkMode:   "default",
			networks:          map[string]*network.EndpointSettings{"my-app": {}, "bridge": {}},
			wantHelperNetMode: "my-app",
		},
		{
			name:              "empty NetworkMode falls back to non-bridge NetworkSettings entry",
			hostNetworkMode:   "",
			networks:          map[string]*network.EndpointSettings{"prod-net": {}},
			wantHelperNetMode: "prod-net",
		},
		{
			name:              "bridge-only parent leaves NetworkMode empty (last-resort same-broken behavior)",
			hostNetworkMode:   "",
			networks:          map[string]*network.EndpointSettings{"bridge": {}},
			wantHelperNetMode: "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeClient("helper-net-test")
			f.parentHostNetworkMode = tc.hostNetworkMode
			f.parentNetworks = tc.networks
			sp := NewSpawner(f, "img:v1", "docker-update", nil, false)

			if _, err := sp.Spawn(context.Background()); err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			if len(f.createOpts) != 1 {
				t.Fatalf("ContainerCreate: want 1 call, got %d", len(f.createOpts))
			}
			got := string(f.createOpts[0].HostConfig.NetworkMode)
			if got != tc.wantHelperNetMode {
				t.Errorf("helper HostConfig.NetworkMode: want %q, got %q", tc.wantHelperNetMode, got)
			}
		})
	}
}
