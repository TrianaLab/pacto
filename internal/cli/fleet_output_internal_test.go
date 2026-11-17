package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/evidence"
	"github.com/trianalab/pacto/v4/pkg/finding"
	"github.com/trianalab/pacto/v4/pkg/fleet"
)

// printFleetTarget must label the revision link with how it was matched, so an
// exact (immutable-digest) link is distinguishable from an inferred, merely
// correlated mutable one (review section S15). The unlabelled/unlinked case is
// covered by TestFleetGetTarget's targets, which carry no revision link.
func TestPrintFleetTarget_LabelsRevisionMatch(t *testing.T) {
	run := func(match string) string {
		cmd := &cobra.Command{}
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		tv := &fleet.TargetView{Target: &fleet.TargetRecord{
			Key: "prod/k8s/x", Service: "x", Compliance: "Compliant",
			ContractRevision: "x@sha256:1", RevisionMatch: match,
		}}
		if err := printFleetTarget(cmd, tv, "text"); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	if out := run("inferred"); !strings.Contains(out, "Revision: x@sha256:1 (inferred)") {
		t.Errorf("inferred link must be labelled:\n%s", out)
	}
	if out := run("exact"); !strings.Contains(out, "Revision: x@sha256:1 (exact)") {
		t.Errorf("exact link must be labelled:\n%s", out)
	}
}

// A finding names the distinct collectors that saw it, and says nothing when none is recorded.
func TestSeenBy(t *testing.T) {
	refs := []finding.EvidenceRef{{Source: "k8s", Collector: "k8s-observer+ebpf"}, {Source: "k8s"}, {Source: "k8s", Collector: "k8s-observer+ebpf"}, {Source: "e", Collector: "pacto-run"}}
	if got := seenBy(refs); got != " (seen by k8s-observer+ebpf, pacto-run)" {
		t.Errorf("seenBy = %q", got)
	}
	if got := seenBy([]finding.EvidenceRef{{Source: "k8s"}}); got != "" {
		t.Errorf("seenBy without collector = %q", got)
	}
}

// The graph answer lists nodes breadth-first; the text view indents by depth,
// so it prints them in path order to keep each node under the parent it was
// reached through.
func TestPrintFleetGraph_IndentsUnderShortestPathParent(t *testing.T) {
	res := &fleet.GraphResult{Root: "r", Direction: fleet.DirectionDependencies, Nodes: []fleet.GraphNode{
		{Key: "a", Name: "a", Depth: 1, Path: []fleet.ServiceKey{"r", "a"}, Provenance: "declared"},
		{Key: "z", Name: "z", Depth: 1, Path: []fleet.ServiceKey{"r", "z"}, Provenance: "observed"},
		{Key: "b", Name: "b", Depth: 2, Path: []fleet.ServiceKey{"r", "a", "b"}, Provenance: "declared+observed"},
		{Key: "t", Name: "t", Depth: 2, Path: []fleet.ServiceKey{"r", "z", "t"}, Provenance: "declared"},
	}, Truncated: true}
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := printFleetGraph(cmd, res, "text"); err != nil {
		t.Fatal(err)
	}
	want := "dependencies of r (4 node(s)):\n" +
		"    a (depth 1, declared)\n" +
		"      b (depth 2, declared+observed)\n" +
		"    z (depth 1, observed)\n" +
		"      t (depth 2, declared)\n" +
		"  truncated: the answer stops at 4 nodes\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
	if res.Nodes[1].Key != "z" {
		t.Error("printing must not reorder the caller's answer")
	}
}

// A search row names the business domain and the offering, a dash when empty.
func TestPrintFleetSearch_RowsNameDomainAndOffering(t *testing.T) {
	res := &fleet.SearchResult{Total: 2, Count: 2, Services: []fleet.ServiceHit{
		{Name: "pay", Status: "Compliant", BusinessDomain: "money", Offering: "payments-api", RevisionCount: 1},
		{Name: "lone"},
	}}
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := printFleetSearch(cmd, res, "text"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"domain=money", "offering=payments-api", "domain=-", "offering=-"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("search text lacks %q:\n%s", want, out.String())
		}
	}
}

