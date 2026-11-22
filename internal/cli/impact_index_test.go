package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/fleet"
	"github.com/trianalab/pacto/v4/pkg/impact"
)

// indexResult is an answer whose consumers came from ghcr.io/acme/pacto-index.
func indexResult(ix impact.ConsumerIndex, lims ...fleet.Limitation) *impact.Result {
	ix.Status, ix.Repository = impact.IndexRead, "ghcr.io/acme/pacto-index"
	return &impact.Result{ConsumerIndex: &ix, Limitations: lims}
}

func TestImpactPrintsConsumersLine(t *testing.T) {
	notIndexed := fleet.Limitation{Code: impact.LimitationConsumerNotIndexed, Message: "1 consumer the fleet holds has no usable organization index entry: web"}
	for name, tc := range map[string]struct {
		res  *impact.Result
		want string
	}{
		"checked": {indexResult(impact.ConsumerIndex{Consumers: 41, Checked: true, Completeness: fleet.CompletenessComplete}),
			"Consumers: 41 from ghcr.io/acme/pacto-index (listed by tag, complete)\n"},
		"unchecked": {indexResult(impact.ConsumerIndex{Consumers: 41, Services: 120, Completeness: fleet.CompletenessPartial}),
			"Consumers: 41 from ghcr.io/acme/pacto-index (listed by tag, 120 services listed, unchecked)\n"},
		"partial": {indexResult(impact.ConsumerIndex{Consumers: 3, Checked: true, Completeness: fleet.CompletenessPartial}, notIndexed),
			"Consumers: 3 from ghcr.io/acme/pacto-index (listed by tag, partial: 1 consumer the fleet holds has no usable organization index entry: web)\n"},
		"fallback": {indexResult(impact.ConsumerIndex{Consumers: 2, Checked: true, Completeness: fleet.CompletenessComplete, Reason: "after ghcr.io/acme/pacto-index: not found"}),
			"Consumers: 2 from ghcr.io/acme/pacto-index (listed by tag, complete, after ghcr.io/acme/pacto-index: not found)\n"},
		"not found": {&impact.Result{ConsumerIndex: &impact.ConsumerIndex{Status: impact.IndexNotFound, Reason: "no organization index at ghcr.io/acme/pacto-index or ghcr.io/pacto-index"}},
			"Consumers: no organization index at ghcr.io/acme/pacto-index or ghcr.io/pacto-index (partial)\n"},
		"unavailable": {&impact.Result{ConsumerIndex: &impact.ConsumerIndex{Status: impact.IndexUnavailable, Repository: "ghcr.io/acme/pacto-index", Reason: "403 Forbidden"}},
			"Consumers: the organization index ghcr.io/acme/pacto-index could not be listed: 403 Forbidden (partial)\n"},
		"skipped": {&impact.Result{ConsumerIndex: &impact.ConsumerIndex{Status: impact.IndexSkipped, Reason: "--root names the consumers"}},
			"Consumers: organization index not read (--root names the consumers)\n"},
		"local pair": {&impact.Result{}, ""},
	} {
		var w bytes.Buffer
		printTargetSnapshots(&w, tc.res)
		if w.String() != tc.want {
			t.Errorf("%s: got %q, want %q", name, w.String(), tc.want)
		}
	}
	// The line follows the target snapshot lines.
	res := indexResult(impact.ConsumerIndex{Checked: true, Completeness: fleet.CompletenessComplete})
	res.TargetSnapshots = []fleet.SourceState{{ID: "targets", Status: fleet.SourceUnavailable}}
	var w bytes.Buffer
	printTargetSnapshots(&w, res)
	if !strings.HasPrefix(w.String(), "Targets: ") || !strings.HasSuffix(w.String(), "(listed by tag, complete)\n") {
		t.Fatalf("got %q", w.String())
	}
}

func TestImpactConsumersLineNeverSaysCompleteWhenPartial(t *testing.T) {
	for _, res := range []*impact.Result{
		indexResult(impact.ConsumerIndex{Consumers: 1, Services: 4, Completeness: fleet.CompletenessComplete}),
		indexResult(impact.ConsumerIndex{Consumers: 1, Checked: true, Completeness: fleet.CompletenessPartial},
			fleet.Limitation{Code: "CONSUMER_INDEX_ENTRY_INVALID", Source: "index", Message: "1 index entries not used: x"}),
		indexResult(impact.ConsumerIndex{Checked: true, Completeness: fleet.CompletenessPartial}),
	} {
		var w bytes.Buffer
		printTargetSnapshots(&w, res)
		if strings.Contains(w.String(), "complete") {
			t.Errorf("%+v reads complete: %q", *res.ConsumerIndex, w.String())
		}
	}
}

