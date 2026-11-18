package fleetsrc

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

var probedAt = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

func TestRegistryRowGHCRShape(t *testing.T) {
	keys := []fleet.ServiceKey{"ghcr.io/x/a"}
	row := RegistryRow(capabilities.Capabilities{Host: "ghcr.io", Referrers: capabilities.ReferrersWriteOnceTags, Listing: capabilities.ListingTags,
		ArtifactType: capabilities.ArtifactTypeUnknown, Auth: "bearer", CheckedAt: probedAt}, keys)
	if row.ID != "registry:ghcr.io" || row.Name != "Registry ghcr.io" || row.Evidence != "no Referrers, no _catalog" ||
		!slices.Equal(row.UsedFor, []string{fleet.UseWriteOnceTags, fleet.UseIndexListedByTag}) ||
		row.AsOf == nil || !row.AsOf.Equal(probedAt) || row.Objects != nil || row.Provenance != "" || row.Status != "" ||
		!slices.Equal(row.Services, keys) {
		t.Fatalf("row = %+v", row)
	}
	want := []string{"mode: referrers=write-once-tags listing=tags", "auth: bearer", "artifactType: not checked (read-only probe)", "cosign signatures: not checked"}
	if !slices.Equal(row.Details, want) {
		t.Fatalf("details = %q", row.Details)
	}
}

func TestRegistryRowNative(t *testing.T) {
	row := RegistryRow(capabilities.Capabilities{Host: "zot.local", Referrers: capabilities.ReferrersNative, Listing: capabilities.ListingCatalog,
		ArtifactType: capabilities.ArtifactTypeAccepted, Auth: "anonymous", CheckedAt: probedAt}, nil)
	if row.Evidence != "Referrers, _catalog" || !slices.Equal(row.UsedFor, []string{fleet.UseNativeReferrers, fleet.UseIndexListedByCatalog}) ||
		row.Details[2] != "artifactType: accepted" {
		t.Fatalf("row = %+v", row)
	}
}

func TestRegistryRowProbeFailedIsPartial(t *testing.T) {
	row := registryProbeFailedRow("oci", "ghcr.io", nil, errors.New("timeout"))
	if row.Status != fleet.SourcePartial || row.UsedFor == nil || len(row.UsedFor) != 0 || row.Evidence != "capabilities unknown" ||
		len(row.Limitations) != 1 || row.Limitations[0].Code != fleet.LimitationRegistryProbeFailed || row.Limitations[0].Source != "oci" ||
		!strings.Contains(row.Limitations[0].Message, "registry ghcr.io could not be probed: timeout") {
		t.Fatalf("row = %+v", row)
	}
}

// probeRecorder answers every probe with a native registry and records the refs.
type probeRecorder struct {
	mu   sync.Mutex
	refs []string
}

func (p *probeRecorder) probe(_ context.Context, ref string) (capabilities.Capabilities, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refs = append(p.refs, ref)
	host, _, _ := strings.Cut(contract.TrimOCIScheme(ref), "/")
	return capabilities.Capabilities{Host: host, Referrers: capabilities.ReferrersNative, Listing: capabilities.ListingCatalog, CheckedAt: probedAt}, nil
}

func registryStore() *fakeStore {
	return &fakeStore{
		bundles: map[string]*contract.Bundle{
			"ghcr.io/x/a:1.0.0": bundleFor("a"), "ghcr.io/x/b:1.0.0": bundleFor("b"), "quay.io/y/c:1.0.0": bundleFor("c"),
		},
		digest: map[string]string{"ghcr.io/x/a:1.0.0": validDigest("a"), "ghcr.io/x/b:1.0.0": validDigest("b"), "quay.io/y/c:1.0.0": validDigest("c")},
	}
}

func TestOCISourceProbesEachHostOnce(t *testing.T) {
	rec := &probeRecorder{}
	s := NewOCISource("oci", registryStore(), []string{"ghcr.io/x/a:1.0.0", "ghcr.io/x/b:1.0.0", "quay.io/y/c:1.0.0"})
	s.RegistryProbe = rec.probe
	col, err := s.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.refs) != 2 || rec.refs[0] != col.Revisions[0].ResolvedRef || rec.refs[1] != col.Revisions[2].ResolvedRef {
		t.Fatalf("probed %q", rec.refs)
	}
	if len(col.Providers) != 2 || col.Providers[0].ID != "registry:ghcr.io" || col.Providers[1].ID != "registry:quay.io" ||
		!slices.Equal(col.Providers[0].Services, []fleet.ServiceKey{fleet.NewServiceKeyDomain("ghcr.io/x", "a"), fleet.NewServiceKeyDomain("ghcr.io/x", "b")}) {
		t.Fatalf("rows = %+v", col.Providers)
	}
}

