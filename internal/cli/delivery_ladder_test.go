package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

// ladderEnv is a cluster env whose promoter publishes view, with ladder
// records read from the approvals repository beside the env's registry.
type ladderEnv struct {
	*clusterEnv
	approvals string
	view      delivery.EnvironmentsView
	client    *dynamicfake.FakeDynamicClient
}

func newLadderEnv(t *testing.T, mode capabilities.ReferrersMode) *ladderEnv {
	t.Helper()
	e := &ladderEnv{clusterEnv: newClusterEnv(t, mode)}
	e.approvals = strings.TrimSuffix(e.repo, delivery.TargetSnapshotRepository) + "approvals"
	p := delivery.LadderProposal{
		{Name: "preview", Source: "namespaces *-preview, 4 found", Provenance: delivery.RungInferred},
		{Name: "qa", Source: "the cluster name eu-qa", Provenance: delivery.RungInferred}}
	e.view = delivery.EnvironmentsView{
		Declared: []delivery.LadderRung{{Name: "staging", Source: "chart", Provenance: delivery.RungDeclared},
			{Name: "production", Upstream: "staging", Source: "chart", Provenance: delivery.RungDeclared}},
		Proposal: p, ProposalDigest: delivery.ProposalDigest(p), Repository: e.approvals, Stream: delivery.LadderStream(clusterHubUID)}
	e.d.newDynamic = func() (dynamic.Interface, error) {
		e.client = e.hub()
		e.publishView(t)
		return e.client, nil
	}
	return e
}

// publishView writes e.view as the promoter's environments ConfigMap.
func (e *ladderEnv) publishView(t *testing.T) {
	t.Helper()
	b, _ := json.Marshal(e.view)
	cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"namespace": delivery.PolicyNamespace, "name": delivery.EnvironmentsConfigMap},
		"data":     map[string]any{"view.json": string(b)}}}
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	if err := e.client.Tracker().Update(gvr, cm, delivery.PolicyNamespace); err != nil {
		if err := e.client.Tracker().Create(gvr, cm, delivery.PolicyNamespace); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *ladderEnv) confirm(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return runRoot(t, e.d, nil, append([]string{"delivery", "ladder", "confirm"}, args...)...)
}

