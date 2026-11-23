/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package delivery

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	dv1 "github.com/trianalab/pacto/integrations/kubernetes/v6/api/delivery/v1alpha1"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/delivery"
	"github.com/trianalab/pacto/v4/pkg/deliveryrecord"
	"github.com/trianalab/pacto/v4/pkg/ocireferrer"
	sigtest "github.com/trianalab/pacto/v4/pkg/signing/testutil"
)

const deployerRole = "d-pacto-operator-delivery-deployer"

var idpID = delivery.PublisherIdentity{Issuer: ghIssuer, Subject: "https://github.com/acme/payments/.github/workflows/ci.yml@refs/heads/main",
	Repository: "https://github.com/acme/payments"}

// idpContract is payments deploying the manifests (or chart) artifact it
// publishes.
func idpContract(kind string, envs ...contract.Environment) *contract.Contract {
	c := delivered("payments", envs...)
	c.Publish.Deploy = kind
	c.Publish.Artifacts = []contract.PublishArtifact{{Name: "image", Type: contract.ArtifactImage, Ref: "oci://ghcr.io/acme/payments"},
		{Name: kind, Type: kind, Ref: "oci://ghcr.io/acme/deploy/payments"}}
	return c
}

// idpEnv is a promoter under profile idp: payments, signed keyless by its
// CI, deploys kind and nothing applies it yet.
func idpEnv(t *testing.T, kind string, funcs interceptor.Funcs, objs ...client.Object) *placeEnv {
	t.Helper()
	pe := newPlaceEnv(t, funcs, objs...)
	pe.r.CreateAppliers, pe.r.DeployerRole = true, deployerRole
	pe.publish(t, "payments", idpContract(kind), nil)
	id := idpID
	pe.accepted["payments"].Identity = &id
	return pe
}

func (pe *placeEnv) flux(t *testing.T, kind, ns, name string) *unstructured.Unstructured {
	t.Helper()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(fluxGVK(kind))
	if err := pe.c.Get(t.Context(), client.ObjectKey{Namespace: ns, Name: name}, u); err != nil {
		t.Fatalf("%s %s/%s: %v", kind, ns, name, err)
	}
	return u
}

func suspended(u *unstructured.Unstructured) bool {
	s, _, _ := unstructured.NestedBool(u.Object, "spec", "suspend")
	return s
}

func (pe *placeEnv) has(t *testing.T, o client.Object, ns, name string) bool {
	t.Helper()
	return pe.c.Get(t.Context(), client.ObjectKey{Namespace: ns, Name: name}, o) == nil
}

// approved is a promote of the managed Stage ns/name a person approved
// (by), or no one ("").
func approved(ns, name, by string) *dv1.Operation {
	op := &dv1.Operation{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name + "-auto-1"},
		Spec: delivery.OperationSpec{Type: delivery.TypePromote, Stage: name, Actor: delivery.Actor{User: promoterSA}}}
	if by != "" {
		op.Status.Approvals = []delivery.ApprovalRecord{{Name: "a1", By: by, Decision: delivery.Approve}}
	}
	return op
}

