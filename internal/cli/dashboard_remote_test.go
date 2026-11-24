package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// remoteReviews records every review asked of the eu-prod cluster and
// answers it with allow; the hub reviews nothing here.
type remoteReviews struct {
	mu    sync.Mutex
	asked []string
	allow func(p authn.Person, ns string, res authn.ResourceAttributes) (bool, error)
}

func (rr *remoteReviews) reviews(context.Context) map[string]authn.Review {
	return map[string]authn.Review{"uid-hub": func(context.Context, authn.Person, string, authn.ResourceAttributes) (bool, error) { return true, nil },
		"uid-eu": func(_ context.Context, p authn.Person, ns string, res authn.ResourceAttributes) (bool, error) {
			rr.mu.Lock()
			rr.asked = append(rr.asked, p.User+"|"+ns+"|"+res.Resource+"|"+res.Verb+"|"+res.Name)
			rr.mu.Unlock()
			return rr.allow(p, ns, res)
		}}
}

// onlyProd lets a person list and get delivery objects in prod only.
func onlyProd(_ authn.Person, ns string, _ authn.ResourceAttributes) (bool, error) {
	return ns == "prod", nil
}

// remoteIdentity is a dashboard on the hub that also reads eu-prod, whose
// Stages are payments-prod in prod and payments-staging in staging; with rr
// set, every eu-prod read is reviewed through it.
func remoteIdentity(t *testing.T, rr *remoteReviews) *dashboardIdentity {
	t.Helper()
	id := identityFor(t, viper.New(), "127.0.0.1", &rest.Config{Host: "https://hub"})
	d := deliveryDeps("alice", nil)
	id.newClient = func(cfg *rest.Config) (dynamic.Interface, error) {
		c, err := d.newDynamic()
		if cfg.Host == "https://eu" {
			st := u("delivery.pacto.trianalab.io/v1alpha1", "Stage", "payments-staging", map[string]any{
				"spec": map[string]any{"service": map[string]any{"name": "payments"}, "applyRef": map[string]any{"name": "payments"}}})
			st.SetNamespace("staging")
			_ = c.(*dynamicfake.FakeDynamicClient).Tracker().Add(st)
		}
		return c, err
	}
	id.entries = entriesOf(entry("hub", "https://hub", "uid-hub"), entry("eu-prod", "https://eu", "uid-eu"))
	id.homeUID = func(context.Context) (string, bool) { return "uid-hub", true }
	if rr != nil {
		id.viewer = authn.NewViewer(rr.reviews)
	}
	return id
}

var dave = authn.Person{User: "dave", Groups: []string{"team-payments"}}

// euRemote is the one remote of id, read for dave.
func euRemote(t *testing.T, id *dashboardIdentity) dashboard.RemoteDelivery {
	t.Helper()
	home, rs := id.remotesFor(context.Background(), dave)
	expect(t, home == fleet.Cluster{ID: "uid-hub", Name: "hub"} && len(rs) == 1 && rs[0].Cluster == fleet.Cluster{ID: "uid-eu", Name: "eu-prod"},
		"home %+v remotes %+v", home, rs)
	return rs[0]
}

func stageNames(sts []delivery.StageObject) []string {
	var out []string
	for _, s := range sts {
		out = append(out, s.Metadata.Namespace+"/"+s.Metadata.Name)
	}
	slices.Sort(out)
	return out
}

