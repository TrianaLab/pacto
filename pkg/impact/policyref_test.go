package impact

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/lock"
)

const (
	policyRef  = "oci://x/policy"
	openPolicy = `{"type":"object"}`
	// tierPolicy requires metadata, which a referrer without it fails; the
	// schema diff classifies the change BREAKING.
	tierPolicy = `{"type":"object","required":["metadata"]}`
)

func policySchema(s string) fstest.MapFS {
	return fstest.MapFS{"policy/schema.json": {Data: []byte(s)}}
}

// policyProvider is the policy service at version, publishing
// policy/schema.json for the services that reference it.
func policyProvider(version string) *contract.Contract {
	c := svcContract("policy", version, contract.Owner{Team: "platform"})
	c.PactoVersion = "2.4"
	return c
}

// policyReferrer is a service whose policy http references the policy
// service, locked to its 1.0.0 revision unless ref says otherwise. Its
// document carries metadata when withMeta is set.
func policyReferrer(name string, withMeta bool, deps ...contract.Dependency) fleet.RawRevision {
	c := svcContract(name, "1.0.0", contract.Owner{Team: name + "-team"}, deps...)
	c.PactoVersion = "2.4"
	c.Policies = []contract.Policy{{Name: "http", Ref: policyRef}}
	doc := "pactoVersion: \"2.4\"\nservice:\n  name: " + name + "\n  version: 1.0.0\npolicies:\n  - name: http\n    ref: " + policyRef + "\n"
	if withMeta {
		doc += "metadata:\n  tier: gold\n"
	}
	return fleet.RawRevision{
		Bundle: &contract.Bundle{Contract: c, FS: fstest.MapFS{"pacto.yaml": {Data: []byte(doc)}}},
		Digest: "sha256:" + name,
		Lock: &lock.Lock{LockVersion: lock.CurrentLockVersion, References: []lock.Reference{{
			Kind: contract.ReferenceKindPolicy, Name: "http", Source: "oci", Ref: policyRef, Digest: "sha256:policy",
		}}},
	}
}

