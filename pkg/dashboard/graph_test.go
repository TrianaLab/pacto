package dashboard

import (
	"testing"
)

func TestExtractServiceNameFromRef_PlainName(t *testing.T) {
	got := extractServiceNameFromRef("my-service")
	if got != "my-service" {
		t.Errorf("expected 'my-service', got %q", got)
	}
}

func TestExtractServiceNameFromRef_OCIRef(t *testing.T) {
	got := extractServiceNameFromRef("oci://ghcr.io/org/my-service:1.0.0")
	if got != "my-service" {
		t.Errorf("expected 'my-service', got %q", got)
	}
}

func TestExtractServiceNameFromRef_NameWithColon(t *testing.T) {
	got := extractServiceNameFromRef("my-service:latest")
	if got != "my-service" {
		t.Errorf("expected 'my-service', got %q", got)
	}
}

func TestExtractServiceNameFromRef_OCIRefNoTag(t *testing.T) {
	got := extractServiceNameFromRef("oci://ghcr.io/org/my-service")
	if got != "my-service" {
		t.Errorf("expected 'my-service', got %q", got)
	}
}

func TestExtractServiceNameFromRef_RegistryPath(t *testing.T) {
	got := extractServiceNameFromRef("ghcr.io/org/my-service:2.0.0")
	if got != "my-service" {
		t.Errorf("expected 'my-service', got %q", got)
	}
}

func TestExtractServiceNameFromRef_Digest(t *testing.T) {
	got := extractServiceNameFromRef("oci://ghcr.io/org/my-service@sha256:abc123def456")
	if got != "my-service" {
		t.Errorf("expected 'my-service', got %q", got)
	}
}

func TestExtractServiceNameFromRef_DigestNoScheme(t *testing.T) {
	got := extractServiceNameFromRef("ghcr.io/org/svc@sha256:deadbeef")
	if got != "svc" {
		t.Errorf("expected 'svc', got %q", got)
	}
}

func TestDepRefMatchesName_Match(t *testing.T) {
	if !depRefMatchesName("oci://ghcr.io/org/my-service:1.0.0", "my-service", nil) {
		t.Error("expected match for OCI ref")
	}
}

func TestDepRefMatchesName_NoMatch(t *testing.T) {
	if depRefMatchesName("oci://ghcr.io/org/other-service:1.0.0", "my-service", nil) {
		t.Error("expected no match")
	}
}

func TestDepRefMatchesName_PlainMatch(t *testing.T) {
	if !depRefMatchesName("my-service", "my-service", nil) {
		t.Error("expected match for plain name")
	}
}

func TestComputeBlastRadius_NoDependents(t *testing.T) {
	index := map[string]*ServiceDetails{
		"svc-a": {Service: Service{Name: "svc-a"}},
		"svc-b": {Service: Service{Name: "svc-b"}},
	}
	got := computeBlastRadius("svc-a", index, nil)
	if got != 0 {
		t.Errorf("expected blast radius 0, got %d", got)
	}
}

func TestComputeBlastRadius_LinearChain(t *testing.T) {
	// Chain: svc-c depends on svc-b, svc-b depends on svc-a (all required)
	// If svc-a breaks, svc-b and svc-c are affected => blast radius = 2
	index := map[string]*ServiceDetails{
		"svc-a": {Service: Service{Name: "svc-a"}},
		"svc-b": {
			Service:      Service{Name: "svc-b"},
			Dependencies: []DependencyInfo{{Ref: "svc-a", Required: true}},
		},
		"svc-c": {
			Service:      Service{Name: "svc-c"},
			Dependencies: []DependencyInfo{{Ref: "svc-b", Required: true}},
		},
	}

	got := computeBlastRadius("svc-a", index, nil)
	if got != 2 {
		t.Errorf("expected blast radius 2 for svc-a, got %d", got)
	}

	got = computeBlastRadius("svc-b", index, nil)
	if got != 1 {
		t.Errorf("expected blast radius 1 for svc-b, got %d", got)
	}

	got = computeBlastRadius("svc-c", index, nil)
	if got != 0 {
		t.Errorf("expected blast radius 0 for svc-c, got %d", got)
	}
}