// records are the ladder records in the approvals repository.
func (e *ladderEnv) records(t *testing.T) []deliveryrecord.Fetched {
	t.Helper()
	repo, _ := ocireferrer.Open(e.approvals, ocireferrer.Options{})
	if _, err := repo.Resolve(t.Context(), deliveryrecord.AnchorTag(e.view.Stream)); err != nil {
		return nil // nothing was ever written
	}
	recs, _, _, err := (&deliveryrecord.Reader{}).ListLadder(t.Context(), repo, clusterHubUID)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

// signed is the one ladder record, verified as alice's.
func (e *ladderEnv) signed(t *testing.T) delivery.LadderRecord {
	t.Helper()
	recs := e.records(t)
	if len(recs) != 1 {
		t.Fatalf("%d records", len(recs))
	}
	st, _, err := delivery.OpenLadderRecord(recs[0].Bundle, func(b []byte) (time.Time, error) {
		id, at, err := signing.VerifyPerson(b, clusterIssuer, e.policy.Signers[0].TrustedRoot)
		if err == nil && id.Subject != "alice@acme.com" {
			err = errors.New(id.Subject)
		}
		return at, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return st.Predicate
}

func TestConfirmSignsTheShownProposal(t *testing.T) {
	for _, mode := range []capabilities.ReferrersMode{capabilities.ReferrersNative, capabilities.ReferrersWriteOnceTags} {
		t.Run(string(mode), func(t *testing.T) {
			e := newLadderEnv(t, mode)
			out, errOut, err := e.confirm(t, "--proposal", e.view.ProposalDigest)
			if err != nil || !strings.HasPrefix(out, "confirmed staging -> production + preview + qa as "+alice+"; recorded "+e.approvals+"@sha256:") ||
				!strings.Contains(errOut, "Confirming the installation ladder staging -> production + preview + qa") ||
				!strings.Contains(errOut, "To sign a confirmation on the installation ladder of cluster "+clusterHubUID) || e.logins != 1 {
				t.Fatalf("%q %q %v", out, errOut, err)
			}
			e.checkSigned(t, mode, out)
		})
	}
	// No --proposal signs the proposal the command prints first.
	e := newLadderEnv(t, capabilities.ReferrersNative)
	if out, _, err := e.confirm(t); err != nil || !strings.HasPrefix(out, "confirmed staging") {
		t.Fatalf("%q %v", out, err)
	}
}

// checkSigned checks the one record is the whole proposal, signed by alice,
// and that a write-once registry's tag was printed in out.
func (e *ladderEnv) checkSigned(t *testing.T, mode capabilities.ReferrersMode, out string) {
	t.Helper()
	if mode == capabilities.ReferrersWriteOnceTags && !strings.Contains(out, "\npushed "+e.approvals+":"+deliveryrecord.AnchorTag(e.view.Stream)+"-ladder-") {
		t.Fatalf("no tag: %q", out)
	}
	rec := e.signed(t)
	if rec.ClusterID != clusterHubUID || rec.ProposalDigest != e.view.ProposalDigest || len(rec.Rungs) != 4 || rec.Dropped != nil ||
		rec.Signer.Subject != "alice@acme.com" || rec.Signer.Issuer != clusterIssuer {
		t.Fatalf("%+v", rec)
	}
}

// ladderRefused runs confirm under mod and checks it fails with want, nothing
// written and logins logins.
func ladderRefused(t *testing.T, mod func(*ladderEnv), want string, logins int, args ...string) {
	t.Helper()
	e := newLadderEnv(t, capabilities.ReferrersNative)
	mod(e)
	_, _, err := e.confirm(t, args...)
	if err == nil || !strings.Contains(err.Error(), want) || len(e.records(t)) != 0 || e.logins != logins {
		t.Fatalf("want %q, got %v, %d logins", want, err, e.logins)
	}
}

func TestConfirmRefusesChangedProposal(t *testing.T) {
	ladderRefused(t, func(*ladderEnv) {}, app.ErrLadderChanged.Error(), 0, "--proposal", "sha256:"+strings.Repeat("0", 64))
	// The proposal changes while the person logs in.
	ladderRefused(t, func(e *ladderEnv) {
		e.onLogin = func() {
			e.view.Proposal = e.view.Proposal[:1]
			e.view.ProposalDigest = delivery.ProposalDigest(e.view.Proposal)
			e.publishView(t)
		}
	}, app.ErrLadderChanged.Error(), 1)
}

func TestConfirmDropKeepsTheRest(t *testing.T) {
	e := newLadderEnv(t, capabilities.ReferrersNative)
	out, _, err := e.confirm(t, "--drop", "qa")
	if err != nil || !strings.HasPrefix(out, "confirmed staging -> production + preview as ") {
		t.Fatalf("%q %v", out, err)
	}
	if rec := e.signed(t); len(rec.Rungs) != 3 || rec.Rungs[2].Name != "preview" || len(rec.Dropped) != 1 || rec.Dropped[0] != "qa" {
		t.Fatalf("%+v", rec)
	}
	ladderRefused(t, func(*ladderEnv) {}, "staging comes from delivery.environments.ladder: change the chart value", 0, "--drop", "staging")
}

func TestConfirmRefusesWithoutSelfSAR(t *testing.T) {
	ladderRefused(t, func(e *ladderEnv) { e.mayApprove = false },
		"LadderConfirmForbidden: you may not approve environmentladders in the cluster; ask for kubectl create clusterrole pacto-ladder-approver", 0)
}

func TestConfirmRefusesGroupOnlyGrant(t *testing.T) {
	ladderRefused(t, func(e *ladderEnv) { e.userReview = func(string) (bool, error) { return false, nil } },
		"LadderConfirmNeedsUserGrant: approve must be granted to your user "+alice+"; groups are not checked", 0)
}

func TestConfirmRefusesAmbientToken(t *testing.T) {
	for _, env := range ambientTokens {
		ladderRefused(t, func(*ladderEnv) { t.Setenv(env, "ambient") }, "approving needs your own fresh login; unset "+env, 0)
	}
}

func TestConfirmNothingProposed(t *testing.T) {
	e := newLadderEnv(t, capabilities.ReferrersNative)
	e.view.Proposal, e.view.ProposalDigest = delivery.LadderProposal{}, ""
	e.view.Confirmed = []delivery.LadderRung{{Name: "preview", Source: "namespaces *-preview, 4 found", Provenance: delivery.RungConfirmed}}
	out, _, err := e.confirm(t)
	if err != nil || out != "nothing to confirm: the installation ladder is staging -> production + preview\n" || e.logins != 0 {
		t.Fatalf("%q %v", out, err)
	}
	out, _, err = e.confirm(t, "--output-format", "json")
	var res ladderConfirmation
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Note != "nothing to confirm" || len(res.Ladder) != 3 {
		t.Fatalf("%q %v", out, err)
	}
}

func TestConfirmJSONOutput(t *testing.T) {
	e := newLadderEnv(t, capabilities.ReferrersWriteOnceTags)
	out, _, err := e.confirm(t, "--drop", "preview", "--output-format", "json")
	var res ladderConfirmation
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.By != alice || len(res.Ladder) != 3 || res.Dropped[0] != "preview" ||
		res.Mode != capabilities.ReferrersWriteOnceTags || !strings.HasPrefix(res.Record, e.approvals+"@sha256:") {
		t.Fatalf("%q %v", out, err)
	}
}

// TestConfirmRefusals: every read or check that fails before the login, and
// a login that does not sign as the reviewed person, gets nothing signed.
func TestConfirmRefusals(t *testing.T) {
	ladderRefused(t, func(e *ladderEnv) {
		e.d.newDynamic = func() (dynamic.Interface, error) { return nil, errors.New("no cluster") }
	}, "no cluster", 0)
	ladderRefused(t, func(*ladderEnv) {}, "trust", 0, "--trust", "/nonexistent")
	ladderRefused(t, func(e *ladderEnv) {
		e.d.newDynamic = func() (dynamic.Interface, error) { e.client = e.hub(); return e.client, nil }
	}, "publishes no installation ladder", 0)
	ladderRefused(t, func(e *ladderEnv) { e.view.Repository = "" }, "the promoter reads no ladder records", 0)
	ladderRefused(t, func(e *ladderEnv) { e.view.Stream = delivery.LadderStream("other") }, "names the stream other/pacto-delivery/ladder, not one of this cluster", 0)
	ladderRefused(t, func(e *ladderEnv) {
		next := e.d.newDynamic
		e.d.newDynamic = func() (dynamic.Interface, error) {
			c, _ := next()
			e.client.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("down") })
			return c, nil
		}
	}, "reading the cluster id: down", 0)
	ladderRefused(t, func(e *ladderEnv) { e.noPolicy = true }, "the installation's approver signers could not be read", 0)
	ladderRefused(t, func(e *ladderEnv) { e.policy.Signers = nil }, "this installation accepts no signed approvals", 0)
	ladderRefused(t, func(*ladderEnv) {}, "pick the approver signer with --issuer: the installation accepts "+clusterIssuer, 0, "--issuer", "https://other")
	ladderRefused(t, func(e *ladderEnv) { e.email = "ci@proj.iam.gserviceaccount.com" }, "AgentsMayNotApprove", 1)
	ladderRefused(t, func(e *ladderEnv) {
		e.user = "oidc:bob@acme.com"
		e.userReview = func(string) (bool, error) { return true, nil }
	}, "ApproverMismatch: your Kubernetes user is oidc:bob@acme.com and your login signs as "+alice, 1)
	ladderRefused(t, func(e *ladderEnv) { e.policy.Signers[0].Subjects = "" }, "has no subjects", 1)
}

// withCandidates makes the promoter list candidates as the namespaces it
// waits to create, readable by the caller unless forbidden.
func (e *ladderEnv) withCandidates(t *testing.T, candidates string, forbidden bool) {
	t.Helper()
	next := e.d.newDynamic
	e.d.newDynamic = func() (dynamic.Interface, error) {
		c, err := next()
		cm := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"namespace": delivery.PolicyNamespace, "name": delivery.PlacementsConfigMap},
			"data":     map[string]any{delivery.NamespaceCandidatesKey: candidates}}}
		if err := e.client.Tracker().Create(schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, cm, delivery.PolicyNamespace); err != nil {
			t.Fatal(err)
		}
		if forbidden {
			e.client.PrependReactor("get", "configmaps", func(a k8stesting.Action) (bool, runtime.Object, error) {
				if a.(k8stesting.GetAction).GetName() != delivery.PlacementsConfigMap {
					return false, nil, nil
				}
				return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, delivery.PlacementsConfigMap, errors.New("no"))
			})
		}
		return c, err
	}
}

