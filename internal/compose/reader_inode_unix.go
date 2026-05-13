//go:build !windows

package compose

import (
	"io/fs"
	"syscall"
)

// statInode extracts the filesystem inode from a stat-time fs.FileInfo
// on unix-like systems (Linux, Darwin, BSD). Returns (inode, true) when
// Sys() is a *syscall.Stat_t with a non-zero Ino, otherwise (0, false).
//
// The Ino==0 fallback path matters: some FUSE drivers and a handful of
// NFS server configurations return 0 for the inode, indicating "no
// stable identity available." Reader.captureBootSnapshot reads the
// boolean half of this return and switches to (mtime, size) drift
// detection when ok=false — see the WR-06 portability note in reader.go.
//
// The explicit uint64 conversion is load-bearing across architectures:
// st.Ino is uint64 on linux/amd64 and darwin/arm64, but the conversion
// keeps the code readable when grepping for "the inode lives here".
func statInode(info fs.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Ino == 0 {
		return 0, false
	}
	return uint64(st.Ino), true //nolint:unconvert // explicit uint64 keeps cross-arch parity readable.
}