func TestPointerNeverAppliesBeforeFirstPromote(t *testing.T) {
	pacto := ladderPacto("payments-staging", "payments")
	_ = unstructured.SetNestedField(pacto.Object, "oci://ghcr.io/acme/payments@sha256:aa", "status", "contract", "resolvedRef")
	for name, setup := range map[string]func(pe *placeEnv){
		// A signed rule declares it: no person is needed.
		"declared": func(pe *placeEnv) {
			pe.r.Placement = func(context.Context) (*delivery.PlacementPolicy, string) {
				return &delivery.PlacementPolicy{Rules: []delivery.PlacementRule{{Name: "all", Environments: []string{"*"}, Namespace: "{environment}"}}}, ""
			}
		},
		// A Pacto of payments runs there, but nothing applies it.
		"observed evidence": func(pe *placeEnv) {
			_ = unstructured.SetNestedField(pacto.Object, "oci://"+pe.root+"/payments@sha256:aa", "status", "contract", "resolvedRef")
			if err := pe.c.Create(t.Context(), namespace("payments-staging")); err != nil {
				t.Fatal(err)
			}
			if err := pe.c.Create(t.Context(), pacto.DeepCopy()); err != nil {
				t.Fatal(err)
			}
		},
		"inferred": func(*placeEnv) {},
	} {
		t.Run(name, func(t *testing.T) {
			pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{}, namespace("staging"), namespace("production"))
			setup(pe)
			pe.pass(t)
			row := rowOf(t, pe.rows(t), "payments", "staging")
			ns := row.Placement.Namespace
			ptr := pe.flux(t, "OCIRepository", ns, "payments-staging")
			ks := pe.flux(t, "Kustomization", ns, "payments-staging")
			if _, hasRef := ptr.Object["spec"].(map[string]any)["ref"]; hasRef || !suspended(ptr) || !suspended(ks) {
				t.Fatalf("%s: pointer %v kustomization %v", name, ptr.Object, ks.Object)
			}
			s := pe.stage(t, ns, "payments-staging")
			if s.Spec.ApplyRef != (delivery.ApplyRef{Kind: delivery.ApplierFlux, Name: "payments-staging"}) ||
				s.Spec.StartLevel != delivery.LevelAutonomous || !s.Spec.Approval.Always {
				t.Fatalf("stage %+v", s.Spec)
			}
			// The promoter's approved promote wrote the digest: only then is
			// it unsuspended.
			if err := pe.r.confirmPlacement(t.Context(), approved(ns, "payments-staging", "alice"), s); err != nil {
				t.Fatal(err)
			}
			if suspended(pe.flux(t, "OCIRepository", ns, "payments-staging")) || suspended(pe.flux(t, "Kustomization", ns, "payments-staging")) {
				t.Fatal("still suspended")
			}
		})
	}
}

func TestUnconfirmedPlacementAppliersStaySuspended(t *testing.T) {
	pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{}, namespace("staging"), namespace("production"))
	pe.pass(t)
	pe.pass(t) // another pass changes nothing
	s := pe.stage(t, "staging", "payments-staging")
	err := pe.r.confirmPlacement(t.Context(), approved("staging", "payments-staging", ""), s)
	if err == nil || !strings.HasPrefix(err.Error(), "deploy payments to namespace staging (each environment is a namespace of the same name)?") {
		t.Fatalf("err %v", err)
	}
	if !suspended(pe.flux(t, "OCIRepository", "staging", "payments-staging")) || len(pe.state(t).Confirmations) != 0 {
		t.Fatal("unsuspended without a person")
	}
}

func TestDeployerOnlyAfterConfirmation(t *testing.T) {
	pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{}, namespace("staging"), namespace("production"))
	pe.pass(t)
	if pe.has(t, &corev1.ServiceAccount{}, "staging", "payments-deployer") || pe.has(t, &rbacv1.RoleBinding{}, "staging", "payments-deployer") {
		t.Fatal("a default placement got its deployer before a person confirmed it")
	}
	pe.r.Placement = func(context.Context) (*delivery.PlacementPolicy, string) {
		return &delivery.PlacementPolicy{Rules: []delivery.PlacementRule{{Name: "all", Environments: []string{"*"}, Namespace: "{environment}"}}}, ""
	}
	pe.pass(t)
	rb := &rbacv1.RoleBinding{}
	if !pe.has(t, &corev1.ServiceAccount{}, "staging", "payments-deployer") || !pe.has(t, rb, "staging", "payments-deployer") ||
		rb.RoleRef.Name != deployerRole || rb.Subjects[0].Name != "payments-deployer" {
		t.Fatalf("a declared placement gets its deployer at once: %+v", rb)
	}
}

