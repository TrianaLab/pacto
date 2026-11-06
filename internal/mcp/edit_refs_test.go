package mcp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/internal/app"
	"github.com/trianalab/pacto/v4/internal/testutil"
	"github.com/trianalab/pacto/v4/pkg/contract"
)

// refRoot writes a postgresql provider under root/pg whose provisioning
// configuration takes a size from an enum, and a consumer under root/orders
// with the given pacto.yaml and extra files. It returns the consumer's
// directory.
func refRoot(t *testing.T, pacto string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pg/pacto.yaml", "pactoVersion: \"2.6\"\nservice: {name: postgresql, version: 16.1.0}\nconfigurations:\n  - {name: provisioning, schema: provisioning.json, required: false}\n")
	write("pg/provisioning.json", `{"type":"object","properties":{"size":{"enum":["small","large"]},"extra":{"type":"object"}},"required":["size"]}`)
	write("orders/pacto.yaml", pacto)
	for rel, content := range files {
		write("orders/"+rel, content)
	}
	return filepath.Join(root, "orders")
}

// realValidator is the `pacto validate` path the command layer hands the
// server; with no registry it resolves local refs only.
var realValidator = app.NewService(&testutil.MockBundleStore{}, nil).ValidationReport

const (
	orders25 = "pactoVersion: \"2.5\"\nservice: {name: orders, version: 1.0.0}\n"
	orders26 = "pactoVersion: \"2.6\"\nservice: {name: orders, version: 1.0.0}\n" +
		"configurations:\n  - name: db\n    ref: ../pg\n    required: true\n    values: {size: small}\n"
	ordersEnv = orders26 + "environments:\n  - {name: prod, file: environments/prod.yaml}\n"
)

func readContract(t *testing.T, dir string) *contract.Contract {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "pacto.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := contract.Parse(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func readEnv(t *testing.T, dir, rel string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	v, err := contract.ParseEnvironmentValues(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// editCase is one pacto_edit call over a refRoot bundle: the changes it
// reports and what it leaves on disk.
type editCase struct {
	name    string
	pacto   string
	files   map[string]string
	input   EditInput
	changes []string
	check   func(t *testing.T, dir string)
}

// runEditCases runs each case with the real validator and checks the written
// bundle still passes pacto_check.
func runEditCases(t *testing.T, tests []editCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := refRoot(t, tt.pacto, tt.files)
			tt.input.Path = dir
			result, err := Edit(context.Background(), realValidator, tt.input)
			if err != nil {
				t.Fatalf("edit: %v", err)
			}
			if !slices.Equal(result.Changes, tt.changes) {
				t.Errorf("changes %q, want %q", result.Changes, tt.changes)
			}
			tt.check(t, dir)
			if check, err := Check(context.Background(), realValidator, dir); err != nil || !check.Valid {
				t.Errorf("written bundle does not check: %+v %v", check, err)
			}
			assertNoStagingLeft(t, dir)
		})
	}
}