// The status header says how many items the limit kept out of how many matched.
func TestPrintFleetStatus_HeaderReportsTotal(t *testing.T) {
	res := &fleet.StatusResult{Total: 5, Truncated: true, Items: []fleet.StatusItem{{Kind: "service", Name: "a", Code: "UNOWNED_SERVICE"}}}
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := printFleetStatus(cmd, res, "text"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "1 of 5 attention item(s):\n") {
		t.Errorf("status header:\n%s", out.String())
	}
}

// The offering text prints each provider's consume snippet, indented under it.
func TestPrintOfferingText_ShowsSnippet(t *testing.T) {
	var buf bytes.Buffer
	printOfferingText(&buf, &fleet.OfferingDetailData{Providers: fleet.OfferingProvidersPreview{Items: []fleet.OfferingProvider{{
		Service: fleet.EntityRef{Label: "pg"},
		Consume: &fleet.ConsumeSnippet{YAML: "dependencies:\n  - name: pg\n"},
	}}}})
	if want := "    To consume pg:\n      dependencies:\n        - name: pg\n"; !strings.Contains(buf.String(), want) {
		t.Errorf("offering text lacks the snippet %q:\n%s", want, buf.String())
	}
}

// TestOfferingTextEdges covers what the CLI fixtures cannot reach cheaply: a
// latest compatible version past the preview, truncated previews, a running
// version, a full lifecycle and hidden consumers.
func TestOfferingTextEdges(t *testing.T) {
	inUse := true
	rev := func(k string) fleet.EntityRef {
		return fleet.EntityRef{Kind: fleet.KindRevision, Key: k, Label: "pg " + k}
	}
	old := rev("old")
	versions := fleet.ProviderVersionsPreview{
		Total: 3, Count: 1, Truncated: true, Range: "^1", LatestCompatible: &old,
		Items: []fleet.ProviderVersion{{Revision: rev("new"), Version: "2.0.0", InUse: &inUse, RangeStatus: fleet.RangeUnsatisfied,
			Lifecycle: &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Sunset: "2027-01-01", Successor: "https://x"}}},
	}
	if got := latestCompatible(versions); got != "pg old" {
		t.Errorf("latest compatible past the preview = %q", got)
	}
	if got := latestCompatible(fleet.ProviderVersionsPreview{}); got != "none" {
		t.Errorf("no latest compatible = %q", got)
	}
	if got := versionNotes(versions.Items[0]); got != " [in use, deprecated since 2026-01-01, sunset 2027-01-01, successor https://x, unsatisfied]" {
		t.Errorf("version notes = %q", got)
	}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	res := &fleet.OfferingVersionsResult{Total: 30, Truncated: true, Providers: []fleet.OfferingProviderVersions{{Provider: fleet.EntityRef{Label: "pg"}, Versions: versions}}}
	if err := printFleetVersions(cmd, res, "text"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pg: latest compatible pg old\n", "  2 more\n", "1 of 30 providers shown\n"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("versions text lacks %q:\n%s", want, buf.String())
		}
	}

	buf.Reset()
	printOfferingText(&buf, &fleet.OfferingDetailData{
		Providers:       fleet.OfferingProvidersPreview{Items: []fleet.OfferingProvider{{Service: fleet.EntityRef{Label: "pg"}, Lifecycle: versions.Items[0].Lifecycle}}},
		Consumers:       fleet.OfferingUsesPreview{Total: 2, Count: 1, Truncated: true, Items: []fleet.OfferingUse{{Consumer: fleet.EntityRef{Label: "app"}, Provider: fleet.EntityRef{Label: "pg"}, Offering: "pg", Via: "dependency", ProviderLifecycle: versions.Items[0].Lifecycle}}},
		HiddenConsumers: 3,
	})
	for _, want := range []string{"    lifecycle: deprecated since 2026-01-01", "    versions: -\n", "via dependency, provider deprecated since", "  1 more\n", "Consumers you may not see: 3\n"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("offering text lacks %q:\n%s", want, buf.String())
		}
	}
}

