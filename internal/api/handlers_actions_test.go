// handlers_actions_test.go covers the three Phase 4 action endpoints
// (POST /api/containers/{service}/update, /rollback, /force-pull) and
// the writeActionError dispatcher.
//
// Test inventory (table-driven where possible, individual where the
// chain shape diverges):
//
//   - TestHandleUpdate_HappyPath                         — 200 + current/previous_digest
//   - TestHandleUpdate_InvalidServiceName_400            — ValidateServiceName rejects ../etc/passwd
//   - TestHandleUpdate_ContainerNotFound_404             — LookupContainer miss
//   - TestHandleUpdate_SelfProtection_409                — svc==selfService
//   - TestHandleUpdate_AllowUpdateFalse_409              — hmi-update.allow-update=false
//   - TestHandleUpdate_ServiceBusy_409                   — ErrServiceBusy sentinel
//   - TestHandleUpdate_ComposeFileMoved_412              — compose.ErrComposeFileMoved
//   - TestHandleUpdate_PullFailed_500                    — ErrPullFailed
//   - TestHandleUpdate_ComposeFailed_500                 — ErrComposeFailed
//   - TestHandleUpdate_VerifyFailed_500_StructuredBody   — typed *VerifyDetail body
//   - TestHandleUpdate_VerifyCanceled_503                — ErrVerifyCanceled
//   - TestHandleUpdate_OrchestratorUnwired_503           — nil orchestrator
//   - TestHandleRollback_HappyPath
//   - TestHandleRollback_NoPreviousDigest_400
//   - TestHandleRollback_AllowRollbackFalse_409
//   - TestHandleForcePull_DefaultNoRecreate_200          — recreate=false default
//   - TestHandleForcePull_WithRecreateTrue_HappyPath
//   - TestHandleForcePull_WithRecreateTrue_AppliesUpdateSafety
//   - TestHandleForcePull_DefaultExemptFromSafetyLabel   — SAFE-03 carve-out
//   - TestHandleActions_PathLeakGuard                    — body never echoes tempdir
//   - TestRoutes_ContainPhase4Endpoints                  — mux pattern walk
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/centroid-is/docker-update/internal/actions"
	"github.com/centroid-is/docker-update/internal/compose"
	"github.com/centroid-is/docker-update/internal/state"
)

// fakeOrchestrator implements actions.Orchestrator. The script maps are
// keyed by service name so tests can stage per-service responses without
// touching unrelated calls. updateCalls / rollbackCalls / forcePullCalls
// record every invocation for assertions about chain order (e.g. "if
// CheckSafetyLabel rejected, ForcePull must NOT have been called").
//
// Goroutine-safety: tests run sequentially per t, but fakeOrchestrator
// could be called from r.Context()-derived goroutines if a handler ever
// spawned one. Mutex-guard the counter slices defensively.
type fakeOrchestrator struct {
	mu sync.Mutex

	lookup    map[string]state.Container
	selfSvc   string
	updateRes map[string]actions.ActionResult
	updateErr map[string]error
	rollRes   map[string]actions.ActionResult
	rollErr   map[string]error
	forceRes  map[string]actions.ActionResult
	forceErr  map[string]error

	updateCalls []string
	rollCalls   []string
	forceCalls  []forcePullCall
}

type forcePullCall struct {
	service  string
	recreate bool
}

func (f *fakeOrchestrator) LookupContainer(svc string) (state.Container, bool) {
	c, ok := f.lookup[svc]
	return c, ok
}

func (f *fakeOrchestrator) CheckSelfProtection(w http.ResponseWriter, svc string) bool {
	if svc == f.selfSvc {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(actions.ActionBodySelfProtection))
		return false
	}
	return true
}

func (f *fakeOrchestrator) SelfService() string { return f.selfSvc }

func (f *fakeOrchestrator) Update(ctx context.Context, svc string) (actions.ActionResult, error) {
	f.mu.Lock()
	f.updateCalls = append(f.updateCalls, svc)
	f.mu.Unlock()
	if err, ok := f.updateErr[svc]; ok {
		return actions.ActionResult{}, err
	}
	if res, ok := f.updateRes[svc]; ok {
		return res, nil
	}
	return actions.ActionResult{}, nil
}

