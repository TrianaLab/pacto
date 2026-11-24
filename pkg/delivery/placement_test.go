package delivery

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/workloadmanager"
)

func signPlacement(t *testing.T, p any) []byte {
	t.Helper()
	b, _, err := signStatement(t.Context(), plainSigner{}, map[string]any{"_type": StatementType, "subject": []Subject{},
		"predicateType": PredicatePlacement, "predicate": p})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ok([]byte) error { return nil }

func TestOlderPromoterStillReadsPolicyWithPlacementPublished(t *testing.T) {
	// The publisher policy carries no placement, so a promoter that knows
	// nothing of placement opens it with its strict decode, and a publisher
	// it no longer names is no longer trusted.
	narrowed := PublisherPolicy{Publishers: []PublisherRule{policyRule}, At: "2026-11-23T10:00:00Z"}
	got, err := OpenPolicy(signPolicy(t, narrowed), ok)
	if err != nil || len(got.Publishers) != 1 {
		t.Fatalf("publisher policy: %+v %v", got, err)
	}
	removed := PublisherIdentity{Issuer: issuerGitHubActions, Subject: "https://github.com/acme/orders/.github/workflows/release.yml@refs/heads/main",
		Repository: "https://github.com/acme/orders", CI: true}
	if v := JudgePublisher(ServiceRef{Name: "orders"}, removed, "", got.Publishers, PublisherSourcePolicy, nil, nil); v.Verdict == VerdictAccepted {
		t.Fatalf("a removed publisher is still trusted: %+v", v)
	}
	// The placement policy beside it is its own predicate: neither opens
	// as the other.
	placement := signPlacement(t, PlacementPolicy{Rules: []PlacementRule{{Name: "r", Environments: []string{"*"}, Namespace: "{service}"}}, At: narrowed.At})
	if _, err := OpenPolicy(placement, ok); err == nil {
		t.Fatal("a placement policy opened as a publisher policy")
	}
	if _, err := OpenPlacementPolicy(signPolicy(t, narrowed), ok); err == nil {
		t.Fatal("a publisher policy opened as a placement policy")
	}
}

func TestPlacementPolicyStrictDecode(t *testing.T) {
	b := signPlacement(t, map[string]any{"rules": []any{}, "at": "2026-11-23T10:00:00Z", "extra": true})
	if _, err := OpenPlacementPolicy(b, ok); err == nil {
		t.Fatal("an unknown key opened")
	}
	b = signPlacement(t, PlacementPolicy{At: "2026-11-23T10:00:00Z"})
	if _, err := OpenPlacementPolicy(b, func([]byte) error { return errors.New("untrusted") }); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("untrusted: %v", err)
	}
	if _, err := OpenPlacementPolicy(signPlacement(t, PlacementPolicy{}), ok); err == nil || !strings.Contains(err.Error(), "signed time") {
		t.Fatalf("no at: %v", err)
	}
}

func TestPlacementPolicyTemplate(t *testing.T) {
	pol, err := OpenPlacementPolicy(signPlacement(t, PlacementPolicy{At: "2026-11-23T10:00:00Z",
		Rules: []PlacementRule{{Name: "per-env", Services: []string{"pay*"}, Environments: []string{"staging"}, Namespace: "{service}-{environment}"}}}), ok)
	if err != nil {
		t.Fatal(err)
	}
	in := placeIn("staging")
	in.Policy = pol.Rules
	in.Namespaces["payments-staging"] = true
	if p := ResolvePlacement(in); p.Namespace != "payments-staging" || p.Provenance != PlacementDeclared || p.Source != PlacementFromPolicy || p.Confirm {
		t.Fatalf("placement %+v", p)
	}
	// Another environment or service is not the rule's.
	in.Rung = "production"
	if p := ResolvePlacement(in); p.Source == PlacementFromPolicy {
		t.Fatalf("another environment: %+v", p)
	}
}