func TestRemotesOnePerContextSkipsHomeAndAlias(t *testing.T) {
	id := remoteIdentity(t, nil)
	alias, clash, broken := entry("eu-admin", "https://eu", "uid-eu"), entry("restored", "https://r", "uid-eu"), entry("ap-dev", "https://ap", "")
	alias.Alias, clash.Conflict, broken.Err = "eu-prod", "eu-prod", errors.New("no such context")
	id.entries = entriesOf(entry("hub", "https://hub", "uid-hub"), entry("eu-prod", "https://eu", "uid-eu"), alias, clash, broken, entry("unread", "https://x", ""))
	home, rs := id.remotesFor(context.Background(), dave)
	// A conflicting or unidentified context is read as partial, never left out.
	expect(t, home.ID == "uid-hub" && len(rs) == 4 && rs[0].Cluster.ID == "uid-eu" && rs[0].API != nil && rs[0].Err == nil && rs[0].Context == "eu-prod" &&
		rs[1].Cluster.ID == "restored" && strings.Contains(fmt.Sprint(rs[1].Err), "cluster id of context eu-prod") &&
		rs[2].Cluster == fleet.Cluster{ID: "ap-dev", Name: "ap-dev"} && rs[2].Err != nil &&
		rs[3].Cluster.ID == "unread" && errors.Is(rs[3].Err, errClusterUnidentified), "home %+v remotes %+v", home, rs)
	// The sign-in cluster is found by server when its id is unknown.
	id.homeUID = nil
	id.entries = entriesOf(entry("hub", "https://hub", "uid-hub"))
	home, rs = id.remotesFor(context.Background(), dave)
	expect(t, home.ID == "" && rs == nil, "one cluster has no remote: %+v %+v", home, rs)
	// A client that cannot be built makes the remote partial.
	id.entries = entriesOf(entry("hub", "https://hub", "uid-hub"), entry("eu-prod", "https://eu", "uid-eu"))
	id.newClient = func(*rest.Config) (dynamic.Interface, error) { return nil, errors.New("bad config") }
	_, rs = id.remotesFor(context.Background(), dave)
	expect(t, len(rs) == 1 && rs[0].Err != nil && rs[0].API == nil, "remotes %+v", rs)
}

func TestLocalModeRemotesUnfiltered(t *testing.T) {
	id := remoteIdentity(t, nil)
	r := euRemote(t, id)
	sts, err := r.API.Stages(context.Background(), "")
	expect(t, err == nil && slices.Equal(stageNames(sts), []string{"prod/payments-prod", "staging/payments-staging"}),
		"local mode reads each context as its own user: %v %v", stageNames(sts), err)
	var o dashboard.Options
	id.options(&o)
	expect(t, o.DeliveryRemotes != nil, "a dashboard that reads contexts fans delivery out")
	home, rs := o.DeliveryRemotes(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, home.ID == "uid-hub" && len(rs) == 1, "the local request reads eu-prod: %+v %+v", home, rs)
	none := identityFor(t, viper.New(), "127.0.0.1", &rest.Config{Host: "https://hub"})
	none.entries = nil
	var no dashboard.Options
	none.options(&no)
	expect(t, no.DeliveryRemotes == nil, "no entries, no remotes")
}

func TestRemotesOffWithoutSignIn(t *testing.T) {
	v := viper.New()
	v.Set("dashboard.allow-anonymous", true)
	open := identityFor(t, v, "0.0.0.0", &rest.Config{Host: "https://hub"})
	open.entries = remoteIdentity(t, nil).entries
	home, rs := open.remotes(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, home.ID == "" && rs == nil, "open shared mode reads no remote: %+v", rs)
	v = viper.New()
	v.Set("dashboard.oidc-issuer-url", "https://idp.example")
	v.Set("dashboard.oidc-client-id", "c")
	v.Set("dashboard.oidc-redirect-url", "https://d/cb")
	shared := identityFor(t, v, "0.0.0.0", &rest.Config{Host: "https://hub"})
	shared.entries = open.entries
	_, rs = shared.remotes(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, rs == nil, "a signed-out request reads no remote: %+v", rs)
}

func TestRemotesOffForLinkSession(t *testing.T) {
	v := viper.New()
	v.Set("dashboard.token-login", true)
	id := identityFor(t, v, "0.0.0.0", &rest.Config{Host: "https://hub"})
	id.entries = remoteIdentity(t, nil).entries
	id.state = func(context.Context) authn.State {
		return authn.State{SignedIn: true, Method: "token", Person: authn.Person{User: "system:serviceaccount:pacto:pacto-viewer"}}
	}
	_, rs := id.remotes(httptest.NewRequest(http.MethodGet, "/", nil))
	expect(t, rs == nil, "a sign-in link session reads no remote: %+v", rs)
}

