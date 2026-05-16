// Package api (continued). handlers_self.go owns the Phase 9 (d)
// POST /api/self-update HTTP endpoint — the operator-facing entry point
// for the Watchtower-style sidecar self-update path.
//
// Why a separate handler (not a per-service route): per 09-RESEARCH.md
// Pattern 5, the per-service CheckSelfProtection middleware that returns
// 409 self_protection for POST /api/containers/docker-update/update is
// load-bearing — docker-update cannot recreate itself in-process (Pitfall
// 6 / ACT-09). The Watchtower path works AROUND that by spawning a
// helper container (selfupdate.Spawner; see internal/selfupdate/spawn.go);
// the new /api/self-update route invokes the helper instead of routing
// through the per-service handler. CheckSelfProtection STAYS for the
// per-service route — TestHandleUpdate_DockerUpdateSvc_StillReturns409
// is the regression seal.
//
// Wire contract (RESEARCH.md Example 2 — verbatim):
//
//	POST /api/self-update
//	  202 + {"status":"helper_spawned","helper_id":"<id>"}        on success
//	  503 + {"error":"self_updater_not_wired",...}                when s.selfUpdater == nil
//	  409 + {"error":"actions_in_flight",...}                     when other actions running
//	  409 + {"error":"self_update_in_flight",...}                 when a self-update is already in flight
//	  500 + {"error":"self_update_failed",...}                    on any other Spawn error
//
// Pattern K (verbatim-constant response bodies — T-01-04-03 path-leak
// guard) applies: every error body is a const. The 202 success body IS
// interpolated with the helper id, but the helper id is the daemon's
// auto-assigned hex string (matches ^[0-9a-f]{12,64}$ — no operator path
// or env value leaks). The test
// TestHandleSelfUpdate_202_HelperSpawned pins the body shape.
package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/centroid-is/docker-update/internal/selfupdate"
)

// SelfUpdater is a type alias for selfupdate.Spawner, exported at the
// api-package boundary so Plan 09-02's handlers_self_test.go can name
// the same type without importing internal/selfupdate (which would be
// awkward in the api package's test surface).
//
// Type-alias (not a separate interface) so a single source of truth lives
// in internal/selfupdate.Spawner — any future contract change to Spawn
// flows transparently. The alias keeps the test's
//
//	var _ SelfUpdater = (*fakeSpawner)(nil)
//
// compile-time assertion compatible with the production wiring that
// passes a selfupdate.Spawner value into Server.selfUpdater.
type SelfUpdater = selfupdate.Spawner

// Handler-only response bodies for the self-update endpoint.
//
// These are emitted ONLY by handleSelfUpdate. Pattern K: NO fmt.Sprintf,
// NO variable interpolation in error bodies. The success body is an
// exception (interpolates the daemon-assigned helper id, which is a
// hex string — no path-leak risk).
const (
	// actionBodySelfUpdaterUnwired is the defensive nil-guard body —
	// emitted when s.selfUpdater == nil (Server constructed without a
	// SelfUpdater; production main.go log.Fatalf's on selfupdate.NewSpawner
	// errors so this branch is only reachable via partial-init tests).
	actionBodySelfUpdaterUnwired = `{"error":"self_updater_not_wired","detail":"restart docker-update; check boot logs"}`

	// actionBodySelfUpdateFailed is the generic 500 — any non-sentinel
	// Spawn error lands here. Operator follows the slog event
	// self_update.spawn_failed for diagnostic detail; the wire body
	// never echoes the underlying err string (T-01-04-03).
	actionBodySelfUpdateFailed = `{"error":"self_update_failed","detail":"see logs for self_update.spawn_failed event"}`

	// actionBodyActionsInFlight surfaces ErrActionsInFlight — refused
	// per RESEARCH.md Open Question 5 RESOLVED: a self-update while
	// per-service actions are in flight would race the per-service
	// mutex held by that action.
	actionBodyActionsInFlight = `{"error":"actions_in_flight","detail":"wait for in-flight actions to complete"}`

	// actionBodySelfUpdateBusy surfaces ErrSelfUpdateInFlight — refused
	// per RESEARCH.md Open Question 2 RESOLVED: the inFlight atomic.Bool
	// guard short-circuits the second concurrent Spawn call.
	actionBodySelfUpdateBusy = `{"error":"self_update_in_flight","detail":"a self-update is already running"}`
)