func TestConfirmCreateNamespacesSignsTheirDigest(t *testing.T) {
	e := newLadderEnv(t, capabilities.ReferrersNative)
	e.withCandidates(t, `["payments-staging","production"]`, false)
	out, _, err := e.confirm(t, "--create-namespaces")
	if err != nil || !strings.Contains(out, "\nallowed Pacto to create the namespaces payments-staging, production\n") {
		t.Fatalf("%q %v", out, err)
	}
	if rec := e.signed(t); rec.NamespacesDigest != delivery.NamespacesDigest([]string{"production", "payments-staging"}) || len(rec.Rungs) != 4 {
		t.Fatalf("%+v", rec)
	}
	// With nothing proposed, the namespaces alone are confirmed.
	e = newLadderEnv(t, capabilities.ReferrersNative)
	e.view.Proposal, e.view.ProposalDigest = delivery.LadderProposal{}, ""
	e.withCandidates(t, `["staging"]`, false)
	if out, _, err := e.confirm(t, "--create-namespaces"); err != nil || !strings.HasPrefix(out, "confirmed staging -> production as ") {
		t.Fatalf("%q %v", out, err)
	}
	if rec := e.signed(t); rec.ProposalDigest != "" || rec.NamespacesDigest != delivery.NamespacesDigest([]string{"staging"}) {
		t.Fatalf("%+v", rec)
	}
}

func TestConfirmCreateNamespacesRefusals(t *testing.T) {
	ladderRefused(t, func(e *ladderEnv) { e.withCandidates(t, `[]`, false) }, "no namespace waits to be created", 0, "--create-namespaces")
	ladderRefused(t, func(e *ladderEnv) { e.withCandidates(t, `["staging"]`, true) },
		"you may not read the namespaces waiting to be created in pacto-delivery/"+delivery.PlacementsConfigMap+", so you cannot allow them", 0, "--create-namespaces")
}
