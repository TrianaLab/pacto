package evidenceingest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/evidenceenvelope"
)

var usageGrant = []string{string(evidence.ResourceUsage)}

func usageObs(kind, name, env string) evidence.Observation {
	return evidence.NewResourceUsage(evidence.SubjectRef{Kind: kind, Name: name},
		evidence.UsageObservation{Provider: "acme-db", ExternalRef: "orders-db", Environment: env,
			Metrics: []evidence.UsageMetric{{Name: "storage-used", Value: 31, Unit: "Gi"}},
			Cost:    &evidence.UsageCost{Amount: "212.40", Currency: "USD", Period: "P30D"}},
		evidence.Provenance{Collector: "platform-adapter", DetectedAt: fixedNow().Add(-time.Minute)})
}

// acceptWith signs set with a key granted kinds and scoped to scope, and
// accepts it against a consumer whose db configuration refs postgresql.
func acceptWith(t *testing.T, kinds, scope []string, set evidence.EvidenceSet) (Record, error) {
	t.Helper()
	pub, priv := testKeypair()
	trust := evidenceenvelope.MapTrustStore{keyID: evidenceenvelope.TrustEntry{PublicKey: pub, ProducerID: "env-a",
		AllowedObservationKinds: kinds, ProviderRepos: scope}}
	a := NewAcceptor(trust, fakeResolver{c: scopedContract("oci://ghcr.io/acme/platform/postgresql:16")}, NewMemoryStore(), fixedNow)
	return a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 1, "e1", set))
}

func TestIngestUsageBoundToDeclaredUse(t *testing.T) {
	before := fixedNow().Add(-time.Minute)
	cases := []struct {
		name string
		set  evidence.EvidenceSet
		want error
	}{
		{"configuration ref", provisionedSet("ghcr.io/a/orders/production/usage/configuration/db", before, usageObs("configuration", "db", "production")), nil},
		{"dependency", provisionedSet("ghcr.io/a/orders/default/usage/dependency/postgresql", before, usageObs("dependency", "postgresql", "")), nil},
		{"unknown configuration", provisionedSet("ghcr.io/a/orders/production/usage/configuration/cache", before, usageObs("configuration", "cache", "production")), ErrInvalidEvidence},
		{"unknown dependency", provisionedSet("ghcr.io/a/orders/default/usage/dependency/redis", before, usageObs("dependency", "redis", "")), ErrInvalidEvidence},
		{"subject in another repository", provisionedSet("ghcr.io/b/orders/production/usage/configuration/db", before, usageObs("configuration", "db", "production")), ErrInvalidEvidence},
		{"dated an hour after accept", provisionedSet("ghcr.io/a/orders/production/usage/configuration/db", fixedNow().Add(time.Hour), usageObs("configuration", "db", "production")), ErrInvalidEvidence},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := acceptWith(t, usageGrant, nil, tc.set)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Accept = %v, want %v", err, tc.want)
			}
			if err == nil && (rec.Compliance != "" || len(rec.Findings) != 0) {
				t.Errorf("a usage record claims no compliance: %+v", rec)
			}
		})
	}
}

func TestIngestUsageUndeclaredEnvironmentRejected(t *testing.T) {
	before := fixedNow().Add(-time.Minute)
	if _, err := acceptWith(t, usageGrant, nil, provisionedSet("ghcr.io/a/orders/staging/usage/configuration/db", before, usageObs("configuration", "db", "staging"))); !errors.Is(err, ErrInvalidEvidence) {
		t.Errorf("payload environment: %v, want ErrInvalidEvidence", err)
	}
	unobserved, _ := evidence.NewUnobserved(evidence.ResourceUsage, evidence.SubjectRef{Kind: "configuration", Name: "db", Environment: "staging"},
		evidence.Failed, evidence.Provenance{Collector: "platform-adapter", DetectedAt: before})
	if _, err := acceptWith(t, usageGrant, nil, provisionedSet("ghcr.io/a/orders/staging/usage/configuration/db", before, unobserved)); !errors.Is(err, ErrInvalidEvidence) {
		t.Errorf("subject environment: %v, want ErrInvalidEvidence", err)
	}
}

