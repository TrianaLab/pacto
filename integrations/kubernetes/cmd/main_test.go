package main

import (
	"flag"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/trianalab/pacto/integrations/kubernetes/v6/internal/dashboard"
	"github.com/trianalab/pacto/v4/pkg/detect"
)

// chartlessFlags are the operator flags the chart deliberately never renders, each with the reason.
// An entry ending in "*" matches every flag with that prefix.
var chartlessFlags = map[string]string{
	"version":         "prints version information and exits",
	"enable-http2":    "kept off on purpose: HTTP/2 rapid-reset hardening",
	"watch-namespace": "deprecated alias of --watch-namespaces",
	"metrics-cert-path": "the chart serves controller-runtime's self-signed metrics certificate; " +
		"mounting one needs a Secret volume the chart does not render",
	"metrics-cert-name": "only meaningful with --metrics-cert-path, which the chart does not render",
	"metrics-cert-key":  "only meaningful with --metrics-cert-path, which the chart does not render",
	"zap-*":             "logging flags; production JSON logging is fixed in main",
}

// chartlessKey returns the chartlessFlags entry covering name, or "" when none does.
func chartlessKey(name string) string {
	if _, ok := chartlessFlags[name]; ok {
		return name
	}
	for k := range chartlessFlags {
		if p, ok := strings.CutSuffix(k, "*"); ok && strings.HasPrefix(name, p) {
			return k
		}
	}
	return ""
}

// controllerArgs returns the body of the pacto-operator.controllerArgs define, cut the same way
// release/scripts/gen_integration_docs.py cuts it.
func controllerArgs(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../charts/pacto-operator/templates/_helpers.tpl")
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(string(raw), `{{- define "pacto-operator.controllerArgs" -}}`)
	if !ok {
		t.Fatal(`_helpers.tpl has no define "pacto-operator.controllerArgs"`)
	}
	body, _, _ = strings.Cut(body, "{{- define ")
	return body
}

func rendered(body, name string) bool {
	return regexp.MustCompile(`--` + regexp.QuoteMeta(name) + `(=|\s|$|")`).MatchString(body)
}

func TestEveryOperatorFlagHasAChartValue(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	bindFlags(fs, &options{})
	body := controllerArgs(t)

	used := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		inChart := rendered(body, f.Name)
		key := chartlessKey(f.Name)
		switch {
		case key != "" && inChart:
			t.Errorf("flag --%s is rendered by pacto-operator.controllerArgs: drop %q from chartlessFlags", f.Name, key)
		case key != "":
			used[key] = true
		case !inChart:
			t.Errorf("flag --%s has no line in pacto-operator.controllerArgs: add a chart value in the same commit, "+
				"or add it to chartlessFlags with the reason", f.Name)
		}
	})
	for k := range chartlessFlags {
		if !used[k] {
			t.Errorf("chartlessFlags entry %q matches no operator flag: remove it", k)
		}
	}
}

func TestChartlessFlagsHaveReasons(t *testing.T) {
	for k, r := range chartlessFlags {
		if strings.TrimSpace(r) == "" {
			t.Errorf("chartlessFlags entry %q has no reason", k)
		}
	}
}

func TestStrayArgumentIsRefused(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	o := &options{}
	bindFlags(fs, o)
	if err := fs.Parse([]string{"--enable-probing", "off", "--leader-elect"}); err != nil {
		t.Fatal(err)
	}
	if err := noStrayArgs(fs); err == nil || !strings.Contains(err.Error(), `"off"`) {
		t.Fatalf("noStrayArgs = %v; want the stray off refused", err)
	}
	fs = flag.NewFlagSet("t", flag.ContinueOnError)
	bindFlags(fs, &options{})
	if err := fs.Parse([]string{"--enable-probing=off", "--leader-elect"}); err != nil || noStrayArgs(fs) != nil {
		t.Fatalf("the equals form must parse cleanly: %v", err)
	}
}

func TestDashboardExternalCatalogFlag(t *testing.T) {
	ref := "oci://ghcr.io/acme/catalog@sha256:" + strings.Repeat("a", 64)
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	o := &options{}
	bindFlags(fs, o)
	if err := fs.Parse([]string{"--dashboard-external-catalog=" + ref}); err != nil {
		t.Fatal(err)
	}
	if o.dashboardExternalCatalog != ref {
		t.Errorf("dashboardExternalCatalog = %q, want %q", o.dashboardExternalCatalog, ref)
	}
}

