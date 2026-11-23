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

// A nil query is no snapshot or an unchecked caller, never a hidden cluster.
func TestInstallationNilQueryHidesDeclared(t *testing.T) {
	assertHiddenDeclared(t, (*Query)(nil).Installation(declaredInput("hardened")), installationNoQuery)
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
	assertDeliveryNull(t, installationFleet(t).Installation(InstallationInput{DeliveryHidden: true}).Delivery, installationDeliveryHidden)
}

func TestInstallationDeliveryNotProbedIsNull(t *testing.T) {
	assertDeliveryNull(t, installationFleet(t).Installation(InstallationInput{DeliveryWired: true}).Delivery, installationNotProbed)
}

// managerRow is an argocd or flux row counting one object of its applying
// kind, or a kubernetes-workloads row.
func managerRow(id string, status SourceStatus) Provider {
	kind := map[string]string{ProviderArgoCD: "Application", ProviderFlux: "Kustomization"}[id]
	return Provider{ID: id, Name: id, Evidence: id, Status: status, Counts: []ProviderCount{{Kind: kind, Namespace: "ns", N: 1}}}
}

// gitopsReadRow is the row of a workload source that discovered GitOps.
var gitopsReadRow = managerRow(ProviderKubernetesWorkloads, SourceAvailable)

func deliveryProbed(installed bool) InstallationInput {
	return InstallationInput{DeliveryWired: true, DeliveryProbed: true, DeliveryInstalled: installed}
}

func TestInstallationAppliersOnlyWhenEnabled(t *testing.T) {
	q := installationFleet(t, gitopsReadRow, managerRow(ProviderArgoCD, SourceAvailable), managerRow(ProviderFlux, SourceAvailable))
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
}

// In-process delivery answers for itself and still reads the appliers.
func TestInstallationInProcessDelivery(t *testing.T) {
	q := installationFleet(t, gitopsReadRow, managerRow(ProviderArgoCD, SourceAvailable), managerRow(ProviderFlux, SourceAvailable))
	in := deliveryProbed(true)
	in.DeliveryInProcess = true
	if d := (*Query)(nil).Installation(in).Delivery; d.Source != installationInProcess+installationAppliersNoQuery || len(d.Appliers) != 0 {
		t.Errorf("in-process delivery = %+v", d)
	}
	if d := q.Installation(in).Delivery; d.Source != installationInProcess || len(d.Appliers) != 2 {
		t.Errorf("in-process delivery with a snapshot = %+v", d)
	}
}

func TestInstallationUnavailableRowIsNoApplier(t *testing.T) {
	d := installationFleet(t, gitopsReadRow, managerRow(ProviderFlux, SourceUnavailable)).Installation(deliveryProbed(true)).Delivery
	if len(d.Appliers) != 0 || d.Completeness != InstallationPartial || !strings.Contains(d.Source, "flux row is not available") {
		t.Errorf("delivery = %+v", d)
	}
}

func TestInstallationPartialRowIsNoApplier(t *testing.T) {
	d := installationFleet(t, gitopsReadRow, managerRow(ProviderArgoCD, SourcePartial), managerRow(ProviderFlux, SourceAvailable)).Installation(deliveryProbed(true)).Delivery
	if len(d.Appliers) != 1 || d.Appliers[0].Manager != ProviderFlux || d.Completeness != InstallationPartial || !strings.Contains(d.Source, "argocd row") ||
		strings.Contains(d.Source, "no workload source") {
		t.Errorf("delivery = %+v", d)
	}
}

// Each cluster's detection has its own argocd row; Argo CD is listed once.
func TestInstallationAppliersListEachManagerOnce(t *testing.T) {
	src := func(id string) Source {
		return NewMemorySource(id, SourceKindDetection, &Collection{Advisory: true, Providers: []Provider{gitopsReadRow, managerRow(ProviderArgoCD, SourceAvailable)}, State: &SourceState{Status: SourceAvailable}})
	}
	d := NewQuery(buildProviders(t, src("detection-1"), src("detection-2"))).Installation(deliveryProbed(true)).Delivery
	if len(d.Appliers) != 1 || d.Appliers[0].Manager != ProviderArgoCD {
		t.Errorf("appliers = %+v", d.Appliers)
	}
	flux := func(id string) Source {
		return NewMemorySource(id, SourceKindDetection, &Collection{Advisory: true, Providers: []Provider{gitopsReadRow, managerRow(ProviderFlux, SourcePartial)}, State: &SourceState{Status: SourceAvailable}})
	}
	d = NewQuery(buildProviders(t, flux("detection-1"), flux("detection-2"))).Installation(deliveryProbed(true)).Delivery
	if strings.Count(d.Source, "flux row is not available") != 1 {
		t.Errorf("source = %q", d.Source)
	}
}

