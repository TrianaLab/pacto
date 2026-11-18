package pactify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// leakFixture is a chart folder whose Deployment sets sensitive literals, reads
// one from a Secret and one from a ConfigMap in the folder.
var leakFixture = map[string]string{
	"Chart.yaml":  "apiVersion: v2\nname: payments\nversion: 1.0.0\n",
	"values.yaml": "db:\n  password: v4lues-s3cret\n",
	"deploy/app.yaml": `apiVersion: apps/v1
kind: Deployment
metadata: {name: payments}
spec:
  template:
    spec:
      containers:
        - name: payments
          image: reg.io/payments:1.0.0
          envFrom:
            - {configMapRef: {name: cm}}
          env:
            - {name: DB_PASSWORD, value: hunter2}
            - {name: DATABASE_URL, value: "postgres://app:u4l-s3cret@db:5432/x"}
            - {name: PORT, value: "8080"}
            - {name: DEBUG, value: "true"}
            - {name: LOG_LEVEL, value: info}
            - {name: API_TOKEN, valueFrom: {secretKeyRef: {name: creds, key: token}}}
`,
	"deploy/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: cm}\ndata: {CLIENT_SECRET: c0nfigmap-s3cret}\n",
}

// leakedValues are the fixture's secret bytes: none may reach anything Pacto
// writes or prints.
var leakedValues = []string{"hunter2", "u4l-s3cret", "v4lues-s3cret", "c0nfigmap-s3cret"}

const sensitiveDescription = "Sensitive: pacto init does not copy its value."

// assertNoLeak fails when a secret byte is in a planned file, in the result's
// JSON or in a file on disk Pacto added or appended to.
func assertNoLeak(t *testing.T, r *Result) {
	t.Helper()
	data, _ := json.Marshal(r)
	for _, v := range leakedValues {
		if strings.Contains(string(data), v) {
			t.Errorf("the result holds %q", v)
		}
		for _, f := range r.Files {
			if strings.Contains(f.Content, v) {
				t.Errorf("%s holds %q", f.Path, v)
			}
			if f.Action != Add && f.Action != Append {
				continue
			}
			if disk, err := os.ReadFile(filepath.Join(r.Dir, f.Path)); err == nil && strings.Contains(string(disk), v) {
				t.Errorf("%s on disk holds %q", f.Path, v)
			}
		}
	}
}

// TestConfigurationKeysAndTypesOnly: the inferred schema carries keys and
// types, never a value; sensitive and Secret-sourced keys are writeOnly, and a
// literal sensitive value in a folder file is named as a next step.
func TestConfigurationKeysAndTypesOnly(t *testing.T) {
	for _, tc := range []struct{ plan, describe bool }{{true, true}, {true, false}, {false, true}, {false, false}} {
		t.Run(fmt.Sprintf("plan=%v,describe=%v", tc.plan, tc.describe), func(t *testing.T) {
			dir := writeTree(t, t.TempDir(), leakFixture)
			r, err := Init(context.Background(), Options{Dir: dir, Plan: tc.plan, DescribeOnly: tc.describe}, os.WriteFile)
			if err != nil {
				t.Fatal(err)
			}
			raw := file(t, r, configSchema).Content
			if strings.Contains(raw, `"default"`) {
				t.Fatalf("the schema has a default:\n%s", raw)
			}
			var schema struct {
				Properties map[string]map[string]any `json:"properties"`
			}
			if err := json.Unmarshal([]byte(raw), &schema); err != nil {
				t.Fatal(err)
			}
			want := map[string]map[string]any{
				"DB_PASSWORD":   {"type": "string", "writeOnly": true, "description": sensitiveDescription},
				"DATABASE_URL":  {"type": "string", "writeOnly": true, "description": sensitiveDescription},
				"CLIENT_SECRET": {"type": "string", "writeOnly": true, "description": sensitiveDescription},
				"API_TOKEN":     {"type": "string", "writeOnly": true, "description": "Read from the Secret creds, whose values pacto init does not read."},
				"PORT":          {"type": "integer"},
				"DEBUG":         {"type": "boolean"},
				"LOG_LEVEL":     {"type": "string"},
			}
			for k, w := range want {
				if fmt.Sprint(schema.Properties[k]) != fmt.Sprint(w) {
					t.Errorf("%s = %v, want %v", k, schema.Properties[k], w)
				}
			}
			if !hasLine(r.Next, "deploy/app.yaml sets DATABASE_URL, DB_PASSWORD to literal values") ||
				!hasLine(r.Next, "deploy/cm.yaml sets CLIENT_SECRET to a literal value") ||
				!hasLine(r.Found, "sensitive keys, marked writeOnly: API_TOKEN, CLIENT_SECRET, DATABASE_URL, DB_PASSWORD") ||
				!hasLine(r.Found, "with their types and no values") {
				t.Errorf("findings: next %v found %v", r.Next, r.Found)
			}
			if !hasDependency(r, "db") {
				t.Errorf("db is not a dependency: %+v", r.Dependencies)
			}
			assertNoLeak(t, r)
		})
	}
}

