package pactify

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
	"gopkg.in/yaml.v3"
)

func planExternals(t *testing.T, opts Options) *Result {
	t.Helper()
	opts.Plan = true
	r, err := Init(context.Background(), opts, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func external(r *Result, identity, where string) (External, bool) {
	i := slices.IndexFunc(r.Externals, func(e External) bool { return e.Identity == identity && strings.Contains(e.Where, where) })
	if i < 0 {
		return External{}, false
	}
	return r.Externals[i], true
}

// TestPactifyClassifiesDottedHosts: a vendor, a managed service and an
// unknown registrable domain are externals; an in-cluster name stays a
// dependency; a namespace short form is no external and is named in Unknown.
func TestPactifyClassifiesDottedHosts(t *testing.T) {
	r := planExternals(t, Options{Dir: fixtureDir(t, "externals"), DescribeOnly: true})
	for _, want := range []External{
		{Identity: "stripe", Class: externalcatalog.ClassVendorAPI, Confidence: "likely", Where: "k8s/deployment.yaml, env STRIPE_API_BASE", Host: "api.stripe.com", Entry: "stripe"},
		{Identity: "aws/rds", Class: externalcatalog.ClassManagedCloud, Confidence: "likely", Where: "k8s/configmap.yaml, env DATABASE_URL", Host: "db.xyz.rds.amazonaws.com", Entry: "aws/rds"},
		{Identity: "host/corp-legacy.co.uk", Class: externalcatalog.ClassUnclassified, Confidence: "likely", Where: "chart/values.yaml:4 ERP_HOST", Host: "erp.corp-legacy.co.uk:8443"},
	} {
		if !slices.Contains(r.Externals, want) {
			t.Errorf("no %+v in %+v", want, r.Externals)
		}
	}
	if !slices.Equal(r.Dependencies, []Finding{{Name: "orders", Where: "k8s/deployment.yaml, env ORDERS_URL"}}) {
		t.Errorf("dependencies: %+v", r.Dependencies)
	}
	if slices.ContainsFunc(r.Externals, func(e External) bool { return strings.Contains(e.Where, "ORDERS_ADDR") }) {
		t.Errorf("orders.shop became an external: %+v", r.Externals)
	}
	if !hasLine(r.Unknown, "env ORDERS_ADDR names a host outside the cluster") {
		t.Errorf("unknown: %q", r.Unknown)
	}
	if !hasLine(r.Found, "checkout calls stripe (api.stripe.com, k8s/deployment.yaml, env STRIPE_API_BASE)") ||
		!strings.HasSuffix(r.Catalog, " (embedded)") || !strings.HasPrefix(r.Catalog, externalcatalog.DefaultDigest()) {
		t.Errorf("found %q, catalog %q", r.Found, r.Catalog)
	}

	// A catalog the caller gives is used and named by its digest.
	cat := externalcatalog.Merge(&externalcatalog.File{APIVersion: externalcatalog.APIVersion}, externalcatalog.Ref{Digest: "sha256:org"}, nil, nil)
	r = planExternals(t, Options{Dir: fixtureDir(t, "externals"), DescribeOnly: true, Catalog: cat})
	if r.Catalog != "sha256:org" {
		t.Errorf("catalog %q", r.Catalog)
	}
	if _, ok := external(r, "stripe", ""); ok {
		t.Errorf("an empty catalog matched stripe: %+v", r.Externals)
	}
}

// TestPactifySecretKeyRefKeyNameOnly: a secretKeyRef contributes its key
// names only, as a possible match, and a Secret manifest in the folder is
// never read for hosts.
func TestPactifySecretKeyRefKeyNameOnly(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"k8s/d.yaml":      "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: pay}\nspec: {template: {spec: {containers: [{name: pay, image: 'r.example/pay:1.0.0', env: [{name: TWILIO_AUTH, valueFrom: {secretKeyRef: {name: creds, key: token}}}]}]}}}\n",
		"k8s/secret.yaml": "apiVersion: v1\nkind: Secret\nmetadata: {name: creds}\nstringData: {SENDGRID_URL: 'https://api.sendgrid.com'}\n",
	})
	r := planExternals(t, Options{Dir: dir, DescribeOnly: true})
	want := []External{{Identity: "twilio", Class: externalcatalog.ClassVendorAPI, Confidence: "possible", Where: "k8s/d.yaml, secretKeyRef creds/token (key name only)", Entry: "twilio"}}
	if !slices.Equal(r.Externals, want) {
		t.Fatalf("externals: %+v", r.Externals)
	}
}