func TestPlacementPolicyBadNamespaceRefused(t *testing.T) {
	for _, ns := range []string{"{team}", "Payments", "", "{service}.{environment}"} {
		b := signPlacement(t, PlacementPolicy{At: "2026-11-23T10:00:00Z", Rules: []PlacementRule{{Name: "r", Environments: []string{"*"}, Namespace: ns}}})
		if _, err := OpenPlacementPolicy(b, ok); err == nil {
			t.Errorf("namespace %q opened", ns)
		}
	}
	b := signPlacement(t, PlacementPolicy{At: "2026-11-23T10:00:00Z", Rules: []PlacementRule{{Name: "r", Namespace: "x"}}})
	if _, err := OpenPlacementPolicy(b, ok); err == nil {
		t.Error("a rule with no environment opened")
	}
}

// placeIn places payments of team-payments, published at
// reg.example/acme/payments, in rung of a cluster hosting staging and
// production, where namespaces staging and production exist.
func placeIn(rung string) PlacementInput {
	return PlacementInput{Service: ServiceRef{Name: "payments"}, Owner: "team-payments", Rung: rung,
		HostedRungs: []string{"staging", "production"}, BundleRepository: "reg.example/acme/payments",
		Namespaces:       map[string]bool{"staging": true, "production": true},
		SystemNamespaces: []string{"pacto-delivery", "flux-system", "argocd"}}
}

// placedPacto is a reconciled Pacto of service owned by team in ns whose
// contract is at repo.
func placedPacto(ns, service, team, repo string, labels map[string]string) PactoView {
	var p PactoView
	p.Metadata.Namespace, p.Metadata.Name, p.Metadata.Labels = ns, service, labels
	p.Status.Contract = &ContractInfoView{ServiceName: service, Owner: &contract.Owner{Team: team}, ResolvedRef: "oci://" + repo + "@sha256:aa"}
	return p
}

var (
	argoManager = workloadmanager.Manager{Name: workloadmanager.ArgoCD, Product: "Argo CD", OwnerKind: "Application", OwnerName: "payments", Evidence: "tracking annotation"}
	helmManager = workloadmanager.Manager{Name: workloadmanager.Helm, Product: "Helm", OwnerKind: "release", OwnerName: "payments", Evidence: "release annotations"}
)

func TestHandWrittenStageWins(t *testing.T) {
	in := placeIn("staging")
	in.Policy = []PlacementRule{{Name: "r", Environments: []string{"*"}, Namespace: "elsewhere"}}
	in.Stages = []StageObject{{Metadata: ObjectMeta{Namespace: "team-a", Name: "payments-staging"},
		Spec: StageSpec{Service: ServiceRef{Name: "payments"}, Environment: "staging"}}}
	// A system namespace is no refusal here: Pacto creates nothing for it.
	p := ResolvePlacement(in)
	if p.Namespace != "team-a" || p.Source != PlacementFromStage || p.Provenance != PlacementDeclared || p.Confirm || p.Refused != "" {
		t.Fatalf("placement %+v", p)
	}
	in.Rung = "production"
	if p := ResolvePlacement(in); p.Source == PlacementFromStage {
		t.Fatalf("another environment: %+v", p)
	}
}

func TestPolicyPlacementIsDeclared(t *testing.T) {
	in := placeIn("production")
	in.Policy = []PlacementRule{{Name: "orders", Services: []string{"orders"}, Environments: []string{"*"}, Namespace: "o"},
		{Name: "prod", Environments: []string{"production"}, Namespace: "prod-{service}"}}
	in.Namespaces["prod-payments"] = true
	p := ResolvePlacement(in)
	if p.Namespace != "prod-payments" || p.Provenance != PlacementDeclared || p.Evidence != "placement rule prod" || p.Confirm {
		t.Fatalf("placement %+v", p)
	}
}

func TestUnverifiedPolicyPlacementIgnored(t *testing.T) {
	// An unverified policy never reaches the input: no rule, no declared step.
	in := placeIn("staging")
	if p := ResolvePlacement(in); p.Provenance == PlacementDeclared || !p.Confirm {
		t.Fatalf("placement %+v", p)
	}
	if _, err := OpenPlacementPolicy(signPlacement(t, PlacementPolicy{At: "2026-11-23T10:00:00Z"}), func([]byte) error { return errors.New("unknown key") }); err == nil {
		t.Fatal("an unverified policy opened")
	}
}