// policySnap holds the policy service 1.0.0 and the referrers; running names
// the services with a target.
func policySnap(t *testing.T, provider *contract.Contract, running []string, revs ...fleet.RawRevision) *fleet.FleetSnapshot {
	t.Helper()
	col := &fleet.Collection{Revisions: append([]fleet.RawRevision{{
		Bundle: &contract.Bundle{Contract: provider, FS: policySchema(openPolicy)},
		Digest: "sha256:policy", RequestedRef: policyRef + ":1.0.0",
	}}, revs...)}
	for _, s := range running {
		col.Targets = append(col.Targets, fleet.RawTarget{Scope: "prod", Kind: "deployment", Name: s, Service: s, Compliance: fleet.StatusCompliant, EvidenceAt: fresh()})
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("test", "memory", col))
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func analyzePolicy(snap *fleet.FleetSnapshot, newSchema string) *Result {
	return Analyze(context.Background(), policyProvider("1.0.0"), policyProvider("2.0.0"),
		policySchema(openPolicy), policySchema(newSchema), snap, Options{})
}

func onlyConsumer(t *testing.T, res *Result) AffectedConsumer {
	t.Helper()
	if len(res.Consumers) != 1 {
		t.Fatalf("consumers = %+v", res.Consumers)
	}
	return res.Consumers[0]
}

func TestPolicyReferrerIncompatibleBlocks(t *testing.T) {
	res := analyzePolicy(policySnap(t, policyProvider("1.0.0"), []string{"orders"}, policyReferrer("orders", false)), tierPolicy)
	c := onlyConsumer(t, res)
	if res.Classification != "BREAKING" || res.Gate != GateBlocked || !strings.Contains(res.GateReason, "1 service that references policy fails its new policy and runs on known targets: orders") {
		t.Errorf("classification %s, gate %s (%s)", res.Classification, res.Gate, res.GateReason)
	}
	want := AffectedConsumer{Service: "orders", Depth: 1, Direct: true, Path: []string{"orders", "policy"}, Owner: "orders-team", OwnerKey: "team:orders-team",
		CompatibilityVerdict: CompatibilityIncompatible, Provenance: fleet.ProvenanceDeclared, Confidence: ConfidenceDeclared,
		Status: c.Status, Targets: c.Targets, Via: []string{fleet.RelationshipPolicyRef}, Note: c.Note, Evidence: c.Evidence, policyVerdict: CompatibilityIncompatible}
	if !reflect.DeepEqual(c, want) || !strings.Contains(c.Note, "metadata") || !strings.Contains(c.Note, "against policy@2.0.0") || !res.blocks(c) {
		t.Errorf("consumer = %+v", c)
	}
	if !slices.Contains(res.Owners, "team:orders-team") || len(res.ActiveTargets) != 1 {
		t.Errorf("owners %v, targets %v", res.Owners, res.ActiveTargets)
	}
}

func TestPolicyReferrerCompatible(t *testing.T) {
	res := analyzePolicy(policySnap(t, policyProvider("1.0.0"), []string{"orders"}, policyReferrer("orders", true)), tierPolicy)
	c := onlyConsumer(t, res)
	if c.CompatibilityVerdict != CompatibilityCompatible || c.Note != "the contract satisfies the policy of policy@2.0.0" || res.blocks(c) {
		t.Errorf("consumer = %+v", c)
	}
	if res.Gate != GateUnverified || hasCode(res.Limitations, "POLICY_REFERRERS_UNCHECKED") {
		t.Errorf("gate %s (%s), limitations %+v", res.Gate, res.GateReason, res.Limitations)
	}
}

func TestPolicyReferrerUnreadableIsUnknown(t *testing.T) {
	noFS := policyReferrer("orders", false)
	noFS.Bundle.FS = nil
	noDoc := policyReferrer("orders", false)
	noDoc.Bundle.FS = fstest.MapFS{}
	for _, tt := range []struct {
		name   string
		rev    fleet.RawRevision
		schema fstest.MapFS
		note   string
	}{
		{"no bundle files", noFS, policySchema(tierPolicy), "the referrer's contract cannot be read, so it cannot be checked against policy@2.0.0"},
		{"no document", noDoc, policySchema(tierPolicy), "pacto.yaml"},
		{"no policy schema", policyReferrer("orders", false), fstest.MapFS{}, "does not contain policy/schema.json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			snap := policySnap(t, policyProvider("1.0.0"), []string{"orders"}, tt.rev)
			res := Analyze(context.Background(), policyProvider("1.0.0"), policyProvider("2.0.0"), policySchema(openPolicy), tt.schema, snap, Options{})
			c := onlyConsumer(t, res)
			if c.CompatibilityVerdict != CompatibilityUnknown || !strings.Contains(c.Note, tt.note) || res.blocks(c) {
				t.Errorf("consumer = %+v", c)
			}
			if !hasCode(res.Limitations, "POLICY_REFERRERS_UNCHECKED") || res.Classification == "BREAKING" && res.Gate == GateClear {
				t.Errorf("%s gate %s, limitations %+v", res.Classification, res.Gate, res.Limitations)
			}
		})
	}
	if v, note := revisionPolicyVerdict(nil, nil, &contract.Bundle{Contract: policyProvider("2.0.0")}); v != CompatibilityUnknown || !strings.Contains(note, "cannot be read") {
		t.Errorf("nil revision: %s %q", v, note)
	}
}

