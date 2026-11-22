package main

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// demoFleet builds the browser demo's fleet from the same bundles the wasm binary embeds.
func demoFleet(t *testing.T) *fleet.FleetSnapshot {
	t.Helper()
	partners, err := NewEmbedSource(os.DirFS("partners"))
	if err != nil {
		t.Fatal(err)
	}
	snap, _, err := buildDemoFleet(bundlesFS(t), partners)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

// demoClusterID is the kube-system UID the demo's hub cluster reports.
const demoClusterID = "5b0c6c3e-7f1a-4c55-9d1e-2b8a4e6f0a11"

// demoHrefs are the demo hrefs published outside the app: the docs' links
// into the demo, the tours' hashes and any hash in the demo transcripts.
func demoHrefs(t *testing.T) []string {
	t.Helper()
	docLink := regexp.MustCompile(`/demo/(#/[^"'\s)<>]+)`)
	hash := regexp.MustCompile(`"(#/[^"]+)"`)
	var out []string
	collect := func(path string, re *regexp.Regexp) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllSubmatch(b, -1) {
			out = append(out, string(m[1]))
		}
	}
	walk := func(root, ext string, re *regexp.Regexp) {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ext) {
				collect(path, re)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	walk("../../docs", ".md", docLink)
	walk("generated", ".md", hash)
	collect("tour.js", hash)
	collect("tours.js", hash)
	return out
}

// entityRoutes map an app route that names one entity to its entity kind.
var entityRoutes = []struct{ prefix, kind string }{
	{"#/fleet/targets/", "target"},
	{"#/fleet/services/", "service"},
	{"#/fleet/changes/", "service"},
	{"#/fleet/graph/service/", "service"},
	{"#/fleet/offerings/", "offering"},
}

// TestDemoHubAndMembersKeepHrefs: the demo is a hub with two members, so its
// target keys are cluster-qualified, and every href published outside the
// app still names exactly one entity: no 409 (a 3-part key that names targets
// in two clusters) and no 404.
func TestDemoHubAndMembersKeepHrefs(t *testing.T) {
	snap := demoFleet(t)
	q := fleet.NewQuery(snap)
	h := demoHandler(dashboard.Options{Fleet: func(context.Context) (*fleet.Query, error) { return q, nil }})
	hrefs := demoHrefs(t)
	resolved := 0
	for _, href := range hrefs {
		path, _, _ := strings.Cut(href, "?")
		for _, r := range entityRoutes {
			enc, ok := strings.CutPrefix(path, r.prefix)
			if !ok || enc == "" || strings.Contains(enc, "/") {
				continue
			}
			key, err := url.PathUnescape(enc)
			if err != nil {
				t.Fatalf("%s: %v", href, err)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fleet/entities/"+r.kind+"?key="+url.QueryEscape(key), nil))
			if rec.Code != http.StatusOK {
				t.Errorf("%s: %s %q answers %d: %s", href, r.kind, key, rec.Code, rec.Body.String())
			}
			resolved++
		}
	}
	if resolved < 10 {
		t.Fatalf("resolved %d entity hrefs of %d collected; the collector lost them", resolved, len(hrefs))
	}
}

// TestDemoClusterListMatchesSources: the join list's approved members are the
// fleet's member clusters by name, each read through a source signed by its
// member key; the pending cluster has no source and no target; the hub's
// targets carry the hub; members add no limitation.
func TestDemoClusterListMatchesSources(t *testing.T) {
	snap := demoFleet(t)
	clusters := map[string]string{}
	for _, c := range snap.Clusters {
		clusters[c.ID] = c.Name
	}
	checkDemoJoinList(t, demoClusterJoins(), clusters)
	checkDemoMembersInFleet(t, snap)
	// Pinned from feat/452-adoption-w1, before any source reported a cluster.
	want := []string{"OWNER_CONFLICT fleet", "SOURCE_PARTIAL registry-mirror", "SOURCE_PARTIAL registry-mirror"}
	var got []string
	for _, lim := range snap.Limitations {
		got = append(got, lim.Code+" "+lim.Source)
	}
	if !slices.Equal(got, want) {
		t.Errorf("limitations changed:\n got %v\nwant %v", got, want)
	}
}

// checkDemoJoinList: the hub is production, eu-prod and us-west are approved
// and in the fleet by those names, ap-dev is pending, hosts dev and is not.
func checkDemoJoinList(t *testing.T, l delivery.ClusterJoinList, clusters map[string]string) {
	t.Helper()
	if l.Hub.UID != demoClusterID || clusters[demoClusterID] != "production" || !l.CanApprove || l.Completeness != fleet.CompletenessComplete {
		t.Errorf("hub = %+v, clusters = %v", l.Hub, clusters)
	}
	states := map[delivery.JoinState][]string{}
	for _, c := range l.Clusters {
		states[c.State] = append(states[c.State], c.Name)
		checkDemoJoinRow(t, c, clusters)
	}
	if !slices.Equal(states[delivery.JoinApproved], []string{"eu-prod", "us-west"}) || !slices.Equal(states[delivery.JoinPending], []string{"ap-dev"}) {
		t.Errorf("states = %v", states)
	}
}

// checkDemoJoinRow: an approved row names its fleet cluster, approver and
// record; a pending one is not in the fleet, says why and hosts dev; each
// carries its key.
func checkDemoJoinRow(t *testing.T, c delivery.ClusterJoinState, clusters map[string]string) {
	t.Helper()
	_, inFleet := clusters[c.UID]
	approved := c.State == delivery.JoinApproved
	if approved && (clusters[c.UID] != c.Name || c.Approver != demoApprover || c.Record == "") {
		t.Errorf("approved %s: fleet names it %q; %+v", c.Name, clusters[c.UID], c)
	}
	if !approved && (inFleet || c.Reason == "" || len(c.Environments) != 1 || c.Environments[0].Name != "dev") {
		t.Errorf("pending %s: in the fleet %v; %+v", c.Name, inFleet, c)
	}
	if len(c.KeyID) != 64 || c.PublicKey == "" {
		t.Errorf("%s: key %q", c.Name, c.KeyID)
	}
}

// checkDemoMembersInFleet: each member source is signed by its member key,
// hub targets carry the hub and member targets sit in namespaces the hub
// never uses.
func checkDemoMembersInFleet(t *testing.T, snap *fleet.FleetSnapshot) {
	t.Helper()
	for _, st := range snap.Sources {
		if st.Kind == fleet.SourceKindTargetSnapshot && (st.Cluster == nil || st.Signer != "member:"+st.Cluster.Name) {
			t.Errorf("member source %s: signer %q cluster %+v", st.ID, st.Signer, st.Cluster)
		}
	}
	for _, tg := range snap.Targets {
		if tg.Cluster == demoClusterID && (tg.ClusterName != "production" || tg.ClusterNameSource != fleet.ClusterNameFromContext) {
			t.Errorf("%s: name %q source %q, want the hub", tg.Key, tg.ClusterName, tg.ClusterNameSource)
		}
		if tg.Cluster != demoClusterID && tg.Scope != "eu-payments" && tg.Scope != "us-ledger" {
			t.Errorf("%s: a member target in a namespace the hub uses", tg.Key)
		}
	}
}
