// Command docker-update is the single-binary container-update daemon for
// Centroid's HMI boxes.
//
// Phase 3 boot order (CONTEXT.md "Lifecycle & Wiring" + 03-04-PLAN.md):
//  1. slog handler (level via DOCKER_UPDATE_LOG_LEVEL)
//  2. state.NewStore (path via DOCKER_UPDATE_STATE_PATH)
//  3. docker.NewClient(ctx)
//  4. compose.NewReader(env)
//  4.5. registry.NewRedactingTransport — http.RoundTripper wrapper, strips sensitive headers
//  4.6. registry.NewResolver(transport) — crane.Digest facade
//  4.7. slog.Info("registry.authn", "keychain", "anonymous") — OBS-04 boot attestation
//  4.8. poll.NewPatterns — compiled tag-pattern regex cache
//  4.9. updates := make(chan poll.StateUpdate, 64) — single-consumer channel
//  4.10. go poll.RunUpdater(ctx, updates, store) — single consumer goroutine
//  5. docker.NewDiscoverer(dockerClient, store, updates, patterns) — promoted to channel producer
//  5.5. cronExpr from DOCKER_UPDATE_CRON (default "0 * * * *")
//  5.6. poll.NewPoller(cronExpr, resolver, patterns, store, updates) — second producer
//  5.7. go poller.Run(ctx) — cron-driven sweep producer
//
// Phase 4 boot order additions (CONTEXT.md "Integration Points"):
//  5.8.  DOCKER_UPDATE_SELF_SERVICE / DOCKER_UPDATE_VERIFY_WINDOW_S /
//        DOCKER_UPDATE_HEALTHCHECK_WINDOW_S env reads (CONTEXT Area 4)
//  5.9.  actions.NewOrchestrator(dockerClient, resolver, composeReader,
//        store, updates, selfService, verifyWindow, healthcheckWindow)
//        — Phase 4 plan 04-03; Phase 9 (a) signature drop of the runner
//        parameter (Plan 09-03 deleted compose.Runner; recreate.Service
//        consumes docker.Client directly).
//  5.10. DOCKER_UPDATE_SELF_IMAGE / DOCKER_UPDATE_SELF_UPDATE_KEEP_HELPER env reads
//        (Phase 9 (d) / Plan 09-04 — self-update Spawner wiring)
//  5.11. selfupdate.NewSpawner(dockerClient, selfImage, selfService,
//        orchestrator.ActionsInFlightFn(), keepHelper) — parent-side helper
//        spawner; consumed by api handleSelfUpdate via WireSelfUpdate
//  6.    api.NewServer(store, dockerClient, composeReader, orchestrator,
//        poller).WireSelfUpdate(spawner, orchestrator.ActionsInFlightFn()).ListenAndServe(":8080")
//
// Helper-mode boot (--self-update-orchestrator flag — set by the parent
// at Spawn time): runSelfUpdateOrchestrator() short-circuits the entire
// server-mode boot path. The helper builds only a docker.Client and runs
// selfupdate.Orchestrate(ctx, cli, target, healthzURL, delay, verifyTimeout).
// Exits 0 on success (AutoRemove GCs the helper), 1 on any failure.
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
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"mime"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/centroid-is/docker-update/internal/actions"
	"github.com/centroid-is/docker-update/internal/api"
	"github.com/centroid-is/docker-update/internal/compose"
	"github.com/centroid-is/docker-update/internal/docker"
	"github.com/centroid-is/docker-update/internal/poll"
	"github.com/centroid-is/docker-update/internal/registry"
	"github.com/centroid-is/docker-update/internal/selfupdate"
	"github.com/centroid-is/docker-update/internal/state"
)

// version / commit / builtAt are stamped at build time via
// -ldflags="-X main.version=... -X main.commit=... -X main.builtAt=...".
// See Dockerfile (stage 2, the go build invocation) + Makefile's image-prod
// target (which derives VERSION via `git describe`, SHA via `git rev-parse`,
// BUILT_AT via `date -u +%Y-%m-%dT%H:%M:%SZ`).
//
// Defaults are "dev" / "unknown" / "unknown" so a `go build` invoked
// directly (e.g. `make build`, or `go run ./cmd/docker-update` during local
// development) still produces a runnable binary that identifies itself
// as a dev build in the boot slog line.
//
// The three values are logged ONCE at boot via the existing "docker-update
// starting" slog.Info call (see main() below) so an operator tailing
// `journalctl` / `docker logs` can confirm which image+commit is running.
// This is the operator-side counterpart to the OCI image labels
// (org.opencontainers.image.version / .revision) the Dockerfile sets on
// the same VERSION / SHA build-args.
var (
	version = "dev"
	commit  = "unknown"
	builtAt = "unknown"
)

