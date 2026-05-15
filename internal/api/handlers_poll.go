// Package api (continued). handlers_poll.go owns the manual-poll kick
// endpoint — POST /api/poll-now — the UI's "Watch now" button (see
// ui/src/lib/actions.ts pollNow()).
//
// Wire shape:
//   - 200 {"polled":true}                — sweep completed (cron sweep body
//                                          ran to completion, possibly with
//                                          per-container errors surfaced via
//                                          state.Notes — same path as the
//                                          hourly tick).
//   - 503 actionBodyPollerUnwired        — server constructed without a
//                                          ManualPoller; production main.go
//                                          log.Fatalf's on poll.NewPoller
//                                          errors so this is only reachable
//                                          via partial-init unit tests.
//   - 500 actionBodyPollFailed           — Sweep returned a non-nil error
//                                          (ctx.Err() — the request ctx was
//                                          cancelled or the 30s outer cap
//                                          fired). Details go to slog.
//
// DETECT-10 invariant preservation: Sweep dispatches StateUpdate messages
// on the same updates channel the hourly tick feeds. The single-consumer
// RunUpdater goroutine (started at boot in cmd/hmi-update/main.go step 4.10)
// remains the sole writer to state.Store. No parallel mutation path is
// introduced.
//
// Timeout budget: the handler caps the sweep at 30 s via context.WithTimeout
// derived from r.Context() so a client disconnect cancels in-flight
// resolver calls promptly AND the outer cap fires deterministically if the
// caller never disconnects. 30 s is generous against the per-call resolver
// timeout (HMI_UPDATE_REGISTRY_TIMEOUT_S default 10 s) and the bounded
// concurrency (HMI_UPDATE_POLL_CONCURRENCY default 4) — a sweep over 10
// containers at 10 s per call with 4-wide fan-out is at most 30 s in the
// pathological all-timeout case.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Handler-only response bodies for POST /api/poll-now. Verbatim constants,
// no fmt.Sprintf — same Pattern K discipline as handlers_actions.go
// (T-01-04-03 path-leak guard defined per-string).
const (
	// pollBodyOK is the 200 success envelope. The client (ui/src/lib/actions.ts
	// pollNow()) only checks r.ok; the JSON body is courtesy for ad-hoc
	// curl debugging.
	pollBodyOK = `{"polled":true}`

	// pollBodyUnwired is emitted ONLY by the defensive nil-guard at the
	// top of handlePollNow. Production main.go log.Fatalf's on
	// poll.NewPoller errors so this branch is only reachable via partial-
	// init unit tests.
	pollBodyUnwired = `{"error":"poller_not_wired","detail":"restart hmi-update; check boot logs for poll.NewPoller errors"}`

	// pollBodyFailed surfaces a Sweep() error (ctx.Err() — client disconnect
	// or 30s outer cap). The detailed err goes to slog via
	// poll.manual.sweep.err; the wire body just points at the logs (the
	// underlying error string may contain operator hostnames / paths from
	// the resolver layer, so T-01-04-03 mandates no echo).
	pollBodyFailed = `{"error":"poll_failed","detail":"see logs for poll.manual.sweep.err event"}`

	// pollSweepTimeout is the outer cap on the manual sweep. See file
	// godoc "Timeout budget" for the calibration rationale.
	pollSweepTimeout = 30 * time.Second
)

// handlePollNow implements POST /api/poll-now. Runs an immediate cron
// sweep against the same updates channel the hourly tick feeds (DETECT-10
// single-consumer invariant preserved — see file godoc).
//
// Errors surface via the verbatim-constant response bodies above; details
// go to slog so an operator tailing journalctl can trace a failed kick
// without the wire body echoing internal state.
func (s *Server) handlePollNow(w http.ResponseWriter, r *http.Request) {
	if s.poller == nil {
		writePollBody(w, http.StatusServiceUnavailable, pollBodyUnwired)
		return
	}
	// Derive a 30s-capped ctx from the request ctx so client disconnect
	// cancels in-flight resolver calls AND the outer cap fires
	// deterministically. See file godoc "Timeout budget".
	ctx, cancel := context.WithTimeout(r.Context(), pollSweepTimeout)
	defer cancel()
	if err := s.poller.Sweep(ctx); err != nil {
		slog.Warn("poll.manual.sweep.err", "err", err)
		writePollBody(w, http.StatusInternalServerError, pollBodyFailed)
		return
	}
	writePollBody(w, http.StatusOK, pollBodyOK)
}

// writePollBody is the verbatim-constant emitter for the manual-poll
// handler path. Mirrors writeActionBody in handlers_actions.go.
// Package-private — only handlePollNow funnels through here.
func writePollBody(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
