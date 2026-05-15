//go:build debug

package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/centroid-is/docker-update/internal/compose"
)

// registerDebugRoutes wires the debug-only endpoints. Built only when
// `go build -tags=debug` is specified; the default (production) build
// uses the no-op stub in debug_compose_noop.go.
//
// Plan 02-05's e2e/tests/compose-drift.spec.ts hits GET /debug/compose-stat
// to assert compose.Reader.CheckUnchanged behaviour without yet having a
// Phase 4 mutating action endpoint (POST /api/containers/:svc/update) to
// exercise the reader naturally.
//
// Phase 4 removes this endpoint once the action endpoints land — search
// for "debug_compose" when doing that work.
//
// Production-build safety (T-02-04-02): the build tag `//go:build debug`
// excludes this file from `go build ./...`. The default Dockerfile in
// Phase 7 will NOT pass `-tags=debug`, so production images cannot serve
// /debug/compose-stat — a probe will fall through to the static handler
// 404 catch-all. The plan's verify gate proves both build variants.
func (s *Server) registerDebugRoutes() {
	s.mux.HandleFunc("GET /debug/compose-stat", s.debugComposeStat)
	slog.Info("debug.route.registered", "route", "/debug/compose-stat")
}

// debugComposeStat returns 200 + {"status":"ok"} when CheckUnchanged sees
// no drift; 412 + {"error":"compose_file_moved","hint":"..."} when the
// compose file has been atomic-saved / replaced / deleted since boot
// (compose.ErrComposeFileMoved). Other unexpected errors surface as 500
// with a generic body — we do NOT echo the underlying stat error string
// or the compose path back to the client (T-02-04-01 path-leak guard
// extends here for symmetry with /healthz).
//
// The handler is the seam plan 02-05's Playwright spec uses to assert
// Pitfall 10 detection. It will be removed in Phase 4 once the mutating
// action handlers exercise CheckUnchanged naturally.
func (s *Server) debugComposeStat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if s.composeReader == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"compose_reader_unwired"}`))
		return
	}
	err := s.composeReader.CheckUnchanged(r.Context())
	if err == nil {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}
	if errors.Is(err, compose.ErrComposeFileMoved) {
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"error":"compose_file_moved","hint":"restart hmi-update to pick up the new docker-compose.yml"}`))
		return
	}
	// Other errors: 500 with no path leak. slog captures the detail.
	slog.Error("debug.compose_stat.fail", "err", err)
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(`{"error":"compose_stat_failed"}`))
}
