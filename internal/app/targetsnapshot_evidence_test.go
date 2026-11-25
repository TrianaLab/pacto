package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/fleetsrc"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// evidenceBody is an Evidence Server targets body holding one accepted target
// of service, reported by a pacto agent.
func evidenceBody(t *testing.T, service string, truncated bool) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(evidenceingest.TargetsResponse{SchemaVersion: evidenceingest.TargetsSchemaVersionCluster, GeneratedAt: snapNow,
		Health: evidenceingest.SourceHealth{Status: evidenceingest.HealthReady, Subjects: 1}, Truncated: truncated,
		Targets: []evidenceingest.TargetDTO{{Subject: service + "-edge", Service: service, Producer: "pacto-agent", Compliance: "Compliant",
			ContractRef: "oci://reg.example/acme/" + service + "@sha256:" + strings.Repeat("c", 64), EvidenceAt: snapNow, AcceptedAt: snapNow}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// evidenceOnlySnapshot is cluster's snapshot with no Pacto and no workload,
// whose evidence section holds one target of service; evidence is the
// completeness of that section.
func evidenceOnlySnapshot(t *testing.T, cluster, service string, evidence fleet.Completeness) delivery.TargetSnapshot {
	asOf := snapNow.Format(time.RFC3339)
	return delivery.TargetSnapshot{Cluster: cluster, AsOf: asOf, Completeness: evidence, Publisher: "pacto-system/recorder",
		Pactos: []delivery.PactoView{}, Workloads: []fleet.RawInferredService{}, Evidence: evidenceBody(t, service, false),
		Sources: []delivery.SnapshotSource{
			{Kind: fleet.SourceKindKubernetes, AsOf: asOf, Completeness: fleet.CompletenessComplete},
			{Kind: fleet.SourceKindKubernetesWorkloads, AsOf: asOf, Completeness: fleet.CompletenessComplete},
			{Kind: fleet.SourceKindEvidenceHTTP, AsOf: asOf, Completeness: evidence}}}
}

// TestImpactSnapshotEvidenceOnlyTargetGivesVerdict: the only target of the
// consumer is one an external producer reported to the Evidence Server, so
// the snapshot's evidence section alone gives impact its verdict: blocked for
// a consumer the change breaks, and for one it does not the gate a
// --target-state source running it gives.
func TestImpactSnapshotEvidenceOnlyTargetGivesVerdict(t *testing.T) {
	t.Parallel()
	for _, compat := range []string{"^1.0.0", "^2.0.0"} {
		t.Run(compat, func(t *testing.T) {
			e := newSnapEnv(t)
			e.push(t, e.key, snapA, evidenceOnlySnapshot(t, snapA, "webapp", fleet.CompletenessComplete))
			root, targetState := testutil.ImpactFleet(t, compat)
			oldDir, newDir := t.TempDir(), t.TempDir()
			testutil.WriteOrders(t, oldDir, "1.0.0", "service")
			testutil.WriteOrders(t, newDir, "2.0.0", "job")
			run := func(fo FleetOptions) *impact.Result {
				fo.LocalRoots, fo.Now = []string{root}, func() time.Time { return snapNow }
				res, err := ordersService(t).Impact(t.Context(), ImpactOptions{OldPath: oldDir, NewPath: newDir, Fleet: fo})
				if err != nil {
					t.Fatal(err)
				}
				return res
			}
			base := run(FleetOptions{TargetStateFiles: []string{targetState}})
			res := run(FleetOptions{TargetSnapshot: []string{"oci://" + e.repo}, TargetSnapshotTrustFile: trustFile(t, e.scoped("rec", snapA))})
			if res.Gate != base.Gate || res.Completeness != fleet.CompletenessComplete {
				t.Fatalf("gate %s (%s), completeness %s, want %s (%s); limitations %+v", res.Gate, res.GateReason, res.Completeness, base.Gate, base.GateReason, res.Limitations)
			}
			if blocked := compat == "^1.0.0"; blocked != (res.Gate == impact.GateBlocked) || blocked && (len(res.Consumers) != 1 || res.Consumers[0].Service != "webapp") {
				t.Fatalf("gate %s, consumers %+v", res.Gate, res.Consumers)
			}
		})
	}
}

// TestSnapshotEvidenceSectionPartialKeepsOtherSections: an evidence section
// read partly makes its source and the answer partial; the Pacto section's
// targets still answer and its source stays available.
func TestSnapshotEvidenceSectionPartialKeepsOtherSections(t *testing.T) {
	t.Parallel()
	e := newSnapEnv(t)
	s := ledgerSnapshot(snapA, snapNow)
	part := evidenceOnlySnapshot(t, snapA, "webapp", fleet.CompletenessPartial)
	s.Completeness, s.Sources = fleet.CompletenessPartial, part.Sources
	s.Sources[2].Limitations = []fleet.Limitation{{Code: fleet.LimitationSourcePartial, Message: "the Evidence Server answered HTTP 500"}}
	e.push(t, e.key, snapA, s) // no evidence body: the read failed
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	id := "targets-" + snapA
	if st := stateOf(t, snap, id+evidenceSuffix); st.Status != fleet.SourcePartial || st.Kind != fleet.SourceKindEvidenceHTTP ||
		!hasLim(snap, id+evidenceSuffix, fleet.LimitationSourcePartial) {
		t.Fatalf("evidence source %+v, limitations %+v", st, snap.Limitations)
	}
	if st := stateOf(t, snap, id); st.Status != fleet.SourceAvailable || st.TargetCount != 1 || snap.Completeness == fleet.CompletenessComplete {
		t.Fatalf("pacto source %+v, completeness %s", st, snap.Completeness)
	}
}

// TestSnapshotUnverifiedEvidenceNeverComplete: with no trust config every
// section of the snapshot reads partial, evidence included.
func TestSnapshotUnverifiedEvidenceNeverComplete(t *testing.T) {
	t.Parallel()
	e := newSnapEnv(t)
	e.push(t, e.key, snapA, evidenceOnlySnapshot(t, snapA, "webapp", fleet.CompletenessComplete))
	snap := readSnap(t, &Service{}, snapOpts("", "oci://"+e.repo))
	id := "targets-" + snapA + evidenceSuffix
	if st := stateOf(t, snap, id); st.Status != fleet.SourcePartial || st.Signer != "" || !hasLim(snap, id, fleet.LimitationTargetSnapshotUnverified) {
		t.Fatalf("evidence source %+v, limitations %+v", st, snap.Limitations)
	}
	if snap.Completeness == fleet.CompletenessComplete {
		t.Fatal("an unverified snapshot read complete")
	}
}

// TestSnapshotEvidenceUnscopedAndStale: a key not scoped to the cluster marks
// every section; an evidence section older than the freshness bound is stale.
func TestSnapshotEvidenceUnscopedAndStale(t *testing.T) {
	t.Parallel()
	e := newSnapEnv(t)
	s := evidenceOnlySnapshot(t, snapA, "webapp", fleet.CompletenessComplete)
	s.Sources[2].AsOf = snapNow.Add(-time.Hour).Format(time.RFC3339)
	e.push(t, e.key, snapA, s)
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, delivery.TrustedKey{Name: "any", PublicKey: e.pub}), "oci://"+e.repo))
	id := "targets-" + snapA
	for _, sid := range []string{id, id + workloadsSuffix, id + evidenceSuffix} {
		if !hasLim(snap, sid, fleet.LimitationTargetSnapshotUnscoped) || stateOf(t, snap, sid).Signer != "any" {
			t.Errorf("%s: limitations %+v", sid, snap.Limitations)
		}
	}
	if st := stateOf(t, snap, id+evidenceSuffix); st.Status != fleet.SourceStale || !hasLim(snap, id+evidenceSuffix, fleet.LimitationTargetSnapshotStale) {
		t.Fatalf("evidence source %+v", st)
	}
}

