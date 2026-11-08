package architecture

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// `--template` has a default value for its bare form (NoOptDefVal), so pflag never
// reads the next word as its value: `--template exclude` keeps only golden paths and
// passes `exclude` on as an argument. Only `--template=exclude` leaves them out.
var spacedTemplateFlagRe = regexp.MustCompile(`--template\s+(only|exclude)\b`)

func TestDocsNeverSpaceTheTemplateFlagValue(t *testing.T) {
	root := filepath.Join(repoRoot(t), "docs")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "superpowers" { // gitignored working notes
			return filepath.SkipDir
		}
		if d.IsDir() || filepath.Ext(p) != ".md" {
			return nil
		}
		raw, err := os.ReadFile(p) //nolint:gosec // a path under the repository root
		if err != nil {
			return err
		}
		for _, m := range spacedTemplateFlagRe.FindAllString(string(raw), -1) {
			rel, _ := filepath.Rel(repoRoot(t), p)
			t.Errorf("%s writes %q: the bare --template means only, so write --template=VALUE", rel, m)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The fleet reports a golden path's signature as verified when the bundle verifies
// against the signer its own contract declares (pkg/fleet SignatureVerified), and
// pathSignatureStep keeps the created service's signature step unknown for that
// reason. The docs once said the fleet's line "stays unknown even when the signature
// verifies", which is false.
var staysUnknownRe = regexp.MustCompile("(?is)signature[^.]*stays\\s+`unknown`[^.]*verif")

func TestGoldenPathsDocsSayTheFleetShowsAVerifiedSignature(t *testing.T) {
	doc := readRepoFile(t, "docs", "golden-paths.md")
	if m := staysUnknownRe.FindString(doc); m != "" {
		t.Errorf("docs/golden-paths.md claims the fleet's signature stays unknown when it verifies: %q", m)
	}
	flat := strings.Join(strings.Fields(doc), " ")
	for _, want := range []string{
		"The fleet shows a path's signature as Verified",
		"keeps its signature step `unknown`",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("docs/golden-paths.md no longer says %q", want)
		}
	}
}
