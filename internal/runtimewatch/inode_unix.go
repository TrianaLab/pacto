//go:build unix

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import (
	"io/fs"
	"syscall"
)

// inodeOf is the inode of a file from os.DirFS; 0 when the FS carries none.
func inodeOf(fi fs.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino) //nolint:unconvert // Ino is not uint64 on every unix
	}
	return 0
}