// An unread input is never a confident empty list: no snapshot, no workload
// source or a failed GitOps discovery leaves the appliers partial.
func TestInstallationAppliersNotReadArePartial(t *testing.T) {
	undiscovered := gitopsReadRow
	undiscovered.GitOpsUndiscovered = true
	for name, c := range map[string]struct {
		q    *Query
		note string
	}{
		"nil query":    {nil, installationAppliersNoQuery},
		"no workloads": {installationFleet(t), installationNoWorkloads},
		"undiscovered": {installationFleet(t, undiscovered), installationUndiscovered},
	} {
		d := c.q.Installation(deliveryProbed(true)).Delivery
		if d.Enabled == nil || !*d.Enabled || len(d.Appliers) != 0 || d.Completeness != InstallationPartial ||
			d.Source != installationProbed+c.note {
			t.Errorf("%s: delivery = %+v", name, d)
		}
	}
}

// A served CRD with no Application applies nothing.
func TestInstallationZeroCountRowIsNoApplier(t *testing.T) {
	empty := managerRow(ProviderArgoCD, SourceAvailable)
	empty.Counts = []ProviderCount{{Kind: "Application"}}
	repos := managerRow(ProviderFlux, SourceAvailable)
	repos.Counts = []ProviderCount{{Kind: "GitRepository", Namespace: "ns", N: 3}}
	d := installationFleet(t, gitopsReadRow, empty, repos).Installation(deliveryProbed(true)).Delivery
	if len(d.Appliers) != 0 || d.Completeness != InstallationComplete {
		t.Errorf("delivery = %+v", d)
	}
}

// With the declared cluster known, only its rows name appliers: Flux in
// uid-1 is not the applier of delivery in uid-2.
func TestInstallationAppliersScopedToDeclaredCluster(t *testing.T) {
	q := NewQuery(buildProviders(t,
		NewMemorySource("k8s-1", SourceKindKubernetes, &Collection{Cluster: clusterOf("uid-1", "staging", ClusterNameFromContext), Targets: []RawTarget{workload("team-a", "web")},
			Providers: []Provider{gitopsReadRow, managerRow(ProviderFlux, SourceAvailable)}}),
		NewMemorySource("k8s-2", SourceKindKubernetes, &Collection{Cluster: clusterOf("uid-2", "prod-eu", ClusterNameFromContext), Targets: []RawTarget{workload("shop", "api")},
			Providers: []Provider{gitopsReadRow, managerRow(ProviderArgoCD, SourceAvailable)}}),
		detectedSource(SourceAvailable, Provider{ID: "installation", Name: "Installation", Evidence: "profile idp", Status: SourceAvailable, Provenance: ProvenanceDeclared, Cluster: "prod-eu", ClusterID: "uid-2"})))
	in := declaredInput("idp")
	in.DeliveryWired, in.DeliveryProbed, in.DeliveryInstalled = true, true, true
	d := q.Installation(in).Delivery
	if len(d.Appliers) != 1 || d.Appliers[0].Manager != ProviderArgoCD || d.Completeness != InstallationComplete {
		t.Errorf("delivery = %+v", d)
	}
}

// The delivery CRDs outlive a chart that dropped delivery: a served API beside
// a declared hardened profile is partial and lists no applier.
func TestInstallationHardenedDeliveryIsPartial(t *testing.T) {
	in := declaredInput("hardened", "idp")
	in.DeliveryWired, in.DeliveryProbed, in.DeliveryInstalled = true, true, true
	d := installationFleet(t, gitopsReadRow, managerRow(ProviderArgoCD, SourceAvailable)).Installation(in).Delivery
	if d.Enabled == nil || !*d.Enabled || len(d.Appliers) != 0 || d.Completeness != InstallationPartial || d.Source != installationProbed+installationDeliveryKept {
		t.Errorf("delivery = %+v", d)
	}
}