func TestEdit_ConfigurationRefOps(t *testing.T) {
	runEditCases(t, []editCase{
		{
			name:    "values raise to 2.6",
			pacto:   orders25,
			input:   EditInput{AddConfigurationRefs: []ConfigurationRefInput{{Name: "db", Ref: "../pg", Required: true, Values: map[string]any{"size": "small"}}}},
			changes: []string{`set pactoVersion to "2.6"`, `added configuration reference "db"`},
			check: func(t *testing.T, dir string) {
				c := readContract(t, dir)
				if c.PactoVersion != "2.6" || !reflect.DeepEqual(c.Configurations[0].Values, map[string]any{"size": "small"}) {
					t.Errorf("contract %+v", c)
				}
			},
		},
		{
			name:    "selector raises to 2.6",
			pacto:   orders25,
			input:   EditInput{AddConfigurationRefs: []ConfigurationRefInput{{Ref: "../pg", Configuration: "provisioning", Values: map[string]any{"size": "large"}}}},
			changes: []string{`set pactoVersion to "2.6"`, `added configuration reference "pg"`},
			check: func(t *testing.T, dir string) {
				if cfg := readContract(t, dir).Configurations[0]; cfg.ProviderConfiguration != "provisioning" || cfg.Name != "pg" {
					t.Errorf("configuration %+v", cfg)
				}
			},
		},
		{
			name:    "remove by name",
			pacto:   orders26,
			input:   EditInput{RemoveConfigurationRefs: []string{"db"}},
			changes: []string{`removed configuration reference "db"`},
			check: func(t *testing.T, dir string) {
				if c := readContract(t, dir); len(c.Configurations) != 0 {
					t.Errorf("configurations %+v", c.Configurations)
				}
			},
		},
		{
			name:    "remove by ref keeps the others and drops its environment values",
			pacto:   ordersEnv + "  - {name: dev, file: environments/dev.yaml}\n",
			files:   map[string]string{"environments/prod.yaml": "db: {size: large}\n", "environments/dev.yaml": "{}\n"},
			input:   EditInput{RemoveConfigurationRefs: []string{"../pg"}},
			changes: []string{`removed configuration reference "db"`, `removed values for configuration "db" from environment "prod"`},
			check: func(t *testing.T, dir string) {
				if v := readEnv(t, dir, "environments/prod.yaml"); len(v) != 0 {
					t.Errorf("prod values %v", v)
				}
			},
		},
		{
			name:    "remove one of two",
			pacto:   orders26 + "  - {name: app, schema: config.json, required: false}\n",
			files:   map[string]string{"config.json": `{"type":"object"}`},
			input:   EditInput{RemoveConfigurationRefs: []string{"db"}},
			changes: []string{`removed configuration reference "db"`},
			check: func(t *testing.T, dir string) {
				if c := readContract(t, dir); len(c.Configurations) != 1 || c.Configurations[0].Name != "app" {
					t.Errorf("configurations %+v", c.Configurations)
				}
			},
		},
	})
}

func TestEdit_EnvironmentOps(t *testing.T) {
	runEditCases(t, []editCase{
		{
			name:    "add environment creates its file and raises to 2.3",
			pacto:   "pactoVersion: \"2.0\"\nservice: {name: orders, version: 1.0.0}\n",
			input:   EditInput{AddEnvironments: []EnvironmentInput{{Name: "prod"}, {Name: "dev", File: "envs/dev.yaml"}}},
			changes: []string{`set pactoVersion to "2.3"`, `added environment "prod" (environments/prod.yaml)`, `added environment "dev" (envs/dev.yaml)`},
			check: func(t *testing.T, dir string) {
				for _, rel := range []string{"environments/prod.yaml", "envs/dev.yaml"} {
					if raw, err := os.ReadFile(filepath.Join(dir, rel)); err != nil || string(raw) != "{}\n" {
						t.Errorf("%s: %q %v", rel, raw, err)
					}
				}
				if c := readContract(t, dir); c.PactoVersion != "2.3" || len(c.Environments) != 2 {
					t.Errorf("contract %+v", c)
				}
			},
		},
		{
			name:    "add environment keeps a file already on disk",
			pacto:   orders26,
			files:   map[string]string{"environments/prod.yaml": "# prod\ndb: {size: large}\n"},
			input:   EditInput{AddEnvironments: []EnvironmentInput{{Name: "prod"}}},
			changes: []string{`added environment "prod" (environments/prod.yaml)`},
			check: func(t *testing.T, dir string) {
				if raw, _ := os.ReadFile(filepath.Join(dir, "environments/prod.yaml")); string(raw) != "# prod\ndb: {size: large}\n" {
					t.Errorf("file rewritten: %q", raw)
				}
			},
		},
		{
			name:  "set values deep-merges and a null deletes",
			pacto: ordersEnv + "  - {name: dev, file: environments/dev.yaml}\n",
			files: map[string]string{"environments/prod.yaml": "db:\n  size: large\n  extra: {a: 1, b: 2}\n", "environments/dev.yaml": "{}\n"},
			input: EditInput{SetEnvironmentValues: []EnvironmentValuesInput{{Environment: "prod", Configuration: "db",
				Values: map[string]any{"size": "small", "extra": map[string]any{"a": nil, "c": 3}}}}},
			changes: []string{`set values for configuration "db" in environment "prod"`},
			check: func(t *testing.T, dir string) {
				want := map[string]any{"db": map[string]any{"size": "small", "extra": map[string]any{"b": float64(2), "c": float64(3)}}}
				if v := readEnv(t, dir, "environments/prod.yaml"); !reflect.DeepEqual(v, want) {
					t.Errorf("values %v, want %v", v, want)
				}
			},
		},
		{
			name:    "nulling every key drops the configuration",
			pacto:   ordersEnv,
			files:   map[string]string{"environments/prod.yaml": "db: {size: large}\n"},
			input:   EditInput{SetEnvironmentValues: []EnvironmentValuesInput{{Environment: "prod", Configuration: "db", Values: map[string]any{"size": nil}}}},
			changes: []string{`set values for configuration "db" in environment "prod"`},
			check: func(t *testing.T, dir string) {
				if v := readEnv(t, dir, "environments/prod.yaml"); len(v) != 0 {
					t.Errorf("values %v", v)
				}
			},
		},
		{
			name:  "values go to an environment the same edit adds",
			pacto: orders26,
			input: EditInput{
				AddEnvironments:      []EnvironmentInput{{Name: "prod"}},
				SetEnvironmentValues: []EnvironmentValuesInput{{Environment: "prod", Configuration: "db", Values: map[string]any{"size": "large"}}},
			},
			changes: []string{`added environment "prod" (environments/prod.yaml)`, `set values for configuration "db" in environment "prod"`},
			check: func(t *testing.T, dir string) {
				if v := readEnv(t, dir, "environments/prod.yaml"); !reflect.DeepEqual(v, map[string]any{"db": map[string]any{"size": "large"}}) {
					t.Errorf("values %v", v)
				}
			},
		},
	})
}

