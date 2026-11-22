/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package loader

import (
	"slices"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
)

// A bundle repository holding write-once draft-<n> tags (declarations waiting
// for a person) lists only its versions: the operator never reads a draft as
// a revision.
func TestListTagsSkipsDraftTags(t *testing.T) {
	reg := newRecordingRegistry(t, false)
	for _, tag := range []string{"1.0.0", "draft-1", "draft-2"} {
		reg.push(t, "acme/invoices:"+tag)
	}
	p := &OCIPuller{keychain: staticKeychain{auth: &authn.AuthConfig{Username: "operator", Password: "read"}}}
	tags, err := p.ListTags(t.Context(), "oci://"+reg.host+"/acme/invoices", nil)
	if err != nil || !slices.Equal(tags, []string{"1.0.0"}) {
		t.Fatalf("ListTags = %v, %v; want [1.0.0]", tags, err)
	}
}
