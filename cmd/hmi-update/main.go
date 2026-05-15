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
//
// Phase 4 boot order additions (CONTEXT.md "Integration Points"):
//  4.11. compose.NewRunner(composePath) — Phase 4 plan 04-02
//        (exec.LookPath("docker") at construction; fail-fast on missing CLI)
//  5.8.  HMI_UPDATE_SELF_SERVICE / HMI_UPDATE_VERIFY_WINDOW_S /
//        HMI_UPDATE_HEALTHCHECK_WINDOW_S env reads (CONTEXT Area 4)
//  5.9.  actions.NewOrchestrator(dockerClient, runner, resolver,
//        composeReader, store, updates, selfService, verifyWindow,
//        healthcheckWindow) — Phase 4 plan 04-03 (third state producer
//        via the same updates channel)
//  6.    api.NewServer(store, dockerClient, composeReader, orchestrator).ListenAndServe(":8080")
//
// The slog ReplaceAttr regex (output-side OBS-04 defense) is installed
// at boot step 1 via newRedactingHandler — partners with internal/registry's
// redactingTransport (request-side defense). Phase 3 plan 03-05 landed
// this defense alongside the e2e redaction test
// (e2e/tests/obs-04-redaction.spec.ts).
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
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/centroid-is/hmi-update/internal/actions"
	"github.com/centroid-is/hmi-update/internal/api"
	"github.com/centroid-is/hmi-update/internal/compose"
	"github.com/centroid-is/hmi-update/internal/docker"
	"github.com/centroid-is/hmi-update/internal/poll"
	"github.com/centroid-is/hmi-update/internal/registry"
	"github.com/centroid-is/hmi-update/internal/state"
)

