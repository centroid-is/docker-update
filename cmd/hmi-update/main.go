// Command hmi-update is the single-binary container-update daemon for
// Centroid's HMI boxes.
//
// Phase 3 boot order (CONTEXT.md "Lifecycle & Wiring" + 03-04-PLAN.md):
//  1. slog handler (level via HMI_UPDATE_LOG_LEVEL)
//  2. state.NewStore (path via HMI_UPDATE_STATE_PATH)
//  3. docker.NewClient(ctx)
//  4. compose.NewReader(env)
//  4.5. registry.NewRedactingTransport — http.RoundTripper wrapper, strips sensitive headers
//  4.6. registry.NewResolver(transport) — crane.Digest facade
//  4.7. slog.Info("registry.authn", "keychain", "anonymous") — OBS-04 boot attestation
//  4.8. poll.NewPatterns — compiled tag-pattern regex cache
//  4.9. updates := make(chan poll.StateUpdate, 64) — single-consumer channel
//  4.10. go poll.RunUpdater(ctx, updates, store) — single consumer goroutine
//  5. docker.NewDiscoverer(dockerClient, store, updates, patterns) — promoted to channel producer
//  5.5. cronExpr from HMI_UPDATE_CRON (default "0 * * * *")
//  5.6. poll.NewPoller(cronExpr, resolver, patterns, store, updates) — second producer
//  5.7. go poller.Run(ctx) — cron-driven sweep producer
//  6. api.NewServer(store, dockerClient, composeReader).ListenAndServe(":8080")
//
// The slog ReplaceAttr regex (output-side OBS-04 defense) lands in Plan
// 03-05 alongside its e2e redaction test. Phase 3 plan 03-04 ships only
// the transport-side defense + the boot attestation event.
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
	"github.com/centroid-is/hmi-update/internal/poll"
	"github.com/centroid-is/hmi-update/internal/registry"
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

	// 4.5. registry.NewRedactingTransport — the http.RoundTripper passed
	// to crane.WithTransport. Strips sensitive headers (Authorization,
	// WWW-Authenticate, X-Registry-Auth, Proxy-Authorization) from any
	// internal logging the transport itself emits (OBS-04 transport-side
	// defense). The wire-level request is unchanged — crane needs
	// Authorization: Bearer <jwt> to function — but slog must never see
	// the value. Phase 3 plan 03-05 lands the output-side counterpart
	// (slog ReplaceAttr regex on ^Bearer / ^Basic).
	transport := registry.NewRedactingTransport()

	// 4.6. registry.NewResolver — wraps crane.Digest. NewResolver does
	// NOT return an error (construction has no I/O); future wiring
	// failures show up at the first poller tick.
	resolver := registry.NewResolver(transport)

	// 4.7. OBS-04 boot attestation event. Operators tail `journalctl`
	// and confirm we're using anonymous keychain (NOT DefaultKeychain —
	// Pitfall 2). The exact spelling matters because Plan 03-05's e2e
	// log scan greps for this string.
	slog.Info("registry.authn", "keychain", "anonymous")

	// 4.8. poll.NewPatterns — compiled tag-pattern regex cache.
	// Populated by docker.Discoverer's upsertFromInspect on every start
	// event (Plan 03-04 Task 1).
	patterns := poll.NewPatterns()

	// 4.9. The single state-update channel. Producers: docker.Discoverer
	// (Phase 2 promoted by Plan 03-04 Task 1) + poll.cronPoller
	// (Plan 03-03). Consumer: poll.RunUpdater. Cap=64 absorbs cron-tick
	// bursts on hosts with many watched containers. CONTEXT.md Area 2
	// "Channel pattern."
	updates := make(chan poll.StateUpdate, 64)

	// 4.10. Spawn the single consumer. RunUpdater drains pending
	// messages on ctx cancel (graceful shutdown story; Phase 4 STATE-04
	// will harden under SIGKILL). Must be spawned BEFORE any producer
	// goroutine, otherwise the first event/sweep could block on a full
	// channel.
	go poll.RunUpdater(ctx, updates, store)

	// 5. docker.NewDiscoverer with the Phase 3 signature: store retained
	// for the read path (serviceForContainerID), but mutations now flow
	// through the updates channel. Patterns is shared with cronPoller
	// via the Patterns cache. Spawned async so the HTTP server comes up
	// immediately; the first GET /api/state may legitimately return an
	// empty containers map while the boot ContainerList is in-flight.
	// The Playwright discovery test in plan 02-05 polls for up to 60s.
	//
	// Discoverer.Run blocks until ctx is cancelled or the events stream
	// errors irrecoverably (the reconnect loop handles transient errors).
	discoverer := docker.NewDiscoverer(dockerClient, store, updates, patterns)
	go func() {
		if err := discoverer.Run(ctx); err != nil {
			slog.Error("discovery.run.exited", "err", err)
		}
	}()

	// 5.5. Cron expression from env. Default "0 * * * *" matches
	// CLAUDE.md / brief; tests override with "@every 5s" via the e2e
	// compose env.
	cronExpr := os.Getenv("HMI_UPDATE_CRON")
	if cronExpr == "" {
		cronExpr = "0 * * * *"
	}

	// 5.6. poll.NewPoller — fails fast on invalid cron expression with
	// a paste-ready remediation message (Phase-3-specific pitfall —
	// RESEARCH.md "Cron string parsing mode mismatch"). The error wraps
	// the original parser error via %w so operators can grep for both
	// "HMI_UPDATE_CRON" and the underlying parse failure.
	poller, err := poll.NewPoller(cronExpr, resolver, patterns, store, updates)
	if err != nil {
		log.Fatalf("poll.NewPoller: %v", err)
	}

	// 5.7. Spawn the cron producer. cronPoller.Run blocks on ctx.Done(),
	// then drains in-flight ticks via cron.Stop().Done() (Phase-3 pitfall —
	// RESEARCH.md "Cron Stop() not awaited"). On exit it returns ctx.Err()
	// which we log but don't propagate — the HTTP server's ListenAndServe
	// call is the process's main blocking surface.
	go func() {
		if err := poller.Run(ctx); err != nil {
			slog.Info("poller.run.exited", "err", err)
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