func TestRemoteListingFiltersByMayList(t *testing.T) {
	rr := &remoteReviews{allow: onlyProd}
	r := euRemote(t, remoteIdentity(t, rr))
	ctx := context.Background()
	sts, err := r.API.Stages(ctx, "")
	expect(t, err == nil && slices.Equal(stageNames(sts), []string{"prod/payments-prod"}), "stages %v %v", stageNames(sts), err)
	ops, err := r.API.Operations(ctx, "")
	expect(t, err == nil && len(ops) == 1 && ops[0].Metadata.Namespace == "prod", "ops %v %v", ops, err)
	expect(t, r.API.(interface{ HiddenNamespaces() int }).HiddenNamespaces() == 1, "staging is counted")
	expect(t, slices.Contains(rr.asked, "dave||stages|list|") && slices.Contains(rr.asked, "dave|prod|stages|list|") &&
		slices.Contains(rr.asked, "dave|staging|stages|list|") && slices.Contains(rr.asked, "dave|prod|operations|list|"),
		"every list is reviewed for dave in eu-prod: %v", rr.asked)
	// A cluster-wide reader is asked once per resource.
	all := &remoteReviews{allow: func(authn.Person, string, authn.ResourceAttributes) (bool, error) { return true, nil }}
	r = euRemote(t, remoteIdentity(t, all))
	sts, _ = r.API.Stages(ctx, "")
	_, _ = r.API.Operations(ctx, "")
	expect(t, len(sts) == 2 && len(all.asked) == 2, "cluster-wide reader: %v asked %v", stageNames(sts), all.asked)
}

// TestRemoteDeniedNamespaceCountedNotNamed reads the environments route of a
// dashboard whose reader may not list staging in eu-prod.
func TestRemoteDeniedNamespaceCountedNotNamed(t *testing.T) {
	id := remoteIdentity(t, &remoteReviews{allow: onlyProd})
	var o dashboard.Options
	id.options(&o)
	body := routeBody(t, dashboard.NewServer(o).APIHandler(), context.Background(), "/api/delivery/environments")
	expect(t, strings.Contains(body, `"hidden":1`) && strings.Contains(body, `"id":"uid-eu"`) && !strings.Contains(body, "staging"),
		"the denied namespace is counted and never named: %s", body)
}

func TestHiddenClusterCountedNotListed(t *testing.T) {
	id := remoteIdentity(t, &remoteReviews{allow: onlyProd})
	id.viewer = authn.NewViewer(func(context.Context) map[string]authn.Review {
		return map[string]authn.Review{"uid-hub": func(context.Context, authn.Person, string, authn.ResourceAttributes) (bool, error) { return true, nil }}
	})
	r := euRemote(t, id)
	expect(t, r.Hidden && r.API == nil, "a cluster nobody reviews dave in is hidden: %+v", r)
	var o dashboard.Options
	id.options(&o)
	body := routeBody(t, dashboard.NewServer(o).APIHandler(), context.Background(), "/api/delivery/environments")
	expect(t, strings.Contains(body, `"hiddenClusters":1`) && !strings.Contains(body, "uid-eu") && !strings.Contains(body, "eu-prod"),
		"the hidden cluster is counted, never listed: %s", body)
}

