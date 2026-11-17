package discover

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

const templatesDir = "testdata/templates"

// entriesOf are the entries of r for field, before the merge, as
// "<value> @ <source>".
func entriesOf(r Reading, field string) []string {
	var out []string
	for _, e := range r.entries {
		if e.Field == field {
			out = append(out, e.Value+" @ "+e.Source)
		}
	}
	slices.Sort(out)
	return out
}

func TestTemplateLiteralPort(t *testing.T) {
	r := readFixture(t, templatesDir)
	f := field(t, r.Fields, "ports[9100]")
	if f.Value != "9100/tcp" || f.Source != "chart/templates/deployment.yaml:13" || f.Confidence != contract.ConfidenceLikely || f.Declare != "" {
		t.Errorf("ports[9100] = %+v", f)
	}
}

func TestTemplateValuesPort(t *testing.T) {
	r := readFixture(t, templatesDir)
	f := field(t, r.Fields, "ports[8443]")
	if f.Value != "8443/tcp" || f.Source != "chart/templates/deployment.yaml:12" {
		t.Errorf("ports[8443] = %+v", f)
	}
}

func TestTemplateImageWithDefaultAppVersion(t *testing.T) {
	r := readFixture(t, templatesDir)
	want := []string{"ghcr.io/acme/payments @ chart/values.yaml:2", "ghcr.io/acme/payments:3.1.0 @ chart/templates/deployment.yaml:10"}
	if got := entriesOf(r, "image"); !slices.Equal(got, want) {
		t.Errorf("image entries = %v, want %v", got, want)
	}
	// Helm values outrank a template.
	if f := field(t, r.Fields, "image"); f.Source != "chart/values.yaml:2" || !slices.Equal(f.Outranks, []string{"chart/templates/deployment.yaml:10"}) {
		t.Errorf("image = %+v", f)
	}
}

func TestTemplateEnvNames(t *testing.T) {
	r := readFixture(t, templatesDir)
	log := field(t, r.Fields, "configuration.LOG_LEVEL")
	if log.Value != "string" || log.Source != "chart/templates/deployment.yaml:15" {
		t.Errorf("LOG_LEVEL = %+v", log)
	}
	if pw := field(t, r.Fields, "configuration.DB_PASSWORD"); pw.Value != "string, writeOnly" || pw.Source != "chart/templates/deployment.yaml:17" {
		t.Errorf("DB_PASSWORD = %+v", pw)
	}
	// The secretKeyRef's name is not an environment variable.
	if slices.ContainsFunc(r.Fields, func(f contract.Inferred) bool { return f.Field == "configuration.db" }) {
		t.Errorf("a secretKeyRef name was read as a key: %+v", r.Fields)
	}
}

func TestTemplateUnresolvedOncePerFile(t *testing.T) {
	r := readFixture(t, templatesDir)
	got := findingsOf(r, CodeUnresolved)
	want := []contract.InferredFinding{
		{Code: CodeUnresolved, Source: "chart/templates/deployment.yaml:23", Message: unresolvedTemplateMessage("chart/templates/deployment.yaml:23")},
		{Code: CodeUnresolved, Source: "chart/templates/worker.yaml:10", Message: unresolvedTemplateMessage("chart/templates/worker.yaml:10")},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("findings = %+v, want %+v", got, want)
	}
	if !strings.Contains(got[1].Message, "chart/templates/worker.yaml") || strings.Contains(got[1].Message, "{{") {
		t.Errorf("message = %q", got[1].Message)
	}
	rec, err := CompleteIn(templatesDir, templatesDir, nil, contract.InferredInputs{})
	if err != nil || rec == nil {
		t.Fatalf("record = %v, %v", rec, err)
	}
	if out := rec.Marshal(); bytes.Contains(out, []byte("s3cr3t-values")) || bytes.Contains(out, []byte("payments.port")) {
		t.Errorf("a template expression reached the record:\n%s", out)
	}
	if slices.ContainsFunc(r.Fields, func(f contract.Inferred) bool {
		return f.Source == "chart/templates/worker.yaml:10" || f.Source == "chart/templates/worker.yaml:12"
	}) {
		t.Errorf("an unresolved line was recorded: %+v", r.Fields)
	}
}

