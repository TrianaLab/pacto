package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	pactomcp "github.com/trianalab/pacto/v4/internal/mcp"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

// hubList is a hub's join list: one member in each state.
func hubList() delivery.ClusterJoinList {
	return delivery.ClusterJoinList{Hub: delivery.ClusterRef{UID: clusterHubUID}, Repository: "ghcr.io/acme/pacto-targets", AsOf: "2026-11-20T10:00:00Z",
		Completeness: fleet.CompletenessComplete, Clusters: []delivery.ClusterJoinState{
			{UID: euProd, Name: "eu-prod", State: delivery.JoinApproved},
			{UID: "bbbb0000", Name: "us-west", State: delivery.JoinPending, Reason: "no approval yet"},
			{UID: "cccc0000", Name: "ap-old", State: delivery.JoinRemoved}}}
}

func listOf(l delivery.ClusterJoinList) func(context.Context) delivery.ClusterJoinList {
	return func(context.Context) delivery.ClusterJoinList { return l }
}

// approverWorld is a shared dashboard whose person is alice and whose
// ServiceAccount answers every review of approve on clusterjoins with
// allowed (or err), recording what it was asked.
func approverWorld(t *testing.T, allowed bool, err error) (*dashboardIdentity, *[]map[string]any) {
	t.Helper()
	id, _, _ := operationAccessWorld(t)
	var mu sync.Mutex
	var asked []map[string]any
	sa := fakeDelivery()
	sa.PrependReactor("create", "subjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		o := a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		spec, _, _ := unstructured.NestedMap(o.Object, "spec")
		mu.Lock()
		asked = append(asked, spec)
		mu.Unlock()
		o.Object["status"] = map[string]any{"allowed": allowed}
		return true, o, err
	})
	own, _ := id.newClient(&rest.Config{BearerToken: "x"})
	id.newClient = func(cfg *rest.Config) (dynamic.Interface, error) {
		if cfg.BearerToken != "" {
			return own, nil
		}
		return sa, nil
	}
	return id, &asked
}

func TestClusterListHidesPendingFromNonApprover(t *testing.T) {
	id, asked := approverWorld(t, false, nil)
	l, err := id.clusterJoins(listOf(hubList()))(t.Context())
	expect(t, err == nil && len(l.Clusters) == 1 && l.Clusters[0].UID == euProd && l.Hidden == 2 && !l.CanApprove,
		"a person who may not approve sees the approved member only: %+v %v", l, err)
	expect(t, len(*asked) == 1, "one review: %+v", *asked)
	attrs := (*asked)[0]["resourceAttributes"].(map[string]any)
	expect(t, (*asked)[0]["user"] == "alice" && attrs["verb"] == "approve" && attrs["group"] == "pacto.trianalab.io" &&
		attrs["resource"] == "clusterjoins" && attrs["name"] == "", "approve on every clusterjoin, asked for the person: %+v", (*asked)[0])

	id, _ = approverWorld(t, true, nil)
	l, _ = id.clusterJoins(listOf(hubList()))(t.Context())
	expect(t, len(l.Clusters) == 3 && l.Hidden == 0 && l.CanApprove, "an approver sees every row: %+v", l)

	id, _ = approverWorld(t, true, errors.New("apiserver down"))
	l, _ = id.clusterJoins(listOf(hubList()))(t.Context())
	expect(t, len(l.Clusters) == 1 && l.Hidden == 2 && l.Completeness == fleet.CompletenessPartial && !l.CanApprove,
		"a failed review hides and counts, partial: %+v", l)

	id, _ = approverWorld(t, true, nil)
	id.cluster = nil
	l, _ = id.clusterJoins(listOf(hubList()))(t.Context())
	expect(t, len(l.Clusters) == 1 && l.Completeness == fleet.CompletenessPartial, "no cluster to ask: %+v", l)
}

func TestClusterListUnauthenticatedShowsAll(t *testing.T) {
	local := identityFor(t, viper.New(), "127.0.0.1", &rest.Config{Host: "https://k"})
	v := viper.New()
	v.Set("dashboard.allow-anonymous", true)
	open := identityFor(t, v, "0.0.0.0", nil)
	for name, id := range map[string]*dashboardIdentity{"local": local, "open": open} {
		l, err := id.clusterJoins(listOf(hubList()))(t.Context())
		expect(t, err == nil && len(l.Clusters) == 3 && l.CanApprove, "%s: %+v", name, l)
	}
	expect(t, local.clusterJoins(nil) == nil, "no joins, no route")
}