func TestFirstApprovalConfirmsPlacementAndUnsuspends(t *testing.T) {
	pe := idpEnv(t, contract.ArtifactChart, interceptor.Funcs{}, namespace("staging"), namespace("production"))
	pe.pass(t)
	s := pe.stage(t, "staging", "payments-staging")
	if s.Spec.ApplyRef != (delivery.ApplyRef{Kind: delivery.ApplierHelmRelease, Name: "payments-staging"}) {
		t.Fatalf("applyRef %+v", s.Spec.ApplyRef)
	}
	if err := pe.r.confirmPlacement(t.Context(), approved("staging", "payments-staging", "alice"), s); err != nil {
		t.Fatal(err)
	}
	st := pe.state(t)
	if len(st.Confirmations) != 1 || st.Confirmations[0] != (placementConfirmation{Stage: s.UID, Operation: "payments-staging-auto-1", By: "alice", At: "2026-11-23T10:00:00Z"}) {
		t.Fatalf("confirmations %+v", st.Confirmations)
	}
	if got := pe.stage(t, "staging", "payments-staging").Annotations[delivery.PlacementConfirmedByAnnotation]; got != "alice" {
		t.Fatalf("annotation %q", got)
	}
	if !pe.has(t, &corev1.ServiceAccount{}, "staging", "payments-deployer") || suspended(pe.flux(t, "OCIRepository", "staging", "payments-staging")) ||
		suspended(pe.flux(t, "HelmRelease", "staging", "payments-staging")) {
		t.Fatal("not confirmed")
	}
	// A confirmed Stage is never asked again, and the next pass keeps its
	// deployer and asks no question.
	pe.writes = nil
	if err := pe.r.confirmPlacement(t.Context(), approved("staging", "payments-staging", "bob"), s); err != nil || len(pe.writes) != 0 {
		t.Fatalf("again: %v %v", err, pe.writes)
	}
	pe.pass(t)
	if q := pe.r.question(t.Context(), pe.stage(t, "staging", "payments-staging")); q != "" {
		t.Fatalf("question %q", q)
	}
}

func TestForgedConfirmedAnnotationUnsuspendsNothing(t *testing.T) {
	pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{}, namespace("staging"), namespace("production"))
	pe.pass(t)
	s := pe.stage(t, "staging", "payments-staging")
	s.Annotations[delivery.PlacementConfirmedByAnnotation] = "mallory"
	if err := pe.c.Update(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	pe.pass(t)
	if pe.r.confirmPlacement(t.Context(), approved("staging", "payments-staging", ""), s) == nil ||
		!suspended(pe.flux(t, "OCIRepository", "staging", "payments-staging")) ||
		pe.has(t, &corev1.ServiceAccount{}, "staging", "payments-deployer") {
		t.Fatal("a forged annotation confirmed the placement")
	}
	if q := pe.r.question(t.Context(), s); !strings.HasPrefix(q, "deploy payments to namespace staging") {
		t.Fatalf("question %q", q)
	}
}

func TestAgentApprovalNeverConfirmsPlacement(t *testing.T) {
	pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{}, namespace("staging"), namespace("production"))
	pe.pass(t)
	s := pe.stage(t, "staging", "payments-staging")
	// An Approval the promoter did not count never reaches the operation's
	// status; a rejection confirms nothing either.
	rejected := approved("staging", "payments-staging", "alice")
	rejected.Status.Approvals[0].Decision = delivery.Reject
	for _, op := range []*dv1.Operation{approved("staging", "payments-staging", ""), rejected} {
		if pe.r.confirmPlacement(t.Context(), op, s) == nil {
			t.Fatal("confirmed without a counted approval")
		}
	}
	if len(pe.state(t).Confirmations) != 0 {
		t.Fatal("confirmed")
	}
}

func TestNoAppliersOutsideIdp(t *testing.T) {
	pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{}, namespace("staging"), namespace("production"))
	pe.r.CreateAppliers = false
	pe.pass(t)
	if r := rowOf(t, pe.rows(t), "payments", "staging"); r.Stage != "" || !strings.HasSuffix(r.Reason, delivery.NoApplierMessage) {
		t.Fatalf("row %+v", r)
	}
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvkOCIRepository)
	if pe.has(t, u, "staging", "payments-staging") {
		t.Fatal("a Flux object outside idp")
	}
}

func TestNoFluxObjectsWithoutDeployArtifact(t *testing.T) {
	pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{}, namespace("staging"), namespace("production"))
	pe.accepted["payments"].Identity = &delivery.PublisherIdentity{Repository: "https://github.com/acme/payments"} // signed with a key
	pe.pass(t)
	if r := rowOf(t, pe.rows(t), "payments", "staging"); r.Stage != "" || !strings.Contains(r.Reason, "signed by a keyless publisher identity") {
		t.Fatalf("row %+v", r)
	}
	c := idpContract(contract.ArtifactManifests)
	c.Publish.Deploy = "image"
	if deployInput(c, &idpID) != nil {
		t.Fatal("an image is no deploy artifact")
	}
}

