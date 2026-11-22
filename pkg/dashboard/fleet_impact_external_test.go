package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// externalFleetQuery: payments (shop) requires stripe and its inferred
// record names stripe-go v76, billing (shop) may use it, and vault (secret)
// is only seen calling erp.corp-legacy.com.
func externalFleetQuery(t *testing.T) *fleet.Query {
	t.Helper()
	stripe := func(required bool) contract.Dependency {
		return contract.Dependency{Name: "stripe", External: true, Version: "2024-06-20", Required: required}
	}
	gomod := []byte("module example.com/payments\n")
	sum := sha256.Sum256(gomod)
	major := 76
	rec := &contract.InferredRecord{Format: contract.InferredRecordFormat, Inputs: contract.InferredInputs{Pacto: "4.6.0", Discovery: "1",
		Sources: []contract.InferredSourceFile{{Path: "go.mod", Digest: "sha256:" + hex.EncodeToString(sum[:])}}},
		Fields: []contract.Inferred{{Field: "dependencies[stripe]", Value: "stripe", Source: "go.mod:3", Confidence: "possible",
			External: &contract.InferredExternal{Identity: "stripe", Sources: []contract.InferredExternalSource{{Kind: "sdk", Path: "go.mod", Line: 3}},
				Client: &contract.InferredClient{SDK: "stripe-go", Major: &major}}}}}
	fs := map[string]fstest.MapFS{"payments": {contract.InferredRecordFile: {Data: rec.Marshal()}, "go.mod": {Data: gomod}}}
	deps := map[string][]contract.Dependency{"payments": {stripe(true)}, "billing": {stripe(false)}}
	col := &fleet.Collection{Observed: []fleet.ObservedEdge{{From: "vault", To: "erp.corp-legacy.com", Count: 1}}}
	for name, ns := range map[string]string{"payments": "shop", "billing": "shop", "vault": "secret"} {
		digest := validDigest(name[:1])
		files := fs[name]
		if files == nil {
			files = fstest.MapFS{}
		}
		col.Revisions = append(col.Revisions, fleet.RawRevision{Digest: digest, Bundle: &contract.Bundle{Contract: &contract.Contract{PactoVersion: "2.5",
			Service: contract.Service{Name: name, Version: "1.0.0", Owner: contract.Owner{Team: name}}, Dependencies: deps[name]}, FS: files}})
		col.Targets = append(col.Targets, fleet.RawTarget{Scope: ns, Kind: fleet.SourceKindKubernetes, Name: name, Service: name, Digest: digest})
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// externalServer serves externalFleetQuery, restricted to the shop
// namespace when restrict is set.
func externalServer(t *testing.T, restrict bool) string {
	t.Helper()
	q := externalFleetQuery(t)
	o := Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }, Impact: noImpact(t)}
	if restrict {
		o.View = func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
			return q.Restrict(func(_, ns string) bool { return ns == "shop" }, nil), nil
		}
		o.Auth = &Auth{Login: true, Routes: http.NotFoundHandler(), Middleware: func(h http.Handler) http.Handler { return h },
			State: func(*http.Request) AuthState { return AuthState{SignedIn: true} }}
	}
	return serveOptions(t, o)
}

func TestFleetImpactExternalEvent(t *testing.T) {
	base := externalServer(t, false)
	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{Event: impact.EventChanged, ExternalKey: "stripe"}, http.StatusOK, &out)
	if out.Gate != impact.GateUnverified || out.Event == nil || out.Event.External != "stripe" || out.External == nil || out.External.Identity != "stripe" {
		t.Fatalf("answer = gate %s event %+v external %+v", out.Gate, out.Event, out.External)
	}
	if out.Pin == "" || out.Service.Kind != fleet.KindExternal || out.Service.Href == "" {
		t.Errorf("pin %q service %+v", out.Pin, out.Service)
	}
	if out.Consumers.Total != 2 || len(out.Consumers.Items) != 2 {
		t.Fatalf("consumers = %+v", out.Consumers)
	}
	for _, c := range out.Consumers.Items {
		if c.Effect != impact.EffectAttention || c.CompatibilityVerdict == impact.CompatibilityCompatible {
			t.Errorf("%s: effect %s verdict %s", c.Service.Label, c.Effect, c.CompatibilityVerdict)
		}
	}
}

func TestFleetImpactExternalUnavailable(t *testing.T) {
	base := externalServer(t, false)
	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{Event: impact.EventUnavailable, ExternalKey: "stripe"}, http.StatusOK, &out)
	if out.Gate != impact.GateUnverified || tally(out.Consumers.ByOutcome, impact.EffectBreaks) != 1 || tally(out.Consumers.ByOutcome, impact.EffectMayDegrade) != 1 {
		t.Errorf("gate %s byOutcome %+v", out.Gate, out.Consumers.ByOutcome)
	}
}