// TestPrintTemplateText_Branches: a verified signature names its signer,
// unresolved, truncated and hidden created services are listed, and a golden
// path with no pinned reference gives the reason instead of a command.
func TestPrintTemplateText_Branches(t *testing.T) {
	var b bytes.Buffer
	printTemplateText(&b, "", &fleet.TemplateDetail{
		Signature:              &fleet.BundleSignature{Status: fleet.SignatureVerified, Signer: "ci@acme"},
		SignatureNote:          "self-declared",
		UnresolvedRequirements: fleet.UnresolvedDependenciesPreview{Total: 1, Items: []fleet.UnresolvedDependency{{Ref: "oci://r/redis", Reason: "no provider"}}},
		CreatedServices:        fleet.RefPreview{Total: 3, Count: 1, Truncated: true, Items: []fleet.EntityRef{{Label: "gift-cards"}}},
		HiddenCreatedServices:  2,
		HiddenRequirements:     1,
		InstantiateReason:      "no digest-pinned reference",
	})
	for _, want := range []string{
		"Golden path: -\n", "Signature: verified, signed by ci@acme\n  self-declared\n",
		"Unresolved requirements:\n  oci://r/redis: no provider\nHidden requirements: 1 on services you may not see\n",
		"Created services:\n  gift-cards\n  2 more\n  2 created services you may not see\n",
		"Create a service: no digest-pinned reference\n",
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("template text lacks %q:\n%s", want, b.String())
		}
	}
	if r := signatureReason(fleet.BundleSignature{Status: fleet.SignatureUnsigned}); r != "" {
		t.Errorf("a check with no reason adds nothing, got %q", r)
	}
	if r := signatureReason(fleet.BundleSignature{Status: fleet.SignatureUnknown, Reason: "registry unreachable"}); r != ": registry unreachable" {
		t.Errorf("a check with a reason names it, got %q", r)
	}
	b.Reset()
	printTemplateText(&b, "x", &fleet.TemplateDetail{})
	if !strings.Contains(b.String(), "Signature: not checked: no source that read it checks signatures\n") {
		t.Errorf("an unchecked signature says so:\n%s", b.String())
	}
	b.Reset()
	printTemplateText(&b, "x", nil)
	printOriginText(&b, nil, nil)
	if b.Len() != 0 {
		t.Errorf("no template and no origin print nothing: %q", b.String())
	}
}

func TestPrintOriginText_Unresolved(t *testing.T) {
	var b bytes.Buffer
	printOriginText(&b, &fleet.OriginDetail{Ref: "oci://r/p:1", Digest: "sha256:x", Provenance: "declared", GoldenPath: &fleet.EntityRef{Label: "p"}}, nil)
	printOriginText(&b, &fleet.OriginDetail{Ref: "oci://r/q:1", Digest: "sha256:y", Provenance: "declared", Reason: "not in this view"}, nil)
	if !strings.Contains(b.String(), "  golden path: p\n") || !strings.Contains(b.String(), "  golden path: not in this view\n") {
		t.Errorf("origin text:\n%s", b.String())
	}
}

func TestPrintTallies(t *testing.T) {
	var buf bytes.Buffer
	printOfferingUses(&buf, "Consumers:", fleet.OfferingUsesPreview{Total: 2, Range: fleet.RangeTally{Satisfied: 1, None: 1}, Phases: fleet.PhaseTally{Ready: 1, Failed: 2}, PhaseConsumers: fleet.PhaseTally{Ready: 1, Failed: 1}})
	for _, want := range []string{"  range of all 2: 1 satisfied, 0 unsatisfied, 0 unknown, 1 not checked\n", "  provisioning requests: 0 requested, 0 provisioning, 1 ready, 2 failed, 0 deleting, 0 not reported\n", "  consumers by phase: 0 requested, 0 provisioning, 1 ready, 1 failed, 0 deleting, 0 not reported\n"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("uses text lacks %q:\n%s", want, buf.String())
		}
	}
	buf.Reset()
	printDependents(&buf, &fleet.ServiceDetailData{})
	if buf.Len() != 0 {
		t.Errorf("no dependents printed %q", buf.String())
	}
	printDependents(&buf, &fleet.ServiceDetailData{Dependents: fleet.RefPreview{Total: 3}, HiddenDependents: 2, DependentsStatus: fleet.ComplianceTally{Compliant: 1, NonCompliant: 1, Invalid: 1}})
	if want := "Dependents: 3 (1 compliant, 1 non-compliant, 0 unknown, 1 other)\n2 dependents you may not see\n"; buf.String() != want {
		t.Errorf("dependents text = %q, want %q", buf.String(), want)
	}
}

