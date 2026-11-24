package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var offeringsAsOf = time.Date(2026, 11, 23, 10, 5, 0, 0, time.UTC)

// offeringsSnapshot holds postgresql, a declared offering of two providers
// (one accepted from an inferred offering that drifted), and xbucket, an
// inferred offering a producer contributed.
func offeringsSnapshot() *fleet.FleetSnapshot {
	read := offeringsAsOf.Add(-5 * time.Minute)
	return &fleet.FleetSnapshot{
		Services: map[fleet.ServiceKey]*fleet.ServiceRecord{
			"ghcr.io/acme/offerings/postgresql": {Key: "ghcr.io/acme/offerings/postgresql", Offering: "postgresql", Revisions: []fleet.RevisionKey{"pg1", "pg2"},
				OfferingDrift: &fleet.OfferingDrift{State: fleet.DriftDrifted, Reason: "source object changed since accepted (generation 3 -> 4)"}},
			"ghcr.io/acme/offerings/postgresql-eu": {Key: "ghcr.io/acme/offerings/postgresql-eu", Offering: "postgresql"},
			"ghcr.io/acme/pacto-inferred/crossplane-6f1c/services/xbucket": {Key: "ghcr.io/acme/pacto-inferred/crossplane-6f1c/services/xbucket", Offering: "xbucket",
				Contribution: &fleet.ServiceContribution{Producer: "crossplane-6f1c"}, Revisions: []fleet.RevisionKey{"xb2", "xb1", "gone"}},
		},
		Revisions: map[fleet.RevisionKey]*fleet.ContractRevision{
			"pg1": {Version: "16.0.0", Digest: "sha256:1"},
			"pg2": {Version: "16.1.0", Digest: "sha256:2", FetchedAt: &read},
			"xb1": {Version: "0.0.0-inferred.1.0", ResolvedRef: "ghcr.io/acme/pacto-inferred/crossplane-6f1c/services:xbucket_0.0.0-inferred.1.0"},
			"xb2": {Version: "0.0.0-inferred.2.0", ResolvedRef: "oci://ghcr.io/acme/pacto-inferred/crossplane-6f1c/services:xbucket_0.0.0-inferred.2.0"},
		},
	}
}

func offeringsText(t *testing.T, res *fleet.EntityList, inferred bool, detected string) string {
	t.Helper()
	ans := offeringsAnswer(offeringsSnapshot(), res)
	return textOf(t, func(cmd *cobra.Command) error { return printFleetOfferings(cmd, ans, inferred, detected, "text") })
}

func TestFleetOfferingsListsDeclared(t *testing.T) {
	res := &fleet.EntityList{Total: 1, Count: 1, InferredTotal: 1, Meta: fleet.ProductMeta{AsOf: offeringsAsOf, Completeness: fleet.CompletenessComplete},
		Entities: []fleet.EntityRef{{Kind: fleet.KindOffering, Key: "postgresql", Label: "postgresql"}}}
	got := offeringsText(t, res, false, "")
	wantLines(t, got, "OFFERING", "postgresql  declared", "ghcr.io/acme/offerings/postgresql 16.1.0", "2026-11-23T10:00:00Z",
		"drift: source object changed since accepted (generation 3 -> 4)",
		"asOf 2026-11-23T10:05:00Z  completeness complete  (+1 inferred: pacto fleet offerings --inferred)")
	ans := offeringsAnswer(offeringsSnapshot(), res)
	if row := ans.Offerings[0]; len(row.Drift) != 1 || row.InferredOffering != nil || ans.InferredTotal != 1 {
		t.Fatalf("answer = %+v", ans)
	}
}

func TestFleetOfferingsDriftLine(t *testing.T) {
	if got := driftLine(fleet.OfferingDrift{State: fleet.DriftUnknown, Reason: "the producer was not read"}); got != "drift unknown: the producer was not read" {
		t.Fatal(got)
	}
	if got := driftLine(fleet.OfferingDrift{State: fleet.DriftDrifted, Reason: "source object changed since accepted"}); got != "drift: source object changed since accepted" {
		t.Fatal(got)
	}
	// A provider with no revision is named by its key alone.
	snap := offeringsSnapshot()
	if from, at := newestVersion(snap, snap.Services["ghcr.io/acme/offerings/postgresql-eu"]); from != "ghcr.io/acme/offerings/postgresql-eu" || at != nil {
		t.Fatal(from, at)
	}
}

