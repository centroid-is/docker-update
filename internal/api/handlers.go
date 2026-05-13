package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/centroid-is/hmi-update/internal/state"
)

// healthz returns 200 if the state store is reachable; 503 with a
// remediation hint otherwise.
//
// Phase 1's only liveness signal is "the in-memory store is non-nil and we
// can take an RLock without crashing." Because state.NewStore is called at
// boot and any I/O failure there causes log.Fatal, by the time we reach
// this handler we *should* be healthy. The defensive nil-check below covers
// the (impossible in practice) case where someone wires a Server with a nil
// store, and gives operators a clear "your state.Store is unwired" message
// rather than a 500 page.
//
// Phase 2 will add the docker-socket reachability check here (DOCK-03).
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	if s.store == nil {
		// Generic remediation hint per T-01-04-03: never echo internal file paths.
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unhealthy","reason":"state store unavailable; check HMI_UPDATE_STATE_PATH and restart"}`))
		return
	}

	// state.Store.Get takes an RLock; if Update is mid-flight we'll block
	// briefly but the contract is "in-memory snapshot, no I/O."
	_ = s.store.Get()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// getState returns the in-memory state snapshot as JSON.
//
// No I/O on this path - per OBS-03 (Phase 4) this must be cheap enough for
// 5s UI polling. The state.State and api.State types are json-tag identical
// (verified by tygo's source-of-truth contract in internal/api/types.go);
// we marshal state.State directly to avoid a redundant copy.
func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
	st := s.store.Get()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(st); err != nil {
		// Headers are already written; we cannot recover. Log and bail.
		slog.Error("getState: encode failed", "err", err)
		return
	}
}

// _ keeps the state import used even if a future refactor temporarily
// stops referencing the type by name. Go's compiler is strict about
// unused imports and this is a load-bearing dependency.
var _ = (*state.Store)(nil)
