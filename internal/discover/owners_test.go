package discover

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

const (
	ownersRepo = "testdata/owners"
	ownersSvc  = "testdata/owners/services/payments"
)

// readIn reads the folder svc, a slash path under the repository root repo.
func readIn(t *testing.T, repo, svc string) Reading {
	t.Helper()
	r, err := readDir(filepath.Join(repo, filepath.FromSlash(svc)), repo, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// owner is the service.owner entries the reading holds, "<value> @ <source>".
func owner(r Reading) []string { return entriesOf(r, "service.owner") }

// codeowners is a repository whose .github/CODEOWNERS is text, holding the
// empty folder services/payments.
func codeowners(t *testing.T, text string) string {
	t.Helper()
	return writeFiles(t, map[string]string{".github/CODEOWNERS": text, "services/payments/README": ""})
}

func TestCodeownersDirPatternMatchesBelow(t *testing.T) {
	for _, pattern := range []string{"/services/payments/", "/services/payments", "services/", "payments"} {
		repo := codeowners(t, pattern+" @acme/team-payments\n")
		if got := owner(readIn(t, repo, "services/payments")); !slices.Equal(got, []string{"acme/team-payments @ repository:.github/CODEOWNERS:1"}) {
			t.Errorf("%s: owner = %v", pattern, got)
		}
	}
}

func TestCodeownersStarDoesNotMatchSubfolders(t *testing.T) {
	if got := owner(readIn(t, codeowners(t, "services/* @acme/a\n"), "services/payments")); got != nil {
		t.Errorf("services/* owns a subfolder: %v", got)
	}
	if got := owner(readIn(t, codeowners(t, "* @acme/a\n"), "services/payments")); len(got) != 1 {
		t.Errorf("* owns nothing: %v", got)
	}
}

func TestCodeownersFixedPaths(t *testing.T) {
	// The fixture's folder holds a CODEOWNERS of its own, which is never read.
	if got := owner(readIn(t, ownersRepo, "services/payments")); !slices.Equal(got, []string{"acme/team-payments @ repository:.github/CODEOWNERS:4", "team-payments @ catalog-info.yaml:7"}) {
		t.Errorf("owner = %v", got)
	}
	repo := writeFiles(t, map[string]string{".gitlab/CODEOWNERS": "* @acme/a\n", "services/CODEOWNERS": "* @acme/a\n", "services/payments/README": ""})
	if got := owner(readIn(t, repo, "services/payments")); got != nil {
		t.Errorf("a CODEOWNERS outside GitHub's paths was read: %v", got)
	}
	// GitHub's order: .github, then the root, then docs.
	repo = writeFiles(t, map[string]string{"CODEOWNERS": "* @acme/root\n", "docs/CODEOWNERS": "* @acme/docs\n", "services/payments/README": ""})
	if got := owner(readIn(t, repo, "services/payments")); !slices.Equal(got, []string{"acme/root @ repository:CODEOWNERS:1"}) {
		t.Errorf("owner = %v", got)
	}
	// A folder that is its own repository cites the file by its folder path.
	dir := writeFiles(t, map[string]string{"docs/CODEOWNERS": "\n* @acme/docs\n"})
	if got := owner(readFixture(t, dir)); !slices.Equal(got, []string{"acme/docs @ docs/CODEOWNERS:2"}) {
		t.Errorf("owner = %v", got)
	}
}

func TestCodeownersLastRuleWins(t *testing.T) {
	repo := codeowners(t, "/services/ @acme/a\n# override\n/services/payments/ @acme/b # the payments team\n/services/ledger/ @acme/c\n")
	if got := owner(readIn(t, repo, "services/payments")); !slices.Equal(got, []string{"acme/b @ repository:.github/CODEOWNERS:3"}) {
		t.Errorf("owner = %v", got)
	}
}

func TestCodeownersLastRuleWithoutOwnersYieldsNone(t *testing.T) {
	r := readIn(t, codeowners(t, "/services/ @acme/a\n/services/payments/\n"), "services/payments")
	if got := owner(r); got != nil || len(r.Findings) != 0 {
		t.Errorf("owner = %v, findings = %+v", got, r.Findings)
	}
}

func TestCodeownersEmailOnlyUnresolved(t *testing.T) {
	r := readIn(t, codeowners(t, "* @acme/a\n/services/payments/ pay@acme.example\n"), "services/payments")
	want := []contract.InferredFinding{{Code: CodeUnresolved, Source: "repository:.github/CODEOWNERS:2",
		Message: "repository:.github/CODEOWNERS:2 names owners by email only, which Pacto cannot read as a team or a person, so no owner is read"}}
	if got := owner(r); got != nil || !slices.Equal(r.Findings, want) {
		t.Errorf("owner = %v, findings = %+v", got, r.Findings)
	}
}

func TestCodeownersTeamThenUser(t *testing.T) {
	r := readIn(t, codeowners(t, "* @alice @acme/a @acme/b\n"), "services/payments")
	f := field(t, r.Fields, "service.owner")
	if f.Value != "acme/a" || f.Declare != "service:\n  owner:\n    team: acme/a\n" || f.Class != contract.InferredClassOwnerFile || f.Confidence != contract.ConfidenceLikely {
		t.Errorf("owner = %+v", f)
	}
	r = readIn(t, codeowners(t, "* @alice @bob\n"), "services/payments")
	if f := field(t, r.Fields, "service.owner"); f.Value != "alice (dri)" || f.Declare != "service:\n  owner:\n    dri: alice\n" {
		t.Errorf("owner = %+v", f)
	}
}

func TestCodeownersUnreadable(t *testing.T) {
	repo := writeFiles(t, map[string]string{".github/CODEOWNERS/x": "", "CODEOWNERS": "* @acme/a\n", "services/payments/README": ""})
	r := readIn(t, repo, "services/payments")
	if got := owner(r); got != nil || len(r.Findings) != 1 || r.Findings[0].Message != "repository:.github/CODEOWNERS is not a regular file, so it is not read" {
		t.Errorf("owner = %v, findings = %+v", got, r.Findings)
	}
	repo = codeowners(t, "* @acme/a\n"+strings.Repeat("#", maxFileSize))
	if r := readIn(t, repo, "services/payments"); owner(r) != nil || len(r.Findings) != 1 || r.Findings[0].Message != "repository:.github/CODEOWNERS is over 1 MiB, so it is not read" {
		t.Errorf("findings = %+v", r.Findings)
	}
	repo = codeowners(t, "* @acme/a\n")
	if err := os.Chmod(filepath.Join(repo, ".github", "CODEOWNERS"), 0); err != nil {
		t.Fatal(err)
	}
	if r := readIn(t, repo, "services/payments"); owner(r) != nil || len(r.Findings) != 1 || r.Findings[0].Message != "repository:.github/CODEOWNERS could not be read" {
		t.Errorf("findings = %+v", r.Findings)
	}
}

func TestCatalogInfoComponentByName(t *testing.T) {
	r := readFixture(t, ownersSvc)
	f := field(t, r.Fields, "service.owner")
	if f.Value != "team-payments" || f.Source != "catalog-info.yaml:7" || f.Class != contract.InferredClassOwnerFile || f.Confidence != contract.ConfidenceLikely {
		t.Errorf("owner = %+v", f)
	}
	// Several components, none named like the service: none is read.
	dir := writeFiles(t, map[string]string{"catalog-info.yaml": "kind: Component\nmetadata: {name: a}\nspec: {owner: group:x}\n---\nkind: Component\nmetadata: {name: b}\nspec: {owner: group:y}\n---\nkind: System\nmetadata: {name: s}\n"})
	r = readFixture(t, dir)
	if got := findingsOf(r, CodeAmbiguous); owner(r) != nil || len(got) != 1 || got[0].Source != "catalog-info.yaml" {
		t.Errorf("owner = %v, findings = %+v", owner(r), r.Findings)
	}
	// The only component, whatever its name; a catalog file below the root is
	// not read.
	dir = writeFiles(t, map[string]string{"catalog-info.yaml": "kind: Component\nmetadata: {name: a}\nspec: {owner: platform}\n", "sub/catalog-info.yaml": "kind: Component\nmetadata: {name: b}\nspec: {owner: user:bob}\n"})
	if got := owner(readFixture(t, dir)); !slices.Equal(got, []string{"platform @ catalog-info.yaml:3"}) {
		t.Errorf("owner = %v", got)
	}
}

func TestCatalogInfoOwnerGroupNamespace(t *testing.T) {
	for ref, want := range map[string]string{
		"group:default/team-payments": "team-payments",
		"group:acme/payments":         "acme/payments",
		"user:default/alice":          "alice (dri)",
		"user:bob":                    "bob (dri)",
		"team-a":                      "team-a",
		"group:bad owner":             "",
		"system:payments":             "",
	} {
		dir := writeFiles(t, map[string]string{"catalog-info.yaml": "kind: Component\nmetadata: {name: x}\nspec:\n  owner: " + ref + "\n"})
		got := owner(readFixture(t, dir))
		if (want == "" && got != nil) || (want != "" && !slices.Equal(got, []string{want + " @ catalog-info.yaml:4"})) {
			t.Errorf("%s: owner = %v, want %s", ref, got, want)
		}
	}
}

func TestCatalogInfoOutranksCodeowners(t *testing.T) {
	rec, err := CompleteIn(ownersSvc, ownersRepo, nil, contract.InferredInputs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := field(t, rec.Fields, "service.owner")
	if f.Value != "team-payments" || f.Source != "catalog-info.yaml:7" || f.Class != contract.InferredClassOwnerFile || f.Confidence != contract.ConfidenceLikely ||
		!slices.Equal(f.Outranks, []string{"deploy/app.yaml:6", "repository:.github/CODEOWNERS:4"}) {
		t.Errorf("owner = %+v", f)
	}
}

func TestOneOwnerFieldRanked(t *testing.T) {
	count := func(rec *contract.InferredRecord) int {
		return len(slices.DeleteFunc(slices.Clone(rec.Fields), func(f contract.Inferred) bool { return f.Field != "service.owner" }))
	}
	rec, err := CompleteIn(ownersSvc, ownersRepo, nil, contract.InferredInputs{}, nil)
	if err != nil || count(rec) != 1 {
		t.Fatalf("record = %+v, %v", rec, err)
	}
	// Without the catalog file, CODEOWNERS writes the field and outranks the
	// team label.
	repo := writeFiles(t, map[string]string{".github/CODEOWNERS": "* @acme/a\n", "svc/deploy.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: svc\n  labels: {team: squad}\n"})
	rec, err = CompleteIn(filepath.Join(repo, "svc"), repo, nil, contract.InferredInputs{}, nil)
	if err != nil || count(rec) != 1 {
		t.Fatalf("record = %+v, %v", rec, err)
	}
	if f := field(t, rec.Fields, "service.owner"); f.Value != "acme/a" || f.Class != contract.InferredClassOwnerFile || !slices.Equal(f.Outranks, []string{"deploy.yaml:5"}) {
		t.Errorf("owner = %+v", f)
	}
	// The team label alone carries no owner-file mark.
	rec, err = CompleteIn(filepath.Join(repo, "svc"), filepath.Join(repo, "svc"), nil, contract.InferredInputs{}, nil)
	if f := field(t, rec.Fields, "service.owner"); err != nil || f.Value != "squad" || f.Class != "" || f.Outranks != nil {
		t.Errorf("owner = %+v, %v", f, err)
	}
	// A declared owner keeps every owner file out of the record.
	rec, err = CompleteIn(ownersSvc, ownersRepo, &contract.Contract{Service: contract.Service{Owner: contract.Owner{Team: "x"}, Domain: "d"}}, contract.InferredInputs{}, nil)
	if err != nil || count(rec) != 0 || slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == "service.domain" }) {
		t.Errorf("record = %+v, %v", rec, err)
	}
}

func TestCatalogLifecycleNotRecorded(t *testing.T) {
	r := readFixture(t, ownersSvc)
	if slices.ContainsFunc(r.entries, func(e entry) bool {
		return strings.Contains(e.Field, "lifecycle") || strings.Contains(e.Value, "production")
	}) {
		t.Errorf("lifecycle recorded: %+v", r.entries)
	}
}

func TestCatalogInfoSystemDomainSanitized(t *testing.T) {
	f := field(t, readFixture(t, ownersSvc).Fields, "service.domain")
	if f.Value != "payments-core" || f.Source != "catalog-info.yaml:9" || f.Declare != "service:\n  domain: payments-core\n" || f.Class != "" {
		t.Errorf("domain = %+v", f)
	}
	for system, want := range map[string]string{"system:default/Billing": "billing", "acme/billing": "acme-billing"} {
		dir := writeFiles(t, map[string]string{"catalog-info.yaml": "kind: Component\nmetadata: {name: x}\nspec: {system: \"" + system + "\"}\n"})
		if f := field(t, readFixture(t, dir).Fields, "service.domain"); f.Value != want {
			t.Errorf("%s: domain = %+v", system, f)
		}
	}
	for _, system := range []string{"!!!", strings.Repeat("a", 64)} {
		dir := writeFiles(t, map[string]string{"catalog-info.yaml": "kind: Component\nmetadata: {name: x}\nspec:\n  system: \"" + system + "\"\n"})
		r := readFixture(t, dir)
		want := []contract.InferredFinding{{Code: CodeUnresolved, Source: "catalog-info.yaml:4",
			Message: "catalog-info.yaml:4 names a system that is not a valid domain, so it is not read"}}
		if len(r.Fields) != 0 || !slices.Equal(r.Findings, want) {
			t.Errorf("%s: fields = %+v, findings = %+v", system, r.Fields, r.Findings)
		}
	}
}

func TestCatalogInfoDependsOnComponentOnly(t *testing.T) {
	r := readFixture(t, ownersSvc)
	for name, line := range map[string]string{"ledger": "catalog-info.yaml:11", "fraud": "catalog-info.yaml:13"} {
		f := field(t, r.Fields, "dependencies["+name+"]")
		if f.Value != name || f.Source != line || f.Confidence != contract.ConfidencePossible || !strings.Contains(f.Declare, "compatibility: unknown") || f.Class != "" {
			t.Errorf("%s = %+v", name, f)
		}
	}
	if slices.ContainsFunc(r.entries, func(e entry) bool { return strings.Contains(e.Field, "payments-db") }) {
		t.Errorf("a resource was read: %+v", r.entries)
	}
	// A declared dependency stays out of the record.
	rec, err := CompleteIn(ownersSvc, ownersRepo, &contract.Contract{Dependencies: []contract.Dependency{{Name: "ledger"}}}, contract.InferredInputs{}, nil)
	if err != nil || slices.ContainsFunc(rec.Fields, func(f contract.Inferred) bool { return f.Field == "dependencies[ledger]" }) {
		t.Errorf("record = %+v, %v", rec, err)
	}
}

func TestCatalogInfoStillOutOfManifests(t *testing.T) {
	sc, err := Dir(ownersSvc)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sc.Manifests, []string{"deploy/app.yaml"}) {
		t.Errorf("manifests = %v", sc.Manifests)
	}
}