func TestComputeBlastRadius_DiamondPattern(t *testing.T) {
	// Diamond: svc-b and svc-c both depend on svc-a, svc-d depends on both svc-b and svc-c
	// If svc-a breaks: svc-b, svc-c, and svc-d are all affected => 3
	index := map[string]*ServiceDetails{
		"svc-a": {Service: Service{Name: "svc-a"}},
		"svc-b": {
			Service:      Service{Name: "svc-b"},
			Dependencies: []DependencyInfo{{Ref: "svc-a", Required: true}},
		},
		"svc-c": {
			Service:      Service{Name: "svc-c"},
			Dependencies: []DependencyInfo{{Ref: "svc-a", Required: true}},
		},
		"svc-d": {
			Service: Service{Name: "svc-d"},
			Dependencies: []DependencyInfo{
				{Ref: "svc-b", Required: true},
				{Ref: "svc-c", Required: true},
			},
		},
	}

	got := computeBlastRadius("svc-a", index, nil)
	if got != 3 {
		t.Errorf("expected blast radius 3 for svc-a, got %d", got)
	}
}

func TestComputeBlastRadius_IgnoresOptionalDeps(t *testing.T) {
	// svc-b depends on svc-a but NOT required => blast radius should be 0
	index := map[string]*ServiceDetails{
		"svc-a": {Service: Service{Name: "svc-a"}},
		"svc-b": {
			Service:      Service{Name: "svc-b"},
			Dependencies: []DependencyInfo{{Ref: "svc-a", Required: false}},
		},
	}
	got := computeBlastRadius("svc-a", index, nil)
	if got != 0 {
		t.Errorf("expected blast radius 0 (optional dep), got %d", got)
	}
}

func TestComputeBlastRadius_NilDetails(t *testing.T) {
	index := map[string]*ServiceDetails{
		"svc-a": nil,
		"svc-b": {
			Service:      Service{Name: "svc-b"},
			Dependencies: []DependencyInfo{{Ref: "svc-a", Required: true}},
		},
	}
	// Should not panic with nil details
	got := computeBlastRadius("svc-a", index, nil)
	if got != 1 {
		t.Errorf("expected blast radius 1, got %d", got)
	}
}

func TestBuildGlobalGraph_BasicServices(t *testing.T) {
	services := []Service{
		{Name: "svc-a", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
		{Name: "svc-b", Version: "2.0.0", Phase: PhaseHealthy, Source: "local"},
	}
	index := map[string]*ServiceDetails{
		"svc-a": {
			Service:      Service{Name: "svc-a", Version: "1.0.0"},
			Dependencies: []DependencyInfo{{Ref: "svc-b", Required: true, Compatibility: "^2.0.0"}},
		},
		"svc-b": {
			Service: Service{Name: "svc-b", Version: "2.0.0"},
		},
	}

	graph := buildGlobalGraph(services, index)
	if graph == nil {
		t.Fatal("expected non-nil graph")
	}

	// Should have 2 nodes (svc-a and svc-b), no external nodes since svc-b is resolved
	if len(graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(graph.Nodes))
	}

	// Find svc-a node and check edges
	var svcA *GraphNodeData
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == "svc-a" {
			svcA = &graph.Nodes[i]
			break
		}
	}
	if svcA == nil {
		t.Fatal("svc-a node not found")
	}
	if len(svcA.Edges) != 1 {
		t.Fatalf("expected 1 edge on svc-a, got %d", len(svcA.Edges))
	}
	if svcA.Edges[0].TargetID != "svc-b" {
		t.Errorf("expected edge target 'svc-b', got %q", svcA.Edges[0].TargetID)
	}
	if !svcA.Edges[0].Resolved {
		t.Error("expected edge to be resolved")
	}
	if !svcA.Edges[0].Required {
		t.Error("expected edge to be required")
	}
	if svcA.Edges[0].Compatibility != "^2.0.0" {
		t.Errorf("expected compatibility '^2.0.0', got %q", svcA.Edges[0].Compatibility)
	}
}

func TestBuildGlobalGraph_ExternalNode(t *testing.T) {
	services := []Service{
		{Name: "svc-a", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
	}
	index := map[string]*ServiceDetails{
		"svc-a": {
			Service:      Service{Name: "svc-a"},
			Dependencies: []DependencyInfo{{Ref: "oci://ghcr.io/org/external-svc:1.0.0", Required: true}},
		},
	}

	graph := buildGlobalGraph(services, index)

	// Should have 2 nodes: svc-a + external node for external-svc
	if len(graph.Nodes) != 2 {
		t.Fatalf("expected 2 nodes (1 service + 1 external), got %d", len(graph.Nodes))
	}

	// Find the external node
	var externalNode *GraphNodeData
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == "external-svc" {
			externalNode = &graph.Nodes[i]
			break
		}
	}
	if externalNode == nil {
		t.Fatal("external node not found")
	}
	if externalNode.Status != "external" {
		t.Errorf("expected status 'external', got %q", externalNode.Status)
	}
}