// TestJoinedMemberHiddenAndCountedForSignedInReader: a signed-in reader
// reviewed in the hub never sees an approved member's services: no live
// channel reviews them there yet (D5).
func TestJoinedMemberHiddenAndCountedForSignedInReader(t *testing.T) {
	hub := &fleet.Cluster{ID: clusterHubUID, Name: "hub", NameSource: fleet.ClusterNameFromContext}
	member := &fleet.Cluster{ID: euProd, Name: "eu-prod", NameSource: fleet.ClusterNameFromApproval}
	target := func(ns, name string) fleet.RawTarget {
		return fleet.RawTarget{Scope: ns, Kind: fleet.SourceKindKubernetes, Name: name, Service: name, Compliance: fleet.StatusCompliant}
	}
	snap, err := fleet.Build(t.Context(), fleet.BuildOptions{},
		fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, &fleet.Collection{Cluster: hub, Targets: []fleet.RawTarget{target("shop", "web")}}),
		fleet.NewMemorySource("targets-"+euProd, fleet.SourceKindTargetSnapshot, &fleet.Collection{Cluster: member, Targets: []fleet.RawTarget{target("eu-payments", "ledger")}}))
	expect(t, err == nil, "build: %v", err)
	all := func(context.Context, authn.Person, string, authn.ResourceAttributes) (bool, error) { return true, nil }
	viewer := authn.NewViewer(func(context.Context) map[string]authn.Review { return map[string]authn.Review{clusterHubUID: all} })
	got, err := viewer.Query(t.Context(), authn.Person{User: alice}, fleet.NewQuery(snap))
	expect(t, err == nil, "view: %v", err)
	s := got.SnapshotForSerialization()
	var hidden []fleet.Limitation
	for _, l := range s.Limitations {
		if l.Code == fleet.LimitationClusterHidden {
			hidden = append(hidden, l)
		}
	}
	expect(t, len(hidden) == 1 && hidden[0].Source == euProd &&
		hidden[0].Message == "cluster eu-prod joined through the registry and has no live channel yet, so its services are hidden from signed-in readers",
		"CLUSTER_HIDDEN = %+v", hidden)
	expect(t, s.Completeness == fleet.CompletenessPartial && len(s.Services) == 1 && s.Services["web"] != nil, "partial, hub services only: %v", s.Services)
}

func TestJoinListerKeepsAListForItsTTL(t *testing.T) {
	expect(t, joinLister(app.NewService(nil, nil), nil, time.Now) == nil, "no joins, no lister")
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	opts := &app.JoinOptions{Repository: e.repo, Hub: clusterHubUID, Authorize: authn.SubjectAccessReview(e.hub()), Delivery: &app.Delivery{Policy: e.policy}}
	now := time.Now()
	list := joinLister(app.NewService(nil, nil), opts, func() time.Time { return now })
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // a cancelled request still reads the shared list
	expect(t, len(list(ctx).Clusters) == 1, "first read")
	e.publish(t, newJoinMember("dddd0000-0000-4000-8000-00000000000d", "us-west"), newJoinMember("x", "x").key)
	expect(t, len(list(t.Context()).Clusters) == 1, "kept within the TTL")
	now = now.Add(joinListTTL)
	expect(t, len(list(t.Context()).Clusters) == 2, "read again after the TTL")
}

func TestDashboardJoinsReadsTheSignInCluster(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	d := e.d
	d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) { return e.hub(), nil }
	cmd := &cobra.Command{}
	got := dashboardJoins(cmd, d, &rest.Config{}, detectedFile(t, e.repo), "pacto-system")
	expect(t, got != nil && got.Repository == e.repo && got.Hub == clusterHubUID && got.Delivery.PolicyNamespace == "pacto-system", "%+v", got)
	expect(t, dashboardJoins(cmd, d, nil, detectedFile(t, e.repo), "") == nil, "no sign-in cluster")
	expect(t, dashboardJoins(cmd, d, &rest.Config{}, "", "") == nil, "no detection")
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) { return nil, errors.New("no client") }
	expect(t, dashboardJoins(cmd, d, &rest.Config{}, detectedFile(t, e.repo), "") == nil && strings.Contains(stderr.String(), "no client"), "%q", stderr.String())
}

