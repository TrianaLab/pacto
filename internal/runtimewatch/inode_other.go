//go:build !unix

/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package runtimewatch

import "io/fs"

// inodeOf: no cgroups here, so nothing resolves and attribution uses the
// network namespace (which never exists either: the agent needs Linux).
func inodeOf(fs.FileInfo) uint64 { return 0 }
