package fleet

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/finding"
)

// pluginPath is the http-service golden path naming a dockerfile plugin, also
// requiring orders (a provider with no offering) and a cache configuration no
// service resolves.
func pluginPath() *contract.Contract {
	c := templated("http-service", "1.0.0")
	c.Service.Template.Plugins = []contract.TemplatePlugin{{Name: "dockerfile", Description: "A Dockerfile"}}
	c.Dependencies = append(c.Dependencies, contract.Dependency{Name: "orders", Ref: "oci://ghcr.io/acme/orders", Required: true, Compatibility: "^1.0.0"})
	c.Configurations = append(c.Configurations, contract.Configuration{Name: "cache", Ref: "oci://ghcr.io/acme/redis@sha256:" + strings.Repeat("0", 64)})
	return c
}

func goldenDetail(t *testing.T, q *Query, name string) *ServiceDetailData {
	t.Helper()
	d, err := q.EntityDetail(KindService, string(NewServiceKeyDomain(acme, name)))
	if err != nil {
		t.Fatal(err)
	}
	return d.Service
}

func TestTemplateDetail(t *testing.T) {
	path := ociRev(acme, pluginPath(), pathFiles)
	q := buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{
		path, createdFrom(pathContentHash(t)), postgres(), ociRev(acme, usesPostgres("orders", "1.0.0"), nil),
	}})
	d := goldenDetail(t, q, "http-service").Template
	if d == nil {
		t.Fatal("a golden path has no template detail")
	}
	if len(d.Plugins) != 1 || d.Plugins[0].Name != "dockerfile" || d.Provenance != ProvenanceDeclared {
		t.Errorf("plugins = %+v, provenance %q", d.Plugins, d.Provenance)
	}
	checkRequirements(t, d)
	if c := d.CreatedServices.Items; len(c) != 1 || c[0].Label != "gift-cards" || d.HiddenCreatedServices != 0 {
		t.Errorf("created services = %+v, hidden %d", c, d.HiddenCreatedServices)
	}
	if d.Instantiate != nil || !strings.Contains(d.InstantiateReason, "no digest-pinned reference") {
		t.Errorf("a tag is never instantiated: %+v, %q", d.Instantiate, d.InstantiateReason)
	}
	if d.Signature.Status != SignatureUnknown || !strings.Contains(d.SignatureNote, "own contract declares") {
		t.Errorf("signature = %+v, note %q", d.Signature, d.SignatureNote)
	}
	if goldenDetail(t, q, "orders").Template != nil {
		t.Error("an ordinary service has a template detail")
	}
}

// checkRequirements checks the requirements of pluginPath: postgresql by
// dependency and configuration, orders with no offering, no provisioning, and
// the unresolved cache configuration with its reason.
func checkRequirements(t *testing.T, d *TemplateDetail) {
	t.Helper()
	if u := d.UnresolvedRequirements.Items; len(u) != 1 || u[0].Ref != "cache" || u[0].Reason == "" {
		t.Errorf("unresolved requirements = %+v", u)
	}
	providers := map[string]string{}
	for _, u := range d.Requirements.Items {
		providers[u.Provider.Label+"/"+u.Via] = u.Offering
	}
	if providers["postgresql/dependency"] != "postgresql" || providers["postgresql/configuration_reference"] != "postgresql" {
		t.Errorf("postgresql requirements = %v", providers)
	}
	if off, ok := providers["orders/dependency"]; !ok || off != "" {
		t.Errorf("a provider with no offering is still a requirement: %v", providers)
	}
	for _, u := range d.Requirements.Items {
		if u.Provisioning != nil {
			t.Errorf("a requirement carries provisioning: %+v", u)
		}
	}
}

