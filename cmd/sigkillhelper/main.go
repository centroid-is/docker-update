// cmd/sigkillhelper is the helper binary spawned by
// internal/state/store_sigkill_test.go. Writes incrementing state values
// in a tight loop until SIGKILLed. arm64 build deferred (V2-ARM64);
// amd64-only per CLAUDE.md "Platform".
//
// This binary is NEVER packaged into the production image — it is built
// ad-hoc by the parent test via `go build ./cmd/sigkillhelper` into
// t.TempDir(). Not part of `make build`.
//
// Why a separate binary (not in-process): SIGKILL kills the entire
// process. If we ran the writer in-process, the parent test would die
// too. Forking is the only way to send SIGKILL "from outside."
//
// Each iteration writes a DISTINCT payload — the counter is embedded in
// the synthetic sha256-shaped CurrentDigest, so a torn write would
// manifest as a truncated JSON document with a parse-error surface at
// the parent test's json.Unmarshal call.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/centroid-is/hmi-update/internal/state"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: sigkillhelper <state-path>")
		os.Exit(2)
	}
	statePath := os.Args[1]
	store, err := state.NewStore(statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewStore: %v\n", err)
		os.Exit(1)
	}

	counter := 0
	for {
		counter++
		if err := store.Update(func(st *state.State) {
			st.Containers["svc"] = state.Container{
				Service:       "svc",
				Image:         "test/image",
				Tag:           "latest",
				CurrentDigest: fmt.Sprintf("sha256:%064d", counter),
			}
		}); err != nil {
			// Persist error -> log to stderr but keep looping. The parent
			// test's contract is that *some* write completed before
			// SIGKILL; we want the loop to keep producing fresh writes
			// rather than exiting cleanly (which would race the SIGKILL).
			fmt.Fprintf(os.Stderr, "Update %d: %v\n", counter, err)
		}
		// Brief sleep so the parent's SIGKILL has a chance to land
		// mid-write rather than always between writes. 100µs is short
		// enough that ~100-500 iterations happen within the parent's
		// 1-50ms delay window.
		time.Sleep(100 * time.Microsecond)
	}
}
