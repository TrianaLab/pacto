/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// TestClusterGateWithoutSubject: a check with no version under it is Unknown,
// not an index out of range.
func TestClusterGateWithoutSubject(t *testing.T) {
	v := clusterGate{}.Check(context.Background(), delivery.GateInput{}, nil)
	if v.Result != delivery.Unknown || v.Message != "no version to check" {
		t.Fatalf("no subject: %+v", v)
	}
}
