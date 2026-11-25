package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// consumablePG is a postgresql 16.1.0 provider with a closed, tiered
// provisioning schema, and the digest its registry serves.
func consumablePG() (*contract.Bundle, string) {
	schema := `{"type":"object","additionalProperties":false,"required":["engine","class","storageGB","backupRetentionDays"],"properties":{
"engine":{"type":"string","enum":["postgres"],"x-pacto-tier":"contract"},
"class":{"type":"string","enum":["small","large"]},
"storageGB":{"type":"integer","minimum":10,"maximum":500,"default":20,"x-pacto-tier":"tunable"},
"backupRetentionDays":{"type":"integer","minimum":1,"maximum":35,"x-pacto-tier":"tunable"}}}`
	c := &contract.Contract{PactoVersion: "2.4", Service: contract.Service{Name: "postgresql", Version: "16.1.0", Offering: "postgresql", Owner: contract.Owner{Team: "data"}},
		Workload:       contract.WorkloadService,
		Configurations: []contract.Configuration{{Name: "provisioning", Schema: "configuration/provisioning.schema.json", Required: true}}}
	b := &contract.Bundle{Contract: c, FS: fstest.MapFS{"configuration/provisioning.schema.json": {Data: []byte(schema)}}}
	return b, fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("postgresql:16.1.0")))
}

// consumableSnippetDir reads the consumable provider of a fleet over the
// postgresql bundle, writes a consumer of its snippet to a temp dir and
// returns the dir, the provider and a store serving the bundle.
func consumableSnippetDir(t *testing.T) (string, fleet.ConsumableProvider, *testutil.MockBundleStore) {
	t.Helper()
	b, digest := consumablePG()
	rev := fleet.RawRevision{Bundle: b, Domain: "ghcr.io/acme", RequestedRef: "oci://ghcr.io/acme/postgresql:16.1.0", Digest: digest,
		Provenance: &fleet.BundleProvenance{Source: "oci", Signature: fleet.BundleSignature{Status: fleet.SignatureVerified, Source: "rule", Signer: "https://github.com/acme/pg/.github/workflows/publish.yml@refs/heads/main"}}}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC) }},
		fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{rev}}))
	if err != nil {
		t.Fatal(err)
	}
	l, err := fleet.NewQuery(snap).Entities(fleet.EntityFilter{Kinds: []fleet.EntityKind{fleet.KindOffering}, Consumable: true})
	if err != nil || len(l.Entities) != 1 || l.Entities[0].Consumable == nil {
		t.Fatalf("consumable list = %+v, %v", l, err)
	}
	p := l.Entities[0].Consumable.Providers[0]
	if p.Snippet == nil || len(p.Snippet.Entries.Configurations) != 1 || len(p.Snippet.Entries.Configurations[0].MissingValues) != 0 {
		t.Fatalf("snippet = %+v", p.Snippet)
	}
	dir := t.TempDir()
	y := "pactoVersion: \"" + contract.Current() + "\"\nservice:\n  name: invoices\n  version: 0.1.0\n" + p.Snippet.YAML
	if err := os.WriteFile(filepath.Join(dir, "pacto.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	store := &testutil.MockBundleStore{
		PullFn: func(_ context.Context, ref string) (*contract.Bundle, error) {
			if strings.Contains(ref, "ghcr.io/acme/postgresql") {
				return b, nil
			}
			return nil, os.ErrNotExist
		},
		// Only the listed version resolves to the listed digest: a lock that
		// resolved any other tag or repository would pin another digest.
		ResolveFn: func(_ context.Context, ref string) (string, error) {
			if ref == "ghcr.io/acme/postgresql:16.1.0" {
				return digest, nil
			}
			return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(ref))), nil
		},
		ListTagsFn: func(context.Context, string) ([]string, error) { return []string{"16.1.0"}, nil },
	}
	return dir, p, store
}

// TestConsumableSnippetValidates: the snippet a consumable list gives passes
// pacto validate as is.
func TestConsumableSnippetValidates(t *testing.T) {
	dir, _, store := consumableSnippetDir(t)
	r, err := NewService(store, nil).Validate(context.Background(), ValidateOptions{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Fatalf("snippet invalid: %+v", r.Errors)
	}
	ran := false
	for _, st := range r.Stages {
		if st.Status == validation.StageFail {
			t.Errorf("stage %s failed: %+v", st.Name, st.Errors)
		}
		if st.Name == "configuration-refs" {
			ran = st.Status == validation.StagePass
		}
	}
	if !ran {
		t.Errorf("configuration-refs did not pass: %s", stageLine(r))
	}
}

// TestConsumableSnippetLocksListedDigest: pacto lock over the snippet pins
// the configuration ref to the digest the list showed.
func TestConsumableSnippetLocksListedDigest(t *testing.T) {
	dir, p, store := consumableSnippetDir(t)
	res, err := NewService(store, nil).Lock(context.Background(), LockOptions{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range res.Lock.References {
		if ref.Kind == "config" && ref.Name == "postgresql" {
			found = true
			if ref.Digest != p.Digest {
				t.Errorf("locked digest = %s, want %s", ref.Digest, p.Digest)
			}
		}
	}
	if !found {
		t.Errorf("no configuration reference locked: %+v", res.Lock.References)
	}
}
