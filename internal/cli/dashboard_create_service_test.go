package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	gcrauthn "github.com/google/go-containerregistry/pkg/authn"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/dashboard"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/oci"
)

// createHost is a declareHost whose registry holds the pg offering and the
// go-http golden path, and whose fleet view holds both: pg consumable under
// a publisher rule, go-http pinned by digest.
type createHost struct {
	*declareHost
	c    *dashboardCreator
	path dashboard.CreateServicePath
	// revision is the revision the last plan answered.
	revision string
}

// goHTTPPath is the go-http golden path: a database on the pg offering.
func goHTTPPath(host string) string {
	return "pactoVersion: \"2.4\"\nservice:\n  name: go-http\n  version: 1.2.0\n  owner: {team: platform}\n  template: {}\n" +
		"dependencies:\n  - name: pg\n    ref: oci://" + host + "/acme/pg:17.0.0\n    compatibility: ^17.0.0\n    required: true\n" +
		"configurations:\n  - name: database\n    ref: oci://" + host + "/acme/pg:17.0.0\n    configuration: db\n" +
		"    values: {class: small, storageGB: 20, engine: postgres}\n    required: true\n"
}

const pgOffering = "pactoVersion: \"2.4\"\nservice:\n  name: pg\n  version: 17.0.0\n  offering: postgresql\nworkload: service\n" +
	"configurations:\n  - name: db\n    schema: configuration/schema.json\n    required: true\n"

// pushBundle pushes files to ref in h's registry and returns its digest.
func (h *declareHost) pushBundle(t *testing.T, ref string, files map[string]string) (string, *contract.Bundle) {
	t.Helper()
	fsys := fstest.MapFS{}
	for p, c := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(c), Mode: 0o644}
	}
	c, err := contract.Parse(strings.NewReader(files["pacto.yaml"]))
	if err != nil {
		t.Fatal(err)
	}
	b := &contract.Bundle{Contract: c, RawYAML: []byte(files["pacto.yaml"]), FS: fsys}
	digest, err := oci.NewClient(gcrauthn.DefaultKeychain, oci.WithInsecureRegistries(h.w.host)).Push(t.Context(), ref, b)
	if err != nil {
		t.Fatal(err)
	}
	return digest, b
}

