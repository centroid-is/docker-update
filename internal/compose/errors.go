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