func TestPrintRevisionPlace(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	rev := &fleet.RevisionDetailData{
		Position: 1, Revisions: 4, Current: true, ServiceTargets: 11, PactoVersion: "2.3", PactoVersionCurrent: "2.5",
		ExactTargets: fleet.RefPreview{Total: 6}, InferredTargets: fleet.RefPreview{Total: 2},
		EvidenceSourceCounts: fleet.SourceCounts{Total: 3, Available: 2, Stale: 1},
	}
	if err := printFleetEntity(cmd, &fleet.EntityDetail{Entity: fleet.EntityRef{Kind: fleet.KindRevision}, Revision: rev}, "text"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Position: 1 of 4 revisions, newest first, current\n",
		"Pacto version: 2.3 (behind 2.5)\n",
		"Targets: 8 of the service's 11 run this revision (6 exact, 2 inferred)\n",
		"Evidence sources: 3 (2 available, 0 partial, 1 stale, 0 unavailable)\n",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("revision text lacks %q:\n%s", want, buf.String())
		}
	}
	buf.Reset()
	printRevisionPlace(&buf, &fleet.RevisionDetailData{Position: 2, Revisions: 4})
	if !strings.Contains(buf.String(), "Position: 2 of 4 revisions, newest first\n") {
		t.Errorf("a revision that is not current says so: %q", buf.String())
	}
	if strings.Contains(buf.String(), "Pacto version") {
		t.Errorf("a revision with no pactoVersion prints none: %q", buf.String())
	}
}

func TestPrintNonCompliantRanking(t *testing.T) {
	var buf bytes.Buffer
	printNonCompliantRanking(&buf, &fleet.EntityAggregate{})
	if buf.Len() != 0 {
		t.Errorf("no non-compliant service printed %q", buf.String())
	}
	printNonCompliantRanking(&buf, &fleet.EntityAggregate{
		ServiceCompliance:         fleet.ComplianceTally{NonCompliant: 9},
		NonCompliantByOwner:       []fleet.OwnerCount{{Key: "team:a", Services: 10, NonCompliant: 3}},
		NonCompliantBeyondRanking: 4, NonCompliantUnidentified: 2,
		NonCompliantByDomain: []fleet.DomainCount{{Domain: "pay", Services: 5, NonCompliant: 5}},
		NonCompliantNoDomain: 3, NonCompliantDomainUnknown: 1,
	})
	want := "Non-compliant services by owner (9): team:a 3 of 10, 4 with other owners, 2 with no identified owner\n" +
		"Non-compliant services by domain (9): pay 5 of 5, 3 with no domain, 1 whose domain is unknown\n"
	if buf.String() != want {
		t.Errorf("ranking text = %q, want %q", buf.String(), want)
	}
}

func TestPrintOriginRanking(t *testing.T) {
	var buf bytes.Buffer
	printOriginRanking(&buf, &fleet.OriginTally{None: fleet.OriginCount{Services: 4}})
	if buf.Len() != 0 {
		t.Errorf("a fleet with no origin printed %q", buf.String())
	}
	printOriginRanking(&buf, &fleet.OriginTally{
		ByPath: []fleet.PathCount{{Key: "acme/http", Services: 5, NonCompliant: 1, Hidden: 2}},
		Beyond: fleet.OriginCount{Services: 3}, Unresolved: fleet.OriginCount{Services: 1}, None: fleet.OriginCount{Services: 7}, Hidden: 2,
	})
	want := "Services by golden path: acme/http 5 (1 non-compliant), 3 from other golden paths, 1 from an origin that resolves to no golden path, 7 with no declared origin\n" +
		"Plus 2 created from a golden path and hidden by your permissions\n"
	if buf.String() != want {
		t.Errorf("origin text = %q, want %q", buf.String(), want)
	}
}

