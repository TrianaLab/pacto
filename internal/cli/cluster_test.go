package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/detect"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	clusterHubUID = "hub00000-0000-4000-8000-000000000000"
	clusterIssuer = "https://idp.acme.example"
	euProd        = "aaaa0000-0000-4000-8000-00000000000a"
	alice         = "oidc:alice@acme.com"
)

// joinMember is a cluster that publishes to the hub's registry.
type joinMember struct {
	uid, name string
	key       ed25519.PrivateKey
}

func newJoinMember(uid, name string) joinMember {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	return joinMember{uid: uid, name: name, key: key}
}

func (m joinMember) keyID() string { return signing.Hint(m.key.Public()) }

// clusterEnv is a hub cluster whose operator lists one approver signer of a
// private Sigstore, a registry its members publish to, the person's
// Kubernetes user and the email their fresh login names.
type clusterEnv struct {
	d      deps
	repo   string
	ss     *sigtest.Sigstore
	policy delivery.ClassPolicy
	user   string
	email  string
	// mayApprove answers the SelfSubjectAccessReview; userReview the
	// SubjectAccessReview of a user with no groups.
	mayApprove bool
	userReview func(user string) (bool, error)
	// noPolicy publishes no identity policy; onLogin runs during the login.
	noPolicy bool
	onLogin  func()
	logins   int
}

func newClusterEnv(t *testing.T, mode capabilities.ReferrersMode) *clusterEnv {
	t.Helper()
	old := ocireferrer.Probes
	ocireferrer.Probes = &ocireferrer.Prober{}
	t.Cleanup(func() { ocireferrer.Probes = old })
	for _, env := range ambientTokens {
		t.Setenv(env, "")
	}
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{Unsupported: mode == capabilities.ReferrersWriteOnceTags, TagsLink: true}))
	t.Cleanup(srv.Close)
	ss := sigtest.New(t)
	e := &clusterEnv{repo: strings.TrimPrefix(srv.URL, "http://") + "/acme/" + delivery.TargetSnapshotRepository, ss: ss,
		user: alice, email: "alice@acme.com", mayApprove: true, userReview: func(u string) (bool, error) { return u == alice, nil }}
	e.policy = delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: clusterIssuer, ClientID: "sigstore", UsernamePrefix: "oidc:",
		Subjects: `.*@acme\.com|.*gserviceaccount\.com`, FulcioURL: ss.Fulcio, TimestampURL: ss.TSA, TrustedRoot: string(ss.RootJSON)}}}
	e.d = defaultDeps()
	e.d.newDynamic = func() (dynamic.Interface, error) { return e.hub(), nil }
	e.d.freshLogin = func(_ context.Context, c authn.FreshConfig) (string, error) {
		e.logins++
		c.Prompt("https://idp/auth?prompt=login")
		if e.onLogin != nil {
			e.onLogin()
		}
		claims, _ := json.Marshal(map[string]any{"sub": e.email, "email": e.email, "email_verified": true, "iss": clusterIssuer, "aud": c.ClientID})
		return "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig", nil
	}
	return e
}

// hub is a client of the hub cluster.
func (e *clusterEnv) hub() *dynamicfake.FakeDynamicClient {
	ns := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": "kube-system", "uid": clusterHubUID}}}
	objs := []runtime.Object{ns}
	if !e.noPolicy {
		policy, _ := json.Marshal(e.policy)
		objs = append(objs, &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"namespace": delivery.PolicyNamespace, "name": delivery.PolicyConfigMap},
			"data":     map[string]any{delivery.PolicyKey: string(policy)}}})
	}
	c := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objs...)
	c.PrependReactor("create", "selfsubjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"allowed": e.mayApprove}}}, nil
	})
	c.PrependReactor("create", "selfsubjectreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"userInfo": map[string]any{"username": e.user, "groups": []any{"platform"}}}}}, nil
	})
	c.PrependReactor("create", "subjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		user, _, _ := unstructured.NestedString(a.(k8stesting.CreateAction).GetObject().(*unstructured.Unstructured).Object, "spec", "user")
		ok, err := e.userReview(user)
		return true, &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"allowed": ok}}}, err
	})
	return c
}

