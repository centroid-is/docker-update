// Command hmi-update is the single-binary container-update daemon for
// Centroid's HMI boxes.
//
// Phase 1 ships a no-op main() so the repo compiles. Plan 04 (Wave 3) wires:
//   - state.Store (atomic JSON persistence)
//   - api.Server (HTTP handlers: /healthz, /api/state, /, /assets/*)
//   - http.ListenAndServe(":8080", mux)
//   - graceful shutdown on SIGTERM
package main

func main() {}
