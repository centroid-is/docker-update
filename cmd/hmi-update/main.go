// Command hmi-update is the single-binary container-update daemon for
// Centroid's HMI boxes.
//
// Phase 1 wires:
//   - state.Store (atomic JSON persistence; renameio + dir-fsync wrapper)
//   - api.Server (HTTP handlers: /healthz, /api/state, /, /assets/*)
//   - slog JSON handler with level configurable via HMI_UPDATE_LOG_LEVEL
//   - http.ListenAndServe on :8080 with 10s read/write timeouts
//
// Graceful SIGTERM shutdown is intentionally deferred to Phase 4 — STATE-04
// owns the SIGKILL fault-injection test and the cleaner shutdown story
// arrives there. Phase 1's main() is intentionally minimal.
package main

import (
	"log"
	"log/slog"
	"os"

	"github.com/centroid-is/hmi-update/internal/api"
	"github.com/centroid-is/hmi-update/internal/state"
)

func main() {
	// slog JSON handler; level via env per CONTEXT.md "Claude's Discretion".
	level := slog.LevelInfo
	if v := os.Getenv("HMI_UPDATE_LOG_LEVEL"); v != "" {
		// Minimal parsing — exact env values: debug, info, warn, error.
		switch v {
		case "debug":
			level = slog.LevelDebug
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	statePath := os.Getenv("HMI_UPDATE_STATE_PATH")
	if statePath == "" {
		statePath = "./hmi_update_state.json"
	}

	store, err := state.NewStore(statePath)
	if err != nil {
		log.Fatalf("state.NewStore: %v", err)
	}

	srv := api.NewServer(store)
	slog.Info("hmi-update starting", "addr", ":8080", "state_path", statePath)
	if err := srv.ListenAndServe(":8080"); err != nil {
		log.Fatalf("ListenAndServe: %v", err)
	}
}