func (f *fakeOrchestrator) Rollback(ctx context.Context, svc string) (actions.ActionResult, error) {
	f.mu.Lock()
	f.rollCalls = append(f.rollCalls, svc)
	f.mu.Unlock()
	if err, ok := f.rollErr[svc]; ok {
		return actions.ActionResult{}, err
	}
	if res, ok := f.rollRes[svc]; ok {
		return res, nil
	}
	return actions.ActionResult{}, nil
}

func (f *fakeOrchestrator) ForcePull(ctx context.Context, svc string, recreate bool) (actions.ActionResult, error) {
	f.mu.Lock()
	f.forceCalls = append(f.forceCalls, forcePullCall{service: svc, recreate: recreate})
	f.mu.Unlock()
	if err, ok := f.forceErr[svc]; ok {
		return actions.ActionResult{}, err
	}
	if res, ok := f.forceRes[svc]; ok {
		return res, nil
	}
	return actions.ActionResult{}, nil
}

// ActionsInFlightFn — Plan 09-04 Orchestrator interface addition.
// The fake never holds per-service mutexes (it short-circuits the
// real lockService path), so this always reports 0 — appropriate for
// the per-service handler tests which do not exercise the self-update
// short-circuit. handleSelfUpdate uses a different test seam
// (newSelfUpdateTestServer in handlers_self_test.go).
func (f *fakeOrchestrator) ActionsInFlightFn() func() int {
	return func() int { return 0 }
}

// newOrchestratorTestServer returns a Server wired with a fakeOrchestrator
// and a no-op state.Store / fakeClient / Reader. Tests configure the fake
// before invoking ServeHTTP.
func newOrchestratorTestServer(t *testing.T, fake *fakeOrchestrator) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := state.NewStore(dir + "/state.json")
	if err != nil {
		t.Fatalf("state.NewStore: %v", err)
	}
	return NewServer(store, fakeClient{}, newTestReader(t, dir), fake, nil)
}

// ---------------------------------------------------------------------
// Update happy path + error branches
// ---------------------------------------------------------------------

