package fleet

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// installationFleet: two clusters, uid-1 (team-a) and uid-2 (shop), with the
// operator's Installation row detected in uid-2 and extra rows.
func installationFleet(t *testing.T, rows ...Provider) *Query {
	t.Helper()
	at := fixedNow()
	inst := Provider{ID: "installation", Name: "Installation", Evidence: "profile hardened; registry ghcr.io/acme", Status: SourceAvailable,
		Provenance: ProvenanceDeclared, AsOf: &at, Cluster: "prod-eu", ClusterID: "uid-2"}
	return NewQuery(buildProviders(t,
		NewMemorySource("k8s-1", SourceKindKubernetes, &Collection{Cluster: clusterOf("uid-1", "staging", ClusterNameFromContext), Targets: []RawTarget{workload("team-a", "web")}}),
		NewMemorySource("k8s-2", SourceKindKubernetes, &Collection{Cluster: clusterOf("uid-2", "prod-eu", ClusterNameFromContext), Targets: []RawTarget{workload("shop", "api")}}),
		detectedSource(SourceAvailable, append([]Provider{inst}, rows...)...)))
}

func declaredInput(profile ...string) InstallationInput {
	at := fixedNow().Add(-time.Minute)
	return InstallationInput{
		Declared:      &InstallationDeclared{Profile: profile, Registry: "ghcr.io/acme", ClusterID: "uid-2", AsOf: at, Source: "the operator's installation block in detected.json"},
		DeclaredState: &SourceState{Status: SourceAvailable},
		Now:           fixedNow(),
	}
}

func TestInstallationFromDeclared(t *testing.T) {
	got := installationFleet(t).Installation(declaredInput("hardened"))
	p, r := got.Profile, got.Registry
	if !slices.Equal(p.Value, []string{"hardened"}) || p.Provenance != ProvenanceDeclared || p.Completeness != InstallationComplete ||
		p.AsOf == nil || !p.AsOf.Equal(fixedNow().Add(-time.Minute)) || !strings.Contains(p.Source, "detected.json") {
		t.Errorf("profile = %+v", p)
	}
	if r.Value == nil || *r.Value != "ghcr.io/acme" || r.Provenance != ProvenanceDeclared || !strings.Contains(r.Source, "detected.json") {
		t.Errorf("registry = %+v", r)
	}
}

func TestInstallationStaleDetectionIsPartial(t *testing.T) {
	in := declaredInput("default")
	in.DeclaredState.Status = SourceStale
	if p := installationFleet(t).Installation(in).Profile; p.Completeness != InstallationPartial || !strings.HasSuffix(p.Source, "(stale)") {
		t.Errorf("profile = %+v", p)
	}
}

func assertHiddenDeclared(t *testing.T, got Installation, why string) {
	t.Helper()
	if got.Profile.Value != nil || got.Registry.Value != nil || got.Profile.Completeness != InstallationUnknown || got.Registry.Completeness != InstallationUnknown ||
		got.Profile.Source != why || got.Registry.Source != why {
		t.Errorf("profile %+v registry %+v, want null with %q", got.Profile, got.Registry, why)
	}
}

func TestInstallationHiddenClusterIsNull(t *testing.T) {
	q := installationFleet(t).Restrict(anyCluster(readerOf("team-a")), anyClusterKind(nil))
	assertHiddenDeclared(t, q.Installation(declaredInput("hardened")), installationHidden)
	visible := installationFleet(t).Restrict(anyCluster(readerOf("team-a", "shop")), anyClusterKind(nil))
	if v := visible.Installation(declaredInput("hardened")).Profile.Value; !slices.Equal(v, []string{"hardened"}) {
		t.Errorf("a view holding uid-2 got profile %v", v)
	}
}

func TestInstallationNilQueryHidesDeclared(t *testing.T) {
	assertHiddenDeclared(t, (*Query)(nil).Installation(declaredInput("hardened")), installationHidden)
}

func TestInstallationNoDetectionIsNull(t *testing.T) {
	q := installationFleet(t)
	assertHiddenDeclared(t, q.Installation(InstallationInput{}), installationNoDetected)
	assertHiddenDeclared(t, q.Installation(InstallationInput{DeclaredState: &SourceState{Status: SourceUnavailable,
		Error: &SourceError{Message: "configmap not found"}}}), "the detection could not be read: configmap not found")
	assertHiddenDeclared(t, q.Installation(InstallationInput{DeclaredState: &SourceState{Status: SourceUnavailable}}), "the detection could not be read")
	assertHiddenDeclared(t, q.Installation(InstallationInput{DeclaredState: &SourceState{Status: SourceAvailable}}), installationNoBlock)
}

