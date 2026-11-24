package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"k8s.io/client-go/dynamic"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
)

const refApprovals = "delivery/streams-approvals"

var keylessStage = delivery.RecordStage{Cluster: "c", Namespace: "prod", Name: "payments-prod"}

// refEnv is keylessEnv with the recorder's key, its trust config and the
// identity policy in files, an intent record of held on the stream, and a
// cluster that fails the test when anything reads a kubeconfig.
type refEnv struct {
	*keylessEnv
	key            ed25519.PrivateKey
	trust, signers string
	intent         string
}

func newRefEnv(t *testing.T, email string) *refEnv {
	t.Helper()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	e := &refEnv{keylessEnv: newKeylessEnv(t, "", email), key: key, trust: writeTrust(t, pub)}
	// The chart's default approvals repository: the streams repository's
	// name with -approvals.
	e.stage["approvals"] = deliveryrecord.AnchorRef(e.host+"/"+refApprovals, keylessStream)
	if err := ocireferrer.Push(context.Background(), e.repo(refApprovals), deliveryrecord.Anchor(keylessStream)); err != nil {
		t.Fatal(err)
	}
	policy, _ := json.Marshal(e.policy)
	e.signers = filepath.Join(t.TempDir(), "identities.json")
	if err := os.WriteFile(e.signers, policy, 0o600); err != nil {
		t.Fatal(err)
	}
	op := delivery.OperationObject{Metadata: delivery.ObjectMeta{Name: "held", UID: "uid-held"},
		Spec: delivery.OperationSpec{IntentDigest: "sha256:i", Actor: delivery.Actor{User: "bob"}}}
	st := delivery.NewIntentRecord(keylessStage, deliveryrecord.Anchor(keylessStream).ManifestDesc.Digest.String(), e.stage["approvals"].(string), op, "2026-11-18T00:00:00Z")
	b, _, err := delivery.SignIntent(context.Background(), edSigner(key), st)
	if err != nil {
		t.Fatal(err)
	}
	if e.intent, err = deliveryrecord.Push(context.Background(), e.repo("delivery/streams"), keylessStream, capabilities.ReferrersNative, delivery.PredicateIntent, b, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", "/nonexistent")
	e.d.newDynamic = func() (dynamic.Interface, error) {
		t.Fatal("a record reference read a kubeconfig")
		return nil, nil
	}
	return e
}

// run runs the delivery command with args in format.
func (e *refEnv) run(t *testing.T, format string, args ...string) (string, string, error) {
	t.Helper()
	v := viper.New()
	v.Set(outputFormatKey, format)
	cmd := newDeliveryCommand(app.NewService(nil, nil), v, e.d)
	out, errOut := &strings.Builder{}, &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// approve approves held from its intent and returns the approval record's
// reference and id.
func (e *refEnv) approve(t *testing.T) (string, string) {
	t.Helper()
	out, errOut, err := e.run(t, "text", "approve", "--intent", e.intent, "--trust", e.trust, "--signers", e.signers, "--note", "ok")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.HasPrefix(out, "approval signed by alice@acme.example (keyless) on prod/held (Stage payments-prod), pushed to the approvals repository "+e.host+"/"+refApprovals+"@") ||
		!strings.Contains(errOut, "To sign approve on prod/held, log in again in your browser") {
		t.Fatalf("stdout %q stderr %q", out, errOut)
	}
	line := strings.Split(strings.TrimSpace(out), "\n")[1]
	id, ref, _ := strings.Cut(strings.TrimPrefix(line, "record "), " at ")
	return ref, id
}

func TestApproveIntentNeedsNoCluster(t *testing.T) {
	e := newRefEnv(t, "alice@acme.example")
	ref, _ := e.approve(t)
	if e.logins != 1 || e.created != 0 || e.approvals(t, refApprovals) != 1 || e.approvals(t, "delivery/streams") != 1 {
		t.Fatalf("%d logins, %d Approvals created, %d approval records (%s)", e.logins, e.created, e.approvals(t, refApprovals), ref)
	}
}

func TestRecordVerifyRefReadsNoKubeconfig(t *testing.T) {
	e := newRefEnv(t, "alice@acme.example")
	ref, id := e.approve(t)
	verify := []string{"record", "verify", ref, "--trust", e.trust, "--signers", e.signers}
	out, _, err := e.run(t, "text", verify...)
	if err != nil || !strings.HasPrefix(out, "approval approve on held by alice@acme.example (keyless, issuer "+keylessIssuer+
		"): decision recorded (signature verified); unknown: it counted only if the operation record lists it (") {
		t.Fatalf("%v\n%s", err, out)
	}
	// The operation concludes, its record listing the approval's.
	st := delivery.Statement{Type: delivery.StatementType, PredicateType: delivery.PredicateOperation, Predicate: delivery.RecordPredicate{
		Kind: delivery.RecordOperation, Intent: delivery.RecordIntent{UID: "uid-held", Name: "held", Stage: keylessStage},
		Approvals: []delivery.RecordApproval{{By: "oidc:alice@acme.example", Decision: delivery.Approve, Record: id}},
		Chain:     delivery.Chain{Stream: keylessStream, Seq: 1}}}
	b, _, _ := delivery.SignRecord(context.Background(), edSigner(e.key), st)
	anchor := deliveryrecord.Anchor(keylessStream).ManifestDesc
	art := ocireferrer.Build(delivery.BundleMediaType, delivery.BundleMediaType, b, &anchor, map[string]string{deliveryrecord.AnnPredicateType: delivery.PredicateOperation})
	if err := ocireferrer.Push(context.Background(), e.repo("delivery/streams"), art); err != nil {
		t.Fatal(err)
	}
	if out, _, err := e.run(t, "text", verify...); err != nil || !strings.Contains(out, "decision recorded (signature verified); counted\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, _, err := e.run(t, "json", verify...); err != nil || !strings.Contains(out, `"counted": "counted"`) {
		t.Fatalf("%v\n%s", err, out)
	}
	opRef := e.host + "/delivery/streams@" + art.ManifestDesc.Digest.String()
	if out, _, err := e.run(t, "text", "record", "verify", opRef, "--trust", e.trust); err != nil || !strings.Contains(out, "operation held:") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, _, err := e.run(t, "text", "record", "verify", opRef); err == nil || !strings.Contains(err.Error(), "does not verify: no trust config") {
		t.Fatal(err)
	}
	// Without the signers a person's approval does not verify.
	out, _, err = e.run(t, "text", "record", "verify", ref, "--trust", e.trust)
	if err == nil || !strings.Contains(out, "approval approve on held by a person (unverified): unverified: ") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestRecordGetRefWithoutStage(t *testing.T) {
	e := newRefEnv(t, "alice@acme.example")
	out, _, err := e.run(t, "text", "record", "get", e.intent, "--trust", e.trust)
	if err != nil || !strings.HasPrefix(out, "intent held on prod/payments-prod asked by bob: signature verified with trust config sha256:") ||
		!strings.Contains(out, "approve it with: pacto delivery approve --intent "+e.intent+" --trust <recorder public key> --signers") {
		t.Fatalf("%v\n%s", err, out)
	}
	// get answers an unverified record and fails nothing.
	if out, _, err := e.run(t, "text", "record", "get", e.intent); err != nil || strings.Contains(out, "approve it with") || !strings.Contains(out, "no trust config was given") {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, _, err := e.run(t, "text", "record", "get", e.host+"/delivery/streams"); err == nil || !strings.Contains(err.Error(), "is not a record reference") {
		t.Fatal(err)
	}
}

func TestRecordGetRefPrintsSigner(t *testing.T) {
	e := newRefEnv(t, "alice@acme.example")
	ref, _ := e.approve(t)
	out, _, err := e.run(t, "text", "record", "get", ref, "--signers", e.signers)
	if err != nil || !strings.Contains(out, "by alice@acme.example (keyless, issuer "+keylessIssuer+")") || !strings.Contains(out, "no trust config was given") {
		t.Fatalf("%v\n%s", err, out)
	}
	yamlPolicy := filepath.Join(t.TempDir(), "identities.yaml")
	raw, _ := os.ReadFile(e.signers)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	signers := doc["signers"].([]any)[0].(map[string]any)
	y := "signers:\n- issuer: " + keylessIssuer + "\n  usernamePrefix: 'oidc:'\n  subjects: '" + signers["subjects"].(string) + "'\n  trustedRoot: |\n    " +
		strings.ReplaceAll(signers["trustedRoot"].(string), "\n", "\n    ") + "\n"
	if err := os.WriteFile(yamlPolicy, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, _, err := e.run(t, "text", "record", "get", ref, "--signers", yamlPolicy); err != nil || !strings.Contains(out, "(keyless, issuer ") {
		t.Fatalf("a YAML policy: %v\n%s", err, out)
	}
}

func TestRecordGetNeedsStageOrRef(t *testing.T) {
	e := newRefEnv(t, "alice@acme.example")
	if _, _, err := e.run(t, "text", "record", "get", "sha256:abc", "-n", "prod"); err == nil || err.Error() != "pass --stage or a record reference" {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "bad")
	_ = os.WriteFile(bad, []byte("signers: 3\n"), 0o600)
	notYAML := filepath.Join(t.TempDir(), "notyaml")
	_ = os.WriteFile(notYAML, []byte("{"), 0o600)
	for _, args := range [][]string{
		{"record", "get", e.intent, "--signers", bad},
		{"record", "get", e.intent, "--signers", notYAML},
		{"record", "get", e.intent, "--signers", filepath.Join(t.TempDir(), "missing")},
		{"record", "get", e.intent, "--trust", filepath.Join(t.TempDir(), "missing")},
		{"approve", "--intent", e.intent, "--signers", bad},
	} {
		if _, _, err := e.run(t, "text", args...); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}

func TestApproveIntentRefusesUnverifiedIntent(t *testing.T) {
	e := newRefEnv(t, "alice@acme.example")
	if _, _, err := e.run(t, "text", "approve", "--intent", e.intent, "--signers", e.signers); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatal(err)
	}
	if _, _, err := e.run(t, "text", "approve", "--intent", e.intent, "--trust", e.trust); err == nil || !strings.Contains(err.Error(), "approving from an intent needs a signer") {
		t.Fatal(err)
	}
	// A workload's certificate never approves.
	w := newRefEnv(t, "ci@robots.example")
	if _, _, err := w.run(t, "text", "approve", "--intent", w.intent, "--trust", w.trust, "--signers", w.signers); err == nil || !strings.Contains(err.Error(), "AgentsMayNotApprove") {
		t.Fatal(err)
	}
	t.Setenv("SIGSTORE_ID_TOKEN", "ambient")
	if _, _, err := e.run(t, "text", "approve", "--intent", e.intent, "--trust", e.trust, "--signers", e.signers); err == nil || !strings.Contains(err.Error(), "unset SIGSTORE_ID_TOKEN") {
		t.Fatal(err)
	}
	if e.logins != 0 || e.approvals(t, refApprovals) != 0 {
		t.Fatalf("%d logins", e.logins)
	}
}

func TestApproveRefusesNameAndIntent(t *testing.T) {
	e := newRefEnv(t, "alice@acme.example")
	for _, args := range [][]string{{"approve", "-n", "prod", "held", "--intent", e.intent}, {"approve"}} {
		if _, _, err := e.run(t, "text", args...); err != errApproveOne {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestApproveIntentHonoursTheNamedIssuer(t *testing.T) {
	e := newRefEnv(t, "alice@acme.example")
	base := []string{"approve", "--intent", e.intent, "--trust", e.trust, "--signers", e.signers}
	if _, _, err := e.run(t, "text", append(base, "--oidc-issuer-url", "https://idp.other.example")...); err == nil ||
		!strings.Contains(err.Error(), "names no signer for the issuer https://idp.other.example") {
		t.Fatal(err)
	}
	if e.logins != 0 {
		t.Fatalf("%d logins for an issuer the policy does not name", e.logins)
	}
	out, _, err := e.run(t, "text", append(base, "--oidc-issuer-url", keylessIssuer)...)
	if err != nil || !strings.Contains(out, "the promoter decides whether it counts") || !strings.Contains(out, "check with: pacto delivery record verify "+e.host) {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestApproveIntentRefusesHumanGroupsBeforeLogin(t *testing.T) {
	e := newRefEnv(t, "alice@acme.example")
	e.policy.HumanGroups = []string{"people"}
	policy, _ := json.Marshal(e.policy)
	_ = os.WriteFile(e.signers, policy, 0o600)
	if _, _, err := e.run(t, "text", "approve", "--intent", e.intent, "--trust", e.trust, "--signers", e.signers); err == nil ||
		!strings.Contains(err.Error(), "humanGroups is set") || e.logins != 0 {
		t.Fatalf("%v, %d logins", err, e.logins)
	}
}

func TestRecordRefRefusesMixedFlags(t *testing.T) {
	e := newRefEnv(t, "alice@acme.example")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"record", "get", e.intent, "--stage", "s"}, "pass a record reference or --stage, not both"},
		{[]string{"record", "verify", e.intent, "--stage", "s"}, "pass a record reference or --stage, not both"},
		{[]string{"record", "verify", e.intent, "--all"}, "pass a record reference or --all, not both"},
		{[]string{"approve", "--intent", e.intent, "--keyless=false"}, "--intent always signs keyless"},
	} {
		if _, _, err := e.run(t, "text", c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v", c.args, err)
		}
	}
	if e.logins != 0 {
		t.Fatalf("%d logins", e.logins)
	}
}