func TestHandleUpdate_HappyPath(t *testing.T) {
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup: map[string]state.Container{
			"stub-watched-container": {Service: "stub-watched-container"},
		},
		updateRes: map[string]actions.ActionResult{
			"stub-watched-container": {
				CurrentDigest:  "sha256:newdigest",
				PreviousDigest: "sha256:olddigest",
			},
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/stub-watched-container/update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		CurrentDigest  string `json:"current_digest"`
		PreviousDigest string `json:"previous_digest"`
		NoOp           bool   `json:"no_op"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if got.CurrentDigest != "sha256:newdigest" {
		t.Errorf("current_digest = %q", got.CurrentDigest)
	}
	if got.PreviousDigest != "sha256:olddigest" {
		t.Errorf("previous_digest = %q", got.PreviousDigest)
	}
	if got.NoOp {
		t.Errorf("no_op should be false on happy path")
	}
	// Confirm Update was actually called (chain ran to completion).
	if len(fake.updateCalls) != 1 || fake.updateCalls[0] != "stub-watched-container" {
		t.Errorf("Update calls = %v, want [stub-watched-container]", fake.updateCalls)
	}
}

func TestHandleUpdate_InvalidServiceName_400(t *testing.T) {
	fake := &fakeOrchestrator{selfSvc: "docker-update"}
	srv := newOrchestratorTestServer(t, fake)
	// Path traversal attempt — ValidateServiceName must reject. URL-encoded
	// "../etc/passwd" so the path-router accepts the request and lets the
	// handler's middleware see the literal value.
	req := httptest.NewRequest(http.MethodPost, "/api/containers/..%2Fetc%2Fpasswd/update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "invalid_service_name") {
		t.Errorf("body %q does not contain invalid_service_name", body)
	}
	// Must be byte-identical to the middleware's exported wire constant.
	if strings.TrimSpace(body) != strings.TrimSpace(actions.ActionBodyInvalidServiceName) {
		t.Errorf("body = %q\nwant = %q", body, actions.ActionBodyInvalidServiceName)
	}
	if len(fake.updateCalls) != 0 {
		t.Errorf("Update must not be called when ValidateServiceName rejects")
	}
}

func TestHandleUpdate_ContainerNotFound_404(t *testing.T) {
	fake := &fakeOrchestrator{selfSvc: "docker-update", lookup: map[string]state.Container{}}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/missing-svc/update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "container_not_found") {
		t.Errorf("body %q does not contain container_not_found", rec.Body.String())
	}
	if len(fake.updateCalls) != 0 {
		t.Errorf("Update must not be called when LookupContainer misses")
	}
}

func TestHandleUpdate_SelfProtection_409(t *testing.T) {
	fake := &fakeOrchestrator{selfSvc: "docker-update"}
	srv := newOrchestratorTestServer(t, fake)
	// Self-targeting path — CheckSelfProtection MUST run before
	// LookupContainer so 409 fires even though docker-update is not in the
	// (empty) lookup map. ACT-09 + middleware order invariant.
	req := httptest.NewRequest(http.MethodPost, "/api/containers/docker-update/update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "self_protection") {
		t.Errorf("body %q does not contain self_protection", rec.Body.String())
	}
	if len(fake.updateCalls) != 0 {
		t.Errorf("Update must not be called on self-targeted action")
	}
}

func TestHandleUpdate_AllowUpdateFalse_409(t *testing.T) {
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup: map[string]state.Container{
			"timescaledb-stub": {
				Service: "timescaledb-stub",
				Labels:  map[string]string{"hmi-update.allow-update": "false"},
			},
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/timescaledb-stub/update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != strings.TrimSpace(actions.ActionBodyActionDisabledUpdate) {
		t.Errorf("body = %q\nwant = %q", rec.Body.String(), actions.ActionBodyActionDisabledUpdate)
	}
	if len(fake.updateCalls) != 0 {
		t.Errorf("Update must not be called when CheckSafetyLabel rejects")
	}
}

func TestHandleUpdate_ServiceBusy_409(t *testing.T) {
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup:  map[string]state.Container{"svc-a": {Service: "svc-a"}},
		updateErr: map[string]error{
			"svc-a": fmt.Errorf("actions.Update svc-a: %w", actions.ErrServiceBusy),
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != strings.TrimSpace(actions.ActionBodyServiceBusy) {
		t.Errorf("body = %q\nwant = %q", rec.Body.String(), actions.ActionBodyServiceBusy)
	}
}

func TestHandleUpdate_ComposeFileMoved_412(t *testing.T) {
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup:  map[string]state.Container{"svc-a": {Service: "svc-a"}},
		updateErr: map[string]error{
			"svc-a": fmt.Errorf("actions.Update: %w", compose.ErrComposeFileMoved),
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != strings.TrimSpace(actions.ActionBodyComposeFileMoved) {
		t.Errorf("body = %q\nwant = %q", rec.Body.String(), actions.ActionBodyComposeFileMoved)
	}
}

func TestHandleUpdate_PullFailed_500(t *testing.T) {
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup:  map[string]state.Container{"svc-a": {Service: "svc-a"}},
		updateErr: map[string]error{
			"svc-a": fmt.Errorf("actions.Update svc-a: %w", actions.ErrPullFailed),
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "pull_failed") {
		t.Errorf("body %q does not contain pull_failed", rec.Body.String())
	}
}

func TestHandleUpdate_ComposeFailed_500(t *testing.T) {
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup:  map[string]state.Container{"svc-a": {Service: "svc-a"}},
		updateErr: map[string]error{
			"svc-a": fmt.Errorf("actions.Update svc-a: %w", actions.ErrComposeFailed),
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "compose_failed") {
		t.Errorf("body %q does not contain compose_failed", rec.Body.String())
	}
}

func TestHandleUpdate_VerifyFailed_500_StructuredBody(t *testing.T) {
	// Build the same wrap shape the orchestrator produces:
	//   fmt.Errorf("%w: %w", ErrVerifyFailed, &VerifyDetail{...})
	detail := &actions.VerifyDetail{
		RestartCount: 3,
		Running:      false,
		ContainerID:  "abc123def456",
		Reason:       "container restarted 3 times in 15s",
	}
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup:  map[string]state.Container{"svc-a": {Service: "svc-a"}},
		updateErr: map[string]error{
			"svc-a": fmt.Errorf("actions.Update svc-a: %w: %w", actions.ErrVerifyFailed, detail),
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// Field-by-field assertion via decode — CONTEXT.md Area 3 lines 102–112
	// LOCK the shape.
	var got struct {
		Error        string `json:"error"`
		Reason       string `json:"reason"`
		ExitCode     *int   `json:"exit_code"`
		RestartCount int    `json:"restart_count"`
		Running      bool   `json:"running"`
		ContainerID  string `json:"container_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if got.Error != "verify_failed" {
		t.Errorf("error = %q", got.Error)
	}
	if got.Reason != "container restarted 3 times in 15s" {
		t.Errorf("reason = %q", got.Reason)
	}
	if got.RestartCount != 3 {
		t.Errorf("restart_count = %d", got.RestartCount)
	}
	if got.Running {
		t.Errorf("running = true, want false")
	}
	if got.ContainerID != "abc123def456" {
		t.Errorf("container_id = %q", got.ContainerID)
	}
	if got.ExitCode != nil {
		t.Errorf("exit_code = %v, want null", got.ExitCode)
	}
	// T-04-04-03 path-leak guard: the body must NOT contain the tempdir
	// prefix (the state.NewStore + Reader paths thread t.TempDir() into
	// the test wiring; if the handler echoed any of those, the assertion
	// fires).
	if bytes.Contains(rec.Body.Bytes(), []byte(t.TempDir())) {
		t.Errorf("verify_failed body leaks tempdir prefix: %q", rec.Body.String())
	}
}

