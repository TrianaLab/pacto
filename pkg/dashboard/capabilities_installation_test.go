package dashboard

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// installationQuery: one cluster, uid-1, whose targets live in shop, with the
// operator's Installation row detected there.
func installationQuery(t *testing.T) *fleet.Query {
	t.Helper()
	at := time.Now()
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{},
		fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{
			Cluster: &fleet.Cluster{ID: "uid-1", Name: "prod"},
			Targets: []fleet.RawTarget{{Scope: "shop", Kind: fleet.SourceKindKubernetes, Name: "app", Service: "app"}},
		}),
		fleet.NewMemorySource("detection", fleet.SourceKindDetection, &fleet.Collection{Advisory: true, State: &fleet.SourceState{Status: fleet.SourceAvailable},
			Providers: []fleet.Provider{{ID: "installation", Name: "Installation", Evidence: "profile hardened", Status: fleet.SourceAvailable,
				Provenance: fleet.ProvenanceDeclared, AsOf: &at, ClusterID: "uid-1"}}}))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

func hardenedConfig(context.Context) (*fleet.InstallationDeclared, fleet.SourceState) {
	return &fleet.InstallationDeclared{Profile: []string{"hardened"}, Registry: "ghcr.io/acme", ClusterID: "uid-1", AsOf: time.Now(), Source: "detected.json"},
		fleet.SourceState{Status: fleet.SourceAvailable}
}

func capabilitiesOf(t *testing.T, opts Options) *capabilitiesOutput {
	t.Helper()
	out, err := NewServer(opts).capabilities(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCapabilitiesReportsInstallation(t *testing.T) {
	q := installationQuery(t)
	got := capabilitiesOf(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }, InstallationConfig: hardenedConfig}).Body.Installation
	if !slices.Equal(got.Profile.Value, []string{"hardened"}) || got.Registry.Value == nil || *got.Registry.Value != "ghcr.io/acme" {
		t.Errorf("profile %+v registry %+v", got.Profile, got.Registry)
	}
	if got.MCP.Served || got.MCP.Source != "this dashboard serves no /mcp endpoint" || got.Clusters.Joined == nil || *got.Clusters.Joined != 1 {
		t.Errorf("mcp %+v clusters %+v", got.MCP, got.Clusters)
	}
	if got.Delivery.Enabled != nil || got.Delivery.Source != "this server reads no delivery" {
		t.Errorf("delivery %+v", got.Delivery)
	}
}

func TestCapabilitiesInstallationWithoutConfig(t *testing.T) {
	got := capabilitiesOf(t, Options{}).Body.Installation
	if got.Profile.Value != nil || !strings.HasPrefix(got.Profile.Source, "no --detected") || got.Clusters.Joined != nil {
		t.Errorf("installation = %+v", got)
	}
}

func TestCapabilitiesInstallationHiddenCluster(t *testing.T) {
	q := installationQuery(t)
	got := capabilitiesOf(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }, InstallationConfig: hardenedConfig,
		View: func(_ context.Context, q *fleet.Query) (*fleet.Query, error) {
			return q.Restrict(func(_, ns string) bool { return ns == "payments" }, nil), nil
		}}).Body.Installation
	if got.Profile.Value != nil || got.Registry.Value != nil || !strings.HasPrefix(got.Profile.Source, "the fleet snapshot in your view holds no Installation row") {
		t.Errorf("profile %+v registry %+v", got.Profile, got.Registry)
	}
	// A caller whose permissions cannot be checked sees no snapshot.
	got = capabilitiesOf(t, Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }, InstallationConfig: hardenedConfig,
		View: func(context.Context, *fleet.Query) (*fleet.Query, error) { return nil, errors.New("apiserver down") }}).Body.Installation
	if got.Registry.Value != nil || !strings.HasPrefix(got.Registry.Source, "no fleet snapshot in your view") ||
		!strings.Contains(got.Clusters.Source, "your permissions could not be checked") {
		t.Errorf("an unchecked caller read registry %+v clusters %+v", got.Registry, got.Clusters)
	}
}

// delivery.asOf is when the cached probe last asked the cluster.
func TestCapabilitiesInstallationDeliveryAsOfFromProbe(t *testing.T) {
	checked := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	body := capabilitiesOf(t, Options{Delivery: func(*http.Request) (DeliveryAPI, error) { return nil, nil },
		DeliveryInstalled: func(context.Context) (bool, error) { return false, nil }, DeliveryCheckedAt: func() time.Time { return checked }}).Body
	if d := body.Installation.Delivery; d.AsOf == nil || !d.AsOf.Equal(checked) {
		t.Errorf("installation.delivery = %+v", d)
	}
}

func TestCapabilitiesInstallationDeliveryProbeError(t *testing.T) {
	probe := func(context.Context) (bool, error) { return false, errors.New("apiserver down") }
	wired := func(*http.Request) (DeliveryAPI, error) { return nil, nil }
	local := &Auth{Local: true, State: func(*http.Request) AuthState { return AuthState{} }}
	for _, au := range []*Auth{nil, local} {
		body := capabilitiesOf(t, Options{Auth: au, Delivery: wired, DeliveryInstalled: probe}).Body
		if d := body.Installation.Delivery; d.Enabled != nil || d.Source != "the delivery probe failed: apiserver down" {
			t.Errorf("auth %v: installation.delivery = %+v", au != nil, d)
		}
		if au != nil && !body.Delivery {
			t.Error("a failed probe hides the delivery tab")
		}
	}
	// The demo's in-process delivery answers for itself.
	if d := capabilitiesOf(t, Options{Delivery: wired}).Body.Installation.Delivery; d.Enabled == nil || !*d.Enabled ||
		!strings.HasPrefix(d.Source, "this server serves delivery itself; no fleet snapshot in your view") || d.Completeness != fleet.InstallationPartial {
		t.Errorf("in-process delivery = %+v", d)
	}
}
