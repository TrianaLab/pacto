package discover

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

const (
	valuesDir          = "testdata/values"
	folderExternalsDir = "testdata/folder-externals"
)

// visited reads dir with a Values visitor and returns every value it got as
// "<kind> <path>:<line> <KEY>=<value>", and the reading.
func visited(t *testing.T, dir string) ([]string, Reading) {
	t.Helper()
	var got []string
	r, err := readDir(dir, dir, "", func(v Value) {
		got = append(got, fmt.Sprintf("%s %s:%d %s=%s", v.Kind, v.Path, v.Line, v.Key, v.Value))
	})
	if err != nil {
		t.Fatal(err)
	}
	return got, r
}

func TestHelmEnvValuesFilesRead(t *testing.T) {
	got, _ := visited(t, valuesDir)
	for _, want := range []string{
		"helmValues chart/values.yaml:1 STRIPE_API_BASE=https://api.stripe.com/v1",
		"helmValues chart/values-production.yaml:12 DATABASE_URL=postgres://db.xyz.rds.amazonaws.com:5432/app",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("no %q in %q", want, got)
		}
	}
}

func TestComposeHostsClassified(t *testing.T) {
	got, _ := visited(t, valuesDir)
	if !slices.Contains(got, "compose compose.yaml:5 SENDGRID_URL=https://api.sendgrid.com") {
		t.Errorf("map form: %q", got)
	}
	if slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, "hunter2") }) {
		t.Errorf("a sensitive key reached the visitor: %q", got)
	}
	dir := writeTree(t, t.TempDir(), map[string]string{"compose.yaml": "services:\n  app:\n    environment:\n      - SENDGRID_URL=https://api.sendgrid.com\n      - PLAIN\n"})
	got, _ = visited(t, dir)
	if !reflect.DeepEqual(got, []string{"compose compose.yaml:4 SENDGRID_URL=https://api.sendgrid.com", "compose compose.yaml:5 PLAIN="}) {
		t.Errorf("list form: %q", got)
	}
	rec, err := CompleteIn(dir, dir, nil, contract.InferredInputs{}, nil)
	if err != nil || !hasExternal(rec, "sendgrid", "compose", "compose.yaml", 4) {
		t.Errorf("compose external: %v %+v", err, rec)
	}
}

func TestComposeEnvFileStaysUnsupported(t *testing.T) {
	got, r := visited(t, valuesDir)
	if !slices.ContainsFunc(r.Findings, func(f contract.InferredFinding) bool {
		return f.Code == CodeUnsupportedFile && f.Source == "compose.yaml:7"
	}) {
		t.Errorf("no env_file finding: %+v", r.Findings)
	}
	if slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, ".env.compose") }) {
		t.Errorf("the env_file was read: %q", got)
	}
}

func TestKustomizeGeneratorHostsClassified(t *testing.T) {
	got, _ := visited(t, valuesDir)
	for _, want := range []string{
		"kustomize k8s/kustomization.yaml:4 TWILIO_URL=https://api.twilio.com",
		"kustomize k8s/cfg.env:3 GITHUB_URL=https://api.github.com",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("no %q in %q", want, got)
		}
	}
}

func TestKustomizeMixedGeneratorsReadsOnlyConfigMap(t *testing.T) {
	got, _ := visited(t, valuesDir)
	if !slices.Contains(got, "kustomize mixed/kustomization.yaml:4 PAGERDUTY_URL=https://api.pagerduty.com") {
		t.Errorf("the configMapGenerator of a mixed kustomization: %q", got)
	}
	if slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, "mailgun") }) {
		t.Errorf("a secretGenerator literal reached the visitor: %q", got)
	}
}

func TestKustomizeEnvsSymlinkNotRead(t *testing.T) {
	outside := writeTree(t, t.TempDir(), map[string]string{"secret.env": "STRIPE_URL=https://api.stripe.com\n"})
	dir := writeTree(t, t.TempDir(), map[string]string{"kustomization.yaml": "configMapGenerator:\n  - name: c\n    envs: [link.env, ../escape.env]\n"})
	if err := os.Symlink(filepath.Join(outside, "secret.env"), filepath.Join(dir, "link.env")); err != nil {
		t.Skip(err)
	}
	got, r := visited(t, dir)
	if len(got) != 0 {
		t.Errorf("a linked envs file was read: %q", got)
	}
	if n := countFindings(r, CodeUnresolved, "kustomization.yaml:3"); n != 1 { // one line names both, compacted
		t.Errorf("findings: %+v", r.Findings)
	}
}

