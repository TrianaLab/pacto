package fleetsrc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/trianalab/pacto/v4/internal/k8sclient"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

// gitopsServed serves every GitOps group.
var gitopsServed = map[string]string{"argoproj.io": "v1alpha1", "kustomize.toolkit.fluxcd.io": "v1", "helm.toolkit.fluxcd.io": "v2", "source.toolkit.fluxcd.io": "v1"}

// listedAs is a status.resources entry of an Application for a Deployment.
func listedAs(ns, name string) string {
	return fmt.Sprintf(`{"group":"apps","kind":"Deployment","namespace":%q,"name":%q}`, ns, name)
}

func appJSON(ns, name, spec string, listed ...string) string {
	return fmt.Sprintf(`{"metadata":{"name":%q,"namespace":%q},"spec":%s,"status":{"resources":[%s]}}`, name, ns, spec, strings.Join(listed, ","))
}

func kustomizationJSON(ns, name, sourceRef, path string, ids ...string) string {
	entries := make([]string, len(ids))
	for i, id := range ids {
		entries[i] = fmt.Sprintf(`{"id":%q,"v":"v1"}`, id)
	}
	return fmt.Sprintf(`{"metadata":{"name":%q,"namespace":%q},"spec":{"sourceRef":%s,"path":%q,"postBuild":{"substitute":{"token":"hunter2-subst"}}},"status":{"inventory":{"entries":[%s]}}}`,
		name, ns, sourceRef, path, strings.Join(entries, ","))
}

func helmReleaseJSON(ns, name, spec string) string {
	return fmt.Sprintf(`{"metadata":{"name":%q,"namespace":%q},"spec":%s}`, name, ns, spec)
}

func gitRepoJSON(ns, name, url string) string {
	return fmt.Sprintf(`{"metadata":{"name":%q,"namespace":%q},"spec":{"url":%q,"secretRef":{"name":"git-creds"}}}`, name, ns, url)
}

