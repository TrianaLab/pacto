package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// TestDemoObservedOnlyExternal builds a snapshot from the embedded bundles plus
// the demo's telemetry and cluster external uses, and checks what the dashboard
// shows: twilio is reached only through a trace (no contract declares it), and
// stripe is inferred from notification-worker's pod spec.
func TestDemoObservedOnlyExternal(t *testing.T) {
	src := bundlesFS(t)
	var revs []fleet.RawRevision
	for _, name := range src.names {
		for ver, e := range src.byName[name] {
			revs = append(revs, fleet.RawRevision{Bundle: e.bundle, RequestedRef: "oci://demo.pacto.local/" + name + ":" + ver,
				ResolvedRef: "oci://demo.pacto.local/" + name + "@sha256:" + e.hash})
		}
	}
	now := time.Now().UTC()
	snap, err := fleet.Build(t.Context(), fleet.BuildOptions{Now: func() time.Time { return now }, FreshnessWindow: 24 * time.Hour},
		fleet.NewMemorySource("local", "local", &fleet.Collection{Revisions: revs}),
		fleet.NewMemorySource("k8s-production", fleet.SourceKindKubernetes, &fleet.Collection{ExternalUses: demoExternalUses(now)}),
		fleet.NewMemorySource("otel-traces", "observation", &fleet.Collection{Observed: demoObservedEdges(now)}))
	if err != nil {
		t.Fatal(err)
	}
	observed, declared, inferred := workerExternalEdges(snap)
	if observed != 1 || declared != 0 || inferred != 1 {
		t.Fatalf("notification-worker edges: observed twilio %d, other twilio %d, inferred stripe %d; want 1, 0, 1", observed, declared, inferred)
	}
	rec := snap.Externals["twilio"]
	if rec == nil || len(rec.Sources) == 0 {
		t.Fatalf("twilio external missing or without sources: %+v", rec)
	}
	for _, s := range rec.Sources {
		if s.Kind != fleet.ExternalSourceObserved {
			t.Errorf("twilio source kind %q, want only observed", s.Kind)
		}
	}
}

// workerExternalEdges counts notification-worker's observed and other edges to
// twilio and its inferred edges to stripe.
func workerExternalEdges(snap *fleet.FleetSnapshot) (observed, declared, inferred int) {
	worker := fleet.NewServiceKey("notification-worker")
	for _, r := range snap.Relationships {
		switch {
		case r.FromService != worker:
		case r.ToExternal == "twilio" && r.Provenance == fleet.ProvenanceObserved:
			observed++
		case r.ToExternal == "twilio":
			declared++
		case r.ToExternal == "stripe" && r.Provenance == fleet.ProvenanceInferred:
			inferred++
		}
	}
	return observed, declared, inferred
}

// TestDemoNewsletterExternalsInferred: newsletter-service's folder names
// stripe through its chart values and its requirements.txt, and an SQS queue
// through its Terraform. The committed record holds both, the SDK major and
// the identity parts the folder does not give; the WASM fleet shows both as
// inferred edges from the repository, and no contract declares either.
func TestDemoNewsletterExternalsInferred(t *testing.T) {
	assertNewsletterRecordExternals(t)
	snap := demoFleet(t)
	for _, l := range snap.Limitations {
		if l.Code == fleet.LimitationExternalCatalogMismatch {
			t.Errorf("the demo records a catalog the snapshot lacks: %+v", l)
		}
	}
	got := newsletterExternalEdges(t, snap)
	want := map[fleet.ExternalKey]string{"stripe": "likely helmValues,sdk", "aws/sqs": "possible terraform"}
	if !maps.Equal(got, want) {
		t.Errorf("newsletter-service inferred external edges = %v, want %v", got, want)
	}
}

// newsletterExternalEdges is "<confidence> <source kinds>" of each edge from
// newsletter-service to stripe or aws/sqs; any edge that is not inferred, or
// a source that names no revision, fails the test.
func newsletterExternalEdges(t *testing.T, snap *fleet.FleetSnapshot) map[fleet.ExternalKey]string {
	t.Helper()
	svc := fleet.NewServiceKey("newsletter-service")
	got := map[fleet.ExternalKey]string{}
	for _, r := range snap.Relationships {
		if r.FromService != svc || (r.ToExternal != "stripe" && r.ToExternal != "aws/sqs") {
			continue
		}
		if r.Provenance != fleet.ProvenanceInferred {
			t.Errorf("%s edge provenance = %q, want only inferred", r.ToExternal, r.Provenance)
			continue
		}
		var kinds []string
		for _, s := range r.InferredSources {
			if s.Revision == "" {
				t.Errorf("%s source %+v names no revision", r.ToExternal, s)
			}
			kinds = append(kinds, s.Kind)
		}
		slices.Sort(kinds)
		got[r.ToExternal] = r.Confidence + " " + strings.Join(slices.Compact(kinds), ",")
	}
	return got
}

// assertNewsletterRecordExternals: the committed record holds stripe with its
// SDK major and aws/sqs with the identity parts the folder does not give.
func assertNewsletterRecordExternals(t *testing.T) {
	t.Helper()
	rec, err := contract.ReadInferredRecord(os.DirFS(filepath.Join("bundles", "newsletter-service")))
	if err != nil || rec == nil {
		t.Fatalf("committed record = %v, %v", rec, err)
	}
	ext := map[string]*contract.InferredExternal{}
	for _, f := range rec.Fields {
		if f.External != nil {
			ext[f.External.Identity] = f.External
		}
	}
	if s := ext["stripe"]; s == nil || s.Client == nil || s.Client.SDK != "stripe" || s.Client.Major == nil || *s.Client.Major != 11 {
		t.Errorf("stripe external = %+v, want client stripe major 11", s)
	}
	if q := ext["aws/sqs"]; q == nil || q.Resource == nil || !slices.Equal(q.Resource.Missing, []string{"account", "region"}) {
		t.Errorf("aws/sqs external = %+v, want resource missing account and region", q)
	}
}