func TestEnsureNamespaceOptIn(t *testing.T) {
	pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{})
	// Off: a missing namespace is refused (P4's reason).
	pe.pass(t)
	if p := rowOf(t, pe.rows(t), "payments", "staging").Placement; !strings.HasPrefix(p.Refused, "namespace staging does not exist") {
		t.Fatalf("placement %+v", p)
	}
	// On, with a declared placement: created at once, kept and recorded.
	pe.r.CreateNamespaces = true
	pe.r.Placement = func(context.Context) (*delivery.PlacementPolicy, string) {
		return &delivery.PlacementPolicy{Rules: []delivery.PlacementRule{{Name: "all", Environments: []string{"*"}, Namespace: "{service}-{environment}"}}}, ""
	}
	pe.pass(t)
	n := &corev1.Namespace{}
	if !pe.has(t, n, "", "payments-staging") || n.Labels[delivery.ManagedLabel] != "pacto" || n.Annotations[delivery.KeepAnnotation] != "true" {
		t.Fatalf("namespace %+v", n.ObjectMeta)
	}
	if !slices.ContainsFunc(pe.state(t).Objects, func(o managedObject) bool { return o.Kind == kindNamespace && o.Name == "payments-staging" }) {
		t.Fatalf("state %+v", pe.state(t))
	}
	if s := pe.stage(t, "payments-staging", "payments-staging"); s.Spec.ApplyRef.Name != "payments-staging" {
		t.Fatalf("stage %+v", s.Spec)
	}
}

func TestNoNamespaceOutsideIdp(t *testing.T) {
	pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{})
	pe.r.CreateAppliers, pe.r.CreateNamespaces = false, true
	pe.r.Placement = func(context.Context) (*delivery.PlacementPolicy, string) {
		return &delivery.PlacementPolicy{Rules: []delivery.PlacementRule{{Name: "all", Environments: []string{"*"}, Namespace: "{environment}"}}}, ""
	}
	pe.pass(t)
	if r := rowOf(t, pe.rows(t), "payments", "staging"); pe.has(t, &corev1.Namespace{}, "", "staging") || !strings.HasSuffix(r.Reason, delivery.NoApplierMessage) {
		t.Fatalf("row %+v", r)
	}
}

func TestNamespaceCreationNeedsConfirmedPlacement(t *testing.T) {
	pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{})
	pe.r.CreateNamespaces = true
	v := pe.pass(t)
	r := rowOf(t, pe.rows(t), "payments", "staging")
	if r.Stage != "" || r.Reason != "waiting: confirm namespace staging with pacto delivery ladder confirm --create-namespaces or create it" ||
		pe.has(t, &corev1.Namespace{}, "", "staging") || v.NamespaceCandidates != 2 {
		t.Fatalf("row %+v view %+v", r, v)
	}
	// A person's ladder record binds the candidates by digest: both are
	// created.
	le := pe.withLadder(t)
	le.push(t, sigtest.Token(bob, personIssuer), ladderRecord(func(l *delivery.LadderRecord) {
		l.Rungs, l.ProposalDigest = l.Rungs[:2], ""
		l.NamespacesDigest = delivery.NamespacesDigest([]string{"staging", "production"})
	}))
	v = pe.pass(t)
	if !pe.has(t, &corev1.Namespace{}, "", "staging") || !pe.has(t, &corev1.Namespace{}, "", "production") || v.NamespaceCandidates != 0 {
		t.Fatalf("not created: %+v", v)
	}
	if st := pe.state(t); !slices.Equal(st.AllowedNamespaces, []string{"production", "staging"}) {
		t.Fatalf("state %+v", st)
	}
}

// withLadder makes pe read ladder records from an approvals repository of
// its own registry, under approver signers of a private Sigstore.
func (pe *placeEnv) withLadder(t *testing.T) *ladderEnv {
	t.Helper()
	le := &ladderEnv{envFixture: pe.envFixture, reg: pe.reg, ss: sigtest.New(t), repo: pe.reg.host + "/acme/pacto-streams-approvals"}
	le.r.LadderRepository, le.r.Policy = le.repo, withSigners(delivery.ClassPolicy{}, le.ss)
	r, _ := ocireferrer.Open(le.repo, ocireferrer.Options{})
	if _, err := deliveryrecord.EnsureAnchor(t.Context(), r, delivery.LadderStream("cid")); err != nil {
		t.Fatal(err)
	}
	return le
}