func newCreateHost(t *testing.T, local, write bool, clusters ...string) *createHost {
	t.Helper()
	h := newDeclareHost(t, local, write, clusters...)
	host := h.w.host
	h.d.svc.BundleStore = oci.NewClient(gcrauthn.DefaultKeychain, oci.WithInsecureRegistries(host))
	pgFiles := map[string]string{"pacto.yaml": pgOffering, "configuration/schema.json": consumablePGSchema}
	pgDigest, pg := h.pushBundle(t, host+"/acme/pg:17.0.0", pgFiles)
	pathDigest, path := h.pushBundle(t, host+"/acme/paths/go-http:1.2.0", map[string]string{"pacto.yaml": goHTTPPath(host)})
	pin := "oci://" + host + "/acme/paths/go-http@" + pathDigest
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("oci", "oci", &fleet.Collection{Revisions: []fleet.RawRevision{
		{Bundle: pg, Domain: host + "/acme", RequestedRef: "oci://" + host + "/acme/pg:17.0.0", Digest: pgDigest,
			Provenance: &fleet.BundleProvenance{Source: "oci", Signature: fleet.BundleSignature{Status: fleet.SignatureVerified, Source: "rule", Signer: "platform@acme.com"}}},
		{Bundle: path, Domain: host + "/acme", RequestedRef: "oci://" + host + "/acme/paths/go-http:1.2.0", ResolvedRef: pin, Digest: pathDigest},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	q := fleet.NewQuery(snap)
	p, why := q.CreatablePath(fleet.NewServiceKeyDomain(host+"/acme", "go-http"))
	if why != "" {
		t.Fatal(why)
	}
	return &createHost{declareHost: h, c: &dashboardCreator{h.d}, path: dashboard.CreateServicePath{CreatablePath: p, View: q}}
}

var createIn = dashboard.CreateServiceInput{Name: "invoices", Owner: "billing", Namespace: "team-billing"}

func (h *createHost) plan(t *testing.T, in dashboard.CreateServiceInput) *dashboard.CreateServiceAnswer {
	t.Helper()
	ans, err := h.c.Plan(h.req(), h.path, in)
	if err != nil {
		t.Fatal(err)
	}
	if ans.Plan != nil {
		h.revision = ans.Plan.Revision
	}
	return ans
}

// accept accepts in, sending back the revision the last plan answered when
// in names none, as the page does.
func (h *createHost) accept(t *testing.T, in dashboard.CreateServiceInput, mode, intent string) *dashboard.CreateServiceAcceptAnswer {
	t.Helper()
	in.IntentDigest = intent
	if in.Revision == "" {
		in.Revision = h.revision
	}
	ans, err := h.c.Accept(h.req(), h.path, dashboard.CreateServiceAcceptInput{CreateServiceInput: in, Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	return ans
}

// allTrue fails t for every check that is false.
func allTrue(t *testing.T, checks map[string]bool, got any) {
	t.Helper()
	for name, ok := range checks {
		if !ok {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestCreateServicePlan(t *testing.T) {
	h := newCreateHost(t, true, true)
	ans := h.plan(t, createIn)
	expect(t, ans.Refusal == nil && ans.Plan != nil, "%+v", ans)
	p := ans.Plan
	allTrue(t, map[string]bool{
		"service":   p.Service == "invoices" && p.Version == "0.1.0",
		"repo":      p.BundleRepo == h.w.host+"/acme/invoices",
		"path":      p.Path == h.path.Ref && p.PathName == "go-http" && p.PathVersion == "1.2.0",
		"namespace": p.Namespace == "team-billing",
		"revision":  strings.HasPrefix(p.Revision, "pacto-create:"),
		"init":      strings.HasPrefix(p.InitCLI, "pacto init invoices --from"),
		"intent":    p.IntentDigest != "",
		"signature": p.Signature.Step == fleet.StepSignature,
		"complete":  p.ConsumableCompleteness == string(fleet.CompletenessComplete),
		"contract":  strings.Contains(p.Contract.Text, "name: invoices"),
		// The person cannot sign here: the plan is a draft on their behalf.
		"draft": p.State == contract.DeclarationDraft && ans.Signing.Mode == signModeNone && ans.SignTicket == "",
	}, p)
	o := ans.Offerings
	expect(t, len(o) == 1 && o[0].Offering == "postgresql" && o[0].Requested, "offerings %+v", o)
	expect(t, slices.Equal(o[0].Classes, []string{"small", "large"}) && len(o[0].Parameters) > 0, "offering %+v", o[0])
	set := createIn
	set.Offerings = []dashboard.CreateServiceOffering{{Offering: "postgresql", Values: map[string]any{"storageGB": 40}}}
	vf := h.plan(t, set).Plan.ValuesFile
	expect(t, vf != nil && vf.Path == app.CreateValuesFile && strings.Contains(vf.Text, "storageGB: 40"), "values file %+v", vf)
}

func TestCreateServiceParameterRefusal(t *testing.T) {
	h := newCreateHost(t, true, true)
	in := createIn
	in.Offerings = []dashboard.CreateServiceOffering{{Offering: "postgresql", Values: map[string]any{"storageGB": 900}}}
	ans := h.plan(t, in)
	expect(t, ans.Plan == nil && ans.Refusal != nil && ans.Refusal.Code == "ParameterOutOfRange" && len(ans.Refusals) == 1, "%+v", ans)
	r := ans.Refusals[0]
	expect(t, r.Code == "ParameterOutOfRange" && r.Field == "configurations[database].values.storageGB" && r.Closest == float64(500) &&
		strings.HasPrefix(r.CLI, "pacto init invoices") && strings.Contains(r.CLI, "storageGB=500") && strings.Contains(r.MCP, `"storageGB":500`), "%+v", r)
	in.Offerings = []dashboard.CreateServiceOffering{{Offering: "redis", Values: map[string]any{"memoryMi": 64}}}
	ans = h.plan(t, in)
	expect(t, len(ans.Refusals) == 1 && ans.Refusals[0].Code == "OfferingNotConsumable", "%+v", ans.Refusals)
}

func TestCreateServiceSARDenied403(t *testing.T) {
	h := newCreateHost(t, false, true)
	h.allow = func(string, string) (bool, error) { return false, nil }
	want := "ana@acme.com may not create services in team-billing"
	_, err := h.c.Plan(h.req(), h.path, createIn)
	var de *dashboard.DeliveryError
	expect(t, errors.As(err, &de) && de.Status == http.StatusForbidden && de.Code == "NotAllowed" && de.Detail == want, "plan: %v", err)
	_, err = h.c.Accept(h.req(), h.path, dashboard.CreateServiceAcceptInput{CreateServiceInput: createIn, Mode: "draft"})
	expect(t, errors.As(err, &de) && de.Status == http.StatusForbidden && de.Code == "NotAllowed", "accept: %v", err)
	expect(t, len(h.repoTags(t, "invoices")) == 0, "written: %v", h.repoTags(t, "invoices"))
	expect(t, slices.Equal(h.reviews, []string{"/team-billing/services/create/invoices"}), "reviews %v (the second is cached)", h.reviews)
}

func TestCreateServiceSARUnreviewable(t *testing.T) {
	h := newCreateHost(t, false, true)
	in := createIn
	in.Cluster = "edge"
	_, err := h.c.Plan(h.req(), h.path, in)
	var de *dashboard.DeliveryError
	expect(t, errors.As(err, &de) && de.Code == "ClusterNotReviewable", "unreviewable: %v", err)
	h.allow = func(string, string) (bool, error) { return false, errors.New("boom") }
	_, err = h.c.Plan(h.req(), h.path, createIn)
	expect(t, errors.As(err, &de) && de.Code == "ReviewFailed" && strings.Contains(de.Detail, "boom"), "failed: %v", err)
	h.d.id.viewer = nil
	_, err = h.c.Plan(h.req(), h.path, createIn)
	expect(t, errors.As(err, &de) && de.Code == "NotAllowed" && strings.Contains(de.Detail, "reviews nobody"), "no viewer: %v", err)
}

func TestCreateServiceLocalNeedsNoSAR(t *testing.T) {
	h := newCreateHost(t, true, true)
	h.st = authn.State{SignedIn: true}
	h.d.id.self = authn.Person{User: "ana@acme.com"}
	ans := h.plan(t, createIn)
	expect(t, ans.Plan != nil && len(h.reviews) == 0, "reviews %v", h.reviews)
	expect(t, strings.HasPrefix(ans.Plan.Revision, "pacto-create:ana@acme.com:"), "the local caller names the revision: %s", ans.Plan.Revision)
}

func TestCreateServiceSignTicketBindsIntent(t *testing.T) {
	h := newCreateHost(t, false, true)
	h.freshLogin(t, personToken())
	h.ownerPolicy(t, "ana@acme.com")
	ans := h.plan(t, createIn)
	expect(t, ans.Signing.Mode == signModeFreshLogin && ans.Plan.State == contract.DeclarationDeclared && ans.SignTicket == "ticket-"+ans.Plan.IntentDigest, "%+v", ans)
	back := createIn
	back.IntentDigest = ans.Plan.IntentDigest
	expect(t, h.plan(t, back).SignedIn, "back from the login the plan says it is live")
	h.revision = ans.Plan.Revision
	pub := h.accept(t, createIn, "sign", ans.Plan.IntentDigest)
	index := h.repoTags(t, "pacto-index")
	allTrue(t, map[string]bool{
		"published": pub.Refusal == nil && pub.Tag == "0.1.0" && pub.Signed && !pub.Draft,
		"bundle":    strings.HasPrefix(pub.Published, h.w.host+"/acme/invoices@sha256:"),
		"init":      strings.HasPrefix(pub.InitCLI, "pacto init invoices"),
		"git":       strings.Contains(pub.GitCLI, "gh repo create <org>/invoices"),
		"pull":      strings.HasPrefix(pub.PullCLI, "pacto pull "),
		"appears":   pub.AppearsWithin == createAppearsWithin,
		"files":     pub.FilesTotal >= 2 && slices.ContainsFunc(pub.Files, func(f dashboard.CreateServiceFile) bool { return f.Path == "pacto.yaml" }),
		"index":     len(index) == 1 && strings.HasSuffix(index[0], "-0.1.0"),
		"note":      strings.HasPrefix(pub.Index, "indexed as "+h.w.host+"/acme/pacto-index:"),
		"audit":     strings.Contains(h.log.String(), "verb=create") && strings.Contains(h.log.String(), "namespace=team-billing"),
	}, pub)
	// The login is used once.
	again := h.accept(t, createIn, "sign", ans.Plan.IntentDigest)
	expect(t, again.Refusal != nil, "%+v", again)
}

func TestCreateServiceSignNeedsLoginAndOwner(t *testing.T) {
	h := newCreateHost(t, false, true)
	out := h.accept(t, createIn, "sign", "sha256:x")
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed" && strings.HasPrefix(out.Refusal.Command, "pacto init invoices"), "no signing here: %+v", out)
	h.freshLogin(t, personToken())
	intent := h.plan(t, createIn).Plan.IntentDigest
	h.d.take = func(*http.Request, string) (string, bool) { return "", false }
	out = h.accept(t, createIn, "sign", intent)
	expect(t, out.Refusal != nil && out.Refusal.Code == "SignerNotAllowed", "no login: %+v", out)
}

// A plan that moved since the login refuses with the fresh plan, and the
// login is still unused.
func TestCreateServiceAcceptPlanStale(t *testing.T) {
	h := newCreateHost(t, false, true)
	h.freshLogin(t, personToken())
	h.ownerPolicy(t, "ana@acme.com")
	ans := h.plan(t, createIn)
	moved := createIn
	moved.Revision = "abc123"
	out := h.accept(t, moved, "sign", ans.Plan.IntentDigest)
	expect(t, out.Refusal != nil && out.Refusal.Code == "PlanStale" && out.Plan != nil && out.Plan.Plan.IntentDigest != ans.Plan.IntentDigest &&
		strings.HasPrefix(out.Refusal.Command, "pacto init invoices"), "%+v", out)
	expect(t, h.d.live(nil, ""), "a stale accept took the login")
}

// No owner policy: the publish refusal reaches the answer word for word.
func TestCreateServiceRefusalVerbatim(t *testing.T) {
	h := newCreateHost(t, false, true)
	h.freshLogin(t, personToken())
	intent := h.plan(t, createIn).Plan.IntentDigest
	out := h.accept(t, createIn, "sign", intent)
	same := createIn
	same.Revision = h.revision
	req := h.c.request(h.req(), h.path, same)
	req.Login, req.IntentDigest = func(context.Context) (string, error) { return personToken(), nil }, intent
	_, direct := h.d.svc.Declare(t.Context(), req)
	var de *app.DeclareError
	expect(t, errors.As(direct, &de) && out.Refusal != nil && out.Refusal.Code == de.Code && out.Refusal.Message == de.Message, "%+v vs %v", out.Refusal, direct)
	expect(t, len(h.repoTags(t, "invoices")) == 0, "written: %v", h.repoTags(t, "invoices"))
}

func TestCreateServiceDraftMode(t *testing.T) {
	h := newCreateHost(t, false, true)
	p := h.plan(t, createIn)
	first := h.accept(t, createIn, "draft", p.Plan.IntentDigest)
	expect(t, first.Refusal == nil && first.Tag == "draft-1" && first.Draft && !first.Signed &&
		strings.HasPrefix(first.AcceptURL, "#/fleet/services/") && strings.Contains(first.AcceptURL, "draft=draft-1"), "%+v", first)
	second := h.accept(t, createIn, "draft", p.Plan.IntentDigest)
	expect(t, second.Tag == "draft-1" && second.Published == "" && second.AcceptURL == first.AcceptURL, "deduped %+v", second)
	expect(t, slices.Equal(h.repoTags(t, "invoices"), []string{"draft-1"}), "tags %v", h.repoTags(t, "invoices"))
	draft := createIn
	draft.FromDraft = &dashboard.DraftRef{Tag: "draft-1"}
	out := h.accept(t, draft, "draft", p.Plan.IntentDigest)
	expect(t, out.Refusal != nil && out.Refusal.Code == "NotAllowed", "a draft accepted with a draft: %+v", out)
	expect(t, strings.Contains(h.log.String(), "tag=draft-1"), "audit %s", h.log.String())
}

func TestCreateServiceExistsHiddenRevealsNoDetail(t *testing.T) {
	h := newCreateHost(t, false, true)
	h.pushBundle(t, h.w.host+"/acme/invoices:1.0.0", map[string]string{"pacto.yaml": strings.Replace(goHTTPPath(h.w.host), "name: go-http", "name: invoices", 1)})
	ans := h.plan(t, createIn)
	expect(t, ans.Plan == nil && ans.Refusal != nil && ans.Refusal.Code == "ServiceExists" &&
		ans.Refusal.Message == "invoices is already taken in this registry: pick another name, or adopt your own service with pacto declare" &&
		len(ans.Drafts) == 0, "%+v", ans)
	out := h.accept(t, createIn, "draft", "sha256:x")
	expect(t, out.Refusal != nil && out.Refusal.Code == "ServiceExists", "%+v", out)
}

func TestCreateServiceUsesPushScope(t *testing.T) {
	h := newCreateHost(t, false, true)
	push := &countingKeychain{}
	h.d.push = push
	p := h.plan(t, createIn)
	expect(t, len(push.hosts) == 0, "the plan read with the push credential: %v", push.hosts)
	out := h.accept(t, createIn, "draft", p.Plan.IntentDigest)
	expect(t, out.Tag == "draft-1" && len(push.hosts) > 0, "the draft %+v went without the push credential (%v)", out, push.hosts)
	// The index entry goes through a push scope that holds the index alone.
	repo, err := h.c.request(h.req(), h.path, createIn).Create.OpenIndex(h.w.host + "/acme/pacto-index")
	expect(t, err == nil && repo != nil, "the index opener: %v", err)
}

func TestCreateServiceNoPushOpensNoIndex(t *testing.T) {
	h := newCreateHost(t, false, false)
	expect(t, !h.c.CanWrite() && h.c.request(h.req(), h.path, createIn).Create.OpenIndex == nil, "an index opener without a push credential")
	ans := h.plan(t, createIn)
	expect(t, !ans.Write.Available && ans.Write.Reason == noPushCredential, "%+v", ans.Write)
}

func TestCapabilitiesPortalCreateURLConfiguredOnly(t *testing.T) {
	v := viper.New()
	expect(t, portalCreateURL(v) == "", "no Backstage connection")
	v.Set("dashboard.backstage-url", "https://backstage.acme.io/")
	expect(t, portalCreateURL(v) == "https://backstage.acme.io/create", "configured: %s", portalCreateURL(v))
}

func TestNewDashboardCreator(t *testing.T) {
	expect(t, newDashboardCreator(nil) == nil, "no declarer, no creator")
	var typed *dashboardDeclarer
	expect(t, newDashboardCreator(typed) == nil, "a nil declarer")
	h := newDeclareHost(t, true, true)
	expect(t, newDashboardCreator(h.d) != nil, "a creator")
}

func TestCreateTrustLoadFailureFailsStart(t *testing.T) {
	v := viper.New()
	v.Set("dashboard.registry", "reg.io/acme")
	v.Set("dashboard.delivery-trust", filepath.Join(t.TempDir(), "missing.json"))
	_, d, err := newDashboardHost(v, "127.0.0.1", nil, nil, nil, nil, defaultDeps(), nil)
	expect(t, err != nil && d == nil, "an unreadable trust file starts the dashboard: %v", err)
	good := filepath.Join(t.TempDir(), "trust.json")
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	if err := os.WriteFile(good, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	v.Set("dashboard.delivery-trust", good)
	id, d, err := newDashboardHost(v, "127.0.0.1", nil, nil, nil, nil, defaultDeps(), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := newDashboardCreator(d).(*dashboardCreator)
	expect(t, c.id == id && id.trust != nil, "the creator takes the trust loaded at start")
}

func TestCreateRefusedPassesOtherErrors(t *testing.T) {
	r, rs, err := createRefused(errors.New("boom"))
	expect(t, r == nil && rs == nil && err != nil, "%v %v %v", r, rs, err)
}

func TestCreateFilesBounded(t *testing.T) {
	files := map[string][]byte{}
	for i := range dashboard.MaxCreateServiceFiles + 2 {
		files[strings.Repeat("f", i+1)] = bytes.Repeat([]byte("x"), 10)
	}
	out, total := createFiles(files)
	expect(t, len(out) == dashboard.MaxCreateServiceFiles && total == dashboard.MaxCreateServiceFiles+2, "%d %d", len(out), total)
}

func TestOfferingViewsRequestedFirst(t *testing.T) {
	q := consumableQuery(t)
	other := offeringViews(q, nil)
	expect(t, len(other) == 1 && other[0].Offering == "postgresql" && !other[0].Requested, "%+v", other)
	asked := offeringViews(q, []string{"postgresql"})
	expect(t, len(asked) == 1 && asked[0].Requested, "%+v", asked)
	expect(t, len(asked[0].Guarantees) == 1 && asked[0].Guarantees[0].Interface == "sql" && asked[0].Guarantees[0].Text != "",
		"the offering carries its provider's guarantees: %+v", asked[0].Guarantees)
}