func TestRemoteHistoryNeedsReview(t *testing.T) {
	rr := &remoteReviews{allow: onlyProd}
	api := euRemote(t, remoteIdentity(t, rr)).API
	ctx := context.Background()
	notFound := func(what string, err error) {
		t.Helper()
		expect(t, isRefusal(err, http.StatusNotFound), "%s in staging answers as absent: %v", what, err)
	}
	_, err := api.History(ctx, "staging", "payments-staging", nil)
	notFound("history", err)
	_, err = api.Record(ctx, "staging", "payments-staging", "sha256:r")
	notFound("record", err)
	_, err = api.Blame(ctx, "staging", "payments-staging", "")
	notFound("blame", err)
	_, err = api.Stage(ctx, "staging", "payments-staging")
	notFound("stage", err)
	_, err = api.Get(ctx, "staging", "held")
	notFound("operation", err)
	_, err = api.Explain(ctx, "staging", "held")
	notFound("explain", err)
	expect(t, slices.Contains(rr.asked, "dave|staging|stages|get|payments-staging") && slices.Contains(rr.asked, "dave|staging|operations|get|held"),
		"single objects are reviewed with get on their name: %v", rr.asked)
	// prod reads pass through.
	h, err := api.History(ctx, "prod", "payments-prod", nil)
	expect(t, err == nil && h.Stage.Name == "payments-prod", "history %v %v", h, err)
	st, err := api.Stage(ctx, "prod", "payments-prod")
	expect(t, err == nil && st.Metadata.Name == "payments-prod", "stage %v %v", st, err)
	b, err := api.Blame(ctx, "prod", "payments-prod", "")
	expect(t, err == nil && b.Stage.Name == "payments-prod", "blame %v %v", b, err)
	_, err = api.Record(ctx, "prod", "payments-prod", "sha256:r")
	expect(t, isRefusal(err, http.StatusUnprocessableEntity), "record %v", err)
	op, err := api.Get(ctx, "prod", "held")
	expect(t, err == nil && op.Metadata.Name == "held", "get %v %v", op, err)
	e, err := api.Explain(ctx, "prod", "held")
	expect(t, err == nil && e.Operation == "prod/held", "explain %v %v", e, err)
	// An undo is never planned with the dashboard's credentials there.
	_, err = api.Undo(ctx, "prod", "held", "payments-prod")
	expect(t, isRefusal(err, http.StatusConflict) && strings.Contains(err.Error(), "use-context eu-prod && pacto delivery undo held -n prod"), "undo %v", err)
}

func TestRemoteReviewFailureHidesClusterPartial(t *testing.T) {
	failing := &remoteReviews{allow: func(authn.Person, string, authn.ResourceAttributes) (bool, error) {
		return false, errors.New("timeout")
	}}
	r := euRemote(t, remoteIdentity(t, failing))
	expect(t, errors.Is(r.Err, errRemoteReview) && r.API == nil, "a failed review hides the cluster: %+v", r)
	// A review that fails after the first answer hides the listing and refuses a read.
	calls := 0
	later := &remoteReviews{allow: func(_ authn.Person, ns string, _ authn.ResourceAttributes) (bool, error) {
		if calls++; calls > 1 {
			return false, errors.New("timeout")
		}
		return false, nil
	}}
	api := euRemote(t, remoteIdentity(t, later)).API
	_, err := api.Stages(context.Background(), "")
	expect(t, errors.Is(err, errRemoteReview), "stages: %v", err)
	_, err = api.Operations(context.Background(), "")
	expect(t, errors.Is(err, errRemoteReview), "operations: %v", err)
	_, err = api.Stage(context.Background(), "prod", "payments-prod")
	expect(t, isRefusal(err, http.StatusServiceUnavailable) && strings.Contains(err.Error(), "eu-prod"), "stage: %v", err)
	// A per-namespace failure on operations after a cluster-wide no.
	n := 0
	ops := &remoteReviews{allow: func(_ authn.Person, ns string, res authn.ResourceAttributes) (bool, error) {
		if res.Resource == "operations" && ns != "" {
			n++
			return false, errors.New("timeout")
		}
		return false, nil
	}}
	_, err = euRemote(t, remoteIdentity(t, ops)).API.Operations(context.Background(), "")
	expect(t, errors.Is(err, errRemoteReview) && n == 1, "operations per namespace: %v", err)
}