// publish signs m's snapshot with signer and tags it.
func (e *clusterEnv) publish(t *testing.T, m joinMember, signer ed25519.PrivateKey) {
	t.Helper()
	der, _ := x509.MarshalPKIXPublicKey(m.key.Public())
	s := delivery.TargetSnapshot{Cluster: m.uid, AsOf: time.Now().UTC().Format(time.RFC3339), Completeness: fleet.CompletenessComplete, Publisher: "pacto/pacto-operator",
		Join: &delivery.SnapshotJoin{Name: m.name, NameSource: fleet.ClusterNameFromNodeLabel, KeyID: m.keyID(), PublicKey: base64.StdEncoding.EncodeToString(der),
			Environments: []delivery.AdvertisedEnvironment{{Name: "production", Source: "the cluster name " + m.name}}}}
	kp, _ := signing.FromPrivateKey(signer)
	b, err := delivery.SignTargetSnapshot(t.Context(), signing.Keyed(kp, ""), delivery.NewTargetSnapshotStatement(s))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deliveryrecord.PushTargetSnapshot(t.Context(), e.registry(), m.uid, b); err != nil {
		t.Fatal(err)
	}
}

func (e *clusterEnv) registry() *remote.Repository {
	r, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
	return r
}

// records are the join records of uid in the registry.
func (e *clusterEnv) records(t *testing.T, uid string) []deliveryrecord.Fetched {
	t.Helper()
	recs, _, _, err := (&deliveryrecord.Reader{}).ListClusterJoins(t.Context(), e.registry(), uid)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

// run runs pacto cluster with args against the hub's registry.
func (e *clusterEnv) run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return runRoot(t, e.d, nil, append(append([]string{"cluster"}, args...), "--registry", e.repo)...)
}

// approve approves m with its own key.
func (e *clusterEnv) approve(t *testing.T, m joinMember, args ...string) (string, string, error) {
	t.Helper()
	return e.run(t, append([]string{"approve", m.uid, "--key", "sha256:" + m.keyID()}, args...)...)
}

// state is m's state as the hub reads it.
func (e *clusterEnv) state(t *testing.T, m joinMember) delivery.ClusterJoinState {
	t.Helper()
	out, _, err := e.run(t, "list", "--output-format", "json")
	var l delivery.ClusterJoinList
	if err != nil || json.Unmarshal([]byte(out), &l) != nil {
		t.Fatalf("%q %v", out, err)
	}
	for _, c := range l.Clusters {
		if c.UID == m.uid {
			return c
		}
	}
	t.Fatalf("no %s in %+v", m.uid, l)
	return delivery.ClusterJoinState{}
}

func TestClusterListText(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	out, _, err := e.run(t, "list")
	want := "eu-prod (node-label)  " + euProd + "  pending  hosts: production (from the cluster name eu-prod)  key sha256:" + m.keyID() + "  no approval yet\n"
	if err != nil || !strings.HasPrefix(out, "hub "+clusterHubUID+" reads "+e.repo+" (complete, as of ") || !strings.HasSuffix(out, want) {
		t.Fatalf("%q %v", out, err)
	}
	// The person's own list offers the approve command (R3-5).
	if out, _, _ := e.run(t, "list", "--output-format", "json"); !strings.Contains(out, `"canApprove": true`) {
		t.Fatalf("%s", out)
	}
}

func TestClusterListJSONMatchesApp(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	got := e.state(t, m)
	want := app.NewService(nil, nil).ClusterJoins(t.Context(), app.JoinOptions{Repository: e.repo, Hub: clusterHubUID,
		Authorize: authn.SubjectAccessReview(e.hub()), Delivery: &app.Delivery{Policy: e.policy}})
	if len(want.Clusters) != 1 || got.KeyID != want.Clusters[0].KeyID || got.State != want.Clusters[0].State || got.Reason != want.Clusters[0].Reason {
		t.Fatalf("%+v != %+v", got, want.Clusters)
	}
}

