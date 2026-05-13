// RED-FIRST per C4. These tests are authored before internal/state/store.go
// exists. Plan 02 (Wave 2) drives them green by implementing NewStore, Get,
// Update, and the version: 1 schema.
//
// What these tests guard:
//   - TestLoadAndPersist: STATE-03 round trip. Open a store at a tempdir path,
//     mutate it, close, re-open, assert the read-back state has version: 1 and
//     contains the seeded service.
//   - TestMissingFile: STATE-03 boot-from-cold. NewStore at a non-existent path
//     must return without error and yield an empty State{Version: 1}, so the
//     binary can come up on a fresh HMI install before any container is
//     watched.
//   - TestCorruptedFile: operator-visible signal — a corrupted state file is
//     NOT silently overwritten; NewStore must return an error mentioning
//     "parse" or "decode" so the operator can intervene.
package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadAndPersist exercises the load+persist round trip via the public API.
// Drives STATE-03.
func TestLoadAndPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// First open: seed.
	s1, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore (first open): %v", err)
	}
	if err := s1.Update(func(st *State) {
		st.Version = 1
		st.Containers = map[string]Container{
			"svc1": {Service: "svc1", Tag: "latest"},
		}
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Second open: re-read from disk via NewStore on the same path.
	s2, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore (second open): %v", err)
	}
	st := s2.Get()
	if st.Version != 1 {
		t.Errorf("Version: want 1, got %d", st.Version)
	}
	if _, ok := st.Containers["svc1"]; !ok {
		t.Errorf("Containers: want key %q, got map %v", "svc1", st.Containers)
	}
}

// TestMissingFile asserts NewStore on a non-existent path yields an empty
// State{Version: 1, Containers: {}} — the boot-from-cold path.
func TestMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore on missing file: want nil err, got %v", err)
	}
	st := s.Get()
	if st.Version != 1 {
		t.Errorf("Version on cold boot: want 1, got %d", st.Version)
	}
	if len(st.Containers) != 0 {
		t.Errorf("Containers on cold boot: want empty map, got %v", st.Containers)
	}
}

// TestCorruptedFile asserts that a state file containing non-JSON garbage
// causes NewStore to return an error mentioning "parse" or "decode" — the
// operator-visible signal that the file needs manual intervention. We MUST
// NOT silently reset the file; that would lose the previous-digest tail and
// make rollback impossible.
func TestCorruptedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := os.WriteFile(path, []byte("this is not JSON {{{{"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	_, err := NewStore(path)
	if err == nil {
		t.Fatalf("NewStore on corrupted file: want error, got nil")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "parse") && !strings.Contains(msg, "decode") {
		t.Errorf("NewStore error: want mention of 'parse' or 'decode' for operator clarity, got: %v", err)
	}
}
