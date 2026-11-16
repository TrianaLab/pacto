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
	dir := writeTree(t, "s", map[string]string{"s/pacto.yaml": "pactoVersion: '" + prevVersion + "'\nservice: {name: s, version: 1.0.0}\n"})
	r := validateAt(t, NewService(nil, nil), ValidateOptions{Path: dir})
	want := `pactoVersion "` + prevVersion + `" is behind "` + contract.Current() + `"; setting pactoVersion: "` + contract.Current() + `" keeps what this contract means`
	if got := behind(t, r); !r.Valid || got != want {
		t.Fatalf("valid=%v message %q", r.Valid, got)
	}
}

func TestValidateCurrentHasNoBehind(t *testing.T) {
	for _, doc := range []string{"pactoVersion: '" + contract.Current() + "'\n", "service: {name: s}\n"} {
		dir := writeTree(t, "s", map[string]string{"s/pacto.yaml": doc})
		if r := validateAt(t, NewService(nil, nil), ValidateOptions{Path: dir}); len(withoutBehind(r.Warnings)) != len(r.Warnings) {
			t.Fatalf("%q: %+v", doc, r.Warnings)
		}
	}
}

func TestValidateBehindKeptNamesFinding(t *testing.T) {
	r := validateAt(t, NewService(&mockBundleStore{}, nil), ValidateOptions{Path: refPair(t, strictSchema, nil)})
	got := behind(t, r)
	if !r.Valid || !strings.Contains(got, `would add ENVIRONMENT_VALUES_MISSING at environments`) || strings.Contains(got, "keeps what") {
		t.Fatalf("valid=%v message %q", r.Valid, got)
	}
}

func TestValidateBehindRefOverridesNamed(t *testing.T) {
	r := validateAt(t, NewService(&mockBundleStore{}, nil), ValidateOptions{Path: refPair(t, openSchema, nil)})
	got := behind(t, r)
	if !strings.Contains(got, "adds no validation finding; the operator then also accepts ContractOverrides values for its ref configurations (db)") || strings.Contains(got, "keeps what") {
		t.Fatalf("message %q", got)
	}
}

// An override that points prod at a file giving db its values would hide
// ENVIRONMENT_VALUES_MISSING; the judged bytes are the file's, so it does not.
func TestValidateBehindIgnoresOverrides(t *testing.T) {
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
	dir := writeTree(t, "s", map[string]string{"s/pacto.yaml": "pactoVersion: '" + prevVersion + "'\nservice: {name: s, version: 1.0.0}\nconfigurations:\n  - {name: app, schema: missing.json, required: false}\n"})
	r := validateAt(t, NewService(nil, nil), ValidateOptions{Path: dir})
	got := behind(t, r)
	if r.Valid || !strings.HasSuffix(got, validation.ErrUnverified.Error()) || strings.Contains(got, "keeps what") {
		t.Fatalf("valid=%v message %q", r.Valid, got)
	}
}

// A bundle that no longer reads when it is loaded again without overrides
// leaves Pacto unable to tell, and it says so.
func TestValidateBehindCheckErrorSaysSo(t *testing.T) {
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
	got := behind(t, r)
	if !strings.Contains(got, "file does not exist") || strings.Contains(got, "keeps what") {
		t.Fatalf("message %q", got)
	}
}
