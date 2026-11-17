package main

import (
	"flag"
	"os"
	"regexp"
	"strings"
	"testing"
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
