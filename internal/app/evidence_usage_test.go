package app

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/evidenceenvelope"
	"github.com/trianalab/pacto/v4/pkg/evidenceingest"
)

func usageFixture() (*Service, func(func(*UsageOptions)) UsageOptions) {
	svc := &Service{sys: sysDeps{now: func() time.Time { return provisionedAt }}}
	base := UsageOptions{ContractRef: "oci://ghcr.io/a/orders" + provisionedDigest, Configuration: "db", Provider: "acme-db",
		Metrics: []evidence.UsageMetric{{Name: "storage-used", Value: 31, Unit: "Gi"}}}
	return svc, func(f func(*UsageOptions)) UsageOptions { o := base; f(&o); return o }
}

func TestUsageEvidence(t *testing.T) {
	t.Parallel()
	svc, with := usageFixture()
	set, err := svc.UsageEvidence(with(func(o *UsageOptions) {
		o.Environment = "production"
		o.Cost = &evidence.UsageCost{Amount: "212.40", Currency: "USD", Period: "P30D"}
	}))
	if err != nil {
		t.Fatal(err)
	}
	// The usage subject is distinct from the provisioning subject of the same resource.
	if set.Subject.Name != "ghcr.io/a/orders/production/usage/configuration/db" || !set.ObservedAt.Equal(provisionedAt) || set.Source != "pacto" {
		t.Errorf("set = %+v", set)
	}
	u, err := set.Observations[0].GetUsageObservation()
	if err != nil || u.Cost.Amount != "212.40" || u.Metrics[0].Value != 31 || u.Environment != "production" {
		t.Errorf("observation = %+v %v", u, err)
	}
	dep, _ := svc.UsageEvidence(with(func(o *UsageOptions) { o.Configuration, o.Dependency = "", "postgresql" }))
	if dep.Subject.Name != "ghcr.io/a/orders/default/usage/dependency/postgresql" {
		t.Errorf("dependency subject %q", dep.Subject.Name)
	}
	set, err = svc.UsageEvidence(with(func(o *UsageOptions) {
		o.Subject, o.Outcome, o.Environment, o.Metrics = "ghcr.io/a/orders/eu/usage/db-main", "Stale", "production", nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if o := set.Observations[0]; set.Subject.Name != "ghcr.io/a/orders/eu/usage/db-main" || o.Outcome != evidence.Stale || len(o.Value) != 0 || o.Subject.Environment != "production" {
		t.Errorf("unobserved = %+v", set)
	}
}

func TestUsageEvidence_Rejects(t *testing.T) {
	t.Parallel()
	svc, with := usageFixture()
	for name, opts := range map[string]UsageOptions{
		"both names":                 with(func(o *UsageOptions) { o.Dependency = "postgresql" }),
		"mutable ref":                with(func(o *UsageOptions) { o.ContractRef = "oci://ghcr.io/a/orders:1.0.0" }),
		"subject outside repository": with(func(o *UsageOptions) { o.Subject = "ghcr.io/b/orders/default/usage/db" }),
		"subject without usage":      with(func(o *UsageOptions) { o.Subject = "ghcr.io/a/orders/default/configuration/db" }),
		"no metric or cost":          with(func(o *UsageOptions) { o.Metrics = nil }),
		"NaN":                        with(func(o *UsageOptions) { o.Metrics = []evidence.UsageMetric{{Name: "m", Value: math.NaN()}} }),
		"unknown outcome":            with(func(o *UsageOptions) { o.Outcome = "Done" }),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.UsageEvidence(opts); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// A provider scope can be granted through ResourceUsage alone, and still needs a provider kind.
func TestTrustConfigScopeRequiresProviderKind(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := (&Service{}).GenerateKey(dir, "", "edgekey", false); err != nil {
		t.Fatal(err)
	}
	load := func(kinds string) error {
		p := filepath.Join(dir, "trust.yaml")
		body := "apiVersion: pacto.dev/evidence-trust/v1\nkeys:\n  - {keyId: k, producerId: p, publicKeyFile: edgekey.pub" +
			kinds + ", allowedProviderRepos: [ghcr.io/acme/platform/postgresql]}\n"
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := loadTrustStore(p)
		return err
	}
	if err := load(", allowedObservationKinds: [ResourceUsage]"); err != nil {
		t.Errorf("a usage-only scoped key: %v", err)
	}
	if err := load(", allowedObservationKinds: [WorkloadObserved]"); err == nil || !strings.Contains(err.Error(), "ResourceProvisioned or ResourceUsage") {
		t.Errorf("a runtime-only scoped key: %v, want the missing-kind rejection naming both kinds", err)
	}
}

func TestUnscopedProvisioningKeysCoverUsage(t *testing.T) {
	t.Parallel()
	ts := evidenceenvelope.MapTrustStore{
		"usage":  {AllowedObservationKinds: []string{string(evidence.ResourceUsage)}},
		"scoped": {AllowedObservationKinds: []string{string(evidence.ResourceUsage)}, ProviderRepos: []string{"ghcr.io/acme/platform/postgresql"}},
	}
	if got := unscopedProvisioningKeys(ts); strings.Join(got, ",") != "usage" {
		t.Errorf("unscopedProvisioningKeys = %v, want [usage]", got)
	}
}

// usage posts a usage report about the named configuration, signed by the
// producer's key with the given sequence.
func (f scopedFixture) usage(t *testing.T, base, producer, configuration string, seq uint64) SendResult {
	t.Helper()
	set, err := f.svc.UsageEvidence(UsageOptions{ContractRef: f.contractRef, Configuration: configuration, Environment: "production", Provider: "acme",
		Cost: &evidence.UsageCost{Amount: "212.40", Currency: "USD", Period: "P30D"}})
	if err != nil {
		t.Fatal(err)
	}
	return f.send(t, base, producer, set, seq)
}

// The CI proof of the usage trust rules on a real Evidence Server: a key needs
// the ResourceUsage grant, its provider scope applies to usage, the endpoint
// speaks v3 until usage is stored, and a usage report keeps its own record next
// to the same resource's provisioning record.
func TestUsageDoesNotReplaceProvisioning(t *testing.T) {
	t.Parallel()
	f := newScopedFixture(t, testutil.ReferrersOptions{})
	host := strings.TrimPrefix(strings.SplitN(f.contractRef, "/orders@", 2)[0], "oci://")
	both := "ResourceProvisioned, ResourceUsage"
	base := f.serveKinds(t, context.Background(), map[string]string{"db-adapter": host + "/platform/postgresql"},
		map[string]string{"db-adapter": both})

	if res := f.usage(t, base, "cache-adapter", "cache", 1); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("usage from a provisioning-only key = %d %s, want 401", res.StatusCode, res.Body)
	}
	if res := f.report(t, base, "db-adapter", "database", 1); res.StatusCode != http.StatusAccepted {
		t.Fatalf("provisioning = %d %s, want 202", res.StatusCode, res.Body)
	}
	if got := targetsSchema(t, base); got.SchemaVersion != evidenceingest.TargetsSchemaVersionProvisioning {
		t.Errorf("schema without usage = %q, want v3", got.SchemaVersion)
	}
	if res := f.usage(t, base, "db-adapter", "cache", 2); res.StatusCode != http.StatusForbidden || !strings.Contains(res.Body, "provider_not_allowed") {
		t.Fatalf("usage for a foreign provider = %d %s, want 403 provider_not_allowed", res.StatusCode, res.Body)
	}
	if res := f.usage(t, base, "db-adapter", "database", 3); res.StatusCode != http.StatusAccepted {
		t.Fatalf("usage = %d %s, want 202", res.StatusCode, res.Body)
	}
	got := targetsSchema(t, base)
	if got.SchemaVersion != evidenceingest.TargetsSchemaVersionUsage || len(got.Provisioning) != 1 || len(got.Usage) != 1 {
		t.Fatalf("targets = %q provisioning %d usage %d, want v4 with both records", got.SchemaVersion, len(got.Provisioning), len(got.Usage))
	}
	if got.Provisioning[0].Phase != "Ready" || got.Usage[0].Cost == nil || got.Usage[0].Name != "database" || got.Usage[0].ProducerKeyID != "db-adapter-key" {
		t.Errorf("provisioning %+v usage %+v", got.Provisioning[0], got.Usage[0])
	}
}

func targetsSchema(t *testing.T, base string) evidenceingest.TargetsResponse {
	t.Helper()
	var body evidenceingest.TargetsResponse
	if err := json.Unmarshal([]byte(getBody(t, base+"/api/evidence/v1/targets")), &body); err != nil {
		t.Fatal(err)
	}
	return body
}
