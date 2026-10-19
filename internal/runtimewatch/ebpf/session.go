/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package ebpf

import "os/exec"

// ReasonAttaching is the collector's reason until its first attach: a caller
// waiting for the program tells "not yet" from "failed" by it.
const ReasonAttaching = "Attaching"

// Session is the cgroup `pacto run` starts its child in, so every socket of
// the child's process tree is attributed to it by cgroup.
type Session interface {
	// Prepare makes cmd start its process directly inside the cgroup.
	Prepare(cmd *exec.Cmd)
	// Close kills whatever is left in the cgroup and removes it.
	Close() error
}