func TestTplFilesSkipped(t *testing.T) {
	r := readFixture(t, templatesDir)
	if slices.ContainsFunc(r.Fields, func(f contract.Inferred) bool { return strings.Contains(f.Source, "_helpers.tpl") }) ||
		slices.ContainsFunc(r.Findings, func(f contract.InferredFinding) bool { return strings.Contains(f.Source, "_helpers.tpl") }) {
		t.Errorf("_helpers.tpl was read: %+v %+v", r.Fields, r.Findings)
	}
	if len(entriesOf(r, "ports[7000]")) != 0 {
		t.Errorf("a port in _helpers.tpl was read")
	}
}

func TestTemplateValueIsLiteralOnly(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{`containerPort: 8080`, "8080"},
		{`containerPort: "{{ .Values.a.b }}"`, "7"},
		{`image: {{ .Chart.Name }}:{{ .Chart.AppVersion }}`, "payments:3.1.0"},
		{`image: '{{- .Values.img -}}:{{ .Values.tag | default .Chart.AppVersion }}'`, "repo/x:3.1.0"},
		{`image: {{ .Values.missing }}`, ""},
		{`image: {{ .Values.a }}`, ""},
		{`image: {{ .Values.a.b | quote }}`, ""},
		{`image: {{ .Values.empty | default "x" }}`, ""},
		{`image: {{ .Values.empty }}`, ""},
		{`image: {{ .Release.Name }}`, ""},
		{`image: {{ .Values.a.b`, ""},
	} {
		values := `{"a": {"b": 7}, "img": "repo/x", "tag": "", "empty": ""}`
		r := readFixture(t, writeFiles(t, map[string]string{
			"Chart.yaml": "name: payments\nappVersion: 3.1.0\n", "values.yaml": values,
			"templates/d.yaml": "kind: Deployment\n" + tc.line + "\n",
		}))
		var got []string
		for _, e := range r.entries {
			if strings.HasPrefix(e.Source, "templates/") {
				_, v, _ := strings.Cut(e.Field+"="+e.Value, "=")
				got = append(got, strings.TrimSuffix(v, "/tcp"))
			}
		}
		unresolved := len(findingsOf(r, CodeUnresolved))
		switch {
		case tc.want == "" && (len(got) != 0 || unresolved != 1):
			t.Errorf("%s: got %v, %d findings; want unresolved", tc.line, got, unresolved)
		case tc.want != "" && (!slices.Equal(got, []string{tc.want}) || unresolved != 0):
			t.Errorf("%s: got %v, %d findings; want %s", tc.line, got, unresolved, tc.want)
		}
	}
}

func TestTemplateEnvBlockEnds(t *testing.T) {
	tmpl := "spec:\n  containers:\n    - env:\n      - name: A\n        valueFrom:\n          secretKeyRef:\n            name: s\n          items:\n            - name: nested\n      - name: B\n" +
		"      {{- end }}\n\n      ports:\n        - name: C\n        - containerPort: http\n    - name: side\n"
	r := readFixture(t, writeFiles(t, map[string]string{"Chart.yaml": "name: x\n", "templates/d.yaml": tmpl}))
	var keys []string
	for _, f := range r.Fields {
		if k, ok := strings.CutPrefix(f.Field, "configuration."); ok {
			keys = append(keys, k)
		}
	}
	if !slices.Equal(keys, []string{"A", "B"}) || slices.ContainsFunc(r.Fields, func(f contract.Inferred) bool { return strings.HasPrefix(f.Field, "ports[") }) {
		t.Errorf("keys = %v, fields %+v", keys, r.Fields)
	}
}

func TestTemplateUnreadable(t *testing.T) {
	r := readFixture(t, writeFiles(t, map[string]string{"Chart.yaml": "name: x\n", "templates/d.yaml": strings.Repeat("x", maxFileSize+1)}))
	if got := findingsOf(r, CodeUnreadable); len(got) != 1 || got[0].Source != "templates/d.yaml" {
		t.Errorf("findings = %+v", got)
	}
}