func TestViewNamesNoNamespaceToCreate(t *testing.T) {
	pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{})
	pe.r.CreateNamespaces = true
	pe.pass(t)
	cm := &corev1.ConfigMap{}
	if err := pe.c.Get(t.Context(), client.ObjectKey{Namespace: deliveryNS, Name: delivery.EnvironmentsConfigMap}, cm); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cm.Data["view.json"], `"staging"`) && strings.Contains(cm.Data["view.json"], "payments") ||
		!strings.Contains(cm.Data["view.json"], `"namespaceCandidates":2`) {
		t.Fatalf("view %s", cm.Data["view.json"])
	}
	private := &corev1.ConfigMap{}
	if err := pe.c.Get(t.Context(), client.ObjectKey{Namespace: deliveryNS, Name: delivery.PlacementsConfigMap}, private); err != nil {
		t.Fatal(err)
	}
	if private.Data[delivery.NamespaceCandidatesKey] != `["production","staging"]` {
		t.Fatalf("candidates %q", private.Data[delivery.NamespaceCandidatesKey])
	}
}

func TestIdpObjectsNotCreatedByPactoNeverTouched(t *testing.T) {
	foreign := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: "staging", Name: "payments-deployer"}}
	ptr := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"namespace": "production", "name": "payments-production"}}}
	ptr.SetGroupVersionKind(gvkOCIRepository)
	pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{}, namespace("staging"), namespace("production"), foreign, ptr)
	pe.r.Placement = func(context.Context) (*delivery.PlacementPolicy, string) {
		return &delivery.PlacementPolicy{Rules: []delivery.PlacementRule{{Name: "all", Environments: []string{"*"}, Namespace: "{environment}"}}}, ""
	}
	pe.pass(t)
	rows := pe.rows(t)
	if r := rowOf(t, rows, "payments", "staging"); r.Reason != "a ServiceAccount payments-deployer in staging not created by Pacto exists" {
		t.Fatalf("staging %+v", r)
	}
	if r := rowOf(t, rows, "payments", "production"); r.Reason != "a OCIRepository payments-production in production not created by Pacto exists" {
		t.Fatalf("production %+v", r)
	}
}

func TestIdpWriteFailures(t *testing.T) {
	boom := errors.New("boom")
	fail := map[string]bool{}
	pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, o client.Object, opts ...client.GetOption) error {
			if fail["get/"+key.Name] {
				return boom
			}
			return c.Get(ctx, key, o, opts...)
		},
	}, namespace("staging"), namespace("production"))
	pe.r.Placement = func(context.Context) (*delivery.PlacementPolicy, string) {
		return &delivery.PlacementPolicy{Rules: []delivery.PlacementRule{{Name: "all", Environments: []string{"*"}, Namespace: "{environment}"}}}, ""
	}
	fail["get/payments-deployer"] = true
	pe.pass(t)
	if r := rowOf(t, pe.rows(t), "payments", "staging"); r.Reason != "the ServiceAccount payments-deployer in staging could not be read: boom" {
		t.Fatalf("staging %+v", r)
	}
	fail["get/payments-deployer"] = false
	saved := maxManagedObjects
	t.Cleanup(func() { maxManagedObjects = saved })
	maxManagedObjects = 0
	pe.pass(t)
	if r := rowOf(t, pe.rows(t), "payments", "staging"); !strings.HasPrefix(r.Reason, "Pacto keeps at most 0 objects") {
		t.Fatalf("staging %+v", r)
	}
	maxManagedObjects = saved
	pe.r.Client = interceptor.NewClient(pe.c, interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
			if o.GetObjectKind().GroupVersionKind().Kind == "Kustomization" {
				return boom
			}
			return c.Create(ctx, o, opts...)
		}})
	pe.pass(t)
	if r := rowOf(t, pe.rows(t), "payments", "staging"); r.Reason != "the Kustomization payments-staging in staging could not be created: boom" {
		t.Fatalf("staging %+v", r)
	}
	// A namespace whose create fails says why.
	pe.r.CreateNamespaces = true
	pe.r.Placement = func(context.Context) (*delivery.PlacementPolicy, string) {
		return &delivery.PlacementPolicy{Rules: []delivery.PlacementRule{{Name: "all", Environments: []string{"*"}, Namespace: "new-{environment}"}}}, ""
	}
	pe.r.Client = interceptor.NewClient(pe.c, interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
			if _, ok := o.(*corev1.Namespace); ok {
				return boom
			}
			return c.Create(ctx, o, opts...)
		}})
	pe.pass(t)
	if r := rowOf(t, pe.rows(t), "payments", "staging"); r.Reason != "the Namespace new-staging could not be created: boom" {
		t.Fatalf("staging %+v", r)
	}
}