// TestPactifySecretRefNameOnly: an envFrom secretRef contributes the Secret's
// name only.
func TestPactifySecretRefNameOnly(t *testing.T) {
	r := planExternals(t, Options{Dir: fixtureDir(t, "externals"), DescribeOnly: true})
	e, ok := external(r, "stripe", "secretRef stripe-credentials")
	if !ok || e.Confidence != "possible" || e.Where != "k8s/deployment.yaml, secretRef stripe-credentials (Secret name only)" || e.Host != "" {
		t.Fatalf("externals: %+v", r.Externals)
	}
	// With no workload there is no environment to read.
	dir := writeTree(t, t.TempDir(), map[string]string{"Dockerfile": "FROM scratch\n"})
	if r := planExternals(t, Options{Dir: dir, DescribeOnly: true}); len(r.Externals) != 0 {
		t.Fatalf("externals without a workload: %+v", r.Externals)
	}
}

// TestPactifyChartValuesLine: a chart's values.yaml is read as data and each
// external names the file, line and key; a root chart reads values.yaml.
func TestPactifyChartValuesLine(t *testing.T) {
	r := planExternals(t, Options{Dir: fixtureDir(t, "externals"), DescribeOnly: true})
	if _, ok := external(r, "aws/rds", "chart/values.yaml:3 DATABASE_URL"); !ok {
		t.Errorf("externals: %+v", r.Externals)
	}
	dir := writeTree(t, t.TempDir(), map[string]string{
		"Dockerfile":  "FROM scratch\n",
		"Chart.yaml":  "apiVersion: v2\nname: pay\nversion: 1.0.0\n",
		"values.yaml": "image: {tag: 1.0.0}\nhosts:\n  - &erp https://api.stripe.com\n  - *erp\nn: 3\n",
	})
	r = planExternals(t, Options{Dir: dir, DescribeOnly: true})
	if !slices.Equal(r.Externals, []External{{Identity: "stripe", Class: externalcatalog.ClassVendorAPI, Confidence: "likely", Where: "values.yaml:3 hosts", Host: "api.stripe.com", Entry: "stripe"}}) {
		t.Errorf("root chart externals: %+v", r.Externals)
	}
	// A values.yaml that is not YAML, or is missing, gives nothing.
	for _, values := range map[string]string{"bad": "a: [\n", "missing": ""} {
		files := map[string]string{"Dockerfile": "FROM scratch\n", "Chart.yaml": "apiVersion: v2\nname: pay\nversion: 1.0.0\n"}
		if values != "" {
			files["values.yaml"] = values
		}
		if r := planExternals(t, Options{Dir: writeTree(t, t.TempDir(), files), DescribeOnly: true}); len(r.Externals) != 0 {
			t.Errorf("externals from %q: %+v", values, r.Externals)
		}
	}
}

// TestPactifyPossibleMatchNotWritten: a key name with nothing to confirm it
// gets no entry and is named in Unknown.
func TestPactifyPossibleMatchNotWritten(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"k8s/d.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: pay}\nspec: {template: {spec: {containers: [{name: pay, image: 'r.example/pay:1.0.0', env: [{name: TWILIO_ACCOUNT, value: abc}]}]}}}\n",
	})
	r := planExternals(t, Options{Dir: dir, DescribeOnly: true})
	if len(r.Externals) != 1 || r.Externals[0].Confidence != "possible" {
		t.Fatalf("externals: %+v", r.Externals)
	}
	if text := file(t, r, "pacto.yaml").Content; strings.Contains(text, "twilio") || strings.Contains(text, "inferred externals") {
		t.Errorf("a possible match was written:\n%s", text)
	}
	if !hasLine(r.Unknown, "k8s/d.yaml, env TWILIO_ACCOUNT suggests twilio: nothing else in the folder confirms it") {
		t.Errorf("unknown: %q", r.Unknown)
	}
}

