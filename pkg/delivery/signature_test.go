package delivery

import (
	"fmt"
	"testing"
)

// The rule the Publish page and the lifecycle answer share.
func TestSignatureEvidence(t *testing.T) {
	const d = "sha256:d"
	full := make([]string, MaxPublished)
	for i := range full {
		full[i] = fmt.Sprintf("sha256:%064d", i)
	}
	oci := Candidates{OCIRepositoryRef: &LocalRef{Name: "oci"}}
	at := func(c Candidates, s StageStatus) StageObject {
		st := stage("prod", "pay", "pay", s)
		st.Spec.Candidates = c
		return st
	}
	cases := map[string]struct {
		stages []StageObject
		want   string
	}{
		"published":             {[]StageObject{at(Candidates{}, StageStatus{Published: []string{d}})}, SignatureSigned},
		"offered":               {[]StageObject{at(Candidates{UpstreamStage: &StageRef{Name: "up"}}, StageStatus{Offered: []string{d}})}, SignatureSigned},
		"entry below the cap":   {[]StageObject{at(oci, StageStatus{Pointer: d})}, SignatureUnsigned},
		"entry at the cap":      {[]StageObject{at(oci, StageStatus{Pointer: d, Published: full})}, SignatureUnknown},
		"downstream":            {[]StageObject{at(Candidates{UpstreamStage: &StageRef{Name: "up"}}, StageStatus{Pointer: d})}, SignatureUnknown},
		"signed beats unsigned": {[]StageObject{at(oci, StageStatus{Pointer: d}), at(Candidates{}, StageStatus{Offered: []string{d}})}, SignatureSigned},
	}
	for name, c := range cases {
		var e SignatureEvidence
		for _, st := range c.stages {
			e.Add(st, d)
		}
		if got := e.Signature(); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}
