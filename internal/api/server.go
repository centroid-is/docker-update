package api

import (
	"net/http"
	"time"

	"github.com/centroid-is/hmi-update/internal/actions"
	"github.com/centroid-is/hmi-update/internal/compose"
	"github.com/centroid-is/hmi-update/internal/docker"
	"github.com/centroid-is/hmi-update/internal/state"
)

// Server wires the HTTP routes for hmi-update.
//
// The mux is built once at construction time via routes(). Server is safe
// for concurrent use by an arbitrary number of in-flight requests because
// state.Store is itself goroutine-safe, the moby SDK Client is safe per its
// own contract, and compose.Reader is read-only after NewReader.
//
// Phase 2 (DOCK-03 / OBS-02) extended the struct with dockerClient and
// composeReader so the upgraded /healthz handler can Ping the daemon and so
// the build-tag-gated /debug/compose-stat (debug_compose.go) can call
// CheckUnchanged. The compose.Reader is held here rather than re-stat'd in
// the debug handler because the Reader's boot-snapshot semantics depend on
// being constructed exactly once at process start (see compose.NewReader's
// doc comment).
//
// Phase 4 (Plan 04-04) extends the struct with orchestrator (an
// actions.Orchestrator) so the three new action endpoints — POST
// /api/containers/{service}/{update,rollback,force-pull} — can delegate
// to the actions package's Update/Rollback/ForcePull bodies. The
// middleware chain (ValidateServiceName → CheckSelfProtection →
// LookupContainer → CheckSafetyLabel) runs in handlers_actions.go via
// helpers on actions.Orchestrator + actions package-level functions.
type Server struct {
	store         *state.Store
	dockerClient  docker.Client
	composeReader *compose.Reader
	orchestrator  actions.Orchestrator
	mux           *http.ServeMux
}

// NewServer constructs a Server backed by the supplied state.Store,
// docker.Client, compose.Reader, and actions.Orchestrator, and registers
// all routes. Pass the Server's Handler() to http.Server or call
// ListenAndServe directly for the standard timeout-applied path.
//
// Phase 2 signature change: the constructor took three arguments (was
// just *state.Store in Phase 1). Phase 4 (Plan 04-04) adds a fourth —
// actions.Orchestrator — so the three new action endpoints can delegate
// to Update/Rollback/ForcePull. cmd/hmi-update/main.go threads the new
// dependency in the documented boot order (slog, state.NewStore,
// docker.NewClient, compose.NewReader, compose.NewRunner,
// registry.NewResolver, ..., actions.NewOrchestrator, NewServer);
// see .planning/phases/04-update-rollback-force-pull-actions-safety-state-persistence/04-CONTEXT.md
// "Integration Points".
//
// dockerClient is consumed by the /healthz handler (DOCK-03) and is
// also passed to actions.NewOrchestrator (Phase 4 action bodies use
// docker.Client.ImagePull/ImageTag/ContainerInspect). composeReader is
// consumed by the build-tag-gated /debug/compose-stat handler that
// plan 02-05's compose-drift.spec.ts hits, and (via the orchestrator)
// by Phase 4's mutating actions for pre-mutation stat-before-act
// checks. orchestrator is consumed by the three Phase 4 action
// handlers in handlers_actions.go.
//
// nil arguments are accepted defensively — the upgraded /healthz handler
// returns 503 with a documented remediation hint for each unwired branch,
// and the action handlers return 503 actionBodyOrchestratorUnwired when
// s.orchestrator is nil. In production main.go log.Fatalf's on each
// constructor error so we never reach NewServer with a nil dependency,
// but the defensive guards keep the surface forgiving for partial-init
// unit tests (TestHandleUpdate_OrchestratorUnwired_503 exercises the
// orchestrator==nil branch directly).
func NewServer(store *state.Store, dockerClient docker.Client, composeReader *compose.Reader, orchestrator actions.Orchestrator) *Server {
	s := &Server{
		store:         store,
		dockerClient:  dockerClient,
		composeReader: composeReader,
		orchestrator:  orchestrator,
		mux:           http.NewServeMux(),
	}
	s.routes()
	// registerDebugRoutes is a no-op in the default (production) build
	// (see debug_compose_noop.go) and registers GET /debug/compose-stat
	// only when the binary is built with `go build -tags=debug` (see
	// debug_compose.go). Holding the call site here keeps the route
	// table fully visible from one file.
	s.registerDebugRoutes()
	return s
}

