package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/centroid-is/hmi-update/internal/compose"
	"github.com/centroid-is/hmi-update/internal/docker"
	"github.com/centroid-is/hmi-update/internal/state"
)

// fakeClient is a configurable docker.Client used by the healthz table tests.
// Each row sets pingErr (returned by Ping) and/or pingDelay (Ping blocks for
// the duration so the timeout branch can be exercised).
//
// The five other Client methods are stubs — healthz only calls Ping. They
// exist solely so fakeClient satisfies the docker.Client interface; if a
// future surface change adds a method to docker.Client the test file will
// fail to compile and the developer will be forced to update the stub. That
// is the intended behaviour per plan 02-01's interface-stability contract.
type fakeClient struct {
	pingErr   error
	pingDelay time.Duration
}

func (f fakeClient) Ping(ctx context.Context) error {
	if f.pingDelay > 0 {
		t := time.NewTimer(f.pingDelay)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return f.pingErr
}

func (fakeClient) ContainerList(ctx context.Context, opts docker.ContainerListOptions) ([]docker.ContainerSummary, error) {
	return nil, nil
}

func (fakeClient) ContainerInspect(ctx context.Context, id string) (docker.ContainerInspect, error) {
	return docker.ContainerInspect{}, nil
}

func (fakeClient) Events(ctx context.Context, opts docker.EventsListOptions) (<-chan docker.EventMessage, <-chan error) {
	ev := make(chan docker.EventMessage)
	er := make(chan error)
	go func() {
		<-ctx.Done()
		close(ev)
		close(er)
	}()
	return ev, er
}

func (fakeClient) ImagePull(ctx context.Context, ref string, opts docker.ImagePullOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (fakeClient) ImageTag(ctx context.Context, src, dst string) error { return nil }

// TestHealthzScenarios exercises every branch of the upgraded /healthz
// handler (DOCK-03 / OBS-02). Each case configures the docker socket path
// via HMI_UPDATE_DOCKER_HOST (which the handler reads via dockerSocketPath)
// and an injected fakeClient with a specific Ping behaviour.
//
// The eight scenarios match plan 02-04 <behavior>:
//   - healthy
//   - socket-missing      (fs.ErrNotExist on stat)
//   - socket-eacces-stat  (fs.ErrPermission on stat — parent dir 0o000)
//   - ping-eacces         (Ping returns syscall.EACCES)
//   - ping-other          (Ping returns a different error)
//   - nil-docker-client   (W2 — defensive nil-guard branch)
//   - ping-timeout        (Ping blocks longer than the 500ms ctx)
//
// (The nil-store branch lives in TestHealthzNilStore in server_test.go;
// it is the Phase 1 carry-over.)
//
// Every case also asserts:
//   - Content-Type contains application/json
//   - Body is valid JSON (sanity)
//   - Body does NOT contain test-host TempDir prefixes (T-01-04-03 path-leak
//     guard — the verbatim CONTEXT.md hint "/var/run/docker.sock" is allowed
//     because it is user advice, not process state)
//   - Total elapsed time is under 2s (the timeout case must not hang)
func TestHealthzScenarios(t *testing.T) {
	cases := []struct {
		name           string
		setupSocket    func(t *testing.T) string // returns the path to set as HMI_UPDATE_DOCKER_HOST
		client         docker.Client
		wantStatus     int
		wantBodySubstr string
	}{
		{
			name: "healthy",
			setupSocket: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "docker.sock")
				if err := os.WriteFile(p, []byte{}, 0o600); err != nil {
					t.Fatal(err)
				}
				return p
			},
			client:         fakeClient{pingErr: nil},
			wantStatus:     http.StatusOK,
			wantBodySubstr: `"status":"ok"`,
		},
		{
			name: "socket-missing",
			setupSocket: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "does-not-exist.sock")
			},
			client:         fakeClient{},
			wantStatus:     http.StatusServiceUnavailable,
			wantBodySubstr: "docker socket missing",
		},
		{
			name: "socket-eacces-on-stat",
			setupSocket: func(t *testing.T) string {
				dir := filepath.Join(t.TempDir(), "unreachable")
				// Create dir with no permissions so a stat *inside* it
				// returns EACCES (the directory itself is stattable; the
				// stat-of-child fails). On macOS APFS this is reliable;
				// see SUMMARY's "Issues Encountered" section if the test
				// proves flaky on other filesystems.
				if err := os.Mkdir(dir, 0o000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
				return filepath.Join(dir, "docker.sock")
			},
			client:         fakeClient{},
			wantStatus:     http.StatusServiceUnavailable,
			wantBodySubstr: "docker socket permission denied",
		},
		{
			name: "ping-eacces",
			setupSocket: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "docker.sock")
				_ = os.WriteFile(p, []byte{}, 0o600)
				return p
			},
			client:         fakeClient{pingErr: syscall.EACCES},
			wantStatus:     http.StatusServiceUnavailable,
			wantBodySubstr: "docker socket permission denied",
		},
		{
			name: "ping-other",
			setupSocket: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "docker.sock")
				_ = os.WriteFile(p, []byte{}, 0o600)
				return p
			},
			client:         fakeClient{pingErr: errors.New("connection refused")},
			wantStatus:     http.StatusServiceUnavailable,
			wantBodySubstr: "docker daemon unreachable",
		},
		{
			// W2 — covers the defensive nil-guard branch in healthz.
			// Without a test the branch is dead code per coverage tooling
			// and a future refactor might drop the guard, opening a
			// nil-pointer panic on a misconfigured boot path.
			//
			// WR-03 fix: the branch now emits healthzBodyClientUnwired
			// ("docker client not wired"), not the misleading
			// socket-missing hint that conflated wiring faults with
			// bind-mount problems.
			name: "nil-docker-client",
			setupSocket: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "docker.sock")
				_ = os.WriteFile(p, []byte{}, 0o600)
				return p
			},
			client:         nil, // explicit nil triggers the defensive guard
			wantStatus:     http.StatusServiceUnavailable,
			wantBodySubstr: "docker client not wired",
		},
		{
			name: "ping-timeout",
			setupSocket: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "docker.sock")
				_ = os.WriteFile(p, []byte{}, 0o600)
				return p
			},
			client:         fakeClient{pingDelay: 2 * time.Second}, // > 500ms timeout
			wantStatus:     http.StatusServiceUnavailable,
			wantBodySubstr: "docker daemon unreachable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sockPath := tc.setupSocket(t)
			t.Setenv("HMI_UPDATE_DOCKER_HOST", sockPath)

			dir := t.TempDir()
			store, err := state.NewStore(filepath.Join(dir, "state.json"))
			if err != nil {
				t.Fatalf("state.NewStore: %v", err)
			}
			composePath := filepath.Join(dir, "docker-compose.yml")
			if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o644); err != nil {
				t.Fatalf("write compose: %v", err)
			}
			reader, err := compose.NewReader(composePath)
			if err != nil {
				t.Fatalf("compose.NewReader: %v", err)
			}

			srv := NewServer(store, tc.client, reader)
			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			rec := httptest.NewRecorder()

			start := time.Now()
			srv.Handler().ServeHTTP(rec, req)
			elapsed := time.Since(start)
			// Timeout test bound: even the worst case (ping-timeout with
			// a 500ms context) must complete in well under 2s.
			if elapsed > 2*time.Second {
				t.Errorf("healthz took %v, expected <2s", elapsed)
			}

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body=%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, tc.wantBodySubstr) {
				t.Errorf("body %q does not contain %q", body, tc.wantBodySubstr)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
				t.Errorf("Content-Type %q does not contain application/json", ct)
			}
			// T-01-04-03 path-leak guard. NB: the verbatim socket path
			// '/var/run/docker.sock' IS allowed in the body (it is user
			// advice, not process state). We block test-host TempDir
			// prefixes which would indicate the handler echoed an
			// absolute filesystem path from os.Stat / r.Context.
			if strings.Contains(body, "/private/") || strings.Contains(body, "/var/folders/") || strings.Contains(body, "/tmp/") {
				t.Errorf("healthz body leaks an absolute path: %q", body)
			}
			// JSON parse sanity.
			var parsed map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
				t.Errorf("body is not valid JSON: %v (body=%q)", err, body)
			}
		})
	}
}
