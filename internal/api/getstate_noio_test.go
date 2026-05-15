// getstate_noio_test.go pins the OBS-03 invariant: GET /api/state MUST
// be in-memory only — no docker.Inspect, no os.Stat, no socket touch.
// The handler is the 5-second UI poll target on the HMI; touching any
// I/O would inflate poll latency and amplify on every running browser
// tab.
//
// Test mechanism: inject a panickingDockerClient that panics with a
// diagnostic message on every method. If anyone adds a docker call
// reachable from GET /api/state (or from anything reachable from it,
// e.g. a closure captured by a goroutine spawned by getState), the test
// panics + fails loudly. The panic message names which method was
// invoked so operators reviewing the failure immediately know which
// I/O leaked.
//
// The handler runs 100 iterations in a tight loop to catch any
// background goroutine that might be deferred or amortised.
package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/centroid-is/hmi-update/internal/docker"
	"github.com/centroid-is/hmi-update/internal/state"
)

// panickingDockerClient panics with the method name on every invocation.
// Used by TestGetState_NoIO to prove the GET /api/state path never
// touches the docker client (OBS-03 invariant).
type panickingDockerClient struct{}

func (panickingDockerClient) Ping(ctx context.Context) error {
	panic("OBS-03 violation: GET /api/state invoked docker.Ping")
}

func (panickingDockerClient) ContainerList(ctx context.Context, opts docker.ContainerListOptions) ([]docker.ContainerSummary, error) {
	panic("OBS-03 violation: GET /api/state invoked docker.ContainerList")
}

func (panickingDockerClient) ContainerInspect(ctx context.Context, id string) (docker.ContainerInspect, error) {
	panic("OBS-03 violation: GET /api/state invoked docker.ContainerInspect")
}

func (panickingDockerClient) Events(ctx context.Context, opts docker.EventsListOptions) (<-chan docker.EventMessage, <-chan error) {
	panic("OBS-03 violation: GET /api/state invoked docker.Events")
}

func (panickingDockerClient) ImagePull(ctx context.Context, ref string, opts docker.ImagePullOptions) (io.ReadCloser, error) {
	panic("OBS-03 violation: GET /api/state invoked docker.ImagePull")
}

func (panickingDockerClient) ImageInspect(ctx context.Context, ref string) (docker.ImageInspect, error) {
	panic("OBS-03 violation: GET /api/state invoked docker.ImageInspect")
}

func (panickingDockerClient) ImageTag(ctx context.Context, src, dst string) error {
	panic("OBS-03 violation: GET /api/state invoked docker.ImageTag")
}

func TestGetState_NoIO(t *testing.T) {
	// Build the Server with the panicking client. The orchestrator is nil
	// (action endpoints aren't exercised by this test). The state.Store
	// is real but rooted in t.TempDir() and seeded with one container so
	// the marshal path has data to encode.
	dir := t.TempDir()
	store, err := state.NewStore(dir + "/state.json")
	if err != nil {
		t.Fatalf("state.NewStore: %v", err)
	}
	if err := store.Update(func(st *state.State) {
		st.Containers["svc-a"] = state.Container{
			Service:       "svc-a",
			Image:         "centroid-is/stub",
			Tag:           "latest",
			CurrentDigest: "sha256:deadbeef",
		}
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	srv := NewServer(store, panickingDockerClient{}, newTestReader(t, dir), nil, nil)

	// 100 iterations to catch any deferred / amortised I/O. If anyone
	// adds a docker call to the GET path, the test panics + fails on
	// iteration 1; the loop just adds confidence and demonstrates the
	// poll-friendly latency claim.
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: status = %d, body = %s", i, rec.Code, rec.Body.String())
		}
		// Verify the body is non-empty JSON (sanity — the handler did
		// something).
		body := rec.Body.String()
		if !strings.Contains(body, "svc-a") {
			t.Fatalf("iter %d: body does not contain seeded container: %s", i, body)
		}
	}
}