func TestKustomizeEnvsPactoignoredNotRead(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"kustomization.yaml": "configMapGenerator:\n  - name: c\n    envs:\n      - app.env\n      - .hidden/app.env\n",
		"app.env":            "STRIPE_URL=https://api.stripe.com\n",
		".hidden/app.env":    "STRIPE_URL=https://api.stripe.com\n",
		".pactoignore":       "app.env\n",
	})
	got, r := visited(t, dir)
	if len(got) != 0 {
		t.Errorf("an ignored envs file was read: %q", got)
	}
	if countFindings(r, CodeUnresolved, "kustomization.yaml:4")+countFindings(r, CodeUnresolved, "kustomization.yaml:5") != 2 {
		t.Errorf("findings: %+v", r.Findings)
	}
}

func countFindings(r Reading, code, source string) int {
	n := 0
	for _, f := range r.Findings {
		if f.Code == code && f.Source == source {
			n++
		}
	}
	return n
}

func TestValueKeyPatternSkipsMalformedLines(t *testing.T) {
	got, _ := visited(t, valuesDir)
	if slices.ContainsFunc(got, func(s string) bool { return strings.Contains(s, "openai") }) {
		t.Errorf("a literal with no '=' reached the visitor: %q", got)
	}
	dir := writeTree(t, t.TempDir(), map[string]string{
		"compose.yaml": "services:\n  app:\n    environment:\n      - postgres://admin:pw-hunter2@host\n      'https://x.example.com': y\n",
	})
	got, r := visited(t, dir)
	if len(got) != 0 || bytes.Contains([]byte(fmt.Sprint(r.Fields)), []byte("pw-hunter2")) {
		t.Errorf("a URL used as a key was read: %q %+v", got, r.Fields)
	}
}

func TestValuesNilVisitorUnchanged(t *testing.T) {
	unresolved := writeTree(t, t.TempDir(), map[string]string{"kustomization.yaml": "configMapGenerator:\n  - name: c\n    envs: [missing.env]\n"})
	for _, dir := range []string{valuesDir, folderExternalsDir, composeDir, kustomizeDir, helmValuesDir, unresolved} {
		_, with := visited(t, dir)
		without, err := ReadDir(dir, dir, "")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(with.Fields, without.Fields) || !reflect.DeepEqual(with.Findings, without.Findings) {
			t.Errorf("%s: a visitor changed the reading", dir)
		}
	}
}

// hasExternal reports whether rec holds the external id read from path at
// line through a source of kind.
func hasExternal(rec *contract.InferredRecord, id, kind, path string, line int) bool {
	return rec != nil && slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool {
		return f.External != nil && f.External.Identity == id && slices.ContainsFunc(f.External.Sources, func(s contract.InferredExternalSource) bool {
			return s.Kind == kind && s.Path == path && s.Line == line
		})
	})
}

// TestEveryDottedHostIsClassifiedOrNamed: every dotted host the folder's
// configuration names reaches the record as an external or a finding, never
// neither.
func TestEveryDottedHostIsClassifiedOrNamed(t *testing.T) {
	rec := complete(t, folderExternalsDir, nil)
	for _, id := range []string{"stripe", "twilio", "host/corp-legacy.com"} {
		if !slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return f.External != nil && f.External.Identity == id }) {
			t.Errorf("no external %s in %+v", id, rec.Fields)
		}
	}
	if !slices.ContainsFunc(rec.Findings, func(f contract.InferredFinding) bool {
		return f.Code == CodeUnresolved && strings.HasPrefix(f.Message, "k8s/app.yaml:14 PARTNER_URL names a host outside the cluster")
	}) {
		t.Errorf("the unidentified host is not named: %+v", rec.Findings)
	}
	if bytes.Contains(rec.Marshal(), []byte("mailgun")) {
		t.Error("a secretGenerator literal reached the record")
	}
}

func TestFolderExternalsInRecordWithSourceLine(t *testing.T) {
	rec := complete(t, folderExternalsDir, nil)
	i := slices.IndexFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == "dependencies[stripe]" })
	if i < 0 {
		t.Fatalf("no stripe: %+v", rec.Fields)
	}
	f := rec.Fields[i]
	want := contract.InferredExternalSource{Kind: "helmValues", Path: "chart/values.yaml", Line: 2, Key: "STRIPE_API_BASE", Host: "api.stripe.com"}
	if f.Confidence != contract.ConfidenceLikely || f.Source != "chart/values.yaml:2" || f.External.Catalog != "default" || !reflect.DeepEqual(f.External.Sources, []contract.InferredExternalSource{want}) {
		t.Errorf("stripe = %+v %+v", f, f.External)
	}
	if !strings.Contains(f.Declare, "identity: stripe") {
		t.Errorf("declare = %q", f.Declare)
	}
	if bytes.Contains(rec.Marshal(), []byte("observed")) {
		t.Error("the record says observed")
	}
	if _, err := contract.ParseInferredRecord(rec.Marshal()); err != nil {
		t.Fatal(err)
	}
	// Declared by its identity under another name, it is left out.
	rec = complete(t, folderExternalsDir, &contract.Contract{Dependencies: []contract.Dependency{{Name: "payments-api", Identity: "stripe"}}})
	if slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == "dependencies[stripe]" }) {
		t.Error("a declared external is inferred")
	}
}