// TestPactifyCommentedEntriesKeepContract: the inferred externals are
// comments, so the parsed contract and its version are those of a run with no
// externals, even when a values key tries to end the comment.
func TestPactifyCommentedEntriesKeepContract(t *testing.T) {
	parsed := func(r *Result) *contract.Contract {
		t.Helper()
		c, err := contract.Parse(strings.NewReader(file(t, r, "pacto.yaml").Content))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	none := externalcatalog.Merge(&externalcatalog.File{APIVersion: externalcatalog.APIVersion}, externalcatalog.Ref{Digest: "sha256:none"}, nil, nil)
	base := parsed(planExternals(t, Options{Dir: fixtureDir(t, "externals"), Catalog: none}))
	r := planExternals(t, Options{Dir: fixtureDir(t, "externals")})
	text := file(t, r, "pacto.yaml").Content
	for _, want := range []string{
		"  # inferred externals: uncomment the ones this service calls and set the version\n",
		"  # - name: stripe  # k8s/deployment.yaml, env STRIPE_API_BASE (likely, catalog " + r.Catalog + ", entry stripe)\n  #   external: true\n  #   required: true\n  #   version: <the API version you call>\n",
		"  # - name: rds  # chart/values.yaml:3 DATABASE_URL (likely, catalog " + r.Catalog + ", entry aws/rds, identity aws/rds)\n",
		"  # - name: corp-legacy-co-uk  # chart/values.yaml:4 ERP_HOST (likely, catalog " + r.Catalog + ", unclassified, identity host/corp-legacy.co.uk)\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("no %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "# dependencies:") || !hasLine(r.Next, "uncomment the ones checkout calls") {
		t.Errorf("header or next: %q\n%s", r.Next, text)
	}
	got := parsed(r)
	if !slices.EqualFunc(got.Dependencies, base.Dependencies, func(a, b contract.Dependency) bool { return a.Name == b.Name && a.Ref == b.Ref }) || got.PactoVersion != base.PactoVersion {
		t.Errorf("dependencies %+v / %+v, version %s / %s", got.Dependencies, base.Dependencies, got.PactoVersion, base.PactoVersion)
	}

	// A values key holding a newline cannot write an uncommented entry, and
	// with no dependency the block says where it goes.
	dir := writeTree(t, t.TempDir(), map[string]string{
		"Dockerfile":  "FROM scratch\n",
		"Chart.yaml":  "apiVersion: v2\nname: pay\nversion: 1.0.0\n",
		"values.yaml": "\"x\\n- name: evil\\n  external: true\": https://api.stripe.com\n",
	})
	r = planExternals(t, Options{Dir: dir, DescribeOnly: true})
	text = file(t, r, "pacto.yaml").Content
	c, err := contract.Parse(strings.NewReader(text))
	if err != nil || len(c.Dependencies) != 0 || strings.Contains(text, "\n- name: evil") || !strings.Contains(text, "# dependencies:\n") ||
		!strings.Contains(text, "values.yaml:1 x?- name: evil?  external: true (likely") {
		t.Fatalf("injected entry: %v %+v\n%s", err, c, text)
	}
}

// TestPactifyRedactsCredentialURL: the credential URL, the JWT and the
// dotted password reach no byte pactify generates except as the normalized
// host.
func TestPactifyRedactsCredentialURL(t *testing.T) {
	r := planExternals(t, Options{Dir: fixtureDir(t, "externals"), Registry: "registry.example/shop", Owner: "shop"})
	out, _ := json.Marshal(r.Externals)
	all := string(out) + strings.Join(r.Found, "\n") + strings.Join(r.Unknown, "\n") + strings.Join(r.Next, "\n") + r.Catalog
	for _, f := range r.Files {
		all += f.Content
	}
	if !strings.Contains(file(t, r, "pacto.yaml").Content, "inferred externals") {
		t.Fatal("pacto.yaml has no inferred externals")
	}
	assertNoCredential(t, all)
	if !strings.Contains(all, "db.xyz.rds.amazonaws.com") {
		t.Error("the normalized host is missing")
	}
	for _, e := range r.Externals {
		if strings.Contains(e.Where, "API_TOKEN") || strings.Contains(e.Where, "SESSION_TOKEN") || strings.Contains(e.Where, "password") {
			t.Errorf("an external came from a secret value: %+v", e)
		}
	}
}

// assertNoCredential fails when s holds any part of the redaction fixtures
// other than the host.
func assertNoCredential(t *testing.T, s string) {
	t.Helper()
	for _, bad := range []string{"user:", "pass@", "/app", "password", "eyJhbGciOi", "s3cr3t"} {
		if strings.Contains(s, bad) {
			t.Errorf("%q leaked", bad)
		}
	}
}

// TestPactifyLineSeparatorKeyStaysComment: a values key holding U+2028 or
// U+2029, which YAML reads as a line break, cannot end the comment and
// declare a dependency, and the text lines carry no such rune either.
func TestPactifyLineSeparatorKeyStaysComment(t *testing.T) {
	for _, sep := range []string{`\L`, `\P`} {
		evil := "stripe_url" + sep + "  - name: evil" + sep + "    external: true" + sep + "    required: true #"
		dir := writeTree(t, t.TempDir(), map[string]string{
			"Dockerfile":       "FROM scratch\n",
			"chart/Chart.yaml": "apiVersion: v2\nname: pay\nversion: 1.0.0\n",
			"chart/values.yaml": "\"" + evil + "\": https://api.stripe.com\n" +
				"\"vendor" + sep + "x\": https://acme.io:8443\n",
		})
		r := planExternals(t, Options{Dir: dir, DescribeOnly: true})
		text := file(t, r, "pacto.yaml").Content
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
			t.Fatal(err)
		}
		c, err := contract.Parse(strings.NewReader(text))
		if err != nil || len(c.Dependencies) != 0 || doc["dependencies"] != nil {
			t.Fatalf("%s: injected entry: %v %+v\n%s", sep, err, c, text)
		}
		all := text + strings.Join(r.Found, "\n") + strings.Join(r.Unknown, "\n")
		for _, e := range r.Externals {
			all += e.Where
		}
		if strings.ContainsAny(all, "\u2028\u2029") {
			t.Errorf("%s: a line separator reached the output:\n%s", sep, all)
		}
	}
}

