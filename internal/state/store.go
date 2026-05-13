package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
)

// Store is the in-memory authoritative copy of the on-disk state document.
//
// It implements the locked-snapshot pattern from
// .planning/research/ARCHITECTURE.md Pattern 1: an RWMutex guards a value
// (not a pointer) that is mutated under the write lock and snapshotted
// under the read lock. Every successful Update writes through to disk via
// persist() while the write lock is still held, so on-disk content always
// trails the in-memory copy by at most one rename.
//
// Store is safe for concurrent use; callers do not need to add their own
// synchronization.
type Store struct {
	path  string
	mu    sync.RWMutex
	state State
}

// NewStore opens (or creates) the state file at path and returns a Store
// holding its contents in memory.
//
// Boot paths:
//
//   - Missing file (os.IsNotExist / fs.ErrNotExist): initialize an empty
//     State{Version: SchemaVersion, Containers: {}} and persist it, so the
//     binary can come up on a fresh HMI install before any container is
//     watched (STATE-03 boot-from-cold).
//   - Empty file (0 bytes): treated identically to a missing file. This
//     covers the case where a previous process crashed mid-create.
//   - Corrupted file (unparseable JSON): returns an error whose message
//     contains "decode" so an operator can intervene. The file is NOT
//     silently reset — that would lose the previous-digest tail and make
//     rollback impossible (TestCorruptedFile / threat T-01-02-05).
//   - Valid JSON file: unmarshalled into s.state verbatim.
func NewStore(path string) (*Store, error) {
	s := &Store{
		path: path,
		state: State{
			Version:    SchemaVersion,
			Containers: map[string]Container{},
		},
	}

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		// Empty file → treat as missing (a previous process may have
		// crashed between create and first persist). Initialize and
		// write through so subsequent boots see a real document.
		if len(data) == 0 {
			if perr := s.persist(); perr != nil {
				return nil, fmt.Errorf("initialize empty state at %s: %w", path, perr)
			}
			return s, nil
		}
		var st State
		if uerr := json.Unmarshal(data, &st); uerr != nil {
			// Test contract: the error message MUST contain "decode" or
			// "parse" so operators see a clear signal in the boot log.
			return nil, fmt.Errorf("decode state at %s: %w", path, uerr)
		}
		// Defensive: ensure the in-memory Containers map is non-nil even
		// if the on-disk document had a literal null. Update() and Get()
		// both rely on the map being non-nil.
		if st.Containers == nil {
			st.Containers = map[string]Container{}
		}
		s.state = st
		return s, nil

	case errors.Is(err, fs.ErrNotExist):
		// Cold boot: create the file with the empty initial state so the
		// caller can immediately Get() and the parent directory has a
		// real document to fsync on the next Update.
		if perr := s.persist(); perr != nil {
			return nil, fmt.Errorf("create state at %s: %w", path, perr)
		}
		return s, nil

	default:
		return nil, fmt.Errorf("read state at %s: %w", path, err)
	}
}

// Get returns a snapshot of the current state under an RLock.
//
// The returned value is a shallow copy of Store.state. The Containers map
// header is shared; callers MUST treat it as read-only. The only caller
// in v1 is internal/api which json.Marshals the result immediately, so
// the shared-map optimization is safe in practice. If a future caller
// needs to mutate the returned state, copy the Containers map first.
func (s *Store) Get() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Update mutates the in-memory state via fn under the write lock and
// then persists the result to disk before returning.
//
// fn is invoked with a pointer to the live state value; any modifications
// it makes are committed if persist() returns nil. The write lock is held
// for the entire fn + persist() sequence so concurrent Get() calls either
// see the previous coherent state or the next coherent state, never a
// half-mutated intermediate.
//
// If fn nils out the Containers map, Update repopulates it to keep the
// "Containers is never nil" invariant that callers (and the tygo-generated
// TS type) rely on.
//
// All I/O happens inside the lock by design — the v1 throughput
// requirement is a handful of writes per minute (hourly cron + docker
// event bursts), so the simplicity of a single critical section beats
// the complexity of a copy-on-write or queue-based design.
func (s *Store) Update(fn func(*State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.state)
	if s.state.Containers == nil {
		s.state.Containers = map[string]Container{}
	}
	return s.persist()
}

// The persist() method body lives in persist.go (Task 2 of plan 01-02).
// Contract: persist() is called by Update() while s.mu is held in write
// mode, must be idempotent, and must produce an atomically-renamed JSON
// file at s.path. See persist.go for the renameio.WriteFile + parent-dir
// Sync() wrapper that satisfies STATE-02 atomicity and research
// correction A5.