func TestPolicyBreakingWithReferrersNeverClear(t *testing.T) {
	for _, tt := range []struct {
		name     string
		withMeta bool
		reason   string
	}{
		{"compatible referrer", true, "1 consumer depends on policy"},
		{"incompatible referrer", false, "1 service that references policy fails its new policy: orders. Review each"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := analyzePolicy(policySnap(t, policyProvider("1.0.0"), nil, policyReferrer("orders", tt.withMeta)), tierPolicy)
			if res.Completeness != fleet.CompletenessComplete || res.Gate != GateUnverified || !strings.Contains(res.GateReason, tt.reason) {
				t.Errorf("completeness %s, gate %s (%s)", res.Completeness, res.Gate, res.GateReason)
			}
		})
	}
}

func TestPolicyReferrerAlsoDependentMerges(t *testing.T) {
	dep := contract.Dependency{Name: "policy", Ref: policyRef + ":1.0.0", Required: true, Compatibility: "^1.0.0"}
	res := analyzePolicy(policySnap(t, policyProvider("1.0.0"), nil, policyReferrer("orders", true, dep)), tierPolicy)
	c := onlyConsumer(t, res)
	if c.CompatibilityVerdict != CompatibilityIncompatible || !reflect.DeepEqual(c.Via, []string{fleet.RelationshipDependency, fleet.RelationshipPolicyRef}) ||
		!strings.Contains(c.Note, "satisfies the policy of policy@2.0.0") {
		t.Errorf("consumer = %+v", c)
	}

	// A transitive dependent that also references the policy is a direct consumer.
	front := policyReferrer("frontend", false, contract.Dependency{Name: "orders", Ref: "oci://x/orders:1.0.0", Compatibility: "^1.0.0"})
	res = analyzePolicy(policySnap(t, policyProvider("1.0.0"), nil, policyReferrer("orders", true, dep), front), tierPolicy)
	if len(res.Consumers) != 2 {
		t.Fatalf("consumers = %+v", res.Consumers)
	}
	if f := res.Consumers[0]; f.Service != "frontend" || !f.Direct || f.Depth != 1 || f.CompatibilityVerdict != CompatibilityIncompatible ||
		f.Provenance != fleet.ProvenanceDeclared || f.Confidence != ConfidenceDeclared || !reflect.DeepEqual(f.Path, []string{"frontend", "policy"}) ||
		!reflect.DeepEqual(f.Via, []string{fleet.RelationshipPolicyRef}) {
		t.Errorf("frontend = %+v", f)
	}
}

func TestPolicyReferrerHiddenNotNamed(t *testing.T) {
	changed := fleet.NewServiceKey("policy")
	edge := func(from string, mut func(*fleet.Relationship)) fleet.Relationship {
		r := fleet.Relationship{FromService: fleet.NewServiceKey(from), ToService: changed, Type: fleet.RelationshipPolicyRef,
			Provenance: fleet.ProvenanceDeclared, Resolved: true}
		if mut != nil {
			mut(&r)
		}
		return r
	}
	snap := &fleet.FleetSnapshot{Relationships: []fleet.Relationship{
		edge("seen", nil),
		edge("hidden", func(r *fleet.Relationship) { r.Hidden = true }),
		edge("unresolved", func(r *fleet.Relationship) { r.Resolved = false }),
		edge("observed", func(r *fleet.Relationship) { r.Provenance = fleet.ProvenanceObserved }),
		edge("config", func(r *fleet.Relationship) { r.Type = fleet.RelationshipConfigRef }),
		edge("other", func(r *fleet.Relationship) { r.ToService = fleet.NewServiceKey("else") }),
		edge("policy", nil),
	}}
	got := policyReferrers(snap, changed)
	if len(got) != 1 || len(got[fleet.NewServiceKey("seen")]) != 1 {
		t.Errorf("referrers = %+v", got)
	}
}