func TestBuildGlobalGraph_ReferenceEdges(t *testing.T) {
	services := []Service{
		{Name: "svc-a", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
		{Name: "config-svc", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
	}
	index := map[string]*ServiceDetails{
		"svc-a": {
			Service:       Service{Name: "svc-a"},
			Configuration: &ConfigurationInfo{Ref: "config-svc"},
			Policy:        &PolicyInfo{Ref: "policy-svc"},
		},
		"config-svc": {
			Service: Service{Name: "config-svc"},
		},
	}

	graph := buildGlobalGraph(services, index)

	// Find svc-a node
	var svcA *GraphNodeData
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == "svc-a" {
			svcA = &graph.Nodes[i]
			break
		}
	}
	if svcA == nil {
		t.Fatal("svc-a not found")
	}

	// Should have 2 reference edges: config-svc (resolved) and policy-svc (unresolved)
	if len(svcA.Edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(svcA.Edges))
	}

	refTypeCount := 0
	for _, e := range svcA.Edges {
		if e.Type == "reference" {
			refTypeCount++
		}
	}
	if refTypeCount != 2 {
		t.Errorf("expected 2 reference edges, got %d", refTypeCount)
	}
}

func TestBuildGlobalGraph_SkipsSelfRefs(t *testing.T) {
	services := []Service{
		{Name: "svc-a", Version: "1.0.0", Source: "local"},
	}
	index := map[string]*ServiceDetails{
		"svc-a": {
			Service:       Service{Name: "svc-a"},
			Configuration: &ConfigurationInfo{Ref: "svc-a"}, // self-reference
		},
	}

	graph := buildGlobalGraph(services, index)

	var svcA *GraphNodeData
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == "svc-a" {
			svcA = &graph.Nodes[i]
			break
		}
	}
	if svcA == nil {
		t.Fatal("svc-a not found")
	}
	if len(svcA.Edges) != 0 {
		t.Errorf("expected 0 edges (self-ref skipped), got %d", len(svcA.Edges))
	}
}

func TestBuildGlobalGraph_EmptyRefSkipped(t *testing.T) {
	services := []Service{
		{Name: "svc-a", Version: "1.0.0", Source: "local"},
	}
	index := map[string]*ServiceDetails{
		"svc-a": {
			Service:       Service{Name: "svc-a"},
			Configuration: &ConfigurationInfo{Ref: ""},
			Policy:        &PolicyInfo{Ref: ""},
		},
	}

	graph := buildGlobalGraph(services, index)
	var svcA *GraphNodeData
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == "svc-a" {
			svcA = &graph.Nodes[i]
			break
		}
	}
	if svcA == nil {
		t.Fatal("svc-a not found")
	}
	if len(svcA.Edges) != 0 {
		t.Errorf("expected 0 edges (empty refs), got %d", len(svcA.Edges))
	}
}

func TestBuildGraph_BasicTree(t *testing.T) {
	index := map[string]*ServiceDetails{
		"root": {
			Service:      Service{Name: "root", Version: "1.0.0"},
			Dependencies: []DependencyInfo{{Ref: "child", Required: true}},
		},
		"child": {
			Service: Service{Name: "child", Version: "2.0.0"},
		},
	}

	graph := buildGraph(index["root"], index)
	if graph == nil {
		t.Fatal("expected non-nil graph")
	}
	if graph.Root == nil {
		t.Fatal("expected non-nil root")
	}
	if graph.Root.Name != "root" {
		t.Errorf("expected root name 'root', got %q", graph.Root.Name)
	}
	if len(graph.Root.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(graph.Root.Dependencies))
	}
	dep := graph.Root.Dependencies[0]
	if dep.Node == nil {
		t.Fatal("expected resolved node")
	}
	if dep.Node.Name != "child" {
		t.Errorf("expected child name 'child', got %q", dep.Node.Name)
	}
}

func TestBuildGraph_UnresolvedDep(t *testing.T) {
	index := map[string]*ServiceDetails{
		"root": {
			Service:      Service{Name: "root", Version: "1.0.0"},
			Dependencies: []DependencyInfo{{Ref: "missing", Required: true}},
		},
	}

	graph := buildGraph(index["root"], index)
	if len(graph.Root.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(graph.Root.Dependencies))
	}
	dep := graph.Root.Dependencies[0]
	if dep.Error != "not resolved" {
		t.Errorf("expected error 'not resolved', got %q", dep.Error)
	}
	if dep.Node != nil {
		t.Error("expected nil node for unresolved dep")
	}
}