func TestClusterListMarkdown(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	out, _, err := e.run(t, "list", "--output-format", "markdown")
	row := "| eu-prod (node-label) | " + euProd + " | pending | production (from the cluster name eu-prod) | sha256:" + m.keyID() + " | no approval yet | - |\n"
	if err != nil || !strings.Contains(out, "| Name | UID | State | Hosts | Key | Approver or reason | Mapping |\n") || !strings.Contains(out, row) {
		t.Fatalf("%q %v", out, err)
	}
}

// TestClusterListPrintsEveryPart: the empty list, a member with no name or
// key, the counts and limitations and an approved member that a pending one
// may replace, in text and markdown.
func TestClusterListPrintsEveryPart(t *testing.T) {
	var b bytes.Buffer
	printClusterList(&b, delivery.ClusterJoinList{Hub: delivery.ClusterRef{UID: "h"}, Hidden: 2, OtherHubs: 1,
		Limitations: []fleet.Limitation{{Code: "X", Message: "m"}}})
	if want := "no member clusters\n2 clusters you may not approve are hidden\n1 join or mapping records name another hub and were not used\n[X] m\n"; !strings.HasSuffix(b.String(), want) {
		t.Fatalf("%q", b.String())
	}
	l := delivery.ClusterJoinList{Clusters: []delivery.ClusterJoinState{
		{UID: "bbbb0000-1111", NameSource: "uid", State: delivery.JoinPending, Reason: "a|b"},
		{UID: "cccc0000-2222", Name: "eu-prod", State: delivery.JoinApproved, Approver: alice, ApprovedAt: "t", KeyID: "k"},
		{UID: "dddd0000-3333", Name: "eu-prod", State: delivery.JoinPending, MayReplace: &delivery.ClusterRef{UID: "cccc0000-2222", Name: "eu-prod"}},
		{UID: "eeee0000-4444", Name: "ap-dev", State: delivery.JoinPending, Status: fleet.SourceStale, SnapshotAsOf: "2026-11-17T10:00:00Z"},
		{UID: "ffff0000-5555", Name: "ap-old", State: delivery.JoinPending, Status: fleet.SourceUnavailable},
	}}
	b.Reset()
	printClusterList(&b, l)
	for _, want := range []string{"bbbb0000 (uid)  bbbb0000-1111  pending  a|b\n", "eu-prod ()  cccc0000-2222  approved  key sha256:k  by " + alice + " on t\n",
		"dddd0000-3333  pending  may replace eu-prod\n", "eeee0000-4444  pending  snapshot stale from 2026-11-17T10:00:00Z\n",
		"ffff0000-5555  pending  snapshot unavailable\n"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("%q in %q", want, b.String())
		}
	}
	b.Reset()
	printClusterListMarkdown(&b, l)
	if !strings.Contains(b.String(), `| a\|b |`) || !strings.Contains(b.String(), "| "+alice+" on t |") || !strings.Contains(b.String(), "| ; may replace eu-prod |") ||
		!strings.Contains(b.String(), "| pending (snapshot stale from 2026-11-17T10:00:00Z) |") {
		t.Fatalf("%q", b.String())
	}
}