// resourcesFixture is a service with one configuration-reference use reported
// Failed by a signed adapter and one dependency use nothing reported on.
func resourcesFixture() *fleet.ServiceDetailData {
	asOf := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	consumption := []fleet.ResourceConsumption{{Environment: "production", State: "not-reported", Provenance: "observed"}}
	return &fleet.ServiceDetailData{
		HiddenProviders: 2,
		ConsumedOfferings: fleet.OfferingUsesPreview{Total: 2, Count: 2, Items: []fleet.OfferingUse{
			{
				Offering: "postgresql", Provider: fleet.EntityRef{Label: "postgresql"}, Via: "configuration_reference",
				ProviderOwner:        &fleet.OwnershipInfo{Owner: "infra-data", Declared: true},
				ProviderAvailability: &fleet.Availability{State: fleet.AvailabilityDegraded},
				Requested: []fleet.RequestedConfiguration{{
					Entry: "database", Configuration: "provisioning", Provenance: "declared",
					Revision: &fleet.EntityRef{Label: "postgresql 16.1.0", ParentService: "postgresql", Version: "16.1.0"},
					Values:   fleet.RuntimePreview{Items: []fleet.RuntimeFact{{Key: "size", Value: "medium"}, {Key: "storageGi", Value: "50"}}},
				}},
				Provisioning: []fleet.ProvisioningStatus{{
					Environment: "production", Entry: "database", Phase: "Failed", Provider: "platform-db",
					ExternalRef: "databases/catalog", Source: "evidence", Verified: true, AsOf: &asOf,
				}},
				Consumption: consumption,
			},
			{
				Provider: fleet.EntityRef{Label: "email-provider"}, Via: "dependency", RangeStatus: "satisfied",
				Provisioning: []fleet.ProvisioningStatus{{Environment: "production", Phase: "Unknown", ReportedPhase: "Ready", Source: "target-state", AsOf: &asOf, Stale: true, Reason: "the last report is older than the provisioning window"}},
				Consumption:  consumption,
			},
		}},
	}
}

func printedResources(d *fleet.ServiceDetailData) string {
	var b bytes.Buffer
	printResources(&b, d)
	return b.String()
}

func TestPrintResourcesProvenance(t *testing.T) {
	out := printedResources(resourcesFixture())
	for _, want := range []string{
		"    production database  Failed  platform-db databases/catalog  signed  asOf 2026-11-01T09:00:00Z\n",
		"    production  Unknown (reported Ready)  unsigned  asOf 2026-11-01T09:00:00Z: the last report is older than the provisioning window\n",
		"    owner infra-data, availability degraded\n",
		"    owner unknown, availability unknown\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("resources lack %q:\n%s", want, out)
		}
	}
}

func TestPrintResourcesRequestedDeclared(t *testing.T) {
	out := printedResources(resourcesFixture())
	want := "    requested (declared): size=medium storageGi=50, provider postgresql@16.1.0, configuration provisioning\n"
	if strings.Count(out, "requested (declared)") != 1 || !strings.Contains(out, want) {
		t.Errorf("resources lack %q once:\n%s", want, out)
	}
	d := resourcesFixture()
	d.ConsumedOfferings.Items[0].Requested[0] = fleet.RequestedConfiguration{Entry: "database", Provenance: "declared"}
	d.ConsumedOfferings.Items[0].RequestedTruncated = true
	out = printedResources(d)
	if !strings.Contains(out, "    requested (declared): none\n") || !strings.Contains(out, "    requested values may be missing: the configurations list was cut\n") {
		t.Errorf("bare request:\n%s", out)
	}
}

func TestPrintResourcesNoOffering(t *testing.T) {
	out := printedResources(resourcesFixture())
	for _, want := range []string{"Resources:\n  postgresql (offering postgresql) via configuration_reference\n", "  email-provider (no offering) via dependency, range satisfied\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("resources lack %q:\n%s", want, out)
		}
	}
}

func TestPrintResourcesHiddenProviders(t *testing.T) {
	if out := printedResources(resourcesFixture()); !strings.Contains(out, "  at least 2 resources from providers you may not see\n") {
		t.Errorf("hidden providers:\n%s", out)
	}
	// Only hidden uses still print the block: nothing visible never reads as none.
	out := printedResources(&fleet.ServiceDetailData{HiddenProviders: 1})
	if !strings.HasPrefix(out, "Resources:\n  at least 1 resources from providers you may not see\n") {
		t.Errorf("only hidden:\n%s", out)
	}
	if out := printedResources(&fleet.ServiceDetailData{}); out != "" {
		t.Errorf("no resources printed %q", out)
	}
}

