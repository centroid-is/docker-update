// Package compose provides a read-only adapter over the docker-compose
// file pointed at by HMI_UPDATE_COMPOSE_PATH. The package's only job in
// Phase 2 is drift detection (Pitfall 10): catch the case where an
// atomic-save editor or operator-relocation replaces the file
// underneath our bind-mount before a Phase 4 update/rollback action
// runs `docker compose -f $HMI_UPDATE_COMPOSE_PATH ...`.
//
// Phase 2 does NOT parse YAML. Service identity comes from the
// com.docker.compose.service container label that the docker daemon
// attaches at compose-up time; the daemon is the source of truth.
//
// The Runner interface (see runner.go) is a stub for Phase 4 — the
// `os/exec`-based `docker compose` invoker — and is independent of the
// stat-based Reader landed in this phase.
package compose

import "errors"

// ErrComposeFileMoved is returned (wrapped) from Reader.CheckUnchanged
// when the compose file's inode (or, on filesystems without stable
// inodes, its (mtime, size) pair) has drifted from the boot snapshot
// captured by NewReader.
//
// Phase 4 maps this sentinel to HTTP 412 with body:
//
//	{"error":"compose_file_moved","hint":"restart hmi-update to pick up the new docker-compose.yml"}
//
// See .planning/research/PITFALLS.md Pitfall 10 for the canonical
// description of the failure mode this sentinel guards against. Callers
// test with errors.Is so the sentinel identity survives any number of
// fmt.Errorf("compose: %w", ...) wraps:
//
//	if err := reader.CheckUnchanged(ctx); err != nil {
//	    if errors.Is(err, compose.ErrComposeFileMoved) {
//	        // serve 412 with remediation hint
//	    }
//	    // other errors: surface as 500
//	}
//
// Note: a deleted compose file (stat ENOENT) is also surfaced as
// ErrComposeFileMoved — the operator remediation is identical (restart
// hmi-update after fixing the path). The underlying fs.ErrNotExist is
// preserved in the wrap chain for callers that want to distinguish.
var ErrComposeFileMoved = errors.New("compose: file moved or replaced since boot")

// ErrComposeFailed is returned (wrapped) from Runner.UpdateService when the
// host `docker compose -f <path> up -d --force-recreate <service>` subprocess
// exits non-zero. The wrap chain preserves the underlying *exec.ExitError so
// callers can read cmd.ProcessState.ExitCode() via errors.As if they need the
// exact code; for branching, errors.Is(err, ErrComposeFailed) is sufficient.
//
// Phase 4 maps this sentinel to HTTP 500 in the action handlers
// (internal/api/handlers_actions.go, Plan 04-04) with body shape:
//
//	{"error":"compose_failed","reason":"<stderr tail>","exit_code":<N>}
//
// The stderr tail is captured by the runner (truncated to 4096 bytes; full
// content stays in the wrap chain via fmt.Errorf("%w", ...)). The slog event
// `compose.run` carries the same data unredacted (compose talks to the local
// daemon — no registry auth in stderr).
var ErrComposeFailed = errors.New("compose: runner returned non-zero exit")
