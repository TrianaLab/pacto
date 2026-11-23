package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer/capabilities"
	"github.com/trianalab/pacto/v4/pkg/signing"
)

const ladderCluster = "cid00000-0000-4000-8000-000000000000"

// ladderView is a view with the chart chain staging -> production and the
// proposal preview, qa.
func ladderView() delivery.EnvironmentsView {
	p := delivery.LadderProposal{
		{Name: "preview", Source: "namespaces *-preview, 4 found", Provenance: delivery.RungInferred},
		{Name: "qa", Source: "the cluster name eu-qa", Provenance: delivery.RungInferred}}
	return delivery.EnvironmentsView{
		Declared: []delivery.LadderRung{{Name: "staging", Source: "chart", Provenance: delivery.RungDeclared},
			{Name: "production", Upstream: "staging", Source: "chart", Provenance: delivery.RungDeclared}},
		Proposal: p, ProposalDigest: delivery.ProposalDigest(p), Stream: delivery.LadderStream(ladderCluster)}
}

// ladderConfigMap is the promoter's environments ConfigMap holding view.
func ladderConfigMap(view string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"namespace": delivery.PolicyNamespace, "name": delivery.EnvironmentsConfigMap},
		"data":     map[string]any{"view.json": view}}}
}

func ladderDelivery(objs ...runtime.Object) *Delivery {
	return &Delivery{Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objs...), Now: time.Now,
		Records: func(path string, _ ...string) (*remote.Repository, error) {
			return ocireferrer.Open(path, ocireferrer.Options{})
		}}
}

func viewJSON(v delivery.EnvironmentsView) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestLadderViewReads(t *testing.T) {
	got, err := ladderDelivery(ladderConfigMap(viewJSON(ladderView()))).LadderView(t.Context())
	if err != nil || got.ProposalDigest != ladderView().ProposalDigest || len(got.Declared) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := ladderDelivery().LadderView(t.Context()); err == nil || !strings.Contains(err.Error(), "publishes no installation ladder in pacto-delivery: it needs delivery.environments") {
		t.Fatalf("missing: %v", err)
	}
	if _, err := ladderDelivery(ladderConfigMap("{")).LadderView(t.Context()); err == nil || !strings.Contains(err.Error(), "the installation ladder in pacto-delivery/"+delivery.EnvironmentsConfigMap) {
		t.Fatalf("bad JSON: %v", err)
	}
	d := ladderDelivery()
	d.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("down")
	})
	if _, err := d.LadderView(t.Context()); err == nil || !strings.Contains(err.Error(), "reading the installation ladder in pacto-delivery: down") {
		t.Fatalf("read: %v", err)
	}
}

func TestLadderRungsDropKeepsTheRest(t *testing.T) {
	rungs, dropped, err := LadderRungs(ladderView(), []string{"qa", "qa"})
	if err != nil || len(rungs) != 3 || rungs[2].Name != "preview" || rungs[2].Upstream != "" || rungs[2].Provenance != delivery.RungInferred ||
		len(dropped) != 1 || dropped[0] != "qa" {
		t.Fatalf("%+v %v %v", rungs, dropped, err)
	}
	if rungs, dropped, err := LadderRungs(ladderView(), nil); err != nil || len(rungs) != 4 || dropped != nil {
		t.Fatalf("%+v %v %v", rungs, dropped, err)
	}
	if _, _, err := LadderRungs(ladderView(), []string{"staging"}); err == nil || err.Error() != "staging comes from delivery.environments.ladder: change the chart value" {
		t.Fatalf("declared: %v", err)
	}
	if _, _, err := LadderRungs(ladderView(), []string{"dev"}); err == nil || err.Error() != "dev is not proposed: the proposal holds preview, qa" {
		t.Fatalf("unknown: %v", err)
	}
}

// ladderRecord is alice's confirmation of ladderView minus qa.
func ladderRecord(t *testing.T) delivery.LadderRecord {
	t.Helper()
	rungs, dropped, err := LadderRungs(ladderView(), []string{"qa"})
	if err != nil {
		t.Fatal(err)
	}
	return delivery.LadderRecord{ClusterID: ladderCluster, Rungs: rungs, ProposalDigest: ladderView().ProposalDigest, Dropped: dropped,
		Signer: delivery.PublisherIdentity{Issuer: joinIssuer, Subject: "alice@acme.com"}, At: time.Now().UTC().Format(time.RFC3339)}
}