func TestEdit_LifecycleOps(t *testing.T) {
	runEditCases(t, []editCase{
		{
			name:    "lifecycle set raises to 2.6",
			pacto:   orders25,
			input:   EditInput{Lifecycle: &LifecycleInput{Deprecated: "2026-01-01", Sunset: "2026-06-01", Successor: "oci://ghcr.io/acme/orders-v2"}},
			changes: []string{`set pactoVersion to "2.6"`, "set service lifecycle to deprecated 2026-01-01, sunset 2026-06-01, successor oci://ghcr.io/acme/orders-v2"},
			check: func(t *testing.T, dir string) {
				want := &contract.InterfaceLifecycle{Deprecated: "2026-01-01", Sunset: "2026-06-01", Successor: "oci://ghcr.io/acme/orders-v2"}
				if c := readContract(t, dir); !reflect.DeepEqual(c.Service.Lifecycle, want) {
					t.Errorf("lifecycle %+v", c.Service.Lifecycle)
				}
			},
		},
		{
			name:    "lifecycle clear",
			pacto:   "pactoVersion: \"2.6\"\nservice:\n  name: orders\n  version: 1.0.0\n  lifecycle: {deprecated: \"2026-01-01\"}\n",
			input:   EditInput{Lifecycle: &LifecycleInput{Clear: true}},
			changes: []string{"removed service lifecycle"},
			check: func(t *testing.T, dir string) {
				if c := readContract(t, dir); c.Service.Lifecycle != nil || c.PactoVersion != "2.6" {
					t.Errorf("contract %+v", c)
				}
			},
		},
	})
}

// assertNoStagingLeft fails when an edit left a temp file in dir or a staged
// copy beside it.
func assertNoStagingLeft(t *testing.T, dir string) {
	t.Helper()
	for _, d := range []string{dir, filepath.Dir(dir)} {
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".pacto-") {
				t.Errorf("left %s in %s", e.Name(), d)
			}
		}
	}
}

