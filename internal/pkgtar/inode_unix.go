//go:build unix

package pkgtar

import (
	"io/fs"
	"syscall"
)

type fileID struct{ dev, ino uint64 }

// hardlinkID returns the identity of a regular file that has more than one
// link, so later links to it can be stored as tar hardlinks.
func hardlinkID(info fs.FileInfo) (fileID, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Nlink < 2 {
		return fileID{}, false
	}
	return fileID{uint64(st.Dev), uint64(st.Ino)}, true
}
