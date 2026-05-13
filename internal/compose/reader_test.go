// RED-FIRST per C4. These tests are authored before internal/compose/reader.go
// and internal/compose/errors.go exist. Plan 02-02 (Wave 1 of Phase 02) drives
// them green by implementing the `Reader` struct + `ErrComposeFileMoved`
// sentinel.
//
// What these tests guard (DOCK-02 / Pitfall 10): the compose file pointed at
// by HMI_UPDATE_COMPOSE_PATH is the source of truth for Phase 4's
// `docker compose -f <path> up -d --force-recreate <svc>` invocation. If an
// atomic-save editor or an operator relocation replaces the file underneath
// the bind-mount, acting on the stale inode is dangerous. Reader captures a
// boot snapshot (inode + mtime + size) once at construction; CheckUnchanged
// re-stats on demand and flags ANY drift signal as the documented sentinel
// so callers can branch on `errors.Is(err, compose.ErrComposeFileMoved)`.
//
// Test contract:
//   - TestNewReader_EmptyPath: empty path is a fail-fast error so an unset
//     HMI_UPDATE_COMPOSE_PATH surfaces a clear operator signal at boot.
//   - TestNewReader_MissingFile: a path that does not exist returns an error
//     wrapping fs.ErrNotExist; cmd/hmi-update/main.go log.Fatalfs on this.
//   - TestNewReader_HappyPath: existing file → snapshot captured; CheckUnchanged
//     returns nil on the first call and on 50 subsequent calls (idempotent,
//     no false positives, no internal state mutation).
//   - TestCheckUnchanged_AtomicRename: vim/VSCode-style save (write tmp +
//     os.Rename atop the target) changes the inode; CheckUnchanged returns
//     ErrComposeFileMoved.
//   - TestCheckUnchanged_InPlaceEdit: os.WriteFile on the same path changes
//     mtime (and likely size); CheckUnchanged returns ErrComposeFileMoved.
//     The 50ms sleep before the rewrite tightens against macOS HFS+ 1s mtime
//     resolution.
//   - TestCheckUnchanged_Concurrent: 8 goroutines × 100 reads against an
//     unchanged file all return nil; -race must stay clean. Goroutine
//     assertions use t.Errorf (not t.Fatal) per the persist_test.go
//     convention.
//   - TestCheckUnchanged_FileDeleted: os.Remove on the watched path → stat
//     ENOENT → CheckUnchanged returns ErrComposeFileMoved (unified
//     remediation: restart hmi-update).
package compose

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestNewReader_EmptyPath asserts that NewReader fails fast on an empty path
// — the operator-visible signal for an unset HMI_UPDATE_COMPOSE_PATH.
func TestNewReader_EmptyPath(t *testing.T) {
	r, err := NewReader("")
	if err == nil {
		t.Fatalf("NewReader(\"\"): want error, got nil (reader=%v)", r)
	}
	if r != nil {
		t.Errorf("NewReader(\"\"): want nil reader on error, got %v", r)
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "compose.newreader") {
		t.Errorf("NewReader empty-path error: want mention of 'compose.NewReader' for operator clarity, got: %v", err)
	}
	if !strings.Contains(msg, "empty") {
		t.Errorf("NewReader empty-path error: want mention of 'empty' for operator clarity, got: %v", err)
	}
}

// TestNewReader_MissingFile asserts that NewReader on a non-existent path
// returns an error wrapping fs.ErrNotExist so callers can branch with
// errors.Is.
func TestNewReader_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yml")

	r, err := NewReader(path)
	if err == nil {
		t.Fatalf("NewReader on missing file: want error, got nil (reader=%v)", r)
	}
	if r != nil {
		t.Errorf("NewReader on missing file: want nil reader on error, got %v", r)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("NewReader on missing file: want errors.Is(err, fs.ErrNotExist) = true, got err=%v", err)
	}
}

// TestNewReader_HappyPath writes a real file, opens a Reader, then calls
// CheckUnchanged 50 times to prove the boot snapshot is captured and the
// no-op path is cheap and idempotent (no false positives, no internal state
// mutation that would surface as a later drift on a quiet file).
func TestNewReader_HappyPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.WriteFile(path, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	r, err := NewReader(path)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	if r == nil {
		t.Fatalf("NewReader: returned nil reader without error")
	}

	ctx := context.Background()
	for i := 0; i < 50; i++ {
		if err := r.CheckUnchanged(ctx); err != nil {
			t.Fatalf("CheckUnchanged iter %d on unchanged file: want nil, got %v", i, err)
		}
	}
}