func TestPrintResourcesConsumptionNotReported(t *testing.T) {
	out := printedResources(resourcesFixture())
	want := "    usage production: not reported by any provider; for live signals ask Mira (query_metric)\n"
	if strings.Count(out, want) != 2 {
		t.Errorf("resources lack %q twice:\n%s", want, out)
	}
	if got := "    " + (fleet.ResourceConsumption{State: "unknown", Reason: "stale"}).Text(); got != "    usage: unknown: stale" {
		t.Errorf("other state = %q", got)
	}
}

// usageAt is the time every usage fixture is as of.
var usageAt = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

func usageFigures() *fleet.UsageFigures {
	return &fleet.UsageFigures{Metrics: []evidence.UsageMetric{{Name: "storage-used", Value: 31, Unit: "Gi"}, {Name: "connections", Value: 12.5}},
		Cost: &evidence.UsageCost{Amount: "212.40", Currency: "USD", Period: "P30D"}}
}

func TestPrintResourcesUsageReported(t *testing.T) {
	c := fleet.ResourceConsumption{Environment: "production", State: "reported", Figures: usageFigures(), Producer: "platform-db", Verified: true, AsOf: &usageAt}
	want := "    usage production: storage-used 31 Gi  connections 12.5  cost 212.40 USD/P30D  platform-db  signed  asOf 2026-11-01T09:00:00Z"
	if got := "    " + c.Text(); got != want {
		t.Errorf("reported =\n%q, want\n%q", got, want)
	}
}

func TestPrintResourcesUsageUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    fleet.ResourceConsumption
		want string
	}{
		{"unsigned", fleet.ResourceConsumption{State: "unknown", Reason: "the usage report is not signed", Reported: usageFigures(), Source: "target-state", AsOf: &usageAt},
			"    usage: unknown: the usage report is not signed (reported storage-used 31 Gi  connections 12.5  cost 212.40 USD/P30D, unsigned, asOf 2026-11-01T09:00:00Z)"},
		{"stale signed", fleet.ResourceConsumption{Environment: "staging", State: "unknown", Reason: "old", Reported: &fleet.UsageFigures{Cost: &evidence.UsageCost{Amount: "1", Currency: "EUR", Period: "P1M"}}, Source: "evidence", Verified: true},
			"    usage staging: unknown: old (reported cost 1 EUR/P1M, signed)"},
		{"conflict", fleet.ResourceConsumption{State: "unknown", Reason: "disagree", Conflict: []string{"a", "b"}},
			"    usage: unknown: disagree (a, b)"},
	} {
		if got := "    " + tc.c.Text(); got != tc.want {
			t.Errorf("%s =\n%q, want\n%q", tc.name, got, tc.want)
		}
	}
}

func TestPrintResourcesFooter(t *testing.T) {
	out := printedResources(resourcesFixture())
	const footer = "Resize or delete runs in the provider's tools. pacto_edit refuses a request the provider's schema rejects, and pacto validate and push fail on it."
	if !strings.HasSuffix(out, "  "+footer+"\n") || fleet.ResourceActionsNote != footer {
		t.Errorf("footer:\n%s", out)
	}
}

