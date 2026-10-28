//go:build !unix

package pkgtar

import "io/fs"

type fileID struct{}

// hardlinkID reports no hardlinks: every file is stored as a regular file.
func hardlinkID(fs.FileInfo) (fileID, bool) { return fileID{}, false }