func TestImpactPrintsNoConsumersLineForLocalPair(t *testing.T) {
	statsOff(t)
	old, cur := t.TempDir(), t.TempDir()
	testutil.WriteOrders(t, old, "1.0.0", "service")
	testutil.WriteOrders(t, cur, "2.0.0", "job")
	out, _, _ := runRoot(t, defaultDeps(), nil, "impact", old, cur, "--local", t.TempDir())
	if !strings.Contains(out, "Impact: orders") || strings.Contains(out, "Consumers:") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestImpactIndexFlag(t *testing.T) {
	for _, v := range []string{"oci://ghcr.io/acme/pacto-index", app.IndexNone} {
		cmd := &cobra.Command{Use: "x"}
		addFleetSourceFlags(cmd.Flags())
		if err := cmd.Flags().Set("index", v); err != nil {
			t.Fatal(err)
		}
		if got := fleetOptions(cmd).Index; got != v {
			t.Errorf("--index %s reached FleetOptions.Index as %q", v, got)
		}
	}
}

func TestImpactIndexRefusedWithEvent(t *testing.T) {
	statsOff(t)
	_, _, err := runRoot(t, defaultDeps(), nil, "impact", "--unavailable", "orders", "--index", "none")
	if err == nil || !strings.Contains(err.Error(), "--index") || !strings.Contains(err.Error(), "--unavailable") {
		t.Fatalf("err %v", err)
	}
}

func TestImpactExitUnchangedByIndexLine(t *testing.T) {
	host, _, _ := targetsEnv(t)
	out, _, err := runImpact(t, host, "--target-snapshot", "off")
	_, _, jsonErr := runImpact(t, host, "--target-snapshot", "off", "--output-format", "json")
	want := "Consumers: no organization index at " + host + "/acme/pacto-index or " + host + "/pacto-index (partial)\n"
	if !strings.Contains(out, want) || impactExit(err) != impactExit(jsonErr) {
		t.Fatalf("text %v, json %v:\n%s", err, jsonErr, out)
	}
}

func TestImpactJSONCarriesConsumerIndex(t *testing.T) {
	host, _, _ := targetsEnv(t)
	out, _, _ := runImpact(t, host, "--target-snapshot", "off", "--output-format", "json")
	doc := decode(t, out)
	ix, _ := doc["consumerIndex"].(map[string]any)
	if ix["status"] != impact.IndexNotFound || ix["completeness"] != string(fleet.CompletenessPartial) {
		t.Fatalf("consumerIndex %v", ix)
	}
}

func TestCIIndexAloneIsNotAConsumerSource(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	out, _, _ := e.run(t, "--output-format", "json")
	doc := decode(t, out)
	row := rowsByService(doc)["reports"]
	imp, _ := row["impact"].(map[string]any)
	if imp["text"] != "unverified (no consumer source)" || doc["index"] != "0 from "+e.host+"/acme/pacto-index (listed by tag, 1 service listed, unchecked)" {
		t.Fatalf("impact %v, index %v", imp, doc["index"])
	}
}

func TestCIPassesIndexToImpact(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	out, _, _ := e.run(t, "--output-format", "json", "--index", "none")
	if doc := decode(t, out); doc["index"] != "organization index not read (--index none)" {
		t.Fatalf("index %v", doc["index"])
	}
	out, _, _ = e.run(t, "--output-format", "json", "--index", "oci://"+e.host+"/other/pacto-index")
	if doc := decode(t, out); doc["index"] != "no organization index at "+e.host+"/other/pacto-index (partial)" {
		t.Fatalf("index %v", doc["index"])
	}
}

// TestCIPrintsIndexLine: a target-state source adds targets with no revision,
// so it never checks the index: the line says unchecked and the row is
// unverified.
func TestCIPrintsIndexLine(t *testing.T) {
	e := newCIEnv(t)
	e.additive(t, "reports")
	out, stderr, err := e.run(t, "--target-state", targetState(t, "other"))
	if ciExit(err) != 2 || !strings.Contains(out, "\nIndex: 0 from "+e.host+"/acme/pacto-index (listed by tag, 1 service listed, unchecked)\n") {
		t.Fatalf("err %v:\n%s\n%s", err, out, stderr)
	}
}