// TestReviewedRemoteOnlyReads: a reviewed remote reads with the dashboard's
// own credentials there, so it offers nothing else they could do.
func TestReviewedRemoteOnlyReads(t *testing.T) {
	api := euRemote(t, remoteIdentity(t, &remoteReviews{allow: onlyProd})).API
	ctx := context.Background()
	readOnly := func(what string, err error) {
		t.Helper()
		var de *dashboard.DeliveryError
		expect(t, errors.As(err, &de) && de.Code == "RemoteClusterReadOnly" && strings.Contains(de.Detail, "eu-prod"), "%s: %v", what, err)
	}
	_, err := api.WhoAmI(ctx)
	readOnly("whoami", err)
	_, err = api.Plan(ctx, delivery.Request{})
	readOnly("plan", err)
	_, _, err = api.Propose(ctx, delivery.Request{})
	readOnly("propose", err)
	_, err = api.Approve(ctx, "prod", "held", delivery.Approve, "")
	readOnly("approve", err)
	readOnly("cancel", api.Cancel(ctx, "prod", "held"))
	_, err = api.Trust(ctx, nil, nil)
	readOnly("trust", err)
	can, err := api.Can(ctx, "create", "operations", "prod")
	expect(t, !can && err == nil, "can %v %v", can, err)
	ns, err := api.StageNamespaces(ctx)
	expect(t, ns == nil && err == nil, "stage namespaces %v %v", ns, err)
	v, rows, readable, err := api.Environments(ctx)
	expect(t, v == nil && rows == nil && !readable && err == nil, "environments")
	pr, err := api.PublishRefusals(ctx, "")
	expect(t, pr == nil && err == nil, "refusals %v %v", pr, err)
	wa := api.WorkloadAccess(ctx, "prod", delivery.WorkloadRef{Kind: "Deployment", Name: "payments"})
	expect(t, !wa.Allowed && wa.Unverified != "" && wa.Name == "payments", "workload access %+v", wa)
	sts, _ := api.Stages(ctx, "")
	_ = api.Describe(ctx, &sts[0], "sha256:x") // read as on the dashboard's own cluster
}

func TestMemoEntriesKeepsAMinute(t *testing.T) {
	calls := 0
	m := memoEntries(func(context.Context) []app.K8sEntry { calls++; return []app.K8sEntry{{Name: "eu-prod"}} })
	m(context.Background())
	if got := m(context.Background()); len(got) != 1 || calls != 1 {
		t.Errorf("resolved %d times: %v", calls, got)
	}
	if memoEntries(nil) != nil {
		t.Error("no entries, nothing to keep")
	}
}

// twoClusterDeps is `pacto mcp --delivery` on the hub whose fleet also reads
// eu-prod, as remoteIdentity builds it.
func twoClusterDeps(t *testing.T) deps {
	t.Helper()
	id := remoteIdentity(t, nil)
	d := deliveryDeps("alice", nil)
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{Host: "https://hub"}, nil }
	d.dynamicFor = id.newClient
	d.resolveK8s = func(*app.Service, context.Context, bool, []string) []app.K8sEntry {
		return id.entries(context.Background())
	}
	d.served = func(*rest.Config, string) (bool, error) { return true, nil }
	return d
}

func TestMCPInboxFansOut(t *testing.T) {
	session := mcpReadServer(t, twoClusterDeps(t), nil)
	for _, route := range []string{"inbox", "environments"} {
		text, isErr := callText(t, session, "pacto_delivery_"+route, map[string]any{})
		expect(t, !isErr && strings.Contains(text, `"id":"uid-eu"`) && strings.Contains(text, `"cluster":"uid-eu"`),
			"%s reads eu-prod as the kubeconfig user: %s", route, text)
	}
}

