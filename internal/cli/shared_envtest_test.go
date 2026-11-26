//go:build perfenvtest

package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/delivery"
)

// TestSharedReadsRBACIsolation proves, against a real apiserver, that the
// dashboard's shared cache shows each person only what their own RBAC lets
// them read: alice reads namespace iso-a, reader every namespace, and carol
// nothing, although carol's token claims to be reader, so a dashboard that
// trusted the claims over the apiserver would show her everything.
func TestSharedReadsRBACIsolation(t *testing.T) {
	alice := perfPerson{name: "alice", groups: []string{"team-a"}, token: idToken("alice", []string{"team-a"})}
	reader := perfPerson{name: "reader", groups: []string{"auditors"}, token: idToken("reader", []string{"auditors"})}
	carol := perfPerson{name: "carol", token: idToken("reader", []string{"auditors"}) + "-carol"}
	cp := startControlPlane(t, []perfPerson{alice, reader, carol})
	ctx := context.Background()
	adm, err := dynamic.NewForConfig(cp.config(cp.admin, -1))
	must(t, err)
	installCRDs(t, ctx, adm)
	seedIsolation(t, ctx, adm)

	// Lists that reach the apiserver with a person's token, not the dashboard's.
	var personLists atomic.Int64
	newClient := func(c *rest.Config) (dynamic.Interface, error) {
		c = rest.CopyConfig(c)
		c.Wrap(func(rt http.RoundTripper) http.RoundTripper {
			return roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet && r.Header.Get("Authorization") != "Bearer "+perfDashboardSA.token &&
					strings.Contains(r.URL.Path, "delivery.pacto.trianalab.io") {
					personLists.Add(1)
				}
				return rt.RoundTrip(r)
			})
		})
		return dynamic.NewForConfig(c)
	}
	type tokenKey struct{}
	id := &dashboardIdentity{cluster: cp.config(perfDashboardSA.token, 0), auth: authn.NewShared(&authn.OIDC{}, nil), policyNS: perfPolicyNS,
		registry: func(...string) app.DeliveryRegistry { return perfRegistry{} }, newClient: newClient, now: time.Now,
		served: func(*rest.Config, string) (bool, error) { return true, nil },
		state: func(ctx context.Context) authn.State {
			tok, _ := ctx.Value(tokenKey{}).(string)
			return authn.State{SignedIn: true, Token: tok, Person: authn.Claims{}.Person(tok)}
		},
		revisions: map[string]delivery.Revision{}, reads: newReadCache()}
	as := func(p perfPerson) dashboard.DeliveryAPI {
		api, err := id.delivery(httptest.NewRequestWithContext(context.WithValue(ctx, tokenKey{}, p.token), http.MethodGet, "/", nil))
		must(t, err)
		return api
	}
	as(alice)
	waitShared(t, id)

	forbidden := func(err error) bool {
		var de *dashboard.DeliveryError
		return errors.As(err, &de) && de.Status == http.StatusForbidden
	}
	stagesIn := func(api dashboard.DeliveryAPI, ns string) []string {
		st, err := api.Stages(ctx, ns)
		must(t, err)
		var out []string
		for _, s := range st {
			out = append(out, s.Metadata.Namespace)
		}
		return out
	}

	before := personLists.Load()
	a := as(alice)
	_, err = a.Stages(ctx, "")
	expect(t, forbidden(err), "alice may not list every namespace: %v", err)
	expect(t, strings.Join(stagesIn(a, "iso-a"), ",") == "iso-a", "alice reads iso-a")
	_, err = a.Stages(ctx, "iso-b")
	expect(t, forbidden(err), "alice may not list iso-b: %v", err)
	_, err = a.Stage(ctx, "iso-b", "s")
	expect(t, forbidden(err), "alice may not get iso-b's Stage: %v", err)
	_, err = a.Get(ctx, "iso-b", "op")
	expect(t, forbidden(err), "alice may not get iso-b's Operation: %v", err)
	ops, err := a.Operations(ctx, "iso-a")
	expect(t, err == nil && len(ops) == 1, "alice reads iso-a's Operations: %v %v", ops, err)

	c := as(carol)
	for _, ns := range []string{"", "iso-a", "iso-b"} {
		_, err = c.Stages(ctx, ns)
		expect(t, forbidden(err), "carol, claiming to be reader, may not list %q: %v", ns, err)
	}
	who, err := c.WhoAmI(ctx)
	expect(t, err == nil && who.User == carol.user(), "carol is who the apiserver says: %+v %v", who, err)

	expect(t, strings.Join(stagesIn(as(reader), ""), ",") == "iso-a,iso-b", "reader reads every namespace")
	expect(t, personLists.Load() == before, "the cache answered every read: %d reached the apiserver", personLists.Load()-before)

	// A removed RoleBinding hides the namespace once the answer expires.
	must(t, adm.Resource(gvrRB).Namespace("iso-a").Delete(ctx, "team-a", metav1.DeleteOptions{}))
	deadline := time.Now().Add(sharedTTL + 20*time.Second)
	for {
		if _, err = as(alice).Stages(ctx, "iso-a"); forbidden(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("alice still reads iso-a after her RoleBinding went: %v", err)
		}
		time.Sleep(time.Second)
	}
}