func TestObservedPactoPlacesOnAdoptedApplier(t *testing.T) {
	in := placeIn("staging")
	in.Namespaces["payments-staging"] = true
	in.Pactos = []PactoView{placedPacto("payments-staging", "payments", "team-payments", "reg.example/acme/payments", nil)}
	in.Occupants = map[string][]NamespaceOccupant{"payments-staging": {{Workload: "Deployment/payments", Service: "payments", Team: "team-payments"}}}
	in.Managers = map[string]workloadmanager.Manager{"payments-staging": argoManager}
	// A Pacto's service and team come from the bundle it references, so a
	// person confirms even an observed one.
	p := ResolvePlacement(in)
	if p.Namespace != "payments-staging" || p.Provenance != PlacementObserved || p.Source != PlacementFromPacto || !p.Confirm ||
		!strings.Contains(p.Evidence, "applied by Argo CD") {
		t.Fatalf("placement %+v", p)
	}
	// With no manager the Pacto is evidence only: inferred, for a person.
	in.Managers = nil
	if p := ResolvePlacement(in); p.Provenance != PlacementInferred || !p.Confirm {
		t.Fatalf("unmanaged %+v", p)
	}
}

func TestSquattedPactoNeverObserved(t *testing.T) {
	in := placeIn("staging")
	in.Namespaces["team-x-staging"] = true
	in.Managers = map[string]workloadmanager.Manager{"team-x-staging": argoManager}
	for _, squat := range []PactoView{
		placedPacto("team-x-staging", "payments", "team-payments", "reg.example/team-x/payments", nil),
		placedPacto("team-x-staging", "payments", "team-x", "reg.example/acme/payments", nil),
		{}, // never reconciled
	} {
		in.Pactos = []PactoView{squat}
		if p := ResolvePlacement(in); p.Namespace == "team-x-staging" {
			t.Fatalf("squatted %+v", p)
		}
	}
}

// TestGenuineBundleSquatterNeedsAPerson: a Pacto in another team's
// namespace that references the service's genuine bundle carries the
// service's own team in its status, so nothing tells it from the service's
// own: it is never confirmed without a person, even on an adopted applier.
func TestGenuineBundleSquatterNeedsAPerson(t *testing.T) {
	in := placeIn("staging")
	in.Namespaces["team-x-staging"] = true
	in.Pactos = []PactoView{placedPacto("team-x-staging", "payments", "team-payments", "reg.example/acme/payments", nil)}
	in.Occupants = map[string][]NamespaceOccupant{"team-x-staging": {{Workload: "Pacto payments", Service: "payments", Team: "team-payments"}}}
	in.Managers = map[string]workloadmanager.Manager{"team-x-staging": argoManager}
	if p := ResolvePlacement(in); !p.Confirm {
		t.Fatalf("placement %+v", p)
	}
}

// TestUnreadStepNeverFallsThrough: evidence that could not be read is
// unknown, never absent: from the step that reads it on, nothing places, so
// the default namespace never answers for an unread policy, Pactos or GitOps
// objects. A hand-written Stage still places.
func TestUnreadStepNeverFallsThrough(t *testing.T) {
	for _, source := range []string{PlacementFromPolicy, PlacementFromPacto, PlacementFromGitOps} {
		in := placeIn("staging")
		in.Unread = map[string]string{source: "x could not be read"}
		if p := ResolvePlacement(in); p.Placed() || p.Provenance != PlacementUnknown || p.Evidence != "x could not be read, so Pacto cannot tell where payments runs in staging" {
			t.Fatalf("%s: %+v", source, p)
		}
	}
	// A step before the unread one still places.
	in := placeIn("staging")
	in.Unread = map[string]string{PlacementFromGitOps: "x could not be read"}
	in.Policy = []PlacementRule{{Name: "r", Environments: []string{"*"}, Namespace: "staging"}}
	if p := ResolvePlacement(in); !p.Placed() || p.Source != PlacementFromPolicy {
		t.Fatalf("policy before the unread step: %+v", p)
	}
	in.Stages = []StageObject{{Metadata: ObjectMeta{Namespace: "team-a", Name: "s"}, Spec: StageSpec{Service: ServiceRef{Name: "payments"}, Environment: "staging"}}}
	in.Unread[PlacementFromPolicy] = "y"
	if p := ResolvePlacement(in); p.Source != PlacementFromStage {
		t.Fatalf("hand-written: %+v", p)
	}
}

