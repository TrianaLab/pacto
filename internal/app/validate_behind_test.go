package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/override"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// withoutBehind is ws without the CONTRACT_BEHIND finding a fixture older than
// Current() carries.
func withoutBehind(ws []contract.ValidationWarning) []contract.ValidationWarning {
	var out []contract.ValidationWarning
	for _, w := range ws {
		if w.Code != "CONTRACT_BEHIND" {
			out = append(out, w)
		}
	}
	return out
}

// writeTree writes files under a new directory and returns the path of dir
// inside it.
func writeTree(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, dir)
}

// prevVersion is the newest version behind Current().
var prevVersion = contract.SupportedVersions[len(contract.SupportedVersions)-2]

// refPair is a 2.3 consumer whose required ref configuration db gets no values
// in prod, and a provider whose configuration schema is schema.
func refPair(t *testing.T, schema string, extra map[string]string) string {
	files := map[string]string{
		"pg/pacto.yaml":        "pactoVersion: \"2.4\"\nservice: {name: postgresql, version: 16.1.0}\nconfigurations:\n  - {name: provisioning, schema: provisioning.json, required: false}\n",
		"pg/provisioning.json": schema,
		"orders/pacto.yaml":    "pactoVersion: \"2.3\"\nservice: {name: orders, version: 1.0.0}\nconfigurations:\n  - {name: db, ref: ../pg, required: true}\nenvironments:\n  - {name: prod, file: prod.yaml}\n",
		"orders/prod.yaml":     "{}\n",
		"orders/full.yaml":     "db: {size: small}\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return writeTree(t, "orders", files)
}

const strictSchema = `{"type":"object","properties":{"size":{"enum":["small","large"]}},"required":["size"]}`
const openSchema = `{"type":"object","properties":{"size":{"type":"string"}}}`

// behind is the one CONTRACT_BEHIND warning of r, which must sit on the
// structural stage.
func behind(t *testing.T, r *ValidateResult) string {
	t.Helper()
	var got []string
	for _, st := range r.Stages {
		for _, w := range st.Warnings {
			if w.Code == "CONTRACT_BEHIND" {
				if st.Name != "structural" || w.Path != "pactoVersion" {
					t.Fatalf("CONTRACT_BEHIND on %s at %q", st.Name, w.Path)
				}
				got = append(got, w.Message)
			}
		}
	}
	if len(got) != 1 {
		t.Fatalf("CONTRACT_BEHIND findings %q in %+v", got, r.Stages)
	}
	return got[0]
}

func validateAt(t *testing.T, svc *Service, opts ValidateOptions) *ValidateResult {
	t.Helper()
	r, err := svc.Validate(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestValidateBehindIsInfoAndValid(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, "s", map[string]string{"s/pacto.yaml": "pactoVersion: '" + prevVersion + "'\nservice: {name: s, version: 1.0.0}\n"})
	r := validateAt(t, NewService(nil, nil), ValidateOptions{Path: dir})
	want := `pactoVersion "` + prevVersion + `" is behind "` + contract.Current() + `"; setting pactoVersion: "` + contract.Current() + `" keeps what this contract means; pacto ci publishes it as ` + contract.Current() + ` when it next publishes this service, if its own check agrees` + migrateHint
	if got := behind(t, r); !r.Valid || got != want {
		t.Fatalf("valid=%v message %q", r.Valid, got)
	}
}

func TestValidateCurrentHasNoBehind(t *testing.T) {
	t.Parallel()
	for _, doc := range []string{"pactoVersion: '" + contract.Current() + "'\n", "service: {name: s}\n"} {
		dir := writeTree(t, "s", map[string]string{"s/pacto.yaml": doc})
		if r := validateAt(t, NewService(nil, nil), ValidateOptions{Path: dir}); len(withoutBehind(r.Warnings)) != len(r.Warnings) {
			t.Fatalf("%q: %+v", doc, r.Warnings)
		}
	}
}

func TestValidateBehindKeptNamesFinding(t *testing.T) {
	t.Parallel()
	r := validateAt(t, NewService(&mockBundleStore{}, nil), ValidateOptions{Path: refPair(t, strictSchema, nil)})
	got := behind(t, r)
	if !r.Valid || !strings.Contains(got, `would add ENVIRONMENT_VALUES_MISSING at environments`) || strings.Contains(got, "keeps what") {
		t.Fatalf("valid=%v message %q", r.Valid, got)
	}
}

func TestValidateBehindRefOverridesNamed(t *testing.T) {
	t.Parallel()
	r := validateAt(t, NewService(&mockBundleStore{}, nil), ValidateOptions{Path: refPair(t, openSchema, nil)})
	got := behind(t, r)
	if !strings.Contains(got, "adds no validation finding; the operator then also accepts ContractOverrides values for its ref configurations (db)") || strings.Contains(got, "keeps what") {
		t.Fatalf("message %q", got)
	}
}

// An override that points prod at a file giving db its values would hide
// ENVIRONMENT_VALUES_MISSING; the judged bytes are the file's, so it does not.
func TestValidateBehindIgnoresOverrides(t *testing.T) {
	t.Parallel()
	opts := ValidateOptions{Path: refPair(t, strictSchema, nil), Overrides: override.Overrides{Values: []map[string]any{{
		"environments": []any{map[string]any{"name": "prod", "file": "full.yaml"}},
	}}}}
	got := behind(t, validateAt(t, NewService(&mockBundleStore{}, nil), opts))
	if !strings.Contains(got, "ENVIRONMENT_VALUES_MISSING") {
		t.Fatalf("message %q", got)
	}
	// pacto.yaml declares no pactoVersion: it is current, whatever an override says.
	dir := writeTree(t, "s", map[string]string{"s/pacto.yaml": "service: {name: s, version: 1.0.0}\n"})
	r := validateAt(t, NewService(nil, nil), ValidateOptions{Path: dir, Overrides: override.Overrides{Values: []map[string]any{{"pactoVersion": prevVersion}}}})
	if !r.Valid || len(withoutBehind(r.Warnings)) != len(r.Warnings) {
		t.Fatalf("got %+v", r.Warnings)
	}
}

func TestValidateBehindInvalidContractNeverSaysKeeps(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, "s", map[string]string{"s/pacto.yaml": "pactoVersion: '" + prevVersion + "'\nservice: {name: s, version: 1.0.0}\nconfigurations:\n  - {name: app, schema: missing.json, required: false}\n"})
	r := validateAt(t, NewService(nil, nil), ValidateOptions{Path: dir})
	got := behind(t, r)
	if r.Valid || !strings.HasSuffix(got, validation.ErrUnverified.Error()) || strings.Contains(got, "keeps what") {
		t.Fatalf("valid=%v message %q", r.Valid, got)
	}
}

// The judged pacto.yaml comes from the one read Validate makes: a registry
// tag that moves, or a pull that fails, after it never reaches the judgement.
func TestValidateBehindJudgesTheOneRead(t *testing.T) {
	t.Parallel()
	pulls := 0
	b := testBundle()
	b.RawYAML = nil
	b.FS.(fstest.MapFS)["pacto.yaml"] = &fstest.MapFile{Data: []byte("pactoVersion: '" + prevVersion + "'\nservice: {name: test-svc, version: 1.0.0}\n")}
	store := &mockBundleStore{PullFn: func(context.Context, string) (*contract.Bundle, error) {
		if pulls++; pulls > 1 {
			return nil, errors.New("registry down")
		}
		return b, nil
	}}
	r := validateAt(t, NewService(store, nil), ValidateOptions{Path: "oci://ghcr.io/acme/svc:1.0.0", Overrides: override.Overrides{SetValues: []string{"service.version=1.0.1"}}})
	if got := behind(t, r); pulls != 1 || !strings.Contains(got, "keeps what") {
		t.Fatalf("%d pulls, message %q", pulls, got)
	}
}

// Behind is decided on the pacto.yaml as written: an override of pactoVersion
// neither invents nor hides CONTRACT_BEHIND, and the message names the
// declared version.
func TestValidateBehindOverriddenVersion(t *testing.T) {
	t.Parallel()
	setVersion := func(v string) override.Overrides {
		return override.Overrides{Values: []map[string]any{{"pactoVersion": v}}}
	}
	current := writeTree(t, "s", map[string]string{"s/pacto.yaml": "pactoVersion: '" + contract.Current() + "'\nservice: {name: s, version: 1.0.0}\n"})
	r := validateAt(t, NewService(nil, nil), ValidateOptions{Path: current, Overrides: setVersion(prevVersion)})
	if len(withoutBehind(r.Warnings)) != len(r.Warnings) {
		t.Fatalf("a current file got %+v", r.Warnings)
	}
	old := writeTree(t, "s", map[string]string{"s/pacto.yaml": "pactoVersion: '" + prevVersion + "'\nservice: {name: s, version: 1.0.0}\n"})
	got := behind(t, validateAt(t, NewService(nil, nil), ValidateOptions{Path: old, Overrides: setVersion(contract.Current())}))
	if !strings.HasPrefix(got, `pactoVersion "`+prevVersion+`" is behind`) {
		t.Fatalf("message %q", got)
	}
}

// A run whose only failure is a stage outside the layers Upgrade re-runs (here
// an unmet guarantee) still cannot tell: it never says the raise keeps.
func TestValidateBehindGuaranteeFailureNeverSaysKeeps(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeClosureFile(t, root, "orders/pacto.yaml", guaranteeProvider)
	writeClosureFile(t, root, "orders/events.yaml", "asyncapi: 3.0.0\ninfo: {title: orders, version: 1.0.0}\n")
	writeClosureFile(t, root, "checkout/pacto.yaml", guaranteeConsumer("  - {name: orders, ref: ../orders, compatibility: ^1.0.0, required: true, requires: [{interface: events, delivery: at-most-once}]}\n"))
	r := validateAt(t, NewService(&mockBundleStore{}, nil), ValidateOptions{Path: filepath.Join(root, "checkout")})
	got := behind(t, r)
	if r.Valid || !strings.HasSuffix(got, validation.ErrUnverified.Error()) {
		t.Fatalf("valid=%v message %q", r.Valid, got)
	}
}

// TestBehindMessageNamesPactoCI pins what CONTRACT_BEHIND says pacto ci does:
// a raise publishes as current if pacto ci's own check agrees, a kept raise
// or an error publishes as written, and a run that already fails is told
// only to fix it.
func TestBehindMessageNamesPactoCI(t *testing.T) {
	t.Parallel()
	cur := contract.Current()
	raises := "; pacto ci publishes it as " + cur + " when it next publishes this service, if its own check agrees"
	ref := &contract.Contract{Configurations: []contract.Configuration{{Name: "db", Ref: "../pg"}}}
	head := `pactoVersion "2.3" is behind "` + cur + `"; `
	unverified := validation.Unverified(contract.ValidationError{Code: "X", Message: "bad"})
	pub := &contract.Contract{Publish: &contract.Publish{}}
	kept := contract.Upgraded{From: "2.3", To: "2.3", Kept: &contract.ValidationError{Code: "ENVIRONMENT_VALUES_MISSING", Path: "environments[0]"}}
	keptText := `setting pactoVersion: "` + cur + `" would add ENVIRONMENT_VALUES_MISSING at environments[0], so review that first`
	noSection := `; pacto ci cannot publish it as written with no publish section, so it fails until pactoVersion is "` + cur + `"`
	for _, tc := range []struct {
		name string
		c    *contract.Contract
		up   contract.Upgraded
		err  error
		want string
	}{
		{"raised", &contract.Contract{}, contract.Upgraded{From: "2.3", To: cur}, nil, head + `setting pactoVersion: "` + cur + `" keeps what this contract means` + raises},
		{"operator", ref, contract.Upgraded{From: "2.3", To: cur}, nil, head + `setting pactoVersion: "` + cur + `" adds no validation finding; the operator then also accepts ContractOverrides values for its ref configurations (db)` + raises},
		{"kept", pub, kept, nil, head + keptText + "; pacto ci publishes it as written"},
		{"error", pub, contract.Upgraded{From: "2.3"}, errors.New("boom"), head + "boom; pacto ci publishes it as written"},
		// With no publish section pacto ci derives none below current: it fails the row.
		{"kept, no section", &contract.Contract{}, kept, nil, head + keptText + noSection},
		{"error, no section", &contract.Contract{}, contract.Upgraded{From: "2.3"}, errors.New("boom"), head + "boom" + noSection},
		{"unverified", &contract.Contract{}, contract.Upgraded{From: "2.3"}, unverified, head + unverified.Error()},
	} {
		if got := behindMessage(tc.c, "2.3", cur, tc.up, tc.err, false); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// TestBehindMessageNamesMigrate pins where CONTRACT_BEHIND names pacto
// migrate: on the two keeping branches of a local file only. A kept raise or
// a failing run would stay as written, and a registry bundle has no file to
// rewrite.
func TestBehindMessageNamesMigrate(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, "s", map[string]string{"s/pacto.yaml": "pactoVersion: '" + prevVersion + "'\nservice: {name: s, version: 1.0.0}\n"})
	if got := behind(t, validateAt(t, NewService(nil, nil), ValidateOptions{Path: dir})); !strings.HasSuffix(got, "; pacto ci publishes it as "+contract.Current()+" when it next publishes this service, if its own check agrees; run `pacto migrate` to rewrite the file") {
		t.Fatalf("local keeping: %q", got)
	}
	if got := behind(t, validateAt(t, NewService(&mockBundleStore{}, nil), ValidateOptions{Path: refPair(t, openSchema, nil)})); !strings.HasSuffix(got, migrateHint) {
		t.Fatalf("local operator: %q", got)
	}
	invalid := writeTree(t, "s", map[string]string{"s/pacto.yaml": "pactoVersion: '" + prevVersion + "'\nservice: {name: s, version: 1.0.0}\nconfigurations:\n  - {name: app, schema: missing.json, required: false}\n"})
	b := testBundle()
	b.RawYAML = nil
	b.FS.(fstest.MapFS)["pacto.yaml"] = &fstest.MapFile{Data: []byte("pactoVersion: '" + prevVersion + "'\nservice: {name: test-svc, version: 1.0.0}\n")}
	oci := NewService(&mockBundleStore{PullFn: func(context.Context, string) (*contract.Bundle, error) { return b, nil }}, nil)
	for name, r := range map[string]*ValidateResult{
		"kept":    validateAt(t, NewService(&mockBundleStore{}, nil), ValidateOptions{Path: refPair(t, strictSchema, nil)}),
		"invalid": validateAt(t, NewService(nil, nil), ValidateOptions{Path: invalid}),
		"oci":     validateAt(t, oci, ValidateOptions{Path: "oci://ghcr.io/acme/svc:1.0.0"}),
	} {
		if got := behind(t, r); strings.Contains(got, "pacto migrate") || (name == "oci" && !strings.Contains(got, "keeps what")) {
			t.Errorf("%s: %q", name, got)
		}
	}
}

// TestBehindDeclaredBuildKeepsMeaning: validate judges a behind file with a
// declared build the way pacto migrate does, so it names migrate.
func TestBehindDeclaredBuildKeepsMeaning(t *testing.T) {
	t.Parallel()
	dir := writeTree(t, "", buildFolder(prevVersion, dockerBuild, map[string]string{"Dockerfile": "FROM scratch\n"}))
	if got := behind(t, validateAt(t, NewService(nil, nil), ValidateOptions{Path: dir})); !strings.HasSuffix(got, migrateHint) {
		t.Fatalf("%q", got)
	}
}