func hasDependency(r *Result, name string) bool {
	for _, d := range r.Dependencies {
		if d.Name == name {
			return true
		}
	}
	return false
}

// TestManyKeysAndTypesOnly: several folders go through the same generator.
func TestManyKeysAndTypesOnly(t *testing.T) {
	root := t.TempDir()
	a := writeTree(t, filepath.Join(root, "a"), map[string]string{"app.yaml": `apiVersion: apps/v1
kind: Deployment
metadata: {name: a}
spec:
  template:
    spec:
      containers:
        - name: a
          image: reg.io/a:1.0.0
          env:
            - {name: DB_PASSWORD, value: hunter2}
`})
	b := writeTree(t, filepath.Join(root, "b"), map[string]string{"app.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: b}\nspec:\n  template:\n    spec:\n      containers:\n        - {name: b, image: reg.io/b:1.0.0}\n"})
	bulk, err := Many(context.Background(), Options{Plan: true}, []string{a, b}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(bulk)
	if strings.Contains(string(data), "hunter2") {
		t.Fatalf("the bulk result holds the value: %s", data)
	}
	for _, r := range bulk.Results {
		assertNoLeak(t, r)
	}
}

// TestShadowedLiteralNamed: a sensitive literal a later variable of the same
// name replaces (a sidecar's secretKeyRef, an env entry over an envFrom
// ConfigMap key) is still in its file, so the next steps still name it; a
// malformed credential URL is named too.
func TestShadowedLiteralNamed(t *testing.T) {
	dir := writeTree(t, t.TempDir(), map[string]string{
		"deploy/app.yaml": `apiVersion: apps/v1
kind: Deployment
metadata: {name: payments}
spec:
  template:
    spec:
      containers:
        - name: payments
          image: reg.io/payments:1.0.0
          envFrom:
            - {configMapRef: {name: cm}}
          env:
            - {name: DB_PASSWORD, value: hunter2}
            - {name: CLIENT_SECRET, valueFrom: {secretKeyRef: {name: creds, key: client}}}
            - {name: CACHE_URL, value: "postgres://app:pa%ss@cache:5432/x"}
            - {name: SKIPPED, valueFrom: {configMapKeyRef: {name: absent, key: k}}}
        - name: sidecar
          image: reg.io/sidecar:1.0.0
          env:
            - {name: DB_PASSWORD, valueFrom: {secretKeyRef: {name: db-creds, key: password}}}
            - {name: CM_TOKEN, valueFrom: {configMapKeyRef: {name: cm, key: CM_TOKEN}}}
`,
		"deploy/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: cm}\ndata: {CLIENT_SECRET: c0nfigmap-s3cret, CM_TOKEN: t0ken}\n",
	})
	r, err := Init(context.Background(), Options{Dir: dir, Plan: true}, os.WriteFile)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLine(r.Next, "deploy/app.yaml sets CACHE_URL, DB_PASSWORD to literal values") ||
		!hasLine(r.Next, "deploy/cm.yaml sets CLIENT_SECRET, CM_TOKEN to literal values") {
		t.Fatalf("next steps: %q", r.Next)
	}
	if strings.Contains(fmt.Sprint(r.Next), "hunter2") || strings.Contains(fmt.Sprint(r.Next), "pa%ss") {
		t.Fatalf("a next step holds a value: %q", r.Next)
	}
}