func TestEdit_ReferenceAndEnvironmentErrors(t *testing.T) {
	tests := []struct {
		name  string
		pacto string
		files map[string]string
		input EditInput
		want  string
	}{
		{"unknown remove", orders26, nil, EditInput{RemoveConfigurationRefs: []string{"cache"}}, `no configuration reference named or pointing at "cache"`},
		{"remove a local-schema configuration", orders25 + "configurations:\n  - {name: app, schema: c.json, required: false}\n", nil,
			EditInput{RemoveConfigurationRefs: []string{"app"}}, `no configuration reference named or pointing at "app"`},
		{"duplicate environment", ordersEnv, nil, EditInput{AddEnvironments: []EnvironmentInput{{Name: "prod"}}}, `environment "prod" is already declared`},
		{"undeclared environment", orders26, nil,
			EditInput{SetEnvironmentValues: []EnvironmentValuesInput{{Environment: "prod", Configuration: "db"}}}, `environment "prod" is not declared; add it with add_environments`},
		{"undeclared configuration", ordersEnv, nil,
			EditInput{SetEnvironmentValues: []EnvironmentValuesInput{{Environment: "prod", Configuration: "cache"}}}, `configuration "cache" is not declared`},
		{"environment file outside the bundle", orders26 + "environments:\n  - {name: prod, file: ../prod.yaml}\n", nil,
			EditInput{Version: strPtr("1.0.1")}, `environment "prod": file "../prod.yaml" is outside the bundle`},
		{"environment file that does not read", ordersEnv, map[string]string{"environments/prod.yaml/x": ""},
			EditInput{Version: strPtr("1.0.1")}, "failed to read environments/prod.yaml"},
		{"environment file that does not parse", ordersEnv, map[string]string{"environments/prod.yaml": "- a\n"},
			EditInput{Version: strPtr("1.0.1")}, `environment "prod": cannot read environments/prod.yaml`},
		{"lifecycle with no deprecation", orders25, nil, EditInput{Lifecycle: &LifecycleInput{Sunset: "2026-06-01"}}, "INVALID_SERVICE_LIFECYCLE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := refRoot(t, tt.pacto, tt.files)
			tt.input.Path = dir
			_, err := Edit(context.Background(), realValidator, tt.input)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err %v, want %q", err, tt.want)
			}
			if raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml")); string(raw) != tt.pacto {
				t.Errorf("pacto.yaml changed:\n%s", raw)
			}
		})
	}
}