// TestDomainlessHandWrittenStageMatchesAnyDomain: a hand-written Stage that
// names no domain is the service's in any domain, as everywhere else in
// delivery; one naming another domain is not.
func TestDomainlessHandWrittenStageMatchesAnyDomain(t *testing.T) {
	in := placeIn("staging")
	in.Service.Domain = "reg.example/acme"
	in.Stages = []StageObject{{Metadata: ObjectMeta{Namespace: "staging", Name: "payments"}, Spec: StageSpec{Service: ServiceRef{Name: "payments"}, Environment: "staging"}}}
	if p := ResolvePlacement(in); p.Source != PlacementFromStage || p.Namespace != "staging" {
		t.Fatalf("domain-less: %+v", p)
	}
	in.Stages[0].Spec.Service.Domain = "reg.example/other"
	if p := ResolvePlacement(in); p.Source == PlacementFromStage {
		t.Fatalf("another domain: %+v", p)
	}
}

// TestGitOpsRepositoryInTwoNamespacesUnknown: a repository deployed into two
// namespaces of the environment (a monorepo of several services) never
// places the service in whichever is listed first.
func TestGitOpsRepositoryInTwoNamespacesUnknown(t *testing.T) {
	in := placeIn("staging")
	in.Repository = "https://github.com/acme/platform"
	in.Namespaces["orders-staging"], in.Namespaces["payments-staging"] = true, true
	in.GitOps = []GitOpsTarget{{Kind: "Application", Namespace: "argocd", Name: "orders", Repositories: []string{in.Repository}, Destination: "orders-staging"},
		{Kind: "Application", Namespace: "argocd", Name: "payments", Repositories: []string{in.Repository}, Destination: "payments-staging"},
		{Kind: "Application", Namespace: "argocd", Name: "payments-2", Repositories: []string{in.Repository}, Destination: "payments-staging"}}
	p := ResolvePlacement(in)
	if p.Placed() || p.Provenance != PlacementUnknown || p.Evidence != "github.com/acme/platform is deployed into 2 namespaces of staging: orders-staging, payments-staging" {
		t.Fatalf("placement %+v", p)
	}
}

func TestTwoObservedNamespacesUnknown(t *testing.T) {
	in := placeIn("staging")
	in.Pactos = []PactoView{placedPacto("b", "payments", "team-payments", "reg.example/acme/payments", map[string]string{EnvironmentLabel: "staging"}),
		placedPacto("a", "payments", "team-payments", "reg.example/acme/payments", map[string]string{EnvironmentLabel: "staging"})}
	p := ResolvePlacement(in)
	if p.Provenance != PlacementUnknown || p.Placed() || p.Evidence != "payments runs in 2 namespaces of staging: a, b" {
		t.Fatalf("placement %+v", p)
	}
}

func TestInferredFromNamespace(t *testing.T) {
	in := placeIn("staging")
	in.InferredFrom = "k8s:payments-staging/Deployment/payments"
	in.Namespaces["payments-staging"] = true
	p := ResolvePlacement(in)
	if p.Namespace != "payments-staging" || p.Provenance != PlacementInferred || p.Source != PlacementFromInferred || !p.Confirm ||
		p.Evidence != "the published contract was inferred from Deployment payments-staging/payments" {
		t.Fatalf("placement %+v", p)
	}
}

func TestGitOpsSourceArgo(t *testing.T) {
	in := placeIn("staging")
	in.Repository = "https://github.com/acme/payments"
	in.Namespaces["payments-staging"] = true
	for _, repos := range [][]string{{"https://github.com/Acme/Payments.git"}, {"https://example.com/other", "git@github.com:acme/payments.git"}, {"ssh://git@github.com/acme/payments/"}} {
		in.GitOps = []GitOpsTarget{{Kind: "Application", Namespace: "argocd", Name: "pay", Repositories: []string{"https://github.com/acme/other"}, Destination: "payments-staging"},
			{Kind: "Application", Namespace: "argocd", Name: "payments", Repositories: repos, Destination: "payments-staging"}}
		p := ResolvePlacement(in)
		if p.Namespace != "payments-staging" || p.Source != PlacementFromGitOps || p.Provenance != PlacementInferred ||
			!strings.Contains(p.Evidence, "Application argocd/payments deploys github.com/acme/payments") {
			t.Fatalf("repos %v: %+v", repos, p)
		}
	}
}