func TestHandleUpdate_VerifyCanceled_503(t *testing.T) {
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup:  map[string]state.Container{"svc-a": {Service: "svc-a"}},
		updateErr: map[string]error{
			"svc-a": fmt.Errorf("actions.Update svc-a: %w", actions.ErrVerifyCanceled),
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "verify_canceled") {
		t.Errorf("body %q does not contain verify_canceled", rec.Body.String())
	}
}

func TestHandleUpdate_OrchestratorUnwired_503(t *testing.T) {
	// Explicitly nil orchestrator — defensive guard branch.
	dir := t.TempDir()
	store, err := state.NewStore(dir + "/state.json")
	if err != nil {
		t.Fatalf("state.NewStore: %v", err)
	}
	srv := NewServer(store, fakeClient{}, newTestReader(t, dir), nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "orchestrator_not_wired") {
		t.Errorf("body %q does not contain orchestrator_not_wired", rec.Body.String())
	}
}

// ---------------------------------------------------------------------
// Rollback
// ---------------------------------------------------------------------

func TestHandleRollback_HappyPath(t *testing.T) {
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup:  map[string]state.Container{"svc-a": {Service: "svc-a"}},
		rollRes: map[string]actions.ActionResult{
			"svc-a": {CurrentDigest: "sha256:old", PreviousDigest: "sha256:new"},
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/rollback", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "sha256:old") {
		t.Errorf("body %q does not contain current_digest", rec.Body.String())
	}
	if len(fake.rollCalls) != 1 {
		t.Errorf("Rollback calls = %v", fake.rollCalls)
	}
}

func TestHandleRollback_NoPreviousDigest_400(t *testing.T) {
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup:  map[string]state.Container{"svc-a": {Service: "svc-a"}},
		rollErr: map[string]error{
			// Matches the orchestrator's wrap shape (orchestrator.go
			// Rollback step 2): "actions.Rollback <svc>: no_previous_digest".
			"svc-a": fmt.Errorf("actions.Rollback svc-a: no_previous_digest"),
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/rollback", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no_previous_digest") {
		t.Errorf("body %q does not contain no_previous_digest", rec.Body.String())
	}
}

func TestHandleRollback_NotADowngrade_409(t *testing.T) {
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup:  map[string]state.Container{"svc-a": {Service: "svc-a"}},
		rollErr: map[string]error{
			"svc-a": fmt.Errorf("actions.Rollback svc-a: %w", actions.ErrNotADowngrade),
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/rollback", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != strings.TrimSpace(actions.ActionBodyNotADowngrade) {
		t.Errorf("body = %q\nwant = %q", rec.Body.String(), actions.ActionBodyNotADowngrade)
	}
	if !strings.Contains(rec.Body.String(), "not_a_downgrade") {
		t.Errorf("body %q must carry the literal token 'not_a_downgrade' for operator grep", rec.Body.String())
	}
}

