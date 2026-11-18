package architecture

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestApprovalDecisionsGoThroughOpenApproval pins the one path from an
// approval/v1 record to a decision. delivery.OpenApproval is the only decoder
// of the statement; a person-signed record becomes a decision only through
// signing.OpenPersonApproval, which runs OpenApproval with VerifyPerson; and
// the recorder (approvalrecorder.go), the promoter (approval.go through
// verifySigned) and the client (internal/app) reach it from the files listed
// here. 511-S3 adds the signer-class check (a non-person approval reads as a
// proposal) in exactly this path, so a new caller must join this list.
func TestApprovalDecisionsGoThroughOpenApproval(t *testing.T) {
	const (
		person   = "pkg/signing/person.go"
		client   = "internal/app/delivery_record_at.go"
		operator = "integrations/kubernetes/internal/delivery/"
	)
	for call, want := range map[string][]string{
		// The client peeks at a record to learn who signed it, then verifies
		// a recorder-signed one with the trust config; the promoter's pin
		// reader (publishers.go) does the same with the pin keys for the
		// approval a pin cites.
		"delivery.OpenApproval(":                  {operator + "publishers.go", client, person},
		"openStatement(bundle, PredicateApproval": {"pkg/delivery/approvalrecord.go"},
		"signing.OpenPersonApproval(":             {operator + "signedapproval.go", client},
		"openSigned(":                             {operator + "approvalrecorder.go", operator + "publishers.go", operator + "signedapproval.go"},
		"r.verifySigned(":                         {operator + "approval.go"},
	} {
		if got := productionFilesCalling(t, call); !slices.Equal(got, want) {
			t.Errorf("%s is called from %v, want exactly %v: an approval record becomes a decision only through signing.OpenPersonApproval", call, got, want)
		}
	}
	src := readRepoFile(t, person)
	if _, body, ok := strings.Cut(src, "func OpenPersonApproval("); !ok || !strings.Contains(body, "VerifyPerson(") {
		t.Errorf("%s: OpenPersonApproval must verify the person with VerifyPerson", person)
	}
}

// productionFilesCalling lists the non-test Go files under the repository
// that contain call, sorted.
func productionFilesCalling(t *testing.T, call string) []string {
	t.Helper()
	root := repoRoot(t)
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".claude", "vendor", "node_modules", ".git", "tests":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // a path under the repository root
		if err != nil {
			return err
		}
		if strings.Contains(string(data), call) {
			rel, _ := filepath.Rel(root, path)
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(found)
	return found
}