func TestGitOpsSourceFlux(t *testing.T) {
	in := placeIn("production")
	in.Repository = "https://github.com/acme/payments"
	in.Namespaces["payments-production"] = true
	in.GitOps = []GitOpsTarget{{Kind: "Kustomization", Namespace: "flux-system", Name: "apps", Repositories: []string{"https://github.com/acme/payments"}},
		{Kind: "Kustomization", Namespace: "flux-system", Name: "payments", Repositories: []string{"https://github.com/acme/payments"}, Destination: "payments-production"}}
	if p := ResolvePlacement(in); p.Namespace != "payments-production" || p.Source != PlacementFromGitOps {
		t.Fatalf("placement %+v", p)
	}
}

func TestGitOpsSourceNeedsAcceptedVerdict(t *testing.T) {
	in := placeIn("staging")
	in.Namespaces["payments-staging"] = true
	in.GitOps = []GitOpsTarget{{Kind: "Application", Name: "payments", Repositories: []string{"https://github.com/acme/payments"}, Destination: "payments-staging"}}
	if p := ResolvePlacement(in); p.Source == PlacementFromGitOps {
		t.Fatalf("no accepted verdict: %+v", p)
	}
}

func TestGitOpsSourceApplierReadsObserved(t *testing.T) {
	in := placeIn("staging")
	in.Repository = "https://github.com/acme/payments"
	in.GitOps = []GitOpsTarget{{Kind: "Application", Namespace: "argocd", Name: "payments", Repositories: []string{in.Repository}, Destination: "payments-staging"}}
	in.Managers = map[string]workloadmanager.Manager{"payments-staging": argoManager}
	// Observed on the applier already running it, even with no namespace
	// listed: Pacto creates no Flux object for it.
	in.Namespaces["payments-staging"] = true
	if p := ResolvePlacement(in); p.Provenance != PlacementObserved || p.Source != PlacementFromGitOps || p.Confirm {
		t.Fatalf("placement %+v", p)
	}
}

func TestHelmManagedWorkloadGetsNoFluxObjects(t *testing.T) {
	in := placeIn("staging")
	in.Managers = map[string]workloadmanager.Manager{"staging": helmManager}
	p := ResolvePlacement(in)
	if p.Provenance != PlacementUnknown || p.Placed() || p.Confirm || !strings.HasPrefix(p.Evidence, "owned by Helm") {
		t.Fatalf("placement %+v", p)
	}
}

func TestIDPOwnedWorkloadReadsObserved(t *testing.T) {
	in := placeIn("staging")
	in.Managers = map[string]workloadmanager.Manager{"staging": {Name: workloadmanager.Unsupported, Product: "Operator", OwnerKind: "Platform", OwnerName: "payments", Evidence: "controller ownerReference"}}
	if p := ResolvePlacement(in); p.Provenance != PlacementUnknown || !strings.Contains(p.Evidence, "owned by Operator (Platform payments") {
		t.Fatalf("foreign controller %+v", p)
	}
	in.Managers["staging"] = workloadmanager.Manager{Name: workloadmanager.FluxKustomization, Product: "Flux", OwnerKind: "Kustomization", OwnerName: "payments", Evidence: "kustomize-controller labels"}
	if p := ResolvePlacement(in); p.Provenance != PlacementObserved || p.Confirm {
		t.Fatalf("adopted Flux %+v", p)
	}
	// A declared placement stays declared on an adopted applier.
	in.Policy = []PlacementRule{{Name: "r", Environments: []string{"*"}, Namespace: "staging"}}
	if p := ResolvePlacement(in); p.Provenance != PlacementDeclared {
		t.Fatalf("declared %+v", p)
	}
}

func TestDefaultNamespaceIsRungName(t *testing.T) {
	p := ResolvePlacement(placeIn("production"))
	if p.Namespace != "production" || p.Provenance != PlacementDefault || p.Source != PlacementFromRungName || !p.Confirm || !p.Placed() {
		t.Fatalf("placement %+v", p)
	}
}