func TestTemplateDetail_Instantiate(t *testing.T) {
	pinned := "oci://ghcr.io/acme/http-service@sha256:" + strings.Repeat("a", 64)
	tests := []struct {
		name, requested, resolved, ref string
	}{
		{"digest-pinned", "oci://ghcr.io/acme/http-service:1.0.0", pinned, pinned},
		{"local directory", "file:///srv/paths/http-service", "", "/srv/paths/http-service"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rev := ociRev(acme, pluginPath(), pathFiles)
			rev.RequestedRef, rev.ResolvedRef = tt.requested, tt.resolved
			rev.Provenance = &BundleProvenance{Signature: BundleSignature{Status: SignatureVerified, Signer: "platform@acme.dev", Issuer: "https://accounts.acme.dev"}}
			d := goldenDetail(t, buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{rev}}), "http-service").Template
			want := "pacto init <name> --from " + tt.ref + " --owner <team>"
			if d.Instantiate == nil || d.Instantiate.Ref != tt.ref || d.Instantiate.CLI != want || !strings.Contains(d.Instantiate.MCP, "from: "+tt.ref) || d.InstantiateReason != "" {
				t.Errorf("instantiate = %+v, reason %q", d.Instantiate, d.InstantiateReason)
			}
			if d.Signature.Status != SignatureVerified || d.Signature.Signer != "platform@acme.dev" {
				t.Errorf("signature = %+v", d.Signature)
			}
		})
	}
}

func TestTemplateDetailRestricted(t *testing.T) {
	r := goldenRestrictFleet(t, templated("http-service", "1.0.0")).Restrict(shopOnly)
	d := goldenDetail(t, r, "http-service").Template
	if d == nil || d.HiddenCreatedServices != 1 {
		t.Fatalf("template detail = %+v", d)
	}
	if c := d.CreatedServices.Items; len(c) != 1 || c[0].Label != "promo-cards" {
		t.Errorf("created services = %+v", c)
	}
}

func TestOriginDetail(t *testing.T) {
	q := goldenFleet(t)
	o := goldenDetail(t, q, "gift-cards").Origin
	if o == nil || o.Provenance != ProvenanceDeclared || o.Ref != "oci://ghcr.io/acme/http-service:1.0.0" || o.Digest != pathContentHash(t) || o.Reason != "" {
		t.Fatalf("origin = %+v", o)
	}
	if o.GoldenPath == nil || o.GoldenPath.Label != "http-service" || o.Revision == nil || o.Revision.Kind != KindRevision {
		t.Errorf("resolved origin = %+v, %+v", o.GoldenPath, o.Revision)
	}
	if goldenDetail(t, q, "orders").Origin != nil {
		t.Error("a service with no declared origin has an origin")
	}
}

func TestOriginDetail_Unresolved(t *testing.T) {
	missing := strings.Repeat("0", 64)
	u := goldenDetail(t, buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{createdFrom("sha256:" + missing)}}), "gift-cards").Origin
	if u == nil || u.GoldenPath != nil || u.Revision != nil || !strings.Contains(u.Reason, missing) || u.Provenance != ProvenanceDeclared {
		t.Errorf("unresolved origin = %+v", u)
	}
}

// stepsByKind indexes steps by step kind and plugin name.
func stepsByKind(steps []CreationStep) map[string]CreationStep {
	out := map[string]CreationStep{}
	for _, s := range steps {
		out[s.Step+s.Name] = s
	}
	return out
}