func TestMCPClustersReviewsSignedInCallers(t *testing.T) {
	svc := app.NewService(nil, nil)
	expect(t, mcpClusters(svc, defaultDeps(), nil, true) == nil, "no joins, no tool")
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	opts := &app.JoinOptions{Repository: e.repo, Hub: clusterHubUID, Authorize: authn.SubjectAccessReview(e.hub()), Delivery: &app.Delivery{Policy: e.policy}}
	l, err := mcpClusters(svc, e.d, opts, false)(t.Context())
	expect(t, err == nil && len(l.Clusters) == 1 && l.CanApprove, "a server without sign-in lists everything: %+v", l)

	d := e.d
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{}, nil }
	d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) { return e.hub(), nil }
	p := mcpClusters(svc, d, opts, true)
	l, _ = p(t.Context())
	expect(t, len(l.Clusters) == 0 && l.Hidden == 1 && l.Completeness == fleet.CompletenessPartial, "nobody reviewed: %+v", l)
	as := func(user string) context.Context {
		return context.WithValue(t.Context(), reviewedKey{}, reviewed{person: authn.Person{User: user}})
	}
	l, _ = p(as(alice))
	expect(t, len(l.Clusters) == 1 && l.CanApprove, "alice may approve: %+v", l)
	l, _ = p(as("oidc:bob@acme.com"))
	expect(t, len(l.Clusters) == 0 && l.Hidden == 1 && !l.CanApprove, "bob may not: %+v", l)
}

// TestClusterListSurfacesAgree: pacto cluster list, pacto_cluster_list and
// GET /api/fleet/clusters answer one list alike.
func TestClusterListSurfacesAgree(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	out, _, err := e.run(t, "list", "--output-format", "json")
	expect(t, err == nil, "cli: %v", err)
	var cli delivery.ClusterJoinList
	_ = json.Unmarshal([]byte(out), &cli)
	fixed := listOf(cli)

	server, err := pactomcp.New("v", pactomcp.Authoring{}, pactomcp.Modes{Fleet: &pactomcp.FleetMode{
		Src:  func(context.Context) (*fleet.Query, error) { return fleet.NewQuery(&fleet.FleetSnapshot{}), nil },
		Opts: pactomcp.FleetOptions{Clusters: func(ctx context.Context) (delivery.ClusterJoinList, error) { return fixed(ctx), nil }}}})
	expect(t, err == nil, "mcp: %v", err)
	tool, isErr := callText(t, sessionOn(t, server), "pacto_cluster_list", map[string]any{})
	expect(t, !isErr, "call: %s", tool)
	o := dashboard.Options{ClusterJoins: func(ctx context.Context) (delivery.ClusterJoinList, error) { return fixed(ctx), nil }}
	route := routeBody(t, dashboard.NewServer(o).APIHandler(), t.Context(), "/api/fleet/clusters")
	sameAnswer(t, "cli and mcp", out, tool)
	sameAnswer(t, "mcp and route", tool, route)
	expect(t, len(cli.Clusters) == 1, "one member: %s", out)
}

// TestVisibleJoins: an approver sees every row; anyone else the approved
// members, the rest counted; a failed review hides and counts them and
// makes the list partial.
func TestVisibleJoins(t *testing.T) {
	v := visibleJoins(hubList(), true, nil)
	expect(t, len(v.Clusters) == 3 && v.Hidden == 0 && v.CanApprove, "approver = %+v", v)
	v = visibleJoins(hubList(), false, nil)
	expect(t, len(v.Clusters) == 1 && v.Clusters[0].UID == euProd && v.Hidden == 2 && !v.CanApprove && v.Completeness == fleet.CompletenessComplete,
		"non-approver = %+v", v)
	v = visibleJoins(hubList(), true, errors.New("timeout"))
	expect(t, len(v.Clusters) == 1 && v.Hidden == 2 && !v.CanApprove && v.Completeness == fleet.CompletenessPartial &&
		len(v.Limitations) == 1 && strings.HasSuffix(v.Limitations[0].Message, ": timeout"), "failed review = %+v", v)
}