func TestUnresolvedRungEnvironmentNotPlaced(t *testing.T) {
	in := placeIn("staging")
	in.Namespaces["legacy"] = true
	in.InferredFrom = "k8s:legacy/Deployment/payments"
	in.Repository = "https://github.com/acme/payments"
	in.GitOps = []GitOpsTarget{{Kind: "Application", Name: "p", Repositories: []string{in.Repository}, Destination: "legacy"}}
	in.Pactos = []PactoView{placedPacto("legacy", "payments", "team-payments", "reg.example/acme/payments", nil)}
	p := ResolvePlacement(in)
	if p.Namespace == "legacy" {
		t.Fatalf("placed in an unresolved namespace: %+v", p)
	}
	for _, what := range []string{"the Pacto", "the inferred-from", "the GitOps destination"} {
		if !strings.Contains(p.Evidence, what+" namespace legacy is not tied to staging") {
			t.Errorf("evidence %q does not say why %s was skipped", p.Evidence, what)
		}
	}
}

func TestSingleHostedRungResolvesAnyNamespace(t *testing.T) {
	in := placeIn("staging")
	in.HostedRungs = []string{"staging"}
	in.InferredFrom = "k8s:payments/Deployment/payments"
	in.Namespaces["payments"] = true
	if p := ResolvePlacement(in); p.Namespace != "payments" || p.Source != PlacementFromInferred {
		t.Fatalf("placement %+v", p)
	}
}

func TestPlacementRefusesAnotherTeamsNamespace(t *testing.T) {
	in := placeIn("staging")
	in.HostedRungs = []string{"staging"}
	in.InferredFrom = "k8s:orders/Deployment/orders"
	in.Namespaces["orders"] = true
	in.Occupants = map[string][]NamespaceOccupant{"orders": {{Workload: "Deployment/orders", Service: "orders", Team: "team-orders"}}}
	p := ResolvePlacement(in)
	if p.Placed() || p.Refused != "namespace orders belongs to orders (team-orders)" {
		t.Fatalf("placement %+v", p)
	}
	// The service's own workload, or its team's, passes.
	in.Occupants["orders"] = []NamespaceOccupant{{Service: "payments", Team: "team-payments"}, {Service: "ledger", Team: "team-payments"}}
	if p := ResolvePlacement(in); !p.Placed() {
		t.Fatalf("own team %+v", p)
	}
}

func TestPlacementUnknownOwnerRefused(t *testing.T) {
	in := placeIn("staging")
	in.Occupants = map[string][]NamespaceOccupant{"staging": {{Workload: "Deployment/legacy"}}}
	if p := ResolvePlacement(in); p.Placed() || p.Refused != "namespace staging holds workloads of an unknown owner" {
		t.Fatalf("placement %+v", p)
	}
	// A bundle with no owner team owns nothing: an occupied namespace is
	// never its own.
	in.Owner = ""
	in.Occupants["staging"] = []NamespaceOccupant{{Service: "orders", Team: "team-orders"}}
	if p := ResolvePlacement(in); p.Placed() {
		t.Fatalf("no owner %+v", p)
	}
}

func TestInferredFromSystemNamespaceRefused(t *testing.T) {
	for _, ns := range []string{"kube-system", "pacto-delivery", "flux-system", "argocd"} {
		in := placeIn("staging")
		in.HostedRungs = []string{"staging"}
		in.InferredFrom = "k8s:" + ns + "/Deployment/coredns"
		in.Namespaces[ns] = true
		if p := ResolvePlacement(in); p.Placed() || p.Refused != ns+" is a system namespace" {
			t.Errorf("%s: %+v", ns, p)
		}
	}
}

func TestPolicyTemplateIntoSystemNamespaceRefused(t *testing.T) {
	in := placeIn("staging")
	in.Service.Name = "kube"
	in.Policy = []PlacementRule{{Name: "sys", Environments: []string{"*"}, Namespace: "{service}-system"}}
	if p := ResolvePlacement(in); p.Placed() || p.Refused != "kube-system is a system namespace" {
		t.Fatalf("template %+v", p)
	}
	in = placeIn("staging")
	in.Policy = []PlacementRule{{Name: "pub", Environments: []string{"staging"}, Namespace: "kube-public"}}
	if p := ResolvePlacement(in); p.Placed() || p.Provenance != PlacementDeclared || p.Refused == "" {
		t.Fatalf("literal %+v", p)
	}
}

