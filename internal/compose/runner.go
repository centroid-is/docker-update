// Package compose (continued). runner.go owns the Phase 4 `os/exec` body
// that shells out to `docker compose -f <path> up -d --force-recreate
// <service>`. It is the third (and last) sibling of the package; reader.go
// (Phase 2) handles drift detection on the compose file, errors.go defines
// the two sentinel errors, and this file lands the production execRunner
// body that all three Phase 4 actions (Update / Rollback / Force-pull-with-
// recreate) call through.
//
// Security note (Pitfall 13 — argv discipline):
//
// The service name is passed to exec.CommandContext as a SEPARATE argv
// element (index 6 after "compose","-f","<path>","up","-d",
// "--force-recreate"). It is NEVER interpolated into a shell string. The
// upstream middleware (internal/actions/middleware.go, Plan 04-03)
// validates service names against the allowlist regex `^[a-zA-Z0-9._-]+$`;
// argv separation here is defense in depth so even a regression in the
// middleware cannot translate a hostile service name into shell
// injection. The runner_test.go TestUpdateService_ArgvDiscipline_NoShellInterpolation
// test pins the exact argv slice; the project-level acceptance grep on
// the verbatim literal `"compose", "-f", r.composePath, "up", "-d",
// "--force-recreate", service` rejects any future "simplification" that
// joins these into a shell command.
//
// cmd.Cancel rationale:
//
// The default os/exec Cancel func (process.Kill — SIGKILL on Unix) is too
// abrupt for compose; override to SIGTERM via cmd.Cancel + cmd.WaitDelay =
// 10 * time.Second so the in-flight `compose up -d --force-recreate` gets
// the same grace operators see in interactive use. docker compose's own
// stop_grace_period default is 10s, so this matches whatever container
// shutdown behavior the operator is already used to. After the 10s grace,
// the runtime SIGKILLs the child and cmd.Run() returns.
//
// Phase 4 plan 04-02 lands the body; the Phase 1 stub at this path was
// `type Runner interface{}` with no implementation.
//
// Production target: linux/amd64 (CLAUDE.md "Constraints — Platform").
// The syscall.SIGTERM signal used by cmd.Cancel is portable across
// Unix-like systems; the runner is not expected to run on Windows
// (consistent with reader.go's WR-06 portability note — Windows builds
// exist for developer experience only).
package compose

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"syscall"
	"time"
)

// Runner is the abstraction over `os/exec`-based docker compose invocations.
// Plan 04's internal/actions.Orchestrator uses this to execute Update /
// Rollback / Force-pull-with-recreate actions issued from the web UI.
//
// Two methods, no more:
//   - UpdateService: runs `docker compose -f <path> up -d --force-recreate
//     <service>` and returns nil on exit 0; on non-zero exit returns a
//     wrapped ErrComposeFailed with the captured stderr in the wrap chain.
//   - ComposePath: returns the compose path the runner was constructed with
//     (for diagnostic logging in the action handler error responses).
//
// Future force-pull-WITHOUT-recreate calls `docker.Client.ImagePull`
// directly; it does NOT go through this runner.
type Runner interface {
	UpdateService(ctx context.Context, service string) error
	ComposePath() string
}

// commandRunner is the test seam: production sets it to exec.CommandContext;
// tests inject a factory that returns an *exec.Cmd pointing at a fake binary
// (e.g. /bin/true for success, /bin/false for non-zero exit, /bin/sh -c
// '...' for stderr-write and ctx-cancel scripts).
//
// The seam is package-private (lowercase type name + lowercase field name on
// execRunner) so external consumers cannot tamper.
type commandRunner func(ctx context.Context, name string, args ...string) *exec.Cmd

// execRunner is the production Runner that shells out to `docker compose`.
//
// dockerBin is resolved once at construction via exec.LookPath so the
// runner fails fast at boot (NewRunner returns an error) if the docker CLI
// is missing — rather than at the first Update click hours later.
//
// composePath is captured verbatim from the constructor argument and
// forwarded as argv element 2 (the -f value). It is the path the operator
// supplied via HMI_UPDATE_COMPOSE_PATH; compose.Reader (sibling file) is
// the boot-snapshot guardian, but the path string itself lives here too
// so a single runner instance carries all state needed to invoke compose.
//
// cmdFactory is the test seam (see commandRunner type above). Production
// sets it to exec.CommandContext in NewRunner; tests overwrite it after
// NewRunner returns.
type execRunner struct {
	composePath string
	dockerBin   string
	cmdFactory  commandRunner
}

// NewRunner constructs a Runner using exec.LookPath("docker") to resolve
// the docker CLI binary path. WR-04: the return type is the Runner
// interface, not *execRunner, so callers in main.go and the actions
// package don't get coupled to the concrete struct.
//
// Failure modes:
//   - exec.LookPath returns exec.ErrNotFound: docker CLI is not in PATH.
//     The wrapped error preserves exec.ErrNotFound so callers can branch
//     with errors.Is. cmd/hmi-update/main.go log.Fatalfs on this so the
//     operator sees the cause at boot rather than discovering it on the
//     first Update click (T-04-02-05).
//   - exec.LookPath returns any other error: surfaced wrapped with the
//     "compose.NewRunner" prefix so boot logs are greppable.
//
// The compose v2 plugin (`docker compose ...`) is the actual command we
// invoke; we don't verify the plugin's presence here (would need a
// `docker compose version` subprocess at boot). The first UpdateService
// call surfaces a plugin-missing error in stderr instead.
func NewRunner(composePath string) (Runner, error) {
	bin, err := exec.LookPath("docker")
	if err != nil {
		return nil, fmt.Errorf("compose.NewRunner: docker CLI not found in PATH (need docker compose plugin v2.20+): %w", err)
	}
	return &execRunner{
		composePath: composePath,
		dockerBin:   bin,
		cmdFactory:  exec.CommandContext,
	}, nil
}