func TestBuildGraph_CyclePrevention(t *testing.T) {
	// Cycle: a -> b -> a
	index := map[string]*ServiceDetails{
		"a": {
			Service:      Service{Name: "a"},
			Dependencies: []DependencyInfo{{Ref: "b", Required: true}},
		},
		"b": {
			Service:      Service{Name: "b"},
			Dependencies: []DependencyInfo{{Ref: "a", Required: true}},
		},
	}

	graph := buildGraph(index["a"], index)
	if graph.Root == nil {
		t.Fatal("expected non-nil root")
	}
	// a -> b -> a (visited, so a won't recurse)
	if len(graph.Root.Dependencies) != 1 {
		t.Fatalf("expected 1 dep on root, got %d", len(graph.Root.Dependencies))
	}
	bNode := graph.Root.Dependencies[0].Node
	if bNode == nil {
		t.Fatal("expected b node")
	}
	// b should have dep on a, but a is already visited so no further recursion
	if len(bNode.Dependencies) != 1 {
		t.Fatalf("expected 1 dep on b, got %d", len(bNode.Dependencies))
	}
	aNode := bNode.Dependencies[0].Node
	if aNode == nil {
		t.Fatal("expected a node (visited)")
	}
	// The visited 'a' should have no dependencies (cycle stopped)
	if len(aNode.Dependencies) != 0 {
		t.Errorf("expected 0 deps on revisited a, got %d", len(aNode.Dependencies))
	}
}

func TestBuildGraph_NilRoot(t *testing.T) {
	graph := buildGraph(nil, nil)
	if graph.Root != nil {
		t.Error("expected nil root for nil service")
	}
}

func TestBuildRefAliases_Empty(t *testing.T) {
	aliases := buildRefAliases(nil)
	if len(aliases) != 0 {
		t.Errorf("expected 0 aliases, got %d", len(aliases))
	}
}

func TestBuildRefAliases_WithImageAndChart(t *testing.T) {
	index := map[string]*ServiceDetails{
		"my-svc": {
			Service:  Service{Name: "my-svc"},
			ImageRef: "ghcr.io/org/my-svc-image:1.0.0",
			ChartRef: "oci://ghcr.io/org/my-svc-chart:1.0.0",
		},
		"nil-svc": nil,
	}
	aliases := buildRefAliases(index)
	// "my-svc-image" -> "my-svc" and "my-svc-chart" -> "my-svc"
	if aliases["my-svc-image"] != "my-svc" {
		t.Errorf("expected alias 'my-svc-image' -> 'my-svc', got %q", aliases["my-svc-image"])
	}
	if aliases["my-svc-chart"] != "my-svc" {
		t.Errorf("expected alias 'my-svc-chart' -> 'my-svc', got %q", aliases["my-svc-chart"])
	}
}

func TestBuildRefAliases_SameNameNoAlias(t *testing.T) {
	index := map[string]*ServiceDetails{
		"api": {
			Service:  Service{Name: "api"},
			ImageRef: "ghcr.io/org/api:1.0.0", // extractServiceNameFromRef returns "api" == name
		},
	}
	aliases := buildRefAliases(index)
	if len(aliases) != 0 {
		t.Errorf("expected 0 aliases when ref name equals service name, got %d", len(aliases))
	}
}

func TestResolveServiceName_DirectMatch(t *testing.T) {
	index := map[string]*ServiceDetails{
		"my-svc": {Service: Service{Name: "my-svc"}},
	}
	got := resolveServiceName("my-svc", index, nil)
	if got != "my-svc" {
		t.Errorf("expected 'my-svc', got %q", got)
	}
}

func TestResolveServiceName_ViaAlias(t *testing.T) {
	index := map[string]*ServiceDetails{
		"my-svc": {Service: Service{Name: "my-svc"}},
	}
	aliases := map[string]string{"my-svc-image": "my-svc"}
	got := resolveServiceName("my-svc-image", index, aliases)
	if got != "my-svc" {
		t.Errorf("expected 'my-svc' via alias, got %q", got)
	}
}

func TestResolveServiceName_NoMatch(t *testing.T) {
	index := map[string]*ServiceDetails{}
	got := resolveServiceName("unknown", index, nil)
	if got != "unknown" {
		t.Errorf("expected 'unknown' (passthrough), got %q", got)
	}
}

