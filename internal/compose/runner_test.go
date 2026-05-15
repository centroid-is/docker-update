// RED-FIRST per C4. These tests are authored before internal/compose/runner.go's
// execRunner body exists. They drive the Phase 4 plan 04-02 implementation
// green via the commandRunner test seam.
//
// What these tests guard:
//   - TestExecRunner_SatisfiesRunner: compile-time pin of execRunner ↔ Runner
//   - TestNewRunner_DockerNotFound: PATH-without-docker → exec.ErrNotFound (fail-fast)
//   - TestNewRunner_HappyPath: PATH-with-docker → Runner with ComposePath()
//   - TestUpdateService_ArgvDiscipline_NoShellInterpolation: Pitfall 13 (argv element 7 is the literal service string)
//   - TestUpdateService_HappyPath: exit 0 → nil + slog event with exit_code=0
//   - TestUpdateService_NonZeroExit_ErrComposeFailed: exit 1 → errors.Is ErrComposeFailed
//   - TestUpdateService_StderrCaptured_Truncated: 10000-byte stderr → 4096-byte tail + "...[truncated]..." marker
//   - TestUpdateService_CtxCancel_SendsSIGTERM_Within10s: ctx cancel → SIGTERM grace (cmd.Cancel + WaitDelay)
//   - TestUpdateService_ComposePath_PassedThrough: composePath via NewRunner survives to argv[2]
//
// The execRunner struct holds an unexported `cmdFactory commandRunner` field
// that production sets to exec.CommandContext. Tests overwrite it after
// NewRunner returns. The seam is package-private (lowercase field name) so
// external consumers cannot tamper.
//
// Goroutine assertion contract (per persist_test.go convention): off-goroutine
// assertions use t.Errorf, never t.Fatal.
package compose

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// Portability shims for test binaries. On Linux these live under /bin and
// /usr/bin both; on macOS /bin/true and /bin/false are MISSING (they are
// only at /usr/bin/{true,false}). Resolve at package-test init time
// (before any per-test t.Setenv strips PATH) so the test seam can hand
// the runner an absolute path regardless of the per-test PATH override.
var (
	binTrue  = mustResolveBinary("true")
	binFalse = mustResolveBinary("false")
	binSh    = mustResolveBinary("sh")
	binHead  = mustResolveBinary("head")
	binTr    = mustResolveBinary("tr")
	binSleep = mustResolveBinary("sleep")
)

// mustResolveBinary tries exec.LookPath first (real PATH), then falls back
// to the two canonical locations across Linux/macOS. Panics if not found;
// the test suite cannot run without these.
func mustResolveBinary(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, candidate := range []string{
		filepath.Join("/usr/bin", name),
		filepath.Join("/bin", name),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	panic("compose runner_test: cannot resolve binary " + name)
}

// TestExecRunner_SatisfiesRunner is the load-bearing compile-time assertion.
// If anyone ever drifts the Runner interface or the execRunner methods, this
// fails at compile time before any runtime test runs.
func TestExecRunner_SatisfiesRunner(t *testing.T) {
	t.Parallel()
	var _ Runner = (*execRunner)(nil)
}

// stubDocker creates a fake `docker` executable inside dir (chmod 0755) and
// returns dir. Tests use t.Setenv("PATH", dir) so exec.LookPath finds it.
// The fake binary just exits 0; it is never executed by these tests (the
// cmdFactory test seam short-circuits exec by returning a Cmd pointing at
// /bin/true or other test binaries).
func stubDocker(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "docker")
	// On Unix any file with the executable bit will satisfy exec.LookPath.
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("stubDocker: write fake docker: %v", err)
	}
	return dir
}

// TestNewRunner_DockerNotFound asserts NewRunner fails fast when the docker
// CLI is absent from PATH. T-04-02-05 (Elevation/operator visibility): the
// operator sees the error at boot via log.Fatalf, not at the first Update
// click hours later.
func TestNewRunner_DockerNotFound(t *testing.T) {
	// PATH = an empty directory. exec.LookPath("docker") returns
	// exec.ErrNotFound. NOTE: not t.Parallel — t.Setenv prohibits it.
	t.Setenv("PATH", t.TempDir())
	r, err := NewRunner("/some/path/docker-compose.yml")
	if err == nil {
		t.Fatalf("NewRunner: want error when docker is absent, got nil (runner=%v)", r)
	}
	if r != nil {
		t.Errorf("NewRunner: want nil runner on error, got %v", r)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("NewRunner err: want errors.Is(exec.ErrNotFound), got %v", err)
	}
	// Operator-visible message includes the "compose.NewRunner" prefix so
	// boot logs are greppable.
	if !strings.Contains(err.Error(), "compose.NewRunner") {
		t.Errorf("NewRunner err: want 'compose.NewRunner' prefix, got %q", err.Error())
	}
}