func TestHandleRollback_AllowRollbackFalse_409(t *testing.T) {
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup: map[string]state.Container{
			"svc-a": {
				Service: "svc-a",
				Labels:  map[string]string{"hmi-update.allow-rollback": "false"},
			},
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/rollback", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != strings.TrimSpace(actions.ActionBodyActionDisabledRollback) {
		t.Errorf("body = %q\nwant = %q", rec.Body.String(), actions.ActionBodyActionDisabledRollback)
	}
	if len(fake.rollCalls) != 0 {
		t.Errorf("Rollback must not be called when label rejects")
	}
}

// ---------------------------------------------------------------------
// ForcePull — default + recreate
// ---------------------------------------------------------------------

func TestHandleForcePull_DefaultNoRecreate_200(t *testing.T) {
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup:  map[string]state.Container{"svc-a": {Service: "svc-a"}},
		forceRes: map[string]actions.ActionResult{
			"svc-a": {CurrentDigest: "sha256:cur"},
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/force-pull", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(fake.forceCalls) != 1 || fake.forceCalls[0].recreate {
		t.Errorf("ForcePull calls = %+v, want one with recreate=false", fake.forceCalls)
	}
}

func TestHandleForcePull_WithRecreateTrue_HappyPath(t *testing.T) {
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup:  map[string]state.Container{"svc-a": {Service: "svc-a"}},
		forceRes: map[string]actions.ActionResult{
			"svc-a": {CurrentDigest: "sha256:cur", PreviousDigest: "sha256:old"},
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/force-pull?recreate=true", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(fake.forceCalls) != 1 || !fake.forceCalls[0].recreate {
		t.Errorf("ForcePull calls = %+v, want one with recreate=true", fake.forceCalls)
	}
}

func TestHandleForcePull_WithRecreateTrue_AppliesUpdateSafety(t *testing.T) {
	// recreate=true MUST honour the Update safety label (RESEARCH.md OQ#5).
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup: map[string]state.Container{
			"svc-a": {
				Service: "svc-a",
				Labels:  map[string]string{"hmi-update.allow-update": "false"},
			},
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/force-pull?recreate=true", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != strings.TrimSpace(actions.ActionBodyActionDisabledUpdate) {
		t.Errorf("body = %q\nwant = %q", rec.Body.String(), actions.ActionBodyActionDisabledUpdate)
	}
	if len(fake.forceCalls) != 0 {
		t.Errorf("ForcePull must NOT be called when CheckSafetyLabel rejects (got %+v)", fake.forceCalls)
	}
}

func TestHandleForcePull_DefaultExemptFromSafetyLabel(t *testing.T) {
	// SAFE-03 carve-out: force-pull without ?recreate ignores
	// allow-update=false (force-pull-no-recreate is read-only with respect
	// to the running container).
	fake := &fakeOrchestrator{
		selfSvc: "docker-update",
		lookup: map[string]state.Container{
			"svc-a": {
				Service: "svc-a",
				Labels:  map[string]string{"hmi-update.allow-update": "false"},
			},
		},
		forceRes: map[string]actions.ActionResult{
			"svc-a": {CurrentDigest: "sha256:cur"},
		},
	}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/svc-a/force-pull", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s (expected 200 — SAFE-03 exempts force-pull-no-recreate)", rec.Code, rec.Body.String())
	}
	if len(fake.forceCalls) != 1 || fake.forceCalls[0].recreate {
		t.Errorf("ForcePull calls = %+v, want one with recreate=false", fake.forceCalls)
	}
}

// ---------------------------------------------------------------------
// Path-leak guard for every error branch (T-04-04-03)
// ---------------------------------------------------------------------