func TestInstallationProfileUnknownFromOlderOperator(t *testing.T) {
	got := installationFleet(t).Installation(declaredInput())
	if p := got.Profile; p.Value != nil || p.Source != installationNoProfile || p.Completeness != InstallationUnknown {
		t.Errorf("profile = %+v", p)
	}
	if r := got.Registry; r.Value == nil || *r.Value != "ghcr.io/acme" {
		t.Errorf("registry = %+v", r)
	}
}

func TestInstallationRegistryEmptyIsKnown(t *testing.T) {
	in := declaredInput("default")
	in.Declared.Registry = ""
	if r := installationFleet(t).Installation(in).Registry; r.Value == nil || *r.Value != "" || r.Completeness != InstallationComplete {
		t.Errorf("registry = %+v", r)
	}
}

func assertDeliveryNull(t *testing.T, d InstallationDelivery, why string) {
	t.Helper()
	if d.Enabled != nil || d.Source != why || d.Completeness != InstallationUnknown || len(d.Appliers) != 0 || d.Appliers == nil || d.Provenance != ProvenanceObserved {
		t.Errorf("delivery = %+v, want null with %q", d, why)
	}
}

func TestInstallationDeliveryProbeFailedIsNull(t *testing.T) {
	in := InstallationInput{DeliveryWired: true, DeliveryProbed: true, DeliveryInstalled: true, DeliveryErr: errors.New("forbidden")}
	assertDeliveryNull(t, installationFleet(t).Installation(in).Delivery, "the delivery probe failed: forbidden")
}

func TestInstallationNoDeliveryWiringIsNull(t *testing.T) {
	assertDeliveryNull(t, installationFleet(t).Installation(InstallationInput{}).Delivery, installationNoDelivery)
}

func TestInstallationDeliveryNotProbedIsNull(t *testing.T) {
	assertDeliveryNull(t, installationFleet(t).Installation(InstallationInput{DeliveryWired: true}).Delivery, installationNotProbed)
}

func managerRow(id string, status SourceStatus) Provider {
	return Provider{ID: id, Name: id, Evidence: id, Status: status}
}

func deliveryProbed(installed bool) InstallationInput {
	return InstallationInput{DeliveryWired: true, DeliveryProbed: true, DeliveryInstalled: installed}
}

func TestInstallationAppliersOnlyWhenEnabled(t *testing.T) {
	q := installationFleet(t, managerRow(ProviderArgoCD, SourceAvailable), managerRow(ProviderFlux, SourceAvailable))
	off := q.Installation(deliveryProbed(false)).Delivery
	if off.Enabled == nil || *off.Enabled || len(off.Appliers) != 0 || off.Completeness != InstallationComplete {
		t.Errorf("disabled delivery = %+v", off)
	}
	on := q.Installation(deliveryProbed(true)).Delivery
	if on.Enabled == nil || !*on.Enabled || len(on.Appliers) != 2 || on.Source != installationProbed {
		t.Fatalf("enabled delivery = %+v", on)
	}
	for _, a := range on.Appliers {
		if a.Mode != InstallationApplierAdopted || a.Provenance != ProvenanceInferred || !strings.HasPrefix(a.Source, "detected "+a.Manager+" row") {
			t.Errorf("applier = %+v", a)
		}
	}
	in := deliveryProbed(true)
	in.DeliveryInProcess = true
	if d := (*Query)(nil).Installation(in).Delivery; d.Source != installationInProcess || len(d.Appliers) != 0 {
		t.Errorf("in-process delivery = %+v", d)
	}
}

func TestInstallationUnavailableRowIsNoApplier(t *testing.T) {
	d := installationFleet(t, managerRow(ProviderFlux, SourceUnavailable)).Installation(deliveryProbed(true)).Delivery
	if len(d.Appliers) != 0 || d.Completeness != InstallationPartial || !strings.Contains(d.Source, "flux row is not available") {
		t.Errorf("delivery = %+v", d)
	}
}