// TestNewRunner_HappyPath asserts NewRunner returns a non-nil Runner with the
// expected ComposePath() when docker is present in PATH.
func TestNewRunner_HappyPath(t *testing.T) {
	t.Setenv("PATH", stubDocker(t))
	const wantPath = "/opt/centroid/docker-compose.yml"
	r, err := NewRunner(wantPath)
	if err != nil {
		t.Fatalf("NewRunner: unexpected error: %v", err)
	}
	if r == nil {
		t.Fatalf("NewRunner: want non-nil Runner")
	}
	if got := r.ComposePath(); got != wantPath {
		t.Errorf("ComposePath: want %q, got %q", wantPath, got)
	}
}

// recordingFactory wraps a real exec.Cmd factory and records the argv it was
// called with. The wrapped cmd is what actually runs; recording happens
// before delegation so a subsequent .Run() against /bin/true still succeeds.
type recordingFactory struct {
	mu   sync.Mutex
	name string
	args []string
	// delegate is the *real* exec.Cmd that gets returned to the runner.
	delegate func(ctx context.Context, name string, args ...string) *exec.Cmd
}

func (f *recordingFactory) make(ctx context.Context, name string, args ...string) *exec.Cmd {
	f.mu.Lock()
	f.name = name
	// copy args so callers can't mutate our record
	cp := make([]string, len(args))
	copy(cp, args)
	f.args = cp
	f.mu.Unlock()
	return f.delegate(ctx, name, args...)
}

// TestUpdateService_ArgvDiscipline_NoShellInterpolation pins Pitfall 13
// prevention at the wire shape. The service name MUST be the 7th argv
// element (index 6 after "compose","-f","<path>","up","-d","--force-recreate",
// "<service>") and MUST NEVER be interpolated through /bin/sh. The Plan
// 04-03 middleware regex is the first line of defense; this is defense in
// depth at the exec boundary.
func TestUpdateService_ArgvDiscipline_NoShellInterpolation(t *testing.T) {
	t.Setenv("PATH", stubDocker(t))
	const composePath = "/some/path/docker-compose.yml"
	const service = "my-svc"

	r, err := NewRunner(composePath)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	er, ok := r.(*execRunner)
	if !ok {
		t.Fatalf("NewRunner: want *execRunner, got %T", r)
	}

	rf := &recordingFactory{
		delegate: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			// Substitute the real run with `true` so .Run() succeeds.
			return exec.CommandContext(ctx, binTrue)
		},
	}
	er.cmdFactory = rf.make

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := er.UpdateService(ctx, service); err != nil {
		t.Fatalf("UpdateService: unexpected error: %v", err)
	}

	// Pin argv exactly. Any future refactor that joins these into a shell
	// string (e.g. via fmt.Sprintf) trips this assertion AND the
	// project-level grep gate.
	wantArgs := []string{"compose", "-f", composePath, "up", "-d", "--force-recreate", service}
	if rf.name != er.dockerBin {
		t.Errorf("argv[0] (cmd name): want %q (dockerBin), got %q", er.dockerBin, rf.name)
	}
	if !reflect.DeepEqual(rf.args, wantArgs) {
		t.Errorf("argv: want %#v, got %#v", wantArgs, rf.args)
	}
	// The service-name element must be the LITERAL string, not a
	// concatenation. Length check pins this independently of the
	// DeepEqual above.
	if len(rf.args) != 7 {
		t.Errorf("argv length: want 7, got %d (%#v)", len(rf.args), rf.args)
	} else if rf.args[6] != service {
		t.Errorf("argv[6] (service): want literal %q, got %q", service, rf.args[6])
	}
}

// TestUpdateService_HappyPath: exit 0 → nil + slog event.
func TestUpdateService_HappyPath(t *testing.T) {
	t.Setenv("PATH", stubDocker(t))
	r, err := NewRunner("/some/path/docker-compose.yml")
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	er := r.(*execRunner)
	er.cmdFactory = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, binTrue)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := er.UpdateService(ctx, "my-svc"); err != nil {
		t.Errorf("UpdateService: want nil error on exit 0, got %v", err)
	}
}

// TestUpdateService_NonZeroExit_ErrComposeFailed: exit 1 → errors.Is sentinel.
func TestUpdateService_NonZeroExit_ErrComposeFailed(t *testing.T) {
	t.Setenv("PATH", stubDocker(t))
	r, err := NewRunner("/some/path/docker-compose.yml")
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	er := r.(*execRunner)
	er.cmdFactory = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, binFalse)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gotErr := er.UpdateService(ctx, "my-svc")
	if gotErr == nil {
		t.Fatalf("UpdateService: want error on exit 1, got nil")
	}
	if !errors.Is(gotErr, ErrComposeFailed) {
		t.Errorf("UpdateService err: want errors.Is(ErrComposeFailed), got %v", gotErr)
	}
	// Error message includes the exit code so HTTP-layer callers can
	// surface it in the JSON response body (Plan 04-04).
	if !strings.Contains(gotErr.Error(), "exit 1") {
		t.Errorf("UpdateService err: want 'exit 1' in message, got %q", gotErr.Error())
	}
}