// TestCheckUnchanged_AtomicRename writes the compose file, opens the Reader,
// then performs a vim/VSCode atomic save: write tmp + os.Rename. The new
// file has a different inode; CheckUnchanged MUST return the sentinel.
func TestCheckUnchanged_AtomicRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(path, []byte("services: {original: {}}\n"), 0o644); err != nil {
		t.Fatalf("write initial compose file: %v", err)
	}

	r, err := NewReader(path)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	ctx := context.Background()
	if err := r.CheckUnchanged(ctx); err != nil {
		t.Fatalf("CheckUnchanged before drift: want nil, got %v", err)
	}

	// Atomic-save: write tmp then rename atop the target. This is what vim
	// (`:w`), VS Code, and Helix do; the resulting file has a different
	// inode from the original even when content is unchanged.
	tmp := path + ".tmp-atomic"
	if err := os.WriteFile(tmp, []byte("services: {replaced: {}}\n"), 0o644); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("os.Rename %s -> %s: %v", tmp, path, err)
	}

	err = r.CheckUnchanged(ctx)
	if err == nil {
		t.Fatalf("CheckUnchanged after atomic rename: want error, got nil")
	}
	if !errors.Is(err, ErrComposeFileMoved) {
		t.Errorf("CheckUnchanged after atomic rename: want errors.Is(err, ErrComposeFileMoved) = true, got err=%v", err)
	}
}

// TestCheckUnchanged_InPlaceEdit rewrites the same file in place. On most
// filesystems the inode is preserved, but mtime and (likely) size change.
// The Reader's belt-and-braces (mtime, size) comparison catches this even
// on stable-inode filesystems.
//
// The 50ms sleep guarantees a different mtime even on slow-resolution
// filesystems (macOS HFS+ is 1s; APFS/ext4 are sub-second). Without the
// sleep this test would flake on HFS+ developer machines.
func TestCheckUnchanged_InPlaceEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.WriteFile(path, []byte("services: {a: {}}\n"), 0o644); err != nil {
		t.Fatalf("write initial compose file: %v", err)
	}

	r, err := NewReader(path)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	// Tighten against low-resolution filesystem mtime (HFS+ = 1s).
	time.Sleep(50 * time.Millisecond)

	if err := os.WriteFile(path, []byte("services: {a: {}, b: {}}\n"), 0o644); err != nil {
		t.Fatalf("in-place rewrite: %v", err)
	}

	err = r.CheckUnchanged(context.Background())
	if err == nil {
		t.Fatalf("CheckUnchanged after in-place edit: want error, got nil")
	}
	if !errors.Is(err, ErrComposeFileMoved) {
		t.Errorf("CheckUnchanged after in-place edit: want errors.Is(err, ErrComposeFileMoved) = true, got err=%v", err)
	}
}

// TestCheckUnchanged_Concurrent spawns 8 goroutines that each call
// CheckUnchanged 100 times against an unchanged file. All calls must return
// nil and `go test -race` must stay clean. Off-goroutine assertions use
// t.Errorf (not t.Fatal) per the persist_test.go convention — t.Fatal in a
// goroutine halts only that goroutine, leaving the test to pass falsely.
func TestCheckUnchanged_Concurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.WriteFile(path, []byte("services: {a: {}}\n"), 0o644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	r, err := NewReader(path)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	const goroutines = 8
	const calls = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			ctx := context.Background()
			for i := 0; i < calls; i++ {
				if err := r.CheckUnchanged(ctx); err != nil {
					t.Errorf("goroutine %d iter %d: CheckUnchanged on unchanged file: want nil, got %v", id, i, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestCheckUnchanged_FileDeleted removes the watched file. Stat returns
// ENOENT; CheckUnchanged unifies this under ErrComposeFileMoved because the
// operator remediation is identical (restart hmi-update). Test passing also
// implicitly verifies that the underlying os error is preserved via wrap
// (so a caller wanting to distinguish can still do `errors.Is(err,
// fs.ErrNotExist)`).
func TestCheckUnchanged_FileDeleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.WriteFile(path, []byte("services: {a: {}}\n"), 0o644); err != nil {
		t.Fatalf("write compose file: %v", err)
	}

	r, err := NewReader(path)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("os.Remove: %v", err)
	}

	err = r.CheckUnchanged(context.Background())
	if err == nil {
		t.Fatalf("CheckUnchanged after delete: want error, got nil")
	}
	if !errors.Is(err, ErrComposeFileMoved) {
		t.Errorf("CheckUnchanged after delete: want errors.Is(err, ErrComposeFileMoved) = true, got err=%v", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("CheckUnchanged after delete: want errors.Is(err, fs.ErrNotExist) = true (underlying stat error preserved), got err=%v", err)
	}
}