func TestPolicyReferrerHiddenCounted(t *testing.T) {
	ref := policyReferrer("secret-ref", false)
	both := policyReferrer("secret-both", false, contract.Dependency{Name: "policy", Ref: policyRef + ":1.0.0", Compatibility: "^1.0.0"})
	col := &fleet.Collection{
		Revisions: []fleet.RawRevision{{Bundle: &contract.Bundle{Contract: policyProvider("1.0.0"), FS: policySchema(openPolicy)},
			Digest: "sha256:policy", RequestedRef: policyRef + ":1.0.0"}, ref, both},
		Targets: []fleet.RawTarget{
			{Scope: "shop", Kind: fleet.SourceKindKubernetes, Name: "policy", Service: "policy", Compliance: fleet.StatusCompliant, EvidenceAt: fresh()},
			{Scope: "secret", Kind: fleet.SourceKindKubernetes, Name: "r", Service: "secret-ref", Compliance: fleet.StatusCompliant, EvidenceAt: fresh()},
			{Scope: "secret", Kind: fleet.SourceKindKubernetes, Name: "b", Service: "secret-both", Compliance: fleet.StatusCompliant, EvidenceAt: fresh()},
		},
	}
	full, err := fleet.Build(context.Background(), fleet.BuildOptions{}, fleet.NewMemorySource("k8s", fleet.SourceKindKubernetes, col))
	if err != nil {
		t.Fatal(err)
	}
	snap := fleet.NewQuery(full).Restrict(func(ns string) bool { return ns == "shop" }).SnapshotForSerialization()
	res := analyzePolicy(snap, tierPolicy)
	if len(res.Consumers) != 0 {
		t.Errorf("a hidden referrer is named: %+v", res.Consumers)
	}
	rev := snap.Revisions[snap.Services[fleet.NewServiceKey("policy")].Revisions[0]]
	if p := BuildProduct(fleet.ProductMeta{}, snap, rev.ServiceKey, rev, rev, res, 0, 0); p.Consumers.Hidden != 2 {
		t.Errorf("hidden consumers = %d, want 2", p.Consumers.Hidden)
	}
}

func TestPolicyRefUnresolvedPartial(t *testing.T) {
	tagged := policyReferrer("orders", false)
	tagged.Lock = nil
	tagged.Bundle.Contract.Policies[0].Ref = policyRef + ":1"
	res := analyzePolicy(policySnap(t, policyProvider("1.0.0"), nil, tagged), tierPolicy)
	if len(res.Consumers) != 0 || res.Completeness != fleet.CompletenessPartial || res.Gate != GateUnverified {
		t.Errorf("consumers %+v, completeness %s, gate %s", res.Consumers, res.Completeness, res.Gate)
	}
	for _, l := range res.Limitations {
		if l.Code == "POLICY_REF_CONSUMERS_UNRESOLVED" && strings.HasPrefix(l.Message, "1 policy references name ") {
			return
		}
	}
	t.Errorf("limitations = %+v", res.Limitations)
}

func TestPolicyReferrerIncompatibleOnNonBreakingNotClear(t *testing.T) {
	// The provider declares its own policies and adds one: the diff calls that
	// NON_BREAKING, yet the referrer fails the added policy.
	oldP := policyProvider("1.0.0")
	oldP.Policies = []contract.Policy{{Name: "base", Schema: "policy/schema.json"}}
	newP := policyProvider("1.1.0")
	newP.Policies = []contract.Policy{{Name: "base", Schema: "policy/schema.json"}, {Name: "tier", Schema: "policy/tier.json"}}
	newFS := fstest.MapFS{"policy/schema.json": {Data: []byte(openPolicy)}, "policy/tier.json": {Data: []byte(tierPolicy)}}
	for _, tt := range []struct {
		name    string
		running []string
		gate    string
	}{
		{"running", []string{"orders"}, GateBlocked},
		{"running nowhere", nil, GateUnverified},
	} {
		t.Run(tt.name, func(t *testing.T) {
			snap := policySnap(t, oldP, tt.running, policyReferrer("orders", false))
			res := Analyze(context.Background(), oldP, newP, policySchema(openPolicy), newFS, snap, Options{})
			c := onlyConsumer(t, res)
			if res.Classification != "NON_BREAKING" || res.Gate != tt.gate || c.CompatibilityVerdict != CompatibilityIncompatible ||
				res.blocks(c) != (tt.gate == GateBlocked) || res.ReleaseBlocking() != (tt.gate == GateBlocked) {
				t.Errorf("classification %s, gate %s (%s), consumer %+v", res.Classification, res.Gate, res.GateReason, c)
			}
		})
	}
}

