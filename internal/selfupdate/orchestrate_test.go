// Package selfupdate (test). orchestrate_test.go covers the helper-side
// Orchestrate flow per 09-04-PLAN.md Task 1 + 09-RESEARCH.md
// § Architecture Patterns / Pattern 4.
//
// The fake docker.Client below scripts recreate.Service's call sequence
// (List → Inspect → Stop → Remove → Create → Start) so Orchestrate can
// be driven end-to-end without a real daemon. A live httptest.Server
// stands in for the new parent's /healthz endpoint.
package selfupdate

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/centroid-is/docker-update/internal/docker"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

// ----------------------------------------------------------------------------
// recreateFake — docker.Client that satisfies recreate.Service's call shape
// ----------------------------------------------------------------------------

// recreateFake is a minimal docker.Client tailored to drive
// recreate.Service through one happy or failure-injected call. It records
// no per-call detail beyond what is needed for the assertions in this
// file.
type recreateFake struct {
	mu sync.Mutex

	// minimal scripted responses for the recreate.Service call chain
	listResult    []docker.ContainerSummary
	inspectResult docker.ContainerInspect
	newID         string

	// failure injection
	stopErr error
}

// compile-time guard
var _ docker.Client = (*recreateFake)(nil)

func newRecreateFake() *recreateFake {
	return &recreateFake{
		listResult: []docker.ContainerSummary{
			{ID: "old-parent-id", Created: 1},
		},
		inspectResult: docker.ContainerInspect{
			Container: container.InspectResponse{
				ID:         "old-parent-id",
				Name:       "/docker-update",
				Config:     &container.Config{Image: "ghcr.io/centroid-is/docker-update:latest"},
				HostConfig: &container.HostConfig{},
				NetworkSettings: &container.NetworkSettings{
					Networks: map[string]*network.EndpointSettings{
						"net-a": {Aliases: []string{"docker-update"}},
					},
				},
			},
		},
		newID: "new-parent-id",
	}
}

func (f *recreateFake) ContainerList(ctx context.Context, opts docker.ContainerListOptions) ([]docker.ContainerSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listResult, nil
}
func (f *recreateFake) ContainerInspect(ctx context.Context, id string) (docker.ContainerInspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inspectResult, nil
}
func (f *recreateFake) ContainerStop(ctx context.Context, id string, opts docker.ContainerStopOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopErr
}
func (f *recreateFake) ContainerRemove(ctx context.Context, id string, opts docker.ContainerRemoveOptions) error {
	return nil
}
func (f *recreateFake) ContainerCreate(ctx context.Context, opts docker.ContainerCreateOptions) (docker.ContainerCreateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return docker.ContainerCreateResult{ID: f.newID}, nil
}
func (f *recreateFake) ContainerStart(ctx context.Context, id string, opts docker.ContainerStartOptions) error {
	return nil
}
func (f *recreateFake) NetworkConnect(ctx context.Context, networkID string, opts docker.NetworkConnectOptions) error {
	return nil
}

// ---- unused docker.Client methods ----

func (f *recreateFake) Ping(ctx context.Context) error { return nil }
func (f *recreateFake) Events(ctx context.Context, opts docker.EventsListOptions) (<-chan docker.EventMessage, <-chan error) {
	ev := make(chan docker.EventMessage)
	er := make(chan error)
	go func() { <-ctx.Done(); close(ev); close(er) }()
	return ev, er
}
func (f *recreateFake) ImagePull(ctx context.Context, ref string, opts docker.ImagePullOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (f *recreateFake) ImageInspect(ctx context.Context, ref string) (docker.ImageInspect, error) {
	return docker.ImageInspect{}, nil
}
func (f *recreateFake) ImageTag(ctx context.Context, src, dst string) error { return nil }
func (f *recreateFake) ImageList(ctx context.Context, opts docker.ImageListOptions) ([]docker.ImageSummary, error) {
	return nil, nil
}

// ----------------------------------------------------------------------------
// Test 1: Success_PollsHealthzAndReturnsNil
//
// Happy path: recreate.Service returns nil; the fake healthz server
// returns 200 on the first GET; Orchestrate returns nil within a
// handful of ms.
// ----------------------------------------------------------------------------

func TestOrchestrate_Success_PollsHealthzAndReturnsNil(t *testing.T) {
	t.Parallel()

	// httptest server returns 200 immediately on every GET.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	f := newRecreateFake()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err := Orchestrate(
		ctx,
		f,
		"docker-update",
		srv.URL+"/healthz",
		0,                  // delay — skip so the test runs fast
		2*time.Second,      // verifyTimeout
	)
	if err != nil {
		t.Fatalf("Orchestrate: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Orchestrate took %v on happy path; expected ~immediate", elapsed)
	}
}

// ----------------------------------------------------------------------------
// Test 2: VerifyTimeout_ReturnsError
//
// recreate.Service succeeds; the fake healthz server returns 503 forever;
// Orchestrate hits verifyTimeout and returns a verify-timeout error.
// ----------------------------------------------------------------------------

func TestOrchestrate_VerifyTimeout_ReturnsError(t *testing.T) {
	t.Parallel()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f := newRecreateFake()
	err := Orchestrate(
		context.Background(),
		f,
		"docker-update",
		srv.URL+"/healthz",
		0,                       // delay
		300*time.Millisecond,    // verifyTimeout — tight to keep the test fast
	)
	if err == nil {
		t.Fatalf("Orchestrate: want non-nil err on verify timeout, got nil")
	}
	if !strings.Contains(err.Error(), "verify timeout") {
		t.Errorf("err message: want to mention 'verify timeout', got %q", err.Error())
	}
	if atomic.LoadInt32(&hits) < 1 {
		t.Errorf("healthz hits: want >= 1, got %d", atomic.LoadInt32(&hits))
	}
}

// ----------------------------------------------------------------------------
// Test 3: RecreateFails_ReturnsError
//
// recreate.Service fails (Stop returns an error); Orchestrate wraps the
// error with "recreate" context and returns it. healthz is NEVER polled
// (we'd never get there).
// ----------------------------------------------------------------------------

func TestOrchestrate_RecreateFails_ReturnsError(t *testing.T) {
	t.Parallel()

	var healthzHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&healthzHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := newRecreateFake()
	f.stopErr = errors.New("daemon: stop failed: device or resource busy")

	err := Orchestrate(
		context.Background(),
		f,
		"docker-update",
		srv.URL+"/healthz",
		0,
		2*time.Second,
	)
	if err == nil {
		t.Fatalf("Orchestrate: want non-nil err on recreate failure, got nil")
	}
	if !strings.Contains(err.Error(), "recreate") {
		t.Errorf("err message: want to mention 'recreate', got %q", err.Error())
	}
	if got := atomic.LoadInt32(&healthzHits); got != 0 {
		t.Errorf("healthz hits: want 0 (never reached past recreate failure), got %d", got)
	}
}

// ----------------------------------------------------------------------------
// Test 4: ContextCancelled_DuringDelay
//
// A parent-side ctx cancel during the pre-recreate delay aborts the
// helper before it touches the daemon. Documents the cooperative-cancel
// path.
// ----------------------------------------------------------------------------

func TestOrchestrate_ContextCancelled_DuringDelay(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := newRecreateFake()
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short window — within the 1s delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := Orchestrate(
		ctx,
		f,
		"docker-update",
		srv.URL+"/healthz",
		1*time.Second,    // delay — long enough for cancel to land first
		2*time.Second,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Orchestrate: want context.Canceled, got %v", err)
	}
}