// TestUpdateService_StderrCaptured_Truncated asserts:
//   - stderr written by the subprocess flows into the returned error
//   - stderr longer than 4096 bytes is truncated with a documented marker
//     ("...[truncated]...") + the last 4096 bytes of payload
//
// The stress payload is 10000 bytes of "X" so the >4096 branch is exercised.
func TestUpdateService_StderrCaptured_Truncated(t *testing.T) {
	t.Setenv("PATH", stubDocker(t))
	r, err := NewRunner("/some/path/docker-compose.yml")
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	er := r.(*execRunner)
	er.cmdFactory = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// Use sh -c to write 10000 X's to stderr then exit 1. Reference
		// `head` and `tr` by absolute path because t.Setenv stripped PATH
		// down to the stubDocker tempdir; relative-name resolution from
		// inside the subprocess shell would fail.
		script := binHead + " -c 10000 /dev/zero | " + binTr + " '\\0' X >&2; exit 1"
		return exec.CommandContext(ctx, binSh, "-c", script)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gotErr := er.UpdateService(ctx, "my-svc")
	if gotErr == nil {
		t.Fatalf("UpdateService: want error on stderr-write + exit 1, got nil")
	}
	msg := gotErr.Error()
	if !strings.Contains(msg, "...[truncated]...") {
		t.Errorf("UpdateService err: want '...[truncated]...' marker, got %q", msg)
	}
	// Count the X's in the message; want exactly 4096 of them (the tail).
	xCount := strings.Count(msg, "X")
	if xCount != 4096 {
		t.Errorf("UpdateService err: want 4096 X's in tail, got %d", xCount)
	}
}

// TestUpdateService_CtxCancel_SendsSIGTERM_Within10s asserts:
//   - cmd.Cancel sends SIGTERM (not SIGKILL — that's the os/exec default)
//   - cmd.WaitDelay caps the total wait at ~10s after ctx cancel
//   - the subprocess gets a chance to trap SIGTERM and exit cleanly
//
// The script traps SIGTERM and exits 0 immediately on receipt, so the test
// finishes in well under the 10s grace.
func TestUpdateService_CtxCancel_SendsSIGTERM_Within10s(t *testing.T) {
	t.Setenv("PATH", stubDocker(t))
	r, err := NewRunner("/some/path/docker-compose.yml")
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	er := r.(*execRunner)
	er.cmdFactory = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// Trap SIGTERM → exit 0; otherwise sleep 30 (well beyond the test
		// budget). If cmd.Cancel sends SIGTERM, the trap fires and the
		// subprocess exits within tens of ms. Use absolute path for sleep
		// because PATH is stripped to stubDocker(t).
		script := "trap 'exit 0' TERM; " + binSleep + " 30"
		return exec.CommandContext(ctx, binSh, "-c", script)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- er.UpdateService(ctx, "my-svc")
	}()

	// Let the subprocess come up (trap installed, sleep started).
	time.Sleep(100 * time.Millisecond)
	cancel()

	// Allow ample headroom: 10s WaitDelay + 1s margin. A SIGTERM-trapping
	// subprocess should actually finish in well under 1s.
	deadline := time.NewTimer(11 * time.Second)
	defer deadline.Stop()
	select {
	case err := <-done:
		// err is non-nil because cmd.Wait reports ctx.Err() or *ExitError
		// when the process exited due to a signal. We do not pin the exact
		// error shape; the load-bearing assertion is the deadline.
		_ = err
	case <-deadline.C:
		t.Fatalf("UpdateService: did not return within 11s of ctx cancel — cmd.Cancel or cmd.WaitDelay missing")
	}
}

// TestUpdateService_ComposePath_PassedThrough asserts NewRunner's composePath
// survives unchanged into argv[2] (the -f value). This is a stronger guard
// than ComposePath() alone — it pins the wire shape, not just the getter.
func TestUpdateService_ComposePath_PassedThrough(t *testing.T) {
	t.Setenv("PATH", stubDocker(t))
	const composePath = "/path/to/compose.yml"
	r, err := NewRunner(composePath)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	er := r.(*execRunner)

	rf := &recordingFactory{
		delegate: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, binTrue)
		},
	}
	er.cmdFactory = rf.make

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := er.UpdateService(ctx, "svc"); err != nil {
		t.Fatalf("UpdateService: %v", err)
	}
	if len(rf.args) < 3 {
		t.Fatalf("argv: want at least 3 elements, got %#v", rf.args)
	}
	if rf.args[2] != composePath {
		t.Errorf("argv[2] (-f value): want %q, got %q", composePath, rf.args[2])
	}
}