// registryFlags classifies every chart-rendered flag whose name holds "registry" or "repository": push (the
// component writes there, so its repository must sit in the push scope and the push prefixes) or read (with
// the reason).
var registryFlags = map[string]string{
	"registry":             "read: names the installation registry; the scope is derived from it",
	"registry-pull-secret": "read: names the Secret that authenticates to the installation registry",
	"registry-push-prefix": "read: describes the push scope; it writes nothing",
	"streams-repository":   "push",
	"approvals-repository": "push",
	"stats-repository":     "push",
	"publishers-registry": "read: the promoter only reads publisher signatures there; " +
		"the pins it confirms go to the streams repository",
	"records-repository":                "read: the promoter reads the recorder's pins from the streams repository",
	"dashboard-stats-registry":          "read: the dashboard only reads run records",
	"ladder-repository":                 "read: the promoter only reads ladder records from the approvals repository",
	"dashboard-registry":                "push",
	"dashboard-declare-registry-secret": "push",
}

var registryFlagPattern = regexp.MustCompile(`--([a-z-]*(?:registry|repository)[a-z-]*)=`)

// chartRegistryFlags returns every --*registry*= or --*repository*= flag in the chart templates.
func chartRegistryFlags(t *testing.T) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	err := filepath.WalkDir("../charts/pacto-operator/templates", func(path string, d iofs.DirEntry, err error) error {
		if err != nil || d.IsDir() || (!strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".tpl")) {
			return err
		}
		raw, err := os.ReadFile(path)
		for _, m := range registryFlagPattern.FindAllStringSubmatch(string(raw), -1) {
			found[m[1]] = true
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestEveryRegistryFlagIsClassified(t *testing.T) {
	found := chartRegistryFlags(t)
	for name := range found {
		if r := registryFlags[name]; r != "push" && !strings.HasPrefix(r, "read: ") {
			t.Errorf("chart flag --%s is not classified in registryFlags: push (it must render under the push "+
				"prefixes) or read: <reason>", name)
		}
	}
	for name := range registryFlags {
		if !found[name] {
			t.Errorf("registryFlags entry %q matches no chart flag: remove it", name)
		}
	}
}

func TestDashboardIndexFlag(t *testing.T) {
	parse := func(args ...string) (*options, error) {
		o := &options{}
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		bindFlags(fs, o)
		return o, fs.Parse(args)
	}
	withRegistry := &detect.Installation{Index: "ghcr.io/acme/pacto-index"}
	noRegistry := &detect.Installation{}
	for _, tc := range []struct {
		args []string
		inst *detect.Installation
		want string
	}{
		{nil, withRegistry, "ghcr.io/acme/pacto-index"},
		{[]string{"--dashboard-index=auto"}, withRegistry, "ghcr.io/acme/pacto-index"},
		{[]string{"--dashboard-index=auto"}, noRegistry, ""},
		{[]string{"--dashboard-index=auto"}, nil, ""},
		{[]string{"--dashboard-index=none"}, withRegistry, ""},
	} {
		o, err := parse(tc.args...)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if got := dashboardIndex(o.dashboardIndex, tc.inst); got != tc.want {
			t.Errorf("%v with %+v: index = %q, want %q", tc.args, tc.inst, got, tc.want)
		}
	}
	for _, bad := range []string{"ghcr.io/acme/pacto-index", "", "Auto"} {
		if _, err := parse("--dashboard-index=" + bad); err == nil || !strings.Contains(err.Error(), "want auto or none") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestDashboardBackstageFlags(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	o := &options{}
	bindFlags(fs, o)
	if o.dashboardBackstageToken.Key != "token" {
		t.Errorf("default token key = %q, want token", o.dashboardBackstageToken.Key)
	}
	if err := fs.Parse([]string{"--dashboard-backstage-url=https://b.example.com",
		"--dashboard-backstage-token-secret=reader", "--dashboard-backstage-token-key=t"}); err != nil {
		t.Fatal(err)
	}
	want := dashboard.KeyRef{Name: "reader", Key: "t"}
	if o.dashboardBackstageURL != "https://b.example.com" || o.dashboardBackstageToken != want {
		t.Errorf("backstage options = %q %+v", o.dashboardBackstageURL, o.dashboardBackstageToken)
	}
}

// TestDashboardRepositoriesDeprecationNamesDiscoveryFlag: the deprecated
// flag points at --dashboard-registry, which turns discovery on; the
// operator's --registry does not reach the dashboard.
func TestDashboardRepositoriesDeprecationNamesDiscoveryFlag(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	bindFlags(fs, &options{})
	if u := fs.Lookup("dashboard-repositories").Usage; !strings.HasPrefix(u, "Deprecated: set --dashboard-registry") {
		t.Errorf("usage %q", u)
	}
}