// seedIsolation creates iso-a and iso-b with one Stage and one Operation each,
// the perf namespaces installRBAC binds, the RBAC and alice's team-a binding in
// iso-a.
func seedIsolation(t *testing.T, ctx context.Context, adm dynamic.Interface) {
	t.Helper()
	for _, ns := range []string{"iso-a", "iso-b"} {
		must(t, create(ctx, adm.Resource(gvrNS), &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": ns}}}, false))
		must(t, create(ctx, adm.Resource(gvrPerfStage).Namespace(ns), toU(delivery.StageObject{Metadata: delivery.ObjectMeta{Namespace: ns, Name: "s"},
			Spec: delivery.StageSpec{Service: delivery.ServiceRef{Name: "svc"}, Environment: "prod", ApplyRef: delivery.ApplyRef{Name: "s"}}},
			"delivery.pacto.trianalab.io/v1", "Stage"), false))
		must(t, create(ctx, adm.Resource(gvrPerfOp).Namespace(ns), toU(delivery.OperationObject{Metadata: delivery.ObjectMeta{Namespace: ns, Name: "op"},
			Spec: delivery.OperationSpec{Type: delivery.TypePromote, Stage: "s", Facet: delivery.FacetPointer, Params: delivery.Params{From: "sha256:a", To: "sha256:b"}}},
			"delivery.pacto.trianalab.io/v1", "Operation"), false))
	}
	// installRBAC binds alice and bob in the perf namespaces too.
	for _, ns := range append(readable("bob"), perfPolicyNS) {
		must(t, create(ctx, adm.Resource(gvrNS), &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": ns}}}, false))
	}
	installRBAC(t, ctx, adm) // auditors read everything, the dashboard its cache
	binding := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding",
		"metadata": map[string]any{"name": "team-a", "namespace": "iso-a"},
		"subjects": []any{map[string]any{"kind": "Group", "name": "team-a", "apiGroup": "rbac.authorization.k8s.io"}},
		"roleRef":  map[string]any{"kind": "ClusterRole", "name": "pacto-delivery-reader", "apiGroup": "rbac.authorization.k8s.io"}}}
	must(t, create(ctx, adm.Resource(gvrRB).Namespace("iso-a"), binding, false))
}

// waitShared waits for the dashboard's shared cache to sync.
func waitShared(t *testing.T, id *dashboardIdentity) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for _, inf := range id.shared.informers {
		for !inf.HasSynced() {
			if time.Now().After(deadline) {
				t.Fatal("the shared cache never synced: does the dashboard's ServiceAccount read Stages and Operations?")
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// TestTokenSignInAgainstAPIServer proves the token classification against a
// real apiserver: a ServiceAccount token minted for the dashboard signs in as
// a link session, although the apiserver refuses it as a bearer; one minted
// for the default audience is refused with TokenAudienceRequired; a person's
// static token signs in as a bearer session; and a dashboard that may not
// review tokens names the missing right.
func TestTokenSignInAgainstAPIServer(t *testing.T) {
	ann := perfPerson{name: "ann", token: "ann-static-token"}
	cp := startControlPlane(t, []perfPerson{ann})
	ctx := context.Background()
	adm, err := dynamic.NewForConfig(cp.config(cp.admin, -1))
	must(t, err)
	rbac := func(kind, name string, extra map[string]any) *unstructured.Unstructured {
		m := map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": kind, "metadata": map[string]any{"name": name}}
		for k, v := range extra {
			m[k] = v
		}
		return &unstructured.Unstructured{Object: m}
	}
	must(t, create(ctx, adm.Resource(gvrCRole), rbac("ClusterRole", "pacto-dashboard-tokenreview", map[string]any{"rules": []any{
		map[string]any{"apiGroups": []any{"authentication.k8s.io"}, "resources": []any{"tokenreviews"}, "verbs": []any{"create"}}}}), false))
	must(t, create(ctx, adm.Resource(gvrCRB), rbac("ClusterRoleBinding", "pacto-dashboard-tokenreview", map[string]any{
		"subjects": []any{map[string]any{"kind": "User", "name": perfDashboardSA.user(), "apiGroup": "rbac.authorization.k8s.io"}},
		"roleRef":  map[string]any{"kind": "ClusterRole", "name": "pacto-dashboard-tokenreview", "apiGroup": "rbac.authorization.k8s.io"}}), false))
	must(t, create(ctx, adm.Resource(gvrNS), &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": "pacto"}}}, false))
	gvrSA := schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}
	must(t, create(ctx, adm.Resource(gvrSA).Namespace("pacto"), &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": "pacto-viewer"}}}, false))
	mint := func(audiences ...string) string {
		spec := map[string]any{"expirationSeconds": int64(600)}
		if audiences != nil {
			spec["audiences"] = audiences
		}
		out, err := adm.Resource(gvrSA).Namespace("pacto").Create(ctx, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "authentication.k8s.io/v1", "kind": "TokenRequest", "metadata": map[string]any{"name": "pacto-viewer"}, "spec": spec}}, metav1.CreateOptions{}, "token")
		must(t, err)
		tok, _, _ := unstructured.NestedString(out.Object, "status", "token")
		return tok
	}
	linkToken, defaultToken := mint(authn.DashboardAudience), mint()

	cluster := cp.config("", 0)
	routes := func(asUser string) *authn.Dashboard {
		own, err := dynamic.NewForConfig(cp.config(asUser, 0))
		must(t, err)
		review := authn.DashboardTokenReview(authn.AudienceReview(own, authn.DashboardAudience), authn.SelfSubjectReview(cluster, func(c *rest.Config) (dynamic.Interface, error) { return dynamic.NewForConfig(c) }))
		return authn.NewShared(nil, authn.NewTokenLogin(review, authn.Lifetime{}))
	}
	signIn := func(d *authn.Dashboard, body string) (*httptest.ResponseRecorder, authn.State) {
		r := httptest.NewRequest(http.MethodPost, "https://dash.example/auth/token", strings.NewReader(body))
		r.Header.Set("Origin", "https://dash.example")
		rec := httptest.NewRecorder()
		d.Routes().ServeHTTP(rec, r)
		var st authn.State
		get := httptest.NewRequest(http.MethodGet, "https://dash.example/api/fleet/services", nil)
		for _, c := range rec.Result().Cookies() {
			get.AddCookie(c)
		}
		d.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { st = authn.StateFrom(r.Context()) })).ServeHTTP(httptest.NewRecorder(), get)
		return rec, st
	}
	d := routes(perfDashboardSA.token)

	rec, st := signIn(d, `{"token":"`+linkToken+`","link":true}`)
	expect(t, rec.Code == http.StatusNoContent && st.SignedIn && st.Token == "" && st.Person.User == "system:serviceaccount:pacto:pacto-viewer" && st.UID != "",
		"a token minted for the dashboard signs in as a link session: %d %s %+v", rec.Code, rec.Body, st)

	rec, _ = signIn(d, `{"token":"`+defaultToken+`"}`)
	expect(t, rec.Code == http.StatusUnauthorized && strings.Contains(rec.Body.String(), "TokenAudienceRequired") &&
		strings.Contains(rec.Body.String(), "kubectl create token pacto-viewer -n pacto --audience pacto-dashboard"),
		"a default-audience ServiceAccount token is refused: %d %s", rec.Code, rec.Body)

	asBearer, err := dynamic.NewForConfig(authn.RESTConfig(cluster, linkToken))
	must(t, err)
	_, _, err = authn.ReviewSelf(ctx, asBearer)
	expect(t, apierrors.IsUnauthorized(err), "the apiserver refuses a dashboard-audience token as a bearer: %v", err)

	rec, st = signIn(d, `{"token":"`+ann.token+`"}`)
	expect(t, rec.Code == http.StatusNoContent && st.Token == ann.token && st.Person.User == ann.user(),
		"a person's static token signs in as a bearer session: %d %s %+v", rec.Code, rec.Body, st)

	rec, _ = signIn(routes(ann.token), `{"token":"`+linkToken+`","link":true}`)
	expect(t, rec.Code == http.StatusUnauthorized && strings.Contains(rec.Body.String(), "create on tokenreviews"),
		"a dashboard that may not review tokens names the right: %d %s", rec.Code, rec.Body)
	rec, _ = signIn(routes(ann.token), `{"token":"`+defaultToken+`"}`)
	expect(t, rec.Code == http.StatusUnauthorized && strings.Contains(rec.Body.String(), "create on tokenreviews") &&
		!strings.Contains(rec.Body.String(), "TokenAudienceRequired"),
		"a ServiceAccount token it could not review for the audience names the right, not a new token: %d %s", rec.Code, rec.Body)
}
