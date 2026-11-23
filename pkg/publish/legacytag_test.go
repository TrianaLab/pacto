package publish

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// pushLegacy writes b as a Pacto before typed bundles did, under the bundle
// tag, and returns that digest.
func pushLegacy(t *testing.T, b *contract.Bundle) string {
	t.Helper()
	img, err := oci.LegacyBundleImage(b)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := name.ParseReference(Repo(b.Contract.Publish.Bundle)+":"+b.Contract.Service.Version, name.Insecure)
	if err := remote.Write(ref, img); err != nil {
		t.Fatal(err)
	}
	h, _ := img.Digest()
	return h.String()
}

// TestPublishPlanLegacyTagIsIdentical: a tag an older Pacto wrote with the
// same content is identical, never a conflict; an absent tag plans the typed
// digest; an unreadable head never panics.
func TestPublishPlanLegacyTagIsIdentical(t *testing.T) {
	ctx := context.Background()
	host, reg := newRegistry(t)
	b := bundleOf(host, "1.0.0", workloadService)
	ds, err := oci.BundleDigests(b)
	if err != nil {
		t.Fatal(err)
	}

	p := MakePlan(ctx, reg, Input{Bundle: b, Revision: "r"})
	bundle := p.Items[len(p.Items)-1]
	expect(t, bundle.State == Absent && bundle.Digest == ds.Typed, "absent: %+v", bundle)

	p = MakePlan(ctx, brokenRegistry{reg, "head"}, Input{Bundle: b, Revision: "r"})
	bundle = p.Items[len(p.Items)-1]
	expect(t, bundle.State == Unreadable && bundle.Digest == "", "head error: %+v", bundle)

	legacy := pushLegacy(t, b)
	expect(t, legacy == ds.Legacy, "pushed %s, legacy %s", legacy, ds.Legacy)
	p = MakePlan(ctx, reg, Input{Bundle: b, Revision: "r", ContractOnly: true})
	bundle = p.Items[len(p.Items)-1]
	expect(t, bundle.State == Identical && bundle.Digest == ds.Legacy && p.Missing == 0, "legacy tag: %+v", bundle)
}

// TestPublishPlanOtherDigestStillConflicts: other content under the tag is a
// conflict whichever packer wrote it.
func TestPublishPlanOtherDigestStillConflicts(t *testing.T) {
	host, reg := newRegistry(t)
	other := bundleOf(host, "1.0.0", workloadService)
	other.FS = fstest.MapFS{"pacto.yaml": {Data: other.RawYAML}, "README.md": {Data: []byte("other")}}
	pushLegacy(t, other)
	p := MakePlan(context.Background(), reg, Input{Bundle: bundleOf(host, "1.0.0", workloadService), Revision: "r"})
	bundle := p.Items[len(p.Items)-1]
	expect(t, bundle.State == Conflict && !p.Ready, "other content: %+v", bundle)
}
