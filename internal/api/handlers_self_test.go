// handlers_self_test.go — RED-first per C4 (CLAUDE.md).
//
// This file is the Wave 0 regression guard for SC-4 (POST /api/self-update
// returns 202 + helper-spawned body) and SC-6 (iv) (CheckSelfProtection
// behaviour: per-service endpoint still 409 for the self target; new
// /api/self-update endpoint bypasses CheckSelfProtection).
//
// At the moment 09-02 lands, NO production code in internal/api supports
// `POST /api/self-update`. The plan's intent is that:
//
//  1. `go test ./internal/api/...` fails with one or more of:
//       - undefined: SelfUpdater
//       - s.selfUpdater undefined (type *Server has no field or method selfUpdater)
//       - s.actionsInFlightFn undefined
//       - handleSelfUpdate undefined
//     OR — once Plan 09-04 adds the fields/interface but BEFORE main.go
//     registers the route — a 404 from the mux on POST /api/self-update.
//     Either failure mode is the documented RED state.
//  2. Plan 09-04 lands:
//       - SelfUpdater interface in internal/api
//       - Server.selfUpdater field
//       - Server.actionsInFlightFn (or equivalent seam — see RESEARCH.md
//         Open Question 5)
//       - handleSelfUpdate method
//       - mux.HandleFunc("POST /api/self-update", s.handleSelfUpdate)
//     and the tests in this file turn GREEN.
//
// Per RESEARCH.md Example 2 + Pattern 5 the route is:
//
//   POST /api/self-update
//     - 202 + {"status":"helper_spawned","helper_id":"<id>"} on success
//     - 503 + {"error":"self_updater_not_wired",...}              when s.selfUpdater == nil
//     - 409 + {"error":"actions_in_flight",...}                   when other actions running
//     - bypasses CheckSelfProtection (the whole point of the new endpoint)
//
// Per RESEARCH.md Pattern 5 the per-service endpoint
// POST /api/containers/{svc}/update STILL returns 409 self_protection
// when svc == selfService — only /api/self-update bypasses that check.
// TestHandleUpdate_DockerUpdateSvc_StillReturns409 below is the control
// case that pins that contract.

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centroid-is/docker-update/internal/state"
)

// fakeSpawner implements the SelfUpdater interface that Plan 09-04 will
// add to internal/api. Per RESEARCH.md Example 2:
//
//	type SelfUpdater interface {
//	    Spawn(ctx context.Context) (helperID string, err error)
//	}
//
// The fake records the call count + returns scripted (id, err).
type fakeSpawner struct {
	returnID  string
	returnErr error
	calls     int
}

func (f *fakeSpawner) Spawn(ctx context.Context) (string, error) {
	f.calls++
	if f.returnErr != nil {
		return "", f.returnErr
	}
	return f.returnID, nil
}

// Compile-time interface assertion. The SelfUpdater type does NOT EXIST
// in package api yet — this line will fail to compile until Plan 09-04
// adds it (per the plan's RED-first contract).
var _ SelfUpdater = (*fakeSpawner)(nil)

// newSelfUpdateTestServer constructs a Server wired with an injectable
// selfUpdater and an actionsInFlight seam. The field names selfUpdater
// and actionsInFlightFn do NOT EXIST on Server yet — Plan 09-04 adds them.
//
// This helper deliberately uses direct struct-field assignment (not a
// constructor) so the RED-state failure messages are precise: "Server has
// no field or method selfUpdater" tells the next plan exactly what to
// add. A NewServerForTest constructor would just hide the missing fields
// behind a layer of indirection.
func newSelfUpdateTestServer(t *testing.T, spawner SelfUpdater, inFlight int) *Server {
	t.Helper()
	dir := t.TempDir()
	// Use the existing test seams to build the bottom layers of a Server
	// (state.Store + fakeClient + a Reader pointing at a stub compose
	// file). The selfUpdater + actionsInFlightFn fields are then injected
	// directly. Plan 09-04 may grow NewServer to accept selfUpdater as a
	// constructor arg; this helper continues to work because it only
	// post-injects the test-specific fields.
	store, err := state.NewStore(filepath.Join(dir, "docker_update_state.json"))
	if err != nil {
		t.Fatalf("state.NewStore: %v", err)
	}
	srv := NewServer(store, fakeClient{}, newTestReader(t, dir), nil, nil)
	srv.selfUpdater = spawner
	// actionsInFlightFn returns the count of in-flight per-service actions.
	// >0 means "self-update must 409" (RESEARCH.md Open Question 5).
	srv.actionsInFlightFn = func() int { return inFlight }
	return srv
}

// ----------------------------------------------------------------------------
// SC-4 (a) — TestHandleSelfUpdate_202_HelperSpawned
//
// Happy path: selfUpdater is wired, no actions in flight; POST /api/self-update
// → 202 + body {"status":"helper_spawned","helper_id":"<id>"}.
// ----------------------------------------------------------------------------

func TestHandleSelfUpdate_202_HelperSpawned(t *testing.T) {
	spawner := &fakeSpawner{returnID: "helper-abc"}
	srv := newSelfUpdateTestServer(t, spawner, 0)
	req := httptest.NewRequest(http.MethodPost, "/api/self-update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"helper_spawned"`) {
		t.Errorf("body missing helper_spawned status: %q", body)
	}
	if !strings.Contains(body, `"helper_id":"helper-abc"`) {
		t.Errorf("body missing helper_id=helper-abc: %q", body)
	}
	if spawner.calls != 1 {
		t.Errorf("Spawn calls: want 1, got %d", spawner.calls)
	}
}