func TestMCPHistoryByCluster(t *testing.T) {
	session := mcpReadServer(t, twoClusterDeps(t), nil)
	text, isErr := callText(t, session, "pacto_delivery_history", map[string]any{"namespace": "staging", "stage": "payments-staging", "cluster": "uid-eu"})
	expect(t, !isErr && strings.Contains(text, "payments-staging"), "the eu-prod Stage through the route: %s", text)
	text, isErr = callText(t, session, "pacto_delivery_history", map[string]any{"namespace": "prod", "stage": "payments-prod", "cluster": "uid-ap"})
	expect(t, isErr && strings.Contains(text, "ClusterUnknown"), "a cluster not read here: %s", text)
}

func TestMCPRemoteUsesCallerIdentity(t *testing.T) {
	rr := &remoteReviews{allow: onlyProd}
	id := remoteIdentity(t, rr)
	reads := mcpDeliveryReads(id, nil, "")(actFor(t, deliveryDeps("alice", nil)), remoteCallerKey(dave))
	body, err := reads(context.WithValue(context.Background(), reviewedKey{}, reviewed{person: dave}), "environments", nil)
	expect(t, err == nil && strings.Contains(string(body), `"id":"uid-eu"`) && !strings.Contains(string(body), "staging"), "dave's answer: %v %s", err, body)
	for _, a := range rr.asked {
		expect(t, strings.HasPrefix(a, "dave|"), "eu-prod reviewed someone else than the caller: %v", rr.asked)
	}
	// Nobody reviewed: no other cluster is read, never as this process.
	asked := len(rr.asked)
	body, err = reads(context.Background(), "environments", nil)
	expect(t, err == nil && !strings.Contains(string(body), "uid-eu") && len(rr.asked) == asked, "no caller: %v %s", err, body)
}

func TestReviewedRemoteListFailureKept(t *testing.T) {
	id := remoteIdentity(t, &remoteReviews{allow: onlyProd})
	next := id.newClient
	id.newClient = func(cfg *rest.Config) (dynamic.Interface, error) {
		c, err := next(cfg)
		c.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("connection refused")
		})
		return c, err
	}
	api := euRemote(t, id).API
	_, err := api.Stages(context.Background(), "")
	expect(t, err != nil && strings.Contains(err.Error(), "connection refused"), "stages: %v", err)
	_, err = api.Operations(context.Background(), "")
	expect(t, err != nil && strings.Contains(err.Error(), "connection refused"), "operations: %v", err)
}

// A restricted reader's plan names no consumer in a member cluster they may
// not see: that member is only counted, and an unnamed request sees none.
func TestViewedMembersHideClustersTheReaderMayNotSee(t *testing.T) {
	members := func(context.Context, string, map[string]bool) delivery.Members {
		return delivery.Members{Sources: []fleet.Source{
			fleet.NewMemorySource("member-uid-eu", fleet.SourceKindTargetSnapshot, &fleet.Collection{}),
			fleet.NewMemorySource("member-uid-us", fleet.SourceKindTargetSnapshot, &fleet.Collection{})}}
	}
	expect(t, viewedMembers(nil, members, personFrom) != nil && viewedMembers(authn.NewViewer(nil), nil, personFrom) == nil, "no viewer, no restriction")
	rr := &remoteReviews{allow: func(_ authn.Person, ns string, res authn.ResourceAttributes) (bool, error) {
		return ns == "" && res.Resource == authn.PactoResource.Resource, nil
	}}
	v := authn.NewViewer(rr.reviews)
	ids := func(m delivery.Members) []string {
		var out []string
		for _, s := range m.Sources {
			out = append(out, s.ID())
		}
		return out
	}
	named := viewedMembers(v, members, func(context.Context) (authn.Person, bool) { return dave, true })(t.Context(), "prod", nil)
	expect(t, slices.Equal(ids(named), []string{"member-uid-eu", delivery.MembersHiddenID}), "dave sees eu, not us: %v", ids(named))
	nobody := viewedMembers(v, members, personFrom)(t.Context(), "prod", nil)
	expect(t, slices.Equal(ids(nobody), []string{delivery.MembersHiddenID}), "an unnamed request sees no member: %v", ids(nobody))
}