func TestFolderExternalsDeterministic(t *testing.T) {
	first := complete(t, folderExternalsDir, nil).Marshal()
	if again := complete(t, folderExternalsDir, nil).Marshal(); !bytes.Equal(first, again) {
		t.Error("two runs differ")
	}
	var files []string
	_ = filepath.WalkDir(folderExternalsDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	copyDir := filepath.Join(t.TempDir(), "folder-externals")
	for _, f := range slices.Backward(files) {
		data, _ := os.ReadFile(f)
		rel, _ := filepath.Rel(folderExternalsDir, f)
		writeTree(t, copyDir, map[string]string{rel: string(data)})
	}
	if reversed := complete(t, copyDir, nil).Marshal(); !bytes.Equal(first, reversed) {
		t.Errorf("a copy written in reverse order differs:\n%s\n%s", first, reversed)
	}
}

func TestCompleteRecordsCitedFileDigests(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"chart/Chart.yaml":  "name: c\n",
		"chart/values.yaml": "STRIPE_API_BASE: https://api.stripe.com\n",
		"terraform.tfstate": `{"password": "state-hunter2"}`,
	})
	rec := complete(t, dir, nil)
	var paths []string
	for _, s := range rec.Inputs.Sources {
		paths = append(paths, s.Path)
	}
	if !slices.Contains(paths, "chart/values.yaml") || slices.Contains(paths, "terraform.tfstate") {
		t.Errorf("sources = %q", paths)
	}
}

func TestCatalogInputsRecordDigestWithoutEmbeddedSuffix(t *testing.T) {
	rec := complete(t, folderExternalsDir, nil)
	if want := []string{"default " + externalcatalog.DefaultDigest()}; !reflect.DeepEqual(rec.Inputs.Catalog, want) {
		t.Errorf("catalog = %q, want %q", rec.Inputs.Catalog, want)
	}
	planned, err := CompletePlanned(folderExternalsDir, nil, nil, contract.InferredInputs{Commit: "4e1a9c0", Pacto: "4.6.0"}, nil)
	if err != nil || !bytes.Equal(planned.Marshal(), rec.Marshal()) {
		t.Errorf("CompletePlanned with nothing planned differs: %v", err)
	}
}

func TestExternalFieldNameFallsBack(t *testing.T) {
	xs := []External{
		{Identity: "aws/rds", Confidence: "possible", Kind: "env", Path: "a.yaml", Key: "RDS"},
		{Identity: "aws/rds", Confidence: "likely", Kind: "env", Path: "a.yaml", Line: 3, Key: "DB", Origin: "default"},
		{Identity: "aws/rds", Confidence: "likely", Kind: "env", Path: "a.yaml", Line: 3, Key: "DB", Origin: "default"},
	}
	cited := map[string]bool{}
	fields, _ := externalFields(xs, func(f string) bool { return f == "dependencies[rds]" }, func(string, string) bool { return false }, cited)
	if len(fields) != 1 || fields[0].Field != "dependencies[aws-rds]" || fields[0].Confidence != "likely" || fields[0].Source != "a.yaml:3" ||
		len(fields[0].External.Sources) != 2 || !cited["a.yaml"] {
		t.Errorf("fields = %+v", fields)
	}
	if DependencyName("host/corp.example") != "corp-example" || DependencyName("stripe") != "stripe" {
		t.Error("DependencyName")
	}
}

func TestExternalsSkipUnrecordablePaths(t *testing.T) {
	sc := &Scan{Env: []EnvVar{
		{File: "a:b.yaml", Name: "STRIPE_URL", Value: "https://api.stripe.com"},
		{File: "c.yaml", Source: "c.yaml:4", Name: "BAD NAME", Value: "https://api.stripe.com"},
		{File: "c.yaml", Name: "PARTNER", Value: "https://partner.io", From: "configmap"},
	}}
	xs, findings := sc.externals(nil, externalcatalog.Default())
	if len(xs) != 1 || xs[0].Path != "c.yaml" || xs[0].Line != 4 || xs[0].Key != "" {
		t.Errorf("externals = %+v", xs)
	}
	if len(findings) != 1 || findings[0].Source != "c.yaml" {
		t.Errorf("findings = %+v", findings)
	}
}

func TestUnidentifiedHostsBounded(t *testing.T) {
	env := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: x}\nspec: {template: {spec: {containers: [{name: x, env: ["
	for i := range MaxFindings + 2 {
		env += fmt.Sprintf("{name: H%d, value: 'https://h%d.io'},", i, i)
	}
	dir := writeTree(t, t.TempDir(), map[string]string{"d.yaml": env + "]}]}}}\n"})
	rec := complete(t, dir, nil)
	if len(rec.Findings) != MaxFindings || rec.FindingsDropped != 2 {
		t.Errorf("findings %d, dropped %d", len(rec.Findings), rec.FindingsDropped)
	}
}
