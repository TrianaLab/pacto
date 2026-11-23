package controller

import (
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// secretBearingKinds are managed kinds whose objects hold key material or
// secret values: a list returns the whole object, so no rule may name them.
var secretBearingKinds = map[string][]string{
	"iam.cnrm.cloud.google.com":           {"iamserviceaccountkeys"},
	"secretmanager.cnrm.cloud.google.com": {"secretmanagersecretversions"},
	"keyvault.azure.com":                  {"vaultsecrets", "secrets"},
	"secretsmanager.services.k8s.aws":     {"secrets"},
	"secretsmanager.aws.upbound.io":       {"secretversions"},
}

// helperBody is the body of the named template name in templates/_helpers.tpl.
func helperBody(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(chartTemplates + "_helpers.tpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range chartDefine.FindAllStringSubmatch(string(data), -1) {
		if m[1] == name {
			return m[2]
		}
	}
	t.Fatalf("no %s template", name)
	return ""
}

// managedKinds are the "<group>/<Kind>" kinds of pacto-operator.managedKinds
// and their plurals.
func managedKinds(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for line := range strings.SplitSeq(helperBody(t, "pacto-operator.managedKinds"), "\n") {
		kind, plural, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("managedKinds line %q is not <group>/<Kind>=<plural>", line)
		}
		out[kind] = plural
	}
	return out
}

// TestManagedKindsMatchCatalog keeps the chart's managed kinds and the
// default catalog's crossplane rows one set, so the chart grants no kind the
// detector never reads and the detector reads no kind the chart never grants.
// A plural is the lower-cased Kind plus "s", the form every catalog kind takes
// today; a kind with another plural adds it here.
func TestManagedKindsMatchCatalog(t *testing.T) {
	f, err := externalcatalog.ParseFile(externalcatalog.DefaultBytes())
	if err != nil {
		t.Fatal(err)
	}
	var catalog []string
	for _, e := range f.Entries {
		catalog = append(catalog, e.Crossplane...)
	}
	kinds := managedKinds(t)
	if got, want := slices.Sorted(maps.Keys(kinds)), slices.Sorted(slices.Values(catalog)); !slices.Equal(got, want) {
		t.Fatalf("pacto-operator.managedKinds lists %v; the default catalog's crossplane rows are %v", got, want)
	}
	for kind, plural := range kinds {
		_, k, _ := strings.Cut(kind, "/")
		if plural != strings.ToLower(k)+"s" {
			t.Errorf("%s has plural %q", kind, plural)
		}
	}
}

// TestManagedRulesListOnly: every rule pacto-operator.managedRules renders
// grants list and nothing else, and names no wildcard resource.
func TestManagedRulesListOnly(t *testing.T) {
	body := helperBody(t, "pacto-operator.managedRules")
	verbs := verbList.FindAllStringSubmatch(body, -1)
	if len(verbs) == 0 {
		t.Fatal("pacto-operator.managedRules renders no rule")
	}
	for _, m := range verbs {
		if strings.TrimSpace(m[1]) != "list" {
			t.Errorf("a managed rule grants [%s]", m[1])
		}
	}
	if strings.Contains(body, "[*]") || strings.Contains(body, `"*"`) || strings.Contains(body, "'*'") {
		t.Error("pacto-operator.managedRules names a wildcard")
	}
	for kind, plural := range managedKinds(t) {
		if strings.Contains(kind+plural, "*") {
			t.Errorf("managed kind %s=%s names a wildcard", kind, plural)
		}
	}
}

// managedKindsGrantNoSecrets fails on a managed kind whose objects hold
// secret values.
func managedKindsGrantNoSecrets(t *testing.T) {
	for kind, plural := range managedKinds(t) {
		group, _, _ := strings.Cut(kind, "/")
		if slices.Contains(secretBearingKinds[group], plural) {
			t.Errorf("pacto-operator.managedKinds grants %s/%s, whose objects hold secret values", group, plural)
		}
	}
}