// ComposePath returns the compose file path the runner was constructed with.
// The action handler (Plan 04-04) uses this in error responses so operators
// can see exactly which compose file the failed invocation referenced.
func (r *execRunner) ComposePath() string { return r.composePath }

// UpdateService runs `docker compose -f <path> up -d --force-recreate <service>`.
//
// Wire shape (verbatim as handed to the kernel via exec):
//
//	argv = "docker", "compose", "-f", <path>, "up", "-d", "--force-recreate", <service>
//
// The first element resolves to r.dockerBin (e.g. /usr/bin/docker) via
// exec.LookPath at NewRunner time; the remaining seven elements are the
// args slice constructed below.
//
// Argv discipline: the service name is a separate argv element (index 7 in
// the verbatim shape above; index 6 of the args slice below) — never
// interpolated into a shell string. Pitfall 13 prevention. The upstream
// middleware (internal/actions/middleware.go, Plan 04-03) has already
// validated the service name against the allowlist regex; this is defense
// in depth.
//
// cmd.Cancel + cmd.WaitDelay:
//   - cmd.Cancel = SIGTERM (override the default SIGKILL from os/exec).
//   - cmd.WaitDelay = 10s (matches docker compose's stop_grace_period
//     default; after 10s the Go runtime sends SIGKILL).
//
// Stderr capture:
//   - Full stderr is read into a bytes.Buffer; on non-zero exit the bytes
//     flow into the returned error via fmt.Errorf("%w", ...) so the HTTP
//     handler (Plan 04-04) can surface a meaningful `reason` field.
//   - For slog, stderr is truncated to the last 4096 bytes with a
//     "...[truncated]..." marker prefix so the structured-log entry stays
//     bounded (compose output can run several KB on a failed recreate).
//
// slog event schema (Pattern G — dotted convention):
//   - On success: slog.Info("compose.run", service, exit_code=0, duration_ms)
//   - On failure: slog.Error("compose.run", service, exit_code, duration_ms,
//     err, stderr_snippet)
//
// Threat coverage:
//   - T-04-02-01 (argv injection): the argv slice is the literal below; the
//     service string is element 6.
//   - T-04-02-02 (SIGKILL too abrupt): cmd.Cancel = SIGTERM.
//   - T-04-02-03 (hung subprocess after ctx cancel): cmd.WaitDelay = 10s.
//   - T-04-02-04 (info disclosure in stderr): accepted; compose talks to
//     the local daemon, no registry auth in stderr.
func (r *execRunner) UpdateService(ctx context.Context, service string) error {
	start := time.Now()
	args := []string{"compose", "-f", r.composePath, "up", "-d", "--force-recreate", service}
	cmd := r.cmdFactory(ctx, r.dockerBin, args...)

	// WARNING-03 fix: place the child in its own process group via
	// Setpgid, and on ctx-cancel signal the WHOLE group (negative pid
	// targets the group). Without Setpgid, `docker compose` spawns
	// helper subprocesses (compose plugin children, per-service docker
	// calls) under its own pgid; cmd.Process.Signal only reaches the
	// direct child (docker), leaving the helpers running past the
	// cmd.WaitDelay grace. With the group-signal approach, every
	// descendant receives SIGTERM and the WaitDelay grace is effective.
	//
	// Setpgid is set BEFORE cmd.Start (which exec.CommandContext defers
	// to cmd.Run); the runtime sets it during the post-fork pre-exec
	// window so the child has its own pgid by the time Cancel can fire.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid -> "signal the process group whose pgid equals
		// the absolute value." Because we set Setpgid above, the child
		// IS the group leader, so its pid equals the pgid.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 10 * time.Second

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	elapsed := time.Since(start)

	// Truncate stderr to last 4096 bytes for slog (compose output can run
	// several KB on a failed recreate). The full content stays in the
	// returned error wrap chain — the HTTP handler decides how much to
	// surface to the operator.
	stderrSnippet := stderr.String()
	if len(stderrSnippet) > 4096 {
		stderrSnippet = "...[truncated]..." + stderrSnippet[len(stderrSnippet)-4096:]
	}

	if err != nil {
		slog.Error("compose.run",
			"service", service,
			"exit_code", exitCode,
			"duration_ms", elapsed.Milliseconds(),
			"err", err,
			"stderr_snippet", stderrSnippet,
		)
		// BLOCKER-03 fix: use the Go 1.20+ multi-%w form to wrap BOTH
		// ErrComposeFailed AND the underlying err. The previous single-
		// %w form dropped the cmd.Run error from the wrap chain, which
		// broke the contract documented on ErrComposeFailed (errors.As
		// against *exec.ExitError + errors.Is against context.Canceled
		// for the ctx-cancel path) and made it impossible for callers
		// to distinguish "compose exit non-zero" from "compose canceled
		// by SIGTERM".
		return fmt.Errorf("compose.UpdateService %s: exit %d: %w: %w: %s",
			service, exitCode, ErrComposeFailed, err, stderrSnippet)
	}

	slog.Info("compose.run",
		"service", service,
		"exit_code", exitCode,
		"duration_ms", elapsed.Milliseconds(),
	)
	return nil
}