func decodeAs[T any](t *testing.T, js string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(js), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// tracked is a Deployment ns/name that Argo CD's tracking id puts in app.
func tracked(ns, name, app string) string {
	return groupedWorkload(ns, name, nil, map[string]string{"argocd.argoproj.io/tracking-id": app + ":apps/Deployment:" + ns + "/" + name})
}

func raw(ns, name string, g fleet.InferredGroup) fleet.RawInferredService {
	return fleet.RawInferredService{Namespace: ns, Kind: "Deployment", Name: name, Group: &g}
}

func argoGroup(ns, name string) fleet.InferredGroup {
	return fleet.InferredGroup{Rule: workloadmanager.ArgoCD, Namespace: ns, Name: name, From: "annotation argocd.argoproj.io/tracking-id"}
}

func workloadOf(t *testing.T, snap *fleet.FleetSnapshot, key string) *fleet.InferredWorkload {
	t.Helper()
	s := snap.Service(key)
	if s == nil || s.Workload == nil {
		t.Fatalf("no inferred %s", key)
	}
	return s.Workload
}

func TestCleanRepoURLStripsCredentials(t *testing.T) {
	for in, want := range map[string]string{
		"https://user:tok@github.com/a/b.git?x=1#f":            "https://github.com/a/b.git",
		"https://github.com/a/b?private_token=s3cr3t":          "https://github.com/a/b",
		"git@github.com:a/b.git":                               "github.com:a/b.git",
		"ssh://git@host/a":                                     "ssh://host/a",
		"https://x-access-token:ghs_abc@github.com/acme/shop/": "https://github.com/acme/shop/",
		"git@host:a/b?token=x#frag":                            "host:a/b",
		// A password in scp-like userinfo never reaches the host:path split.
		"deploy:ghp_SECRET@github.com:acme/shop.git":    "github.com:acme/shop.git",
		"oauth2:glpat-SECRET@gitlab.com:group/repo.git": "gitlab.com:group/repo.git",
		"github.com:org/repo@v1":                        "github.com:org/repo@v1",
	} {
		if got, ok := CleanRepoURL(in); !ok || got != want {
			t.Errorf("CleanRepoURL(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "https://github.com/a\x00b", "https://[::1", "https://h/%zz", "file:///repo", "not a url", "relative/path", "host:", ":path", "a/b:c",
		"https:user:pw@host", "deploy:tok@git.example.com/x", "a@b:"} {
		if got, ok := CleanRepoURL(in); ok {
			t.Errorf("CleanRepoURL(%q) = %q, want refused", in, got)
		}
	}
}

func TestResolveArgoRepository(t *testing.T) {
	idx := newGitopsIndex(nil)
	idx.addApplication(decodeAs[application](t, appJSON("argocd", "cart",
		`{"source":{"repoURL":"https://tok@github.com/acme/shop.git","path":"deploy/cart"},"sources":[{"repoURL":"https://github.com/acme/shop.git","path":"deploy/cart"},{"repoURL":"https://github.com/acme/values","path":"cart"}]}`,
		listedAs("shop", "cart"))))
	g, repos, why := idx.resolveGroup(raw("shop", "cart", argoGroup("", "cart")))
	want := []fleet.InferredRepository{
		{URL: "https://github.com/acme/shop.git", Path: "deploy/cart", From: "Application argocd/cart spec.source.repoURL", Namespace: "argocd", Kind: "Application"},
		{URL: "https://github.com/acme/values", Path: "cart", From: "Application argocd/cart spec.sources[1].repoURL", Namespace: "argocd", Kind: "Application"},
	}
	if g.Rule != workloadmanager.ArgoCD || g.Namespace != "" || !reflect.DeepEqual(repos, want) || why != "" {
		t.Errorf("resolved %+v %+v %q", g, repos, why)
	}
	idx.addApplication(decodeAs[application](t, appJSON("shop", "redis", `{"sources":[{"repoURL":"https://charts.bitnami.com/bitnami","chart":"redis"},{"repoURL":"oci://registry/charts","chart":"other"}]}`, listedAs("shop", "redis"))))
	if _, repos, why := idx.resolveGroup(raw("shop", "redis", argoGroup("shop", "redis"))); repos != nil || why != "the source is Helm chart redis, not a Git repository" {
		t.Errorf("chart source = %+v %q", repos, why)
	}
	idx.addApplication(decodeAs[application](t, appJSON("shop", "bare", `{"source":{"repoURL":"%zz"}}`, listedAs("shop", "bare"))))
	if _, repos, why := idx.resolveGroup(raw("shop", "bare", argoGroup("shop", "bare"))); repos != nil || why != "Application shop/bare names no Git repository Pacto can show" {
		t.Errorf("no usable source = %+v %q", repos, why)
	}
	var many []string
	for i := range 7 {
		many = append(many, fmt.Sprintf(`{"repoURL":"https://github.com/acme/r%d"}`, i))
	}
	idx.addApplication(decodeAs[application](t, appJSON("shop", "many", `{"sources":[`+strings.Join(many, ",")+`]}`, listedAs("shop", "many"))))
	// The source sends every repository; pkg/fleet keeps the bound and
	// records the rest as dropped, so the list never reads as complete.
	if _, repos, _ := idx.resolveGroup(raw("shop", "many", argoGroup("shop", "many"))); len(repos) != 7 {
		t.Errorf("sent %d repositories, want all 7", len(repos))
	}
}

func TestResolveArgoRepositoriesPastTheBoundAreDropped(t *testing.T) {
	var many []string
	for i := range 7 {
		many = append(many, fmt.Sprintf(`{"repoURL":"https://github.com/acme/r%d"}`, i))
	}
	fake := &workloadFake{versions: gitopsServed, pages: map[string][2]string{
		"deployments||":  {tracked("shop", "cart", "argocd_cart")},
		"applications||": {appJSON("argocd", "cart", `{"sources":[`+strings.Join(many, ",")+`]}`, listedAs("shop", "cart"))},
	}}
	w := workloadOf(t, buildOne(t, NewWorkloadSource("w", nil, fake, nil)), "k8s:shop/Deployment/cart")
	if len(w.Repositories) != fleet.MaxInferredRepositories || !slices.Equal(w.Dropped, []string{"repositories[5]", "repositories[6]"}) {
		t.Errorf("repositories %d, dropped %v", len(w.Repositories), w.Dropped)
	}
}

func TestUnknownFromNamesTheObjectsTheReasonWasReadFrom(t *testing.T) {
	idx := newGitopsIndex(nil)
	idx.addApplication(decodeAs[application](t, appJSON("argocd", "solo", `{}`)))
	idx.addKustomization(decodeAs[kustomization](t, kustomizationJSON("flux-system", "apps", `{"kind":"GitRepository","name":"platform"}`, "", "shop_api_apps_Deployment")))
	idx.addHelmRelease(decodeAs[helmRelease](t, helmReleaseJSON("shop", "rel", `{"chartRef":{"kind":"GitRepository","name":"charts","namespace":"flux-system"}}`)))
	idx.unknown["GitRepository"] = notServed + "GitRepositories"
	k := fleet.InferredGroup{Rule: workloadmanager.FluxKustomization, Namespace: "flux-system", Name: "apps"}
	h := fleet.InferredGroup{Rule: workloadmanager.FluxHelmRelease, Namespace: "shop", Name: "rel"}
	for name, c := range map[string]struct {
		w    fleet.RawInferredService
		why  string
		want []fleet.WorkloadScope
	}{
		"no reason":             {raw("shop", "x", argoGroup("argocd", "x")), "", nil},
		"no group":              {fleet.RawInferredService{}, "x", nil},
		"prefixed Application":  {raw("shop", "x", argoGroup("argocd", "x")), "no Application named argocd/x was read", []fleet.WorkloadScope{{Namespace: "argocd", Kind: "Application"}}},
		"one unprefixed match":  {raw("shop", "solo", argoGroup("", "solo")), "Application argocd/solo does not list this workload", []fleet.WorkloadScope{{Namespace: "argocd", Kind: "Application"}}},
		"unresolved unprefixed": {raw("shop", "gone", argoGroup("", "gone")), "no Application named gone was read", []fleet.WorkloadScope{{Kind: "Application"}}},
		// The Kustomization exists, so its GitRepository reason discloses it.
		"Kustomization source": {raw("shop", "api", k), notServed + "GitRepositories", []fleet.WorkloadScope{{Namespace: "flux-system", Kind: "Kustomization"}, {Namespace: "flux-system", Kind: "GitRepository"}}},
		"HelmRelease chartRef": {raw("shop", "api", h), "no GitRepository named flux-system/charts was read", []fleet.WorkloadScope{{Namespace: "shop", Kind: "HelmRelease"}, {Namespace: "flux-system", Kind: "GitRepository"}}},
	} {
		if got := idx.unknownFrom(c.w, c.why); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", name, got, c.want)
		}
	}
	// A reason that names no object needs no review.
	for _, u := range []string{discoveryUnknown, notServed + "Applications"} {
		idx.unknown["Application"] = u
		if got := idx.unknownFrom(raw("shop", "x", argoGroup("argocd", "x")), u); got != nil {
			t.Errorf("%q: %+v", u, got)
		}
	}
}

func TestResolveOtherRulesNameNoRepository(t *testing.T) {
	idx := newGitopsIndex(nil)
	if g, repos, why := idx.resolveGroup(fleet.RawInferredService{}); g != nil || repos != nil || why != "" {
		t.Errorf("no group resolved to %+v %+v %q", g, repos, why)
	}
	helm := fleet.InferredGroup{Rule: workloadmanager.Helm, Namespace: "shop", Name: "exporter"}
	if g, repos, why := idx.resolveGroup(raw("shop", "x", helm)); *g != helm || repos != nil || why != "" {
		t.Errorf("Helm group resolved to %+v %+v %q", g, repos, why)
	}
}

func TestResolveRefusesUnlistedWorkload(t *testing.T) {
	idx := newGitopsIndex(nil)
	idx.addApplication(decodeAs[application](t, appJSON("payments", "payments-api", `{"source":{"repoURL":"https://github.com/acme/payments"}}`, listedAs("payments", "api"))))
	idx.addKustomization(decodeAs[kustomization](t, kustomizationJSON("flux-system", "apps", `{"kind":"GitRepository","name":"platform"}`, "./apps", "shop_web_apps_Deployment")))
	idx.addHelmRelease(decodeAs[helmRelease](t, helmReleaseJSON("observability", "log-shipper", `{"targetNamespace":"logging","chart":{"spec":{"chart":"./charts/x","sourceRef":{"kind":"GitRepository","name":"platform","namespace":"flux-system"}}}}`)))
	g := argoGroup("payments", "payments-api")
	got, repos, why := idx.resolveGroup(raw("team-x", "evil", g))
	if *got != g || repos != nil || why != "Application payments/payments-api does not list this workload" {
		t.Errorf("unlisted Argo CD workload = %+v %+v %q", got, repos, why)
	}
	k := fleet.InferredGroup{Rule: workloadmanager.FluxKustomization, Namespace: "flux-system", Name: "apps"}
	if _, repos, why := idx.resolveGroup(raw("team-x", "evil", k)); repos != nil || why != "Kustomization flux-system/apps does not list this workload" {
		t.Errorf("unlisted Kustomization workload = %+v %q", repos, why)
	}
	h := fleet.InferredGroup{Rule: workloadmanager.FluxHelmRelease, Namespace: "observability", Name: "log-shipper"}
	if _, repos, why := idx.resolveGroup(raw("team-x", "evil", h)); repos != nil || why != "HelmRelease observability/log-shipper installs into another namespace" {
		t.Errorf("HelmRelease into another namespace = %+v %q", repos, why)
	}
	// A status list past the bound may hide the workload: that is unknown, never "does not list".
	ids := make([]string, maxGitOpsStatusEntries+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("shop_w%d_apps_Deployment", i)
	}
	idx.addKustomization(decodeAs[kustomization](t, kustomizationJSON("flux-system", "big", `{"kind":"GitRepository","name":"platform"}`, "", ids...)))
	big := fleet.InferredGroup{Rule: workloadmanager.FluxKustomization, Namespace: "flux-system", Name: "big"}
	if _, _, why := idx.resolveGroup(raw("shop", "w10000", big)); why != "Pacto read only the first 10000 entries Kustomization flux-system/big lists" {
		t.Errorf("truncated inventory = %q", why)
	}
}

func TestResolveArgoNameMatchesManyIsAmbiguous(t *testing.T) {
	spec := `{"source":{"repoURL":"https://github.com/acme/shop"}}`
	fake := &workloadFake{versions: gitopsServed, pages: map[string][2]string{
		"deployments||":  {tracked("shop", "cart", "cart")},
		"applications||": {appJSON("argocd", "cart", spec, listedAs("shop", "cart")) + "," + appJSON("argocd-team-b", "cart", spec)},
	}}
	snap := buildOne(t, NewWorkloadSource("w", nil, fake, nil))
	w := workloadOf(t, snap, "k8s:shop/Deployment/cart")
	if w.Group.Rule != workloadmanager.Workload || w.Group.Label != "Ungrouped" || w.Group.Ambiguous != "Argo CD Application cart matches more than one Application" || w.Repositories != nil {
		t.Errorf("workload = %+v %+v", w.Group, w.Repositories)
	}
	if !hasCode(snap.Limitations, fleet.LimitationServiceGroupAmbiguous) {
		t.Errorf("limitations = %+v", snap.Limitations)
	}
	data, _ := json.Marshal(snap)
	if strings.Contains(string(data), "argocd-team-b") || strings.Contains(string(data), "argocd/cart") {
		t.Error("the ambiguity names an Application's namespace")
	}
	// Nor how many match: one may sit where the viewer lists no Application.
	if strings.Contains(w.Group.Ambiguous, "2") {
		t.Errorf("the ambiguity counts the Applications: %q", w.Group.Ambiguous)
	}
}

func TestResolveFluxKustomizationGitRepository(t *testing.T) {
	idx := newGitopsIndex(nil)
	idx.addKustomization(decodeAs[kustomization](t, kustomizationJSON("flux-system", "apps", `{"kind":"GitRepository","name":"platform"}`, "./apps/shop", "shop_api_apps_Deployment")))
	idx.addGitRepository(decodeAs[gitRepository](t, gitRepoJSON("flux-system", "platform", "https://user:pw@github.com/acme/platform")))
	k := fleet.InferredGroup{Rule: workloadmanager.FluxKustomization, Namespace: "flux-system", Name: "apps"}
	_, repos, why := idx.resolveGroup(raw("shop", "api", k))
	want := []fleet.InferredRepository{{URL: "https://github.com/acme/platform", Path: "./apps/shop", From: "GitRepository flux-system/platform spec.url", Namespace: "flux-system", Kind: "GitRepository"}}
	if !reflect.DeepEqual(repos, want) || why != "" {
		t.Errorf("resolved %+v %q", repos, why)
	}
	for ref, reason := range map[string]string{
		`{"kind":"OCIRepository","name":"manifests"}`:                "the source is OCIRepository manifests, not a GitRepository",
		`{"kind":"GitRepository","name":"gone","namespace":"other"}`: "no GitRepository named other/gone was read",
		`{"kind":"GitRepository","name":"badurl"}`:                   "GitRepository flux-system/badurl names no URL Pacto can show",
		`{"name":"nokind"}`: "no source reference was read",
		`{"kind":"GitRepository","name":"platform","namespace":"x-y"}`: "no GitRepository named x-y/platform was read",
	} {
		idx := newGitopsIndex(nil)
		idx.addKustomization(decodeAs[kustomization](t, kustomizationJSON("flux-system", "apps", ref, "", "shop_api_apps_Deployment")))
		idx.addGitRepository(decodeAs[gitRepository](t, gitRepoJSON("flux-system", "badurl", "not a url")))
		if _, repos, why := idx.resolveGroup(raw("shop", "api", k)); repos != nil || why != reason {
			t.Errorf("%s: %+v %q, want %q", ref, repos, why, reason)
		}
	}
}

func TestResolveFluxHelmReleaseNonGitSource(t *testing.T) {
	h := fleet.InferredGroup{Rule: workloadmanager.FluxHelmRelease, Namespace: "observability", Name: "log-shipper"}
	for spec, reason := range map[string]string{
		`{"chart":{"spec":{"chart":"podinfo","sourceRef":{"kind":"HelmRepository","name":"podinfo"}}},"values":{"password":"hunter2-values"}}`: "the source is HelmRepository podinfo, not a GitRepository",
		`{"chartRef":{"kind":"OCIRepository","name":"podinfo"}}`:                                                                               "the source is OCIRepository podinfo, not a GitRepository",
	} {
		idx := newGitopsIndex(nil)
		idx.addHelmRelease(decodeAs[helmRelease](t, helmReleaseJSON("observability", "log-shipper", spec)))
		if _, repos, why := idx.resolveGroup(raw("observability", "shipper", h)); repos != nil || why != reason {
			t.Errorf("%s: %+v %q, want %q", spec, repos, why, reason)
		}
	}
	idx := newGitopsIndex(nil)
	idx.addHelmRelease(decodeAs[helmRelease](t, helmReleaseJSON("observability", "log-shipper", `{"targetNamespace":"logging","chart":{"spec":{"chart":"./charts/shipper","sourceRef":{"kind":"GitRepository","name":"platform","namespace":"flux-system"}}}}`)))
	idx.addGitRepository(decodeAs[gitRepository](t, gitRepoJSON("flux-system", "platform", "https://github.com/acme/platform")))
	_, repos, why := idx.resolveGroup(raw("logging", "shipper", h))
	want := []fleet.InferredRepository{{URL: "https://github.com/acme/platform", Path: "./charts/shipper", From: "GitRepository flux-system/platform spec.url", Namespace: "flux-system", Kind: "GitRepository"}}
	if !reflect.DeepEqual(repos, want) || why != "" {
		t.Errorf("Git chart source = %+v %q", repos, why)
	}
}

func TestResolveRepositoryUnknownReasons(t *testing.T) {
	why := func(t *testing.T, fake *workloadFake, namespaces []string, key string) string {
		t.Helper()
		return workloadOf(t, buildOne(t, NewWorkloadSource("w", nil, fake, namespaces)), key).RepositoryUnknown
	}
	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "argoproj.io", Resource: "applications"}, "", errors.New("denied"))
	if got := why(t, &workloadFake{versions: gitopsServed, pages: map[string][2]string{"deployments||": {tracked("shop", "cart", "argocd_cart")}},
		errs: map[string]error{"applications|": forbidden}}, nil, "k8s:shop/Deployment/cart"); got != "Applications unreadable (forbidden)" {
		t.Errorf("forbidden = %q", got)
	}
	apps := make([]string, maxGitOpsObjects+1)
	for i := range apps {
		apps[i] = appJSON("argocd", fmt.Sprintf("a%d", i), `{}`)
	}
	if got := why(t, &workloadFake{versions: gitopsServed, pages: map[string][2]string{"deployments||": {tracked("shop", "cart", "argocd_cart")},
		"applications||": {strings.Join(apps[:3000], ","), "t1"}, "applications||t1": {strings.Join(apps[3000:], ",")}}}, nil, "k8s:shop/Deployment/cart"); got != "Pacto read only the first 5000 Applications" {
		t.Errorf("capped = %q", got)
	}
	if got := why(t, &workloadFake{versions: gitopsServed, pages: map[string][2]string{"deployments|shop|": {tracked("shop", "cart", "argocd_cart")}}}, []string{"shop"}, "k8s:shop/Deployment/cart"); got != "Applications outside the namespaces this source reads" {
		t.Errorf("unwatched = %q", got)
	}
	if got := why(t, &workloadFake{versions: gitopsServed, pages: map[string][2]string{"deployments||": {tracked("shop", "cart", "argocd_cart")}}}, nil, "k8s:shop/Deployment/cart"); got != "no Application named argocd/cart was read" {
		t.Errorf("missing = %q", got)
	}
	if got := why(t, &workloadFake{versions: gitopsServed, pages: map[string][2]string{"deployments||": {tracked("shop", "cart", "cart")}}}, nil, "k8s:shop/Deployment/cart"); got != "no Application named cart was read" {
		t.Errorf("missing in the Argo CD namespace = %q", got)
	}
	// An unprefixed name may live in any namespace: a scoped source read none,
	// which says nothing of the namespaces it does not read.
	if got := why(t, &workloadFake{versions: gitopsServed, pages: map[string][2]string{"deployments|shop|": {tracked("shop", "cart", "cart")}}}, []string{"argocd", "shop"}, "k8s:shop/Deployment/cart"); got != "no Application named cart was read in the namespaces this source reads" {
		t.Errorf("unprefixed on a scoped source = %q", got)
	}
	if got := why(t, &workloadFake{pages: map[string][2]string{"deployments||": {tracked("shop", "cart", "cart")}}}, nil, "k8s:shop/Deployment/cart"); got != "the cluster does not serve Applications" {
		t.Errorf("unserved = %q", got)
	}
}