func TestIngestRejectsMixedKinds(t *testing.T) {
	before := fixedNow().Add(-time.Minute)
	both := []string{string(evidence.ResourceProvisioned), string(evidence.ResourceUsage), string(evidence.WorkloadObserved)}
	workload := evidence.NewWorkloadObserved(evidence.SubjectRef{Kind: "service", Name: "orders"}, "service",
		evidence.Provenance{Collector: "platform-adapter", DetectedAt: before})
	for name, obs := range map[string][]evidence.Observation{
		"provisioning then usage": {provisioned("configuration", "db", "production"), usageObs("configuration", "db", "production")},
		"usage then provisioning": {usageObs("configuration", "db", "production"), provisioned("configuration", "db", "production")},
		"runtime then usage":      {workload, usageObs("configuration", "db", "production")},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := acceptWith(t, both, nil, provisionedSet("ghcr.io/a/orders/production/usage/configuration/db", before, obs...))
			if !errors.Is(err, ErrInvalidEvidence) || !strings.Contains(err.Error(), "only one provider-reported kind") {
				t.Errorf("Accept = %v, want the one-kind rejection", err)
			}
		})
	}
}

func TestIngestUsageSubjectNeedsUsageSegment(t *testing.T) {
	before := fixedNow().Add(-time.Minute)
	for _, subject := range []string{"ghcr.io/a/orders/production/configuration/db", "ghcr.io/a/orders/production/usages/db", "ghcr.io/a/orders/usage",
		"ghcr.io/a/orders/production/usage", "ghcr.io/a/orders/usage/configuration/db"} {
		_, err := acceptWith(t, usageGrant, nil, provisionedSet(subject, before, usageObs("configuration", "db", "production")))
		if !errors.Is(err, ErrInvalidEvidence) || !strings.Contains(err.Error(), "/usage/") {
			t.Errorf("%s: Accept = %v, want the usage-segment rejection", subject, err)
		}
	}
	// The segment is a usage rule only: a provisioning subject may name anything under the repository.
	if _, err := acceptWith(t, []string{string(evidence.ResourceProvisioned)}, nil,
		provisionedSet("ghcr.io/a/orders/production/configuration/db", before, provisioned("configuration", "db", "production"))); err != nil {
		t.Errorf("provisioning subject: %v", err)
	}
}

func TestIngestUsageForeignProviderRepoRejected(t *testing.T) {
	pg := []string{"ghcr.io/acme/platform/postgresql"}
	set := func(kind, name string) evidence.EvidenceSet {
		return provisionedSet("ghcr.io/a/orders/production/usage/"+kind+"/"+name, fixedNow().Add(-time.Minute), usageObs(kind, name, "production"))
	}
	if _, err := acceptWith(t, usageGrant, pg, set("configuration", "db")); err != nil {
		t.Fatalf("own provider rejected: %v", err)
	}
	pub, priv := testKeypair()
	trust := evidenceenvelope.MapTrustStore{keyID: evidenceenvelope.TrustEntry{PublicKey: pub, ProducerID: "env-a", AllowedObservationKinds: usageGrant, ProviderRepos: pg}}
	a := NewAcceptor(trust, fakeResolver{c: scopedContract("oci://ghcr.io/acme/platform/redis:7")}, NewMemoryStore(), fixedNow)
	if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 1, "e1", set("configuration", "db"))); !errors.Is(err, ErrProviderNotAllowed) {
		t.Errorf("redis reference: %v, want ErrProviderNotAllowed", err)
	}
	if _, err := acceptWith(t, usageGrant, pg, set("dependency", "stripe")); !errors.Is(err, ErrProviderNotAllowed) {
		t.Errorf("ref-less dependency: %v, want ErrProviderNotAllowed", err)
	}
}

func TestIngestUsageNeedsTheGrant(t *testing.T) {
	set := provisionedSet("ghcr.io/a/orders/production/usage/configuration/db", fixedNow().Add(-time.Minute), usageObs("configuration", "db", "production"))
	for name, kinds := range map[string][]string{"empty allowlist": nil, "provisioning only": {string(evidence.ResourceProvisioned)}} {
		if _, err := acceptWith(t, kinds, nil, set); !errors.Is(err, evidenceenvelope.ErrKindNotAllowed) {
			t.Errorf("%s: Accept = %v, want ErrKindNotAllowed", name, err)
		}
	}
}