func TestNamespaceWithUnattributedWorkloadRefused(t *testing.T) {
	in := placeIn("staging")
	in.Policy = []PlacementRule{{Name: "r", Environments: []string{"*"}, Namespace: "legacy"}}
	in.Namespaces["legacy"] = true
	in.Occupants = map[string][]NamespaceOccupant{"legacy": {{Workload: "Deployment/billing"}}}
	// Even a declared placement never lands beside a workload nobody owns.
	if p := ResolvePlacement(in); p.Placed() || !strings.Contains(p.Refused, "unknown owner") {
		t.Fatalf("placement %+v", p)
	}
}

func TestRungNamespaceSharedByTeams(t *testing.T) {
	in := placeIn("staging")
	in.Occupants = map[string][]NamespaceOccupant{"staging": {{Workload: "Deployment/orders", Service: "orders", Team: "team-orders"}}}
	if p := ResolvePlacement(in); p.Placed() || p.Refused == "" {
		t.Fatalf("default into a shared namespace: %+v", p)
	}
	in.InferredFrom = "k8s:staging/Deployment/payments"
	if p := ResolvePlacement(in); p.Placed() {
		t.Fatalf("inferred into a shared namespace: %+v", p)
	}
	in.Policy = []PlacementRule{{Name: "shared", Environments: []string{"staging"}, Namespace: "staging"}}
	if p := ResolvePlacement(in); !p.Placed() || p.Provenance != PlacementDeclared {
		t.Fatalf("declared into a shared namespace: %+v", p)
	}
}

func TestTwoRungsOneNamespaceRefused(t *testing.T) {
	in := placeIn("production")
	in.HostedRungs = []string{"production"}
	in.InferredFrom = "k8s:payments/Deployment/payments"
	in.Namespaces["payments"] = true
	in.Taken = map[string]string{"payments": "staging"}
	if p := ResolvePlacement(in); p.Placed() || p.Refused != "namespace payments already holds the staging environment of payments" {
		t.Fatalf("placement %+v", p)
	}
}

func TestMissingNamespaceWithoutCreate(t *testing.T) {
	in := placeIn("staging")
	delete(in.Namespaces, "staging")
	if p := ResolvePlacement(in); p.Placed() || p.Refused != "namespace staging does not exist: create it or set delivery.environments.createNamespaces=true" {
		t.Fatalf("placement %+v", p)
	}
	in.CreateNamespaces = true
	if p := ResolvePlacement(in); !p.Placed() || !p.Confirm {
		t.Fatalf("create %+v", p)
	}
}

func TestRepoKey(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/Acme/Payments.git": "github.com/acme/payments",
		"git@github.com:acme/payments.git":     "github.com/acme/payments",
		"ssh://git@github.com/acme/payments/":  "github.com/acme/payments",
		"github.com/acme/payments":             "github.com/acme/payments",
	} {
		if got := repoKey(in); got != want {
			t.Errorf("repoKey(%q) = %q", in, got)
		}
	}
}

func TestPlacementStepsAlwaysPlace(t *testing.T) {
	// The last step always applies, so ResolvePlacement always answers;
	// with every step skipped, the zero placement is not placed.
	if (Placement{}).Placed() {
		t.Fatal("the zero placement is placed")
	}
	saved := placementSteps
	t.Cleanup(func() { placementSteps = saved })
	placementSteps = nil
	if p := ResolvePlacement(placeIn("staging")); p != (Placement{}) {
		t.Fatalf("no step: %+v", p)
	}
}

const (
	memberUID = "6f1c2a3b-4d5e-4f60-8a7b-9c0d1e2f3a4b"
	otherUID  = "0a1b2c3d-4e5f-4a6b-8c7d-8e9f0a1b2c3d"
)