func TestHandleActions_PathLeakGuard(t *testing.T) {
	// For every error sentinel branch, the response body MUST NOT echo
	// the test-host TempDir prefix. The test scaffold seeds the orchestrator
	// path inputs (state.json + compose stub) under t.TempDir(); if any
	// handler echoed a path from the wrap chain or from the error itself,
	// the assertion fires.
	tempDir := t.TempDir()
	cases := []struct {
		name string
		err  error
	}{
		{"service_busy", fmt.Errorf("actions.Update %s: %w", tempDir, actions.ErrServiceBusy)},
		{"compose_file_moved", fmt.Errorf("actions.Update %s: %w", tempDir, compose.ErrComposeFileMoved)},
		{"pull_failed", fmt.Errorf("actions.Update %s/imagepull: %w", tempDir, actions.ErrPullFailed)},
		{"compose_failed", fmt.Errorf("actions.Update %s/compose: %w", tempDir, actions.ErrComposeFailed)},
		{"verify_canceled", fmt.Errorf("actions.Update %s: %w", tempDir, actions.ErrVerifyCanceled)},
		{
			"verify_failed",
			fmt.Errorf("actions.Update %s: %w: %w",
				tempDir,
				actions.ErrVerifyFailed,
				&actions.VerifyDetail{
					RestartCount: 1, Running: false, ContainerID: "abc",
					Reason: "container restarted 1 times in 15s",
				}),
		},
		{"rollback_no_prev", fmt.Errorf("actions.Rollback %s: no_previous_digest", tempDir)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeOrchestrator{
				selfSvc:   "docker-update",
				lookup:    map[string]state.Container{"svc-a": {Service: "svc-a"}},
				updateErr: map[string]error{"svc-a": tc.err},
				rollErr:   map[string]error{"svc-a": tc.err},
			}
			srv := newOrchestratorTestServer(t, fake)
			// Route to /update by default; rollback_no_prev uses /rollback.
			path := "/api/containers/svc-a/update"
			if tc.name == "rollback_no_prev" {
				path = "/api/containers/svc-a/rollback"
			}
			req := httptest.NewRequest(http.MethodPost, path, nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if bytes.Contains(rec.Body.Bytes(), []byte(tempDir)) {
				t.Errorf("body leaks tempdir %q: %q", tempDir, rec.Body.String())
			}
			// Belt-and-braces: also reject /private/ /var/folders/ /tmp/
			// prefixes (the TempDir may be one of these on different OS).
			b := rec.Body.String()
			if strings.Contains(b, "/private/") || strings.Contains(b, "/var/folders/") || strings.Contains(b, "/tmp/") {
				t.Errorf("body leaks abs-path prefix: %q", b)
			}
		})
	}
}

// ---------------------------------------------------------------------
// Route registration smoke test
// ---------------------------------------------------------------------

func TestRoutes_ContainPhase4Endpoints(t *testing.T) {
	// Probe every Phase 4 action endpoint with a Server that has all
	// wiring nil except the mux — every route should respond (even if with
	// 503 actionBodyOrchestratorUnwired). If a route is NOT registered the
	// stdlib ServeMux returns 404; that's the regression we catch.
	dir := t.TempDir()
	store, err := state.NewStore(dir + "/state.json")
	if err != nil {
		t.Fatalf("state.NewStore: %v", err)
	}
	srv := NewServer(store, fakeClient{}, newTestReader(t, dir), nil, nil)

	endpoints := []string{
		"/api/containers/svc-a/update",
		"/api/containers/svc-a/rollback",
		"/api/containers/svc-a/force-pull",
	}
	for _, ep := range endpoints {
		req := httptest.NewRequest(http.MethodPost, ep, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code == http.StatusNotFound {
			t.Errorf("POST %s returned 404 — route not registered", ep)
		}
		// Existing endpoints stay reachable as well.
	}

	// Belt-and-braces — wrong method must NOT invoke the action handler.
	// Go 1.22+ method-scoped routing would naturally return 405, but the
	// static handler at "/" is the catch-all and absorbs GETs to
	// /api/containers/... with a 404 (no SPA fallback — Pitfall 8). The
	// invariant we actually care about: the action handler (which would
	// write a 200/4xx/5xx action body) is NOT invoked. We assert via the
	// body — a 404 from the static handler contains no action body
	// keywords ("current_digest", "no_op", "error":"..._failed").
	req := httptest.NewRequest(http.MethodGet, "/api/containers/svc-a/update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Errorf("GET /api/containers/svc-a/update returned 200 — action handler should not run for GET")
	}
	body := rec.Body.String()
	if strings.Contains(body, `"current_digest"`) || strings.Contains(body, `"no_op"`) {
		t.Errorf("GET response body looks like an action result: %q", body)
	}
}