func TestApproveWritesPersonRecord(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	out, errOut, err := e.approve(t, m)
	if err != nil || !strings.HasPrefix(out, "approved eu-prod (aaaa0000) as "+alice+"; recorded "+e.repo+"@sha256:") ||
		!strings.Contains(errOut, "To sign an approval on cluster "+euProd+", log in again in your browser") || e.logins != 1 {
		t.Fatalf("%q %q %v", out, errOut, err)
	}
	recs := e.records(t, m.uid)
	if len(recs) != 1 {
		t.Fatalf("%d records", len(recs))
	}
	id, _, err := signing.VerifyPerson(recs[0].Bundle, clusterIssuer, e.policy.Signers[0].TrustedRoot)
	st, oerr := delivery.OpenClusterJoin(recs[0].Bundle, func([]byte) error { return err })
	if oerr != nil || id.Subject != "alice@acme.com" || st.Predicate.Approver != alice || st.Predicate.Hub != clusterHubUID ||
		st.Predicate.KeyID != m.keyID() || st.Predicate.Decision != delivery.JoinApprove || st.Predicate.Name != "" {
		t.Fatalf("%+v %+v %v", id, st.Predicate, oerr)
	}
	if s := e.state(t, m); s.State != delivery.JoinApproved || s.Approver != alice {
		t.Fatalf("%+v", s)
	}
}

// approveRefused runs approve of a published member under mod and checks it fails
// with want, nothing written and logins logins.
func approveRefused(t *testing.T, mod func(*clusterEnv), want string, logins int, args ...string) *clusterEnv {
	t.Helper()
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	mod(e)
	_, _, err := e.approve(t, m, args...)
	if err == nil || !strings.Contains(err.Error(), want) || len(e.records(t, m.uid)) != 0 || e.logins != logins {
		t.Fatalf("want %q, got %v, %d logins", want, err, e.logins)
	}
	return e
}

func TestApproveRefusesWithoutSelfSAR(t *testing.T) {
	approveRefused(t, func(e *clusterEnv) { e.mayApprove = false }, "ClusterApproveForbidden: you may not approve clusterjoins named "+euProd+" in the hub; ask for kubectl create clusterrole", 0)
}

func TestApproveRefusesGroupOnlyGrant(t *testing.T) {
	approveRefused(t, func(e *clusterEnv) { e.userReview = func(string) (bool, error) { return false, nil } },
		"ClusterApproveNeedsUserGrant: approve must be granted to your user "+alice+"; groups are not checked", 0)
}

func TestApproveWarnsWhenUserReviewForbidden(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	e.userReview = func(string) (bool, error) {
		return false, apierrors.NewForbidden(schema.GroupResource{Group: "authorization.k8s.io", Resource: "subjectaccessreviews"}, "", errors.New("no"))
	}
	out, errOut, err := e.approve(t, m)
	if err != nil || !strings.HasPrefix(out, "approved eu-prod") || !strings.Contains(errOut, "the record counts only if that user, without your groups, may") {
		t.Fatalf("%q %q %v", out, errOut, err)
	}
	approveRefused(t, func(e *clusterEnv) {
		e.userReview = func(string) (bool, error) { return false, errors.New("apiserver down") }
	},
		"asking the hub whether "+alice+" may approve", 0)
}

func TestApproveRefusesAmbientToken(t *testing.T) {
	for _, env := range ambientTokens {
		approveRefused(t, func(*clusterEnv) { t.Setenv(env, "ambient") }, "approving needs your own fresh login; unset "+env, 0)
	}
}

func TestApproveRefusesApproverMismatch(t *testing.T) {
	approveRefused(t, func(e *clusterEnv) {
		e.user = "oidc:bob@acme.com"
		e.userReview = func(string) (bool, error) { return true, nil }
	}, "ApproverMismatch: your Kubernetes user is oidc:bob@acme.com and your login signs as "+alice+"; nothing was signed", 1)
	approveRefused(t, func(e *clusterEnv) { e.email = "eve@evil.example" }, "eve@evil.example", 1)
	approveRefused(t, func(e *clusterEnv) { e.policy.Signers[0].Subjects = "" }, "has no subjects", 1)
}

func TestApproveRefusesMachineLogin(t *testing.T) {
	approveRefused(t, func(e *clusterEnv) { e.email = "ci@proj.iam.gserviceaccount.com" }, "AgentsMayNotApprove", 1)
}

