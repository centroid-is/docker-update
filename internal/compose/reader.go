package compose

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"
)

// Reader holds the boot snapshot of the compose file's stat metadata
// and exposes a CheckUnchanged method that re-stats the path on demand.
//
// Reader is safe for concurrent use. CheckUnchanged is read-only and
// may be called from any goroutine; an RWMutex protects the boot
// snapshot fields in the (currently impossible) case where a future
// CONTEXT.md decision rotates the snapshot at runtime. The mutex is
// taken in read mode for CheckUnchanged; no caller in v1 takes write
// mode after construction.
//
// Design intent: the Reader is the cheapest possible primitive that
// satisfies DOCK-02 ("stat-before-act and inode-drift detection"). A
// single os.Stat at boot captures the snapshot; a single os.Stat per
// CheckUnchanged call compares against it. There is no goroutine, no
// fsnotify dependency, no YAML parsing — the docker daemon is the
// source of truth for compose service identity (CONTEXT.md
// "Compose-File Reader").
type Reader struct {
	path string

	mu sync.RWMutex
	// bootInode is set at construction. On Linux/Darwin,
	// syscall.Stat_t.Ino gives a stable inode that uniquely identifies
	// the file at that moment. If a future operator atomic-saves the
	// compose file (vim/VSCode/Helix style: write tmp + os.Rename atop
	// target), the new file has a different inode and CheckUnchanged
	// trips ErrComposeFileMoved.
	bootInode uint64
	// bootModTime + bootSize are the fallback signal for filesystems
	// where inodes are not stable (some FUSE mounts return inode 0 or
	// reuse inodes within a session). They are ALSO a belt-and-braces
	// signal for in-place edits: same inode but updated content. We
	// always compare (mtime, size) — not only when bootInodeStable is
	// false — so an `os.WriteFile(path, ...)` in place is detected even
	// on ext4/APFS where the inode would not change.
	//
	// Do not "simplify" by gating mtime/size on !bootInodeStable —
	// in-place edit detection is load-bearing for Pitfall 10. See
	// internal/state/persist.go for an analogous "do not simplify"
	// callout pattern.
	bootModTime time.Time
	bootSize    int64
	// bootInodeStable is false when the filesystem returned inode 0
	// (FUSE / NFS variants) or the OS doesn't expose syscall.Stat_t at
	// all. In that case CheckUnchanged compares (mtime, size) only;
	// the slog event at boot logs drift_signal="mtime-size-fallback"
	// so a future operator running on a non-stable-inode HMI sees the
	// weaker guarantee in the boot log.
	bootInodeStable bool
}

// NewReader stats the compose file at path and captures a boot snapshot
// (inode + mtime + size). NewReader fails fast on:
//
//   - empty path (an unset HMI_UPDATE_COMPOSE_PATH env var) — surfaces as
//     a clear "compose.NewReader: empty path" error so the operator can
//     fix their compose service environment block.
//   - the file not existing or not being stattable — the wrapped error
//     preserves the underlying os error (fs.ErrNotExist / fs.ErrPermission /
//     etc.) so callers can branch with errors.Is.
//
// The caller (cmd/hmi-update/main.go in plan 02-04) wraps the error and
// calls log.Fatalf so the operator sees the cause at boot rather than
// discovering it on the first Phase 4 update/rollback attempt.
func NewReader(path string) (*Reader, error) {
	if path == "" {
		return nil, fmt.Errorf("compose.NewReader: empty path (set HMI_UPDATE_COMPOSE_PATH)")
	}
	r := &Reader{path: path}
	if err := r.captureBootSnapshot(); err != nil {
		return nil, fmt.Errorf("compose.NewReader: %w", err)
	}
	return r, nil
}

