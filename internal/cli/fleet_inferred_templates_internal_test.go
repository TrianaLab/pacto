package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/trianalab/pacto/v4/pkg/fleet"
)

var tmplAsOf = time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)

// tmplItem is one inferred template a configured catalog read.
func tmplItem(name string) fleet.InferredTemplate {
	return fleet.InferredTemplate{Name: name, Entity: "template:default/" + name, From: "Backstage (catalog.acme.io)",
		URL: "https://catalog.acme.io/create/templates/default/" + name, Acceptable: true, Provenance: fleet.ProvenanceInferred}
}

// tmplList is a golden-path answer with one declared path and the block l.
func tmplList(l *fleet.InferredTemplateList) *fleet.EntityList {
	return &fleet.EntityList{Count: 1, Total: 1, Meta: fleet.ProductMeta{Completeness: fleet.CompletenessComplete},
		Entities: []fleet.EntityRef{{Kind: fleet.KindService, Key: "go-http-postgres", Label: "go-http-postgres",
			Template: true, Status: "Compliant"}}, InferredTemplates: l}
}

// printTmpl prints res as text with --backstage-url set to url and returns
// stdout and stderr.
func printTmpl(t *testing.T, res *fleet.EntityList, url string) (string, string) {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().String("backstage-url", url, "")
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	if err := printFleetEntities(cmd, res, "text"); err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String()
}

func tmplBlock(items ...fleet.InferredTemplate) *fleet.InferredTemplateList {
	return &fleet.InferredTemplateList{Items: items, Total: len(items), AsOf: &tmplAsOf, Completeness: fleet.CompletenessComplete}
}

func TestFleetEntitiesTemplateTextListsInferred(t *testing.T) {
	out, errOut := printTmpl(t, tmplList(tmplBlock(tmplItem("springboot-service"))), "https://catalog.acme.io")
	want := "Inferred golden paths (complete, asOf 2026-11-23T09:00:00Z):\n" +
		"  springboot-service  inferred from Backstage (catalog.acme.io)  template:default/springboot-service\n" +
		"    open: https://catalog.acme.io/create/templates/default/springboot-service\n" +
		"    accept: pacto declare template:default/springboot-service --registry <registry> --backstage-url https://catalog.acme.io\n"
	if !strings.Contains(out, want) || !strings.Contains(out, "  golden path go-http-postgres") {
		t.Errorf("text:\n%s\nwant:\n%s", out, want)
	}
	if errOut != "" {
		t.Errorf("a complete block warns: %q", errOut)
	}
}

func TestFleetEntitiesDeclaredHidesInferred(t *testing.T) {
	out, _ := printTmpl(t, tmplList(nil), "")
	if strings.Contains(out, "Inferred golden paths") {
		t.Errorf("no block prints a heading:\n%s", out)
	}
}

func TestFleetEntitiesInferredNoneComplete(t *testing.T) {
	out, _ := printTmpl(t, tmplList(tmplBlock()), "")
	if !strings.Contains(out, "Inferred golden paths: none (complete, asOf 2026-11-23T09:00:00Z)\n") {
		t.Errorf("empty block:\n%s", out)
	}
}

func TestFleetEntitiesInferredPartialSaysWhy(t *testing.T) {
	l := tmplBlock()
	l.Completeness = fleet.CompletenessPartial
	l.Limitations = []fleet.Limitation{{Code: fleet.LimitationSourceUnavailable, Message: "catalog unreachable"}}
	out, errOut := printTmpl(t, tmplList(l), "")
	if !strings.Contains(out, "Inferred golden paths: none (partial, asOf 2026-11-23T09:00:00Z)\n  note: catalog unreachable\n") {
		t.Errorf("partial block:\n%s", out)
	}
	if !strings.Contains(errOut, "warning: inferred golden paths may be incomplete (as of 2026-11-23T09:00:00Z)\n") ||
		!strings.Contains(errOut, "catalog unreachable") {
		t.Errorf("stderr: %q", errOut)
	}
}

// detected is an item a detected (unconfigured) catalog read.
func detected() fleet.InferredTemplate {
	it := tmplItem("springboot-service")
	it.URL, it.Acceptable = "", false
	return it
}

func TestFleetEntitiesInferredDetectedHasNoLink(t *testing.T) {
	out, _ := printTmpl(t, tmplList(tmplBlock(detected())), "")
	if strings.Contains(out, "open: ") || !strings.Contains(out, "    open it in your Backstage\n") {
		t.Errorf("detected item:\n%s", out)
	}
}

func TestFleetEntitiesInferredDetectedHasNoAccept(t *testing.T) {
	out, _ := printTmpl(t, tmplList(tmplBlock(detected())), "")
	if strings.Contains(out, "accept:") || !strings.Contains(out, "    set --backstage-url to accept templates from this catalog\n") {
		t.Errorf("detected item:\n%s", out)
	}
}

func TestFleetEntitiesNameTakenHasNoAccept(t *testing.T) {
	it := tmplItem("orders")
	it.NameTaken = true
	out, _ := printTmpl(t, tmplList(tmplBlock(it)), "https://catalog.acme.io")
	if strings.Contains(out, "accept:") || !strings.Contains(out, "    the name is held by another service: no accept\n") {
		t.Errorf("taken item:\n%s", out)
	}
}

func TestFleetEntitiesDeclaredPathShowsCatalogLink(t *testing.T) {
	d := tmplItem("go-http-postgres")
	d.DeclaredAs = "go-http-postgres"
	other := tmplItem("other")
	other.DeclaredAs = "other"
	l := tmplBlock()
	l.Declared = []fleet.InferredTemplate{other, d}
	out, _ := printTmpl(t, tmplList(l), "")
	if !strings.Contains(out, "Compliant\n    open: https://catalog.acme.io/create/templates/default/go-http-postgres\n") {
		t.Errorf("declared path link:\n%s", out)
	}
	if strings.Contains(out, "templates/default/other") {
		t.Errorf("a link under the wrong row:\n%s", out)
	}
}

// TestFleetEntitiesAcceptWithoutURLFlag: an acceptable item printed by a
// command with no --backstage-url names the flag for the person to fill in.
func TestFleetEntitiesAcceptWithoutURLFlag(t *testing.T) {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := printFleetEntities(cmd, tmplList(tmplBlock(tmplItem("a"))), "text"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--backstage-url <url>\n") {
		t.Errorf("accept line:\n%s", out.String())
	}
}
