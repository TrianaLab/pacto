package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/trianalab/pacto/v4/pkg/contract"
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
	for _, want := range []string{"    lifecycle: deprecated since 2026-01-01", "    versions: -\n", "via dependency, provider deprecated since", "  1 more\n", "3 consumers you may not see\n"} {
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
		InstantiateReason:      "no digest-pinned reference",
	})
	for _, want := range []string{
		"Golden path: -\n", "Signature: verified, signed by ci@acme\n  self-declared\n",
		"Unresolved requirements:\n  oci://r/redis: no provider\n",
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
