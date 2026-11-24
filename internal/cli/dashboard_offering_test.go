package cli

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
	"github.com/trianalab/pacto/v4/pkg/publish"
)

// sourceDiscovery serves the API discovery of a cluster that serves the
// cluster-scoped SourceKind (example.io/v1, sourcekinds) and the namespaced
// Widget (example.io/v1, widgets).
func sourceDiscovery(t *testing.T) *rest.Config {
	t.Helper()
	body := map[string]string{
		"/api":    `{"kind":"APIVersions","versions":["v1"]}`,
		"/api/v1": `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"v1","resources":[]}`,
		"/apis":   `{"kind":"APIGroupList","apiVersion":"v1","groups":[{"name":"example.io","versions":[{"groupVersion":"example.io/v1","version":"v1"}],"preferredVersion":{"groupVersion":"example.io/v1","version":"v1"}}]}`,
		"/apis/example.io/v1": `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"example.io/v1","resources":[` +
			`{"name":"sourcekinds","singularName":"sourcekind","namespaced":false,"kind":"SourceKind","verbs":["get","update"]},` +
			`{"name":"widgets","singularName":"widget","namespaced":true,"kind":"Widget","verbs":["get","update"]}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		b, ok := body[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			b = `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`
		}
		_, _ = w.Write([]byte(b))
	}))
	t.Cleanup(srv.Close)
	return &rest.Config{Host: srv.URL}
}

var xbucketSource = &fleet.SourceObject{APIVersion: "example.io/v1", Kind: "SourceKind", Name: "xbuckets.example.io", Generation: 2}

// offeringHost is a host whose one cluster c1 serves sourceDiscovery and
// whose detection advertises xbucket, contributed by c1's installation;
// it returns the inferred offering as the viewer's fleet names it.
func offeringHost(t *testing.T, local bool) (*declareHost, dashboard.DeclareWorkload) {
	t.Helper()
	h := newDeclareHost(t, local, true, "c1")
	cfg := sourceDiscovery(t)
	h.d.entries = func(context.Context) []app.K8sEntry { return []app.K8sEntry{{Name: "c1", UID: "c1", Config: cfg}} }
	from, detected := h.w.offeringContribution(t)
	h.d.detected = detected
	ref := contract.TrimOCIScheme(from)
	head, err := publish.NewClient(oci.NewKeychain(oci.EnvCredentialOptions()), h.w.host).Head(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	repo, _, _ := contract.SplitOCIRef(ref)
	return h, dashboard.DeclareWorkload{Key: "xbucket-key", Name: "xbucket", Offering: "xbucket", Cluster: "c1", From: repo + "@" + head.Digest, Source: xbucketSource}
}

func (h *declareHost) storage() *dashboard.OfferingInput {
	return &dashboard.OfferingInput{Version: "1.0.0", Owner: "team-storage", Registry: h.w.host + "/acme/offerings"}
}

func TestDashboardAcceptOfferingPlan(t *testing.T) {
	h, w := offeringHost(t, true)
	ans := h.claimPlan(t, w, dashboard.DeclareInput{OfferingAcceptance: h.storage()})
	p := ans.Plan
	expect(t, ans.Refusal == nil && p != nil && p.BundleRepo == h.w.host+"/acme/offerings/xbucket" && p.Version == "1.0.0" &&
		p.InferredFrom == selfFrom(w) && strings.HasPrefix(p.InferredFrom, "offering:idp-c1/xbucket@sha256:"), "%+v %+v", ans.Refusal, p)
	expect(t, strings.HasPrefix(ans.Command, "pacto declare offering:xbucket --from "+w.From+" --detected ") && strings.HasSuffix(ans.Command, " --intent "+p.IntentDigest) &&
		ans.PRCLI == "" && ans.NoDraft == "", "%q %q", ans.Command, ans.PRCLI)
	// No offering input plans the offering the fleet names, and is refused for
	// its missing version.
	none := h.claimPlan(t, w, dashboard.DeclareInput{})
	expect(t, none.Refusal != nil && none.Refusal.Code == "VersionRequired" && strings.HasPrefix(none.Command, "pacto declare offering:xbucket "), "%+v", none.Refusal)
	// Another registry than the dashboard's is refused before anything is read.
	off := h.storage()
	off.Registry = "evil.example/acme"
	out := h.claimPlan(t, w, dashboard.DeclareInput{OfferingAcceptance: off})
	expect(t, out.Plan == nil && out.Refusal != nil && out.Refusal.Code == "NotAllowed" && strings.Contains(out.Refusal.Message, "publishes only under"), "%+v", out.Refusal)
	acc := h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: "x", Mode: "sign", OfferingAcceptance: off})
	expect(t, acc.Refusal != nil && acc.Refusal.Code == "NotAllowed", "%+v", acc.Refusal)
}

// TestDashboardAcceptOfferingSharedSignsOnly: a shared host never writes an
// offering draft with its own push credential; a local one does.
func TestDashboardAcceptOfferingSharedSignsOnly(t *testing.T) {
	h, w := offeringHost(t, false)
	ans := h.claimPlan(t, w, dashboard.DeclareInput{OfferingAcceptance: h.storage()})
	expect(t, ans.NoDraft == noOfferingDraft, "%+v", ans)
	for _, src := range []*fleet.SourceObject{xbucketSource, nil} {
		w.Source = src
		out := h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: ans.Plan.IntentDigest, Mode: "draft", OfferingAcceptance: h.storage()})
		expect(t, out.Refusal != nil && out.Refusal.Code == "DraftNotOffered" && out.Tag == "", "%+v", out)
	}
	expect(t, len(h.repoTags(t, "offerings/xbucket")) == 0 && len(h.reviews) == 0, "pushed %v reviews %v", h.repoTags(t, "offerings/xbucket"), h.reviews)
	lh, lw := offeringHost(t, true)
	p := lh.claimPlan(t, lw, dashboard.DeclareInput{OfferingAcceptance: lh.storage()})
	out := lh.accept(t, lw, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "draft", OfferingAcceptance: lh.storage()})
	expect(t, out.Refusal == nil && out.Tag == "draft-1" && len(lh.reviews) == 0, "%+v %+v", out, out.Refusal)
	line := lh.log.String()
	for _, want := range []string{"verb=accept-offering", "subject=offering:xbucket", "actor=ana@acme.com", "tag=draft-1"} {
		expect(t, strings.Contains(line, want), "audit line %q lacks %q", line, want)
	}
}

// TestDashboardAcceptOfferingReviewsSourceObject: signing on a shared host
// asks update on the source object in the cluster that advertised it, and
// refuses without it.
func TestDashboardAcceptOfferingReviewsSourceObject(t *testing.T) {
	h, w := offeringHost(t, false)
	h.allow = func(string, string) (bool, error) { return false, nil }
	out := h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: "x", Mode: "sign", OfferingAcceptance: h.storage()})
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed" && out.Refusal.Message == "accepting xbucket needs update on SourceKind xbuckets.example.io", "%+v", out.Refusal)
	expect(t, slices.Equal(h.reviews, []string{"c1//sourcekinds/update/xbuckets.example.io"}), "reviews %v", h.reviews)
	h.allow = func(string, string) (bool, error) { return false, errors.New("boom") }
	w.Source = &fleet.SourceObject{APIVersion: "example.io/v1", Kind: "Widget", Namespace: "storage", Name: "w1"}
	out = h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: "x", Mode: "sign", OfferingAcceptance: h.storage()})
	expect(t, out.Refusal != nil && out.Refusal.Code == "ReviewFailed" && strings.Contains(out.Refusal.Message, "Widget storage/w1"), "%+v", out.Refusal)
	expect(t, len(h.repoTags(t, "offerings/xbucket")) == 0, "pushed %v", h.repoTags(t, "offerings/xbucket"))
	// Allowed, the person's fresh login signs the declared offering.
	h, w = offeringHost(t, false)
	h.ownerPolicy(t, "ana@acme.com")
	h.freshLogin(t, personToken())
	p := h.claimPlan(t, w, dashboard.DeclareInput{OfferingAcceptance: h.storage()})
	expect(t, p.Refusal == nil && p.Signing.Mode == "fresh-login", "%+v %+v", p.Refusal, p.Signing)
	signed := h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: p.Plan.IntentDigest, Mode: "sign", OfferingAcceptance: h.storage()})
	expect(t, signed.Refusal == nil && signed.Tag == "1.0.0" && signed.Signed, "%+v %+v", signed, signed.Refusal)
	expect(t, slices.Equal(h.repoTags(t, "offerings/xbucket"), []string{"1.0.0"}), "tags %v", h.repoTags(t, "offerings/xbucket"))
}

// TestDashboardAcceptOfferingMemberClusterFailsClosed: a cluster the
// dashboard reads no discovery of, and one whose reviewer cannot review the
// person (a member cluster), refuse.
func TestDashboardAcceptOfferingMemberClusterFailsClosed(t *testing.T) {
	h, w := offeringHost(t, false)
	w.Cluster = "m1"
	out := h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: "x", Mode: "sign", OfferingAcceptance: h.storage()})
	expect(t, out.Refusal != nil && out.Refusal.Code == "ClusterNotReviewable", "%+v", out.Refusal)
	w.Cluster = "c1"
	h.allow = func(string, string) (bool, error) { return false, authn.ErrClusterNotReviewable }
	out = h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: "x", Mode: "sign", OfferingAcceptance: h.storage()})
	expect(t, out.Refusal != nil && out.Refusal.Code == "ClusterNotReviewable", "%+v", out.Refusal)
	// A source object Pacto could not read is never taken as none.
	w.Source, w.SourceUnread = nil, "the file could not be read"
	out = h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: "x", Mode: "sign", OfferingAcceptance: h.storage()})
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed" && strings.Contains(out.Refusal.Message, "the file could not be read"), "%+v", out.Refusal)
	// A kind discovery cannot map is refused, never allowed.
	w.Source = &fleet.SourceObject{APIVersion: "example.io/v1", Kind: "Gadget", Name: "g"}
	h.allow = func(string, string) (bool, error) { return true, nil }
	out = h.accept(t, w, dashboard.DeclareAcceptInput{IntentDigest: "x", Mode: "sign", OfferingAcceptance: h.storage()})
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed" && strings.Contains(out.Refusal.Message, "cannot tell which resource"), "%+v", out.Refusal)
	expect(t, len(h.repoTags(t, "offerings/xbucket")) == 0, "pushed %v", h.repoTags(t, "offerings/xbucket"))
}

// TestDashboardAcceptOfferingNoSourceObjectNoReview: an offering a producer
// the trust config grants (no cluster) is reviewed nowhere: the owner rule
// the person's login runs is its gate.
func TestDashboardAcceptOfferingNoSourceObjectNoReview(t *testing.T) {
	h, w := offeringHost(t, false)
	w.Cluster, w.Source = "", nil
	expect(t, h.d.mayAccept(t.Context(), w, dashboard.DeclareAcceptInput{Mode: "sign"}) == nil && len(h.reviews) == 0, "reviews %v", h.reviews)
	out := h.d.mayAccept(t.Context(), w, dashboard.DeclareAcceptInput{Mode: "draft"})
	expect(t, out != nil && out.Code == "DraftNotOffered", "%+v", out)
}

var (
	oldDigest = "sha256:" + strings.Repeat("1", 64)
	newDigest = "sha256:" + strings.Repeat("2", 64)
)

func TestDashboardReacceptOfferingNewDigestAllowed(t *testing.T) {
	older, newer := contract.OfferingInferredFrom("idp-c1", "xbucket", oldDigest), contract.OfferingInferredFrom("idp-c1", "xbucket", newDigest)
	p := &app.DeclarePlan{InferredFrom: newer, Baseline: &app.DeclareBaseline{From: app.Declared{InferredFrom: older}},
		Drafts: []app.DraftView{{Tag: "draft-1", From: app.Declared{InferredFrom: older}}}}
	expect(t, ownedBy(p) == nil, "a re-acceptance after a source change was refused")
	w := dashboard.DeclareWorkload{Name: "xbucket", Offering: "xbucket", From: "r.example/acme/pacto-inferred/idp-c1/services@" + newDigest}
	expect(t, selfFrom(w) == newer && ownView(p, w, nil) == nil && len(p.Drafts) == 1, "%q %+v", selfFrom(w), p.Drafts)
	expect(t, ownView(&app.DeclarePlan{InferredFrom: older}, w, &dashboard.DraftRef{Tag: "draft-1"}) == nil, "an older draft of the offering is its own")
}

func TestDashboardAcceptOfferingOtherProducerRefused(t *testing.T) {
	mine := contract.OfferingInferredFrom("idp-c1", "xbucket", newDigest)
	for _, other := range []string{contract.OfferingInferredFrom("idp-c2", "xbucket", newDigest), contract.OfferingInferredFrom("idp-c1", "xqueue", newDigest),
		"k8s:billing/Deployment/xbucket"} {
		p := &app.DeclarePlan{InferredFrom: mine, BundleRepo: "r/acme/offerings/xbucket", Baseline: &app.DeclareBaseline{From: app.Declared{InferredFrom: other}}}
		refusal := ownedBy(p)
		expect(t, refusal != nil && refusal.Code == "NotAllowed" && refusal.Message == "r/acme/offerings/xbucket belongs to "+other, "%s: %+v", other, refusal)
	}
}

func TestDashboardDeclarerResolvesSourceObjectResource(t *testing.T) {
	cfg := sourceDiscovery(t)
	res, ns, ok := sourceResource(cfg, *xbucketSource)
	expect(t, ok && ns == "" && res == authn.ResourceAttributes{Group: "example.io", Resource: "sourcekinds", Verb: "update", Name: "xbuckets.example.io"}, "%+v %q %v", res, ns, ok)
	res, ns, ok = sourceResource(cfg, fleet.SourceObject{APIVersion: "example.io/v1", Kind: "Widget", Namespace: "storage", Name: "w1"})
	expect(t, ok && ns == "storage" && res.Resource == "widgets" && res.Name == "w1", "%+v %q %v", res, ns, ok)
}

func TestDashboardDeclarerUnmappedKindRefused(t *testing.T) {
	cfg := sourceDiscovery(t)
	for name, c := range map[string]struct {
		cfg *rest.Config
		src fleet.SourceObject
	}{
		"unmapped kind":       {cfg, fleet.SourceObject{APIVersion: "example.io/v1", Kind: "Gadget", Name: "g"}},
		"bad apiVersion":      {cfg, fleet.SourceObject{APIVersion: "a/b/c", Kind: "SourceKind", Name: "g"}},
		"namespaced, no ns":   {cfg, fleet.SourceObject{APIVersion: "example.io/v1", Kind: "Widget", Name: "w1"}},
		"no discovery client": {&rest.Config{Host: "https://x", TLSClientConfig: rest.TLSClientConfig{CAData: []byte("not a certificate")}}, *xbucketSource},
	} {
		if res, ns, ok := sourceResource(c.cfg, c.src); ok || res != (authn.ResourceAttributes{}) || ns != "" {
			t.Errorf("%s: %+v %q %v", name, res, ns, ok)
		}
	}
}
