package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/centroid-is/hmi-update/internal/compose"
	"github.com/centroid-is/hmi-update/internal/state"
)

// newTestServer creates a Server backed by a fresh, empty state.Store rooted
// in t.TempDir() so each test gets an isolated state file. Phase 2 extended
// the constructor signature with docker.Client + *compose.Reader; this
// helper injects an always-healthy fakeClient (from handlers_healthz_test.go)
// and a Reader pointing at a freshly-written compose stub so tests that
// don't care about docker still get a non-nil wire-up.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := state.NewStore(filepath.Join(dir, "hmi_update_state.json"))
	if err != nil {
		t.Fatalf("state.NewStore: %v", err)
	}
	return NewServer(store, fakeClient{}, newTestReader(t, dir))
}

// newTestServerWithContainer seeds the store with one container so tests
// can assert against a non-empty /api/state payload.
func newTestServerWithContainer(t *testing.T, svc string) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := state.NewStore(filepath.Join(dir, "hmi_update_state.json"))
	if err != nil {
		t.Fatalf("state.NewStore: %v", err)
	}
	if err := store.Update(func(st *state.State) {
		st.Containers[svc] = state.Container{
			Service:       svc,
			Image:         "centroid-is/stub",
			Tag:           "latest",
			CurrentDigest: "sha256:deadbeef",
		}
	}); err != nil {
		t.Fatalf("store.Update: %v", err)
	}
	return NewServer(store, fakeClient{}, newTestReader(t, dir))
}

// newTestReader writes a minimal docker-compose.yml inside dir and returns
// the resulting *compose.Reader. Used by both newTestServer helpers and the
// TestHealthzNilStore case (see below). Failures are t.Fatal — a Reader is
// required by NewServer's signature.
func newTestReader(t *testing.T, dir string) *compose.Reader {
	t.Helper()
	composePath := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	reader, err := compose.NewReader(composePath)
	if err != nil {
		t.Fatalf("compose.NewReader: %v", err)
	}
	return reader
}

func TestHealthz(t *testing.T) {
	// Phase 1 carry-over: with the upgraded /healthz the "healthy" branch
	// requires both the socket file to be stattable AND the docker.Client
	// Ping to succeed. The fakeClient injected by newTestServer always
	// returns nil from Ping; here we point HMI_UPDATE_DOCKER_HOST at a
	// real file so the stat step also passes.
	sock := filepath.Join(t.TempDir(), "docker.sock")
	if err := os.WriteFile(sock, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HMI_UPDATE_DOCKER_HOST", sock)

	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Code; got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	body := rec.Body.String()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("body is not valid JSON: %v (body=%q)", err, body)
	}
	if parsed["status"] != "ok" {
		t.Errorf(`body["status"] = %v, want "ok"`, parsed["status"])
	}

	// V7 security check (threat T-01-04-03): the response body and headers
	// must not echo absolute filesystem paths.
	if strings.Contains(body, "/private/") || strings.Contains(body, "/var/folders/") || strings.Contains(body, "/tmp/") {
		t.Errorf("/healthz body leaks an absolute path: %q", body)
	}
}

func TestHealthzNilStore(t *testing.T) {
	// Defensive: a Server with a nil store should return 503 with a generic
	// remediation hint, not crash and not leak the state path.
	srv := &Server{mux: http.NewServeMux()}
	srv.routes()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Code; got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", got, http.StatusServiceUnavailable)
	}
	if !strings.Contains(rec.Body.String(), "unhealthy") {
		t.Errorf("body = %q, want to contain 'unhealthy'", rec.Body.String())
	}
}

func TestGetStateEmpty(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Code; got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var st state.State
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if st.Version != state.SchemaVersion {
		t.Errorf("version = %d, want %d", st.Version, state.SchemaVersion)
	}
	if st.Containers == nil {
		t.Error("containers should be non-nil (empty map, not null)")
	}
}

func TestGetStateWithContainer(t *testing.T) {
	srv := newTestServerWithContainer(t, "stub-watched-container")
	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Code; got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}

	var st state.State
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	c, ok := st.Containers["stub-watched-container"]
	if !ok {
		t.Fatalf("containers[stub-watched-container] missing; got %+v", st.Containers)
	}
	if c.Service != "stub-watched-container" {
		t.Errorf("service = %q", c.Service)
	}
	if c.CurrentDigest != "sha256:deadbeef" {
		t.Errorf("current_digest = %q", c.CurrentDigest)
	}
}

func TestAssetsStrictNoFallback(t *testing.T) {
	// Pitfall 8 invariant: an unknown /assets/* path MUST 404, not return
	// index.html. This is the regression test for the SPA-fallback trap.
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/assets/this-does-not-exist.js", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Code; got != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", got, http.StatusNotFound)
	}
	body := rec.Body.String()
	// Even on 404, the body must NOT echo the SPA shell.
	if strings.Contains(strings.ToLower(body), "<!doctype html") {
		t.Errorf("/assets/<missing>.js 404 body echoes index.html: %q", body)
	}
}

func TestIndexHTMLCacheControl(t *testing.T) {
	// UI-SPEC asset cache contract: GET / must serve Cache-Control: no-cache.
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Code; got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(strings.ToLower(cc), "no-cache") {
		t.Errorf("Cache-Control = %q, want to contain no-cache", cc)
	}
	body := rec.Body.String()
	if !strings.Contains(strings.ToLower(body), "<!doctype html") {
		t.Errorf("body does not look like index.html: %s", body[:min(120, len(body))])
	}
}

func TestAssetsImmutable(t *testing.T) {
	// Discover a real Vite-emitted /assets/*.js by reading index.html from
	// the embed.FS. The test is conditional on the dist/ being populated
	// (which plan 03 ensures), but we skip gracefully if not.
	indexBytes, err := os.ReadFile("dist/index.html")
	if err != nil {
		t.Skipf("dist/index.html not available; skipping asset test (run `make ui` first): %v", err)
	}
	re := regexp.MustCompile(`/assets/[A-Za-z0-9._-]+\.js`)
	match := re.FindString(string(indexBytes))
	if match == "" {
		t.Skip("no /assets/*.js reference in index.html; skipping")
	}

	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, match, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Code; got != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d", match, got, http.StatusOK)
	}
	ct := strings.ToLower(rec.Header().Get("Content-Type"))
	if ct != "application/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q (Pitfall 8: distroless lacks system mime.types)", ct, "application/javascript; charset=utf-8")
	}
	cc := strings.ToLower(rec.Header().Get("Cache-Control"))
	if cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want %q", cc, "public, max-age=31536000, immutable")
	}
	// Verify there IS a body (i.e. we served the file, not just a header-only response).
	body, _ := io.ReadAll(rec.Body)
	if len(body) == 0 {
		t.Error("response body is empty")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