// TestWriteLadderPushesWhatThePromoterReads: in either form the record is on
// the cluster's ladder stream, verifies as alice's and holds what she kept.
func TestWriteLadderPushesWhatThePromoterReads(t *testing.T) {
	for _, mode := range bothModes {
		t.Run(string(mode), func(t *testing.T) {
			e := newJoinEnv(t, mode)
			d := ladderDelivery(ladderConfigMap(viewJSON(ladderView())))
			ref, got, err := d.WriteLadder(t.Context(), ladderRecord(t), e.personSigner(t), e.repo)
			if err != nil || got != mode || !strings.HasPrefix(ref, e.repo+"@sha256:") {
				t.Fatalf("%q %q %v", ref, got, err)
			}
			repo, _ := ocireferrer.Open(e.repo, ocireferrer.Options{})
			recs, _, _, err := (&deliveryrecord.Reader{}).ListLadder(t.Context(), repo, ladderCluster)
			if err != nil || len(recs) != 1 {
				t.Fatalf("%d records, %v", len(recs), err)
			}
			st, _, err := delivery.OpenLadderRecord(recs[0].Bundle, func(b []byte) (time.Time, error) {
				id, at, err := signing.VerifyPerson(b, joinIssuer, e.policy.Signers[0].TrustedRoot)
				if err == nil && id.Subject != "alice@acme.com" {
					err = errors.New(id.Subject)
				}
				return at, err
			})
			if err != nil || len(st.Predicate.Rungs) != 3 || st.Predicate.Dropped[0] != "qa" {
				t.Fatalf("%+v %v", st.Predicate, err)
			}
		})
	}
}

// TestWriteLadderRefuses: a view that cannot be read or whose proposal
// changed, a record Check refuses, a repository that cannot be opened or
// reached and a signer that fails get nothing written.
func TestWriteLadderRefuses(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	view := viewJSON(ladderView())
	changed := ladderView()
	changed.ProposalDigest = "sha256:" + strings.Repeat("0", 64)
	noAt := ladderRecord(t)
	noAt.At = "soon"
	down := signerFunc(func(context.Context, string, []byte) ([]byte, error) { return nil, errors.New("signer down") })
	for _, c := range []struct {
		d      *Delivery
		rec    delivery.LadderRecord
		signer delivery.Signer
		repo   string
		want   string
	}{
		{ladderDelivery(), ladderRecord(t), down, e.repo, "publishes no installation ladder"},
		{ladderDelivery(ladderConfigMap(viewJSON(changed))), ladderRecord(t), down, e.repo, ErrLadderChanged.Error()},
		{ladderDelivery(ladderConfigMap(view)), noAt, down, e.repo, `at "soon" is not RFC 3339`},
		{ladderDelivery(ladderConfigMap(view)), ladderRecord(t), down, "Not A Repo", "invalid"},
		{ladderDelivery(ladderConfigMap(view)), ladderRecord(t), down, e.repo, "signer down"},
		{ladderDelivery(ladderConfigMap(view)), ladderRecord(t), e.personSigner(t), "127.0.0.1:1/acme/approvals", "127.0.0.1:1"},
	} {
		if _, _, err := c.d.WriteLadder(t.Context(), c.rec, c.signer, c.repo); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("want %q, got %v", c.want, err)
		}
	}
}

func TestLadderRungsSignConfirmedAgain(t *testing.T) {
	v := ladderView()
	v.Confirmed = []delivery.LadderRung{{Name: "dev", Source: "the cluster name eu-dev", Provenance: delivery.RungConfirmed}}
	rungs, _, err := LadderRungs(v, []string{"qa"})
	if err != nil || len(rungs) != 4 || rungs[2] != (delivery.LadderRung{Name: "dev", Source: "the cluster name eu-dev", Provenance: delivery.RungInferred}) {
		t.Fatalf("%+v %v", rungs, err)
	}
}

// placementsConfigMap is the promoter's private ConfigMap listing
// candidates under its namespaces key.
func placementsConfigMap(candidates string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"namespace": delivery.PolicyNamespace, "name": delivery.PlacementsConfigMap},
		"data":     map[string]any{delivery.NamespaceCandidatesKey: candidates}}}
}

func TestNamespaceCandidates(t *testing.T) {
	got, err := ladderDelivery(placementsConfigMap(`["payments-staging","orders-staging"]`)).NamespaceCandidates(t.Context())
	if err != nil || len(got) != 2 || got[0] != "payments-staging" {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := ladderDelivery().NamespaceCandidates(t.Context()); err != nil || got != nil {
		t.Fatalf("none: %v %v", got, err)
	}
	if got, err := ladderDelivery(placementsConfigMap("")).NamespaceCandidates(t.Context()); err != nil || got != nil {
		t.Fatalf("empty: %v %v", got, err)
	}
	if _, err := ladderDelivery(placementsConfigMap("{")).NamespaceCandidates(t.Context()); err == nil || !strings.Contains(err.Error(), "waiting to be created in pacto-delivery/") {
		t.Fatalf("bad JSON: %v", err)
	}
	for _, c := range []struct {
		err  error
		want string
	}{
		{apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, delivery.PlacementsConfigMap, errors.New("no")), "you may not read the namespaces"},
		{errors.New("down"), "reading the namespaces waiting to be created in pacto-delivery: down"},
	} {
		d := ladderDelivery()
		d.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, c.err
		})
		if _, err := d.NamespaceCandidates(t.Context()); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("want %q, got %v", c.want, err)
		}
	}
}

