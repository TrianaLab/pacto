package contract

import (
	"strings"
	"testing"
)

// rest is the tail of every refusal message.
const rest = "; a contribution declares only service.name and service.version " +
	"(with service.offering also service.summary and configurations of name, schema and required): move the rest to pacto.inferred.yaml"

func TestContributionRefusal(t *testing.T) {
	raw := func(s string) *Bundle { return &Bundle{RawYAML: []byte(s)} }
	const ok = "pactoVersion: \"2.5\"\nservice:\n  name: a\n  version: 1.0.0\n"
	for _, tc := range []struct {
		name string
		b    *Bundle
		rec  *InferredRecord
		want string
	}{
		{"name and version only", raw(ok), nil, ""},
		{"record without declaration", raw(ok), &InferredRecord{}, ""},
		{"record with declaration", raw(ok), &InferredRecord{Declaration: &InferredDeclaration{}}, "a contribution carries no declaration"},
		{"unreadable", &Bundle{}, nil, "it declares pacto.yaml" + rest},
		{"invalid yaml", raw(":\n- ["), nil, "it declares pacto.yaml" + rest},
		{"top-level field", raw(ok + "owner: x\n"), nil, "it declares owner" + rest},
		{"service not a map", raw("pactoVersion: \"2.5\"\nservice: x\n"), nil, "it declares service" + rest},
		{"service field", raw(ok + "  owner: x\n"), nil, "it declares service.owner" + rest},
	} {
		if got := ContributionRefusal(tc.b, tc.rec); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// offeringYAML is an inferred offering's pacto.yaml: the offering section and
// one provisioning configuration.
const offeringYAML = "pactoVersion: \"2.5\"\nservice:\n  name: xbucket\n  version: 0.0.0-inferred.2.0\n" +
	"  offering: xbucket\n  summary: An object bucket\nconfigurations:\n  - name: provisioning\n    schema: configuration/provisioning.json\n    required: true\n"

func TestContributionAllowsOfferingSection(t *testing.T) {
	if got := ContributionRefusal(&Bundle{RawYAML: []byte(offeringYAML)}, &InferredRecord{}); got != "" {
		t.Fatalf("offering section refused: %q", got)
	}
}

func TestContributionOfferingNamedAfterItsOffering(t *testing.T) {
	yml := strings.Replace(offeringYAML, "name: xbucket", "name: pg-offering", 1)
	if got := ContributionRefusal(&Bundle{RawYAML: []byte(yml)}, nil); !strings.Contains(got, "service.name pg-offering is not its service.offering xbucket") {
		t.Fatalf("got %q", got)
	}
}

func TestContributionOfferingSectionNeedsOffering(t *testing.T) {
	for name, yml := range map[string]string{
		"no offering":    strings.Replace(offeringYAML, "  offering: xbucket\n", "", 1),
		"empty offering": strings.Replace(offeringYAML, "offering: xbucket", `offering: ""`, 1),
		"offering a map": strings.Replace(offeringYAML, "offering: xbucket", "offering: {a: b}", 1),
	} {
		got := ContributionRefusal(&Bundle{RawYAML: []byte(yml)}, nil)
		if got != "it declares configurations"+rest {
			t.Errorf("%s: got %q", name, got)
		}
	}
}

func TestContributionOfferingRefusesOtherKeys(t *testing.T) {
	for _, tc := range []struct{ name, yml, field string }{
		{"dependencies", offeringYAML + "dependencies: []\n", "dependencies"},
		{"service.owner", strings.Replace(offeringYAML, "  summary:", "  owner: team\n  summary:", 1), "service.owner"},
		{"configurations ref", offeringYAML + "    ref: oci://x/y:1.0.0\n", "configurations[0].ref"},
		{"configurations values", offeringYAML + "    values: {}\n", "configurations[0].values"},
		{"configurations not a list", offeringYAML[:strings.Index(offeringYAML, "configurations:")] + "configurations: x\n", "configurations"},
	} {
		if got := ContributionRefusal(&Bundle{RawYAML: []byte(tc.yml)}, nil); got != "it declares "+tc.field+rest {
			t.Errorf("%s: got %q", tc.name, got)
		}
	}
}

func TestContributionStillRefusesDeclaration(t *testing.T) {
	rec := &InferredRecord{Declaration: &InferredDeclaration{}}
	if got := ContributionRefusal(&Bundle{RawYAML: []byte(offeringYAML)}, rec); got != "a contribution carries no declaration" {
		t.Fatalf("got %q", got)
	}
}