// ----------------------------------------------------------------------------
// SC-4 (a) negative — TestHandleSelfUpdate_503_WhenUnwired
//
// Defensive nil-guard: if main.go never wired a SelfUpdater (production
// log.Fatalf's on selfupdate.NewSpawner errors, so this branch is only
// reachable via partial-init tests) → 503 + self_updater_not_wired body.
// ----------------------------------------------------------------------------

func TestHandleSelfUpdate_503_WhenUnwired(t *testing.T) {
	srv := newSelfUpdateTestServer(t, nil, 0)
	req := httptest.NewRequest(http.MethodPost, "/api/self-update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "self_updater_not_wired") {
		t.Errorf("body missing self_updater_not_wired: %q", rec.Body.String())
	}
}

// ----------------------------------------------------------------------------
// SC-6 (iv) — TestHandleSelfUpdate_BypassesCheckSelfProtection
//
// The new endpoint MUST NOT route through CheckSelfProtection — that's
// the whole point of having a separate endpoint per RESEARCH.md Pattern 5.
// We assert by:
//   1. Wiring a Server whose selfService matches docker-update (the value
//      that would normally trigger the 409 self_protection branch).
//   2. POSTing to /api/self-update (NOT /api/containers/docker-update/update).
//   3. Asserting 202 + the helper-spawned body — NOT 409.
// ----------------------------------------------------------------------------

func TestHandleSelfUpdate_BypassesCheckSelfProtection(t *testing.T) {
	spawner := &fakeSpawner{returnID: "helper-bypass"}
	srv := newSelfUpdateTestServer(t, spawner, 0)
	// Force selfService=docker-update so a CheckSelfProtection slip would
	// fire 409 — the test must observe 202 instead.
	//
	// NOTE: srv.orchestrator is nil in this fixture (the helper passes nil
	// to NewServer). The per-service handlers' CheckSelfProtection call
	// goes through orchestrator.CheckSelfProtection — so we don't directly
	// hit it here. The contract we're pinning is the ROUTING decision:
	// the /api/self-update mux entry MUST land on handleSelfUpdate, NOT
	// on handleUpdate, regardless of selfService value.
	req := httptest.NewRequest(http.MethodPost, "/api/self-update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusConflict {
		t.Fatalf("status: 409 conflict means CheckSelfProtection fired; the /api/self-update endpoint MUST bypass it (RESEARCH.md Pattern 5). body=%s", rec.Body.String())
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202 (bypass success), got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"helper_spawned"`) {
		t.Errorf("body missing helper_spawned: %q", rec.Body.String())
	}
	if spawner.calls != 1 {
		t.Errorf("Spawn calls: want 1, got %d", spawner.calls)
	}
}

// ----------------------------------------------------------------------------
// SC-6 (iv) control — TestHandleUpdate_DockerUpdateSvc_StillReturns409
//
// The CheckSelfProtection contract MUST still fire for the per-service
// endpoint. /api/self-update bypasses; /api/containers/docker-update/update
// still 409s. If a Plan 09-04 implementation accidentally relaxes
// CheckSelfProtection (e.g. by guarding it on "self-update endpoint
// exists" instead of "this is the per-service endpoint") this test fails.
//
// Uses the existing handlers_actions_test.go pattern: fakeOrchestrator
// with selfSvc=docker-update.
// ----------------------------------------------------------------------------

func TestHandleUpdate_DockerUpdateSvc_StillReturns409(t *testing.T) {
	fake := &fakeOrchestrator{selfSvc: "docker-update"}
	srv := newOrchestratorTestServer(t, fake)
	req := httptest.NewRequest(http.MethodPost, "/api/containers/docker-update/update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status: want 409 (per-service self_protection still fires post-Phase-9), got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "self_protection") {
		t.Errorf("body missing self_protection: %q", rec.Body.String())
	}
	if len(fake.updateCalls) != 0 {
		t.Errorf("Update MUST NOT be called for self-targeted per-service endpoint")
	}
}

// ----------------------------------------------------------------------------
// SC-6 (iv) — TestHandleSelfUpdate_409_ActionsInFlight
//
// Per RESEARCH.md Open Question 5: if another per-service action is in
// flight, POST /api/self-update returns 409 actions_in_flight (the
// operator must wait for in-flight actions to drain before self-update
// triggers a recreate of docker-update itself).
//
// The actionsInFlightFn field on Server returns the current count; the
// handler short-circuits to 409 when count > 0.
// ----------------------------------------------------------------------------

func TestHandleSelfUpdate_409_ActionsInFlight(t *testing.T) {
	spawner := &fakeSpawner{returnID: "helper-busy"}
	srv := newSelfUpdateTestServer(t, spawner, 1)
	req := httptest.NewRequest(http.MethodPost, "/api/self-update", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status: want 409 (actions in flight), got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "actions_in_flight") {
		t.Errorf("body missing actions_in_flight: %q", rec.Body.String())
	}
	if spawner.calls != 0 {
		t.Errorf("Spawn calls: want 0 (short-circuit before spawn), got %d", spawner.calls)
	}
}
