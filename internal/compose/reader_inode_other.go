//go:build windows

package compose

import "io/fs"

// statInode is the Windows fallback companion of the unix
// implementation in reader_inode_unix.go (WR-06).
//
// Windows does not expose inodes in the unix sense — the closest
// equivalent is the file index returned by GetFileInformationByHandle,
// which the standard library's syscall.Win32FileAttributeData does
// NOT carry. Rather than add a Windows-specific syscall path for a
// platform that is NOT a supported deployment target (CLAUDE.md
// Constraint: "amd64 only for v1" implies linux/amd64; the Dockerfile
// final stage is distroless static-debian12), we return (0, false)
// and let Reader degrade to (mtime, size) drift detection.
//
// This degradation is identical to the FUSE/NFS path on Linux, so the
// operator-facing contract ("we degrade when the FS doesn't expose a
// stable inode") is honoured cleanly. Boot log signal is
// "mtime-size-fallback" in both cases.
func statInode(_ fs.FileInfo) (uint64, bool) {
	return 0, false
}