// TestPactifyIgnoredValuesNotRead: a chart values.yaml .pactoignore leaves
// out is not read.
func TestPactifyIgnoredValuesNotRead(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"Dockerfile":        "FROM scratch\n",
		".pactoignore":      "chart/values.yaml\n",
		"chart/Chart.yaml":  "apiVersion: v2\nname: pay\nversion: 1.0.0\n",
		"chart/values.yaml": "stripe_url: https://api.stripe.com\nvendor: {url: 'https://acme.io:8443'}\n",
	})
	r := planExternals(t, Options{Dir: dir, DescribeOnly: true})
	if len(r.Externals) != 0 || hasLine(r.Unknown, "values.yaml") {
		t.Errorf("an ignored values.yaml was read: %+v %q", r.Externals, r.Unknown)
	}
}

// TestPactifyChartUnidentifiedHostUnknown: a chart value naming a host no
// catalog entry identifies is named in Unknown by its key, never its value,
// as an env value is; a sensitive key's value is never parsed, so it gives no
// such line.
func TestPactifyChartUnidentifiedHostUnknown(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"Dockerfile":        "FROM scratch\n",
		"chart/Chart.yaml":  "apiVersion: v2\nname: pay\nversion: 1.0.0\n",
		"chart/values.yaml": "payments: {url: 'https://acme.io/v1'}\nlegacy: {host: 'db1.corp.local:5432'}\nDATABASE_DSN: 'postgres://u:p@db.xyz.rds.amazonaws.com:5432/app'\n",
		"k8s/d.yaml":        "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: pay}\nspec: {template: {spec: {containers: [{name: pay, image: 'r.example/pay:1.0.0', env: [{name: DATABASE_DSN, value: 'postgres://u:p@db.xyz.rds.amazonaws.com:5432/app'}]}]}}}\n",
	})
	r := planExternals(t, Options{Dir: dir, DescribeOnly: true})
	for _, want := range []string{"chart/values.yaml:1 url names a host outside the cluster", "chart/values.yaml:2 host names a host outside the cluster"} {
		if !hasLine(r.Unknown, want) {
			t.Errorf("no %q in %q", want, r.Unknown)
		}
	}
	if hasLine(r.Unknown, "DATABASE_DSN") || strings.Contains(strings.Join(r.Unknown, "\n"), "acme.io") {
		t.Errorf("a sensitive or raw value was characterised: %q", r.Unknown)
	}
}
