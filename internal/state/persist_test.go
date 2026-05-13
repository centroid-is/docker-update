// RED-FIRST per C4. This test is authored before internal/state/persist.go
// exists. Plan 02 (Wave 2) drives it green by implementing the Store +
// persist() pair using renameio.WriteFile + explicit dir-fsync wrapper
// (research correction A5 / Pitfall A in 01-RESEARCH.md).
//
// What this test guards (FOUND-02 / STATE-02): under heavy write contention,
// every reader of the on-disk state file must see either the previous valid
// JSON snapshot or the next valid JSON snapshot — never a torn or truncated
// half-write. The atomic-rename semantics of renameio.WriteFile provide this
// guarantee at the filesystem layer; this test exercises it under load so a
// regression (e.g. a future "optimization" to in-place writes) is caught.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestPersistAtomicity spawns a writer goroutine that mutates state 1000 times
// and a reader goroutine that os.ReadFile's the state path in a tight loop;
// every successful read must parse as valid JSON.
//
// The reader goroutine uses t.Errorf (not t.Fatal). t.Fatal inside a goroutine
// does not propagate to the test runner — it only halts the goroutine that
// calls it, leaving the test to pass falsely. t.Errorf marks the test failed
// and is goroutine-safe.
func TestPersistAtomicity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Seed with one container so the writer has something to mutate.
	if err := s.Update(func(st *State) {
		st.Version = 1
		st.Containers = map[string]Container{
			"svc1": {Service: "svc1", Tag: "v0"},
		}
	}); err != nil {
		t.Fatalf("seed Update: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(2)

	// Writer: 1000 mutations under the store's lock + persist on each.
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 0; i < 1000; i++ {
			if err := s.Update(func(st *State) {
				c := st.Containers["svc1"]
				c.Tag = fmt.Sprintf("v%d", i)
				st.Containers["svc1"] = c
			}); err != nil {
				t.Errorf("Update iter %d: %v", i, err)
				return
			}
		}
	}()

	// Reader: race the writer reading the on-disk file. Every successful read
	// must parse as valid JSON. A torn write would surface here as a JSON
	// parse error or an empty file.
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				data, err := os.ReadFile(path)
				if err != nil {
					// Transient — the file may not yet exist on the very first
					// iteration if the writer hasn't flushed. Skip and retry.
					continue
				}
				if len(data) == 0 {
					t.Errorf("readback returned empty file")
					continue
				}
				var st State
				if err := json.Unmarshal(data, &st); err != nil {
					t.Errorf("readback parsed as invalid JSON: %v\ndata: %s", err, data)
				}
			}
		}
	}()

	wg.Wait()
}
