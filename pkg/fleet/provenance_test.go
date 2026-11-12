package fleet

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

func testProvenance(source string) *BundleProvenance {
	created := fixedNow().Add(-time.Hour)
	return &BundleProvenance{
		Signature: BundleSignature{Status: SignatureVerified, Signer: "me@example.com", Issuer: "https://issuer", Bundles: 1},
		Referrers: NewReferrersPreview([]OCIReferrer{{ArtifactType: "application/vnd.dev.sigstore.bundle.v0.3+json", Digest: "sha256:sig", Created: &created}}),
		Source:    source, CheckedAt: fixedNow(),
	}
}

func provRaw(t *testing.T, p *BundleProvenance) RawRevision {
	return RawRevision{Bundle: schemaBundle(t, "1.0.0"), Digest: "sha256:one", Provenance: p}
}

func TestBuildKeepsProvenance(t *testing.T) {
	raw := provRaw(t, testProvenance("oci"))
	q := schemaFleet(t, []RawRevision{raw}, nil)
	key := revKeyForVersion(t, q, "svc", "1.0.0")
	raw.Provenance.Referrers.Items[0].Created = &time.Time{}
	raw.Provenance.Referrers.Items[0].Digest = "sha256:changed"

	got := q.snap.Revisions[key].Provenance
	if got == nil || got.Referrers.Items[0].Digest != "sha256:sig" || got.Referrers.Items[0].Created.IsZero() {
		t.Fatalf("snapshot provenance = %+v", got)
	}
	d, err := q.EntityDetail(KindRevision, string(key))
	if err != nil {
		t.Fatal(err)
	}
	if b := d.Revision.Provenance.Bundle; b == nil || b.Signature.Status != SignatureVerified {
		t.Errorf("revision detail provenance.bundle = %+v", b)
	}
	view, err := q.GetService("svc")
	if err != nil {
		t.Fatal(err)
	}
	if p := view.Revisions[0].Provenance; p == nil || p.Signature.Signer != "me@example.com" {
		t.Errorf("clone provenance = %+v", p)
	}
}

func TestMergeProvenance(t *testing.T) {
	build := func(order ...*BundleProvenance) *BundleProvenance {
		var srcs []Source
		for i, p := range order {
			srcs = append(srcs, NewMemorySource(fmt.Sprintf("s%d", i), "local", &Collection{Revisions: []RawRevision{provRaw(t, p)}}))
		}
		snap, err := Build(context.Background(), BuildOptions{Now: fixedNow}, srcs...)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range snap.Revisions {
			return r.Provenance
		}
		return nil
	}
	for _, order := range [][]*BundleProvenance{{nil, testProvenance("oci")}, {testProvenance("oci"), nil}} {
		if got := build(order...); got == nil || got.Source != "oci" {
			t.Errorf("nil + oci = %+v, want oci", got)
		}
	}
	for _, order := range [][]*BundleProvenance{{testProvenance("b"), testProvenance("a")}, {testProvenance("a"), testProvenance("b")}} {
		if got := build(order...); got == nil || got.Source != "a" {
			t.Errorf("both checked = %+v, want a", got)
		}
	}
}

func signatureFleet() *Query {
	prov := func(status string) *BundleProvenance {
		p := testProvenance("oci")
		p.Signature.Status = status
		if status == SignatureUnknown {
			p.ReferrersUnavailable = "registry down"
		}
		return p
	}
	return detailFleet(contract.Owner{}, []*ContractRevision{
		{Key: "svc@1", Version: "1.0.0", Provenance: prov(SignatureVerified)},
		{Key: "svc@2", Version: "2.0.0", Provenance: prov(SignatureUnsigned)},
		{Key: "svc@3", Version: "3.0.0", Provenance: prov(SignatureUnknown)},
		{Key: "svc@4", Version: "4.0.0"},
		{Key: "svc@5", Version: "5.0.0", Provenance: prov(SignatureVerified)},
	}, []*TargetRecord{
		{Key: "t1", Name: "t1", ContractRevision: "svc@1"},
		{Key: "t2", Name: "t2", ContractRevision: "svc@2"},
		{Key: "t3", Name: "t3", ContractRevision: "svc@3"},
		{Key: "t4", Name: "t4", ContractRevision: "svc@4"},
	})
}

func TestServiceSummarySignatures(t *testing.T) {
	got := serviceDetailOf(t, signatureFleet()).Summary.Signatures
	if want := (SignatureTally{Verified: 1, Unsigned: 1, Unknown: 1, NotChecked: 1}); got != want {
		t.Errorf("signatures = %+v, want %+v", got, want)
	}
	var odd SignatureTally
	odd.add(&BundleProvenance{Signature: BundleSignature{Status: "weird"}})
	if odd.Unknown != 1 {
		t.Errorf("unrecognised status = %+v, want unknown", odd)
	}
	odd.add(&BundleProvenance{Signature: BundleSignature{Status: SignatureUnverified}})
	if odd.Unverified != 1 {
		t.Errorf("unverified status = %+v, want unverified", odd)
	}
}

func TestServiceDetailSignatures(t *testing.T) {
	got := serviceDetailOf(t, signatureFleet()).Signatures
	if got.Total != 4 || got.Count != 4 || got.Truncated {
		t.Fatalf("signatures = %+v", got)
	}
	for i, want := range []string{"svc@4", "svc@3", "svc@2", "svc@1"} {
		if got.Items[i].Revision.Key != want {
			t.Errorf("row %d = %s, want %s", i, got.Items[i].Revision.Key, want)
		}
	}
	if r := got.Items[0]; r.Checked || r.Signature != nil || r.CheckedAt != nil {
		t.Errorf("unchecked row = %+v", r)
	}
	if r := got.Items[1]; r.ReferrersUnavailable != "registry down" {
		t.Errorf("unknown row lost why its referrers are unavailable: %+v", r)
	}
	assertVerifiedRow(t, got.Items[3])
}

func assertVerifiedRow(t *testing.T, v RevisionSignature) {
	t.Helper()
	if !v.Checked || v.Signature == nil || v.Signature.Signer != "me@example.com" || v.Referrers != 1 || v.Source != "oci" || v.CheckedAt == nil || !v.CheckedAt.Equal(fixedNow()) {
		t.Errorf("verified row = %+v", v)
	}
}

func TestNewReferrersPreview(t *testing.T) {
	items := make([]OCIReferrer, 250)
	for i := range items {
		items[i] = OCIReferrer{ArtifactType: "t", Digest: fmt.Sprintf("sha256:%03d", 249-i)}
	}
	items[0].ArtifactType = strings.Repeat("a", 300)
	p := NewReferrersPreview(items)
	if p.Total != 250 || p.Count != MaxDetailPreview || !p.Truncated || p.Items[0].Digest != "sha256:000" || p.Items[199].Digest != "sha256:199" {
		t.Errorf("preview total %d count %d truncated %v first %s", p.Total, p.Count, p.Truncated, p.Items[0].Digest)
	}
	long := NewReferrersPreview(items[:1]).Items[0].ArtifactType
	if long != strings.Repeat("a", maxRuntimeValueLen)+"…" {
		t.Errorf("artifact type not capped: %d", len(long))
	}
	if empty := NewReferrersPreview(nil); empty.Items == nil || empty.Total != 0 {
		t.Errorf("empty = %+v", empty)
	}
}
