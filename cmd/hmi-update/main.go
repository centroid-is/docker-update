// Command hmi-update is the single-binary container-update daemon for
// Centroid's HMI boxes.
//
// Phase 2 boot order (CONTEXT.md "Lifecycle & Wiring"):
//  1. slog handler (level via HMI_UPDATE_LOG_LEVEL)
//  2. state.NewStore (path via HMI_UPDATE_STATE_PATH, default ./hmi_update_state.json)
//  3. docker.NewClient(ctx) — DOCK-01 (fail-fast on bad DOCKER_HOST)
//  4. compose.NewReader(env) — DOCK-02 (fail-fast on missing
//     HMI_UPDATE_COMPOSE_PATH or unstattable file)
//  5. docker.Discoverer goroutine — DOCK-04 (boot list + events loop;
//     does NOT block the HTTP server starting up)
//  6. api.NewServer(store, dockerClient, composeReader).ListenAndServe(":8080")
//
// Each constructor fail-fast log.Fatalf includes the constructor name in
// the error message so an operator greps `journalctl` and immediately knows
// which subsystem refused to start (T-02-04-05 — repudiation mitigation).
//
// Graceful SIGTERM shutdown is intentionally deferred to Phase 4 (STATE-04
// owns the SIGKILL fault-injection test and the cleaner shutdown story).
// The Discoverer goroutine receives ctx = context.Background() in v2; when
// Phase 4 lands graceful shutdown it will replace this with a
// context.WithCancel(ctx) hooked to a SIGTERM handler.
package main

import (
	"context"
	"log"
	"log/slog"
	"os"

	"github.com/centroid-is/hmi-update/internal/api"
	"github.com/centroid-is/hmi-update/internal/compose"
	"github.com/centroid-is/hmi-update/internal/docker"
	"github.com/centroid-is/hmi-update/internal/state"
)

func main() {
	// 1. slog JSON handler; level via env per CONTEXT.md "Claude's Discretion".
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

	// 2. state.NewStore (unchanged from Phase 1).
	statePath := os.Getenv("HMI_UPDATE_STATE_PATH")
	if statePath == "" {
		statePath = "./hmi_update_state.json"
	}
	store, err := state.NewStore(statePath)
	if err != nil {
		log.Fatalf("state.NewStore: %v", err)
	}

	// 3. docker.NewClient (DOCK-01). FromEnv honours DOCKER_HOST for tests
	// and falls back to /var/run/docker.sock. API version negotiation is
	// on so the same binary works against any Engine API the HMI happens
	// to be running.
	//
	// ctx = context.Background() because graceful shutdown is Phase 4's
	// concern (STATE-04). We hold the ctx in a named var so the
	// Discoverer goroutine and any future ctx-scoped consumers share it
	// — Phase 4's SIGTERM handler replaces this with a context.WithCancel
	// derived context.
	ctx := context.Background()
	dockerClient, err := docker.NewClient(ctx)
	if err != nil {
		log.Fatalf("docker.NewClient: %v", err)
	}

	// 4. compose.NewReader (DOCK-02). Plan 02-05's Task 0 wires
	// HMI_UPDATE_COMPOSE_PATH into the e2e compose stack so this
	// log.Fatalf does NOT fire under tests. An unset env var here is a
	// configuration error — the operator must point us at the
	// bind-mounted docker-compose.yml.
	composePath := os.Getenv("HMI_UPDATE_COMPOSE_PATH")
	composeReader, err := compose.NewReader(composePath)
	if err != nil {
		log.Fatalf("compose.NewReader: %v", err)
	}

	// 5. docker.Discoverer goroutine (DOCK-04). Spawned async so the HTTP
	// server comes up immediately; the first GET /api/state may
	// legitimately return an empty containers map while the boot
	// ContainerList is in-flight. The Playwright discovery test in plan
	// 02-05 polls for up to 60s.
	//
	// Discoverer.Run blocks until ctx is cancelled or the events stream
	// errors irrecoverably (the reconnect loop handles transient errors).
	// We log.Fatalf-equivalent via slog.Error if Run exits with an error
	// — the goroutine cannot kill the process directly without racing the
	// HTTP server. A future graceful-shutdown story (Phase 4) will
	// surface this through the same context cancellation path that
	// brings down the HTTP server.
	discoverer := docker.NewDiscoverer(dockerClient, store)
	go func() {
		if err := discoverer.Run(ctx); err != nil {
			slog.Error("discovery.run.exited", "err", err)
		}
	}()

	// 6. api.NewServer with the Phase 2 three-arg signature.
	srv := api.NewServer(store, dockerClient, composeReader)
	slog.Info("hmi-update starting",
		"addr", ":8080",
		"state_path", statePath,
		"compose_path", composePath,
	)
	if err := srv.ListenAndServe(":8080"); err != nil {
		log.Fatalf("ListenAndServe: %v", err)
	}
}
