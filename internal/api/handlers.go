package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/centroid-is/docker-update/internal/state"
)

// dockerSocketPath returns the path that healthz stats to determine docker
// socket reachability. Overridable via HMI_UPDATE_DOCKER_HOST for tests
// (per .planning/phases/02-docker-client-compose-file-reader/02-CONTEXT.md
// "Healthz Remediation Hints" step 1). In production the value defaults to
// the canonical docker socket bind-mount target so a default-install HMI
// works with zero env config.
func dockerSocketPath() string {
	if v := os.Getenv("HMI_UPDATE_DOCKER_HOST"); v != "" {
		return v
	}
	return "/var/run/docker.sock"
}

// healthz response bodies (DOCK-03 / OBS-02).
//
// These five strings are VERBATIM from
// .planning/phases/02-docker-client-compose-file-reader/02-CONTEXT.md
// "Healthz Remediation Hints". They are documented in the threat register
// (T-02-04-01) as the sole permitted /healthz response payloads — any new
// string here is a security review item because the path-leak guard
// (T-01-04-03) is defined per-string. Do NOT interpolate variables into
// these constants; if a future branch needs a dynamic field, build a
// dedicated typed body and add it to the threat model first.
//
// The EACCES hint deliberately references `id -g docker` — that is the
// Pitfall 9 remediation (set compose `user: '65532:$(id -g docker)'`).
// Operators copy-paste this string verbatim.
const (
	healthzBodyOK            = `{"status":"ok"}`
	healthzBodySocketEACCES  = `{"status":"unhealthy","reason":"docker socket permission denied — set compose user: '65532:$(id -g docker)' (Pitfall 9)"}`
	healthzBodySocketMissing = `{"status":"unhealthy","reason":"docker socket missing — add bind-mount '/var/run/docker.sock:/var/run/docker.sock'"}`
	healthzBodyDaemonUnreach = `{"status":"unhealthy","reason":"docker daemon unreachable"}`
	healthzBodyStateUnwired  = `{"status":"unhealthy","reason":"state store unavailable; check HMI_UPDATE_STATE_PATH and restart"}`
	// healthzBodyClientUnwired is emitted ONLY by the defensive nil-guard
	// in Server.healthz when s.dockerClient is nil. Production main.go
	// log.Fatalf's on docker.NewClient errors so this branch is only
	// reachable via test wiring; the dedicated body (WR-02 review fix:
	// formerly reused healthzBodySocketMissing, which lied — the socket
	// might be fine while the client is simply unwired in test scaffolding).
	healthzBodyClientUnwired = `{"status":"unhealthy","reason":"docker client not wired — restart hmi-update; if this persists, check boot logs for docker.NewClient errors"}`
)

// looksLikeSocketEACCES is the narrow substring backstop for EACCES
// detection on docker Ping errors (WR-05). It deliberately matches the
// two known docker / kernel phrasings — "connect: permission denied"
// (Go net package format for unix socket EACCES) and "operation not
// permitted" (Linux EPERM phrasing the daemon occasionally surfaces
// for SELinux denials) — and nothing else. Generic "permission denied"
// strings from unrelated SDK error paths (e.g. registry 403 responses)
// must not match.
//
// Drop this helper if the moby SDK gains a typed Forbidden errdef the
// client.IsErrXxx surface exposes — at which point the typed
// errors.Is path is sufficient on its own.
func looksLikeSocketEACCES(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "connect: permission denied") ||
		strings.Contains(s, "operation not permitted")
}