func TestOCISourceUnresolvedHostGetsNoRow(t *testing.T) {
	rec := &probeRecorder{}
	s := NewOCISource("oci", registryStore(), []string{"ghcr.io/x/a:1.0.0", "unreachable.io/z/d:1.0.0"})
	s.RegistryProbe = rec.probe
	col, _ := s.Collect(context.Background())
	if len(col.Providers) != 1 || col.Providers[0].ID != "registry:ghcr.io" || len(rec.refs) != 1 {
		t.Fatalf("rows = %+v, probed %q", col.Providers, rec.refs)
	}
}

func TestOCISourceNoProbeNoRows(t *testing.T) {
	col, err := NewOCISource("oci", registryStore(), []string{"ghcr.io/x/a:1.0.0"}).Collect(context.Background())
	if err != nil || col.Providers != nil {
		t.Fatalf("rows = %+v %v", col.Providers, err)
	}
}

func TestOCISourceProbeBudget(t *testing.T) {
	s := NewOCISource("oci", registryStore(), []string{"ghcr.io/x/a:1.0.0"})
	s.probeBudget = 50 * time.Millisecond
	s.RegistryProbe = func(ctx context.Context, _ string) (capabilities.Capabilities, error) {
		<-ctx.Done()
		return capabilities.Capabilities{}, ctx.Err()
	}
	start := time.Now()
	col, err := s.Collect(context.Background())
	if err != nil || time.Since(start) > 5*time.Second {
		t.Fatalf("collect took %s: %v", time.Since(start), err)
	}
	if len(col.Providers) != 1 || col.Providers[0].Status != fleet.SourcePartial {
		t.Fatalf("rows = %+v", col.Providers)
	}
}

// registrySnapshot builds the OCI source's rows beside Kubernetes targets of a
// in namespace "a" and b in namespace "b".
func registrySnapshot(t *testing.T, refs ...string) *fleet.FleetSnapshot {
	t.Helper()
	rec := &probeRecorder{}
	s := NewOCISource("oci", registryStore(), refs)
	s.RegistryProbe = rec.probe
	targets := fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Targets: []fleet.RawTarget{
		{Scope: "a", Kind: "kubernetes", Name: "a", Service: "a", Domain: "ghcr.io/x"},
		{Scope: "b", Kind: "kubernetes", Name: "b", Service: "b", Domain: "ghcr.io/x"},
	}})
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, targets, s)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func registryRowIDs(q *fleet.Query) []string {
	var ids []string
	for _, p := range q.Providers().Providers {
		if strings.HasPrefix(p.ID, fleet.ProviderRegistryPrefix) {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

func TestRegistryRowsDoNotChangeCompleteness(t *testing.T) {
	failing := NewOCISource("oci", registryStore(), []string{"ghcr.io/x/a:1.0.0"})
	failing.RegistryProbe = func(context.Context, string) (capabilities.Capabilities, error) {
		return capabilities.Capabilities{}, errors.New("down")
	}
	without := NewOCISource("oci", registryStore(), []string{"ghcr.io/x/a:1.0.0"})
	a, err := fleet.Build(context.Background(), fleet.BuildOptions{}, failing)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := fleet.Build(context.Background(), fleet.BuildOptions{}, without)
	if a.Completeness != b.Completeness || len(a.Providers) != 1 || a.Providers[0].Status != fleet.SourcePartial {
		t.Fatalf("completeness %s vs %s, rows %+v", a.Completeness, b.Completeness, a.Providers)
	}
}

func TestRegistryRowHiddenWhenNoVisibleRef(t *testing.T) {
	q := fleet.NewQuery(registrySnapshot(t, "ghcr.io/x/b:1.0.0"))
	if ids := registryRowIDs(q); !slices.Equal(ids, []string{"registry:ghcr.io"}) {
		t.Fatalf("unrestricted rows = %q", ids)
	}
	only := func(ns string) bool { return ns == "a" }
	if ids := registryRowIDs(q.Restrict(only, func(ns, _ string) bool { return only(ns) })); len(ids) != 0 {
		t.Fatalf("a viewer who sees no service of the host saw %q", ids)
	}
}

func TestRegistryRowKeptWhenOneRefVisible(t *testing.T) {
	q := fleet.NewQuery(registrySnapshot(t, "ghcr.io/x/a:1.0.0", "ghcr.io/x/b:1.0.0"))
	want := q.Providers().Providers
	only := func(ns string) bool { return ns == "a" }
	got := q.Restrict(only, func(ns, _ string) bool { return only(ns) }).Providers().Providers
	i := slices.IndexFunc(got, func(p fleet.Provider) bool { return p.ID == "registry:ghcr.io" })
	j := slices.IndexFunc(want, func(p fleet.Provider) bool { return p.ID == "registry:ghcr.io" })
	if i < 0 || j < 0 || got[i].Evidence != want[j].Evidence || !slices.Equal(got[i].Details, want[j].Details) {
		t.Fatalf("rows = %+v, want the registry row unchanged", got)
	}
}
