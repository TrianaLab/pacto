package mcp

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"
)

const offeringFrom = "oci://ghcr.io/acme/pacto-inferred/crossplane-6f1c/services:xbucket_0.0.0-inferred.2.0"

func offeringArgs() map[string]any {
	return map[string]any{"service": "offering:xbucket", "from": offeringFrom, "version": "1.0.0", "owner": "team-storage", "registry": "ghcr.io/acme/offerings"}
}

// TestMCPDeclareOfferingArgsAccepted: an acceptance takes owner, version,
// from and registry; a workload's arguments are refused before any plan.
func TestMCPDeclareOfferingArgsAccepted(t *testing.T) {
	var plans []DeclareArgs
	out, res := callDeclare(t, stubDeclarer{plans: &plans}, offeringArgs())
	if res.IsError || len(plans) != 1 || plans[0].From != offeringFrom || plans[0].Version != "1.0.0" || plans[0].Owner != "team-storage" {
		t.Fatalf("%+v %+v", out, plans)
	}
	for k, v := range map[string]any{"namespace": "shop", "accept": []string{"ledger"}, "max_items": 5, "call_window": "2h", "name": "x"} {
		args := maps.Clone(offeringArgs())
		args[k] = v
		var plans []DeclareArgs
		out, _ := callDeclare(t, stubDeclarer{plans: &plans}, args)
		if len(plans) != 0 || out.Refusal == nil || out.Refusal.Code != RefusalFieldNotAcceptable ||
			!strings.HasPrefix(out.Refusal.CLI, "pacto declare offering:xbucket --from "+offeringFrom) {
			t.Errorf("%s: %+v", k, out)
		}
	}
}

// TestMCPDeclareVersionAndFromAreAnOfferings: a workload or a claim takes
// no version or from.
func TestMCPDeclareVersionAndFromAreAnOfferings(t *testing.T) {
	for k, v := range map[string]any{"version": "1.0.0", "from": offeringFrom} {
		var plans []DeclareArgs
		out, _ := callDeclare(t, stubDeclarer{plans: &plans}, map[string]any{"service": "invoices", k: v})
		if len(plans) != 0 || out.Refusal == nil || out.Refusal.Code != RefusalFieldNotAcceptable {
			t.Errorf("workload %s: %+v", k, out)
		}
		out, _ = callDeclare(t, stubDeclarer{plans: &plans}, map[string]any{"service": "external:stripe", "owner": "t", k: v})
		if len(plans) != 0 || out.Refusal == nil || out.Refusal.Code != RefusalFieldNotAcceptable {
			t.Errorf("workload %s: %+v", k, out)
		}
	}
}

// TestMCPDeclareOfferingSchemaRefusesBadName: an offering name or version
// that is none never reaches the planner.
func TestMCPDeclareOfferingSchemaRefusesBadName(t *testing.T) {
	for _, svc := range []string{"offering:Bad_Name", "offering:", "offering:x;rm"} {
		var plans []DeclareArgs
		args := offeringArgs()
		args["service"] = svc
		out, _ := callDeclare(t, stubDeclarer{plans: &plans}, args)
		if len(plans) != 0 || out.Refusal == nil || out.Refusal.Code != RefusalFieldNotAcceptable || strings.Contains(out.Refusal.CLI, ";") {
			t.Errorf("%q: %+v", svc, out)
		}
	}
	var plans []DeclareArgs
	args := offeringArgs()
	args["version"] = "1.0"
	res := callToolOn(t, mustNew("test", Authoring{Declare: stubDeclarer{plans: &plans}}, Modes{}), "pacto_declare", args)
	if !res.IsError || len(plans) != 0 {
		t.Fatal(resultText(t, res))
	}
}

func TestMCPDeclareOfferingVerbAndCommand(t *testing.T) {
	a := DeclareArgs{Service: "offering:xbucket", From: offeringFrom, Version: "1.0.0", Owner: "team-storage", Registry: "ghcr.io/acme/offerings"}
	want := "pacto declare offering:xbucket --from " + offeringFrom + " --detected k8s:<namespace>/<configmap> --registry ghcr.io/acme/offerings --version 1.0.0 --owner team-storage"
	if got := a.Command(planDigest); got != want+" --intent "+planDigest {
		t.Fatal(got)
	}
	if got := (DeclareArgs{Service: "offering:xbucket"}).Command(""); got !=
		"pacto declare offering:xbucket --from <contribution> --detected k8s:<namespace>/<configmap> --registry <registry> --version <version>" {
		t.Fatal(got)
	}
	if name, ok := a.Offering(); !ok || name != "xbucket" || a.Verb() != "pacto declare" {
		t.Fatal(name, ok, a.Verb())
	}
}

func TestDeclareToolNamesOffering(t *testing.T) {
	tool := listTools(t, mustNew("test", Authoring{Declare: stubDeclarer{}}, Modes{}))["pacto_declare"]
	schema, _ := json.Marshal(tool.InputSchema)
	if !strings.Contains(tool.Description, "acceptCLI (pacto declare offering:<name> --from <ref> --detected <detection> --registry <registry> --version <version> --from-draft draft-<n> --yes)") || !strings.Contains(string(schema), `"version"`) ||
		!strings.Contains(string(schema), `"from"`) {
		t.Fatalf("%s\n%s", tool.Description, schema)
	}
}

// TestMCPFleetEntitiesDeclarationOfferingCase: pacto_fleet_entities says
// what declaration inferred lists with kind offering.
func TestMCPFleetEntitiesDeclarationOfferingCase(t *testing.T) {
	tool := listTools(t, mustNew("test", Authoring{}, Modes{Fleet: &FleetMode{Src: staticFleet(buildFleetQuery(t))}}))["pacto_fleet_entities"]
	schema, _ := json.Marshal(tool.InputSchema)
	if !strings.Contains(string(schema), "with kind offering, list only the inferred offerings producers contribute") ||
		!strings.Contains(string(schema), "never consumable") {
		t.Fatalf("%s", schema)
	}
}
