package api

import (
	"net/http"
	"time"

	"github.com/centroid-is/hmi-update/internal/state"
)

// Server wires the HTTP routes for hmi-update.
//
// The mux is built once at construction time via routes(). Server is safe
// for concurrent use by an arbitrary number of in-flight requests because
// state.Store is itself goroutine-safe.
type Server struct {
	store *state.Store
	mux   *http.ServeMux
}

// NewServer constructs a Server backed by the supplied state.Store and
// registers all routes. Pass the Server's Handler() to http.Server or call
// ListenAndServe directly for the standard timeout-applied path.
func NewServer(store *state.Store) *Server {
	s := &Server{store: store, mux: http.NewServeMux()}
	s.routes()
	return s
}

// routes wires every HTTP endpoint Phase 1 exposes. The static handler is
// registered at "/" so it catches "/", "/index.html", and "/assets/*"; it
// returns 404 for any other unmatched path (no SPA fallback per Pitfall 8).
func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.healthz)
	s.mux.HandleFunc("GET /api/state", s.getState)
	// Static handler matches /, /index.html, and /assets/* (it owns the
	// "/" catch-all). 404s on anything else inside the static handler.
	s.mux.Handle("/", newStaticHandler())
}

// Handler returns the underlying ServeMux for callers that want to wrap it
// in middleware or attach it to a custom http.Server.
func (s *Server) Handler() http.Handler { return s.mux }

// ListenAndServe binds the server to addr with the Phase 1 security
// timeouts applied. Read and write timeouts are both 10 seconds to
// mitigate slow-loris (threat T-01-04-02 in the plan's STRIDE register).
func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.mux,
		ReadTimeout:  10 * time.Second, // Slow-loris mitigation per Phase 1 security domain
		WriteTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}
