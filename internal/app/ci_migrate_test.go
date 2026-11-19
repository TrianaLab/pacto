package app

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/validation"
)

// behindDoc is a commented CRLF pacto.yaml for name declaring prevVersion.
func behindDoc(name string) string {
	return "# " + name + " contract\r\npactoVersion: \"" + prevVersion + "\" # as written\r\nservice:\r\n  name: " + name + "\r\n  version: 1.0.0\r\n"
}

// asBehind is the 2.5 document y declaring prevVersion instead.
func asBehind(y string) string {
	return strings.Replace(y, strconv.Quote(contract.Current()), strconv.Quote(prevVersion), 1)
}

// migrateDir reads the folder rel below root the way pacto ci does and
// migrates it, returning the bundle as read and the migrated one.
func migrateDir(t *testing.T, s *Service, root, rel string) (*contract.Bundle, *contract.Bundle, CIMigration) {
	t.Helper()
	dir := filepath.Join(root, rel)
	b, err := s.ciBundle(dir, root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	nb, m := s.migrateForCI(t.Context(), b, dir, root, &bundleResolverAdapter{svc: s, base: rootBase(dir)}, s.localInputs(dir, ""), nil)
	return b, nb, m
}

func TestMigrateForCIRaises(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"orders/pacto.yaml": behindDoc("orders")})
	s := &Service{Version: "9.9.9"}
	_, nb, m := migrateDir(t, s, root, "orders")
	if m.State != migrationMigrated || m.From != prevVersion || m.To != contract.Current() || nb.Contract.PactoVersion != contract.Current() {
		t.Fatalf("migration %+v contract %s", m, nb.Contract.PactoVersion)
	}
	raw, _ := fs.ReadFile(nb.FS, DefaultContractPath)
	if c, err := contract.Parse(bytes.NewReader(raw)); err != nil || c.PactoVersion != contract.Current() {
		t.Fatalf("pacto.yaml in the FS: %v %q", err, raw)
	}
	rec, err := contract.ReadInferredRecord(nb.FS)
	if err != nil || rec == nil || len(rec.Migrations) != 1 {
		t.Fatalf("record %+v %v", rec, err)
	}
	want := contract.InferredMigration{From: prevVersion, To: contract.Current(), Source: DefaultContractPath, By: "pacto ci 9.9.9"}
	if rec.Migrations[0] != want || contract.VerifyInferredRecord(nb.FS, rec) != nil {
		t.Fatalf("migration entry %+v", rec.Migrations[0])
	}
}

func TestCIMigratedBytesDifferOnlyInVersion(t *testing.T) {
	root := t.TempDir()
	doc := behindDoc("orders")
	writeFiles(t, root, map[string]string{"orders/pacto.yaml": doc})
	_, nb, _ := migrateDir(t, &Service{}, root, "orders")
	got, _ := fs.ReadFile(nb.FS, DefaultContractPath)
	want := strings.Replace(doc, strconv.Quote(prevVersion), strconv.Quote(contract.Current()), 1)
	if string(got) != want || !bytes.Equal(nb.RawYAML, got) || !strings.Contains(string(got), "# as written\r\n") {
		t.Fatalf("migrated pacto.yaml:\n%q\nwant\n%q", got, want)
	}
}