func TestWriteLadderBindsNamespaceCandidates(t *testing.T) {
	e := newJoinEnv(t, capabilities.ReferrersNative)
	rec := ladderRecord(t)
	rec.NamespacesDigest = delivery.NamespacesDigest([]string{"payments-staging"})
	view := ladderConfigMap(viewJSON(ladderView()))
	if _, _, err := ladderDelivery(view, placementsConfigMap(`["payments-staging"]`)).WriteLadder(t.Context(), rec, e.personSigner(t), e.repo); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ladderDelivery(view, placementsConfigMap(`["orders-staging"]`)).WriteLadder(t.Context(), rec, e.personSigner(t), e.repo); !errors.Is(err, ErrNamespacesChanged) {
		t.Fatalf("changed: %v", err)
	}
	if _, _, err := ladderDelivery(view, placementsConfigMap("{")).WriteLadder(t.Context(), rec, e.personSigner(t), e.repo); err == nil {
		t.Fatal("unreadable candidates")
	}
}

// inNamespace moves a ConfigMap fixture to ns.
func inNamespace(u *unstructured.Unstructured, ns string) *unstructured.Unstructured {
	_ = unstructured.SetNestedField(u.Object, ns, "metadata", "namespace")
	return u
}

func rowsConfigMap(ns, rows string) *unstructured.Unstructured {
	u := inNamespace(placementsConfigMap(""), ns)
	_ = unstructured.SetNestedField(u.Object, rows, "data", "rows.json")
	return u
}

func TestEnvironmentsReadsConfiguredNamespace(t *testing.T) {
	rows := `[{"service":"payments","environment":"production","source":"ladder","reason":"waiting"}]`
	d := ladderDelivery(inNamespace(ladderConfigMap(viewJSON(ladderView())), "ops"), rowsConfigMap("ops", rows),
		ladderConfigMap(`{"proposalDigest":"sha256:default"}`))
	d.PolicyNamespace = "ops"
	view, got, readable, err := d.Environments(t.Context())
	if err != nil || view == nil || view.ProposalDigest != ladderView().ProposalDigest || !readable || len(got) != 1 || got[0].Service != "payments" {
		t.Fatalf("%+v %+v %v %v", view, got, readable, err)
	}
	d.PolicyNamespace = ""
	if view, got, readable, err := d.Environments(t.Context()); view != nil || got != nil || readable || err != nil {
		t.Fatalf("no delivery namespace, nothing read: %+v %v %v %v", view, got, readable, err)
	}
}

func TestEnvironmentsAnswers(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, delivery.PlacementsConfigMap, errors.New("no"))
	view := ladderConfigMap(viewJSON(ladderView()))
	for _, c := range []struct {
		name               string
		objs               []runtime.Object
		rowsErr            error
		wantView, readable bool
		wantRows           int
		wantErr            string
	}{
		{name: "no ladder"},
		{name: "bad view", objs: []runtime.Object{ladderConfigMap("{")}, wantErr: "the installation ladder in pacto-delivery/"},
		{name: "no rows yet", objs: []runtime.Object{view}, wantView: true, readable: true},
		{name: "empty rows", objs: []runtime.Object{view, placementsConfigMap("")}, wantView: true, readable: true},
		{name: "rows", objs: []runtime.Object{view, rowsConfigMap(delivery.PolicyNamespace, `[{"service":"a"},{"service":"b"}]`)}, wantView: true, readable: true, wantRows: 2},
		{name: "bad rows", objs: []runtime.Object{view, rowsConfigMap(delivery.PolicyNamespace, "{")}, wantErr: "the placements in pacto-delivery/"},
		{name: "rows hidden", objs: []runtime.Object{view}, rowsErr: forbidden, wantView: true},
		{name: "rows down", objs: []runtime.Object{view}, rowsErr: errors.New("down"), wantErr: "reading the placements in pacto-delivery: down"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := ladderDelivery(c.objs...)
			d.PolicyNamespace = delivery.PolicyNamespace
			if c.rowsErr != nil {
				d.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "configmaps", func(a k8stesting.Action) (bool, runtime.Object, error) {
					return a.(k8stesting.GetAction).GetName() == delivery.PlacementsConfigMap, nil, c.rowsErr
				})
			}
			v, rows, readable, err := d.Environments(t.Context())
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) || v != nil {
					t.Fatalf("want %q, got %v %+v", c.wantErr, err, v)
				}
				return
			}
			if err != nil || (v != nil) != c.wantView || readable != c.readable || len(rows) != c.wantRows {
				t.Fatalf("%+v %+v %v %v", v, rows, readable, err)
			}
		})
	}
}