func TestGitOpsDecodeDropsValues(t *testing.T) {
	leaks := []string{"hunter2", "hunter2-values", "hunter2-subst", "hunter2-bad", "git-creds"}
	fake := &workloadFake{versions: gitopsServed, pages: map[string][2]string{
		"deployments||": {tracked("shop", "cart", "argocd_cart")},
		"applications||": {appJSON("argocd", "cart", `{"source":{"repoURL":"https://github.com/acme/shop","helm":{"values":"password: hunter2","valuesObject":{"p":"hunter2"},"parameters":[{"name":"p","value":"hunter2"}]}}}`,
			listedAs("shop", "cart"))},
		"helmreleases||":    {helmReleaseJSON("shop", "x", `{"values":{"password":"hunter2-values"}}`)},
		"kustomizations||":  {kustomizationJSON("flux-system", "apps", `{"kind":"GitRepository","name":"platform"}`, "")},
		"gitrepositories||": {`{"metadata":"hunter2-bad"}`},
	}}
	col, err := NewWorkloadSource("w", nil, fake, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snap := buildOne(t, NewWorkloadSource("w", nil, fake, nil))
	flux := providerRow(snap, fleet.ProviderFlux)
	if flux == nil || !strings.Contains(flux.Evidence, "GitRepositories unreadable") {
		t.Fatalf("a page that failed decoding = %+v", flux)
	}
	for _, v := range []any{col, snap} {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		for _, leak := range leaks {
			if strings.Contains(string(data), leak) {
				t.Errorf("%T holds %q", v, leak)
			}
		}
	}
	for _, p := range col.Providers {
		for _, l := range p.Limitations {
			if strings.Contains(l.Message, "hunter2") || strings.Contains(l.Message, "metadata") {
				t.Errorf("limitation %q carries page content", l.Message)
			}
		}
	}
	if _, _, err := listItems[gitRepository](context.Background(), fake, k8sclient.WorkloadGVRs["Deployment"], "", "bad"); err != nil {
		t.Fatalf("an empty page failed: %v", err)
	}
	fake.pages["deployments||bad"] = [2]string{`"hunter2"`}
	if _, _, err := listItems[gitRepository](context.Background(), fake, k8sclient.WorkloadGVRs["Deployment"], "", "bad"); err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("decode error = %v", err)
	}
}

func TestWorkloadSourceAsksForNoSecret(t *testing.T) {
	fake := &workloadFake{versions: gitopsServed}
	if _, err := NewWorkloadSource("w", nil, fake, nil).Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	var resources []string
	for gvr := range fake.gvrs {
		resources = append(resources, gvr.Resource)
	}
	slices.Sort(resources)
	want := []string{"applications", "cronjobs", "daemonsets", "deployments", "gitrepositories", "helmreleases", "kustomizations", "statefulsets"}
	if !slices.Equal(resources, want) {
		t.Errorf("asked for %v, want %v", resources, want)
	}
	for gvr := range fake.gvrs {
		if gr, ok := k8sclient.GitOpsResources[map[string]string{"applications": "Application", "kustomizations": "Kustomization", "helmreleases": "HelmRelease", "gitrepositories": "GitRepository"}[gvr.Resource]]; ok && gvr.Version != gitopsServed[gr.Group] {
			t.Errorf("listed %v, not the preferred version", gvr)
		}
	}
	if !slices.Equal(fake.discovered, gitopsGroups()) {
		t.Errorf("discovered %v, want one call for %v", fake.discovered, gitopsGroups())
	}
}
