package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/internal/authn"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const (
	keylessIssuer = "https://idp.acme.example"
	keylessStream = "c/prod/payments-prod"
)

// keylessEnv is deliveryDeps with a keyless signer published for acme, the
// Stage keeping an approvals repository in mode on a test registry, and a
// fresh login that answers for email.
type keylessEnv struct {
	d       deps
	host    string
	policy  delivery.ClassPolicy
	stage   map[string]any
	logins  int
	created int
}

func newKeylessEnv(t *testing.T, mode capabilities.ReferrersMode, email string) *keylessEnv {
	t.Helper()
	// A fresh prober: the process one remembers each host, and this server
	// can reuse the port of an earlier one with other capabilities.
	old := ocireferrer.Probes
	ocireferrer.Probes = &ocireferrer.Prober{}
	t.Cleanup(func() { ocireferrer.Probes = old })
	srv := httptest.NewServer(testutil.NewReferrersRegistry(testutil.ReferrersOptions{Unsupported: mode == capabilities.ReferrersWriteOnceTags}))
	t.Cleanup(srv.Close)
	ss := sigtest.New(t)
	e := &keylessEnv{d: deliveryDeps("alice", nil), host: strings.TrimPrefix(srv.URL, "http://")}
	e.policy = delivery.ClassPolicy{Signers: []delivery.PersonSigner{{Issuer: keylessIssuer, ClientID: "sigstore", UsernamePrefix: "oidc:",
		Subjects: `^[^@]+@acme\.example$`, FulcioURL: ss.Fulcio, TimestampURL: ss.TSA, TrustedRoot: string(ss.RootJSON)}}}
	e.stage = map[string]any{"id": keylessStream, "anchor": deliveryrecord.AnchorRef(e.host+"/delivery/streams", keylessStream),
		"approvals": deliveryrecord.AnchorRef(e.host+"/delivery/approvals", keylessStream)}
	if mode != "" {
		e.stage["registryMode"] = string(mode)
	}
	for _, path := range []string{"delivery/approvals", "delivery/streams"} {
		if err := ocireferrer.Push(context.Background(), e.repo(path), deliveryrecord.Anchor(keylessStream)); err != nil {
			t.Fatal(err)
		}
	}
	e.d.newRecordRepo = func(path string, _ ...string) (*remote.Repository, error) {
		return ocireferrer.Open(path, ocireferrer.Options{Insecure: []string{e.host}})
	}
	e.d.freshLogin = func(_ context.Context, c authn.FreshConfig) (string, error) {
		e.logins++
		c.Prompt("https://idp/auth?prompt=login")
		claims, _ := json.Marshal(map[string]any{"sub": email, "email": email, "email_verified": true, "iss": keylessIssuer, "aud": c.ClientID})
		return "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig", nil
	}
	plain := e.d.newDynamic
	e.d.newDynamic = func() (dynamic.Interface, error) {
		c, err := plain()
		fc := c.(*dynamicfake.FakeDynamicClient)
		stages := c.Resource(schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "stages"}).Namespace("prod")
		s, _ := stages.Get(context.Background(), "payments-prod", metav1.GetOptions{})
		_ = unstructured.SetNestedField(s.Object, e.stage, "status", "stream")
		_, _ = stages.Update(context.Background(), s, metav1.UpdateOptions{})
		policy, _ := json.Marshal(e.policy)
		cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"namespace": delivery.PolicyNamespace, "name": delivery.PolicyConfigMap},
			"data":     map[string]any{delivery.PolicyKey: string(policy)}}}
		_, _ = c.Resource(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}).Namespace(delivery.PolicyNamespace).Create(context.Background(), cm, metav1.CreateOptions{})
		fc.PrependReactor("create", "approvals", func(k8stesting.Action) (bool, runtime.Object, error) {
			e.created++
			return false, nil, nil
		})
		return c, err
	}
	return e
}

func (e *keylessEnv) repo(path string) *remote.Repository {
	r, _ := ocireferrer.Open(e.host+"/"+path, ocireferrer.Options{Insecure: []string{e.host}})
	return r
}

// approvals counts what is attached to the stream anchor in path.
func (e *keylessEnv) approvals(t *testing.T, path string) int {
	t.Helper()
	descs, tags, err := ocireferrer.Attached(context.Background(), e.repo(path), deliveryrecord.Anchor(keylessStream).ManifestDesc, deliveryrecord.AnchorTag(keylessStream)+"-")
	if err != nil {
		t.Fatal(err)
	}
	return len(descs) + len(tags)
}

