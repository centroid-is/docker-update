//go:build sigkill_test
// +build sigkill_test

// RED-FIRST per C4. Build-tagged so default `go test ./...` stays fast.
//
// What this test guards (STATE-04): the renameio + parent-dir-fsync
// pattern (internal/state/persist.go) must leave hmi_update_state.json
// in a parseable state (either prior or new content) even when the
// writer process is SIGKILLed mid-write. Parent-test spawns
// cmd/sigkillhelper, sends SIGKILL at randomized 1-50ms intervals,
// verifies the on-disk file parses cleanly after every iteration.
// 100 iterations, zero corruption.
//
// To run: make test-sigkill (or go test -tags=sigkill_test ./internal/state/...).
//
// Why a build tag: the fork/exec/SIGKILL pattern is slow (~5-15s wall-clock
// for 100 iterations) and OS-coupled. Default `go test ./...` should stay
// fast (<5s); operators must explicitly opt in via `make test-sigkill`.
//
// Goroutine assertion contract (Pattern I from 04-PATTERNS.md): n/a here
// because the test is single-threaded; the helper is a subprocess. SIGKILL
// is the only signal sent, and helper.Wait() reaps the zombie before the
// parent reads the file.
package state_test

import (
	"encoding/json"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/centroid-is/hmi-update/internal/state"
)

const sigkillIterations = 100

func TestSIGKILLDuringWrite(t *testing.T) {
	tmpDir := t.TempDir()
	helperBin := filepath.Join(tmpDir, "sigkillhelper")
	statePath := filepath.Join(tmpDir, "state.json")

	// Build the helper binary once at test entry. Path is relative to this
	// test file's directory (internal/state -> ../../cmd/sigkillhelper).
	cmd := exec.Command("go", "build", "-o", helperBin, "../../cmd/sigkillhelper")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v: %s", err, out)
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Track how many iterations actually exercised the unmarshal path
	// (file present + non-empty + parseable). On macOS the helper's Go
	// runtime startup can outrun a short SIGKILL delay, so a fraction of
	// iterations will return file-missing or empty. We require at least
	// 1 successful parse to confirm the test is meaningfully exercising
	// the renameio + dir-fsync invariant rather than skipping the whole
	// way through; if zero parses occur the test is uninformative and we
	// fail with a clear diagnostic so the operator can widen the SIGKILL
	// delay range.
	parsedCount := 0

	for i := 0; i < sigkillIterations; i++ {
		helper := exec.Command(helperBin, statePath)
		if err := helper.Start(); err != nil {
			t.Fatalf("iter %d: start helper: %v", i, err)
		}

		delay := time.Duration(1+rng.Intn(50)) * time.Millisecond
		time.Sleep(delay)
		_ = helper.Process.Signal(syscall.SIGKILL)
		_ = helper.Wait() // reap; ignore exit code (SIGKILL = -9)

		data, err := os.ReadFile(statePath)
		if err != nil {
			// File-missing is acceptable on ANY iteration where the
			// helper's Go runtime startup (which on macOS can take
			// 50-100ms) outran the parent's 1-50ms SIGKILL delay. Once
			// the helper writes once, the file persists across
			// subsequent iterations (SIGKILL on a later helper cannot
			// unlink the file written by a previous one). So a missing
			// file on iter N just means EVERY iteration up to N had its
			// SIGKILL beat the helper's first write — uninformative but
			// not a corruption signal.
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("iter %d (delay %v): read state: %v", i, delay, err)
		}
		if len(data) == 0 {
			// Empty file is acceptable per state.NewStore contract
			// (treated as "no state yet" — see store.go boot paths).
			continue
		}
		var st state.State
		if err := json.Unmarshal(data, &st); err != nil {
			t.Fatalf("iter %d (delay %v): file CORRUPTED after SIGKILL:\n  err: %v\n  data: %q",
				i, delay, err, string(data))
		}
		if st.Version != state.SchemaVersion {
			t.Fatalf("iter %d: unexpected version %d in parsed state", i, st.Version)
		}
		parsedCount++
	}

	if parsedCount == 0 {
		t.Fatalf("zero iterations parsed an on-disk state file across %d SIGKILL events: "+
			"the helper's runtime startup is consistently outracing the 1-50ms SIGKILL delay. "+
			"Widen the delay range in this test or investigate helper startup latency.",
			sigkillIterations)
	}
	t.Logf("PASSED %d SIGKILL iterations with zero corruption (%d iterations exercised the unmarshal path)",
		sigkillIterations, parsedCount)
}