func TestEdit_DryRunWritesNothing(t *testing.T) {
	dir := refRoot(t, orders25, nil)
	result, err := Edit(context.Background(), realValidator, EditInput{
		Path:                 dir,
		DryRun:               true,
		AddConfigurationRefs: []ConfigurationRefInput{{Name: "db", Ref: "../pg", Values: map[string]any{"size": "small"}}},
		AddEnvironments:      []EnvironmentInput{{Name: "prod"}},
		SetEnvironmentValues: []EnvironmentValuesInput{{Environment: "prod", Configuration: "db", Values: map[string]any{"size": "large"}}},
		Lifecycle:            &LifecycleInput{Deprecated: "2026-01-01"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`set pactoVersion to "2.6"`, "set service lifecycle to deprecated 2026-01-01", `added configuration reference "db"`,
		`added environment "prod" (environments/prod.yaml)`, `set values for configuration "db" in environment "prod"`}
	if !slices.Equal(result.Changes, want) {
		t.Errorf("changes %q, want %q", result.Changes, want)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml")); string(raw) != orders25 {
		t.Errorf("pacto.yaml changed:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "environments")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("environments written: %v", err)
	}
	assertNoStagingLeft(t, dir)
}

func TestEditRejectsValuesTheProviderSchemaRejects(t *testing.T) {
	t.Run("inline values", func(t *testing.T) {
		dir := refRoot(t, orders25, nil)
		_, err := Edit(context.Background(), realValidator, EditInput{Path: dir,
			AddConfigurationRefs: []ConfigurationRefInput{{Name: "db", Ref: "../pg", Values: map[string]any{"size": "huge"}}}})
		if err == nil || !strings.Contains(err.Error(), "CONFIG_VALUES_VALIDATION_FAILED") {
			t.Fatalf("err %v", err)
		}
		if raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml")); string(raw) != orders25 {
			t.Errorf("pacto.yaml changed:\n%s", raw)
		}
		assertNoStagingLeft(t, dir)
	})
	t.Run("environment values", func(t *testing.T) {
		dir := refRoot(t, ordersEnv, map[string]string{"environments/prod.yaml": "db: {size: large}\n"})
		_, err := Edit(context.Background(), realValidator, EditInput{Path: dir,
			SetEnvironmentValues: []EnvironmentValuesInput{{Environment: "prod", Configuration: "db", Values: map[string]any{"size": "huge"}}}})
		if err == nil || !strings.Contains(err.Error(), "ENVIRONMENT_VALUES_INVALID") {
			t.Fatalf("err %v", err)
		}
		if raw, _ := os.ReadFile(filepath.Join(dir, "environments/prod.yaml")); string(raw) != "db: {size: large}\n" {
			t.Errorf("environment file changed: %q", raw)
		}
	})
	t.Run("an added environment whose file already holds rejected values", func(t *testing.T) {
		dir := refRoot(t, orders26, map[string]string{"environments/prod.yaml": "db: {size: huge}\n"})
		_, err := Edit(context.Background(), realValidator, EditInput{Path: dir, AddEnvironments: []EnvironmentInput{{Name: "prod"}}})
		if err == nil || !strings.Contains(err.Error(), "ENVIRONMENT_VALUES_INVALID") {
			t.Fatalf("err %v", err)
		}
		if raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml")); string(raw) != orders26 {
			t.Errorf("pacto.yaml changed:\n%s", raw)
		}
	})

}

func TestEditFailsClosedOnUnresolvableRefs(t *testing.T) {
	t.Run("an unresolvable ref fails closed", func(t *testing.T) {
		unreachable := app.NewService(&testutil.MockBundleStore{PullFn: func(context.Context, string) (*contract.Bundle, error) {
			return nil, errors.New("not found")
		}}, nil).ValidationReport
		for name, validate := range map[string]Validator{"wired": unreachable, "fallback": nil} {
			dir := refRoot(t, orders25, nil)
			_, err := Edit(context.Background(), validate, EditInput{Path: dir,
				AddConfigurationRefs: []ConfigurationRefInput{{Name: "db", Ref: "oci://ghcr.io/acme/pg:16.1.0", Values: map[string]any{"size": "small"}}}})
			if err == nil || !strings.Contains(err.Error(), "CONFIG_REF_UNRESOLVED") {
				t.Errorf("%s: err %v", name, err)
			}
		}
	})
	t.Run("a 2.6 ref added without values fails closed", func(t *testing.T) {
		unreachable := app.NewService(&testutil.MockBundleStore{PullFn: func(context.Context, string) (*contract.Bundle, error) {
			return nil, errors.New("not found")
		}}, nil).ValidationReport
		pacto := "pactoVersion: \"2.6\"\nservice: {name: orders, version: 1.0.0}\nenvironments:\n  - {name: prod, file: environments/prod.yaml}\n"
		dir := refRoot(t, pacto, map[string]string{"environments/prod.yaml": "{}\n"})
		_, err := Edit(context.Background(), unreachable, EditInput{Path: dir,
			AddConfigurationRefs: []ConfigurationRefInput{{Name: "db", Ref: "oci://ghcr.io/acme/pg:1.0.0"}}})
		if err == nil || !strings.Contains(err.Error(), "CONFIG_REF_UNRESOLVED") {
			t.Fatalf("err %v", err)
		}
		if raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml")); string(raw) != pacto {
			t.Errorf("pacto.yaml changed:\n%s", raw)
		}
	})
}

func TestEditAddEnvironmentValidatesNewFile(t *testing.T) {
	pacto := orders25 + "configurations:\n  - {name: app, schema: config.json, required: false}\n"
	files := map[string]string{"config.json": `{"type":"object","properties":{"port":{"type":"integer"}}}`}
	edit := func(port any) error {
		dir := refRoot(t, pacto, files)
		_, err := Edit(context.Background(), nil, EditInput{Path: dir,
			AddEnvironments:      []EnvironmentInput{{Name: "prod"}},
			SetEnvironmentValues: []EnvironmentValuesInput{{Environment: "prod", Configuration: "app", Values: map[string]any{"port": port}}}})
		return err
	}
	if err := edit(8080); err != nil {
		t.Errorf("valid values: %v", err)
	}
	if err := edit("eighty"); err == nil || !strings.Contains(err.Error(), "ENVIRONMENT_VALUES_INVALID") {
		t.Errorf("invalid values in a file not on disk yet: %v", err)
	}
}