// TestSnapshotEvidenceSectionInvalidUnused: an evidence body the mapping
// refuses is not used, and its source alone is unavailable; a snapshot that
// names sections but leaves one out reads that one partial.
func TestSnapshotEvidenceSectionInvalidUnused(t *testing.T) {
	t.Parallel()
	e := newSnapEnv(t)
	s := evidenceOnlySnapshot(t, snapA, "webapp", fleet.CompletenessComplete)
	s.Evidence = json.RawMessage(`{"schemaVersion":"pacto.dev/evidence-source/v1"}`)
	s.Sources = s.Sources[1:]
	e.push(t, e.key, snapA, s)
	snap := readSnap(t, &Service{}, snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	id := "targets-" + snapA
	wantUnused(t, snap, id+evidenceSuffix, fleet.LimitationTargetSnapshotInvalid)
	if st := stateOf(t, snap, id); st.Status != fleet.SourcePartial {
		t.Fatalf("a section left out read %+v", st)
	}
}

// TestEvidenceMappingSharedWithEvidenceHTTPSource: one targets body read by
// an --evidence-url source and from a snapshot yields the same targets.
func TestEvidenceMappingSharedWithEvidenceHTTPSource(t *testing.T) {
	t.Parallel()
	body := evidenceBody(t, "webapp", true)
	srv := probedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == evidenceingest.TargetsPath {
			_, _ = w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	viaHTTP, err := fleetsrc.NewEvidenceHTTPSource("ev", srv.URL).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e := newSnapEnv(t)
	s := evidenceOnlySnapshot(t, snapA, "webapp", fleet.CompletenessComplete)
	s.Evidence = body
	e.push(t, e.key, snapA, s)
	srcs, err := (&Service{}).targetSnapshotSources(t.Context(), snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	if err != nil || len(srcs) != 3 {
		t.Fatalf("sources %d, %v", len(srcs), err)
	}
	viaSnapshot, _ := srcs[2].Collect(t.Context())
	if len(viaHTTP.Targets) != 1 || !reflect.DeepEqual(viaHTTP.Targets, viaSnapshot.Targets) {
		t.Fatalf("http %+v\nsnapshot %+v", viaHTTP.Targets, viaSnapshot.Targets)
	}
	if len(viaSnapshot.Limitations) == 0 || viaSnapshot.Limitations[0].Message != viaHTTP.Limitations[0].Message {
		t.Fatalf("limitations http %+v, snapshot %+v", viaHTTP.Limitations, viaSnapshot.Limitations)
	}
}

// TestCISnapshotEvidenceOnlyIsConsumerSource: pacto ci reads the evidence
// section as a consumer source that answered, so the row has a verdict.
func TestCISnapshotEvidenceOnlyIsConsumerSource(t *testing.T) {
	t.Parallel()
	e := newCIRepo(t)
	e.breakingPayments(t)
	e.write(t, map[string]string{"webapp/pacto.yaml": consumerOf("webapp", e.host)})
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	repo := e.host + "/acme/" + delivery.TargetSnapshotRepository
	s := evidenceOnlySnapshot(t, snapA, "webapp", fleet.CompletenessComplete)
	now := time.Now().UTC().Format(time.RFC3339)
	s.AsOf = now
	for i := range s.Sources {
		s.Sources[i].AsOf = now
	}
	pushAt(t, repo, key, snapA, s)
	trust := trustFile(t, delivery.TrustedKey{Name: "rec", PublicKey: pemOf(t, key), Streams: []string{snapA + "/*/*"}})
	p, _ := e.planWith(t, FleetOptions{TargetSnapshot: []string{"oci://" + repo}, TargetSnapshotTrustFile: trust})
	if r := row(t, p, "payments"); r.Impact.Gate != impact.GateBlocked || strings.Contains(r.Impact.Text, "no consumer source") {
		t.Fatalf("row %+v, snapshot %+v", r, p.Snapshot)
	}
}

// TestSnapshotEvidenceCarriesExtensionReports: a v6 evidence section carries
// a producer's extension report through the target snapshot exactly as the
// evidence-http source reads it, and the record keeps the cluster that
// accepted it.
func TestSnapshotEvidenceCarriesExtensionReports(t *testing.T) {
	t.Parallel()
	present := true
	body, err := json.Marshal(evidenceingest.TargetsResponse{SchemaVersion: evidenceingest.TargetsSchemaVersionExtensions, GeneratedAt: snapNow,
		Health: evidenceingest.SourceHealth{Status: evidenceingest.HealthReady, Subjects: 1}, Targets: []evidenceingest.TargetDTO{},
		Cluster: &evidenceingest.ClusterDTO{ID: snapA, Name: "a"},
		Extensions: []evidenceingest.ExtensionDTO{{Subject: "reg.example/acme/webapp/prod/extension/backup", Service: "webapp",
			ContractRef: "oci://reg.example/acme/webapp@sha256:" + strings.Repeat("c", 64), Producer: "acme-backup", ProducerKeyID: "acme-backup-2026",
			Ref: "x.acme.io/backup", Outcome: string(evidence.Observed), Present: &present, EvidenceAt: snapNow, AcceptedAt: snapNow, Cluster: snapA}}})
	if err != nil {
		t.Fatal(err)
	}
	viaHTTP := collectEvidenceHTTP(t, body)
	e := newSnapEnv(t)
	s := evidenceOnlySnapshot(t, snapA, "webapp", fleet.CompletenessComplete)
	s.Evidence = body
	e.push(t, e.key, snapA, s)
	srcs, err := (&Service{}).targetSnapshotSources(t.Context(), snapOpts(trustFile(t, e.scoped("rec", snapA)), "oci://"+e.repo))
	if err != nil || len(srcs) != 3 {
		t.Fatalf("sources %d, %v", len(srcs), err)
	}
	viaSnapshot, _ := srcs[2].Collect(t.Context())
	if len(viaSnapshot.Extensions) != 1 || !reflect.DeepEqual(viaHTTP.Extensions, viaSnapshot.Extensions) {
		t.Fatalf("http %+v\nsnapshot %+v", viaHTTP.Extensions, viaSnapshot.Extensions)
	}
	if x := viaSnapshot.Extensions[0]; x.Cluster == nil || x.Cluster.ID != snapA || !x.Verified || x.Ref != "x.acme.io/backup" ||
		x.Subject != "reg.example/acme/webapp/prod/extension/backup" {
		t.Fatalf("extension %+v", x)
	}
	assertCarriedRecordUnmatched(t, srcs)
}

// collectEvidenceHTTP serves body as an Evidence Server targets response and
// reads it back through the evidence-http source.
func collectEvidenceHTTP(t *testing.T, body []byte) *fleet.Collection {
	t.Helper()
	srv := probedServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	data, err := fleetsrc.NewEvidenceHTTPSource("ev", srv.URL).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// assertCarriedRecordUnmatched builds a snapshot from srcs and checks the
// record carried by srcs[2] is accepted as valid and reaches matching.
func assertCarriedRecordUnmatched(t *testing.T, srcs []fleet.Source) {
	t.Helper()
	// Build accepts the carried record as valid: no revision declares the ref,
	// so it is dropped as unmatched, never as invalid.
	snap, err := fleet.Build(t.Context(), fleet.BuildOptions{}, srcs...)
	if err != nil {
		t.Fatal(err)
	}
	var unmatched bool
	for _, l := range snap.Limitations {
		if l.Source == srcs[2].ID() && l.Code == fleet.LimitationSourceRecordInvalid {
			t.Errorf("the carried record is invalid: %+v", l)
		}
		unmatched = unmatched || (l.Source == srcs[2].ID() && l.Code == fleet.LimitationExtensionUnmatched)
	}
	if !unmatched {
		t.Errorf("the carried record never reached matching: %+v", snap.Limitations)
	}
}