// routes wires every HTTP endpoint Phase 2 + Phase 4 expose in
// production builds. The static handler is registered at "/" so it
// catches "/", "/index.html", and "/assets/*"; it returns 404 for any
// other unmatched path (no SPA fallback per Pitfall 8).
//
// The build-tag-gated /debug/compose-stat route is registered separately
// via registerDebugRoutes (debug_compose.go / debug_compose_noop.go) so
// production binaries never see it in the route table.
//
// Phase 4 (Plan 04-04) adds three method-scoped POST routes for the
// per-container action endpoints (ACT-01..05). The {service} path
// variable is consumed via r.PathValue("service") inside
// actions.ValidateServiceName (Go 1.22+ ServeMux).
func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.healthz)
	s.mux.HandleFunc("GET /api/state", s.getState)
	// Phase 4 action endpoints (ACT-01..05). Method-scoped routes per
	// Go 1.22+ ServeMux; {service} is the path variable consumed by
	// actions.ValidateServiceName inside the chain.
	s.mux.HandleFunc("POST /api/containers/{service}/update", s.handleUpdate)
	s.mux.HandleFunc("POST /api/containers/{service}/rollback", s.handleRollback)
	s.mux.HandleFunc("POST /api/containers/{service}/force-pull", s.handleForcePull)
	// Static handler matches /, /index.html, and /assets/* (it owns the
	// "/" catch-all). 404s on anything else inside the static handler.
	s.mux.Handle("/", newStaticHandler())
}

// Handler returns the underlying ServeMux for callers that want to wrap it
// in middleware or attach it to a custom http.Server.
func (s *Server) Handler() http.Handler { return s.mux }

// ListenAndServe binds the server to addr with timeouts calibrated for
// both slow-loris mitigation (ReadTimeout) AND the longest legitimate
// action duration (WriteTimeout).
//
// Timeout budget (BLOCKER-02 fix from Phase 4 review):
//
//	ReadTimeout = 10s — caps the request-line + headers + body read.
//	  Requests in this service are small (POST with no body, or a JSON
//	  body well under 1 KB); 10 s is a generous slow-loris cap.
//
//	WriteTimeout = 180s — covers the worst-case Update / Rollback /
//	  ForcePull-with-recreate pipeline. Verify-after-recreate runs for
//	  up to HMI_UPDATE_HEALTHCHECK_WINDOW_S seconds (default 60 s);
//	  docker pull + docker compose up -d --force-recreate can add
//	  another ~30 s on a slow network or a large image; we add ~90 s of
//	  margin so the response writes always complete inside the window.
//	  The earlier 10 s value (Phase 1) was calibrated against /healthz
//	  and /api/state only — the Phase 4 action endpoints make 10 s
//	  unworkable.
//
// Slow-loris is still mitigated: the threat targets RECEIVE-side
// dribbling of request headers, which ReadTimeout caps. WriteTimeout
// only governs response-body emission, which the action handler does
// in a single flush via json.NewEncoder.Encode — there is no
// per-byte adversary on the response side.
func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: s.mux,
		// Slow-loris cap on receive side (T-01-04-02).
		ReadTimeout: 10 * time.Second,
		// Worst-case action duration: 60 s healthcheck verify + 30 s
		// pull + 30 s recreate + buffer = ~120 s; we round up to 180 s
		// to absorb unexpected jitter on slow registries or compose
		// plugins. See BLOCKER-02 in 04-REVIEW.md.
		WriteTimeout: 180 * time.Second,
	}
	return srv.ListenAndServe()
}
