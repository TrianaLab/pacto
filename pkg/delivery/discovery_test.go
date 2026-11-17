package delivery

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// withOwnerFile makes b a 2.5 bundle with no declared owner whose inferred
// record names one read from an owner file.
func withOwnerFile(b *contract.Bundle) *contract.Bundle {
	b.Contract.PactoVersion, b.Contract.Service.Owner = "2.5", contract.Owner{}
	rec := (&contract.InferredRecord{Format: contract.InferredRecordFormat,
		Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "2", Sources: []contract.InferredSourceFile{{Path: "repository:.github/CODEOWNERS", Digest: "sha256:" + strings.Repeat("0", 64)}}},
		Fields: []contract.Inferred{{Field: "service.owner", Value: "team team-x", Source: "repository:.github/CODEOWNERS:4", Confidence: contract.ConfidenceLikely, Class: contract.InferredClassOwnerFile}},
	}).Marshal()
	b.FS = fstest.MapFS{contract.InferredRecordFile: {Data: rec}}
	return b
}

// An owner read from an owner file never routes a delivery decision: the
// floor's owners are the same with and without it, and never name it.
func TestDiscoveredOwnerIsNotTheOwner(t *testing.T) {
	floor := func(owned bool) []string {
		fb, tb := bundle("payments", "1.0.0"), bundle("payments", "2.0.0")
		if owned {
			fb, tb = withOwnerFile(fb), withOwnerFile(tb)
		} else {
			fb.Contract.Service.Owner, tb.Contract.Service.Owner = contract.Owner{}, contract.Owner{}
		}
		p := planWith(t, promote(dA, dB), StageStatus{Pointer: dA, Verified: []string{dA}}, rev(dA, c1, fb), rev(dB, c2, tb), checkoutPacto("^1.0.0"))
		return p.Floor.Owners
	}
	without, with := floor(false), floor(true)
	if !slices.Equal(without, with) || slices.Contains(with, "team-x") {
		t.Errorf("floor owners %v without the record, %v with it", without, with)
	}
}