func TestCreationChecklist(t *testing.T) {
	q := buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{
		ociRev(acme, pluginPath(), pathFiles), createdFrom(pathContentHash(t)), postgres(), ociRev(acme, usesPostgres("orders", "1.0.0"), nil),
	}})
	steps := goldenDetail(t, q, "gift-cards").Creation
	want := []string{StepSignature, StepContract, StepValidation, StepPlugin, StepPublished, StepFleet, StepDeployed, StepProvisioning}
	if len(steps) != len(want) {
		t.Fatalf("steps = %+v", steps)
	}
	for i, s := range steps {
		if s.Step != want[i] {
			t.Errorf("step %d = %s, want %s", i, s.Step, want[i])
		}
	}
	got := stepsByKind(steps)
	for step, status := range map[string]string{
		StepSignature: StepStatusUnknown, StepContract: StepStatusDone, StepValidation: StepStatusUnknown, // no contract document to validate
		StepPlugin + "dockerfile": StepStatusUnknown, StepPublished: StepStatusDone, StepFleet: StepStatusDone,
		StepDeployed: StepStatusUnknown, StepProvisioning: StepStatusNotApplicable,
	} {
		if got[step].Status != status || got[step].Detail == "" {
			t.Errorf("%s = %+v, want %s", step, got[step], status)
		}
	}
	if !strings.Contains(got[StepFleet].Detail, "src") || !strings.Contains(got[StepDeployed].Detail, "no operational target") {
		t.Errorf("fleet %q, deployed %q", got[StepFleet].Detail, got[StepDeployed].Detail)
	}

	// A local contract document, so the revision is validated: the step reads
	// the snapshot record, never a detached copy.
	doc := "pactoVersion: \"2.4\"\nservice:\n  name: gift-cards\n  version: 0.1.0\n  owner: {team: checkout}\n  origin: {ref: ../http-service, digest: \"sha256:" + strings.Repeat("0", 64) + "\"}\n"
	local := &contract.Bundle{Contract: mustParse(t, doc), RawYAML: []byte(doc), FS: fstest.MapFS{}}
	unresolved := goldenDetail(t, buildDomainSnap(t, "local", &Collection{Revisions: []RawRevision{
		{Bundle: local, Domain: acme, RequestedRef: "file:///srv/gift-cards", Digest: "sha256:local"},
	}}), "gift-cards").Creation
	u := stepsByKind(unresolved)
	if u[StepSignature].Status != StepStatusUnknown || u[StepSignature].Detail != "the golden path is not in this snapshot" {
		t.Errorf("unresolved signature = %+v", u[StepSignature])
	}
	if u[StepValidation].Status != StepStatusDone {
		t.Errorf("validation of a valid local contract = %+v", u[StepValidation])
	}
	if u[StepPublished].Status != StepStatusUnknown || u[StepPublished].Detail != "seen only in local files" || len(unresolved) != 7 {
		t.Errorf("unresolved checklist = %+v", unresolved)
	}
}

func TestCreationChecklistSignature(t *testing.T) {
	tests := []struct {
		sig            *BundleProvenance
		status, detail string
	}{
		{&BundleProvenance{Signature: BundleSignature{Status: SignatureVerified, Signer: "platform@acme.dev", Issuer: "https://idp"}}, StepStatusUnknown, "signed by platform@acme.dev (https://idp), the signer the golden path's own contract declares"},
		{&BundleProvenance{Signature: BundleSignature{Status: SignatureUnverified}}, StepStatusFailed, "failed verification"},
		{&BundleProvenance{Signature: BundleSignature{Status: SignatureUnsigned}}, StepStatusUnknown, "the golden path is unsigned"},
		{&BundleProvenance{Signature: BundleSignature{Status: SignatureUnknown, Reason: "registry unreachable"}}, StepStatusUnknown, "registry unreachable"},
		{&BundleProvenance{Signature: BundleSignature{Status: SignatureUnknown}}, StepStatusUnknown, "was not checked"},
		{nil, StepStatusUnknown, "no source that read this revision checks signatures"},
	}
	for _, tt := range tests {
		got := pathSignatureStep(&ContractRevision{Provenance: tt.sig})
		if got.Step != StepSignature || got.Status != tt.status || !strings.Contains(got.Detail, tt.detail) || got.Status == StepStatusDone {
			t.Errorf("%+v: step = %+v", tt.sig, got)
		}
	}
}

