package evidenceingest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

const backupRef = "x.acme.io/backup"

func extContract() contract.Contract {
	c := ordersContract()
	c.Capabilities = []contract.Capability{
		{Type: contract.CapabilityHealth},
		{Type: contract.CapabilityExtension, Ref: backupRef},
		{Type: contract.CapabilityExtension, Ref: "x.acme.io/scan"},
	}
	return c
}

func extProv() evidence.Provenance {
	return evidence.Provenance{Collector: "acme-backup", DetectedAt: fixedNow().Add(-time.Minute)}
}

func capObs(name string, present bool) evidence.Observation {
	return evidence.NewCapabilityObserved(evidence.SubjectRef{Kind: "capability", Name: name}, present, extProv())
}

func extSet(subject string, observed time.Time, obs ...evidence.Observation) evidence.EvidenceSet {
	return evidence.EvidenceSet{
		Subject:      evidence.SubjectRef{Kind: "service", Name: subject},
		ContractRef:  ordersRef,
		Source:       "acme-backup",
		ObservedAt:   observed,
		Observations: obs,
	}
}

// acceptExtension signs set as producer acme-backup with a key granted refs
// (and CapabilityObserved only when refs is non-empty) and accepts it.
func acceptExtension(t *testing.T, refs []string, set evidence.EvidenceSet) (Record, error) {
	t.Helper()
	pub, priv := testKeypair()
	entry := evidenceenvelope.TrustEntry{PublicKey: pub, ProducerID: "acme-backup", ExtensionRefs: refs}
	if len(refs) > 0 {
		entry.AllowedObservationKinds = []string{string(evidence.CapabilityObserved)}
	}
	a := NewAcceptor(evidenceenvelope.MapTrustStore{keyID: entry}, fakeResolver{c: extContract()}, NewMemoryStore(), fixedNow)
	signed, err := evidenceenvelope.Sign(evidenceenvelope.Envelope{
		ID: "x1", Producer: evidenceenvelope.Producer{ID: "acme-backup", KeyID: keyID}, Sequence: 1,
		IssuedAt: fixedNow().Add(-time.Hour), ExpiresAt: fixedNow().Add(time.Hour), EvidenceSet: set,
	}, priv)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	return a.Accept(context.Background(), data)
}

func TestAcceptExtensionReportTable(t *testing.T) {
	before := fixedNow().Add(-time.Minute)
	const subject = "ghcr.io/a/orders/production/extension/backup"
	granted := []string{backupRef}
	failed, _ := evidence.NewUnobserved(evidence.CapabilityObserved, evidence.SubjectRef{Kind: "capability", Name: backupRef}, evidence.Failed, extProv())
	workload := evidence.NewWorkloadObserved(evidence.SubjectRef{Kind: "service", Name: "orders"}, "service", extProv())
	cases := []struct {
		name       string
		refs       []string
		set        evidence.EvidenceSet
		want       error
		compliance string
	}{
		{"present", granted, extSet(subject, before, capObs(backupRef, true)), nil, finding.StatusCompliant},
		{"absent", granted, extSet(subject, before, capObs(backupRef, false)), nil, finding.StatusNonCompliant},
		{"failed outcome", granted, extSet(subject, before, failed), nil, finding.StatusUnknown},
		{"undeclared ref", []string{backupRef, "x.acme.io/restore"}, extSet(subject, before, capObs("x.acme.io/restore", true)), ErrInvalidEvidence, ""},
		{"repeated ref", granted, extSet(subject, before, capObs(backupRef, true), capObs(backupRef, false)), ErrInvalidEvidence, ""},
		{"subject in another repository", granted, extSet("ghcr.io/b/orders/production/extension/backup", before, capObs(backupRef, true)), ErrInvalidEvidence, ""},
		{"subject without the extension segment", granted, extSet("ghcr.io/a/orders/production/backup", before, capObs(backupRef, true)), ErrInvalidEvidence, ""},
		{"observedAt an hour after accept", granted, extSet(subject, fixedNow().Add(time.Hour), capObs(backupRef, true)), ErrInvalidEvidence, ""},
		{"key without the ref", nil, extSet(subject, before, capObs(backupRef, true)), evidenceenvelope.ErrExtensionNotAllowed, ""},
		{"key granted another ref", []string{"x.acme.io/scan"}, extSet(subject, before, capObs(backupRef, true)), evidenceenvelope.ErrExtensionNotAllowed, ""},
		{"mixed with a runtime kind", granted, extSet(subject, before, capObs(backupRef, true), workload), evidenceenvelope.ErrKindNotAllowed, ""},
		{"mixed with a provider kind", granted, extSet(subject, before, capObs(backupRef, true), provisioned("configuration", "db", "production")), evidenceenvelope.ErrKindNotAllowed, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := acceptExtension(t, tc.refs, tc.set)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Accept = %v, want %v", err, tc.want)
			}
			if err != nil {
				return
			}
			if rec.Compliance != tc.compliance || rec.Coverage.Required != 1 || len(rec.Coverage.Unverified) != 0 {
				t.Errorf("record = %s %+v, want %s judging only the named extension", rec.Compliance, rec.Coverage, tc.compliance)
			}
		})
	}
}