// healthz returns 200 with body healthzBodyOK only when ALL of:
//   - the state store is reachable (non-nil + Get does not panic)
//   - the docker socket at dockerSocketPath() is stattable
//   - the docker daemon responds to Ping within 500ms
//
// Any failure surfaces as a 503 with one of the four documented remediation
// hints above. Detection flow (CONTEXT.md "Healthz Remediation Hints"):
//
//  1. State store wired?               -> nil store: 503 healthzBodyStateUnwired
//  2. Docker client wired? (defensive) -> nil client: 503 healthzBodySocketMissing
//  3. os.Stat(dockerSocketPath()):
//     fs.ErrNotExist    -> 503 healthzBodySocketMissing
//     fs.ErrPermission  -> 503 healthzBodySocketEACCES
//     other             -> 503 healthzBodyDaemonUnreach (slog the err)
//  4. dockerClient.Ping(ctx500ms):
//     syscall.EACCES OR err.Error() contains "permission denied"
//     -> 503 healthzBodySocketEACCES
//     other / timeout   -> 503 healthzBodyDaemonUnreach
//  5. All green -> 200 healthzBodyOK
//
// Security invariant (T-01-04-03): the response body MUST NOT echo any
// absolute filesystem path or env-var literal value. The verbatim hint
// strings reference '/var/run/docker.sock' which is operator advice
// (Pitfall 9 remediation), not process state. The path-leak guard in
// every healthz test case enforces this invariant for the test-host
// TempDir prefixes ('/private/', '/var/folders/', '/tmp/').
//
// Performance: the Ping context has a 500ms hard ceiling (CONTEXT.md
// "Claude's Discretion" prefers 500ms over 1s — fails fast under wedge).
// The Phase 1 10s http.Server ReadTimeout / WriteTimeout caps any
// per-connection cost.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	// Step 1: state store wired?
	if s.store == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(healthzBodyStateUnwired))
		return
	}
	// Touch the store to prove it is still readable (Phase 1 contract).
	// state.Store.Get takes an RLock and returns a snapshot; the discard
	// keeps the call observable in -race traces.
	_ = s.store.Get()

	// Step 2: docker client wired? Defensive nil-guard — production
	// main.go log.Fatalf's on docker.NewClient errors so we should never
	// reach this branch in a real boot. The W2 nil-docker-client test
	// case in handlers_healthz_test.go exercises this branch directly so
	// the guard is not dead code.
	//
	// WR-03 fix: emit healthzBodyClientUnwired (not the socket-missing
	// hint). A nil dockerClient says nothing about whether the bind-mount
	// exists; conflating the two surfaces a misleading remediation
	// ("add bind-mount") when the real cause is "wiring/boot fault".
	if s.dockerClient == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(healthzBodyClientUnwired))
		return
	}

	// Step 3: stat the docker socket.
	if _, err := os.Stat(dockerSocketPath()); err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(healthzBodySocketMissing))
			return
		case errors.Is(err, fs.ErrPermission):
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(healthzBodySocketEACCES))
			return
		default:
			// Other stat errors are surfaced as "daemon unreachable" —
			// we still do NOT echo the path or the underlying syscall
			// error string back to the client; only into slog.
			slog.Warn("healthz.socket.stat.fail", "err", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(healthzBodyDaemonUnreach))
			return
		}
	}

	// Step 4: Ping with a 500ms timeout.
	pingCtx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
	defer cancel()
	if err := s.dockerClient.Ping(pingCtx); err != nil {
		// EACCES detection ladder. The TYPED path is the primary
		// signal; the substring fallback is a narrowly-scoped
		// belt-and-braces backstop for SDK error shapes that fail to
		// unwrap cleanly to syscall.EACCES (WR-05).
		//
		// Pinned SDK: github.com/moby/moby/client v0.4.1 (see go.mod
		// + internal/docker/_sdk_shape.txt). On this SDK version
		// errors.Is(err, syscall.EACCES) handles the dominant case —
		// the Go net package's *os.SyscallError implements Unwrap
		// down to syscall.EACCES. The substring backstop covers two
		// known fringes:
		//   1. The moby/moby/client transport wraps some failures
		//      in *url.Error or *errors.errorString without
		//      preserving the syscall.Errno chain (rare on Linux
		//      kernels, observed historically on macOS unix-socket
		//      paths).
		//   2. A daemon-side response surfaces "permission denied"
		//      text without a syscall errno — typically a SELinux
		//      AVC denial that arrives as a non-typed HTTP body.
		//
		// Tightened from a bare "permission denied" match to
		// "connect: permission denied" / "operation not permitted"
		// — both are the exact docker / kernel phrasings, and
		// neither appears in unrelated SDK errors (e.g. registry
		// auth flows). Drop the substring backstop when the SDK
		// publishes a typed Forbidden / Unauthorized errdef the
		// client exposes via IsErrXxx (currently only
		// IsErrConnectionFailed is exposed per _sdk_shape.txt).
		if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) || looksLikeSocketEACCES(err) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(healthzBodySocketEACCES))
			return
		}
		slog.Warn("healthz.daemon.ping.fail", "err", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(healthzBodyDaemonUnreach))
		return
	}

	// Step 5: all green.
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(healthzBodyOK))
}

// getState returns the in-memory state snapshot as JSON.
//
// No I/O on this path - per OBS-03 (Phase 4) this must be cheap enough for
// 5s UI polling. The state.State and api.State types are json-tag identical
// (verified by tygo's source-of-truth contract in internal/api/types.go);
// we marshal state.State directly to avoid a redundant copy.
func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
	st := s.store.Get()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(st); err != nil {
		// Headers are already written; we cannot recover. Log and bail.
		slog.Error("getState: encode failed", "err", err)
		return
	}
}

// _ keeps the state import used even if a future refactor temporarily
// stops referencing the type by name. Go's compiler is strict about
// unused imports and this is a load-bearing dependency.
var _ = (*state.Store)(nil)
