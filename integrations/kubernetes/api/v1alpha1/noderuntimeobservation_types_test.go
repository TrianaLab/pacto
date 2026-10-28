/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package v1alpha1

import "testing"

// The MaxRuntime* bounds shipped in v6.0 and must keep compiling in a minor
// release; they mirror the CRD's MaxItems markers.
func TestDeprecatedRuntimeBounds(t *testing.T) {
	if MaxRuntimePodsPerNode != 256 || MaxRuntimeListenersPerPod != 64 || MaxRuntimeConnectionsPerPod != 128 {
		t.Fatal("MaxRuntime* bounds changed")
	}
}