// TestPrintOfferingAdapters: signed and unsigned adapters, an unnamed one, a
// disagreement, the bound, conflicting and unattributed requests, the empty
// state, unresolved uses and the unsigned tallies.
func TestPrintOfferingAdapters(t *testing.T) {
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	printOfferingText(&buf, &fleet.OfferingDetailData{
		Consumers: fleet.OfferingUsesPreview{Total: 1, Count: 1, Items: []fleet.OfferingUse{{Consumer: fleet.EntityRef{Label: "app"}, Provider: fleet.EntityRef{Label: "scan"}}},
			Phases: fleet.PhaseTally{Ready: 3, ReadyUnsigned: 2}, PhaseConsumers: fleet.PhaseTally{Ready: 2, ReadyUnsigned: 1}},
		Adapters: fleet.OfferingAdaptersPreview{Total: 4, Count: 3, Truncated: true, Conflicting: 1, Unattributed: 2, Items: []fleet.OfferingAdapter{
			{Provider: "scanner", Producer: "security-adapter", Verified: true, Environments: []string{"production"}, Phases: fleet.PhaseTally{Ready: 1}, LastReportedAt: &at},
			{Provider: "scanner", Producer: "security-adapter", Environments: []string{"default", "staging"}, Phases: fleet.PhaseTally{Ready: 1, ReadyUnsigned: 1, Unknown: 1}, Disagrees: 1},
			{Producer: "platform-adapter", Environments: []string{"default"}, Phases: fleet.PhaseTally{Unknown: 1}},
		}},
		UnresolvedUses: fleet.UnresolvedUses{Count: 2, Reason: "not pinned", Provenance: fleet.ProvenanceInferred},
	})
	for _, want := range []string{
		"Adapters (observed):\n",
		"  scanner by security-adapter, signed: production; 1 ready; last reported 2026-11-01T10:00:00Z\n",
		"  scanner by security-adapter, unsigned target-state file: default, staging; 1 ready (1 unsigned), 1 unknown, disagrees on 1\n",
		"  unnamed adapter by platform-adapter, unsigned target-state file: default; 1 unknown\n",
		"  1 more\n", "  1 requests where adapters disagree, so their status is Unknown\n", "  2 requests no adapter reports on\n",
		"Unresolved uses (inferred): 2 may name a provider of this offering (not pinned)\n",
		"  provisioning requests: 0 requested, 0 provisioning, 3 ready (2 unsigned), 0 failed",
		"  consumers by phase: 0 requested, 0 provisioning, 2 ready (1 unsigned), 0 failed",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("offering text lacks %q:\n%s", want, buf.String())
		}
	}
	buf.Reset()
	printOfferingText(&buf, &fleet.OfferingDetailData{})
	if out := buf.String(); !strings.Contains(out, "Adapters (observed):\n  No adapter has reported. Provisioning status is Unknown.\n") || strings.Contains(out, "Unresolved uses") {
		t.Errorf("empty adapters text:\n%s", out)
	}
	buf.Reset()
	printOfferingText(&buf, &fleet.OfferingDetailData{HiddenConsumers: 1})
	if out := buf.String(); !strings.Contains(out, "  "+fleet.NoAdapterVisible+"\n") {
		t.Errorf("empty adapters text with hidden consumers:\n%s", out)
	}
}

// TestOverviewPrinterEdges: a bucket with targets is named, a named domain
// prints its name and an overview with no domain prints no table.
func TestOverviewPrinterEdges(t *testing.T) {
	if got := countSuffix(2, "warning"); got != " · warning 2" {
		t.Errorf("countSuffix = %q", got)
	}
	if got := domainLabel(fleet.DomainTally{Domain: "shop"}); got != "shop" {
		t.Errorf("domainLabel = %q", got)
	}
	var buf bytes.Buffer
	if printOverviewDomains(&buf, nil); buf.Len() != 0 {
		t.Errorf("no domain prints a table: %q", buf.String())
	}
}

// An inferred name keeps its source in text output, as in JSON and the dashboard.
func TestPrintFleet_MarksInferredName(t *testing.T) {
	inf := &contract.Inferred{Field: "service.name", Value: "payments", Source: "chart/Chart.yaml:2"}
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	res := &fleet.SearchResult{Total: 2, Count: 2, Services: []fleet.ServiceHit{{Name: "payments", NameInferred: inf}, {Name: "orders"}}}
	if err := printFleetSearch(cmd, res, "text"); err != nil {
		t.Fatal(err)
	}
	if err := printFleetService(cmd, &fleet.ServiceView{Service: &fleet.ServiceRecord{Name: "payments", NameInferred: inf}}, "text"); err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(out.String()) {
		if strings.Contains(line, "orders") && strings.Contains(line, "inferred") {
			t.Errorf("declared row marked: %q", line)
		}
	}
	for _, want := range []string{"payments", "Service: payments (name inferred from chart/Chart.yaml:2)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Count(out.String(), "(name inferred from chart/Chart.yaml:2)") != 2 {
		t.Errorf("want the mark on the search row and the Service line:\n%s", out.String())
	}
}