// captureBootSnapshot stats the file once and stores the result on the
// Reader. Called from NewReader; not exported. Logs the snapshot at
// slog.Info exactly once so the operator can see which drift signal
// regime is active for this HMI's filesystem.
func (r *Reader) captureBootSnapshot() error {
	info, err := os.Stat(r.path)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.bootModTime = info.ModTime()
	r.bootSize = info.Size()

	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Ino != 0 {
		r.bootInode = uint64(st.Ino) //nolint:unconvert // st.Ino type differs between linux (uint64) and darwin (uint64) — explicit conversion keeps cross-platform parity readable.
		r.bootInodeStable = true
		slog.Info("compose.reader.boot",
			"path", r.path,
			"inode", r.bootInode,
			"mtime", r.bootModTime.Format(time.RFC3339Nano),
			"size", r.bootSize,
			"drift_signal", "inode-primary",
		)
	} else {
		// Fallback: (mtime, size) only. Document in slog so a future
		// operator on a FUSE/NFS HMI knows why the signal is weaker.
		r.bootInodeStable = false
		slog.Info("compose.reader.boot",
			"path", r.path,
			"mtime", r.bootModTime.Format(time.RFC3339Nano),
			"size", r.bootSize,
			"drift_signal", "mtime-size-fallback",
		)
	}
	return nil
}

// CheckUnchanged re-stats the compose file and returns:
//
//   - nil if the file's inode (when stable) AND (mtime, size) match the
//     boot snapshot.
//   - a wrapped ErrComposeFileMoved if any of those signals differ.
//   - a wrapped fs.ErrNotExist (or other os error) AND ErrComposeFileMoved
//     if the stat itself fails. We unify "deleted" under "moved" because
//     the operator remediation is the same: restart hmi-update. The
//     underlying os error is preserved in the wrap chain so callers can
//     still branch on errors.Is(err, fs.ErrNotExist) if needed.
//
// CheckUnchanged is O(1) — a single stat syscall — so calling it before
// every Phase 4 mutating action is cheap even on the busiest HMIs (10s
// of actions per minute would be exotic; one stat is microseconds).
//
// The ctx parameter is accepted for API symmetry with the rest of the
// codebase and to leave room for future cancellation (e.g. a phase that
// wants a stat with a deadline). The current implementation does not
// block, so ctx is unused; explicit `_ = ctx` documents that.
func (r *Reader) CheckUnchanged(ctx context.Context) error {
	_ = ctx

	info, err := os.Stat(r.path)
	if err != nil {
		// File deleted or otherwise unreachable → treat as drift. Wrap
		// so errors.Is(err, ErrComposeFileMoved) is true AND
		// errors.Is(err, fs.ErrNotExist) is true (preserving both
		// signals for callers that want to distinguish — Phase 4's 412
		// handler does not distinguish today, but Phase 5's UI might).
		return fmt.Errorf("compose.CheckUnchanged stat %s: %w: %w", r.path, ErrComposeFileMoved, err)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	// Compare (mtime, size) — always; this is the belt-and-braces
	// signal that also catches in-place edits. Equal() not == on time
	// because time.Time carries a monotonic clock reading on some
	// platforms and == would falsely diverge across stat readbacks.
	if !info.ModTime().Equal(r.bootModTime) || info.Size() != r.bootSize {
		return fmt.Errorf("compose.CheckUnchanged %s: mtime/size drift (boot mtime=%s size=%d, now mtime=%s size=%d): %w",
			r.path,
			r.bootModTime.Format(time.RFC3339Nano), r.bootSize,
			info.ModTime().Format(time.RFC3339Nano), info.Size(),
			ErrComposeFileMoved,
		)
	}

	// Compare inode (when stable). On FUSE-style filesystems we skip
	// this comparison entirely; the mtime/size check above is the
	// only signal.
	if r.bootInodeStable {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			//nolint:unconvert // explicit uint64 conversion keeps cross-platform parity readable.
			if uint64(st.Ino) != r.bootInode {
				return fmt.Errorf("compose.CheckUnchanged %s: inode drift (boot=%d, now=%d): %w",
					r.path, r.bootInode, uint64(st.Ino),
					ErrComposeFileMoved,
				)
			}
		}
		// If info.Sys() is no longer a *syscall.Stat_t (impossible on a
		// single boot lifetime — we'd have failed bootInodeStable above)
		// we silently skip the inode comparison and rely on mtime/size.
	}

	return nil
}