func TestConfirmPlacementRefusals(t *testing.T) {
	boom := errors.New("boom")
	fail := map[string]bool{}
	pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, o client.Object, opts ...client.GetOption) error {
			if fail[key.Name] {
				return boom
			}
			return c.Get(ctx, key, o, opts...)
		},
	}, namespace("staging"), namespace("production"))
	pe.pass(t)
	s := pe.stage(t, "staging", "payments-staging")
	ok := approved("staging", "payments-staging", "alice")
	// Not managed, not ours, or not running: nothing to do.
	var nilR *EnvironmentReconciler
	if nilR.confirmPlacement(t.Context(), ok, s) != nil || nilR.question(t.Context(), s) != "" {
		t.Fatal("nil")
	}
	hand := s.DeepCopy()
	hand.Labels = nil
	other := s.DeepCopy()
	other.UID = "other"
	for _, st := range []*dv1.Stage{hand, other} {
		if err := pe.r.confirmPlacement(t.Context(), ok, st); err != nil || pe.r.question(t.Context(), st) != "" {
			t.Fatal(err)
		}
	}
	// The private state cannot be read.
	fail[delivery.PlacementsConfigMap] = true
	if pe.r.confirmPlacement(t.Context(), ok, s) == nil || pe.r.question(t.Context(), s) != "" {
		t.Fatal("unreadable state")
	}
	fail[delivery.PlacementsConfigMap] = false
	// The pointer cannot be read, or is not the one Pacto created.
	fail["payments-staging"] = true
	if err := pe.r.confirmPlacement(t.Context(), ok, s); err == nil || !strings.Contains(err.Error(), "reading OCIRepository payments-staging") {
		t.Fatalf("err %v", err)
	}
	fail["payments-staging"] = false
	ptr := pe.flux(t, "OCIRepository", "staging", "payments-staging")
	if err := pe.c.Delete(t.Context(), ptr); err != nil {
		t.Fatal(err)
	}
	ptr.SetResourceVersion("")
	ptr.SetUID("squatter")
	if err := pe.c.Create(t.Context(), ptr); err != nil {
		t.Fatal(err)
	}
	if err := pe.r.confirmPlacement(t.Context(), ok, s); err == nil || !strings.Contains(err.Error(), "is not the one Pacto created") {
		t.Fatalf("err %v", err)
	}
	// The deployer cannot be created.
	fail["payments-deployer"] = true
	if err := pe.r.confirmPlacement(t.Context(), ok, s); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("err %v", err)
	}
	fail["payments-deployer"] = false
	// No row names the Stage yet.
	cm := &corev1.ConfigMap{}
	if err := pe.c.Get(t.Context(), client.ObjectKey{Namespace: deliveryNS, Name: delivery.PlacementsConfigMap}, cm); err != nil {
		t.Fatal(err)
	}
	cm.Data["rows.json"] = "[]"
	if err := pe.c.Update(t.Context(), cm); err != nil {
		t.Fatal(err)
	}
	if err := pe.r.confirmPlacement(t.Context(), ok, s); err == nil || !strings.Contains(err.Error(), "is not known") || pe.r.question(t.Context(), s) != "" {
		t.Fatalf("err %v", err)
	}
	cm.Data["rows.json"] = "{"
	if err := pe.c.Update(t.Context(), cm); err != nil {
		t.Fatal(err)
	}
	if err := pe.r.confirmPlacement(t.Context(), ok, s); err == nil || !strings.Contains(err.Error(), "rows.json") {
		t.Fatalf("err %v", err)
	}
}

