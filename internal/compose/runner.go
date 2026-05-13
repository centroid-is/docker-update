// Package compose shells out to the host `docker compose` CLI to perform
// `up -d --force-recreate <service>` and related operations.
//
// Phase 1 ships the interface only; the body lands in phase 4 (ACTION-01..05).
package compose

// Runner is the abstraction over `os/exec`-based docker compose invocations.
// Plan-04's internal/api server uses this to execute Update / Rollback /
// Force-pull actions issued from the web UI.
//
// TODO(phase-4): implement — exec.CommandContext + stderr capture into slog
// land here. See STACK.md §"Drive docker compose via CLI or Go library?".
type Runner interface{}