func TestEditAtomicOnFailure(t *testing.T) {
	input := func(dir string) EditInput {
		return EditInput{Path: dir, Version: strPtr("1.0.1"),
			SetEnvironmentValues: []EnvironmentValuesInput{{Environment: "prod", Configuration: "db", Values: map[string]any{"size": "large"}}}}
	}
	assertUnchanged := func(t *testing.T, dir string) {
		t.Helper()
		if raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml")); string(raw) != ordersEnv {
			t.Errorf("pacto.yaml changed:\n%s", raw)
		}
		if raw, _ := os.ReadFile(filepath.Join(dir, "environments/prod.yaml")); string(raw) != "db: {size: small}\n" {
			t.Errorf("environment file changed: %q", raw)
		}
		assertNoStagingLeft(t, dir)
	}

	t.Run("the last file fails to write", func(t *testing.T) {
		dir := refRoot(t, ordersEnv, map[string]string{"environments/prod.yaml": "db: {size: small}\n"})
		o := osFileOps()
		o.writeFile = func(path string, data []byte, perm os.FileMode) error {
			if strings.Contains(filepath.Base(path), "pacto.yaml") && strings.HasPrefix(path, dir) {
				return errors.New("disk full")
			}
			return os.WriteFile(path, data, perm)
		}
		if _, err := o.edit(context.Background(), realValidator, input(dir)); err == nil || !strings.Contains(err.Error(), "failed to write") {
			t.Fatalf("err %v", err)
		}
		assertUnchanged(t, dir)
	})
	t.Run("a directory cannot be created", func(t *testing.T) {
		dir := refRoot(t, ordersEnv, map[string]string{"environments/prod.yaml": "db: {size: small}\n"})
		o := osFileOps()
		o.mkdirAll = func(path string, perm os.FileMode) error {
			if strings.HasPrefix(path, dir) {
				return errors.New("denied")
			}
			return os.MkdirAll(path, perm)
		}
		if _, err := o.edit(context.Background(), realValidator, input(dir)); err == nil || !strings.Contains(err.Error(), "failed to create") {
			t.Fatalf("err %v", err)
		}
		assertUnchanged(t, dir)
	})
	t.Run("the first rename fails", func(t *testing.T) {
		dir := refRoot(t, ordersEnv, map[string]string{"environments/prod.yaml": "db: {size: small}\n"})
		o := osFileOps()
		o.rename = func(string, string) error { return errors.New("busy") }
		if _, err := o.edit(context.Background(), realValidator, input(dir)); err == nil || !strings.Contains(err.Error(), "failed to write") {
			t.Fatalf("err %v", err)
		}
		assertUnchanged(t, dir)
	})
}

func TestEdit_StagingFailures(t *testing.T) {
	values := func(dir string) EditInput {
		return EditInput{Path: dir, AddConfigurationRefs: []ConfigurationRefInput{{Name: "db", Ref: "../pg", Values: map[string]any{"size": "small"}}}}
	}
	tests := map[string]func(o *fileOps){
		"temp directory": func(o *fileOps) {
			o.mkdirTemp = func(string, string) (string, error) { return "", errors.New("read-only") }
		},
		"directory": func(o *fileOps) {
			o.mkdirAll = func(string, os.FileMode) error { return errors.New("denied") }
		},
		"file": func(o *fileOps) {
			o.writeFile = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
		},
	}
	for name, break_ := range tests {
		t.Run(name, func(t *testing.T) {
			dir := refRoot(t, orders25, map[string]string{"notes/readme.txt": "hi"})
			o := osFileOps()
			break_(&o)
			if _, err := o.edit(context.Background(), realValidator, values(dir)); err == nil || !strings.Contains(err.Error(), "failed to stage the edited bundle") {
				t.Fatalf("err %v", err)
			}
			if raw, _ := os.ReadFile(filepath.Join(dir, "pacto.yaml")); string(raw) != orders25 {
				t.Errorf("pacto.yaml changed:\n%s", raw)
			}
		})
	}
}