func TestConfirmPlacementDeclaredAndWriteFailures(t *testing.T) {
	boom := errors.New("boom")
	pe := idpEnv(t, contract.ArtifactManifests, interceptor.Funcs{}, namespace("staging"), namespace("production"))
	pe.r.Placement = func(context.Context) (*delivery.PlacementPolicy, string) {
		return &delivery.PlacementPolicy{Rules: []delivery.PlacementRule{{Name: "all", Environments: []string{"*"}, Namespace: "{environment}"}}}, ""
	}
	pe.pass(t)
	s := pe.stage(t, "staging", "payments-staging")
	if q := pe.r.question(t.Context(), s); q != "" {
		t.Fatalf("a declared placement asks nothing: %q", q)
	}
	failPatch := func(kind string) {
		pe.r.Client = interceptor.NewClient(pe.c, interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, o client.Object, p client.Patch, opts ...client.PatchOption) error {
				k := o.GetObjectKind().GroupVersionKind().Kind
				if _, ok := o.(*corev1.ConfigMap); ok {
					k = "ConfigMap"
				}
				if k == kind {
					return boom
				}
				return c.Patch(ctx, o, p, opts...)
			}})
	}
	failPatch("OCIRepository")
	if pe.r.confirmPlacement(t.Context(), approved("staging", "payments-staging", ""), s) == nil {
		t.Fatal("unsuspend failure")
	}
	failPatch("ConfigMap")
	if pe.r.confirmPlacement(t.Context(), approved("staging", "payments-staging", ""), s) == nil || len(pe.state(t).Confirmations) != 0 {
		t.Fatal("state write failure")
	}
	// A declared placement needs no person: the operation's actor confirms.
	failPatch("none")
	if err := pe.r.confirmPlacement(t.Context(), approved("staging", "payments-staging", ""), s); err != nil {
		t.Fatal(err)
	}
	if c := pe.state(t).Confirmations; len(c) != 1 || c[0].By != promoterSA {
		t.Fatalf("confirmations %+v", c)
	}
	// The state ConfigMap gone: nothing can be recorded.
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: deliveryNS, Name: delivery.PlacementsConfigMap}}
	if err := pe.c.Delete(t.Context(), cm); err != nil {
		t.Fatal(err)
	}
	if pe.r.writeState(t.Context(), managedState{}) == nil {
		t.Fatal("no ConfigMap")
	}
}

// managedStageWorld is the harness Stage payments-prod made a managed Stage
// whose pointer Pacto created suspended, with its placement p in the
// private state.
func managedStageWorld(t *testing.T, always bool, p delivery.Placement) *env {
	t.Helper()
	s := stageObj(dA)
	s.UID, s.Labels = "uid-stage", map[string]string{delivery.ManagedLabel: delivery.ManagedBy}
	s.Spec.Approval.Always, s.Spec.ApplyRef.Name = always, "payments-prod"
	ptr := ociRepo("payments-prod", dA, "latest@"+dA, false)
	ptr.SetUID("uid-ptr")
	_ = unstructured.SetNestedField(ptr.Object, true, "spec", "suspend")
	state := `{"objects":[{"kind":"Stage","namespace":"` + ns + `","name":"payments-prod","uid":"uid-stage","service":"payments","environment":"prod"},` +
		`{"kind":"OCIRepository","namespace":"` + ns + `","name":"payments-prod","uid":"uid-ptr","service":"payments","environment":"prod"}]}`
	row := `[{"service":"payments","environment":"prod","source":"ladder","stage":"` + ns + `/payments-prod","placement":{"namespace":"` + ns +
		`","provenance":"` + p.Provenance + `","source":"` + p.Source + `","evidence":"` + p.Evidence + `","confirm":` + map[bool]string{true: "true", false: "false"}[p.Confirm] + `}}]`
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: deliveryNS, Name: delivery.PlacementsConfigMap},
		Data: map[string]string{"stages.json": state, "rows.json": row}}
	e := newEnv(t, s, ptr, verifiedRepo("payments-candidates", "latest@"+dB),
		kustomization("payments", "payments-prod", "True", "ReconciliationSucceeded", "latest@"+dA, "latest@"+dA),
		deployment("payments", 2, 2), pactoObj("payments", "payments", c1, "Compliant", 2), cm)
	envs := &EnvironmentReconciler{Client: e.c, APIReader: e.c, Namespace: deliveryNS, Now: e.st.Now, DeployerRole: deployerRole}
	e.st.Environments, e.op.Environments = envs, envs
	return e
}

