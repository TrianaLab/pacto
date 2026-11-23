package evidenceingest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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

// extRecord is an accepted extension record about backupRef (with details) and
// x.acme.io/scan (Failed), accepted in cluster.
func extRecord(cluster string) Record {
	present := capObs(backupRef, true)
	present.Value = json.RawMessage(`{"present":true,"details":{"lastBackup":"2026-11-22T03:00:00Z"}}`)
	failed, _ := evidence.NewUnobserved(evidence.CapabilityObserved, evidence.SubjectRef{Kind: "capability", Name: "x.acme.io/scan"}, evidence.Failed, extProv())
	var env evidenceenvelope.Envelope
	env.Producer = evidenceenvelope.Producer{ID: "acme-backup", KeyID: "acme-backup-2026"}
	env.EvidenceSet = extSet("ghcr.io/a/orders/production/extension/backup", fixedNow().Add(-time.Minute), present, failed)
	return Record{Envelope: env, AcceptedAt: fixedNow(), Service: "orders", Domain: "ghcr.io/a", Digest: "sha256:bb", Cluster: cluster}
}

func TestHandleTargetsProjectsExtensions(t *testing.T) {
	var body TargetsResponse
	if err := json.Unmarshal([]byte(targetsBody(t, []Record{extRecord("uid-a"), clusterRecord("payments", "")}, nil)), &body); err != nil {
		t.Fatal(err)
	}
	if body.SchemaVersion != TargetsSchemaVersionExtensions {
		t.Errorf("schema = %q, want v6 with an extension report", body.SchemaVersion)
	}
	if len(body.Targets) != 1 || body.Targets[0].Subject != "payments" {
		t.Errorf("an extension report is never a target: %+v", body.Targets)
	}
	if len(body.Extensions) != 2 {
		t.Fatalf("extensions = %+v, want one per observation", body.Extensions)
	}
	got := body.Extensions[0]
	if got.Present == nil || !*got.Present || string(got.Details) != `{"lastBackup":"2026-11-22T03:00:00Z"}` {
		t.Errorf("observed DTO = %+v", got)
	}
	got.Present, got.Details = nil, nil
	want := ExtensionDTO{Subject: "ghcr.io/a/orders/production/extension/backup", Service: "orders", Domain: "ghcr.io/a", Digest: "sha256:bb",
		ContractRef: ordersRef, Producer: "acme-backup", ProducerKeyID: "acme-backup-2026", Ref: backupRef, Outcome: "Observed",
		EvidenceAt: fixedNow().Add(-time.Minute), AcceptedAt: fixedNow(), Cluster: "uid-a"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DTO = %+v, want %+v", got, want)
	}
	if f := body.Extensions[1]; f.Ref != "x.acme.io/scan" || f.Outcome != "Failed" || f.Present != nil || f.Details != nil {
		t.Errorf("a non-Observed DTO carries no payload: %+v", f)
	}
}

// TestHandleTargetsExtensionsShareBudget: the extension DTOs share the targets
// budget and its truncation flag.
func TestHandleTargetsExtensionsShareBudget(t *testing.T) {
	h := NewHandler(NewAcceptor(nil, fakeResolver{}, healthStore{recs: []Record{extRecord(""), clusterRecord("payments", "")}, health: SourceHealth{Status: HealthReady}}, fixedNow), nil, nil)
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	for _, tc := range []struct{ budget, extensions int }{{0, 0}, {1, 1}, {2, 2}} {
		h.maxTargets = tc.budget
		var b TargetsResponse
		getJSON(t, srv.URL+TargetsPath, &b)
		if len(b.Extensions) != tc.extensions || len(b.Targets) != 0 || !b.Truncated {
			t.Errorf("budget %d: extensions %d targets %d truncated %v", tc.budget, len(b.Extensions), len(b.Targets), b.Truncated)
		}
	}
}

// TestHandleTargetsMixedRecordStaysTarget: a record stored before extension
// grants existed, mixing an extension observation with a runtime kind, still
// projects as a target.
func TestHandleTargetsMixedRecordStaysTarget(t *testing.T) {
	rec := extRecord("")
	rec.Envelope.EvidenceSet.Observations = []evidence.Observation{capObs(backupRef, true),
		evidence.NewWorkloadObserved(evidence.SubjectRef{Kind: "service", Name: "orders"}, "service", extProv())}
	body := targetsBody(t, []Record{rec}, nil)
	if strings.Contains(body, `"extensions"`) || !strings.Contains(body, `"schemaVersion":"`+TargetsSchemaVersion+`"`) ||
		!strings.Contains(body, `"subject":"ghcr.io/a/orders/production/extension/backup"`) {
		t.Errorf("a mixed record stays a v2 target: %s", body)
	}
}

func TestHandleTargetsWithoutExtensionsKeepsVersion(t *testing.T) {
	prov := Record{Service: "orders"}
	prov.Envelope.EvidenceSet = provisionedSet("ghcr.io/a/orders/production/db", fixedNow(), provisioned("configuration", "db", "production"))
	use := Record{Service: "orders"}
	use.Envelope.EvidenceSet = provisionedSet("ghcr.io/a/orders/production/usage/configuration/db", fixedNow(), usageObs("configuration", "db", "production"))
	for name, tc := range map[string]struct {
		recs []Record
		want string
	}{
		"targets only":          {[]Record{clusterRecord("old", "")}, TargetsSchemaVersion},
		"provisioning":          {[]Record{prov}, TargetsSchemaVersionProvisioning},
		"usage":                 {[]Record{prov, use}, TargetsSchemaVersionUsage},
		"cluster":               {[]Record{clusterRecord("here", "uid-a"), prov, use}, TargetsSchemaVersionCluster},
		"extension and cluster": {[]Record{clusterRecord("here", "uid-a"), extRecord("")}, TargetsSchemaVersionExtensions},
	} {
		body := targetsBody(t, tc.recs, nil)
		if !strings.Contains(body, `"schemaVersion":"`+tc.want+`"`) {
			t.Errorf("%s: body %s, want schema %s", name, body, tc.want)
		}
		if tc.want != TargetsSchemaVersionExtensions && strings.Contains(body, `"extensions"`) {
			t.Errorf("%s: a body with no extension report carries no extensions key: %s", name, body)
		}
	}
}
