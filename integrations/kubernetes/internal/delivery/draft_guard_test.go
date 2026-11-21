/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"slices"
	"testing"
)

// The registry watch judges versions only: draft-<n> tags pushed after the
// published 1.0.0, even signed by the rule's signer, get no verdict and never
// become the candidate.
func TestRegistryWatchSkipsDraftTags(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	d := w.push("1.0.0", acmeRepo, w.ci(acmeSubject, acmeRepo))
	drafts := make([]string, 0, 2)
	for _, tag := range []string{"draft-1", "draft-2"} {
		drafts = append(drafts, w.push(tag, acmeRepo, w.ci(acmeSubject, acmeRepo)))
	}
	st := w.reconcile()
	if vs := st.Publisher.Verdicts; len(vs) != 1 || vs[0].Digest != d || vs[0].Version != "1.0.0" {
		t.Fatalf("verdicts = %+v; want 1.0.0 only", vs)
	}
	if st.Candidate != d || slices.ContainsFunc(drafts, func(x string) bool { return slices.Contains(st.Published, x) }) {
		t.Fatalf("candidate %q, published %v; want 1.0.0 and no draft", st.Candidate, st.Published)
	}
}