func TestPlacementRuleClusters(t *testing.T) {
	open := func(clusters ...string) (PlacementPolicy, error) {
		return OpenPlacementPolicy(signPlacement(t, PlacementPolicy{At: "2026-11-23T10:00:00Z",
			Rules: []PlacementRule{{Name: "eu", Environments: []string{"production"}, Namespace: "{service}", ClusterUIDs: clusters}}}), ok)
	}
	if p, err := open(memberUID); err != nil || !slices.Equal(p.Rules[0].ClusterUIDs, []string{memberUID}) {
		t.Fatalf("uid: %+v %v", p, err)
	}
	for _, bad := range []string{"eu-prod", "", strings.ToUpper(memberUID), memberUID + "0", "6f1c2a3b4d5e4f608a7b9c0d1e2f3a4b"} {
		if _, err := open(memberUID, bad); err == nil || !strings.Contains(err.Error(), "name the member by its uid") {
			t.Errorf("cluster %q: %v", bad, err)
		}
	}
	b := signPlacement(t, map[string]any{"at": "2026-11-23T10:00:00Z",
		"rules": []any{map[string]any{"name": "r", "environments": []string{"*"}, "namespace": "x", "clusters": []string{memberUID}, "members": true}}})
	if _, err := OpenPlacementPolicy(b, ok); err == nil {
		t.Fatal("an unknown rule key opened")
	}
}

func TestPlaceByPolicySkipsRuleForOtherCluster(t *testing.T) {
	in := placeIn("production")
	in.ClusterUID = memberUID
	in.Namespaces["eu-payments"], in.Namespaces["us-payments"] = true, true
	in.Policy = []PlacementRule{
		{Name: "us", Environments: []string{"production"}, Namespace: "us-{service}", ClusterUIDs: []string{otherUID}},
		{Name: "eu", Environments: []string{"production"}, Namespace: "eu-{service}", ClusterUIDs: []string{otherUID, memberUID}},
	}
	if p := ResolvePlacement(in); p.Namespace != "eu-payments" || p.Source != PlacementFromPolicy || p.Evidence != "placement rule eu" {
		t.Fatalf("placement %+v", p)
	}
	// No rule for this cluster: the next step places.
	in.ClusterUID = "1b2c3d4e-5f60-4a7b-8c9d-0e1f2a3b4c5d"
	if p := ResolvePlacement(in); p.Source == PlacementFromPolicy {
		t.Fatalf("another cluster's rule placed: %+v", p)
	}
	// A cluster that does not know its uid matches no rule naming clusters.
	in.ClusterUID = ""
	if p := ResolvePlacement(in); p.Source == PlacementFromPolicy {
		t.Fatalf("a rule naming clusters placed without a uid: %+v", p)
	}
}

func TestPlaceByPolicyApprovalRenameNeedsUID(t *testing.T) {
	// The hub renamed the member eu-prod at approval; the member never
	// learns that name, so a rule naming it is refused at open.
	rule := func(cluster string) []byte {
		return signPlacement(t, PlacementPolicy{At: "2026-11-23T10:00:00Z",
			Rules: []PlacementRule{{Name: "eu", Environments: []string{"production"}, Namespace: "eu-{service}", ClusterUIDs: []string{cluster}}}})
	}
	if _, err := OpenPlacementPolicy(rule("eu-prod"), ok); err == nil || !strings.Contains(err.Error(), "a display name can differ between the hub and the member") {
		t.Fatalf("display name: %v", err)
	}
	pol, err := OpenPlacementPolicy(rule(memberUID), ok)
	if err != nil {
		t.Fatal(err)
	}
	in := placeIn("production")
	in.ClusterUID, in.Policy = memberUID, pol.Rules
	in.Namespaces["eu-payments"] = true
	if p := ResolvePlacement(in); p.Namespace != "eu-payments" || p.Provenance != PlacementDeclared {
		t.Fatalf("placement %+v", p)
	}
}

func TestPlaceByPolicyWithoutClustersUnchanged(t *testing.T) {
	// 511-S5's policies name no cluster: every cluster, with or without a
	// uid, places by them as before.
	for _, uid := range []string{"", memberUID} {
		in := placeIn("staging")
		in.ClusterUID = uid
		in.Policy = []PlacementRule{{Name: "per-env", Services: []string{"pay*"}, Environments: []string{"staging"}, Namespace: "{service}-{environment}"}}
		in.Namespaces["payments-staging"] = true
		if p := ResolvePlacement(in); p.Namespace != "payments-staging" || p.Source != PlacementFromPolicy {
			t.Fatalf("uid %q: %+v", uid, p)
		}
	}
}