// usageHandler is a host whose store holds one runtime record, plus whatever
// the returned accept function adds.
func usageHandler(t *testing.T) (*Handler, string, func(seq uint64, set evidence.EvidenceSet)) {
	t.Helper()
	a, priv := provisioningAcceptor(t, []string{string(evidence.ResourceProvisioned), string(evidence.ResourceUsage), string(evidence.WorkloadObserved)})
	runtime := testEvidenceSet()
	runtime.ContractRef = ordersRef
	if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, 1, "e1", runtime)); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(a, nil, nil)
	mux := http.NewServeMux()
	h.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return h, srv.URL, func(seq uint64, set evidence.EvidenceSet) {
		t.Helper()
		if _, err := a.Accept(context.Background(), signedEnvelopeBytes(t, priv, seq, "e"+string(rune('0'+seq)), set)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTargetsSchemaV3WithoutUsage(t *testing.T) {
	_, url, accept := usageHandler(t)
	before := fixedNow().Add(-time.Minute)
	accept(2, provisionedSet("ghcr.io/a/orders/production/configuration/db", before, provisioned("configuration", "db", "production")))
	var body TargetsResponse
	getJSON(t, url+TargetsPath, &body)
	if body.SchemaVersion != TargetsSchemaVersionProvisioning || body.Usage != nil || len(body.Provisioning) != 1 {
		t.Errorf("schema %q usage %+v provisioning %d, want v3, no usage and one provisioning record", body.SchemaVersion, body.Usage, len(body.Provisioning))
	}
}

func TestTargetsSchemaV4WithUsage(t *testing.T) {
	h, url, accept := usageHandler(t)
	before := fixedNow().Add(-time.Minute)
	accept(2, provisionedSet("ghcr.io/a/orders/production/usage/configuration/db", before, usageObs("configuration", "db", "production")))
	var body TargetsResponse
	getJSON(t, url+TargetsPath, &body)
	if body.SchemaVersion != TargetsSchemaVersionUsage || body.Provisioning != nil {
		t.Fatalf("schema %q provisioning %+v, want v4 and no provisioning", body.SchemaVersion, body.Provisioning)
	}
	if len(body.Targets) != 1 || body.Targets[0].Subject != "payments" {
		t.Errorf("a usage report is never a target: %+v", body.Targets)
	}
	if len(body.Usage) != 1 {
		t.Fatalf("usage = %+v", body.Usage)
	}
	want := UsageDTO{Subject: "ghcr.io/a/orders/production/usage/configuration/db", Service: "orders", Domain: "ghcr.io/a", Digest: "sha256:" + strings.Repeat("b", 64),
		ContractRef: ordersRef, Producer: "env-a", ProducerKeyID: keyID, Kind: "configuration", Name: "db", Environment: "production",
		Provider: "acme-db", ExternalRef: "orders-db", Metrics: []evidence.UsageMetric{{Name: "storage-used", Value: 31, Unit: "Gi"}},
		Cost: &evidence.UsageCost{Amount: "212.40", Currency: "USD", Period: "P30D"}, Outcome: "Observed", EvidenceAt: before, AcceptedAt: fixedNow()}
	if !reflect.DeepEqual(body.Usage[0], want) {
		t.Errorf("usage DTO = %+v, want %+v", body.Usage[0], want)
	}
	// Usage shares the targets budget: the usage record sorts first in the store.
	for _, tc := range []struct{ budget, targets, usage int }{{0, 0, 0}, {1, 0, 1}} {
		h.maxTargets = tc.budget
		var b TargetsResponse
		getJSON(t, url+TargetsPath, &b)
		if len(b.Targets) != tc.targets || len(b.Usage) != tc.usage || !b.Truncated {
			t.Errorf("budget %d: targets %d usage %d truncated %v", tc.budget, len(b.Targets), len(b.Usage), b.Truncated)
		}
	}
}
