// Package compose provides a read-only adapter over the docker-compose
// file pointed at by DOCKER_UPDATE_COMPOSE_PATH. The package's job is
// drift detection (Pitfall 10): catch the case where an atomic-save
// editor or operator-relocation replaces the file underneath our
// bind-mount before an Update / Rollback action runs.
//
// Pre-Phase-9 the package ALSO shipped a Runner that shelled out to
// `docker compose -f <path> up -d --force-recreate <service>`. Plan
// 09-03 deleted Runner in favor of the socket-only internal/recreate
// primitive (see internal/recreate/recreate.go); the compose package's
// surviving surface is the Reader (drift detection) plus two sentinels
// in errors.go (ErrComposeFileMoved for the live drift path,
// ErrComposeFailed retained only for public-API backward-compat).
//
// The package does NOT parse YAML. Service identity comes from the
// com.docker.compose.service container label that the docker daemon
// attaches at compose-up time; the daemon is the source of truth.
package compose

import "errors"

// ErrComposeFileMoved is returned (wrapped) from Reader.CheckUnchanged
// when the compose file's inode (or, on filesystems without stable
// inodes, its (mtime, size) pair) has drifted from the boot snapshot
// captured by NewReader.
//
// Phase 4 maps this sentinel to HTTP 412 with body:
//
//	{"error":"compose_file_moved","hint":"restart docker-update to pick up the new docker-compose.yml"}
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
// docker-update after fixing the path). The underlying fs.ErrNotExist is
// preserved in the wrap chain for callers that want to distinguish.
var ErrComposeFileMoved = errors.New("compose: file moved or replaced since boot")

// ErrComposeFailed: pre-Phase-9 this sentinel was emitted from
// Runner.UpdateService on non-zero `docker compose` subprocess exit.
// Plan 09-03 deleted compose.Runner in favor of the socket-only
// internal/recreate primitive; this sentinel is RETAINED only as a
// public-API breakage guard — no internal code emits it anymore. The
// action-layer sentinel (actions.ErrComposeFailed) is the live 500-path
// dispatch key; see internal/actions/errors.go.
//
// If you find yourself wanting to errors.Is against this value from
// new code, you almost certainly want actions.ErrComposeFailed instead.
//
// Deprecated: kept for backward-compat; the live recreate path uses
// internal/recreate.Service which wraps with actions.ErrComposeFailed.
var ErrComposeFailed = errors.New("compose: runner returned non-zero exit")