// A BREAKING change that a dependent's range refuses and a referrer's
// document fails names both in one blocked reason.
func TestPolicyReferrerBlocksBesideABrokenDependent(t *testing.T) {
	dep := contract.Dependency{Name: "policy", Ref: policyRef + ":1.0.0", Required: true, Compatibility: "^1.0.0"}
	broken := fleet.RawRevision{Bundle: mkBundle(svcContract("billing", "1.0.0", contract.Owner{Team: "billing"}, dep))}
	res := analyzePolicy(policySnap(t, policyProvider("1.0.0"), []string{"orders", "billing"}, policyReferrer("orders", false), broken), tierPolicy)
	if res.Gate != GateBlocked || !strings.Contains(res.GateReason, "do not accept policy 2.0.0: billing") ||
		!strings.Contains(res.GateReason, "fails its new policy and runs on known targets: orders") {
		t.Errorf("gate %s (%s)", res.Gate, res.GateReason)
	}
}

// Two referrers that fail and cannot be checked are counted in the plural.
func TestPolicyReferrersCountedInThePlural(t *testing.T) {
	noFS := func(name string) fleet.RawRevision {
		r := policyReferrer(name, false)
		r.Bundle.FS = nil
		return r
	}
	res := analyzePolicy(policySnap(t, policyProvider("1.0.0"), []string{"orders", "shipping"},
		policyReferrer("orders", false), policyReferrer("shipping", false), noFS("billing"), noFS("ledger")), tierPolicy)
	if !strings.Contains(res.GateReason, "2 services that reference policy fail its new policy and run on known targets: orders, shipping") {
		t.Errorf("gate reason %q", res.GateReason)
	}
	for _, l := range res.Limitations {
		if l.Code == "POLICY_REFERRERS_UNCHECKED" && strings.HasPrefix(l.Message, "2 services that reference policy") && strings.HasSuffix(l.Message, "breaks them is unknown") {
			return
		}
	}
	t.Errorf("limitations = %+v", res.Limitations)
}

// Failing referrers that run, one on a stale target: only the fresh one is
// said to run there.
func TestPolicyReferrersFailingOnStaleTarget(t *testing.T) {
	col := &fleet.Collection{
		Revisions: []fleet.RawRevision{{Bundle: &contract.Bundle{Contract: policyProvider("1.0.0"), FS: policySchema(openPolicy)},
			Digest: "sha256:policy", RequestedRef: policyRef + ":1.0.0"}, policyReferrer("orders", false), policyReferrer("billing", false)},
		Targets: []fleet.RawTarget{
			{Scope: "prod", Kind: "deployment", Name: "orders", Service: "orders", Compliance: fleet.StatusCompliant, EvidenceAt: ago(time.Hour)},
			{Scope: "prod", Kind: "deployment", Name: "billing", Service: "billing", Compliance: fleet.StatusCompliant, EvidenceAt: ago(30 * time.Hour)},
		},
	}
	snap, err := fleet.Build(context.Background(), fleet.BuildOptions{Now: func() time.Time { return evidenceNow }}, fleet.NewMemorySource("test", "memory", col))
	if err != nil {
		t.Fatal(err)
	}
	res := analyzePolicy(snap, tierPolicy)
	want := "2 services that reference policy fail its new policy: orders runs on a known target; billing was last seen on a target more than 24h before asOf"
	if res.Gate != GateBlocked || !strings.Contains(res.GateReason, want) {
		t.Errorf("gate %s: %q", res.Gate, res.GateReason)
	}
}
