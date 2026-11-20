package cli

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/spf13/viper"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// twoClusterQuery is prod and shop in eu, prod in us.
func twoClusterQuery(t *testing.T) *fleet.Query {
	t.Helper()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	src := func(id, name string, nss ...string) fleet.Source {
		col := &fleet.Collection{Cluster: &fleet.Cluster{ID: id, Name: name, NameSource: fleet.ClusterNameFromContext}}
		for _, ns := range nss {
			col.Targets = append(col.Targets, fleet.RawTarget{Scope: ns, Kind: fleet.SourceKindKubernetes, Name: ns + "-" + name, Service: ns + "-" + name, EvidenceAt: &now})
		}
		return fleet.NewMemorySource(name, fleet.SourceKindKubernetes, col)
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return now }},
		src("uid-eu", "eu-prod", "prod", "shop"), src("uid-us", "us-prod", "prod"))
	if err != nil {
		t.Fatal(err)
	}
	return fleet.NewQuery(snap)
}

// viewFacts is what a reader learns about clusters from a view.
func viewFacts(q *fleet.Query) (string, map[string]string, []fleet.Limitation) {
	s := q.SnapshotForSerialization()
	access := map[string]string{}
	for _, c := range s.Clusters {
		access[c.ID] = c.Access
	}
	var hidden []fleet.Limitation
	for _, l := range s.Limitations {
		if l.Code == fleet.LimitationClusterHidden {
			hidden = append(hidden, l)
		}
	}
	return q.SnapshotID(), access, hidden
}

// parityViews are one person's views of q through the dashboard, the HTTP
// MCP fleet tools and the MCP delivery reads, all over d's resolved contexts.
func parityViews(t *testing.T, d deps, home *rest.Config, entries []app.K8sEntry, alice authn.Person, q *fleet.Query) map[string]*fleet.Query {
	t.Helper()
	aliceCtx := context.WithValue(context.Background(), reviewedKey{}, reviewed{person: alice})
	v := viper.New()
	v.Set("dashboard.oidc-issuer-url", "https://idp.example")
	v.Set("dashboard.oidc-client-id", "pacto-dashboard")
	v.Set("dashboard.oidc-redirect-url", "https://dash.example/auth/callback")
	id, err := newDashboardIdentity(v, "0.0.0.0", home, entriesOf(entries...), d)
	expect(t, err == nil, "identity: %v", err)
	id.state = func(context.Context) authn.State { return authn.State{SignedIn: true, Person: alice} }
	dash, err := id.view(context.Background(), q)
	expect(t, err == nil, "dashboard: %v", err)

	cmd := newMCPCommand(app.NewService(nil, nil), "v", d)
	for k, val := range map[string]string{"transport": "http", "auth-issuer": "https://idp.example", "context": "eu-prod"} {
		expect(t, cmd.Flags().Set(k, val) == nil, "flag %s", k)
	}
	mcpView, _ := mcpFleetView(cmd, app.NewService(nil, nil), d)
	viaMCP, err := mcpView(aliceCtx, nil, q)
	expect(t, err == nil, "mcp: %v", err)

	var viaReads *fleet.Query
	fm := &pactomcp.FleetMode{Src: func(context.Context) (*fleet.Query, error) { return q, nil }, Opts: pactomcp.FleetOptions{
		View: func(ctx context.Context, ti *auth.TokenInfo, q *fleet.Query) (*fleet.Query, error) {
			r, err := mcpView(ctx, ti, q)
			viaReads = r
			return r, err
		}}}
	base := baseDashboardIdentity(home, d, nil, delivery.PolicyNamespace)
	_, err = mcpDeliveryReads(base, fm, "")(actFor(t, d), remoteCallerKey(alice))(aliceCtx, "inbox", nil)
	expect(t, err == nil && viaReads != nil, "delivery reads: %v", err)
	return map[string]*fleet.Query{"dashboard": dash, "mcp": viaMCP, "delivery reads": viaReads}
}

// TestViewerParityAcrossSurfaces: the dashboard, the HTTP MCP fleet tools and
// the MCP delivery reads narrow one two-cluster fleet for one person to the
// same view, reviewed per cluster through the same resolved contexts.
func TestViewerParityAcrossSurfaces(t *testing.T) {
	h := &hostClusters{allow: func(host, ns string) bool { return host == "https://eu" && ns == "prod" }}
	d := readDeps(nil)
	d.dynamicFor = h.dynamicFor
	home := &rest.Config{Host: "https://eu"}
	d.clusterConfig = func() (*rest.Config, error) { return home, nil }
	entries := []app.K8sEntry{entry("eu-prod", "https://eu", "uid-eu"), entry("us-prod", "https://us", "uid-us")}
	d.resolveK8s = func(*app.Service, context.Context, bool, []string) []app.K8sEntry { return entries }
	views := parityViews(t, d, home, entries, authn.Person{User: "alice", Groups: []string{"team-a"}}, twoClusterQuery(t))

	wantID, wantAccess, wantHidden := viewFacts(views["dashboard"])
	if wantAccess["uid-us"] != fleet.ClusterNoAccess || len(wantHidden) != 1 || slices.Contains(services(views["dashboard"]), "prod-us-prod") {
		t.Fatalf("dashboard view: %v %v %v", wantAccess, wantHidden, services(views["dashboard"]))
	}
	for name, got := range views {
		id, access, hidden := viewFacts(got)
		if id != wantID || !maps.Equal(access, wantAccess) || !slices.Equal(hidden, wantHidden) {
			t.Errorf("%s: %s %v %v, dashboard %s %v %v", name, id, access, hidden, wantID, wantAccess, wantHidden)
		}
	}
}

func services(q *fleet.Query) []string {
	var out []string
	for _, s := range q.SnapshotForSerialization().Services {
		out = append(out, s.Name)
	}
	return out
}