// TestPrintExternalFoldsAndHides covers the lines a plain external lacks: more
// declarations than listed, the other-egress node, a folded identity and
// hidden consumers.
func TestPrintExternalFoldsAndHides(t *testing.T) {
	var b strings.Builder
	printExternal(&b, fleet.ExternalRecord{Key: fleet.OtherEgress, Class: fleet.ClassOtherEgress, SourcesTotal: 3, Folded: 4, HiddenConsumers: 2}, "", fleet.ExternalEvidence{})
	printExternal(&b, fleet.ExternalRecord{Key: "x", Catalog: &fleet.ExternalCatalogMatch{Origin: "organization", Digest: "short", Entry: "x"},
		Sources: []fleet.ExternalSource{{Kind: fleet.ExternalSourceContract, Service: "payments", Index: 2, Dependency: "x", Superseded: true}}}, fleet.OtherEgress, fleet.ExternalEvidence{})
	for _, want := range []string{"payments pacto.yaml dependencies[2] (name: x, not in the newest revision)", "3 sources in all, across every consumer revision", "folds    4 external systems", "hidden consumers 2", "folded into other-egress", "catalog  organization short entry x"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("lacks %q:\n%s", want, b.String())
		}
	}
	if strings.Contains(b.String(), "joined by name") {
		t.Errorf("the other-egress node reads as joined by name:\n%s", b.String())
	}
}

func TestFleetEntityExternalPrintsInferredSources(t *testing.T) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	ext := &fleet.ExternalDetailData{
		External: fleet.ExternalRecord{Key: "stripe", Class: "vendor-api", HiddenConsumers: 2, Sources: []fleet.ExternalSource{
			{Kind: fleet.ExternalSourceContract, Service: "pay", Index: 1, Dependency: "stripe-api", Provenance: fleet.ProvenanceDeclared},
			{Kind: fleet.ExternalSourcePodSpec, Service: "checkout", Provenance: fleet.ProvenanceInferred},
		}},
		Catalog: &fleet.ExternalCatalogRef{Origin: "default", Digest: "sha256:3e9a"},
		ExternalEvidence: fleet.ExternalEvidence{
			InferredSources: []fleet.ExternalInferredSource{
				{Service: "checkout", InferredSource: fleet.InferredSource{Kind: "env", Where: "deployment/checkout env STRIPE_API_BASE", Host: "api.stripe.com", Confidence: "likely", Entry: "stripe"}},
				{Service: "checkout", InferredSource: fleet.InferredSource{Kind: "secretKeyRef", Where: "deployment/checkout secretKeyRef stripe/KEY", Confidence: "possible"}},
			},
			InferredSourcesTotal: 5,
			ObservedSources:      []fleet.ExternalObservedSource{{Service: "checkout", ObservedSourceStat: fleet.ObservedSourceStat{Source: "otel", Count: 4}}},
			ObservedSourcesTotal: 1,
		},
	}
	if err := printFleetEntity(cmd, &fleet.EntityDetail{Entity: fleet.EntityRef{Kind: fleet.KindExternal, Key: "stripe"}, External: ext}, "text"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"stripe  external, vendor-api  (used by 0 consumers)\n",
		"  sources  pay pacto.yaml dependencies[1] (name: stripe-api)\n",
		"           checkout env deployment/checkout env STRIPE_API_BASE (inferred, likely)\n             -> api.stripe.com (host and port only)\n",
		"           checkout secretKeyRef deployment/checkout secretKeyRef stripe/KEY (inferred, possible)\n",
		"           checkout otel (observed, 4 calls)\n           3 more inferred sources\n",
		"  catalog  none (",
		"  hidden consumers 2\n",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("external text lacks %q:\n%s", want, buf.String())
		}
	}
	if strings.Contains(buf.String(), "pod-spec") {
		t.Errorf("a pod-spec source prints through its inferred evidence only:\n%s", buf.String())
	}
}

// A target-only owner says where it comes from; a declared one prints bare.
func TestFleetGetTextOwnerProvenance(t *testing.T) {
	for prov, want := range map[string]string{
		fleet.OwnerProvenanceDeclaredViaTarget: "Owner: pay (declared via target)\n",
		fleet.OwnerProvenanceDeclared:          "Owner: pay\n",
		"":                                     "Owner: pay\n",
	} {
		cmd := &cobra.Command{}
		var out bytes.Buffer
		cmd.SetOut(&out)
		s := &fleet.ServiceRecord{Name: "payments", Owner: contract.Owner{Team: "pay"}, OwnerProvenance: prov}
		if err := printFleetService(cmd, &fleet.ServiceView{Service: s}, "text"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), want) {
			t.Errorf("%q: want %q in:\n%s", prov, want, out.String())
		}
	}
}