// TestApproveRefusesKeyMismatch: a key other than the snapshot's, or the tag
// swapped to another key while the person logs in, gets nothing signed.
func TestApproveRefusesKeyMismatch(t *testing.T) {
	other := newJoinMember(euProd, "eu-prod")
	approveRefused(t, func(*clusterEnv) {}, "ClusterKeyMismatch: the snapshot of "+euProd+" is signed by sha256:", 0, "--key", "sha256:"+other.keyID())
	e := approveRefused(t, func(e *clusterEnv) { e.onLogin = func() { e.publish(t, other, other.key) } }, "ClusterKeyMismatch: the snapshot of "+euProd+" is now signed by sha256:"+other.keyID()[:12], 1)
	if e.logins != 1 {
		t.Fatal(e.logins)
	}
}

func TestApproveRequiresKey(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	if _, _, err := e.run(t, "approve", euProd); err == nil || !strings.Contains(err.Error(), `required flag(s) "key" not set`) {
		t.Fatal(err)
	}
	if _, _, err := e.run(t, "approve", euProd, "--key", "91ab"); err == nil || !strings.Contains(err.Error(), "--key wants sha256:<64 hex>") {
		t.Fatal(err)
	}
}

func TestApproveRefusesNameNotADNSLabel(t *testing.T) {
	for _, name := range []string{"EU_prod", "eu;rm", "еu-prod"} {
		approveRefused(t, func(*clusterEnv) {}, "is not a DNS label", 0, "--name", name)
	}
}

func TestApproveNoSignersConfigured(t *testing.T) {
	approveRefused(t, func(e *clusterEnv) { e.policy.Signers = nil }, "this hub accepts no signed approvals: set delivery.identities.signers on the hub's operator", 0)
	approveRefused(t, func(e *clusterEnv) { e.noPolicy = true }, "the hub's approver signers could not be read", 0)
	approveRefused(t, func(e *clusterEnv) {
		e.policy.Signers = append(e.policy.Signers, delivery.PersonSigner{Issuer: "https://other.example", Subjects: "x"})
	},
		"pick the approver signer with --issuer: the hub accepts "+clusterIssuer+", https://other.example", 0)
}

func TestApproveAlreadyApprovedWritesNothing(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	if _, _, err := e.approve(t, m); err != nil {
		t.Fatal(err)
	}
	out, _, err := e.approve(t, m)
	if err != nil || out != "eu-prod (aaaa0000) is already approved; nothing was signed\n" || e.logins != 1 || len(e.records(t, m.uid)) != 1 {
		t.Fatalf("%q %v", out, err)
	}
}