// newRedactingHandler builds a slog.JSONHandler whose ReplaceAttr
// closure elides any string-kinded attr whose value matches
// ^(Bearer|Basic)\s, contains "Bearer "/"Basic " as a substring, OR
// looks like a bare base64-encoded "username:password" credential pair
// (the Pitfall 2 regression shape: `Og==` is the empty-creds placeholder
// DefaultKeychain emits when docker login was run with an empty
// username — without the "Basic " prefix). Output-side OBS-04 defense;
// partners with internal/registry's redactingTransport (request-side
// defense). Either alone is sufficient under the CONTEXT.md Area 4
// threat model; both together survive a future careless logger call
// that bypasses the transport.
//
// Why three layers (regex + substring + bare-base64):
//   - The ^(Bearer|Basic)\s regex catches the canonical case of a
//     header value passed directly as an attr value (e.g.
//     slog.String("authorization", req.Header.Get("Authorization"))).
//   - The strings.Contains fallback catches a logger that
//     concatenated key+value into one string (e.g.
//     slog.String("header", "Authorization=Bearer xyz")) — the
//     regex anchor would miss this shape.
//   - The bare-base64 probe (CR-03) catches a logger that stripped the
//     "Basic " prefix before passing the value to slog, e.g.
//     slog.String("authn", "Og=="). Decodes the value via base64 and
//     redacts if the decoded payload contains a colon — the
//     "username:password" shape RFC 7617 §2 mandates for Basic auth.
//     Bounded by length (4..200 bytes) to keep the cost negligible
//     on the 99% non-credential string path.
//
// Non-string attrs (ints, durations, times, bools) are checked first
// via a.Value.Kind() and pass through with no regex overhead.
//
// Test contract: cmd/hmi-update/main_test.go's TestSlogReplaceAttr_*
// suite exercises all three paths plus the negative pass-through cases.
func newRedactingHandler(out io.Writer, level slog.Level) slog.Handler {
	bearerOrBasic := regexp.MustCompile(`^(Bearer|Basic)\s`)
	return slog.NewJSONHandler(out, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Value.Kind() != slog.KindString {
				return a
			}
			s := a.Value.String()
			if bearerOrBasic.MatchString(s) {
				return slog.Attr{Key: a.Key, Value: slog.StringValue("REDACTED")}
			}
			if strings.Contains(s, "Bearer ") || strings.Contains(s, "Basic ") {
				return slog.Attr{Key: a.Key, Value: slog.StringValue("REDACTED")}
			}
			// CR-03: bare base64-encoded credentials probe. The length
			// guard keeps the cost negligible for non-credential strings;
			// the HasSuffix("=") guard is a pre-filter so we only attempt
			// a base64 decode on values that visibly end with the
			// padding character. A successful decode that contains ':'
			// matches RFC 7617's user:pass shape — treat as Basic-auth
			// credential and redact.
			if len(s) >= 4 && len(s) <= 200 && strings.HasSuffix(s, "=") {
				if decoded, err := base64.StdEncoding.DecodeString(s); err == nil &&
					bytes.Contains(decoded, []byte{':'}) {
					return slog.Attr{Key: a.Key, Value: slog.StringValue("REDACTED")}
				}
			}
			return a
		},
	})
}

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
	// newRedactingHandler installs the OBS-04 output-side defense: any
	// string-kinded attr whose value matches ^(Bearer|Basic)\s or
	// contains "Bearer "/"Basic " mid-string is replaced with "REDACTED".
	// Belt-and-braces with internal/registry's redactingTransport
	// (request-side defense). See newRedactingHandler godoc.
	slog.SetDefault(slog.New(newRedactingHandler(os.Stdout, level)))

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

	// 4.11. compose.NewRunner — Phase 4 plan 04-02 body. exec.LookPath("docker")
	// runs at construction so a missing docker CLI fails fast at boot rather
	// than on the first Update click (T-04-02-05). The runner is consumed by
	// actions.NewOrchestrator below (step 5.9); main.go does not invoke
	// UpdateService directly.
	runner, err := compose.NewRunner(composePath)
	if err != nil {
		log.Fatalf("compose.NewRunner: %v", err)
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

	// 5.8. Phase 4 env vars (CONTEXT.md Area 4 + Area 3).
	//
	// HMI_UPDATE_SELF_SERVICE (default "hmi-update") is the compose service
	// name THIS process runs as; the action middleware compares the
	// {service} path-parameter against it and refuses self-update with 409
	// self_protection (ACT-09 — the manual self-upgrade procedure in
	// PROJECT.md is the documented escape hatch).
	//
	// HMI_UPDATE_VERIFY_WINDOW_S (default 15) is the verify-after-recreate
	// poll duration; HMI_UPDATE_HEALTHCHECK_WINDOW_S (default 60) is the
	// extended window when a container opts in via
	// hmi-update.wait-for-healthy=true label.
	selfService := os.Getenv("HMI_UPDATE_SELF_SERVICE")
	if selfService == "" {
		selfService = "hmi-update"
	}
	verifyWindow := time.Duration(envInt("HMI_UPDATE_VERIFY_WINDOW_S", 15)) * time.Second
	healthcheckWindow := time.Duration(envInt("HMI_UPDATE_HEALTHCHECK_WINDOW_S", 60)) * time.Second

	// 5.9. actions.NewOrchestrator — THIRD producer of state mutations
	// (docker.Discoverer + poll.cronPoller are the first two). All three
	// feed the single `updates` channel; RunUpdater (step 4.10) is the
	// single consumer — DETECT-10 carry-forward.
	orchestrator, err := actions.NewOrchestrator(
		dockerClient, runner, resolver, composeReader, store, updates,
		selfService, verifyWindow, healthcheckWindow,
	)
	if err != nil {
		log.Fatalf("actions.NewOrchestrator: %v", err)
	}

	// 6. api.NewServer with the Phase 4 four-arg signature (Plan 04-04).
	srv := api.NewServer(store, dockerClient, composeReader, orchestrator)
	slog.Info("hmi-update starting",
		"addr", ":8080",
		"state_path", statePath,
		"compose_path", composePath,
		"self_service", selfService,
		"verify_window", verifyWindow.String(),
		"healthcheck_window", healthcheckWindow.String(),
	)
	if err := srv.ListenAndServe(":8080"); err != nil {
		log.Fatalf("ListenAndServe: %v", err)
	}
}

// envInt reads an int from the named env var, falling back to def if
// missing, unparseable, or <= 0. Mirrors internal/poll/poller.go's
// envInt helper (Plan 04-04 reuses the convention; copy-paste rather
// than promote-export keeps the poll package's API narrow).
func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