func TestInstallationPartialRowIsNoApplier(t *testing.T) {
	d := installationFleet(t, managerRow(ProviderArgoCD, SourcePartial), managerRow(ProviderFlux, SourceAvailable)).Installation(deliveryProbed(true)).Delivery
	if len(d.Appliers) != 1 || d.Appliers[0].Manager != ProviderFlux || d.Completeness != InstallationPartial || !strings.Contains(d.Source, "argocd row") {
		t.Errorf("delivery = %+v", d)
	}
}

// Each cluster's detection has its own argocd row; Argo CD is listed once.
func TestInstallationAppliersListEachManagerOnce(t *testing.T) {
	src := func(id string) Source {
		return NewMemorySource(id, SourceKindDetection, &Collection{Advisory: true, Providers: []Provider{managerRow(ProviderArgoCD, SourceAvailable)}, State: &SourceState{Status: SourceAvailable}})
	}
	d := NewQuery(buildProviders(t, src("detection-1"), src("detection-2"))).Installation(deliveryProbed(true)).Delivery
	if len(d.Appliers) != 1 || d.Appliers[0].Manager != ProviderArgoCD {
		t.Errorf("appliers = %+v", d.Appliers)
	}
	flux := func(id string) Source {
		return NewMemorySource(id, SourceKindDetection, &Collection{Advisory: true, Providers: []Provider{managerRow(ProviderFlux, SourcePartial)}, State: &SourceState{Status: SourceAvailable}})
	}
	d = NewQuery(buildProviders(t, flux("detection-1"), flux("detection-2"))).Installation(deliveryProbed(true)).Delivery
	if strings.Count(d.Source, "flux row is not available") != 1 {
		t.Errorf("source = %q", d.Source)
	}
}

func TestInstallationProvisioningUnknownUntilDetected(t *testing.T) {
	row := managerRow("crossplane", SourceAvailable)
	row.UsedFor = []string{UseReporting}
	p := installationFleet(t, row).Installation(InstallationInput{Now: fixedNow()}).Provisioning
	if p.Engine != InstallationEngineUnknown || p.Completeness != InstallationUnknown || p.Source != installationProvisioning || p.AsOf == nil {
		t.Errorf("provisioning = %+v", p)
	}
}

func TestInstallationClustersFromCallerView(t *testing.T) {
	q := installationFleet(t)
	if c := q.Installation(InstallationInput{}).Clusters; c.Joined == nil || *c.Joined != 2 || c.Pending != nil ||
		c.Completeness != InstallationComplete || c.AsOf == nil || !strings.Contains(c.Source, "join requests are not read here") {
		t.Errorf("clusters = %+v", c)
	}
	r := q.Restrict(anyCluster(readerOf("team-a")), anyClusterKind(nil))
	if c := r.Installation(InstallationInput{}).Clusters; c.Joined == nil || *c.Joined != 1 {
		t.Errorf("restricted clusters = %+v", c)
	}
	partial := NewQuery(buildProviders(t,
		NewMemorySource("k8s-1", SourceKindKubernetes, &Collection{Cluster: clusterOf("uid-1", "staging", ClusterNameFromContext), Targets: []RawTarget{workload("team-a", "web")}}),
		NewMemorySource("mirror", "oci", &Collection{State: &SourceState{Status: SourcePartial}})))
	if c := partial.Installation(InstallationInput{}).Clusters; c.Completeness != InstallationPartial {
		t.Errorf("clusters of a partial snapshot = %+v", c)
	}
}

func TestInstallationNilQuery(t *testing.T) {
	got := (*Query)(nil).Installation(InstallationInput{})
	if got.Clusters.Joined != nil || got.Clusters.Completeness != InstallationUnknown || got.Profile.Value != nil || got.Registry.Value != nil {
		t.Errorf("installation = %+v", got)
	}
}

func TestInstallationMCPFromHost(t *testing.T) {
	m := (*Query)(nil).Installation(InstallationInput{MCPServed: true, MCPSource: "this MCP server, over stdio", Now: fixedNow()}).MCP
	if !m.Served || m.Provenance != InstallationProvenanceHost || m.Source != "this MCP server, over stdio" || m.AsOf == nil || !m.AsOf.Equal(fixedNow()) {
		t.Errorf("mcp = %+v", m)
	}
}