func TestFleetImpactExternalUnknownIs404(t *testing.T) {
	base := externalServer(t, false)
	code, body := call(t, http.MethodPost, base+"/api/fleet/impact", impactRequest{Event: impact.EventChanged, ExternalKey: "twilio"})
	if code != http.StatusNotFound || !strings.Contains(fmt.Sprint(body["detail"]), "no external system") {
		t.Errorf("unknown external = %d %v", code, body)
	}
}

// TestFleetImpactExternalHiddenIs404: an external only hidden consumers name
// answers exactly as one that does not exist.
func TestFleetImpactExternalHiddenIs404(t *testing.T) {
	if externalFleetQuery(t).Snapshot().Externals["host/corp-legacy.com"] == nil {
		t.Fatal("the fixture holds no host/corp-legacy.com record")
	}
	base := externalServer(t, true)
	code, hidden := call(t, http.MethodPost, base+"/api/fleet/impact", impactRequest{Event: impact.EventChanged, ExternalKey: "host/corp-legacy.com"})
	_, missing := call(t, http.MethodPost, base+"/api/fleet/impact", impactRequest{Event: impact.EventChanged, ExternalKey: "host/no-such.com"})
	if code != http.StatusNotFound || hidden["detail"] != missing["detail"] || hidden["status"] != missing["status"] {
		t.Errorf("hidden %v and missing %v must answer the same 404", hidden, missing)
	}
	code, _ = call(t, http.MethodPost, base+"/api/fleet/impact", impactRequest{Event: impact.EventChanged, ExternalKey: "stripe"})
	if code != http.StatusOK {
		t.Errorf("a visible external answered %d", code)
	}
}

func TestFleetImpactExternalRequestErrors(t *testing.T) {
	base := externalServer(t, false)
	for _, tc := range []struct {
		name string
		req  impactRequest
		want string
	}{
		{"no event", impactRequest{ExternalKey: "stripe"}, "read only with event changed or unavailable"},
		{"with serviceKey", impactRequest{Event: impact.EventChanged, ExternalKey: "stripe", ServiceKey: "payments"}, "exactly one of"},
		{"with offeringKey", impactRequest{Event: impact.EventUnavailable, ExternalKey: "stripe", OfferingKey: "db"}, "exactly one of"},
		{"changed of a service", impactRequest{Event: impact.EventChanged, ServiceKey: "payments"}, "send externalKey"},
		{"removed", impactRequest{Event: impact.EventRemoved, ExternalKey: "stripe"}, "changed or unavailable"},
		{"regressed", impactRequest{Event: impact.EventRegressed, ExternalKey: "stripe", Revision: "1.0.0"}, "changed or unavailable"},
		{"between revisions", impactRequest{Event: impact.EventChanged, ExternalKey: "stripe", FromRevisionKey: "a", ToRevisionKey: "b"}, "without fromRevisionKey"},
	} {
		code, body := call(t, http.MethodPost, base+"/api/fleet/impact", tc.req)
		if code != http.StatusUnprocessableEntity || !strings.Contains(fmt.Sprint(body), tc.want) {
			t.Errorf("%s = %d %v, want 422 naming %q", tc.name, code, body, tc.want)
		}
	}
}

// TestFleetImpactExternalNeverNoService: an external request never reads
// the service list, so a known external with no service of its name answers.
func TestFleetImpactExternalNeverNoService(t *testing.T) {
	base := externalServer(t, false)
	code, body := call(t, http.MethodPost, base+"/api/fleet/impact", impactRequest{Event: impact.EventUnavailable, ExternalKey: "stripe"})
	if code != http.StatusOK || strings.Contains(fmt.Sprint(body), "no service") {
		t.Errorf("external = %d %v", code, body)
	}
}

func TestFleetImpactOutcomeAttention(t *testing.T) {
	base := externalServer(t, false)
	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{Event: impact.EventChanged, ExternalKey: "stripe", Outcome: impact.EffectAttention}, http.StatusOK, &out)
	if out.Consumers.Outcome != impact.EffectAttention || out.Consumers.Matching != 2 || tally(out.Consumers.ByOutcome, impact.EffectAttention) != 2 {
		t.Errorf("consumers = %+v", out.Consumers)
	}
}

func TestFleetImpactExternalRowsCarrySourcesClient(t *testing.T) {
	base := externalServer(t, false)
	var out ProductImpact
	postJSON(t, base+"/api/fleet/impact", impactRequest{Event: impact.EventChanged, ExternalKey: "stripe"}, http.StatusOK, &out)
	rows := map[string]ProductImpactConsumer{}
	for _, c := range out.Consumers.Items {
		rows[c.Service.Label] = c
	}
	pay, bill := rows["payments"], rows["billing"]
	if !slices.Contains(pay.Sources, "declared") || pay.Client == nil || pay.Client.SDK != "stripe-go" || pay.Client.Major != 76 || pay.Client.Confidence != "possible" {
		t.Errorf("payments = sources %v client %+v", pay.Sources, pay.Client)
	}
	if !slices.Contains(bill.Sources, "declared") || bill.Client != nil {
		t.Errorf("billing = sources %v client %+v", bill.Sources, bill.Client)
	}
}