func TestRefValuesSuggestions(t *testing.T) {
	suggest := func(pacto string, files map[string]string) []Suggestion {
		dir := refRoot(t, pacto, files)
		check, err := Check(context.Background(), realValidator, dir)
		if err != nil || !check.Valid {
			t.Fatalf("check %+v %v", check, err)
		}
		var out []Suggestion
		for _, s := range check.Suggestions {
			if s.ToolCall != nil && s.ToolCall.Params["set_environment_values"] != nil {
				out = append(out, s)
			}
		}
		return out
	}
	noValues := "pactoVersion: \"2.6\"\nservice: {name: orders, version: 1.0.0}\nconfigurations:\n  - {name: db, ref: ../pg, required: true}\n"

	s := suggest(noValues, nil)
	if len(s) != 1 || s[0].ToolCall.Params["add_environments"] == nil || !strings.Contains(s[0].Message, `"db" takes its schema from ../pg`) {
		t.Errorf("no environment: %+v", s)
	}
	withEnv := noValues + "environments:\n  - {name: prod, file: environments/prod.yaml}\n"
	if s := suggest(withEnv, map[string]string{"environments/prod.yaml": "db: {size: small}\n"}); len(s) != 0 {
		t.Errorf("environment gives values: %+v", s)
	}
	if s := suggest(orders26, nil); len(s) != 0 {
		t.Errorf("inline values: %+v", s)
	}
}

// A provider whose schema needs nothing leaves an environment with no values
// valid; the suggestion then names that environment.
func TestRefValuesSuggestions_NamesTheEnvironment(t *testing.T) {
	c, err := contract.Parse(strings.NewReader("pactoVersion: \"2.6\"\nservice: {name: orders, version: 1.0.0}\n" +
		"configurations:\n  - {name: db, ref: ../pg, required: true}\nenvironments:\n  - {name: prod, file: environments/prod.yaml}\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := refValuesSuggestions(c, fstest.MapFS{"environments/prod.yaml": {Data: []byte("{}\n")}})
	if len(s) != 1 || s[0].ToolCall.Params["add_environments"] != nil ||
		s[0].ToolCall.Params["set_environment_values"].([]map[string]any)[0]["environment"] != "prod" {
		t.Errorf("with an environment: %+v", s)
	}
}

func TestEditTool_ReferenceOps(t *testing.T) {
	dir := refRoot(t, orders25, nil)
	res := callToolOn(t, NewServer(realValidator, "test"), "pacto_edit", map[string]any{
		"path":                   dir,
		"add_configuration_refs": `[{"name":"db","ref":"../pg","required":true,"configuration":"provisioning","values":{"size":"small"}}]`,
		"add_environments":       `[{"name":"prod"}]`,
		"set_environment_values": `[{"environment":"prod","configuration":"db","values":{"size":"large"}}]`,
		"lifecycle":              `{"deprecated":"2026-01-01"}`,
	})
	if res.IsError {
		t.Fatalf("edit: %s", resultText(t, res))
	}
	if v := readEnv(t, dir, "environments/prod.yaml"); !reflect.DeepEqual(v, map[string]any{"db": map[string]any{"size": "large"}}) {
		t.Errorf("values %v", v)
	}
	res = callToolOn(t, NewServer(realValidator, "test"), "pacto_edit", map[string]any{
		"path": dir, "remove_configuration_refs": `["db"]`, "lifecycle": `{"clear":true}`,
	})
	if res.IsError {
		t.Fatalf("remove: %s", resultText(t, res))
	}
	if c := readContract(t, dir); len(c.Configurations) != 0 || c.Service.Lifecycle != nil {
		t.Errorf("contract %+v", c)
	}
}