func TestCodeownersCitedInInputs(t *testing.T) {
	rec, err := CompleteIn(ownersSvc, ownersRepo, nil, contract.InferredInputs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(rec.Inputs.Sources, func(s contract.InferredSourceFile) bool { return s.Path == "repository:.github/CODEOWNERS" })
	data, _ := os.ReadFile(filepath.Join(ownersRepo, ".github", "CODEOWNERS"))
	if i < 0 || rec.Inputs.Sources[i].Digest != sha(data) {
		t.Errorf("sources = %+v", rec.Inputs.Sources)
	}
	if _, err := contract.ParseInferredRecord(rec.Marshal()); err != nil {
		t.Error(err)
	}
}

// TestDeclareSnippetsValidate: every declare the readers write, each under
// its own field, pasted together into an empty 2.5 contract, validates. A
// configuration declare is a schema property, and a publish declare names
// the registry as a placeholder, each checked by its own test.
func TestDeclareSnippetsValidate(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"Dockerfile":          "FROM x\nLABEL org.opencontainers.image.description=\"Takes payments\"\nHEALTHCHECK CMD curl -f http://localhost:8080/healthz\n",
		"api/openapi.v1.yaml": "openapi: 3.0.0\ninfo: {title: t, version: '1'}\npaths: {}\n",
		"catalog-info.yaml":   "kind: Component\nmetadata: {name: x}\nspec:\n  owner: user:alice\n  system: payments\n  dependsOn: [component:ledger]\n",
	})
	rec, err := CompleteIn(dir, dir, nil, contract.InferredInputs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"pactoVersion": "2.5", "service": map[string]any{"name": "x"}}
	snippets := 0
	for _, f := range rec.Fields {
		if f.Declare == "" || strings.HasPrefix(f.Field, "configuration.") || strings.HasPrefix(f.Field, "publish.") {
			continue
		}
		var part map[string]any
		if err := yamlUnmarshal(f.Declare, &part); err != nil {
			t.Fatalf("%s: %v", f.Field, err)
		}
		for k, v := range part {
			if m, ok := v.(map[string]any); ok && k == "service" {
				for sk, sv := range m {
					doc["service"].(map[string]any)[sk] = sv
				}
				continue
			}
			list, _ := doc[k].([]any)
			doc[k] = append(list, v.([]any)...)
		}
		snippets++
	}
	raw := yamlMarshal(t, doc)
	c, err := contract.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if v := validation.Validate(c, raw, os.DirFS(dir)); !v.IsValid() || snippets != 6 {
		t.Errorf("%d snippets, %s: %+v", snippets, raw, v.Errors)
	}
}

func sha(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func yamlUnmarshal(s string, v any) error { return yaml.Unmarshal([]byte(s), v) }

func yamlMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