func TestFleetOfferingsInferredListsInferredRows(t *testing.T) {
	gen := offeringsAsOf.Add(-3 * time.Minute)
	row := func(accepted *fleet.EntityRef) fleet.EntityRef {
		return fleet.EntityRef{Kind: fleet.KindService, Key: "ghcr.io/acme/pacto-inferred/crossplane-6f1c/services/xbucket", Label: "xbucket",
			Declaration: fleet.DeclarationInferred, InferredOffering: &fleet.InferredOfferingFacts{Producer: "crossplane-6f1c", Offering: "xbucket",
				From: "CompositeResourceDefinition xbuckets.storage.acme.io", Generation: 2, Classes: []string{"large", "small"}, AsOf: &gen, AcceptedAs: accepted,
				Ref: "oci://ghcr.io/acme/pacto-inferred/crossplane-6f1c/services:xbucket_0.0.0-inferred.2.0"}}
	}
	res := &fleet.EntityList{Total: 1, Count: 1, Meta: fleet.ProductMeta{AsOf: offeringsAsOf, Completeness: fleet.CompletenessComplete},
		Entities: []fleet.EntityRef{row(nil)}}
	got := offeringsText(t, res, true, "k8s:pacto-system/pacto-operator-detected")
	from := "oci://ghcr.io/acme/pacto-inferred/crossplane-6f1c/services:xbucket_0.0.0-inferred.2.0"
	wantLines(t, got, "xbucket   inferred", "CompositeResourceDefinition xbuckets.storage.acme.io gen 2", "large, small", "2026-11-23T10:02:00Z",
		"producer crossplane-6f1c",
		"accept: pacto declare offering:xbucket --from "+from+" --detected k8s:pacto-system/pacto-operator-detected --registry <registry> --version <version> --owner <team>",
		"asOf 2026-11-23T10:05:00Z  completeness complete\n")
	if strings.Contains(got, "(+") {
		t.Errorf("the inferred list points at itself:\n%s", got)
	}

	// Accepted already: the declared offering, and the command without an owner.
	res.Entities = []fleet.EntityRef{row(&fleet.EntityRef{Kind: fleet.KindService, Key: "ghcr.io/acme/offerings/xbucket"})}
	got = offeringsText(t, res, true, "")
	wantLines(t, got, "accepted as ghcr.io/acme/offerings/xbucket; accept again: pacto declare offering:xbucket --from "+from+
		" --detected k8s:<namespace>/<configmap> --registry <registry> --version <version>\n")

	// JSON: one document with the offerings, asOf and completeness.
	out, _, err := runRoot(t, defaultDeps(), nil, "fleet", "offerings", "--inferred", "--local", t.TempDir(), "--output-format", "json")
	doc := decode(t, out)
	if err != nil || doc["offerings"] == nil || doc["asOf"] == nil || doc["completeness"] == nil {
		t.Fatalf("err %v, doc %v", err, doc)
	}
	if _, _, err := runRoot(t, defaultDeps(), nil, "fleet", "offerings", "--local", t.TempDir(), "--offset", "-1"); err == nil {
		t.Fatal("a negative offset was answered")
	}
	if _, _, err := runRoot(t, defaultDeps(), nil, "fleet", "offerings", "--local", t.TempDir(), "--external-catalog", "oci://r.example/c:1"); err == nil {
		t.Fatal("a fleet that cannot be built was answered")
	}
	ans := offeringsAnswer(offeringsSnapshot(), &fleet.EntityList{Entities: []fleet.EntityRef{{Key: "missing", InferredOffering: &fleet.InferredOfferingFacts{}}}})
	if ans.Offerings[0].From != "" {
		t.Fatalf("a contribution with no revision has a ref: %+v", ans.Offerings[0])
	}
}

func TestFleetOfferingsPartialSaysUnknown(t *testing.T) {
	next := 25
	res := &fleet.EntityList{Total: 0, Meta: fleet.ProductMeta{AsOf: offeringsAsOf, Completeness: fleet.CompletenessPartial}}
	got := offeringsText(t, res, true, "")
	wantLines(t, got, "No inferred offerings read: the answer is partial, so whether there are any is unknown")
	res.Meta.Completeness = fleet.CompletenessComplete
	wantLines(t, offeringsText(t, res, false, ""), "No declared offerings\n")
	res = &fleet.EntityList{Total: 30, Count: 1, Truncated: true, NextOffset: &next, Meta: fleet.ProductMeta{AsOf: offeringsAsOf, Completeness: fleet.CompletenessComplete},
		Entities: []fleet.EntityRef{{Kind: fleet.KindOffering, Key: "none", Label: "none"}}}
	wantLines(t, offeringsText(t, res, false, ""), "none      declared    -     -        -", "... 1 of 30 offerings shown")
}