func TestCreationChecklistSteps(t *testing.T) {
	ready := []ProvisioningStatus{{Phase: "Ready"}}
	pg := EntityRef{Label: "postgresql"}
	tests := []struct {
		name string
		got  CreationStep
		want string
	}{
		{"not validated", validationStep(&ContractRevision{Version: "0.1.0"}), StepStatusUnknown},
		{"valid", validationStep(&ContractRevision{Version: "0.1.0", validated: true, Valid: true}), StepStatusDone},
		{"invalid", validationStep(&ContractRevision{Version: "0.1.0", validated: true}), StepStatusFailed},
		{"valid, unverified ref", validationStep(&ContractRevision{Version: "0.1.0", validated: true, Valid: true, Validation: []finding.Finding{{Code: "CONFIG_REF_UNVERIFIED"}}}), StepStatusUnknown},
		{"valid, not enforced", validationStep(&ContractRevision{Version: "0.1.0", validated: true, Valid: true, Validation: []finding.Finding{{Code: "X_NOT_ENFORCED"}}}), StepStatusUnknown},
		{"valid, other warning", validationStep(&ContractRevision{Version: "0.1.0", validated: true, Valid: true, Validation: []finding.Finding{{Code: "SOME_WARNING"}}}), StepStatusDone},
		{"published by resolved ref", publishedStep([]*ContractRevision{{ResolvedRef: "oci://r/x@sha256:1"}}), StepStatusDone},
		{"local only", publishedStep([]*ContractRevision{{RequestedRef: "file:///x"}}), StepStatusUnknown},
		{"one target", deployedStep([]*TargetRecord{{}}), StepStatusDone},
		{"two targets", deployedStep([]*TargetRecord{{}, {}}), StepStatusDone},
		{"no target", deployedStep(nil), StepStatusUnknown},
		{"nothing consumed", provisioningStep(nil), StepStatusNotApplicable},
		{"policy only", provisioningStep([]OfferingUse{{Via: RelationshipPolicyRef}}), StepStatusNotApplicable},
		{"all ready", provisioningStep([]OfferingUse{{Provisioning: ready}, {Provisioning: ready}}), StepStatusDone},
		{"one failed", provisioningStep([]OfferingUse{{Provisioning: ready}, {Provider: pg, Provisioning: []ProvisioningStatus{{Phase: "Failed"}}}}), StepStatusFailed},
		{"no report", provisioningStep([]OfferingUse{{Provisioning: ready}, {Provisioning: []ProvisioningStatus{{Phase: StatusUnknown}}}}), StepStatusUnknown},
	}
	for _, tt := range tests {
		if tt.got.Status != tt.want || tt.got.Detail == "" {
			t.Errorf("%s: %+v, want %s", tt.name, tt.got, tt.want)
		}
	}
	if d := validationStep(&ContractRevision{Validation: []finding.Finding{{Code: "CONFIG_REF_UNVERIFIED"}}, validated: true, Valid: true}).Detail; !strings.Contains(d, "CONFIG_REF_UNVERIFIED") {
		t.Errorf("unverified validation detail = %q", d)
	}
	if d := deployedStep([]*TargetRecord{{}, {}}).Detail; d != "2 operational targets" {
		t.Errorf("deployed detail = %q", d)
	}
	// A status nothing reported is no report: the detail never counts it as one.
	if d := provisioningStep([]OfferingUse{{Provisioning: []ProvisioningStatus{{Phase: StatusUnknown}}}}).Detail; d != "no provider has reported provisioning for what the service consumes" {
		t.Errorf("unreported detail = %q", d)
	}
	if d := provisioningStep([]OfferingUse{{Provisioning: []ProvisioningStatus{{Phase: "Provisioning", ReportedPhase: "Provisioning"}, {Phase: StatusUnknown}}}}).Detail; d != "2 of 2 provisioning statuses are not Ready" {
		t.Errorf("reported detail = %q", d)
	}
	if d := provisioningStep([]OfferingUse{{Provider: pg, Provisioning: []ProvisioningStatus{{Phase: "Failed"}}}}).Detail; !strings.Contains(d, "postgresql") {
		t.Errorf("failed detail = %q", d)
	}
}

func TestCreationChecklistOnlyForCreatedServices(t *testing.T) {
	q := goldenFleet(t)
	for _, name := range []string{"http-service", "orders", "postgresql"} {
		if c := goldenDetail(t, q, name).Creation; c != nil {
			t.Errorf("%s has a creation checklist: %+v", name, c)
		}
	}
	if goldenDetail(t, q, "gift-cards").Creation == nil {
		t.Error("a created service has no creation checklist")
	}
}

// TestGoldenPathDependencies: the generic Dependencies preview and the summary
// of a golden path read its templateEdges and still add up.
func TestGoldenPathDependencies(t *testing.T) {
	q := buildDomainSnap(t, "oci", &Collection{Revisions: []RawRevision{
		ociRev(acme, pluginPath(), pathFiles), postgres(), ociRev(acme, usesPostgres("orders", "1.0.0"), nil),
	}})
	view, err := q.GetService(string(NewServiceKeyDomain(acme, "http-service")))
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Dependencies) != 2 {
		t.Errorf("golden path dependencies = %+v", view.Dependencies)
	}
	d := goldenDetail(t, q, "http-service")
	labels := map[string]bool{}
	for _, r := range d.Dependencies.Items {
		labels[r.Label] = true
	}
	if !labels["postgresql"] || !labels["orders"] || d.Summary.DeclaredDependencies != 2 {
		t.Errorf("dependencies preview = %+v, declared %d", d.Dependencies.Items, d.Summary.DeclaredDependencies)
	}
}