func TestResolveServiceName_PactoSuffix(t *testing.T) {
	index := map[string]*ServiceDetails{
		"payment-gateway": {Service: Service{Name: "payment-gateway"}},
	}
	got := resolveServiceName("payment-gateway-pacto", index, nil)
	if got != "payment-gateway" {
		t.Errorf("expected 'payment-gateway' via -pacto suffix strip, got %q", got)
	}
}

func TestResolveServiceName_PactoSuffix_NoMatch(t *testing.T) {
	index := map[string]*ServiceDetails{}
	got := resolveServiceName("unknown-pacto", index, nil)
	if got != "unknown-pacto" {
		t.Errorf("expected 'unknown-pacto' (passthrough), got %q", got)
	}
}

func TestDepRefMatchesName_WithAlias(t *testing.T) {
	aliases := map[string]string{"my-svc-image": "my-svc"}
	if !depRefMatchesName("ghcr.io/org/my-svc-image:1.0.0", "my-svc", aliases) {
		t.Error("expected match via alias")
	}
}

func TestDepRefMatchesName_PactoSuffix(t *testing.T) {
	if !depRefMatchesName("oci://ghcr.io/acme/payment-gateway-pacto:1.0.0", "payment-gateway", nil) {
		t.Error("expected match via -pacto suffix stripping")
	}
}

func TestComputeBlastRadius_WithAliases(t *testing.T) {
	// svc-b depends on "ghcr.io/org/svc-a-image:1.0.0" which should resolve to "svc-a" via alias.
	index := map[string]*ServiceDetails{
		"svc-a": {
			Service:  Service{Name: "svc-a"},
			ImageRef: "ghcr.io/org/svc-a-image:1.0.0",
		},
		"svc-b": {
			Service:      Service{Name: "svc-b"},
			Dependencies: []DependencyInfo{{Ref: "ghcr.io/org/svc-a-image:1.0.0", Required: true}},
		},
	}
	aliases := buildRefAliases(index)
	got := computeBlastRadius("svc-a", index, aliases)
	if got != 1 {
		t.Errorf("expected blast radius 1 (svc-b depends on svc-a via alias), got %d", got)
	}
}

func TestBuildGlobalGraph_WithOCIRefAliases(t *testing.T) {
	// Test that buildGlobalGraph correctly resolves OCI ref aliases.
	services := []Service{
		{Name: "svc-a", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
		{Name: "svc-b", Version: "1.0.0", Phase: PhaseHealthy, Source: "local"},
	}
	index := map[string]*ServiceDetails{
		"svc-a": {
			Service:  Service{Name: "svc-a", Version: "1.0.0"},
			ImageRef: "ghcr.io/org/svc-a-img:1.0.0",
		},
		"svc-b": {
			Service:      Service{Name: "svc-b", Version: "1.0.0"},
			Dependencies: []DependencyInfo{{Ref: "ghcr.io/org/svc-a-img:1.0.0", Required: true}},
		},
	}

	graph := buildGlobalGraph(services, index)
	if graph == nil {
		t.Fatal("expected non-nil graph")
	}

	// svc-b should have an edge to svc-a (resolved via alias)
	var svcB *GraphNodeData
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == "svc-b" {
			svcB = &graph.Nodes[i]
			break
		}
	}
	if svcB == nil {
		t.Fatal("svc-b node not found")
	}
	if len(svcB.Edges) != 1 {
		t.Fatalf("expected 1 edge on svc-b, got %d", len(svcB.Edges))
	}
	if svcB.Edges[0].TargetID != "svc-a" {
		t.Errorf("expected edge target 'svc-a' (via alias), got %q", svcB.Edges[0].TargetID)
	}
	if !svcB.Edges[0].Resolved {
		t.Error("expected edge to be resolved via alias")
	}
}

func TestNormalizePhase(t *testing.T) {
	cases := []struct {
		in   Phase
		want Phase
	}{
		{PhaseHealthy, PhaseHealthy},
		{PhaseDegraded, PhaseDegraded},
		{PhaseInvalid, PhaseInvalid},
		{PhaseUnknown, PhaseUnknown},
		{PhaseReference, PhaseReference},
		{"Progressing", PhaseUnknown},
		{"", PhaseUnknown},
	}
	for _, c := range cases {
		got := NormalizePhase(c.in)
		if got != c.want {
			t.Errorf("NormalizePhase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