// TestAcceptExtensionNamesAuthenticatedProducer: the finding names the producer
// the key authenticated, whatever source the set declares, and the stored
// envelope keeps the source it was signed with.
func TestAcceptExtensionNamesAuthenticatedProducer(t *testing.T) {
	set := extSet("ghcr.io/a/orders/production/extension/backup", fixedNow().Add(-time.Minute), capObs(backupRef, false))
	set.Source = "edge-eu-west"
	rec, err := acceptExtension(t, []string{backupRef}, set)
	if err != nil {
		t.Fatal(err)
	}
	want := `extension capability "x.acme.io/backup" is reported absent by acme-backup`
	if len(rec.Findings) != 1 || rec.Findings[0].Message != want || len(rec.Findings[0].EvidenceRefs) != 1 || rec.Findings[0].EvidenceRefs[0].Source != "acme-backup" {
		t.Fatalf("findings = %+v, want one naming acme-backup", rec.Findings)
	}
	if rec.Envelope.EvidenceSet.Source != "edge-eu-west" {
		t.Errorf("stored envelope source = %q, want the signed edge-eu-west", rec.Envelope.EvidenceSet.Source)
	}
}

func TestIsExtensionReport(t *testing.T) {
	workload := evidence.NewWorkloadObserved(evidence.SubjectRef{Kind: "service", Name: "orders"}, "service", extProv())
	cases := []struct {
		name string
		obs  []evidence.Observation
		want bool
	}{
		{"empty set", nil, false},
		{"all extension", []evidence.Observation{capObs(backupRef, true), capObs("x.acme.io/scan", false)}, true},
		{"mixed", []evidence.Observation{capObs(backupRef, true), workload}, false},
		{"cap1 only", []evidence.Observation{capObs("cap1", true)}, false},
	}
	for _, tc := range cases {
		if got := isExtensionReport(evidence.EvidenceSet{Observations: tc.obs}); got != tc.want {
			t.Errorf("%s: isExtensionReport = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestHasInnerSegment(t *testing.T) {
	cases := []struct {
		rest, seg string
		want      bool
	}{
		{"production/extension/backup", "extension", true},
		{"production/usage/configuration/db", "usage", true},
		{"production/usage/configuration/db", "extension", false},
		{"extension/backup", "extension", false},
		{"production/backup/extension", "extension", false},
		{"production/extension", "extension", false},
	}
	for _, tc := range cases {
		if got := hasInnerSegment(tc.rest, tc.seg); got != tc.want {
			t.Errorf("hasInnerSegment(%q, %q) = %v, want %v", tc.rest, tc.seg, got, tc.want)
		}
	}
}