func inferredHere() delivery.Placement {
	return delivery.Placement{Namespace: ns, Provenance: delivery.PlacementInferred, Source: delivery.PlacementFromGitOps,
		Evidence: "Argo CD source github.com/acme/payments", Confirm: true}
}

func TestHeldPromoteReasonCarriesPlacement(t *testing.T) {
	e := managedStageWorld(t, true, inferredHere())
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 1 {
		t.Fatalf("want one automatic promote: %v %d", err, len(e.ops()))
	}
	op := e.ops()[0]
	want := "deploy payments to namespace " + ns + " (Argo CD source github.com/acme/payments)? automatic promote of the newest eligible candidate"
	if op.Spec.Reason != want {
		t.Fatalf("reason %q", op.Spec.Reason)
	}
	// The person approves it: the pointer gets its digest, the placement is
	// confirmed and the pointer unsuspended.
	held := e.drive(op.Name)
	e.approve(held, "a1", "alice", delivery.Approve)
	e.drive(op.Name)
	e.drive(op.Name) // the digest is written, then the next reconcile reads it
	ptr := &unstructured.Unstructured{}
	ptr.SetGroupVersionKind(gvkOCIRepository)
	if err := e.c.Get(t.Context(), client.ObjectKey{Namespace: ns, Name: "payments-prod"}, ptr); err != nil {
		t.Fatal(err)
	}
	digest, _, _ := unstructured.NestedString(ptr.Object, "spec", "ref", "digest")
	if digest != dB || suspended(ptr) {
		t.Fatalf("pointer %v", ptr.Object)
	}
	sa := &corev1.ServiceAccount{}
	if err := e.c.Get(t.Context(), client.ObjectKey{Namespace: ns, Name: "payments-deployer"}, sa); err != nil {
		t.Fatal(err)
	}
}

func TestUnapprovedPromoteNeverUnsuspends(t *testing.T) {
	e := managedStageWorld(t, false, inferredHere())
	if err := e.reconcileStage("payments-prod"); err != nil || len(e.ops()) != 1 {
		t.Fatalf("want one automatic promote: %v %d", err, len(e.ops()))
	}
	e.drive(e.ops()[0].Name)
	op := e.drive(e.ops()[0].Name)
	if !strings.HasPrefix(op.Status.Message, "deploy payments to namespace "+ns) {
		t.Fatalf("op %+v", op.Status)
	}
	ptr := &unstructured.Unstructured{}
	ptr.SetGroupVersionKind(gvkOCIRepository)
	if err := e.c.Get(t.Context(), client.ObjectKey{Namespace: ns, Name: "payments-prod"}, ptr); err != nil || !suspended(ptr) {
		t.Fatalf("pointer %v %v", err, ptr.Object)
	}
}

func TestTrustQuestionCarriesPlacement(t *testing.T) {
	w := newWatch(t, registryAt("-"))
	s := w.e.getStage()
	s.UID, s.Labels = "uid-stage", map[string]string{delivery.ManagedLabel: delivery.ManagedBy}
	must(t, w.e.c.Update(t.Context(), s))
	p := inferredHere()
	row := `[{"service":"payments","environment":"prod","source":"ladder","stage":"` + ns + `/payments-prod","placement":{"namespace":"` + ns +
		`","provenance":"inferred","evidence":"` + p.Evidence + `","confirm":true}}]`
	must(t, w.e.c.Create(t.Context(), &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: deliveryNS, Name: delivery.PlacementsConfigMap},
		Data: map[string]string{"stages.json": `{"objects":[{"kind":"Stage","namespace":"` + ns + `","name":"payments-prod","uid":"uid-stage"}]}`, "rows.json": row}}))
	w.e.st.Environments = &EnvironmentReconciler{APIReader: w.e.c, Namespace: deliveryNS}
	w.push("1.0.0", otherRepo, w.ci(otherSub, otherRepo))
	w.reconcile()
	ops := w.trustOps()
	want := "deploy payments to namespace " + ns + " (Argo CD source github.com/acme/payments)? new signer " + otherSub
	if len(ops) != 1 || ops[0].Spec.Reason != want {
		t.Fatalf("ops %+v", ops)
	}
}