func TestApproveAmbiguousPrefix(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	a, b := newJoinMember(euProd, "eu-prod"), newJoinMember("aaaa0000-0000-4000-8000-00000000000b", "us-west")
	e.publish(t, a, a.key)
	e.publish(t, b, b.key)
	key := "sha256:" + a.keyID()
	for arg, want := range map[string]string{
		"aaaa0000":  "aaaa0000 names 2 clusters (" + euProd + ", " + b.uid + "): name more of the id",
		"aaaa":      "name the cluster by its id or at least 8 characters of it",
		"ffff0000x": "no cluster ffff0000x publishes to " + e.repo,
	} {
		if _, _, err := e.run(t, "approve", arg, "--key", key); err == nil || err.Error() != want {
			t.Fatalf("%s: %v", arg, err)
		}
	}
	if out, _, err := e.run(t, "approve", "aaaa0000-0000-4000-8000-00000000000", "--key", key); err == nil || out != "" {
		t.Fatalf("a prefix of two: %q %v", out, err)
	}
	if out, _, err := e.run(t, "approve", "aaaa0000-0000-4000-8000-00000000000a", "--key", key); err != nil || !strings.HasPrefix(out, "approved eu-prod (aaaa0000)") {
		t.Fatalf("the full id: %q %v", out, err)
	}
	if out, _, err := e.run(t, "approve", b.uid, "--key", "sha256:"+b.keyID()); err != nil || !strings.HasPrefix(out, "approved us-west") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestApproveNameOverride(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	out, _, err := e.approve(t, m, "--name", "eu-west")
	if err != nil || !strings.HasPrefix(out, "approved eu-west (aaaa0000) as "+alice) {
		t.Fatalf("%q %v", out, err)
	}
	if s := e.state(t, m); s.Name != "eu-west" || s.NameSource != fleet.ClusterNameFromApproval {
		t.Fatalf("%+v", s)
	}
	// Another name is a new decision, the same one is not.
	if out, _, err := e.approve(t, m, "--name", "eu-west"); err != nil || !strings.Contains(out, "already approved") {
		t.Fatalf("%q %v", out, err)
	}
	if out, _, err := e.approve(t, m, "--name", "eu-north"); err != nil || !strings.HasPrefix(out, "approved eu-north") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestApproveWriteOnceRegistryPrintsTag(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersWriteOnceTags)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	out, _, err := e.approve(t, m)
	tag := e.repo + ":" + deliveryrecord.AnchorTag(delivery.ClusterJoinStream(euProd)) + "-join-"
	if err != nil || !strings.Contains(out, "\npushed "+tag) || !strings.HasSuffix(out, " (write-once tag; registry has no Referrers)\n") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestRemoveWritesRevocation(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	if _, _, err := e.approve(t, m); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := e.run(t, "remove", "aaaa0000")
	if err != nil || !strings.HasPrefix(out, "removed eu-prod (aaaa0000) as "+alice+"; recorded ") || !strings.Contains(errOut, "To sign a removal on cluster "+euProd) {
		t.Fatalf("%q %q %v", out, errOut, err)
	}
	if s := e.state(t, m); s.State != delivery.JoinRemoved {
		t.Fatalf("%+v", s)
	}
	if out, _, err := e.run(t, "remove", euProd); err != nil || out != "eu-prod (aaaa0000) is already removed; nothing was signed\n" || e.logins != 2 {
		t.Fatalf("%q %v", out, err)
	}
}

// TestRemoveRefusesMemberWithoutKey: a snapshot with no join block names no
// key to decide on.
func TestRemoveRefusesMemberWithoutKey(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	s := delivery.TargetSnapshot{Cluster: euProd, AsOf: time.Now().UTC().Format(time.RFC3339), Completeness: fleet.CompletenessComplete, Publisher: "p"}
	kp, _ := signing.FromPrivateKey(newJoinMember(euProd, "x").key)
	b, _ := delivery.SignTargetSnapshot(t.Context(), signing.Keyed(kp, ""), delivery.NewTargetSnapshotStatement(s))
	if _, err := deliveryrecord.PushTargetSnapshot(t.Context(), e.registry(), euProd, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.run(t, "remove", euProd); err == nil || !strings.Contains(err.Error(), "names no key to decide on: the snapshot carries no join block") {
		t.Fatal(err)
	}
}

// TestRemoveApprovedMemberWithoutJoinBlock: a member that stopped
// publishing its join block is removed under its approved key (R4-2).
func TestRemoveApprovedMemberWithoutJoinBlock(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	if _, _, err := e.approve(t, m); err != nil {
		t.Fatal(err)
	}
	s := delivery.TargetSnapshot{Cluster: euProd, AsOf: time.Now().UTC().Format(time.RFC3339), Completeness: fleet.CompletenessComplete, Publisher: "p"}
	kp, _ := signing.FromPrivateKey(m.key)
	b, _ := delivery.SignTargetSnapshot(t.Context(), signing.Keyed(kp, ""), delivery.NewTargetSnapshotStatement(s))
	if _, err := deliveryrecord.PushTargetSnapshot(t.Context(), e.registry(), euProd, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.run(t, "remove", euProd); err != nil {
		t.Fatal(err)
	}
	if st := e.state(t, m); st.State != delivery.JoinRemoved {
		t.Fatalf("%+v", st)
	}
}

// TestApproveRefusesSnapshotNotSelfSigned: a snapshot its own key did not
// sign is refused before the login (R4-8).
func TestApproveRefusesSnapshotNotSelfSigned(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, newJoinMember("x", "x").key)
	if _, _, err := e.approve(t, m); err == nil || !strings.Contains(err.Error(), "names no key to decide on: the snapshot is not signed by the key it names") || e.logins != 0 {
		t.Fatalf("%v after %d logins", err, e.logins)
	}
}

// detectedFile writes a detection advertising targets and returns its path.
func detectedFile(t *testing.T, targets string) string {
	t.Helper()
	b, _ := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: time.Now(), Cluster: clusterHubUID, Installation: &detect.Installation{Registry: "r", Targets: targets}})
	path := filepath.Join(t.TempDir(), "detected.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClusterNeedsRegistry(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	if _, _, err := runRoot(t, e.d, nil, "cluster", "approve", euProd, "--key", "sha256:"+strings.Repeat("a", 64)); err == nil || err.Error() != needsRegistry {
		t.Fatal(err)
	}
	if _, _, err := runRoot(t, e.d, nil, "cluster", "list"); err == nil || err.Error() != needsRegistry {
		t.Fatal(err)
	}
	if _, _, err := runRoot(t, e.d, nil, "cluster", "list", "--detected", detectedFile(t, "")); err == nil || err.Error() != needsRegistry {
		t.Fatal(err)
	}
	if _, _, err := runRoot(t, e.d, nil, "cluster", "list", "--detected", filepath.Join(t.TempDir(), "none")); err == nil || !strings.Contains(err.Error(), "names no registry") {
		t.Fatal(err)
	}
	out, _, err := runRoot(t, e.d, nil, "cluster", "list", "--detected", detectedFile(t, e.repo))
	if err != nil || !strings.Contains(out, "reads "+e.repo+" ") {
		t.Fatalf("%q %v", out, err)
	}
	if got := targetsRepository("oci://ghcr.io/acme/"); got != "ghcr.io/acme/pacto-targets" {
		t.Fatal(got)
	}
}

// TestClusterHubContext: --context reads the hub through that context, and a
// hub that cannot be reached or identified is an error.
func TestClusterHubContext(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	asked := ""
	e.d.contextConfig = func(name string) (*rest.Config, error) { asked = name; return &rest.Config{Host: name}, nil }
	e.d.dynamicFor = func(*rest.Config) (dynamic.Interface, error) { return e.hub(), nil }
	if out, _, err := e.run(t, "list", "--context", "hub"); err != nil || asked != "hub" || !strings.Contains(out, euProd) {
		t.Fatalf("%q %v", out, err)
	}
	if _, _, err := e.run(t, "list", "--context", "a", "--context", "b"); err == nil || err.Error() != "name one hub cluster with --context" {
		t.Fatal(err)
	}
	e.d.contextConfig = func(string) (*rest.Config, error) { return nil, errors.New("no context") }
	if _, _, err := e.run(t, "list", "--context", "gone"); err == nil || err.Error() != "reaching the hub cluster: no context" {
		t.Fatal(err)
	}
	e.d.newDynamic = func() (dynamic.Interface, error) { return dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), nil }
	if _, _, err := e.run(t, "list"); err == nil || !strings.Contains(err.Error(), "reading the cluster id") {
		t.Fatal(err)
	}
}