// TestMigrateForCICompletesAfterRaise: completion runs on the raised
// contract, so the record holds the inferred fields beside the migration,
// names the catalog a current folder records, and quotes no value the
// folder sets.
func TestMigrateForCICompletesAfterRaise(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"orders", "current"} {
		if err := os.CopyFS(filepath.Join(root, rel), os.DirFS("../discover/testdata/complete")); err != nil {
			t.Fatal(err)
		}
	}
	writeFiles(t, root, map[string]string{
		"orders/pacto.yaml":  behindDoc("orders"),
		"current/pacto.yaml": "pactoVersion: " + strconv.Quote(contract.Current()) + "\nservice: {name: current, version: 1.0.0}\n",
	})
	s := &Service{}
	_, nb, m := migrateDir(t, s, root, "orders")
	cur, err := s.ciBundle(filepath.Join(root, "current"), root, "", nil)
	if err != nil || m.State != migrationMigrated {
		t.Fatalf("%v %+v", err, m)
	}
	rec, _ := contract.ReadInferredRecord(nb.FS)
	want, _ := contract.ReadInferredRecord(cur.FS)
	if rec == nil || want == nil || len(rec.Fields) == 0 || len(rec.Fields) != len(want.Fields) || strings.Join(rec.Inputs.Catalog, ",") != strings.Join(want.Inputs.Catalog, ",") {
		t.Fatalf("record %+v, a current folder's %+v", rec, want)
	}
	for _, leak := range []string{"hunter2", "u4l-s3cret", "v4lues-s3cret", "c0nfigmap-s3cret"} {
		if bytes.Contains(rec.Marshal(), []byte(leak)) || bytes.Contains(nb.RawYAML, []byte(leak)) {
			t.Errorf("the migrated record or pacto.yaml holds %q", leak)
		}
	}
}

// TestMigrateForCIKeepsInferredName: service.name may be left undeclared
// only from pactoVersion 2.5, so a behind contract without one does not
// validate as written and is kept, its inferred name and the publish
// refusal naming it intact.
func TestMigrateForCIKeepsInferredName(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"orders/pacto.yaml": "pactoVersion: " + strconv.Quote(prevVersion) + "\nservice:\n  version: 1.0.0\n"})
	b, nb, m := migrateDir(t, &Service{}, root, "orders")
	inf, ok := nb.NameInferred()
	if nb != b || m.State != migrationKept || !ok || nb.Contract.Service.Name != "orders" || inf.Value != "orders" {
		t.Fatalf("migration %+v name %q inferred %v", m, nb.Contract.Service.Name, ok)
	}
	if _, err := publishable(nb, true); err == nil || !strings.Contains(err.Error(), "service.name is inferred from") {
		t.Fatalf("publishable: %v", err)
	}
}

// TestMigrateForCIInvalidCompletedRecordKeeps: a raised bundle whose
// completed record does not validate keeps the contract as written. Here
// the bundle holds another k8s/deploy.yaml than the folder completion
// reads, so the record cites a digest the bundle does not hold.
func TestMigrateForCIInvalidCompletedRecordKeeps(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"orders/pacto.yaml":      behindDoc("orders"),
		"orders/k8s/deploy.yaml": deployment("ghcr.io/acme/orders:1.0"),
	})
	dir := filepath.Join(root, "orders")
	s := &Service{}
	b, err := s.ciBundle(dir, root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.FS, err = copyFSWithReplace(b.FS, "k8s/deploy.yaml", []byte("# edited\n")); err != nil {
		t.Fatal(err)
	}
	nb, m := s.migrateForCI(t.Context(), b, dir, root, nil, s.localInputs(dir, ""), nil)
	if nb != b || m.Reason != "INFERRED_RECORD_INVALID at pacto.inferred.yaml after completion" || m.State != migrationKept || m.To != prevVersion {
		t.Fatalf("migration %+v", m)
	}
}

func TestCIKeptPublishesAsWritten(t *testing.T) {
	dir := refPair(t, strictSchema, nil)
	b, nb, m := migrateDir(t, &Service{}, filepath.Dir(dir), "orders")
	if nb != b || m.State != migrationKept || m.From != "2.3" || m.To != "2.3" || m.Reason != "ENVIRONMENT_VALUES_MISSING at environments[0].file from "+contract.Current() {
		t.Fatalf("migration %+v", m)
	}
	if rec, _ := contract.ReadInferredRecord(nb.FS); rec != nil {
		t.Fatalf("a kept contract carries a record: %+v", rec)
	}
}

func TestCIUpgradeErrorKeeps(t *testing.T) {
	root := t.TempDir()
	escaped := `"` + strings.Replace(prevVersion, ".", `\x2E`, 1) + `"`
	writeFiles(t, root, map[string]string{"orders/pacto.yaml": "pactoVersion: " + escaped + "\nservice: {name: orders, version: 1.0.0}\n"})
	b, nb, m := migrateDir(t, &Service{}, root, "orders")
	if nb != b || m.State != migrationKept || m.From != prevVersion || !strings.Contains(m.Reason, "not written as a simple value") {
		t.Fatalf("migration %+v", m)
	}
}