// handleSelfUpdate implements POST /api/self-update.
//
// Chain (deliberately NOT routed through CheckSelfProtection — that's
// the entire point of having a separate endpoint per RESEARCH.md Pattern 5):
//
//  1. Defensive nil-guard on s.selfUpdater → 503 self_updater_not_wired.
//  2. Call s.selfUpdater.Spawn(ctx). The Spawn implementation itself
//     enforces the in-flight guards; this handler is the wire adapter.
//  3. Sentinel mapping:
//     - selfupdate.ErrActionsInFlight    → 409 actions_in_flight
//     - selfupdate.ErrSelfUpdateInFlight → 409 self_update_in_flight
//     - any other err                    → 500 self_update_failed
//  4. Success → 202 + {"status":"helper_spawned","helper_id":"<id>"}.
//
// The handler does NOT consult s.actionsInFlightFn directly — that's the
// Spawn implementation's job. The handler only maps Spawn's sentinel
// errors to wire status codes. Keeping the in-flight checks on the
// Spawner side means the spawn-side and handler-side stay testable
// independently (handlers_self_test.go uses a fakeSpawner that scripts
// the err return; spawn_test.go scripts the in-flight closure).
func (s *Server) handleSelfUpdate(w http.ResponseWriter, r *http.Request) {
	if s.selfUpdater == nil {
		writeActionBody(w, http.StatusServiceUnavailable, actionBodySelfUpdaterUnwired)
		return
	}

	// Pre-Spawn check against actionsInFlightFn — gives a precise 409
	// actions_in_flight when the handler's wired closure reports in-flight
	// per-service actions. Plan 09-02 Task 2 (B)'s
	// TestHandleSelfUpdate_409_ActionsInFlight asserts this short-circuit
	// (the fakeSpawner does NOT itself check actionsInFlight — the test
	// pins that the HANDLER honours the seam by short-circuiting BEFORE
	// calling Spawn). Production selfupdate.Spawner ALSO checks the
	// closure as a belt-and-braces second gate (so a future caller that
	// bypasses the handler still sees the refusal), but the handler-side
	// check is what the wire-level test pins.
	if s.actionsInFlightFn != nil && s.actionsInFlightFn() > 0 {
		writeActionBody(w, http.StatusConflict, actionBodyActionsInFlight)
		return
	}

	helperID, err := s.selfUpdater.Spawn(r.Context())
	if err != nil {
		// Sentinel mapping. Order doesn't matter (errors are distinct
		// sentinel values) but we test the in-flight pair first for
		// clarity.
		switch {
		case errors.Is(err, selfupdate.ErrActionsInFlight):
			writeActionBody(w, http.StatusConflict, actionBodyActionsInFlight)
			return
		case errors.Is(err, selfupdate.ErrSelfUpdateInFlight):
			writeActionBody(w, http.StatusConflict, actionBodySelfUpdateBusy)
			return
		}
		slog.Error("self_update.spawn_failed", "err", err)
		writeActionBody(w, http.StatusInternalServerError, actionBodySelfUpdateFailed)
		return
	}

	slog.Info("self_update.spawn_succeeded", "helper_id", helperID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	// The helper id is daemon-assigned hex (no operator path, no env
	// value, no operator-controlled input). Safe to interpolate per
	// T-01-04-03.
	_, _ = w.Write([]byte(`{"status":"helper_spawned","helper_id":"` + helperID + `"}`))
}