// delivery.asOf is when the probe last asked the cluster, not the request.
func TestInstallationDeliveryAsOfFromProbe(t *testing.T) {
	in := deliveryProbed(false)
	in.Now, in.DeliveryAsOf = fixedNow(), fixedNow().Add(-50*time.Second)
	if d := installationFleet(t).Installation(in).Delivery; d.AsOf == nil || !d.AsOf.Equal(in.DeliveryAsOf) {
		t.Errorf("delivery = %+v", d)
	}
	in.DeliveryAsOf = time.Time{}
	if d := installationFleet(t).Installation(in).Delivery; d.AsOf == nil || !d.AsOf.Equal(fixedNow()) {
		t.Errorf("delivery = %+v", d)
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

// TestInstallationPendingFromJoins: the join requests the host read are
// counted beside the joined clusters, with where they were read.
func TestInstallationPendingFromJoins(t *testing.T) {
	n, at := 1, fixedNow()
	in := InstallationInput{JoinPending: &n, JoinSource: "registry ghcr.io/acme/pacto-targets", JoinAsOf: &at}
	c := installationFleet(t).Installation(in).Clusters
	if c.Pending == nil || *c.Pending != 1 || c.Joined == nil || c.Completeness != InstallationComplete ||
		c.Source != installationClustersInView+"; cluster join requests: registry ghcr.io/acme/pacto-targets" {
		t.Errorf("clusters = %+v", c)
	}
	if c := (*Query)(nil).Installation(in).Clusters; c.Pending == nil || c.Joined != nil || c.AsOf == nil || !c.AsOf.Equal(at) {
		t.Errorf("clusters without a snapshot = %+v", c)
	}
}

// TestInstallationPendingNullWhenPartial: a join count the host could not
// give is null with why, and the part is partial.
func TestInstallationPendingNullWhenPartial(t *testing.T) {
	in := InstallationInput{JoinSource: "registry ghcr.io/acme/pacto-targets was read partly, so pending clusters are not counted"}
	c := installationFleet(t).Installation(in).Clusters
	if c.Pending != nil || c.Completeness != InstallationPartial || !strings.HasSuffix(c.Source, in.JoinSource) {
		t.Errorf("clusters = %+v", c)
	}
}

func TestInstallationNilQuery(t *testing.T) {
	got := (*Query)(nil).Installation(InstallationInput{})
	if got.Clusters.Joined != nil || got.Clusters.Completeness != InstallationUnknown || got.Profile.Value != nil || got.Registry.Value != nil ||
		got.Clusters.Source != installationNoQuery+installationJoinsNotRead {
		t.Errorf("installation = %+v", got)
	}
}

func TestInstallationMCPFromHost(t *testing.T) {
	m := (*Query)(nil).Installation(InstallationInput{MCPServed: true, MCPSource: "this MCP server, over stdio", Now: fixedNow()}).MCP
	if !m.Served || m.Provenance != InstallationProvenanceHost || m.Source != "this MCP server, over stdio" || m.AsOf == nil || !m.AsOf.Equal(fixedNow()) {
		t.Errorf("mcp = %+v", m)
	}
}

// idpFluxInstallation is the delivery part where Flux applies workloads in
// uid-2, whose operator declared profile.
func idpFluxInstallation(t *testing.T, profile ...string) InstallationDelivery {
	t.Helper()
	q := NewQuery(buildProviders(t,
		NewMemorySource("k8s-2", SourceKindKubernetes, &Collection{Cluster: clusterOf("uid-2", "prod-eu", ClusterNameFromContext), Targets: []RawTarget{workload("shop", "api")},
			Providers: []Provider{gitopsReadRow, managerRow(ProviderFlux, SourceAvailable)}}),
		detectedSource(SourceAvailable, Provider{ID: "installation", Name: "Installation", Evidence: "profile", Status: SourceAvailable, Provenance: ProvenanceDeclared, Cluster: "prod-eu", ClusterID: "uid-2"})))
	in := declaredInput(profile...)
	in.DeliveryWired, in.DeliveryProbed, in.DeliveryInstalled = true, true, true
	return q.Installation(in).Delivery
}

// Under profile idp the Flux row is the Flux IDP mode delivers through.
func TestInstallationAppliersIDPFlux(t *testing.T) {
	d := idpFluxInstallation(t, "idp")
	if len(d.Appliers) != 1 || d.Appliers[0].Mode != InstallationApplierIDPFlux ||
		d.Appliers[0].Source != "detected flux row; profile idp declared; "+installationApplierEvidence {
		t.Errorf("appliers = %+v", d.Appliers)
	}
}

func TestInstallationAppliersAdoptedOutsideIDP(t *testing.T) {
	for _, profile := range [][]string{{"default"}, nil} {
		d := idpFluxInstallation(t, profile...)
		if len(d.Appliers) != 1 || d.Appliers[0].Mode != InstallationApplierAdopted || d.Appliers[0].Source != "detected flux row; "+installationApplierEvidence {
			t.Errorf("%v: appliers = %+v", profile, d.Appliers)
		}
	}
}