// run runs approve on prod/held with args, verbose, and returns stdout,
// stderr and the error.
func (e *keylessEnv) run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	v := viper.New()
	v.Set(outputFormatKey, "text")
	v.Set("verbose", true)
	cmd := newDeliveryCommand(v, e.d)
	out, errOut := &strings.Builder{}, &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(append([]string{"approve", "-n", "prod", "held"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestApproveRoutesKeylessWhenSignerConfigured(t *testing.T) {
	e := newKeylessEnv(t, "", "alice@acme.example")
	out, errOut, err := e.run(t, "--note", "ok")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "approval signed by alice@acme.example (keyless) on prod/held, attached to the approvals anchor "+e.host+"/delivery/approvals@sha256:") ||
		!strings.Contains(out, "record sha256:") || !strings.Contains(errOut, "To sign approve on prod/held, log in again in your browser") {
		t.Fatalf("stdout %q stderr %q", out, errOut)
	}
	if e.logins != 1 || e.created != 0 || e.approvals(t, "delivery/approvals") != 1 || e.approvals(t, "delivery/streams") != 0 {
		t.Fatalf("%d logins, %d Approvals created, %d approval records", e.logins, e.created, e.approvals(t, "delivery/approvals"))
	}
}

// TestApproveKeylessOptOut: another issuer, or --keyless=false, records the
// decision in the cluster.
func TestApproveKeylessOptOut(t *testing.T) {
	e := newKeylessEnv(t, "", "alice@acme.example")
	if out, errOut, err := e.run(t, "--oidc-issuer-url", "https://other.example"); err != nil || e.created != 1 ||
		!strings.Contains(errOut, "the installation accepts no signed approvals from this issuer") {
		t.Fatalf("another issuer: %q %q %v", out, errOut, err)
	}
	if out, errOut, err := e.run(t, "--keyless=false"); err != nil || e.created != 2 || strings.Contains(errOut, "recording the decision in the cluster") ||
		!strings.Contains(out, "as Approval") {
		t.Fatalf("--keyless=false: %q %q %v", out, errOut, err)
	}
}

func TestApproveKeylessPrintsWriteOnceTag(t *testing.T) {
	e := newKeylessEnv(t, capabilities.ReferrersWriteOnceTags, "alice@acme.example")
	out, _, err := e.run(t)
	tag := e.host + "/delivery/approvals:" + deliveryrecord.AnchorTag(keylessStream) + "-approval-"
	if err != nil || !strings.Contains(out, "approval signed by alice@acme.example (keyless) on prod/held, pushed "+tag) ||
		!strings.Contains(out, "(write-once tag; registry has no Referrers)") {
		t.Fatalf("%q %v", out, err)
	}
	if e.approvals(t, "delivery/approvals") != 1 {
		t.Fatal("no write-once approval tag")
	}
}

// TestApproveKeylessSaysWhatItCouldNotAsk: a caller the apiserver does not
// let ask about the keyless identity is told the decision counts only if
// that identity, without the caller's groups, may update the workload.
func TestApproveKeylessSaysWhatItCouldNotAsk(t *testing.T) {
	e := newKeylessEnv(t, "", "alice@acme.example")
	plain := e.d.newDynamic
	e.d.newDynamic = func() (dynamic.Interface, error) {
		c, err := plain()
		c.(*dynamicfake.FakeDynamicClient).PrependReactor("create", "subjectaccessreviews", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("forbidden")
		})
		return c, err
	}
	out, errOut, err := e.run(t)
	if err != nil || !strings.HasPrefix(out, "approval signed by") || !strings.Contains(errOut, "could not ask whether oidc:alice@acme.example itself may update") {
		t.Fatalf("%q %q %v", out, errOut, err)
	}
}

// clusterPath runs approve under a policy or Stage change and checks it
// records the decision in the cluster, saying why with -v.
func clusterPath(t *testing.T, mod func(*keylessEnv), why string, args ...string) *keylessEnv {
	t.Helper()
	e := newKeylessEnv(t, "", "alice@acme.example")
	mod(e)
	out, errOut, err := e.run(t, args...)
	if err != nil || e.created != 1 || e.approvals(t, "delivery/approvals") != 0 || !strings.Contains(errOut, "recording the decision in the cluster: "+why) {
		t.Fatalf("%q %q %v, %d created", out, errOut, err, e.created)
	}
	return e
}

func TestApproveKeepsClusterPathOnFreshnessStage(t *testing.T) {
	e := newKeylessEnv(t, "", "alice@acme.example")
	plain := e.d.newDynamic
	e.d.newDynamic = func() (dynamic.Interface, error) {
		c, err := plain()
		stages := c.Resource(schema.GroupVersionResource{Group: "delivery.pacto.trianalab.io", Version: "v1alpha1", Resource: "stages"}).Namespace("prod")
		s, _ := stages.Get(context.Background(), "payments-prod", metav1.GetOptions{})
		_ = unstructured.SetNestedField(s.Object, "15m0s", "status", "authFreshness")
		_, _ = stages.Update(context.Background(), s, metav1.UpdateOptions{})
		return c, err
	}
	// The fresh-login cluster path then asks for the issuer the cluster
	// trusts: the keyless path was not taken.
	_, errOut, err := e.run(t)
	if err == nil || !strings.Contains(err.Error(), "this Stage asks for a fresh login to approve") ||
		!strings.Contains(errOut, "the Stage asks for a fresh login, which a certificate cannot show") || e.approvals(t, "delivery/approvals") != 0 {
		t.Fatalf("%q %v", errOut, err)
	}
}

func TestApproveKeepsClusterPathUnderTeamNarrowing(t *testing.T) {
	clusterPath(t, func(e *keylessEnv) { e.policy.TeamNarrowing = true }, "owner teams narrow who may answer")
}

func TestApproveKeepsClusterPathWithHumanGroups(t *testing.T) {
	clusterPath(t, func(e *keylessEnv) { e.policy.HumanGroups = []string{"team-payments"} }, "humanGroups is set")
}

func TestApproveKeepsClusterPathWithoutApprovalsRepository(t *testing.T) {
	clusterPath(t, func(e *keylessEnv) { delete(e.stage, "approvals") }, "the Stage keeps no approvals repository")
	clusterPath(t, func(e *keylessEnv) { e.policy.Signers = nil }, "the installation accepts no signed approvals")
	clusterPath(t, func(e *keylessEnv) {}, "the operation, its Stage or the identity policy could not be read", "--delivery-namespace", "elsewhere")
}

func TestApproveIgnoresAmbientToken(t *testing.T) {
	for _, env := range ambientTokens {
		t.Run(env, func(t *testing.T) {
			e := newKeylessEnv(t, "", "alice@acme.example")
			t.Setenv(env, "ambient")
			_, _, err := e.run(t)
			if err == nil || err.Error() != "approving needs your own fresh login; unset "+env || e.logins != 0 || e.created != 0 || e.approvals(t, "delivery/approvals") != 0 {
				t.Fatalf("%v, %d logins", err, e.logins)
			}
		})
	}
}

func TestApproveKeylessFailures(t *testing.T) {
	fail := func(email string, mod func(*keylessEnv), want string, args ...string) {
		t.Helper()
		e := newKeylessEnv(t, "", email)
		mod(e)
		if _, _, err := e.run(t, args...); err == nil || !strings.Contains(err.Error(), want) || e.created != 0 || e.approvals(t, "delivery/approvals") != 0 {
			t.Fatalf("want %q, got %v", want, err)
		}
	}
	ok := "alice@acme.example"
	fail(ok, func(e *keylessEnv) { e.policy.Signers[0].TrustedRoot = "" }, "names no trustedRoot")
	fail(ok, func(e *keylessEnv) { e.policy.Signers[0].TrustedRoot = "{" }, "the trusted root of the signer")
	fail(ok, func(*keylessEnv) {}, "CA bundle", "--oidc-ca-file", "/nonexistent/ca.crt")
	fail(ok, func(e *keylessEnv) {
		e.d.freshLogin = func(context.Context, authn.FreshConfig) (string, error) { return "", errors.New("login refused") }
	}, "login refused")
	fail("", func(*keylessEnv) {}, "carries no email claim")
	fail(ok, func(e *keylessEnv) { e.policy.Signers[0].TimestampURL = "" }, "keyless signing needs a timestamp authority")
	fail("ci@proj.iam.gserviceaccount.com", func(e *keylessEnv) { e.policy.Signers[0].Subjects = ".+" }, "AgentsMayNotApprove")
}

func TestApproveRoute(t *testing.T) {
	a := delivery.PersonSigner{Issuer: "https://a", Subjects: "x"}
	b := delivery.PersonSigner{Issuer: "https://b", Subjects: "x"}
	st := &delivery.StageObject{}
	st.Status.Stream.Approvals = "reg/approvals@sha256:x"
	if s, why := approveRoute(delivery.ClassPolicy{Signers: []delivery.PersonSigner{a, b}}, st, "https://b"); why != "" || s != b {
		t.Fatalf("named issuer: %+v %q", s, why)
	}
	if _, why := approveRoute(delivery.ClassPolicy{Signers: []delivery.PersonSigner{a, b}}, st, ""); why == "" {
		t.Fatal("two signers and no issuer picked one")
	}
	if s, why := approveRoute(delivery.ClassPolicy{Signers: []delivery.PersonSigner{a}}, st, ""); why != "" || s != a {
		t.Fatalf("the only signer: %+v %q", s, why)
	}
}