// TestCIBlockScalarVersion: Upgrade reads no version from a block-scalar
// pactoVersion Parse accepts; the parsed one names the migration, and a
// current one is never reported kept.
func TestCIBlockScalarVersion(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"current/pacto.yaml": "pactoVersion: |-\n  " + contract.Current() + "\nservice: {name: current, version: 1.0.0}\n",
		"behind/pacto.yaml":  "pactoVersion: |-\n  " + prevVersion + "\nservice: {name: behind, version: 1.0.0}\n",
	})
	if b, nb, m := migrateDir(t, &Service{}, root, "current"); nb != b || m != (CIMigration{From: contract.Current(), To: contract.Current(), State: migrationCurrent}) {
		t.Fatalf("current: %+v", m)
	}
	if b, nb, m := migrateDir(t, &Service{}, root, "behind"); nb != b || m.State != migrationKept || m.From != prevVersion || m.To != prevVersion || m.Reason == "" {
		t.Fatalf("behind: %+v", m)
	}
}

func TestMigrateForCICurrentUntouched(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"current/pacto.yaml": "pactoVersion: " + strconv.Quote(contract.Current()) + "\nservice: {name: current, version: 1.0.0}\n",
		"none/pacto.yaml":    "service: {name: none, version: 1.0.0}\n",
	})
	for _, rel := range []string{"current", "none"} {
		if b, nb, m := migrateDir(t, &Service{}, root, rel); nb != b || m.State != migrationCurrent || m.From != m.To {
			t.Fatalf("%s: migration %+v", rel, m)
		}
	}
}

// TestMigrateForCICopyFailureKeeps: a folder the raise cannot copy keeps
// its contract as written.
func TestMigrateForCICopyFailureKeeps(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"orders/pacto.yaml": behindDoc("orders")})
	dir := filepath.Join(root, "orders")
	s := &Service{}
	b, err := s.ciBundle(dir, root, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	b.FS = unreadableFS{b.FS}
	nb, m := s.migrateForCI(t.Context(), b, dir, root, nil, s.localInputs(dir, ""), nil)
	if nb != b || m.State != migrationKept || !strings.HasSuffix(m.Reason, "after the raise") {
		t.Fatalf("migration %+v", m)
	}
}

// unreadableFS lists a file it cannot read.
type unreadableFS struct{ fs.FS }

func (b unreadableFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(b.FS, name)
	if name == "." {
		entries = append(entries, fs.FileInfoToDirEntry(unreadableInfo{}))
	}
	return entries, err
}

type unreadableInfo struct{}

func (unreadableInfo) Name() string       { return "broken.txt" }
func (unreadableInfo) Size() int64        { return 0 }
func (unreadableInfo) Mode() fs.FileMode  { return 0o644 }
func (unreadableInfo) ModTime() time.Time { return time.Time{} }
func (unreadableInfo) IsDir() bool        { return false }
func (unreadableInfo) Sys() any           { return nil }

func TestAddedFinding(t *testing.T) {
	before := validation.ValidationResult{Warnings: []contract.ValidationWarning{{Code: "W", Path: "a"}}}
	after := validation.ValidationResult{Warnings: []contract.ValidationWarning{{Code: "W", Path: "a"}, {Code: "W", Path: "a"}}}
	if got := addedFinding(before, before); got != "" {
		t.Fatalf("same run: %q", got)
	}
	if got := addedFinding(before, after); got != "W at a" {
		t.Fatalf("a second W: %q", got)
	}
	withError := validation.ValidationResult{Errors: []contract.ValidationError{{Code: "E"}}}
	if got := addedFinding(withError, withError); got != "" {
		t.Fatalf("the same error: %q", got)
	}
	if got := addedFinding(before, withError); got != "E" {
		t.Fatalf("an error with no path: %q", got)
	}
}