// TestApproveAuthorityFailures: a review the hub cannot answer refuses
// before any login.
func TestApproveAuthorityFailures(t *testing.T) {
	fail := func(resource string) func(*clusterEnv) {
		return func(e *clusterEnv) {
			e.d.newDynamic = func() (dynamic.Interface, error) {
				c := e.hub()
				c.PrependReactor("create", resource, func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("down") })
				return c, nil
			}
		}
	}
	approveRefused(t, fail("selfsubjectaccessreviews"), "asking the hub whether you may approve clusterjoins: down", 0)
	approveRefused(t, fail("selfsubjectreviews"), "asking the hub who you are: down", 0)
}

func TestFleetReadsJoinsWithDetected(t *testing.T) {
	e := newClusterEnv(t, capabilities.ReferrersNative)
	m := newJoinMember(euProd, "eu-prod")
	e.publish(t, m, m.key)
	if _, _, err := e.approve(t, m); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(detect.Result{SchemaVersion: detect.SchemaVersion, AsOf: time.Now(), Cluster: clusterHubUID, Installation: &detect.Installation{Registry: "r", Targets: e.repo}})
	cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "pacto-detected", "namespace": "pacto", "labels": map[string]any{detect.ManagedByLabel: detect.ManagedBy}},
		"data":     map[string]any{detect.ConfigMapKey: string(b)}}}
	cm.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: detect.FieldManager, FieldsV1: metav1.NewFieldsV1(`{"f:data":{"f:` + detect.ConfigMapKey + `":{}}}`)}})
	d := e.d
	d.clusterConfig = func() (*rest.Config, error) { return &rest.Config{}, nil }
	d.newDynamic = func() (dynamic.Interface, error) {
		c := e.hub()
		_, err := c.Resource(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}).Namespace("pacto").Create(context.Background(), cm, metav1.CreateOptions{})
		return c, err
	}
	cmd := &cobra.Command{}
	opts := fleetJoins(cmd, d, true, "k8s:pacto/pacto-detected")
	if got, err := opts.Resolve(t.Context()); err != nil || got.Repository != e.repo || got.Hub != clusterHubUID {
		t.Fatalf("%+v %v", got, err)
	}
	snap, err := app.NewService(nil, nil).Fleet(t.Context(), app.FleetOptions{ClusterJoins: opts})
	if err != nil || !hasSource(snap, "targets-"+euProd) {
		t.Fatalf("%v %+v", err, snap.Sources)
	}
	for name, got := range map[string]*app.JoinOptions{
		"no --k8s":   fleetJoins(cmd, d, false, "k8s:pacto/pacto-detected"),
		"a file":     fleetJoins(cmd, d, true, detectedFile(t, e.repo)),
		"no cluster": fleetJoins(cmd, deps{}, true, "k8s:pacto/pacto-detected"),
	} {
		if got != nil {
			t.Errorf("%s: %+v", name, got)
		}
	}
	// A detection that is not there yet is resolved again at each read (R3-3).
	if _, err := fleetJoins(cmd, d, true, "k8s:pacto/none").Resolve(t.Context()); err == nil || !strings.Contains(err.Error(), "the detection k8s:pacto/none names no registry") {
		t.Fatalf("%v", err)
	}
	cm.Object["data"] = map[string]any{detect.ConfigMapKey: strings.Replace(string(b), e.repo, "", 1)}
	if got, err := opts.Resolve(t.Context()); err != nil || got.Repository != "" {
		t.Fatalf("an operator with no registry: %+v %v", got, err)
	}
}

// hasSource reports whether snap has a source id.
func hasSource(snap *fleet.FleetSnapshot, id string) bool {
	for _, s := range snap.Sources {
		if s.ID == id {
			return true
		}
	}
	return false
}

// TestApproveExampleNamesRegistry: the example a person copies names the
// registry, which every cluster command needs (R3-1).
func TestApproveExampleNamesRegistry(t *testing.T) {
	cmd := newClusterDecisionCommand(nil, nil, deps{}, delivery.JoinApprove)
	if !strings.Contains(cmd.Example, "--registry ") {
		t.Fatalf("%q", cmd.Example)
	}
}

// TestFindClusterInPartialList: a cluster missing from a partial list may
// exist past the bounds, which its full id reads (R3-2).
func TestFindClusterInPartialList(t *testing.T) {
	_, err := findCluster(delivery.ClusterJoinList{Repository: "r", Completeness: fleet.CompletenessPartial}, "9f1c4e2a")
	if err == nil || !strings.Contains(err.Error(), "the list is partial: name its full id") {
		t.Fatal(err)
	}
}
