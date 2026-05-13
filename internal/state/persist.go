package state

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/google/renameio/v2"
)

// persist writes the in-memory state to disk atomically.
//
// renameio.WriteFile handles temp-file-in-same-dir + fsync(file) + rename
// — the standard atomic-write recipe — so a reader that os.ReadFile's
// s.path concurrently always sees either the previous coherent JSON
// document or the next one, never a torn or truncated half-write
// (TestPersistAtomicity / STATE-02).
//
// renameio.WriteFile does NOT, however, fsync the parent directory after
// the rename. Without that fsync, the rename is durable across a process
// crash (the kernel's page cache holds the new inode entry) but NOT
// durable across a host power loss before the directory inode is
// flushed. We close that window explicitly below.
//
// This wrapper is research correction A5 (Option 2 in
// .planning/phases/01-walking-skeleton-test-harness/01-RESEARCH.md, lines
// 478-513) — see also research/PITFALLS.md Pitfall 7 and renameio
// issue #11. The rationale is documented inline so a future reviewer
// does not "simplify" by stripping the dir-fsync as redundant — it is
// load-bearing for HMI durability across operator-triggered power
// cycles, and Phase 4's SIGKILL fault-injection test depends on it.
//
// Caller invariant: must be called with s.mu held in write mode. Update
// is the only caller in v1 and satisfies this; persist must never be
// called from a Get() path.
func (s *Store) persist() error {
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	if err := renameio.WriteFile(s.path, data, 0o644); err != nil {
		return err
	}
	// Best-effort parent-directory fsync. If os.Open(dir) fails — e.g. the
	// directory was unlinked out from under us mid-write — the rename
	// itself is still visible to subsequent opens via the kernel page
	// cache; we only lose durability across an immediate power loss in
	// that window. Returning nil here matches the renameio semantics
	// (the data is on disk and visible) and avoids surfacing a non-fatal
	// durability hint as a hard write error. The accept-no-error decision
	// is logged in the threat register (T-01-02-02 / disposition mitigate).
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return nil
	}
	defer dir.Close()
	_ = dir.Sync()
	return nil
}