// registerMIMETypes seeds Go's process-global mime.TypeByExtension table
// with the four extensions Vite emits into internal/api/dist/assets (.js,
// .css, .svg, .json) plus .woff2 for any future webfont. Distroless
// (gcr.io/distroless/static-debian12:nonroot) intentionally ships NO
// /etc/mime.types — research/PITFALLS.md Pitfall 8 documents the failure
// mode this prevents: Chromium loads Vite's ES-module bundle via
// <script type="module" src=".../index-<hash>.js">, and per
// developer.chrome.com/blog/javascript-modules-mime, a wrong MIME type
// (or text/plain) is a HARD load failure with no remediation in the
// browser. Without an explicit registration here, Go's default
// mime.TypeByExtension(".js") falls back to text/plain on distroless and
// the UI never boots.
//
// The internal/api package's init() also registers the same extensions
// as a belt-and-braces (package-load defense — runs on import even
// before main() executes). Both call sites are idempotent;
// mime.AddExtensionType returns nil on duplicate calls and the second
// registration is a no-op. Keep BOTH:
//   - cmd/docker-update/main.go (this function) — boot-time attestation in
//     the operator-visible boot path; greppable from main.go.
//   - internal/api/static.go init() — package-scoped invariant; survives
//     any future refactor that replaces this binary's main with an
//     alternate entrypoint (e.g. a Phase 7 health-only binary).
//
// MUST be called before api.NewServer; tests that import api/ pick up
// the package-init registration anyway, so the test binary is not
// dependent on this function being called.
func registerMIMETypes() {
	// .js — application/javascript is the canonical IETF type for ES
	// modules (RFC 9239 §6); Chromium accepts both application/javascript
	// and text/javascript for <script type="module">, but
	// application/javascript is the unambiguous form. charset=utf-8 is
	// load-bearing for the e2e smoke spec's exact-match assertion.
	_ = mime.AddExtensionType(".js", "application/javascript; charset=utf-8")
	// .css — text/css; charset=utf-8 matches the browser default and
	// the e2e exact-match assertion.
	_ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
	// .svg — image/svg+xml; no charset (SVG is XML, browsers infer).
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
	// .json — application/json; charset=utf-8. Not currently emitted
	// by Vite into /assets but defensive for future inline JSON modules
	// or i18n catalogs.
	_ = mime.AddExtensionType(".json", "application/json; charset=utf-8")
	// .woff2 — webfont; included for forward-compat with a future
	// Phase 6+ custom font (Phase 5 uses the system font stack).
	_ = mime.AddExtensionType(".woff2", "font/woff2")
}

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
// Test contract: cmd/docker-update/main_test.go's TestSlogReplaceAttr_*
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
	// Phase 9 (d) — flag parsing happens FIRST so the same binary can
	// branch into helper-mode (--self-update-orchestrator) without paying
	// the cost of MIME-type registration, slog redactor install, state
	// store open, etc. Helper-mode runs a tiny lifecycle (wait → recreate
	// → poll-healthz → exit) and needs none of the server-mode
	// infrastructure.
	//
	// The flag set is intentionally minimal: --self-update-orchestrator
	// and --target are the two flags the helper-spawn contract pins
	// (internal/selfupdate/spawn.go's Spawn builds the helper's Cmd as
	// ["docker-update", "--self-update-orchestrator", "--target=<svc>"]).
	// Anything else lives in env vars — flag.Parse() is the boundary.
	selfUpdateOrchestrator := flag.Bool("self-update-orchestrator", false,
		"internal: run as helper container driving a docker-update self-recreate; not for operator use")
	targetFlag := flag.String("target", "",
		"internal: target service name for --self-update-orchestrator")
	flag.Parse()

	// Helper-mode branch — short-lived process that drives the recreate
	// of the parent docker-update via the daemon socket and exits.
	if *selfUpdateOrchestrator {
		runSelfUpdateOrchestrator(*targetFlag)
		return
	}

	// 0. mime.AddExtensionType for the embedded UI bundle extensions.
	// Distroless ships no /etc/mime.types so Go's default
	// mime.TypeByExtension(".js") returns text/plain — Chromium rejects
	// that for ES modules with a hard MIME error (research/PITFALLS.md
	// Pitfall 8). This MUST run before api.NewServer (step 6 below).
	// internal/api/static.go's init() also registers these as a defense
	// in depth; either alone is sufficient.
	registerMIMETypes()

	// 1. slog JSON handler; level via env per CONTEXT.md "Claude's Discretion".
	level := slog.LevelInfo
	if v := os.Getenv("DOCKER_UPDATE_LOG_LEVEL"); v != "" {
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
	statePath := os.Getenv("DOCKER_UPDATE_STATE_PATH")
	if statePath == "" {
		statePath = "./docker_update_state.json"
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
	// DOCKER_UPDATE_COMPOSE_PATH into the e2e compose stack so this
	// log.Fatalf does NOT fire under tests. An unset env var here is a
	// configuration error — the operator must point us at the
	// bind-mounted docker-compose.yml.
	composePath := os.Getenv("DOCKER_UPDATE_COMPOSE_PATH")
	composeReader, err := compose.NewReader(composePath)
	if err != nil {
		log.Fatalf("compose.NewReader: %v", err)
	}

	// Phase 9 (a) — the compose-runner construction step is DELETED.
	// The recreate primitive
	// now lives in internal/recreate and consumes the docker.Client facade
	// directly (no exec.LookPath, no subprocess). cmd/docker-update no
	// longer needs to depend on the host docker CLI being present at boot;
	// the only host-side dependency is /var/run/docker.sock (already
	// validated by the healthz path / discovery).

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
	cronExpr := os.Getenv("DOCKER_UPDATE_CRON")
	if cronExpr == "" {
		cronExpr = "0 * * * *"
	}

	// 5.6. poll.NewPoller — fails fast on invalid cron expression with
	// a paste-ready remediation message (Phase-3-specific pitfall —
	// RESEARCH.md "Cron string parsing mode mismatch"). The error wraps
	// the original parser error via %w so operators can grep for both
	// "DOCKER_UPDATE_CRON" and the underlying parse failure.
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
	// DOCKER_UPDATE_SELF_SERVICE (default "docker-update") is the compose service
	// name THIS process runs as; the action middleware compares the
	// {service} path-parameter against it and refuses self-update with 409
	// self_protection (ACT-09 — the manual self-upgrade procedure in
	// PROJECT.md is the documented escape hatch).
	//
	// DOCKER_UPDATE_VERIFY_WINDOW_S (default 15) is the verify-after-recreate
	// poll duration; DOCKER_UPDATE_HEALTHCHECK_WINDOW_S (default 60) is the
	// extended window when a container opts in via
	// hmi-update.wait-for-healthy=true label.
	selfService := os.Getenv("DOCKER_UPDATE_SELF_SERVICE")
	if selfService == "" {
		selfService = "docker-update"
	}
	verifyWindow := time.Duration(envInt("DOCKER_UPDATE_VERIFY_WINDOW_S", 15)) * time.Second
	healthcheckWindow := time.Duration(envInt("DOCKER_UPDATE_HEALTHCHECK_WINDOW_S", 60)) * time.Second

	// 5.9. actions.NewOrchestrator — THIRD producer of state mutations
	// (docker.Discoverer + poll.cronPoller are the first two). All three
	// feed the single `updates` channel; RunUpdater (step 4.10) is the
	// single consumer — DETECT-10 carry-forward.
	//
	// Phase 9 (a) signature: the runner parameter is GONE; the orchestrator
	// now invokes internal/recreate.Service via the docker.Client facade
	// directly. cmd/docker-update no longer wires a compose.Runner.
	orchestrator, err := actions.NewOrchestrator(
		dockerClient, resolver, composeReader, store, updates,
		selfService, verifyWindow, healthcheckWindow,
	)
	if err != nil {
		log.Fatalf("actions.NewOrchestrator: %v", err)
	}

	// 5.10. Phase 9 (d) — self-update Spawner (Plan 09-04).
	//
	// The Spawner builds + starts a one-shot helper container that
	// recreates THIS process via the daemon socket. The helper is the
	// SAME docker-update image (C1) launched with the
	// --self-update-orchestrator flag.
	//
	// Resolution of `selfImage` (the image reference the helper container
	// is launched from): in v1 we read DOCKER_UPDATE_SELF_IMAGE env. If
	// unset, fall back to the canonical `ghcr.io/centroid-is/docker-update:latest`
	// (operator-overridable). A future enhancement can resolve this from
	// the parent's own container inspect at boot, but the env-var path is
	// the operator-facing contract today and matches the
	// ghcr.io/centroid-is/docker-update:latest tag the README documents.
	selfImage := os.Getenv("DOCKER_UPDATE_SELF_IMAGE")
	if selfImage == "" {
		selfImage = "ghcr.io/centroid-is/docker-update:latest"
	}
	keepHelper := os.Getenv("DOCKER_UPDATE_SELF_UPDATE_KEEP_HELPER") == "true"
	spawner := selfupdate.NewSpawner(dockerClient, selfImage, selfService,
		orchestrator.ActionsInFlightFn(), keepHelper)

	// 6. api.NewServer with the Phase 4 five-arg signature (Plan 04-04 +
	// manual-poll kick). The fifth arg threads the existing cron poller
	// into POST /api/poll-now — the UI's "Watch now" button calls
	// poller.Sweep on the same updates channel the hourly tick feeds, so
	// DETECT-10's single-consumer invariant is preserved.
	//
	// Phase 9 (d) — Spawner + actionsInFlightFn are direct field injections
	// (rather than 6th/7th constructor args) so handlers_self_test.go's
	// 5-arg NewServer + post-inject pattern (Plan 09-02 RED) works without
	// a coordinated update across every existing NewServer call site.
	// Production wiring threads them in lockstep with the orchestrator.
	srv := api.NewServer(store, dockerClient, composeReader, orchestrator, poller)
	srv.WireSelfUpdate(spawner, orchestrator.ActionsInFlightFn())
	slog.Info("docker-update starting",
		// Version vars stamped at build time via Dockerfile -ldflags=-X.
		// "dev" / "unknown" / "unknown" when invoked from `go build` /
		// `make build`. Logged here so operators tailing `docker logs`
		// can identify the running image+commit (Phase 7 DEPLOY-01).
		"version", version,
		"commit", commit,
		"builtAt", builtAt,
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

// envDuration reads a time.Duration from the named env var, falling back
// to def if missing or unparseable. Accepts the standard time.ParseDuration
// format ("1s", "60s", "500ms", "2m" etc.). Plan 09-04 introduces this
// helper alongside envInt; the two share the same convention but parse
// distinct value types so promoting either to a shared package would not
// reduce duplication enough to justify a new dependency.
//
// Used by the --self-update-orchestrator branch + the parent-side Spawner
// wiring for DOCKER_UPDATE_SELF_UPDATE_DELAY (default 1s) and
// DOCKER_UPDATE_SELF_VERIFY_TIMEOUT (default 60s).
func envDuration(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

// runSelfUpdateOrchestrator is the helper-mode entry point invoked by
// the --self-update-orchestrator flag branch in main(). Builds a docker
// client (using the bind-mounted /var/run/docker.sock the helper inherits
// from the parent's Spawn call) and runs selfupdate.Orchestrate.
//
// Helper-mode is intentionally minimal: no slog redactor (logs are
// short-lived; the helper exits within ~3-90s), no state store, no HTTP
// server. Failure surfaces as os.Exit(1) so the helper's exit code is the
// operator's primary signal (visible in `docker ps -a`).
//
// healthzURL is built from the target service name + the docker DNS
// resolution on the compose project's default network — the same way a
// sibling container in the stack would address docker-update. Port 8080
// matches the production ListenAndServe.
func runSelfUpdateOrchestrator(target string) {
	// Minimal slog handler — no redactor, info level. Helper-mode logs
	// are short-lived and the operator's path to them is `docker logs
	// <helper-id>` (only when KEEP_HELPER=true; AutoRemove erases them
	// otherwise).
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if target == "" {
		slog.Error("self_update.orchestrator.no_target",
			"hint", "main.go must pass --target=<svc>; this is set by the parent's Spawn call")
		os.Exit(1)
	}

	ctx := context.Background()
	cli, err := docker.NewClient(ctx)
	if err != nil {
		slog.Error("self_update.orchestrator.docker_client", "err", err)
		os.Exit(1)
	}

	delay := envDuration("DOCKER_UPDATE_SELF_UPDATE_DELAY", 1*time.Second)
	verifyTimeout := envDuration("DOCKER_UPDATE_SELF_VERIFY_TIMEOUT", 60*time.Second)
	// The new parent answers at <target>:8080 over the docker network
	// (same compose project; docker DNS resolves the service name to
	// the new container's IP after recreate.Service returns).
	healthzURL := fmt.Sprintf("http://%s:8080/healthz", target)

	slog.Info("self_update.orchestrator.boot",
		"target", target,
		"delay", delay.String(),
		"verify_timeout", verifyTimeout.String(),
		"healthz_url", healthzURL,
	)

	if err := selfupdate.Orchestrate(ctx, cli, target, healthzURL, delay, verifyTimeout); err != nil {
		slog.Error("self_update.orchestrate.failed", "err", err)
		os.Exit(1)
	}
	// Success — exit 0; AutoRemove (default) cleans up the helper.
}
